package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// An assertion must reach every descendant, however deep.
//
// annotateForAssertion labels the copy an assertion evaluates against with the
// types the schema assigns. A descendant it fails to label atomises as
// xs:untypedAtomic, and since XSD 1.1 makes an evaluation error a false
// assertion result rather than a separate outcome, an unlabelled descendant
// does not merely lose precision — it turns a satisfied assertion into a
// violated one, and a valid document into an invalid one.
//
// The walk carried a `depth > 32` bound, on the theory that a recursive type
// would otherwise not terminate. It descends el.ChildElements(), so it is the
// instance that drives it and the instance is finite; the bound never
// prevented a loop and only ever truncated. These documents differ from each
// other in nothing but nesting, and every one of them satisfies its assertion.
func TestAssertionAnnotatesBelowFormerDepthBound(t *testing.T) {
	// xs:date is the operative choice: xs:untypedAtomic will not compare
	// against it (XPTY0004), so an unannotated <d> fails the test where an
	// annotated one passes. A numeric comparison would not show the bug,
	// because untypedAtomic casts to double and the comparison still
	// succeeds.
	const src = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root" type="T"/>
  <xs:complexType name="T">
    <xs:sequence>
      <xs:element name="d" type="xs:date"/>
      <xs:element name="c" type="T" minOccurs="0"/>
    </xs:sequence>
    <xs:assert test="every $x in .//d satisfies $x gt xs:date('1900-01-01')"/>
  </xs:complexType>
</xs:schema>`

	doc := func(depth int) string {
		var b strings.Builder
		b.WriteString("<root><d>2020-01-01</d>")
		for i := 0; i < depth; i++ {
			b.WriteString("<c><d>2020-01-01</d>")
		}
		b.WriteString(strings.Repeat("</c>", depth))
		b.WriteString("</root>")
		return b.String()
	}

	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	// Assertions are XSD 1.1 only. Options{} defaults to 1.0, where they
	// are silently ignored and every document below would pass vacuously.
	s, err := Load(st.Root, "", Options{Version: Version11})
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}

	// 31 and 32 passed before the fix as well; they are the baseline that
	// says the schema and assertion are right, so that a failure at 33 is
	// read as the depth bound and not as a broken case.
	for _, depth := range []int{0, 1, 31, 32, 33, 64, 200} {
		d, err := xdm.ParseString(doc(depth), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("depth %d: parse instance: %v", depth, err)
		}
		if err := s.Validate(d.Root, ValidateOptions{}); err != nil {
			t.Errorf("depth %d: valid document rejected: %v", depth, err)
		}
	}
}

// The same truncation seen through instance-of rather than through a
// comparison: an unannotated descendant is xs:untypedAtomic, so the test goes
// false instead of raising, and the document is rejected just the same.
func TestAssertionInstanceOfBelowFormerDepthBound(t *testing.T) {
	const src = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root" type="T"/>
  <xs:complexType name="T">
    <xs:sequence>
      <xs:element name="n" type="xs:integer"/>
      <xs:element name="c" type="T" minOccurs="0"/>
    </xs:sequence>
    <xs:assert test="every $x in .//n satisfies data($x) instance of xs:integer"/>
  </xs:complexType>
</xs:schema>`

	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{Version: Version11})
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}

	for _, depth := range []int{32, 33, 100} {
		var b strings.Builder
		b.WriteString("<root><n>1</n>")
		for i := 0; i < depth; i++ {
			b.WriteString("<c><n>1</n>")
		}
		b.WriteString(strings.Repeat("</c>", depth))
		b.WriteString("</root>")

		d, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("depth %d: parse instance: %v", depth, err)
		}
		if err := s.Validate(d.Root, ValidateOptions{}); err != nil {
			t.Errorf("depth %d: valid document rejected: %v", depth, err)
		}
	}
}
