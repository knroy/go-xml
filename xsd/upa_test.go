package xsd

import (
	"strings"
	"testing"
)

// checkSchema loads a schema and runs the component constraints.
func checkSchema(t *testing.T, src string, opts CheckOptions) error {
	t.Helper()
	s := mustParseSchema(t, src)
	return s.CheckConstraints(opts)
}

// TestUPADetectsAmbiguousChoice covers the simplest violation: two branches of
// a choice that accept the same element.
func TestUPADetectsAmbiguousChoice(t *testing.T) {
	err := checkSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:choice>
	      <xs:element name="a" type="xs:string"/>
	      <xs:element name="a" type="xs:string"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`, CheckOptions{})
	if err == nil {
		t.Fatal("two identical branches of a choice violate UPA")
	}
	if !strings.Contains(err.Error(), "cos-nonambig") {
		t.Errorf("error %q does not cite cos-nonambig", err)
	}
}

// TestUPADetectsOptionalPrefix covers the case the spec's own working group
// used as its example:
//
//	<sequence>
//	  <element ref="a" minOccurs="0"/>
//	  <element ref="b" minOccurs="0"/>
//	  <element ref="a" maxOccurs="2"/>
//	</sequence>
//
// After an optional "a", the automaton cannot tell whether a following "a" is
// the third particle or a repetition of the first.
func TestUPADetectsOptionalPrefix(t *testing.T) {
	err := checkSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:sequence>
	      <xs:element name="a" type="xs:string" minOccurs="0"/>
	      <xs:element name="b" type="xs:string" minOccurs="0"/>
	      <xs:element name="a" type="xs:string" maxOccurs="2"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`, CheckOptions{})
	if err == nil {
		t.Fatal("the optional-prefix sequence violates UPA")
	}
}

// TestUPAElementAgainstWildcard covers the case XSD 1.1 later relaxed: in 1.0
// an element competing with a wildcard that admits it is a violation, and 1.1
// instead prefers the element. A 1.0 validator must not silently adopt the 1.1
// rule, because it changes which schemas are accepted.
func TestUPAElementAgainstWildcard(t *testing.T) {
	err := checkSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:choice>
	      <xs:element name="a" type="xs:string"/>
	      <xs:any namespace="##any" processContents="skip"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`, CheckOptions{})
	if err == nil {
		t.Fatal("an element competing with a wildcard violates UPA in XSD 1.0")
	}
}

// TestUPAAcceptsUnambiguous confirms the check does not fire on ordinary
// content models, which matters more than any individual rejection: a UPA check
// that rejects valid schemas is worse than none.
func TestUPAAcceptsUnambiguous(t *testing.T) {
	for _, src := range []string{
		`<xs:complexType name="t"><xs:sequence>
		   <xs:element name="a" type="xs:string"/>
		   <xs:element name="b" type="xs:string"/>
		 </xs:sequence></xs:complexType>`,

		`<xs:complexType name="t"><xs:choice>
		   <xs:element name="a" type="xs:string"/>
		   <xs:element name="b" type="xs:string"/>
		 </xs:choice></xs:complexType>`,

		// A repeated element is not ambiguous with itself: the same
		// particle matching twice is one particle, not two.
		`<xs:complexType name="t"><xs:sequence>
		   <xs:element name="a" type="xs:string" maxOccurs="unbounded"/>
		 </xs:sequence></xs:complexType>`,

		// Optional elements with distinct names are fine.
		`<xs:complexType name="t"><xs:sequence>
		   <xs:element name="a" type="xs:string" minOccurs="0"/>
		   <xs:element name="b" type="xs:string" minOccurs="0"/>
		   <xs:element name="c" type="xs:string"/>
		 </xs:sequence></xs:complexType>`,

		// Two wildcards over disjoint namespaces do not overlap.
		`<xs:complexType name="t"><xs:choice>
		   <xs:any namespace="urn:a"/>
		   <xs:any namespace="urn:b"/>
		 </xs:choice></xs:complexType>`,
	} {
		full := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
			src + `</xs:schema>`
		if err := checkSchema(t, full, CheckOptions{}); err != nil {
			t.Errorf("this model is unambiguous but was rejected:\n%s\n%v", src, err)
		}
	}
}

// TestUPALaxAcceptsSameDeclaration covers the documented divergence.
//
// Saxon and XSV accept a model where two competing particles reference the same
// element declaration, on the grounds that the *declaration* is identifiable
// even though the particle is not. Michael Kay calls it "a known minor
// departure from the spec". The strict reading is the default; LaxUPA selects
// theirs.
func TestUPALaxAcceptsSameDeclaration(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="a" type="xs:string"/>
	  <xs:complexType name="t">
	    <xs:choice>
	      <xs:element ref="a"/>
	      <xs:element ref="a"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`

	if err := checkSchema(t, src, CheckOptions{}); err == nil {
		t.Error("the strict reading should reject two references to one declaration")
	}
	if err := checkSchema(t, src, CheckOptions{LaxUPA: true}); err != nil {
		t.Errorf("LaxUPA should accept it: %v", err)
	}
}

// TestElementDeclarationsConsistent covers the constraint that is separate from
// UPA: the same element name meaning two different types in one content model.
func TestElementDeclarationsConsistent(t *testing.T) {
	err := checkSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:sequence>
	      <xs:element name="a" type="xs:string"/>
	      <xs:element name="b" type="xs:string"/>
	      <xs:element name="a" type="xs:int"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`, CheckOptions{})
	if err == nil {
		t.Fatal("one name with two types in a content model is inconsistent")
	}
	if !strings.Contains(err.Error(), "cos-element-consistent") {
		t.Errorf("error %q does not cite cos-element-consistent", err)
	}
}

// TestCheckConstraintsIsSeparate records that the constraints are opt-in.
//
// Loading a schema does not run them, following the precedent Xerces sets with
// schema-full-checking (default false): they are the expensive half, they say
// nothing about whether an instance is valid, and a caller validating against a
// schema they already trust has no reason to pay for them.
func TestCheckConstraintsIsSeparate(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:choice>
	      <xs:element name="a" type="xs:string"/>
	      <xs:element name="a" type="xs:string"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`

	// The schema loads despite violating UPA.
	s := mustParseSchema(t, src)
	if err := s.CheckConstraints(CheckOptions{}); err == nil {
		t.Error("CheckConstraints should report the violation")
	}
}

// TestRecursiveGroupIsRefused covers a shape that appears in real schemas and
// crashed the compiler.
//
// A model group that reaches itself — <xs:group name="expr"> whose content
// references expr — makes the particle tree a cyclic graph, while Glushkov
// construction assumes a tree, so following the cycle recursed until the stack
// was gone.
//
// Model Group Correct clause 2 (§3.8.6) settles what to do about it: circular
// groups are disallowed outright, so such a document is not a schema and is
// refused when it is read, before any consumer of the component graph can walk
// into the cycle. This test used only to require that CheckConstraints
// terminate, on the view that a recursive group was legal and merely
// uncompilable; the suite disagrees — groupB013, groupB014 and groupB015 are
// all circular and all expected to be invalid.
func TestRecursiveGroupIsRefused(t *testing.T) {
	_, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="expr">
	    <xs:sequence>
	      <xs:element name="lit" type="xs:string" minOccurs="0"/>
	      <xs:group ref="expr" minOccurs="0"/>
	    </xs:sequence>
	  </xs:group>
	  <xs:complexType name="t"><xs:group ref="expr"/></xs:complexType>
	</xs:schema>`)
	if err == nil {
		t.Fatal("a circular model group should be refused at load")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error should name the circularity, got: %v", err)
	}
}

// TestGroupCycleThroughTwoDefinitions covers indirect circularity.
//
// Clause 2 of Model Group Correct bans a self-reference "at any depth", which
// includes a cycle that passes through another definition rather than closing
// on itself directly. Checking only for a group that names itself would miss
// this, and it is the shape groupB015 uses.
func TestGroupCycleThroughTwoDefinitions(t *testing.T) {
	_, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="foo"><xs:sequence><xs:group ref="bar"/></xs:sequence></xs:group>
	  <xs:group name="bar"><xs:sequence><xs:group ref="foo"/></xs:sequence></xs:group>
	</xs:schema>`)
	if err == nil {
		t.Fatal("a cycle through two group definitions should be refused")
	}
}

// TestDisjointGroupReuseIsNotACycle guards the cycle search against reporting
// a group reached twice by different routes.
//
// Marking a group "seen" for the whole search rather than for the current
// descent would call this circular: base is reached once from left and once
// from right, and neither route revisits anything. It is an ordinary
// diamond, and a very common way to reuse a group.
func TestDisjointGroupReuseIsNotACycle(t *testing.T) {
	mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="base"><xs:sequence><xs:element name="a"/></xs:sequence></xs:group>
	  <xs:group name="left"><xs:sequence><xs:group ref="base"/></xs:sequence></xs:group>
	  <xs:group name="right"><xs:sequence><xs:group ref="base"/></xs:sequence></xs:group>
	  <xs:group name="top">
	    <xs:sequence><xs:group ref="left"/><xs:group ref="right"/></xs:sequence>
	  </xs:group>
	</xs:schema>`)
}
