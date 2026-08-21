package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// load11 assembles a schema under XSD 1.1.
func load11(t *testing.T, src string) *Schema {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the schema: %v", err)
	}
	s, err := Load(tree.Root, "s.xsd", Options{Version: Version11})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func check11(t *testing.T, s *Schema, doc string) error {
	t.Helper()
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the instance: %v", err)
	}
	return s.Validate(tree.Root, ValidateOptions{})
}

// TestAssertion covers <xs:assert>, the co-constraint XSD 1.0 cannot state at
// all: a rule relating two of an element's own children.
func TestAssertion(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="period">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="start" type="xs:int"/>
	        <xs:element name="end" type="xs:int"/>
	      </xs:sequence>
	      <xs:assert test="number(end) >= number(start)"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<period><start>1</start><end>5</end></period>`); err != nil {
		t.Errorf("end after start should be valid: %v", err)
	}
	err := check11(t, s, `<period><start>5</start><end>1</end></period>`)
	if err == nil {
		t.Fatal("end before start should be rejected")
	}
	if !strings.Contains(err.Error(), "cvc-assertion") {
		t.Errorf("error %q does not cite cvc-assertion", err)
	}
}

// TestAssertionOnAttributes covers an assertion reading the element's own
// attributes, which is the commonest real use.
func TestAssertionOnAttributes(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="range">
	    <xs:complexType>
	      <xs:attribute name="lo" type="xs:int"/>
	      <xs:attribute name="hi" type="xs:int"/>
	      <xs:assert test="number(@hi) > number(@lo)"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<range lo="1" hi="9"/>`); err != nil {
		t.Errorf("should be valid: %v", err)
	}
	if err := check11(t, s, `<range lo="9" hi="1"/>`); err == nil {
		t.Error("hi below lo should be rejected")
	}
}

// TestAssertionIsConfinedToSubtree covers the rule that keeps assertion
// evaluation local.
//
// An assertion may not look outside the element being validated. Without the
// confinement an assertion on a deeply nested element could walk the whole
// document, making validation quadratic — and would make the result depend on
// where the element happens to sit.
func TestAssertionIsConfinedToSubtree(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="marker" type="xs:string"/>
	        <xs:element name="inner">
	          <xs:complexType>
	            <xs:sequence><xs:element name="v" type="xs:string"/></xs:sequence>
	            <!-- The parent's marker is not reachable from here. -->
	            <xs:assert test="count(../marker) = 0"/>
	          </xs:complexType>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<root><marker>m</marker><inner><v>x</v></inner></root>`); err != nil {
		t.Errorf("the assertion should not see outside its element: %v", err)
	}
}

// TestConditionalTypeAssignment covers <xs:alternative>: the element's type is
// chosen by an XPath test on its attributes.
func TestConditionalTypeAssignment(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <!-- The attribute means the type must be complex with simple content:
	       an element with a plain simple type may carry no attributes. -->
	  <xs:complexType name="numeric">
	    <xs:simpleContent>
	      <xs:extension base="xs:int"><xs:attribute name="kind" type="xs:string"/></xs:extension>
	    </xs:simpleContent>
	  </xs:complexType>
	  <xs:complexType name="textual">
	    <xs:simpleContent>
	      <xs:extension base="xs:string"><xs:attribute name="kind" type="xs:string"/></xs:extension>
	    </xs:simpleContent>
	  </xs:complexType>
	  <xs:element name="value" type="textual">
	    <xs:alternative test="@kind = 'number'" type="numeric"/>
	    <xs:alternative type="textual"/>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<value kind="number">42</value>`); err != nil {
		t.Errorf("a numeric value should validate as xs:int: %v", err)
	}
	if err := check11(t, s, `<value kind="text">hello</value>`); err != nil {
		t.Errorf("a textual value should validate as xs:string: %v", err)
	}
	// The alternative selected xs:int, so a non-numeric value must fail.
	if err := check11(t, s, `<value kind="number">hello</value>`); err == nil {
		t.Error("a non-numeric value with kind=number should be rejected")
	}
}

// TestOpenContent covers <xs:openContent>, which lets a schema accept elements
// its content model does not name — the mechanism that makes a schema
// forward-compatible with documents produced against a later version of it.
func TestOpenContent(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:openContent mode="interleave">
	        <xs:any namespace="##any" processContents="skip"/>
	      </xs:openContent>
	      <xs:sequence>
	        <xs:element name="known" type="xs:string"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<root><known>x</known></root>`); err != nil {
		t.Errorf("the named element alone should be valid: %v", err)
	}
	if err := check11(t, s, `<root><known>x</known><future/></root>`); err != nil {
		t.Errorf("open content should admit an unnamed element: %v", err)
	}
	if err := check11(t, s, `<root><future/><known>x</known></root>`); err != nil {
		t.Errorf("interleave mode should admit it before the named one: %v", err)
	}
}

// TestXSD11FeaturesAreOffUnderVersion10 is the guard that matters most.
//
// XSD 1.1 changes which documents are valid, so honouring its features under a
// 1.0 schema would silently accept documents a 1.0 processor rejects. The
// features are always parsed — a schema using them is not made valid by
// pretending they are absent — but only honoured when the caller asks for 1.1.
func TestXSD11FeaturesAreOffUnderVersion10(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="range">
	    <xs:complexType>
	      <xs:attribute name="lo" type="xs:int"/>
	      <xs:attribute name="hi" type="xs:int"/>
	      <xs:assert test="number(@hi) > number(@lo)"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s10, err := Load(tree.Root, "s.xsd", Options{})
	if err != nil {
		t.Fatalf("a schema using 1.1 features should still load under 1.0: %v", err)
	}
	if s10.Version != Version10 {
		t.Fatal("the default version should be 1.0")
	}

	// Under 1.0 the assertion is not evaluated, so the document that
	// violates it is accepted.
	if err := check11(t, s10, `<range lo="9" hi="1"/>`); err != nil {
		t.Errorf("under 1.0 the assertion should be ignored: %v", err)
	}

	// Under 1.1 the same schema rejects it.
	s11 := load11(t, src)
	if err := check11(t, s11, `<range lo="9" hi="1"/>`); err == nil {
		t.Error("under 1.1 the assertion should be enforced")
	}
}

// TestAssertionCompileErrorIsReported records that a malformed test is a schema
// error rather than something discovered per document.
func TestAssertionCompileErrorIsReported(t *testing.T) {
	tree, err := xdm.ParseString(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e">
	    <xs:complexType><xs:assert test="this is not xpath ((("/></xs:complexType>
	  </xs:element>
	</xs:schema>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(tree.Root, "s.xsd", Options{Version: Version11}); err == nil {
		t.Fatal("a malformed assertion test should be a schema error")
	}
}
