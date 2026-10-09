package relaxng

import (
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A oneOrMore nested inside a oneOrMore duplicates its operand on every
// child, so the derivative pattern grows multiplicatively in the number of
// children. Measured before MaxPatternSize existed: a 189-byte schema and a
// 63-byte instance of fourteen children cost 1.35 s and 1.2 GB, growing about
// ninefold for every two children added; sixteen children did not finish.
//
// MaxDepth could not bound it — the document is two levels deep whatever its
// width, so the depth bound is never approached.
//
// The pattern's growth IS its allocation, so the bound is asserted on bytes
// allocated rather than on wall time, which a loaded runner inflates. Measured:
// about 5 MB once the bound fires, at twelve children and at forty alike;
// 142 MB at twelve children with MaxPatternSize disabled. Twelve is checked
// first because it is the largest width the unbounded code still finishes, so
// a lost bound fails here with a figure instead of hanging at forty.
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

	// Forty is well past the point that used to take a gigabyte.
	for _, n := range []int{12, 40} {
		doc, err := xdm.ParseString("<r>"+strings.Repeat("<a/>", n)+"</r>",
			xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var verr error
		if b := allocated(func() { verr = s.Validate(doc.Root) }); b > 32<<20 {
			t.Fatalf("%d children allocated %d bytes; the pattern bound did "+
				"not apply", n, b)
		}
		// The bound is reported as a limit, not as a validity verdict: the
		// document is in fact valid, and answering "invalid" would be a wrong
		// answer rather than a refusal to answer.
		if verr == nil {
			continue // bounded and still answered correctly
		}
		if !strings.Contains(verr.Error(), "exceeds") {
			t.Fatalf("refused for the wrong reason: %v", verr)
		}
	}
}

// allocated reports the bytes f allocates. TotalAlloc is cumulative, so the
// figure depends on the work done and not on when the collector runs or how
// loaded the machine is.
func allocated(f func()) uint64 {
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	f()
	runtime.ReadMemStats(&m1)
	return m1.TotalAlloc - m0.TotalAlloc
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
//
// As above, the bound is asserted on bytes allocated, not on wall time:
// about 2.4 MB for every width here once the bound fires, against 85 MB at
// eleven attributes without it. The check is fatal so that a lost bound stops
// at twelve, which still finishes, rather than hanging at fourteen.
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
		// Default options: the point is that the limit the package already
		// ships is the one that fires.
		var verr error
		if b := allocated(func() { verr = s.Validate(doc.Root) }); b > 16<<20 {
			t.Fatalf("%d attributes allocated %d bytes; the pattern bound did "+
				"not apply", n, b)
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

// walkSize is patternSize computed the way it was before the constructors
// stored it: a walk of the whole pattern.
func walkSize(p pattern) int {
	switch t := p.(type) {
	case *choicePat:
		return 1 + walkSize(t.Left) + walkSize(t.Right)
	case *groupPat:
		return 1 + walkSize(t.Left) + walkSize(t.Right)
	case *interleavePat:
		return 1 + walkSize(t.Left) + walkSize(t.Right)
	case *afterPat:
		return 1 + walkSize(t.Left) + walkSize(t.Right)
	case *oneOrMorePat:
		return 1 + walkSize(t.Pattern)
	case *listPat:
		return 1 + walkSize(t.Pattern)
	case *refPat:
		if t.static != nil {
			return int(t.static.size)
		}
	}
	return 1
}

// The stored size must be the walked size on every derivative the validator
// carries, with and without a builder, or MaxPatternSize would fire on
// different inputs than it did when patternSize walked the pattern.
func TestStoredPatternSizeMatchesWalk(t *testing.T) {
	s := compileBoundarySchema(t, `<element name="r" xmlns="http://relaxng.org/ns/structure/1.0">
  <oneOrMore><interleave><oneOrMore><element name="a"><empty/></element></oneOrMore>
    <optional><element name="b"><list><oneOrMore><data type="token"/></oneOrMore></list></element></optional>
  </interleave></oneOrMore>
</element>`)
	for _, pb := range []*patBuilder{nil, newPatBuilder()} {
		p := pb.closeTag(pb.open(s.start, xdm.QName{Local: "r"}))
		for i := 0; i < 8; i++ {
			for _, name := range []string{"a", "b"} {
				p = pb.end(pb.closeTag(pb.open(p, xdm.QName{Local: name})))
				if got, want := patternSize(p), walkSize(p); got != want {
					t.Fatalf("builder %v, child %d %s: patternSize %d, walk %d", pb != nil, i, name, got, want)
				}
			}
		}
	}
}
