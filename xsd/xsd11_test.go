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

// TestOpenContentSuffixMode covers the difference between the two modes.
//
// Suffix mode admits the wildcard only once the content model has been
// satisfied; interleave admits it anywhere. Letting suffix match at the start
// would make it mean interleave.
func TestOpenContentSuffixMode(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:openContent mode="suffix">
	        <xs:any namespace="##any" processContents="skip"/>
	      </xs:openContent>
	      <xs:sequence>
	        <xs:element name="a" type="xs:string"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<root><a>x</a></root>`); err != nil {
		t.Errorf("the model alone should be valid: %v", err)
	}
	if err := check11(t, s, `<root><a>x</a><extra/></root>`); err != nil {
		t.Errorf("suffix content after the model should be valid: %v", err)
	}
	if err := check11(t, s, `<root><extra/><a>x</a></root>`); err == nil {
		t.Error("suffix content before the model should be rejected")
	}
}

// TestOpenContentIsInherited covers open content flowing to a derived type,
// which is the same shape of bug as attribute uses not being inherited: a type
// extending one with open content silently closed a model its base had opened.
func TestOpenContentIsInherited(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="B">
	    <xs:openContent mode="interleave">
	      <xs:any namespace="##any" processContents="skip"/>
	    </xs:openContent>
	    <xs:sequence><xs:element name="a" type="xs:string"/></xs:sequence>
	  </xs:complexType>
	  <xs:complexType name="R">
	    <xs:complexContent>
	      <xs:extension base="B">
	        <xs:sequence><xs:element name="d" type="xs:string" minOccurs="0"/></xs:sequence>
	      </xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="root" type="R"/>
	</xs:schema>`)

	if err := check11(t, s, `<root><a>x</a><extra/></root>`); err != nil {
		t.Errorf("the derived type should inherit its base's open content: %v", err)
	}
}

// TestInheritableAttributes covers the XSD 1.1 feature that lets an ancestor's
// attribute choose a descendant's type.
//
// Without it, conditional type assignment on a nested element sees only that
// element's own attributes, so a document-level xml:lang cannot select the type
// of anything below it — which is the case the feature exists for.
func TestInheritableAttributes(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence><xs:element ref="chap"/></xs:sequence>
	      <xs:attribute name="lang" type="xs:string" inheritable="true"/>
	    </xs:complexType>
	  </xs:element>
	  <xs:element name="chap">
	    <xs:alternative test="@lang='de'">
	      <xs:complexType>
	        <xs:sequence><xs:element name="de" type="xs:string"/></xs:sequence>
	      </xs:complexType>
	    </xs:alternative>
	    <xs:alternative>
	      <xs:complexType>
	        <xs:sequence><xs:element name="en" type="xs:string"/></xs:sequence>
	      </xs:complexType>
	    </xs:alternative>
	  </xs:element>
	</xs:schema>`)

	// The ancestor's lang selects the German alternative for chap.
	if err := check11(t, s, `<doc lang="de"><chap><de>x</de></chap></doc>`); err != nil {
		t.Errorf("an inherited attribute should select the type: %v", err)
	}
	// And the default alternative otherwise.
	if err := check11(t, s, `<doc lang="en"><chap><en>x</en></chap></doc>`); err != nil {
		t.Errorf("the default alternative should apply: %v", err)
	}
	// The wrong child for the selected type is rejected.
	if err := check11(t, s, `<doc lang="de"><chap><en>x</en></chap></doc>`); err == nil {
		t.Error("the German alternative should not accept <en>")
	}
}

// TestAssertionComparesTypedValues covers assertions running on the PSVI.
//
// "@length eq count(entry)" compares an integer with an integer only if the
// attribute carries the type the schema gave it. Untyped, it promotes to a
// string against a numeric operand and raises XPTY0004 — so the assertion
// fails to *evaluate* rather than being true or false, which is a different
// and much less useful answer.
func TestAssertionComparesTypedValues(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="list">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="entry" type="xs:string" minOccurs="0"
	                    maxOccurs="unbounded"/>
	      </xs:sequence>
	      <xs:attribute name="length" type="xs:integer"/>
	      <xs:assert test="@length eq count(entry)"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<list length="2"><entry>a</entry><entry>b</entry></list>`); err != nil {
		t.Errorf("a matching count should be valid: %v", err)
	}
	err := check11(t, s, `<list length="3"><entry>a</entry></list>`)
	if err == nil {
		t.Fatal("a mismatched count should be rejected")
	}
	// The failure must be the assertion being false, not the comparison
	// raising.
	if strings.Contains(err.Error(), "could not be evaluated") {
		t.Errorf("the assertion raised instead of evaluating: %v", err)
	}
}

// TestSimpleTypeAssertion covers <xs:assertion> as a facet, where the value
// under test is bound to $value.
//
// There is no element to be the context item, so an expression has nothing else
// to refer to — a simple-type assertion that cannot see $value can say nothing
// at all.
func TestSimpleTypeAssertion(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="even">
	    <xs:restriction base="xs:integer">
	      <xs:assertion test="$value mod 2 = 0"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:element name="n" type="even"/>
	</xs:schema>`)

	if err := check11(t, s, `<n>4</n>`); err != nil {
		t.Errorf("an even value should be valid: %v", err)
	}
	if err := check11(t, s, `<n>5</n>`); err == nil {
		t.Error("an odd value should be rejected")
	}
}

// TestAssertionCanUseCurrentDate records that the date and time functions work
// inside an assertion.
//
// XSD 1.1 permits them and this package has no transform to inherit a clock
// from, so the context sets one — read once, so that two calls inside one
// assertion cannot disagree.
func TestAssertionCanUseCurrentDate(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:attribute name="a" type="xs:string"/>
	      <xs:assert test="year-from-date(current-date()) gt 2000"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<e a="x"/>`); err != nil {
		t.Errorf("current-date() should be available in an assertion: %v", err)
	}
}

// TestNotQNameExcludesNames covers {disallowed names}: a wildcard whose
// namespace constraint admits a name may still refuse it by name.
//
// The two tests are independent, which is the part worth pinning: a schema
// writes notQName precisely to exclude something the namespace constraint lets
// through, so applying only the constraint admits exactly the names the author
// meant to keep out.
func TestNotQNameExcludesNames(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:any namespace="##any" notQName="bad" processContents="skip"
	                minOccurs="0" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<e><good/></e>`); err != nil {
		t.Errorf("a name not in notQName should be admitted: %v", err)
	}
	if err := check11(t, s, `<e><bad/></e>`); err == nil {
		t.Error("notQName should exclude the name it lists")
	}
}

// TestNotQNameUnprefixedIsAbsentNamespace pins the resolution rule that makes
// notQName differ from every other QName-valued attribute: an unprefixed entry
// names the absent namespace even when a default namespace is in scope.
//
// Resolving it against the default would silently retarget the exclusion at a
// namespace the author did not name, admitting the element they excluded.
func TestNotQNameUnprefixedIsAbsentNamespace(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           xmlns="urn:d" targetNamespace="urn:d">
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:any namespace="##any" notQName="bad" processContents="skip"
	                minOccurs="0" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	// {urn:d}bad is *not* excluded: the notQName entry is unprefixed and so
	// names {}bad, despite urn:d being the default namespace here.
	if err := check11(t, s, `<e xmlns="urn:d"><bad/></e>`); err != nil {
		t.Errorf("unprefixed notQName should not exclude a qualified name: %v", err)
	}
	if err := check11(t, s, `<e xmlns="urn:d"><bad xmlns=""/></e>`); err == nil {
		t.Error("unprefixed notQName should exclude the unqualified name")
	}
}

// TestNotQNameDefined covers ##defined: refuse any name the schema declares
// globally, which is how an extension wildcard is written so that it cannot
// shadow a declared element.
func TestNotQNameDefined(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="known" type="xs:string"/>
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:any namespace="##any" notQName="##defined" processContents="skip"
	                minOccurs="0" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<e><unknown/></e>`); err != nil {
		t.Errorf("##defined should admit an undeclared name: %v", err)
	}
	if err := check11(t, s, `<e><known/></e>`); err == nil {
		t.Error("##defined should exclude a globally declared name")
	}
}

// TestNotQNameDefinedSibling covers ##definedSibling, which is local where
// ##defined is global: it excludes names declared by other particles in the
// same content model, and nothing else.
//
// The wildcard here precedes the declaration it must exclude, which is why the
// set cannot be resolved while the particle tree is still being walked.
func TestNotQNameDefinedSibling(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="elsewhere" type="xs:string"/>
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:any namespace="##any" notQName="##definedSibling"
	                processContents="skip" minOccurs="0" maxOccurs="unbounded"/>
	        <xs:element name="sib" minOccurs="0"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<e><other/></e>`); err != nil {
		t.Errorf("##definedSibling should admit an unrelated name: %v", err)
	}
	// elsewhere is declared globally but is not a sibling, so unlike
	// ##defined it stays admitted — this is the distinction between them.
	if err := check11(t, s, `<e><elsewhere/></e>`); err != nil {
		t.Errorf("##definedSibling should not exclude a non-sibling global: %v", err)
	}
	if err := check11(t, s, `<e><sib/><sib/></e>`); err == nil {
		t.Error("##definedSibling should exclude a name declared alongside it")
	}
}

// TestAllExtensionMerges covers §3.4.2.3.3 clause 2.2: extending an all group
// with an all group yields one all group, not a sequence of two.
//
// A sequence would require every base child to precede every extension child,
// which is exactly the ordering an all group exists to not impose — so the 1.0
// splice rejects documents a 1.1 schema permits.
func TestAllExtensionMerges(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:all>
	      <xs:element name="a"/>
	    </xs:all>
	  </xs:complexType>
	  <xs:complexType name="ext">
	    <xs:complexContent>
	      <xs:extension base="base">
	        <xs:all>
	          <xs:element name="b"/>
	        </xs:all>
	      </xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="e" type="ext"/>
	</xs:schema>`)

	// The extension's child first: a sequence splice would reject this.
	if err := check11(t, s, `<e><b/><a/></e>`); err != nil {
		t.Errorf("merged all group should accept either order: %v", err)
	}
	if err := check11(t, s, `<e><a/><b/></e>`); err != nil {
		t.Errorf("merged all group should accept either order: %v", err)
	}
	if err := check11(t, s, `<e><a/></e>`); err == nil {
		t.Error("merged all group should still require the extension's child")
	}
}

// TestAllExtensionOptionalBase pins that minOccurs="0" on a merged all group
// survives the merge.
//
// Once the members are folded into a larger group there is no group left to
// carry the bound, so it has to move onto each member. Dropping it would turn
// an optional base's children into required ones.
func TestAllExtensionOptionalBase(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:all minOccurs="0">
	      <xs:element name="a"/>
	    </xs:all>
	  </xs:complexType>
	  <xs:complexType name="ext">
	    <xs:complexContent>
	      <xs:extension base="base">
	        <xs:all minOccurs="0">
	          <xs:element name="b"/>
	        </xs:all>
	      </xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="e" type="ext"/>
	</xs:schema>`)

	if err := check11(t, s, `<e><b/><a/></e>`); err != nil {
		t.Errorf("optional merged all group should accept both: %v", err)
	}
	if err := check11(t, s, `<e/>`); err != nil {
		t.Errorf("optional merged all group should accept nothing: %v", err)
	}
}

// TestAllGroupReferenceFlattens covers <xs:group ref="..."/> naming an all
// group from inside an all group, which XSD 1.1 permits so that an all group
// can be shared.
//
// The members of the referenced group are members of the enclosing one — a
// nested all adds no ordering of its own — so flattening is the meaning rather
// than an approximation.
func TestAllGroupReferenceFlattens(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="g">
	    <xs:all>
	      <xs:element name="b"/>
	      <xs:element name="c" maxOccurs="2"/>
	    </xs:all>
	  </xs:group>
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:all>
	        <xs:element name="a" minOccurs="0"/>
	        <xs:group ref="g"/>
	      </xs:all>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<e><c/><b/><c/><a/></e>`); err != nil {
		t.Errorf("referenced all group members should interleave: %v", err)
	}
	if err := check11(t, s, `<e><c/><c/><c/><b/></e>`); err == nil {
		t.Error("a member's maxOccurs should survive flattening")
	}
	if err := check11(t, s, `<e><c/><c/></e>`); err == nil {
		t.Error("a required member of a referenced all group should be required")
	}
}

// TestAllWildcardMinOccurs pins that a wildcard particle in an all group
// carries its own minOccurs in XSD 1.1.
//
// Falling short of it is the same failure as a missing element; there is just
// no name to name in the message, which is why the check is easy to write only
// for element particles and so to omit here.
func TestAllWildcardMinOccurs(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:all>
	        <xs:element name="a" minOccurs="0"/>
	        <xs:any namespace="urn:w" processContents="skip"
	                minOccurs="2" maxOccurs="2"/>
	      </xs:all>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<e xmlns:w="urn:w"><w:x/><w:y/></e>`); err != nil {
		t.Errorf("two wildcard matches should satisfy minOccurs=2: %v", err)
	}
	if err := check11(t, s, `<e xmlns:w="urn:w"><w:x/></e>`); err == nil {
		t.Error("one wildcard match should not satisfy minOccurs=2")
	}
}

// TestMultipleIDsOnOneElement covers the XSD 1.1 relaxation that an element may
// carry more than one ID, and that two of them may hold the same value.
//
// The binding is to the element, not to the attribute, so the same value twice
// on one element is one binding. Counting occurrences instead rejects a
// document the schema permits.
func TestMultipleIDsOnOneElement(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element ref="p" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	  <xs:element name="p">
	    <xs:complexType>
	      <xs:attribute name="a" type="xs:ID"/>
	      <xs:attribute name="b" type="xs:ID"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<doc><p a="x" b="x"/></doc>`); err != nil {
		t.Errorf("two IDs on one element may share a value: %v", err)
	}
	// The same value on two *different* elements is still a duplicate.
	if err := check11(t, s, `<doc><p a="x"/><p a="x"/></doc>`); err == nil {
		t.Error("the same ID on two elements should still be a duplicate")
	}
}

// TestListOfIDAndIDREF covers xs:list of xs:ID, which XSD 1.0 forbade because
// an element could carry only one ID. Lifting that restriction lifts this one,
// and each item of the list is its own binding.
func TestListOfIDAndIDREF(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="ids"><xs:list itemType="xs:ID"/></xs:simpleType>
	  <xs:simpleType name="refs"><xs:list itemType="xs:IDREF"/></xs:simpleType>
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element ref="p" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	  <xs:element name="p">
	    <xs:complexType>
	      <xs:attribute name="id" type="ids"/>
	      <xs:attribute name="ref" type="refs"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<doc><p id="a b"/><p ref="a b"/></doc>`); err != nil {
		t.Errorf("a list of IDs should define each item: %v", err)
	}
	if err := check11(t, s, `<doc><p id="a b"/><p ref="a c"/></doc>`); err == nil {
		t.Error("a list IDREF item matching no ID should fail")
	}
	if err := check11(t, s, `<doc><p id="a b"/><p id="b c"/></doc>`); err == nil {
		t.Error("a list ID item repeated on another element should fail")
	}
}

// TestDefaultedAttributeBindsID covers XSD 1.1 permitting a default on an
// xs:ID or xs:IDREF attribute.
//
// The schema supplies the value to the infoset, so nothing downstream can tell
// it from a written one: it takes part in ID/IDREF binding the same way.
// Skipping absent attributes accepts documents where two elements share a
// defaulted ID, or where a defaulted IDREF points at nothing.
func TestDefaultedAttributeBindsID(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element ref="p" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	  <xs:element name="p">
	    <xs:complexType>
	      <xs:attribute name="id" type="xs:ID" default="d1"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<doc><p/></doc>`); err != nil {
		t.Errorf("a single defaulted ID should be fine: %v", err)
	}
	// Both elements take the default, so both bind d1 — a duplicate that
	// is invisible unless defaults are recorded.
	if err := check11(t, s, `<doc><p/><p/></doc>`); err == nil {
		t.Error("two elements taking the same defaulted ID should collide")
	}
	// One written, one defaulted, colliding.
	if err := check11(t, s, `<doc><p id="d1"/><p/></doc>`); err == nil {
		t.Error("a written ID colliding with a defaulted one should fail")
	}
}

// TestDefaultedIDREFMustResolve pins that a defaulted IDREF is checked against
// the document's IDs like any other reference.
func TestDefaultedIDREFMustResolve(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element ref="p" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	  <xs:element name="p">
	    <xs:complexType>
	      <xs:attribute name="id" type="xs:ID"/>
	      <xs:attribute name="ref" type="xs:IDREF" default="target"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<doc><p id="target"/></doc>`); err != nil {
		t.Errorf("a defaulted IDREF with a matching ID should pass: %v", err)
	}
	if err := check11(t, s, `<doc><p id="other"/></doc>`); err == nil {
		t.Error("a defaulted IDREF matching no ID should fail")
	}
}

// TestIDOwnershipIsVersionDependent pins the difference between the two
// versions' ID binding, which is the whole of the 1.1 relaxation.
//
// XSD 1.0 permits an element at most one ID, so every value is its own binding
// and two sibling elements holding the same ID collide. 1.1 permits several per
// element and binds a value to the element it identifies, so the same two
// siblings are one binding. Applying the 1.1 rule under 1.0 accepts a document
// 1.0 rejects, which is why the owner depends on the version rather than being
// a single rule for both.
func TestIDOwnershipIsVersionDependent(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="v" type="xs:ID" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`
	doc := `<root><v>abc</v><v>abc</v></root>`

	assertInvalid(t, schema, doc, "cvc-id.2")

	s := load11(t, schema)
	if err := check11(t, s, doc); err != nil {
		t.Errorf("XSD 1.1 binds both to the same element, so this is valid: %v", err)
	}
}

// TestAssertionSeesTypedDescendants covers assertions reaching past the
// immediate children.
//
// An assertion reaches as far as any XPath does, and a descendant left untyped
// atomises as xs:untypedAtomic — so "instance of xs:date" is false and a
// comparison raises XPTY0004. Either way the assertion answers something other
// than the question the schema asked.
func TestAssertionSeesTypedDescendants(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="inner">
	    <xs:sequence>
	      <xs:element name="d" type="xs:date"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:element name="temp">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="event" type="inner"/>
	      </xs:sequence>
	      <xs:assert test="data(event/d) instance of xs:date"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<temp><event><d>2001-01-01</d></event></temp>`); err != nil {
		t.Errorf("a grandchild should carry its declared type: %v", err)
	}
}

// TestAssertionTemporalAtomization pins that the date and duration families
// atomise to their own types in an assertion, not to xs:untypedAtomic.
//
// These were absent from the annotation table, so every temporal comparison in
// an assertion was a string comparison — which silently gives the right answer
// for ISO dates and the wrong one for anything with a timezone or a duration.
func TestAssertionTemporalAtomization(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="a" type="xs:date"/>
	        <xs:element name="b" type="xs:date"/>
	      </xs:sequence>
	      <xs:assert test="a castable as xs:date and xs:date(a) lt xs:date(b)"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<e><a>2001-01-01</a><b>2002-01-01</b></e>`); err != nil {
		t.Errorf("dates should compare as dates: %v", err)
	}
	if err := check11(t, s, `<e><a>2002-01-01</a><b>2001-01-01</b></e>`); err == nil {
		t.Error("the ordering assertion should fail when reversed")
	}
}

// TestAssertionDoesNotSeeComments pins that comments and processing
// instructions are invisible to an assertion.
//
// XSD 1.1 builds the tree an assertion sees with them excluded unless the
// processor offers an option to include them, and this one does not. A schema
// writing empty(.//comment()) is asking about the schema-visible content, and
// the suite's assert023 turns on exactly this: the same instance is valid by
// default and invalid only for a processor told to expose them.
func TestAssertionDoesNotSeeComments(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="temp">
	    <xs:complexType>
	      <xs:sequence/>
	      <xs:attribute name="x"/>
	      <xs:assert test="empty(.//comment()) and empty(.//processing-instruction())"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<temp x="1"><!--hidden--></temp>`); err != nil {
		t.Errorf("a comment should not be visible to an assertion: %v", err)
	}
	if err := check11(t, s, `<temp x="1"><?pi go?></temp>`); err != nil {
		t.Errorf("a PI should not be visible to an assertion: %v", err)
	}
}

// TestValueBoundInComplexAssertion covers $value being in scope in every
// assertion, not only those on a simple type.
//
// On a complex type it is the simple content where there is one and the empty
// sequence otherwise, which is what makes empty($value) the way a schema
// asserts "this element has element content, not a value". Leaving it unbound
// raises XPST0008 and turns the assertion into an evaluation failure — a
// different answer from either true or false.
func TestValueBoundInComplexAssertion(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="elem">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="d" type="xs:string"/>
	      </xs:sequence>
	      <xs:assert test="empty($value)"/>
	    </xs:complexType>
	  </xs:element>
	  <xs:element name="simple">
	    <xs:complexType>
	      <xs:simpleContent>
	        <xs:extension base="xs:integer">
	          <xs:attribute name="k"/>
	          <xs:assert test="$value gt 5"/>
	        </xs:extension>
	      </xs:simpleContent>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<elem><d>x</d></elem>`); err != nil {
		t.Errorf("$value should be the empty sequence for element content: %v", err)
	}
	// On simple content $value carries the declared type, so this is a
	// numeric comparison rather than a string one.
	if err := check11(t, s, `<simple k="a">9</simple>`); err != nil {
		t.Errorf("$value should carry the simple content's type: %v", err)
	}
	if err := check11(t, s, `<simple k="a">3</simple>`); err == nil {
		t.Error("the $value assertion should fail for 3")
	}
}

// TestOpenContentExtensionUnions covers §3.4.2.3.3 clause 3: an extension's
// open content is the union of the base's and its own, not a replacement.
//
// An extension may only widen what its base accepts. A derived openContent that
// replaced the base's would let an extension close content the base had opened,
// which is a restriction wearing an extension's spelling.
func TestOpenContentExtensionUnions(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="B">
	    <xs:openContent mode="interleave">
	      <xs:any namespace="urn:one" processContents="skip"/>
	    </xs:openContent>
	    <xs:sequence>
	      <xs:element name="a"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:complexType name="R">
	    <xs:complexContent>
	      <xs:extension base="B">
	        <xs:openContent mode="interleave">
	          <xs:any namespace="urn:two" processContents="skip"/>
	        </xs:openContent>
	        <xs:sequence/>
	      </xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="doc" type="R"/>
	</xs:schema>`)

	// The base's namespace is still open, which is the part a replacement
	// would have lost.
	if err := check11(t, s, `<doc><a/><x xmlns="urn:one"/></doc>`); err != nil {
		t.Errorf("the base's open content should survive extension: %v", err)
	}
	if err := check11(t, s, `<doc><a/><x xmlns="urn:two"/></doc>`); err != nil {
		t.Errorf("the extension's open content should apply: %v", err)
	}
	if err := check11(t, s, `<doc><a/><x xmlns="urn:three"/></doc>`); err == nil {
		t.Error("a namespace neither wildcard admits should still be refused")
	}
}

// TestOpenContentNoneKeepsBase pins that mode="none" on an extension leaves the
// base's open content in force.
//
// It says the type declares none of its own, not that the base's is revoked —
// an extension cannot take away what its base allowed.
func TestOpenContentNoneKeepsBase(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="B">
	    <xs:openContent mode="interleave">
	      <xs:any namespace="urn:one" processContents="skip"/>
	    </xs:openContent>
	    <xs:sequence>
	      <xs:element name="a"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:complexType name="R">
	    <xs:complexContent>
	      <xs:extension base="B">
	        <xs:openContent mode="none"/>
	        <xs:sequence/>
	      </xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="doc" type="R"/>
	</xs:schema>`)

	if err := check11(t, s, `<doc><a/><x xmlns="urn:one"/></doc>`); err != nil {
		t.Errorf("mode=none should not revoke the base's open content: %v", err)
	}
}

// TestOpenContentSuffixIsASuffix pins that suffix mode means the wildcard runs
// to the end of the content, not merely that the model could have stopped.
//
// Admitting a model element after the suffix has begun makes suffix mean
// interleave with extra steps, which is the distinction the mode exists for.
func TestOpenContentSuffixIsASuffix(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:openContent mode="suffix">
	        <xs:any namespace="urn:o" processContents="skip"/>
	      </xs:openContent>
	      <xs:sequence>
	        <xs:element name="a"/>
	        <xs:element name="b" minOccurs="0"/>
	        <xs:element name="c" minOccurs="0"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<doc><a/><b/><c/><x xmlns="urn:o"/></doc>`); err != nil {
		t.Errorf("a genuine suffix should be admitted: %v", err)
	}
	// The model may end after b, but c follows the wildcard, so this is not
	// a suffix.
	if err := check11(t, s, `<doc><a/><b/><x xmlns="urn:o"/><c/></doc>`); err == nil {
		t.Error("a model element after the suffix should be refused")
	}
}

// TestDefaultOpenContentAppliesToEmpty covers appliesToEmpty="false" against a
// type whose content model matches only the empty sequence.
//
// The attribute asks about the content, not how it was spelled: <xs:sequence/>
// is an element-only type admitting nothing but the empty sequence, and testing
// only for the empty content kind misses it — the default open content then
// opens a type the schema said to leave closed.
func TestDefaultOpenContentAppliesToEmpty(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:defaultOpenContent mode="interleave" appliesToEmpty="false">
	    <xs:any namespace="urn:o" processContents="skip"/>
	  </xs:defaultOpenContent>
	  <xs:element name="empty">
	    <xs:complexType><xs:sequence/></xs:complexType>
	  </xs:element>
	  <xs:element name="nonEmpty">
	    <xs:complexType>
	      <xs:sequence><xs:element name="a"/></xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<empty><x xmlns="urn:o"/></empty>`); err == nil {
		t.Error("appliesToEmpty=false should leave an empty model closed")
	}
	if err := check11(t, s, `<nonEmpty><a/><x xmlns="urn:o"/></nonEmpty>`); err != nil {
		t.Errorf("a non-empty model should still be opened: %v", err)
	}
}

// TestAttributeWildcardExtensionUnions covers the attribute wildcard combining
// the way open content does: unioned for an extension, replaced for a
// restriction.
//
// An extension may only widen what its base admits, so taking the base's
// wildcard only when the derived type declared none refused attributes the base
// type accepted.
func TestAttributeWildcardExtensionUnions(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="B">
	    <xs:sequence/>
	    <xs:anyAttribute notNamespace="urn:cain" processContents="lax"/>
	  </xs:complexType>
	  <xs:complexType name="E">
	    <xs:complexContent>
	      <xs:extension base="B">
	        <xs:sequence/>
	        <xs:anyAttribute notNamespace="urn:abel" processContents="lax"/>
	      </xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="eden" type="E"/>
	</xs:schema>`)

	// Two disjoint negations: their union admits everything, including the
	// namespace each one excludes on its own.
	if err := check11(t, s, `<eden xmlns:a="urn:abel" a:x="1"/>`); err != nil {
		t.Errorf("the base wildcard should admit urn:abel: %v", err)
	}
	if err := check11(t, s, `<eden xmlns:c="urn:cain" c:x="1"/>`); err != nil {
		t.Errorf("the extension wildcard should admit urn:cain: %v", err)
	}
}

// TestAllGroupWildcardTakesOverflow pins that a particle whose bound is used up
// does not fail the element when another particle still admits it.
//
// XSD 1.1 permits a wildcard alongside named particles in an all group, and it
// is there precisely to take what the named ones cannot. Stopping at the first
// particle matching by name reports a bound violation for content the group
// accepts.
func TestAllGroupWildcardTakesOverflow(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:all>
	        <xs:element name="a" minOccurs="0"/>
	        <xs:any processContents="skip" minOccurs="0" maxOccurs="unbounded"/>
	      </xs:all>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	// The second <a/> exceeds the named particle's bound, and the wildcard
	// takes it.
	if err := check11(t, s, `<root><a/><a/></root>`); err != nil {
		t.Errorf("the wildcard should absorb the overflow: %v", err)
	}

	// With no wildcard there is nothing to absorb it, so the bound stands.
	s2 := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:all>
	        <xs:element name="a" minOccurs="0"/>
	      </xs:all>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)
	if err := check11(t, s2, `<root><a/><a/></root>`); err == nil {
		t.Error("without a wildcard the bound violation should be reported")
	}
}

// TestIdentityConstraintSkipsSkippedContent pins that a selector does not reach
// into content matched by a processContents="skip" wildcard.
//
// An identity constraint selects nodes out of the PSVI, and skipped content was
// never assessed — it has no schema-normalized values and no type annotations,
// so there is nothing there for a field to select. Reaching into it makes a key
// report a duplicate for an element the schema explicitly said not to look at.
func TestIdentityConstraintSkipsSkippedContent(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:choice maxOccurs="unbounded">
	        <xs:element ref="note"/>
	        <xs:element ref="wrapper"/>
	      </xs:choice>
	    </xs:complexType>
	    <xs:key name="k">
	      <xs:selector xpath=".//note"/>
	      <xs:field xpath="@id"/>
	    </xs:key>
	  </xs:element>
	  <xs:element name="note">
	    <xs:complexType>
	      <xs:sequence/>
	      <xs:attribute name="id" type="xs:string"/>
	    </xs:complexType>
	  </xs:element>
	  <xs:element name="wrapper">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:any processContents="skip" minOccurs="0" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	// The nested note repeats an id, but it is in skipped content, so the
	// key never sees it.
	if err := check11(t, s,
		`<doc><note id="n1"/><wrapper><note id="n1"/></wrapper></doc>`); err != nil {
		t.Errorf("a duplicate in skipped content should be invisible: %v", err)
	}
	// A nested note with no id at all: a key requires every field, but not
	// for a node it does not select.
	if err := check11(t, s,
		`<doc><note id="n1"/><wrapper><note/></wrapper></doc>`); err != nil {
		t.Errorf("a missing field in skipped content should be invisible: %v", err)
	}
	// Outside the skipped content the key still applies.
	if err := check11(t, s, `<doc><note id="n1"/><note id="n1"/></doc>`); err == nil {
		t.Error("a duplicate outside skipped content should still be caught")
	}
}

// TestTemporalEnumerationComparesByValue pins that the enumeration facet
// compares dates and durations by value rather than by spelling.
//
// 2010-09-19T24:00:00Z and 2010-09-20T00:00:00Z denote the same instant, so an
// enumeration listing either admits both. Comparing lexical forms rejects
// documents the spec accepts.
func TestTemporalEnumerationComparesByValue(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:simpleType>
	      <xs:restriction base="xs:dateTime">
	        <xs:enumeration value="2010-09-20T00:00:00Z"/>
	        <xs:enumeration value="2010-09-20T12:00:00Z"/>
	      </xs:restriction>
	    </xs:simpleType>
	  </xs:element>
	</xs:schema>`)

	// 24:00:00 on the 19th is 00:00:00 on the 20th.
	if err := check11(t, s, `<doc>2010-09-19T24:00:00Z</doc>`); err != nil {
		t.Errorf("24:00 should equal the next day's 00:00: %v", err)
	}
	// The same instant written in another timezone.
	if err := check11(t, s, `<doc>2010-09-20T13:00:00.000+01:00</doc>`); err != nil {
		t.Errorf("an equal instant in another timezone should match: %v", err)
	}
	if err := check11(t, s, `<doc>2010-09-20T06:00:00Z</doc>`); err == nil {
		t.Error("a different instant should not match")
	}
}

// TestIdentityConstraintComparesTemporalByValue pins the same rule for key and
// keyref, where it decides whether a reference resolves.
//
// A key sequence compares values, not spellings, so a keyref written
// 05:00:00+05:00 has to find a key written 00:00:00Z — the same time.
func TestIdentityConstraintComparesTemporalByValue(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="target" type="xs:time"/>
	        <xs:element name="equiv" type="xs:time" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	    <xs:key name="t">
	      <xs:selector xpath="target"/>
	      <xs:field xpath="."/>
	    </xs:key>
	    <xs:keyref name="r" refer="t">
	      <xs:selector xpath="equiv"/>
	      <xs:field xpath="."/>
	    </xs:keyref>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s,
		`<doc><target>00:00:00Z</target><equiv>05:00:00+05:00</equiv></doc>`); err != nil {
		t.Errorf("an equal time in another timezone should resolve: %v", err)
	}
	if err := check11(t, s,
		`<doc><target>00:00:00Z</target><equiv>06:00:00Z</equiv></doc>`); err == nil {
		t.Error("a different time should not resolve")
	}
}

// TestDurationKeyComparesByValue covers the duration family, where equality is
// months and seconds agreeing rather than the components being written the same
// way.
//
// PT29H and P1DT5H are one duration; P1M and P30D are not, and are not even
// comparable — which is why an incomparable pair must not be treated as equal.
func TestDurationKeyComparesByValue(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="target" type="xs:duration"/>
	        <xs:element name="equiv" type="xs:duration" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	    <xs:key name="t">
	      <xs:selector xpath="target"/>
	      <xs:field xpath="."/>
	    </xs:key>
	    <xs:keyref name="r" refer="t">
	      <xs:selector xpath="equiv"/>
	      <xs:field xpath="."/>
	    </xs:keyref>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s,
		`<doc><target>P1DT5H</target><equiv>PT29H</equiv></doc>`); err != nil {
		t.Errorf("PT29H should equal P1DT5H: %v", err)
	}
	// A month is not thirty days: the two are incomparable, so the keyref
	// does not resolve.
	if err := check11(t, s,
		`<doc><target>P1M</target><equiv>P30D</equiv></doc>`); err == nil {
		t.Error("P30D should not resolve against P1M")
	}
}

// TestWildcardIntersectionUnionsDisallowedNames pins that intersecting two
// attribute wildcards unions their {disallowed names}.
//
// An attribute has to be admitted by both operands, so either one refusing a
// name is enough to refuse it. That is the opposite of the union case, where a
// name survives only if both refuse it, and getting it backwards admits every
// name only one side disallowed.
func TestWildcardIntersectionUnionsDisallowedNames(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:attributeGroup name="a">
	    <xs:anyAttribute namespace="##local" processContents="skip" notQName="a b c"/>
	  </xs:attributeGroup>
	  <xs:attributeGroup name="b">
	    <xs:anyAttribute namespace="##local" processContents="skip" notQName="c d e"/>
	  </xs:attributeGroup>
	  <xs:element name="computer">
	    <xs:complexType>
	      <xs:sequence/>
	      <xs:attributeGroup ref="a"/>
	      <xs:attributeGroup ref="b"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<computer z="1"/>`); err != nil {
		t.Errorf("a name neither group disallows should be admitted: %v", err)
	}
	// Disallowed by only one side each — the intersection refuses both.
	if err := check11(t, s, `<computer a="1"/>`); err == nil {
		t.Error("a name the first group disallows should be refused")
	}
	if err := check11(t, s, `<computer d="1"/>`); err == nil {
		t.Error("a name the second group disallows should be refused")
	}
	if err := check11(t, s, `<computer c="1"/>`); err == nil {
		t.Error("a name both groups disallow should be refused")
	}
}

// TestDynamicElementDeclarationsConsistent covers cvc-complex-type.2.4.k: two
// elements of one name in one content model may not be matched with different
// types.
//
// Element Declarations Consistent is mostly a schema-time rule, but a wildcard
// can only break it at validation time — the schema cannot know which global
// declaration a lax wildcard will pick up. The type has to come from the
// position that actually matched: scanning the model's positions finds the
// local declaration for both children and sees no conflict.
func TestDynamicElementDeclarationsConsistent(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="zing">
	    <xs:sequence>
	      <xs:element name="e" type="xs:date"/>
	      <xs:element name="f" type="xs:string"/>
	      <xs:any namespace="##local" processContents="lax"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:element name="doc" type="zing"/>
	  <xs:element name="e" type="xs:time"/>
	</xs:schema>`

	s := load11(t, schema)
	// The first <e> takes the local declaration (xs:date), the second the
	// global one through the wildcard (xs:time). One name, two types.
	if err := check11(t, s,
		`<doc><e>2008-11-03</e><f/><e>12:20:02</e></doc>`); err == nil {
		t.Error("two types for one name in a content model should be refused")
	}
	// A different name through the wildcard is no conflict.
	if err := check11(t, s,
		`<doc><e>2008-11-03</e><f/><g>anything</g></doc>`); err != nil {
		t.Errorf("a distinct name through the wildcard is fine: %v", err)
	}

	// XSD 1.0 has no dynamic half to the rule, and accepts it.
	assertValid(t, schema, `<doc><e>2008-11-03</e><f/><e>12:20:02</e></doc>`)
}

// TestPlusINFIsAFloat covers the XSD 1.1 addition of a leading plus to the
// lexical space of xs:float and xs:double. XSD 1.0 admitted only "INF".
func TestPlusINFIsAFloat(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="n" type="xs:float"/>
	</xs:schema>`)

	for _, v := range []string{"+INF", "INF", "-INF", "NaN"} {
		if err := check11(t, s, `<n>`+v+`</n>`); err != nil {
			t.Errorf("%q should be a valid xs:float: %v", v, err)
		}
	}
	if err := check11(t, s, `<n>++INF</n>`); err == nil {
		t.Error("++INF should not be a valid xs:float")
	}
}

// TestDynamicEDTAllowsDerivedTypes pins that the dynamic Element Declarations
// Consistent check compares by derivation, not by identity.
//
// A global <e type="xs:positiveInteger"/> reached through a wildcard does not
// conflict with a local <e type="xs:integer"/>: everything the global admits
// the local admits too. Union membership counts for the same reason.
func TestDynamicEDTAllowsDerivedTypes(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="zing">
	    <xs:sequence>
	      <xs:element name="e" type="xs:integer"/>
	      <xs:element name="f" type="xs:integer"/>
	      <xs:any namespace="##local" processContents="lax"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:element name="doc" type="zing"/>
	  <xs:element name="e" type="xs:positiveInteger"/>
	</xs:schema>`)

	if err := check11(t, s, `<doc><e>1</e><f>2</f><e>3</e></doc>`); err != nil {
		t.Errorf("a derived type should not conflict: %v", err)
	}
}

// TestAssertionTypesAnonymousSimpleTypes covers an element whose type is an
// anonymous restriction.
//
// The annotation was taken from the type's own name, which an anonymous type
// does not have, so the node stayed untyped: "even-number lt 500" against a
// restriction of xs:int compared a string with an integer and raised XPTY0004,
// when the schema plainly gave the element a numeric type.
func TestAssertionTypesAnonymousSimpleTypes(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="Example">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="n">
	          <xs:simpleType>
	            <xs:restriction base="xs:int">
	              <xs:minInclusive value="0"/>
	            </xs:restriction>
	          </xs:simpleType>
	        </xs:element>
	      </xs:sequence>
	      <xs:assert test="n lt 500"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<Example><n>42</n></Example>`); err != nil {
		t.Errorf("an anonymous restriction should still atomise numerically: %v", err)
	}
	if err := check11(t, s, `<Example><n>900</n></Example>`); err == nil {
		t.Error("the assertion should fail for 900")
	}
}

// TestListValueBindsAsSequence pins that $value for a list type is a sequence
// with one item per list item, not one item holding the whole literal.
//
// Given the literal as a single item, data($value) yields "2 4 6 8 10" and the
// arithmetic raises FORG0001 rather than running over five numbers.
func TestListValueBindsAsSequence(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="ints"><xs:list itemType="xs:integer"/></xs:simpleType>
	  <xs:element name="list">
	    <xs:complexType>
	      <xs:simpleContent>
	        <xs:extension base="ints">
	          <xs:assert test="count($value) le 5"/>
	          <xs:assert test="every $x in data($value) satisfies ($x mod 2 = 0)"/>
	        </xs:extension>
	      </xs:simpleContent>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<list>2 4 6 8 10</list>`); err != nil {
		t.Errorf("an all-even list should satisfy the assertion: %v", err)
	}
	if err := check11(t, s, `<list>2 4 5</list>`); err == nil {
		t.Error("a list with an odd item should fail the assertion")
	}
	// count($value) is the number of list items, which only holds if the
	// binding is a sequence rather than one item.
	if err := check11(t, s, `<list>2 4 6 8 10 12</list>`); err == nil {
		t.Error("six items should exceed count($value) le 5")
	}
}

// TestDeclaredXSIAttributeUse covers a type declaring one of the four xsi:
// attributes, which is how XSD 1.1 makes xsi:type mandatory.
//
// The xsi: attributes are permitted on any element and are not subject to a
// type's attribute uses, so they were skipped outright — which left a declared
// use unmatched, and a type requiring xsi:type reported it missing even when
// the instance carried it.
func TestDeclaredXSIAttributeUse(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
	  <xs:complexType name="B">
	    <xs:sequence/>
	    <xs:attribute ref="xsi:type" use="required"/>
	  </xs:complexType>
	  <xs:complexType name="R">
	    <xs:complexContent>
	      <xs:restriction base="B">
	        <xs:sequence/>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="root" type="B"/>
	</xs:schema>`)

	if err := check11(t, s,
		`<root xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="R"/>`); err != nil {
		t.Errorf("a declared xsi:type use should be satisfied by the attribute: %v", err)
	}
	if err := check11(t, s, `<root/>`); err == nil {
		t.Error("a required xsi:type should still be required when absent")
	}
}

// TestLocalTargetNamespace covers XSD 1.1's targetNamespace on a local
// declaration.
//
// It is how a schema declares a component belonging somewhere other than its
// own target namespace without importing one, and it overrides form and
// elementFormDefault — both of which only choose between the target namespace
// and none, so neither can express this.
func TestLocalTargetNamespace(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:simpleContent>
	      <xs:extension base="xs:string">
	        <xs:anyAttribute/>
	      </xs:extension>
	    </xs:simpleContent>
	  </xs:complexType>
	  <xs:element name="x">
	    <xs:complexType>
	      <xs:simpleContent>
	        <xs:restriction base="base">
	          <xs:attribute name="a" type="xs:integer" targetNamespace="http://test1"/>
	        </xs:restriction>
	      </xs:simpleContent>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s,
		`<x xmlns:t="http://test1" t:a="100">Hello</x>`); err != nil {
		t.Errorf("the local attribute should be in the named namespace: %v", err)
	}
	// The declared type applies, so a non-integer fails.
	if err := check11(t, s,
		`<x xmlns:t="http://test1" t:a="oops">Hello</x>`); err == nil {
		t.Error("the local declaration's type should be enforced")
	}
}

// TestOpenContentOnEmptyType covers appliesToEmpty="true", which exists
// precisely to open a type with no content model at all.
//
// Refusing children before consulting the wildcard makes the attribute do
// nothing, since an empty type is the only kind it applies to.
func TestOpenContentOnEmptyType(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:defaultOpenContent mode="interleave" appliesToEmpty="true">
	    <xs:any namespace="urn:o" processContents="skip"/>
	  </xs:defaultOpenContent>
	  <xs:complexType name="c"/>
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="p" type="c"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s,
		`<root><p><x xmlns="urn:o"/></p></root>`); err != nil {
		t.Errorf("appliesToEmpty should open an empty type: %v", err)
	}
	// The wildcard still bounds what may appear.
	if err := check11(t, s, `<root><p><x/></p></root>`); err == nil {
		t.Error("a child the wildcard does not admit should be refused")
	}
}

// TestTimeWrapsAtMidnight pins that xs:time's value space is a time of day
// rather than a point on a timeline.
//
// 24:00:00 and 00:00:00 name the same time, so two spellings of one value must
// compare equal — which a key sequence and an enumeration both notice. This is
// not true of xs:dateTime, where 24:00:00 means midnight starting the next day
// and the date carries it.
func TestTimeWrapsAtMidnight(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="target" type="xs:time"/>
	        <xs:element name="equiv" type="xs:time" maxOccurs="unbounded"/>
	      </xs:sequence>
	    </xs:complexType>
	    <xs:key name="t">
	      <xs:selector xpath="target"/><xs:field xpath="."/>
	    </xs:key>
	    <xs:keyref name="r" refer="t">
	      <xs:selector xpath="equiv"/><xs:field xpath="."/>
	    </xs:keyref>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<doc><target>00:00:00Z</target>`+
		`<equiv>05:00:00+05:00</equiv><equiv>24:00:00Z</equiv>`+
		`<equiv>24:00:00+00:00</equiv></doc>`); err != nil {
		t.Errorf("every spelling of midnight should resolve: %v", err)
	}
	if err := check11(t, s,
		`<doc><target>00:00:00Z</target><equiv>12:00:00Z</equiv></doc>`); err == nil {
		t.Error("a different time should not resolve")
	}
}

// TestAssertionsAccumulateOnDerivation covers §3.4.2.4: a derived type has to
// satisfy its base's assertions as well as its own.
//
// Without this a restriction could widen what its base accepted just by
// declaring an assertion of its own, which is the opposite of restricting.
func TestAssertionsAccumulateOnDerivation(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:simpleContent>
	      <xs:extension base="xs:integer">
	        <xs:assert test=". = (1 to 10, 20, 30)"/>
	      </xs:extension>
	    </xs:simpleContent>
	  </xs:complexType>
	  <xs:complexType name="derived">
	    <xs:simpleContent>
	      <xs:restriction base="base">
	        <xs:assert test=". mod 2 = 0"/>
	      </xs:restriction>
	    </xs:simpleContent>
	  </xs:complexType>
	  <xs:element name="e" type="derived"/>
	</xs:schema>`)

	if err := check11(t, s, `<e>4</e>`); err != nil {
		t.Errorf("4 satisfies both assertions: %v", err)
	}
	// Satisfies the derived assertion but not the base's.
	if err := check11(t, s, `<e>80</e>`); err == nil {
		t.Error("80 should fail the base's assertion")
	}
	// Satisfies the base's but not the derived one.
	if err := check11(t, s, `<e>7</e>`); err == nil {
		t.Error("7 should fail the derived assertion")
	}
}

// TestAllGroupSuffixIsASuffix pins that suffix mode is enforced inside an all
// group too: the members may come in any order, but a suffix is still at the
// end.
func TestAllGroupSuffixIsASuffix(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:defaultOpenContent mode="suffix" appliesToEmpty="false">
	    <xs:any namespace="urn:o" processContents="skip"/>
	  </xs:defaultOpenContent>
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:all>
	        <xs:element name="a" maxOccurs="2"/>
	        <xs:element name="b" minOccurs="0"/>
	      </xs:all>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<doc><b/><a/><x xmlns="urn:o"/></doc>`); err != nil {
		t.Errorf("a genuine suffix should be admitted: %v", err)
	}
	if err := check11(t, s, `<doc><b/><x xmlns="urn:o"/><a/></doc>`); err == nil {
		t.Error("a group member after the suffix should be refused")
	}
}

// TestMinOccursCheckedForEveryCounter covers a counter bug that affected both
// versions.
//
// Completeness checked only the counters enclosing the *final* position. In
// <a minOccurs="5"/><b minOccurs="0"/> the last position is b, which is in none
// of a's scopes, so a's minimum was never looked at and four <a/> passed.
func TestMinOccursCheckedForEveryCounter(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="a" minOccurs="5" maxOccurs="10"/>
	        <xs:element name="b" minOccurs="0"/>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	assertValid(t, schema, `<doc><a/><a/><a/><a/><a/><b/></doc>`)
	assertInvalid(t, schema, `<doc><a/><a/><a/><a/><b/></doc>`,
		"cvc-complex-type.2.4.b")
	// The trailing optional element is what hid it; without one the final
	// position was inside a's counter and the check worked.
	assertInvalid(t, schema, `<doc><a/><a/><a/><a/></doc>`,
		"cvc-complex-type.2.4.b")
}

// TestConditionalInclusion covers XSD 1.1's versioning attributes (§4.2.1).
//
// An element the conditions exclude is treated as though it were not written,
// so one schema document can carry both a 1.0 and a 1.1 spelling of the same
// declaration and each processor reads the one it understands.
func TestConditionalInclusion(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           xmlns:vc="http://www.w3.org/2007/XMLSchema-versioning">
	  <xs:element name="temp">
	    <xs:complexType>
	      <xs:sequence/>
	      <xs:attribute name="x" use="required"/>
	      <xs:assert test="@x > 300" vc:minVersion="1.1"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	// 1.1 sees the assertion.
	s := load11(t, schema)
	if err := check11(t, s, `<temp x="400"/>`); err != nil {
		t.Errorf("400 satisfies the assertion: %v", err)
	}
	if err := check11(t, s, `<temp x="200"/>`); err == nil {
		t.Error("200 should fail the assertion under 1.1")
	}
	// 1.0 never sees it, so the same document is valid.
	assertValid(t, schema, `<temp x="200"/>`)
}

// TestConditionalInclusionAvailability pins the three-way availability rule
// that typeAvailable and typeUnavailable share.
//
// Each keeps the element only when every name in its list is definitely
// available (or definitely unavailable, respectively). A name this processor
// cannot resolve at all leaves the condition undecided, and an undecided
// condition keeps the element — which is the only reading under which the
// suite's vc011 and vc013 can both hold, since they use the same instance and
// expect opposite answers from lists differing only by an unresolvable name.
func TestConditionalInclusionAvailability(t *testing.T) {
	build := func(cond string) string {
		return `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:vc="http://www.w3.org/2007/XMLSchema-versioning">
		  <xs:element name="temp">
		    <xs:complexType>
		      <xs:sequence/>
		      <xs:attribute name="x" ` + cond + `/>
		    </xs:complexType>
		  </xs:element>
		</xs:schema>`
	}
	doc := `<temp x="204"/>`

	// A known type is available, so typeAvailable keeps the attribute.
	if err := check11(t, load11(t, build(`vc:typeAvailable="xs:integer"`)), doc); err != nil {
		t.Errorf("a known type should keep the attribute: %v", err)
	}
	// One unresolvable name in the list makes it undecided, so the
	// attribute is dropped and x is undeclared.
	if err := check11(t, load11(t,
		build(`vc:typeAvailable="xs:integer vc:bananaSkin"`)), doc); err == nil {
		t.Error("an unresolvable name should leave typeAvailable unsatisfied")
	}
	// typeUnavailable naming only known types drops the attribute.
	if err := check11(t, load11(t, build(`vc:typeUnavailable="xs:integer"`)), doc); err == nil {
		t.Error("typeUnavailable naming an available type should drop the attribute")
	}
	// Mixing in an unresolvable name leaves it undecided, so it stays.
	if err := check11(t, load11(t,
		build(`vc:typeUnavailable="vc:list-of-QNames xs:integer"`)), doc); err != nil {
		t.Errorf("an unresolvable name should keep the attribute: %v", err)
	}
}

// TestIdentityConstraintXPathDefaultNamespace covers XSD 1.1's
// xpathDefaultNamespace, which is how a schema writes a selector over qualified
// elements without inventing a prefix for them.
func TestIdentityConstraintXPathDefaultNamespace(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           targetNamespace="urn:d" xmlns:d="urn:d"
	           elementFormDefault="qualified">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="emp" maxOccurs="unbounded">
	          <xs:complexType>
	            <xs:sequence><xs:element name="nr" type="xs:string"/></xs:sequence>
	          </xs:complexType>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	    <xs:unique name="u">
	      <xs:selector xpath="emp" xpathDefaultNamespace="urn:d"/>
	      <xs:field xpath="nr" xpathDefaultNamespace="urn:d"/>
	    </xs:unique>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s,
		`<doc xmlns="urn:d"><emp><nr>1</nr></emp><emp><nr>2</nr></emp></doc>`); err != nil {
		t.Errorf("distinct values should satisfy the constraint: %v", err)
	}
	// Without the default namespace the selector would match nothing and
	// the duplicate would go unnoticed.
	if err := check11(t, s,
		`<doc xmlns="urn:d"><emp><nr>1</nr></emp><emp><nr>1</nr></emp></doc>`); err == nil {
		t.Error("a duplicate should be caught through xpathDefaultNamespace")
	}
}

// TestDateLexicalSpaceChecksRanges pins that a date's components must name a
// date that occurs, not merely three well-formed numbers.
//
// 2001-02-30 is the everyday case; -0003-02-29 is the same trap under the
// proleptic Gregorian leap rule, where the year is astronomical — 0 is 1 BCE,
// so year 0 is a leap year and year -3 is not.
func TestDateLexicalSpaceChecksRanges(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="d" type="xs:date"/>
	</xs:schema>`

	for _, v := range []string{"2001-02-28", "2000-02-29", "0000-02-29", "-0004-02-29"} {
		assertValid(t, schema, `<d>`+v+`</d>`)
	}
	for _, v := range []string{
		"2001-02-30", "2001-13-01", "2001-00-01", "2001-01-00",
		"1900-02-29", // divisible by 100 but not 400
		"-0003-02-29",
	} {
		assertInvalid(t, schema, `<d>`+v+`</d>`, "cvc-datatype-valid")
	}
}

// TestTimeLexicalSpaceChecksRanges pins the hour-24 rule: 24:00:00 names the
// end of a day and is the only hour-24 form the lexical space admits.
func TestTimeLexicalSpaceChecksRanges(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="t" type="xs:time"/>
	</xs:schema>`

	for _, v := range []string{"00:00:00", "23:59:59", "24:00:00", "24:00:00.0"} {
		assertValid(t, schema, `<t>`+v+`</t>`)
	}
	for _, v := range []string{"25:00:00", "24:00:01", "24:01:00", "00:60:00",
		"00:00:60", "24:00:00.5"} {
		assertInvalid(t, schema, `<t>`+v+`</t>`, "cvc-datatype-valid")
	}
}

// TestGregorianLexicalRanges covers the partial calendar types, whose
// components are bounded even though they carry no full date.
//
// gMonthDay has no year, so February is given 29 days: --02-29 is a date that
// occurs, just not every year.
func TestGregorianLexicalRanges(t *testing.T) {
	for _, c := range []struct{ typ, good, bad string }{
		{"gYearMonth", "2001-12", "2001-13"},
		{"gMonth", "--12", "--13"},
		{"gDay", "---31", "---32"},
		{"gMonthDay", "--02-29", "--02-30"},
	} {
		schema := `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="v" type="xs:` + c.typ + `"/>
		</xs:schema>`
		assertValid(t, schema, `<v>`+c.good+`</v>`)
		assertInvalid(t, schema, `<v>`+c.bad+`</v>`, "cvc-datatype-valid")
	}
}

// TestNotNamespaceTargetInNoNamespaceSchema pins that ##targetNamespace names
// the absent namespace when the schema has no target namespace.
//
// Appending the empty target namespace to the excluded list does not do it:
// Allows answers the absent namespace from ExcludesAbsent and never reaches the
// list, so the wildcard admitted every unqualified attribute it was written to
// refuse.
func TestNotNamespaceTargetInNoNamespaceSchema(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="eden">
	    <xs:complexType>
	      <xs:sequence/>
	      <xs:anyAttribute notNamespace=" ##targetNamespace " processContents="skip"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	if err := check11(t, s, `<eden xmlns:n="urn:n" n:x="1"/>`); err != nil {
		t.Errorf("a qualified attribute should be admitted: %v", err)
	}
	if err := check11(t, s, `<eden cain="abel"/>`); err == nil {
		t.Error("an unqualified attribute is in the target namespace here")
	}
}

// TestDefinedSiblingInOpenContent covers ##definedSibling on an open content
// wildcard, which is not a particle in any content model.
//
// Its siblings are that model's element names: open content is written to
// admit what the model does not already name, and ##definedSibling is how a
// schema says so without listing them. The set has to be resolved per type,
// since one defaultOpenContent is shared by every type in the document that
// declares none of its own.
func TestDefinedSiblingInOpenContent(t *testing.T) {
	s := load11(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:defaultOpenContent mode="interleave">
	    <xs:any processContents="skip" notQName="##definedSibling"/>
	  </xs:defaultOpenContent>
	  <xs:element name="root" type="zing"/>
	  <xs:complexType name="zing">
	    <xs:sequence>
	      <xs:element name="b" type="xs:string" minOccurs="0"/>
	      <xs:element name="c" type="xs:string" minOccurs="0"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`)

	// A name the model does not declare goes through the wildcard.
	if err := check11(t, s, `<root><b/><d/></root>`); err != nil {
		t.Errorf("an undeclared name should pass the wildcard: %v", err)
	}
	// A second <c/> is a sibling name, so the wildcard refuses it and the
	// model has already used its one occurrence.
	if err := check11(t, s, `<root><c/><b/><c/></root>`); err == nil {
		t.Error("a sibling name should be refused by the wildcard")
	}
}

// TestStringSubtypeLexicalSpaces covers the xs:string branch's named subtypes,
// which narrow the lexical space rather than the value space.
//
// Part 2 defines them by pattern but states those patterns in prose rather than
// as facets on the type, so nothing a schema inherits from xs:ID would reject a
// value that is not an NCName — "87123_" starts with a digit and was accepted.
func TestStringSubtypeLexicalSpaces(t *testing.T) {
	for _, c := range []struct {
		typ  string
		good []string
		bad  []string
	}{
		{"NCName", []string{"a", "_x", "a-b.c1"}, []string{"87123_", "a:b", "-x", ""}},
		{"ID", []string{"_d8732d"}, []string{"87123_", "a:b"}},
		{"Name", []string{"a:b", "_x", ":x"}, []string{"1abc", "-x"}},
		{"NMTOKEN", []string{"1abc", "a:b", "-x"}, []string{"a b"}},
		{"language", []string{"en", "en-GB", "en-us-x1"}, []string{"1en", "toolongsubtag", "en-"}},
	} {
		schema := `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="v" type="xs:` + c.typ + `"/>
		</xs:schema>`
		for _, v := range c.good {
			assertValid(t, schema, `<v>`+v+`</v>`)
		}
		for _, v := range c.bad {
			assertInvalid(t, schema, `<v>`+v+`</v>`, "cvc")
		}
	}
}

// TestStringSubtypeThroughRestriction pins that the check walks to the nearest
// built-in ancestor: a user-defined restriction of xs:ID is still an xs:ID.
func TestStringSubtypeThroughRestriction(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="myID">
	    <xs:restriction base="xs:ID">
	      <xs:maxLength value="10"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:element name="v" type="myID"/>
	</xs:schema>`

	assertValid(t, schema, `<v>_ok</v>`)
	assertInvalid(t, schema, `<v>9bad</v>`, "cvc")
}

// TestEffectivelyEmptyContentRejectsWhitespace pins that a content model
// matching nothing but the empty sequence admits no character data, whitespace
// included.
//
// The indentation exception belongs to element-only content, where there are
// elements for the whitespace to sit between. <xs:sequence/> has none, so it is
// empty content in every sense — the suite's open012.n3 is annotated "invalid,
// even whitespace is not allowed".
func TestEffectivelyEmptyContentRejectsWhitespace(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType><xs:sequence/></xs:complexType>
	  </xs:element>
	</xs:schema>`

	assertValid(t, schema, `<doc/>`)
	assertValid(t, schema, `<doc></doc>`)
	assertInvalid(t, schema, "<doc>\n  \n</doc>", "cvc-complex-type.2.1")
	assertInvalid(t, schema, `<doc>x</doc>`, "cvc-complex-type.2.1")

	// A model with something in it keeps the indentation exception.
	withContent := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:sequence><xs:element name="a"/></xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`
	assertValid(t, withContent, "<doc>\n  <a/>\n</doc>")
}
