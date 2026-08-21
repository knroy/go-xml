package xsd

import (
	"strings"
	"testing"

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
