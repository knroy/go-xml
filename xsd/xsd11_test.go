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
