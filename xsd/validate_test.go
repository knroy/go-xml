package xsd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// validateString is the test helper: load a schema, parse a document, validate.
func validateString(t *testing.T, schema, doc string) error {
	t.Helper()
	s := mustParseSchema(t, schema)
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the instance: %v", err)
	}
	return s.Validate(tree.Root, ValidateOptions{})
}

// assertValid fails the test if the document does not validate.
func assertValid(t *testing.T, schema, doc string) {
	t.Helper()
	if err := validateString(t, schema, doc); err != nil {
		t.Errorf("document should be valid:\n%v", err)
	}
}

// assertInvalid fails the test if the document validates, and checks that the
// failure cites the expected code.
func assertInvalid(t *testing.T, schema, doc, code string) {
	t.Helper()
	err := validateString(t, schema, doc)
	if err == nil {
		t.Errorf("document should be invalid")
		return
	}
	if code != "" && !strings.Contains(err.Error(), code) {
		t.Errorf("error %q does not cite %s", err, code)
	}
}

const seqSchema = `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType>
      <xs:sequence>
        <xs:element name="a" type="xs:string"/>
        <xs:element name="b" type="xs:int" minOccurs="0"/>
        <xs:element name="c" type="xs:string" maxOccurs="unbounded"/>
      </xs:sequence>
    </xs:complexType>
  </xs:element>
</xs:schema>`

func TestValidateSequence(t *testing.T) {
	assertValid(t, seqSchema, `<root><a>x</a><b>1</b><c>y</c></root>`)
	assertValid(t, seqSchema, `<root><a>x</a><c>y</c></root>`)
	assertValid(t, seqSchema, `<root><a>x</a><c>y</c><c>z</c><c>w</c></root>`)
}

func TestValidateSequenceRejects(t *testing.T) {
	// Out of order.
	assertInvalid(t, seqSchema, `<root><c>y</c><a>x</a></root>`, "cvc-complex-type.2.4")
	// A required element missing.
	assertInvalid(t, seqSchema, `<root><a>x</a></root>`, "cvc-complex-type.2.4")
	// An element that is not in the model at all.
	assertInvalid(t, seqSchema, `<root><a>x</a><c>y</c><zz/></root>`, "cvc-complex-type.2.4")
	// b appearing twice, where maxOccurs is 1.
	assertInvalid(t, seqSchema, `<root><a>x</a><b>1</b><b>2</b><c>y</c></root>`,
		"cvc-complex-type.2.4")
}

func TestValidateSimpleTypeInContent(t *testing.T) {
	// b is an xs:int, so a non-numeric value fails.
	assertInvalid(t, seqSchema, `<root><a>x</a><b>notanint</b><c>y</c></root>`,
		"cvc-datatype-valid")
}

const choiceSchema = `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType>
      <xs:choice>
        <xs:element name="a" type="xs:string"/>
        <xs:element name="b" type="xs:string"/>
      </xs:choice>
    </xs:complexType>
  </xs:element>
</xs:schema>`

func TestValidateChoice(t *testing.T) {
	assertValid(t, choiceSchema, `<root><a>x</a></root>`)
	assertValid(t, choiceSchema, `<root><b>x</b></root>`)
	assertInvalid(t, choiceSchema, `<root><a>x</a><b>y</b></root>`, "")
	assertInvalid(t, choiceSchema, `<root/>`, "")
}

const allSchema = `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType>
      <xs:all>
        <xs:element name="a" type="xs:string"/>
        <xs:element name="b" type="xs:string"/>
        <xs:element name="c" type="xs:string" minOccurs="0"/>
      </xs:all>
    </xs:complexType>
  </xs:element>
</xs:schema>`

// TestValidateAll covers xs:all, whose whole point is that order does not
// matter. XSD 1.0 confines it enough that a seen-set decides it.
func TestValidateAll(t *testing.T) {
	assertValid(t, allSchema, `<root><a>x</a><b>y</b></root>`)
	assertValid(t, allSchema, `<root><b>y</b><a>x</a></root>`)
	assertValid(t, allSchema, `<root><c>z</c><b>y</b><a>x</a></root>`)

	// Each particle at most once.
	assertInvalid(t, allSchema, `<root><a>x</a><a>x</a><b>y</b></root>`,
		"cvc-complex-type.2.4.j")
	// A required particle missing.
	assertInvalid(t, allSchema, `<root><a>x</a></root>`, "cvc-complex-type.2.4.b")
}

const attrSchema = `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType>
      <xs:attribute name="req" type="xs:int" use="required"/>
      <xs:attribute name="opt" type="xs:string"/>
      <xs:attribute name="fix" type="xs:string" fixed="only"/>
    </xs:complexType>
  </xs:element>
</xs:schema>`

func TestValidateAttributes(t *testing.T) {
	assertValid(t, attrSchema, `<root req="1"/>`)
	assertValid(t, attrSchema, `<root req="1" opt="x"/>`)
	assertValid(t, attrSchema, `<root req="1" fix="only"/>`)

	assertInvalid(t, attrSchema, `<root/>`, "cvc-complex-type.4")
	assertInvalid(t, attrSchema, `<root req="x"/>`, "cvc-attribute.3")
	assertInvalid(t, attrSchema, `<root req="1" extra="x"/>`, "cvc-complex-type.3.2.2")
	assertInvalid(t, attrSchema, `<root req="1" fix="other"/>`, "cvc-attribute.4")
}

func TestValidateFacets(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="small">
	    <xs:restriction base="xs:int">
	      <xs:minInclusive value="1"/>
	      <xs:maxInclusive value="10"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:element name="root" type="small"/>
	</xs:schema>`

	assertValid(t, schema, `<root>5</root>`)
	assertValid(t, schema, `<root>1</root>`)
	assertValid(t, schema, `<root>10</root>`)
	assertInvalid(t, schema, `<root>0</root>`, "minInclusive")
	assertInvalid(t, schema, `<root>11</root>`, "maxInclusive")
}

// TestValidateBoundsAreExact guards against comparing through float64.
//
// xs:unsignedLong's maximum does not fit in an int64 and loses its last digits
// as a float64, so a float comparison would admit values outside the type.
func TestValidateBoundsAreExact(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root" type="xs:unsignedLong"/>
	</xs:schema>`

	assertValid(t, schema, `<root>18446744073709551615</root>`)
	assertInvalid(t, schema, `<root>18446744073709551616</root>`, "maxInclusive")
}

func TestValidatePattern(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="code">
	    <xs:restriction base="xs:string">
	      <xs:pattern value="[A-Z]{3}"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:element name="root" type="code"/>
	</xs:schema>`

	assertValid(t, schema, `<root>ABC</root>`)
	assertInvalid(t, schema, `<root>AB</root>`, "pattern")
	// The pattern facet is anchored: a value merely containing a match
	// must fail, unlike fn:matches.
	assertInvalid(t, schema, `<root>XABCX</root>`, "pattern")
}

// TestValidateEnumerationComparesValues covers the rule that an enumeration is
// compared on the value space. For a numeric type "1.0" and "1" are the same
// value, so an enumeration of one admits the other.
func TestValidateEnumerationComparesValues(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="e">
	    <xs:restriction base="xs:decimal">
	      <xs:enumeration value="1.0"/>
	      <xs:enumeration value="2"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:element name="root" type="e"/>
	</xs:schema>`

	assertValid(t, schema, `<root>1.0</root>`)
	assertValid(t, schema, `<root>1</root>`)
	assertValid(t, schema, `<root>2.00</root>`)
	assertInvalid(t, schema, `<root>3</root>`, "enumeration")
}

func TestValidateListLengthCountsItems(t *testing.T) {
	// The length facets on a list count items, not characters.
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="l">
	    <xs:restriction>
	      <xs:simpleType><xs:list itemType="xs:int"/></xs:simpleType>
	      <xs:maxLength value="3"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:element name="root" type="l"/>
	</xs:schema>`

	assertValid(t, schema, `<root>1 2 3</root>`)
	assertValid(t, schema, `<root>100 200 300</root>`)
	assertInvalid(t, schema, `<root>1 2 3 4</root>`, "maxLength")
}

// TestValidateUnionTakesFirstMatch covers the rule that a union takes the first
// member accepting the value, and that each member is tried under its own
// whiteSpace.
func TestValidateUnionTakesFirstMatch(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="u">
	    <xs:union memberTypes="xs:int xs:string"/>
	  </xs:simpleType>
	  <xs:element name="root" type="u"/>
	</xs:schema>`

	assertValid(t, schema, `<root>42</root>`)
	assertValid(t, schema, `<root>notanint</root>`)
	// xs:int collapses whitespace, so the padded value matches the int
	// member. Normalising once up front, before choosing a member, would
	// give a different answer.
	assertValid(t, schema, `<root>  42  </root>`)
}

func TestValidateNillable(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
	  <xs:element name="root" type="xs:int" nillable="true"/>
	</xs:schema>`

	assertValid(t, schema,
		`<root xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:nil="true"/>`)
	// A nilled element must be empty.
	assertInvalid(t, schema,
		`<root xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:nil="true">1</root>`,
		"cvc-elt.3.2.1")
}

func TestValidateNilOnNonNillable(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root" type="xs:int"/>
	</xs:schema>`
	assertInvalid(t, schema,
		`<root xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:nil="true"/>`,
		"cvc-elt.3.1")
}

func TestValidateElementOnlyRejectsText(t *testing.T) {
	assertInvalid(t, seqSchema,
		`<root>stray text<a>x</a><c>y</c></root>`, "cvc-complex-type.2.3")
	// Whitespace between elements is permitted, which is what lets a valid
	// document be indented.
	assertValid(t, seqSchema, "<root>\n  <a>x</a>\n  <c>y</c>\n</root>")
}

func TestValidateMixedAllowsText(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType mixed="true">
	      <xs:sequence>
	        <xs:element name="a" type="xs:string" minOccurs="0" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`
	assertValid(t, schema, `<root>text <a>x</a> more text</root>`)
}

func TestValidateEmptyContent(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType><xs:attribute name="a" type="xs:string"/></xs:complexType>
	  </xs:element>
	</xs:schema>`
	assertValid(t, schema, `<root a="x"/>`)
	assertInvalid(t, schema, `<root a="x">text</root>`, "cvc-complex-type.2.1")
	assertInvalid(t, schema, `<root a="x"><kid/></root>`, "cvc-complex-type.2.1")
}

func TestValidateWildcard(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:any namespace="##any" processContents="skip"
	                minOccurs="0" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	assertValid(t, schema, `<root><anything/><at:all xmlns:at="urn:x"/></root>`)
}

func TestValidateStrictWildcardNeedsDeclaration(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="known" type="xs:string"/>
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:any namespace="##any" processContents="strict"
	                minOccurs="0" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	assertValid(t, schema, `<root><known>x</known></root>`)
	assertInvalid(t, schema, `<root><unknown/></root>`, "cvc-complex-type.2.4.c")
}

func TestValidateSubstitutionGroup(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           xmlns:t="urn:t" targetNamespace="urn:t" elementFormDefault="qualified">
	  <xs:element name="head" type="xs:string"/>
	  <xs:element name="member" type="xs:string" substitutionGroup="t:head"/>
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence><xs:element ref="t:head"/></xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	s, err := loadFromMap(t, "m.xsd", map[string]string{"m.xsd": schema})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, doc := range []string{
		`<root xmlns="urn:t"><head>x</head></root>`,
		`<root xmlns="urn:t"><member>x</member></root>`,
	} {
		tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(tree.Root, ValidateOptions{}); err != nil {
			t.Errorf("%s should be valid:\n%v", doc, err)
		}
	}
}

func TestValidateXSIType(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:sequence><xs:element name="a" type="xs:string"/></xs:sequence>
	  </xs:complexType>
	  <xs:complexType name="derived">
	    <xs:complexContent>
	      <xs:extension base="base">
	        <xs:sequence><xs:element name="b" type="xs:string"/></xs:sequence>
	      </xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="root" type="base"/>
	</xs:schema>`

	assertValid(t, schema, `<root><a>x</a></root>`)
	assertValid(t, schema,
		`<root xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
		       xsi:type="derived"><a>x</a><b>y</b></root>`)
	// An unrelated type may not be substituted. The prefix has to be
	// declared on the instance, since xsi:type is resolved against the
	// namespaces in scope where it is written, not against the schema.
	assertInvalid(t, schema,
		`<root xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
		       xmlns:xs="http://www.w3.org/2001/XMLSchema"
		       xsi:type="xs:string">x</root>`, "cvc-elt.4.3")

	// A prefix that is not in scope on the instance is a different fault.
	assertInvalid(t, schema,
		`<root xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
		       xsi:type="nope:t">x</root>`, "cvc-elt.4.2")
}

func TestValidateIDUniqueness(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="item" maxOccurs="unbounded">
	          <xs:complexType>
	            <xs:attribute name="id" type="xs:ID"/>
	            <xs:attribute name="ref" type="xs:IDREF"/>
	          </xs:complexType>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	assertValid(t, schema, `<root><item id="a"/><item id="b" ref="a"/></root>`)
	// A forward reference is legal: the check happens at the root, not per
	// element.
	assertValid(t, schema, `<root><item id="a" ref="b"/><item id="b"/></root>`)

	assertInvalid(t, schema, `<root><item id="a"/><item id="a"/></root>`, "cvc-id.2")
	assertInvalid(t, schema, `<root><item id="a" ref="nope"/></root>`, "cvc-id.1")
}

func TestValidateAnnotateWritesTypes(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence><xs:element name="n" type="xs:int"/></xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	tree, err := xdm.ParseString(`<root><n>1</n></root>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(tree.Root, ValidateOptions{Annotate: true}); err != nil {
		t.Fatalf("should be valid: %v", err)
	}
	n := tree.Root.ChildElements()[0].ChildElements()[0]
	if n.TypeAnnotation != "int" {
		t.Errorf("TypeAnnotation is %q, want int", n.TypeAnnotation)
	}
}

// TestValidateLargeMaxOccursDoesNotExplode guards the decision not to unroll
// occurrence bounds.
//
// Unrolling makes the automaton linear in the *value* of maxOccurs, which is
// exponential in the size of the schema text. Both Xerces and Saxon hit this;
// this uses runtime counters instead, so a huge bound costs nothing to compile.
func TestValidateLargeMaxOccursDoesNotExplode(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="a" type="xs:string" minOccurs="0" maxOccurs="100000000"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	done := make(chan error, 1)
	go func() { done <- validateString(t, schema, `<root><a>x</a><a>y</a></root>`) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("should be valid: %v", err)
		}
	case <-timeoutAfterSecond():
		t.Fatal("a large maxOccurs made compilation explode")
	}
}

func TestValidateMaxErrorsIsBounded(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="n" type="xs:int" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	var b strings.Builder
	b.WriteString("<root>")
	for i := 0; i < 50; i++ {
		b.WriteString("<n>bad</n>")
	}
	b.WriteString("</root>")

	s := mustParseSchema(t, schema)
	tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Validate(tree.Root, ValidateOptions{MaxErrors: 5})
	if err == nil {
		t.Fatal("should be invalid")
	}
	ve, ok := err.(*ValidationErrors)
	if !ok {
		t.Fatalf("error is %T, want *ValidationErrors", err)
	}
	if len(ve.Errors) > 5 {
		t.Errorf("got %d errors, want at most 5", len(ve.Errors))
	}
}

// TestAttributeGroupIsFlattened guards a bug that dropped every attribute
// declared through an attribute group.
//
// readAttributes used to return a slice the caller assigned, while the fixup
// that flattened a group reference appended to a *local* variable — so the
// group's attributes were resolved and then thrown away. The schema loaded
// cleanly and every document using one of those attributes was rejected.
func TestAttributeGroupIsFlattened(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:attributeGroup name="g">
	    <xs:attribute name="a" type="xs:string"/>
	    <xs:attribute name="b" type="xs:int"/>
	  </xs:attributeGroup>
	  <xs:element name="root">
	    <xs:complexType><xs:attributeGroup ref="g"/></xs:complexType>
	  </xs:element>
	</xs:schema>`

	assertValid(t, schema, `<root a="x" b="1"/>`)
	assertInvalid(t, schema, `<root b="notanint"/>`, "cvc-attribute.3")
	assertInvalid(t, schema, `<root c="x"/>`, "cvc-complex-type.3.2.2")
}

// TestAttributesAreInherited covers §3.4.2: a derived type's attribute uses
// include the base's, for extension and restriction alike.
//
// Without it every attribute declared on a base type vanished from its
// subtypes, which is silent in the same way: the schema loads and the document
// is rejected for carrying an attribute its own base declared.
func TestAttributesAreInherited(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:attribute name="ba" type="xs:string"/>
	  </xs:complexType>
	  <xs:complexType name="mid">
	    <xs:complexContent>
	      <xs:extension base="base"><xs:attribute name="ma" type="xs:string"/></xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:complexType name="leaf">
	    <xs:complexContent>
	      <xs:extension base="mid"><xs:attribute name="la" type="xs:string"/></xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="root" type="leaf"/>
	</xs:schema>`

	// Every level's attribute is available on the leaf.
	assertValid(t, schema, `<root ba="x" ma="y" la="z"/>`)
	assertValid(t, schema, `<root ba="x"/>`)
	assertInvalid(t, schema, `<root zz="x"/>`, "cvc-complex-type.3.2.2")
}

// TestQNameLengthFacetIsIgnored records a deliberate divergence from the naive
// reading.
//
// The length facets are measured in the value space, and a QName's value is a
// (namespace, local name) pair rather than a string — there is no length to
// compare. The W3C suite expects a 46-character QName to satisfy length="7",
// which is only possible if the facet is ignored.
func TestQNameLengthFacetIsIgnored(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="q">
	    <xs:restriction base="xs:QName"><xs:length value="7"/></xs:restriction>
	  </xs:simpleType>
	  <xs:element name="root" type="q"/>
	</xs:schema>`

	assertValid(t, schema, `<root>short</root>`)
	assertValid(t, schema,
		`<root xmlns:x="urn:x">x:a-considerably-longer-local-name-than-seven</root>`)
}

// TestRepeatedChoiceOfGroups covers a counter bug that only appears once a
// group reference makes position numbering non-monotonic.
//
// The runtime tells a repetition restart from a continuation. The first version
// did it by comparing position indices, on the reasoning that a restart goes
// backwards — which holds only while positions are numbered in the order they
// appear. A choice of two groups numbers the second group's positions after the
// first's, so moving from the first group's end into the second group's start
// is a restart that runs *forwards*: the whole thing was read as one long
// repetition and rejected once it passed maxOccurs.
func TestRepeatedChoiceOfGroups(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="x">
	    <xs:sequence>
	      <xs:element name="x1" type="xs:string"/>
	      <xs:element name="x2" type="xs:string"/>
	    </xs:sequence>
	  </xs:group>
	  <xs:group name="y">
	    <xs:choice>
	      <xs:element name="y1" type="xs:string"/>
	      <xs:element name="y2" type="xs:string"/>
	    </xs:choice>
	  </xs:group>
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:choice minOccurs="0" maxOccurs="4">
	        <xs:group ref="x"/>
	        <xs:group ref="y"/>
	      </xs:choice>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	// Two repetitions, the second entering the *other* group.
	assertValid(t, schema, `<root><x1>a</x1><x2>b</x2><y1>c</y1></root>`)
	// Four repetitions, the maximum.
	assertValid(t, schema,
		`<root><y1>a</y1><y2>b</y2><y1>c</y1><x1>d</x1><x2>e</x2></root>`)
	// Five exceeds it.
	assertInvalid(t, schema,
		`<root><y1>a</y1><y1>b</y1><y1>c</y1><y1>d</y1><y1>e</y1></root>`,
		"cvc-complex-type.2.4")
}

// TestElementDefaultAppliesToEmptyContent covers §3.3.4 clause 5.2: an element
// with no content and a value constraint takes that value.
//
// Without it an empty <price/> declared with default="0" was validated as the
// empty string, which is not a valid xs:decimal — so a document the schema
// explicitly provides for was rejected.
func TestElementDefaultAppliesToEmptyContent(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="price" type="xs:decimal" default="0"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	assertValid(t, schema, `<root><price/></root>`)
	assertValid(t, schema, `<root><price>1.5</price></root>`)
	assertInvalid(t, schema, `<root><price>notanumber</price></root>`,
		"cvc-datatype-valid")
}

// TestNilArgumentsReturnErrors pins that the package's entry points refuse a
// nil document rather than dereferencing it.
//
// A nil root is a caller's mistake, not an attack. But this library is meant
// to run inside servers, where a nil arriving from a failed parse upstream is
// exactly how it happens — and a panic there takes down every other request in
// the process, not just the one that caused it.
func TestNilArgumentsReturnErrors(t *testing.T) {
	if err := NewSchema().Validate(nil, ValidateOptions{}); err == nil {
		t.Error("Validate(nil) should return an error")
	}
	if _, err := ParseSchema(nil); err == nil {
		t.Error("ParseSchema(nil) should return an error")
	}
}

// TestValidateMaxDepth pins the validator's own recursion bound.
//
// It is not the parser's. Validation recurses once per element depth at
// roughly 3 kB of stack a level, and exceeding Go's stack limit is a
// `fatal error: stack overflow` that recover() cannot catch — so it kills the
// process rather than failing the request. A caller who raises
// xdm.ParseOptions.MaxDepth to accept a legitimately deep document has not
// thereby agreed to arm that crash, so the two limits are separate knobs.
func TestValidateMaxDepth(t *testing.T) {
	const src = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="r" type="R"/>
	  <xs:complexType name="R"><xs:sequence>
	    <xs:element name="r" type="R" minOccurs="0"/>
	  </xs:sequence></xs:complexType></xs:schema>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSchema(tree.Root)
	if err != nil {
		t.Fatal(err)
	}

	const depth = 3000
	doc := strings.Repeat("<r>", depth) + strings.Repeat("</r>", depth)
	dt, err := xdm.ParseString(doc, xdm.ParseOptions{MaxDepth: depth + 10, MaxBytes: -1})
	if err != nil {
		t.Fatal(err)
	}

	// Past the default, validation fails rather than recursing on.
	err = s.Validate(dt.Root, ValidateOptions{})
	if err == nil {
		t.Error("nesting past the default MaxDepth should be refused")
	} else if !strings.Contains(err.Error(), "nesting exceeds") {
		t.Errorf("error %q does not say the nesting limit was reached", err)
	}

	// A caller who means it can raise the bound.
	if err := s.Validate(dt.Root, ValidateOptions{MaxDepth: depth + 10}); err != nil {
		t.Errorf("a raised MaxDepth should allow the document: %v", err)
	}

	// The reported path is bounded too: a failure at depth 3000 must not
	// produce three thousand path segments no one can read.
	err = s.Validate(dt.Root, ValidateOptions{})
	var ve *ValidationErrors
	if errors.As(err, &ve) && len(ve.Errors) > 0 {
		if n := len(ve.Errors[0].Path); n > 200 {
			t.Errorf("error path is %d characters; it should be elided", n)
		}
	}
}

// Whitespace-only text in an element whose declared content is element-only
// is ignorable (XML 1.0 section 2.10), and XSLT 2.0 section 4.4 makes removing
// it unconditional for a source document. The schema is where the content
// model first becomes known, so this is where the removal has to happen — the
// DTD-derived counterpart can do it at parse time, but a schema is not read
// until after the parse.
//
// This is the producer half of what strip-space-007 in the XSLT suite asserts.
func TestAnnotateStripsIgnorableWhitespace(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="a" type="xs:string"/>
	        <xs:element name="keep" type="xs:string"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	const doc = "<root>\n  <a>x</a>\n  <keep>   </keep>\n</root>"
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(tree.Root, ValidateOptions{Annotate: true}); err != nil {
		t.Fatalf("should be valid: %v", err)
	}
	root := tree.Root.ChildElements()[0]
	for _, c := range root.Children {
		if c.Kind == xdm.KindText {
			t.Errorf("element-only content kept whitespace text %q", c.Value)
		}
	}
	if n := len(root.Children); n != 2 {
		t.Errorf("root has %d children, want 2", n)
	}
	// The whitespace inside "keep" is CONTENT: its type is xs:string, so
	// there is nothing ignorable about it.
	keep := root.ChildElements()[1]
	if got := keep.StringValue(); got != "   " {
		t.Errorf("simple content lost its whitespace: got %q", got)
	}
}

// The removal is scoped to annotating a whole DOCUMENT. A caller assessing a
// CONSTRUCTED element — xsl:copy-of with validation="strict", XSLT 2.0
// section 19.2.1 — is validating a result tree, and section 4.4 says nothing
// about those, so its whitespace must survive.
func TestAnnotateDoesNotStripWhenValidatingABareElement(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence><xs:element name="a" type="xs:string"/></xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	tree, err := xdm.ParseString("<root>\n  <a>x</a>\n</root>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	el := tree.Root.ChildElements()[0]
	if err := s.Validate(el, ValidateOptions{Annotate: true}); err != nil {
		t.Fatalf("should be valid: %v", err)
	}
	text := 0
	for _, c := range el.Children {
		if c.Kind == xdm.KindText {
			text++
		}
	}
	if text == 0 {
		t.Error("validating a bare element stripped its whitespace")
	}
}

// xml:space="preserve" says the whitespace here is content, and it is honoured
// on the same footing as it is in the DTD-derived rule.
func TestAnnotateHonoursXMLSpacePreserve(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           xmlns:xml="http://www.w3.org/XML/1998/namespace">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence><xs:element name="a" type="xs:string"/></xs:sequence>
	      <xs:attribute ref="xml:space"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	tree, err := xdm.ParseString(
		"<root xml:space=\"preserve\">\n  <a>x</a>\n</root>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Validate(tree.Root, ValidateOptions{Annotate: true})
	root := tree.Root.ChildElements()[0]
	text := 0
	for _, c := range root.Children {
		if c.Kind == xdm.KindText {
			text++
		}
	}
	if text == 0 {
		t.Error("xml:space=\"preserve\" did not preserve the whitespace")
	}
}

// The XPath dynamic context XSD 1.1 gives an assertion or a type alternative
// defines the *default collection* to be the empty sequence, not an absence.
// fn:collection() with no argument therefore returns () rather than raising
// FODC0002.
//
// The distinction is not academic: a type alternative whose test raises is
// silently skipped, so a false answer and an error are indistinguishable from
// the outside. saxonData/CTA cta0022 writes "empty(collection())" into an
// alternative deliberately, and with no resolver configured the alternative was
// passed over and the element fell through to its declared union type — which
// made a valid xs:date fail as "no member type of the union", the worst kind of
// failure this validator can produce.
func TestAlternativeSeesAnEmptyDefaultCollection(t *testing.T) {
	schema := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	  elementFormDefault="qualified">
	 <xs:element name="doc">
	  <xs:complexType><xs:sequence>
	   <xs:element ref="when"/></xs:sequence></xs:complexType></xs:element>
	 <xs:element name="when" type="u">
	  <xs:alternative test="empty(collection())" type="xs:date"/>
	  <xs:alternative type="xs:error"/>
	 </xs:element>
	 <xs:simpleType name="u"><xs:union memberTypes="xs:date xs:time xs:gYearMonth"/></xs:simpleType>
	</xs:schema>`
	tree, err := xdm.ParseString(schema, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the schema: %v", err)
	}
	s, err := Load(tree.Root, "s.xsd", Options{Version: Version11})
	if err != nil {
		t.Fatalf("loading the schema: %v", err)
	}
	inst, err := xdm.ParseString(`<doc><when>2010-10-16</when></doc>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the instance: %v", err)
	}
	if err := s.Validate(inst.Root, ValidateOptions{}); err != nil {
		t.Errorf("the alternative must select xs:date, not fall through to "+
			"the declared union:\n%v", err)
	}
}

// A *named* collection is still unavailable: the empty default collection is
// one specific value, not a blanket "every collection is empty" resolver.
func TestAssertionNamedCollectionIsStillUnavailable(t *testing.T) {
	ctx := newAssertContext(nil)
	if ctx.Collections == nil {
		t.Fatal("an assertion context must carry a collection resolver")
	}
	if _, err := ctx.Collections.ResolveCollection("", ""); err != nil {
		t.Errorf("the default collection must be the empty sequence: %v", err)
	}
	if _, err := ctx.Collections.ResolveCollection("urn:x", ""); err == nil {
		t.Error("a named collection must not resolve")
	}
}

// A repeated group whose every member is optional puts each position at both
// ends of the repetition, so the automaton cannot tell a step forward through
// the group from a wraparound into a fresh repetition. Both readings are legal
// attributions, and the two occurrence bounds want opposite ones: maxOccurs is
// satisfied if the fewest repetitions fit, minOccurs if the most reach it.
const optionalSeqSchema = `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="doc">
    <xs:complexType>
      <xs:sequence maxOccurs="3">
        <xs:element name="a" minOccurs="0"/>
        <xs:any namespace="##other" minOccurs="0" maxOccurs="unbounded"
                processContents="skip"/>
        <xs:element name="b" minOccurs="0" maxOccurs="2"/>
      </xs:sequence>
    </xs:complexType>
  </xs:element>
</xs:schema>`

func TestRepeatedOptionalSequence(t *testing.T) {
	// One iteration: a, then any number of wildcards, then b. Reading each
	// forward step as a restart spends maxOccurs="3" before the b.
	assertValid(t, optionalSeqSchema,
		`<doc xmlns:x="o"><a/><x:q/><x:q/><x:q/><b/></doc>`)
	// Two iterations, the second beginning with a wraparound into the
	// wildcard: still inside the bound of three.
	assertValid(t, optionalSeqSchema,
		`<doc xmlns:x="o"><a/><x:q/><x:q/><b/><x:q/><x:q/></doc>`)
	assertValid(t, optionalSeqSchema, `<doc><b/><b/><b/></doc>`)
	assertInvalid(t, optionalSeqSchema,
		`<doc><b/><b/><b/><b/><b/><b/><b/></doc>`, "cvc-complex-type.2.4")
	// Known shortfall, and the same before this pair of counts as after:
	// b twice per iteration and three iterations is six, but the low count
	// stops at three. The wraparound from b back to b is the outer scope's
	// own loop-back edge and not also a step within one iteration, so it
	// has no second reading to prefer, and the two scopes' remaining room
	// is approximated apart rather than searched together. Nothing in the
	// W3C suite turns on it.
	assertInvalid(t, optionalSeqSchema,
		`<doc><b/><b/><b/><b/></doc>`, "cvc-complex-type.2.4")
}

// The mirror of the case above: here the ambiguous transition has to be read
// as a restart, because that is the only reading under which the group meets
// its minimum. One position, one edge, and the same edge is both the element's
// own repetition and the sequence's.
func TestRepeatedSequenceMinOccurs(t *testing.T) {
	const schema = `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType>
      <xs:sequence minOccurs="2" maxOccurs="unbounded">
        <xs:element name="e" maxOccurs="unbounded"/>
      </xs:sequence>
    </xs:complexType>
  </xs:element>
</xs:schema>`
	assertValid(t, schema, `<root><e/><e/></root>`)
	assertInvalid(t, schema, `<root><e/></root>`, "cvc-complex-type.2.4")

	// And with both bounds fixed the admissible lengths are 2 and 4 only:
	// three e is neither one sequence nor two, and the outer scope must not
	// offer a repetition the low count has not yet spent.
	const fixed = `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType>
      <xs:sequence maxOccurs="2">
        <xs:element name="e" minOccurs="2" maxOccurs="2"/>
      </xs:sequence>
    </xs:complexType>
  </xs:element>
</xs:schema>`
	assertValid(t, fixed, `<root><e/><e/></root>`)
	assertInvalid(t, fixed, `<root><e/><e/><e/></root>`, "cvc-complex-type.2.4")
	assertInvalid(t, fixed, `<root><e/><e/><e/><e/><e/></root>`, "cvc-complex-type.2.4")
}

// TestValidateContextCancels pins that a caller can bound how long validation
// runs. The schema is the one docs/security.md names as the quadratic case: a
// recursive element carrying a key with a ".//" selector, where the cost grows
// as the square of the nesting depth.
func TestValidateContextCancels(t *testing.T) {
	const src = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="r" type="R">
	    <xs:key name="k">
	      <xs:selector xpath=".//r"/>
	      <xs:field xpath="@id"/>
	    </xs:key>
	  </xs:element>
	  <xs:complexType name="R">
	    <xs:sequence><xs:element ref="r" minOccurs="0"/></xs:sequence>
	    <xs:attribute name="id" type="xs:string"/>
	  </xs:complexType></xs:schema>`
	s := mustParseSchema(t, src)

	const depth = 900
	var b strings.Builder
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, "<r id=\"v%d\">", i)
	}
	b.WriteString(strings.Repeat("</r>", depth))
	dt, err := xdm.ParseString(b.String(), xdm.ParseOptions{
		MaxDepth: depth + 10, MaxBytes: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	opts := ValidateOptions{MaxDepth: depth + 10}

	// The document is valid, so a run that completes returns nil — which is
	// what makes a context error here unambiguous evidence of cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = s.ValidateContext(ctx, dt.Root, opts)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ValidateContext returned %v, want context.DeadlineExceeded", err)
	}
	// A deadline nobody looks at until the end is not a bound: the whole point
	// is to stop mid-walk, not to report afterwards that time had run out.
	if elapsed > time.Second {
		t.Errorf("cancellation took %v; the deadline was not honoured promptly",
			elapsed)
	}

	// A live context still validates the document normally, and the
	// no-context entry point is unchanged.
	if err := s.ValidateContext(context.Background(), dt.Root, opts); err != nil {
		t.Errorf("an uncancelled run should accept the document: %v", err)
	}
	if err := s.Validate(dt.Root, opts); err != nil {
		t.Errorf("Validate should accept the document: %v", err)
	}

	// An already-cancelled context stops before any work at all.
	dead, stop := context.WithCancel(context.Background())
	stop()
	if err := s.ValidateContext(dead, dt.Root, opts); !errors.Is(
		err, context.Canceled) {
		t.Errorf("a cancelled context gave %v, want context.Canceled", err)
	}
}
