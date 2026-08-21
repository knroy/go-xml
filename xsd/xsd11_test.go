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

// TestNotNamespaceAllowsUnqualified covers the difference between the two
// spellings of a negated wildcard, which is easy to miss and rejects real
// documents when missed.
//
// XSD 1.0's ##other excludes unqualified names unconditionally — clause 2.3 of
// Wildcard allows Namespace Name. XSD 1.1's notNamespace excludes only what it
// lists, so an unqualified name is permitted unless ##local appears.
func TestNotNamespaceAllowsUnqualified(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:sequence/>
	      <xs:anyAttribute notNamespace="urn:no" processContents="skip"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<e plain="x"/>`); err != nil {
		t.Errorf("notNamespace should admit an unqualified attribute: %v", err)
	}
	if err := check11(t, s, `<e xmlns:n="urn:no" n:a="x"/>`); err == nil {
		t.Error("notNamespace should exclude what it lists")
	}

	// With ##local it excludes unqualified names too.
	s2 := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:sequence/>
	      <xs:anyAttribute notNamespace="##local" processContents="skip"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)
	if err := check11(t, s2, `<e plain="x"/>`); err == nil {
		t.Error("notNamespace=\"##local\" should exclude unqualified attributes")
	}
}

// TestAttributeWildcardIntersection covers §3.10.6: when more than one
// attribute group contributes a wildcard, the type's wildcard is their
// intersection — an attribute must satisfy all of them.
//
// Two bugs met here. The type kept only the last wildcard rather than
// intersecting, and the fixup that flattened an attribute group copied its
// uses but dropped its wildcard entirely, so a type whose only wildcard arrived
// through a group had none.
func TestAttributeWildcardIntersection(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e" type="T"/>
	  <xs:complexType name="T">
	    <xs:sequence/>
	    <xs:attributeGroup ref="a"/>
	    <xs:attributeGroup ref="b"/>
	  </xs:complexType>
	  <xs:attributeGroup name="a">
	    <xs:anyAttribute notNamespace="##local" processContents="lax"/>
	  </xs:attributeGroup>
	  <xs:attributeGroup name="b">
	    <xs:anyAttribute notNamespace="urn:eve" processContents="lax"/>
	  </xs:attributeGroup>
	</xs:schema>`)

	// Admitted by both.
	if err := check11(t, s, `<e xmlns:m="urn:adam" m:a="x"/>`); err != nil {
		t.Errorf("an attribute both wildcards admit should be valid: %v", err)
	}
	// Excluded by the first.
	if err := check11(t, s, `<e plain="x"/>`); err == nil {
		t.Error("an unqualified attribute is excluded by ##local")
	}
	// Excluded by the second.
	if err := check11(t, s, `<e xmlns:f="urn:eve" f:a="x"/>`); err == nil {
		t.Error("urn:eve is excluded by the second wildcard")
	}
}

// TestExplicitTimezone covers the XSD 1.1 facet, and xs:dateTimeStamp, which is
// xs:dateTime with the facet fixed at required — the type that says "an
// instant" rather than "a wall-clock reading".
func TestExplicitTimezone(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="stamp" type="xs:dateTimeStamp"/>
	</xs:schema>`)

	if err := check11(t, s, `<stamp>2024-01-01T00:00:00Z</stamp>`); err != nil {
		t.Errorf("a value with a timezone should be valid: %v", err)
	}
	if err := check11(t, s, `<stamp>2024-01-01T00:00:00</stamp>`); err == nil {
		t.Error("xs:dateTimeStamp requires a timezone")
	}
}

// TestIDThroughUnion covers a bug that affected XSD 1.0 as much as 1.1.
//
// An ID declared through a union was never recorded, because idKind walked the
// union's base chain — which is xs:anySimpleType — rather than looking at the
// member that validates the value. Every reference to such an ID was then
// reported as dangling.
func TestIDThroughUnion(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="idOrBool">
	    <xs:union memberTypes="xs:ID xs:boolean"/>
	  </xs:simpleType>
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="v" type="idOrBool" maxOccurs="unbounded"/>
	        <xs:element name="ref" type="xs:IDREF"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	assertValid(t, schema,
		`<root><v>abc</v><v>true</v><ref>abc</ref></root>`)
	// The ID is recorded, so a duplicate is caught.
	assertInvalid(t, schema,
		`<root><v>abc</v><v>abc</v><ref>abc</ref></root>`, "cvc-id.2")
	// And a dangling reference is still caught.
	assertInvalid(t, schema,
		`<root><v>abc</v><ref>nope</ref></root>`, "cvc-id.1")
}
