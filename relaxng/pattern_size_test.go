package relaxng

import (
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
