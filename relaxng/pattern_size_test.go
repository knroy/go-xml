package relaxng

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// A oneOrMore nested inside a oneOrMore duplicates its operand on every
// child, so the derivative pattern grows multiplicatively in the number of
// children. Measured before MaxPatternSize existed: a 189-byte schema and a
// 63-byte instance of fourteen children cost 1.35 s and 1.2 GB, growing about
// ninefold for every two children added; sixteen children did not finish.
//
// MaxDepth could not bound it — the document is two levels deep whatever its
// width, so the depth bound is never approached.
func TestNestedOneOrMoreIsBounded(t *testing.T) {
	const sch = `<element name="r" xmlns="http://relaxng.org/ns/structure/1.0">
  <oneOrMore><oneOrMore><oneOrMore>
    <element name="a"><empty/></element>
  </oneOrMore></oneOrMore></oneOrMore>
</element>`
	schTree, err := xdm.ParseString(sch, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(schTree.Root)
	if err != nil {
		t.Fatal(err)
	}

	// Well past the point that used to take a gigabyte.
	doc, err := xdm.ParseString("<r>"+strings.Repeat("<a/>", 40)+"</r>",
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	verr := s.Validate(doc.Root)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("validation took %v; the pattern bound did not apply", elapsed)
	}
	// The bound is reported as a limit, not as a validity verdict: the
	// document is in fact valid, and answering "invalid" would be a wrong
	// answer rather than a refusal to answer.
	if verr == nil {
		return // bounded and still answered correctly
	}
	if !strings.Contains(verr.Error(), "exceeds") {
		t.Fatalf("refused for the wrong reason: %v", verr)
	}
}

// The bound must not refuse the ordinary use of oneOrMore, which is common.
func TestPlainOneOrMoreStillValidates(t *testing.T) {
	const sch = `<element name="r" xmlns="http://relaxng.org/ns/structure/1.0">
  <oneOrMore><element name="a"><empty/></element></oneOrMore>
</element>`
	schTree, _ := xdm.ParseString(sch, xdm.ParseOptions{})
	s, err := Compile(schTree.Root)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := xdm.ParseString("<r>"+strings.Repeat("<a/>", 5000)+"</r>",
		xdm.ParseOptions{})
	if err := s.Validate(doc.Root); err != nil {
		t.Fatalf("a plain oneOrMore of 5000 children was refused: %v", err)
	}
}

// The same multiplicative growth reaches the pattern through attributes, and
// there the bound could not fire: the element path checks the size once,
// before startTagOpenDeriv, and the attribute loop that follows took every
// remaining derivative with no check between iterations.
//
// Measured on the unfixed code with the DEFAULT options, against this
// 189-byte schema: ten attributes 18.8 ms, eleven 101 ms, twelve 584 ms,
// thirteen 3.99 s, and the fourteen-attribute document — 106 bytes — did not
// finish in sixty seconds. Roughly sixfold per attribute added. Setting
// MaxPatternSize to 1, the strictest value the API accepts, changed none of
// those figures: the guard was unreachable, not miscalibrated. With the check
// in the loop every one of them is refused in about 2 ms.
func TestAttributePatternSizeIsBounded(t *testing.T) {
	const sch = `<element name="r" xmlns="http://relaxng.org/ns/structure/1.0">
<oneOrMore><oneOrMore><attribute><anyName/></attribute></oneOrMore></oneOrMore><text/></element>`
	schTree, err := xdm.ParseString(sch, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(schTree.Root)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range []int{10, 12, 14, 20} {
		var sb strings.Builder
		sb.WriteString("<r")
		for i := 0; i < n; i++ {
			sb.WriteString(` a`)
			sb.WriteString(strconv.Itoa(i))
			sb.WriteString(`="v"`)
		}
		sb.WriteString("/>")
		doc, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		// Default options: the point is that the limit the package already
		// ships is the one that fires.
		verr := s.Validate(doc.Root)
		elapsed := time.Since(start)

		if elapsed > 500*time.Millisecond {
			t.Errorf("%d attributes took %v; the pattern bound did not apply",
				n, elapsed)
		}
		// The refusal must name the limit. A validity failure here would be a
		// wrong answer rather than a refusal to answer: the document does in
		// fact match the schema.
		if verr == nil {
			t.Errorf("%d attributes: expected the pattern bound to refuse", n)
			continue
		}
		if !strings.Contains(verr.Error(), "the derivative pattern exceeds") {
			t.Errorf("%d attributes: refused for the wrong reason: %v", n, verr)
		}
	}
}

// The bound must not refuse a legitimately wide document. A schema whose
// attributes do not nest oneOrMore inside oneOrMore does not accumulate, so
// the check added to the attribute loop never trips however many attributes
// the element carries.
func TestWideAttributesStillValidate(t *testing.T) {
	const sch = `<element name="r" xmlns="http://relaxng.org/ns/structure/1.0">
  <zeroOrMore><attribute><anyName/></attribute></zeroOrMore><text/>
</element>`
	schTree, err := xdm.ParseString(sch, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(schTree.Root)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	sb.WriteString("<r")
	for i := 0; i < 2000; i++ {
		sb.WriteString(` a`)
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(`="v"`)
	}
	sb.WriteString("/>")
	doc, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root); err != nil {
		t.Fatalf("a document of 2000 attributes was refused: %v", err)
	}
}
