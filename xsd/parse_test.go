package xsd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// parseSchemaString is the test helper: parse a schema document from source.
func parseSchemaString(t *testing.T, src string) (*Schema, error) {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test schema as XML: %v", err)
	}
	return ParseSchema(tree.Root)
}

func mustParseSchema(t *testing.T, src string) *Schema {
	t.Helper()
	s, err := parseSchemaString(t, src)
	if err != nil {
		t.Fatalf("ParseSchema: %v", err)
	}
	return s
}

func TestParseGlobalElement(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           targetNamespace="urn:t" xmlns:t="urn:t">
	  <xs:element name="root" type="xs:string"/>
	</xs:schema>`)

	d := s.Elements[xdm.QName{URI: "urn:t", Local: "root"}]
	if d == nil {
		t.Fatal("root was not declared")
	}
	if d.Scope != ScopeGlobal {
		t.Error("a top-level element declaration should be global")
	}
	st, ok := d.Type.(*SimpleType)
	if !ok || st.Name.Local != "string" {
		t.Errorf("type is %v, want xs:string", d.Type)
	}
}

// TestLocalElementFormDefault covers the rule that decides whether a local
// element is in the target namespace.
//
// An unqualified local element is in the *absent* namespace whatever the
// document's target namespace is. Getting this backwards makes every local
// element unmatchable against an instance, and it is silent: the schema parses
// and simply never matches.
func TestLocalElementFormDefault(t *testing.T) {
	unqualified := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence><xs:element name="child" type="xs:string"/></xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	child := firstLocalElement(t, unqualified, "root")
	if child.Name.URI != "" {
		t.Errorf("with elementFormDefault unqualified, the local element is "+
			"in %q, want the absent namespace", child.Name.URI)
	}

	qualified := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"
	           elementFormDefault="qualified">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence><xs:element name="child" type="xs:string"/></xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)

	child = firstLocalElement(t, qualified, "root")
	if child.Name.URI != "urn:t" {
		t.Errorf("with elementFormDefault qualified, the local element is "+
			"in %q, want urn:t", child.Name.URI)
	}
}

func firstLocalElement(t *testing.T, s *Schema, root string) *ElementDecl {
	t.Helper()
	for name, d := range s.Elements {
		if name.Local != root {
			continue
		}
		ct, ok := d.Type.(*ComplexType)
		if !ok || ct.Particle == nil {
			t.Fatalf("%s has no content model", root)
		}
		g, ok := ct.Particle.Term.(*ModelGroup)
		if !ok || len(g.Particles) == 0 {
			t.Fatalf("%s's content model is not a group", root)
		}
		ed, ok := g.Particles[0].Term.(*ElementDecl)
		if !ok {
			t.Fatalf("the first particle is not an element declaration")
		}
		return ed
	}
	t.Fatalf("%s was not declared", root)
	return nil
}

func TestParseOccurs(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:sequence>
	      <xs:element name="a" type="xs:string" minOccurs="0"/>
	      <xs:element name="b" type="xs:string" maxOccurs="unbounded"/>
	      <xs:element name="c" type="xs:string" minOccurs="2" maxOccurs="5"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`)

	ct := s.Types[xdm.QName{Local: "t"}].(*ComplexType)
	g := ct.Particle.Term.(*ModelGroup)
	want := []struct{ min, max int }{{0, 1}, {1, Unbounded}, {2, 5}}
	for i, w := range want {
		got := g.Particles[i]
		if got.MinOccurs != w.min || got.MaxOccurs != w.max {
			t.Errorf("particle %d is (%d, %d), want (%d, %d)",
				i, got.MinOccurs, got.MaxOccurs, w.min, w.max)
		}
	}
}

func TestParseOccursRejectsInverted(t *testing.T) {
	_, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:sequence>
	      <xs:element name="a" type="xs:string" minOccurs="3" maxOccurs="1"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`)
	if err == nil {
		t.Fatal("minOccurs greater than maxOccurs should be rejected")
	}
	if !strings.Contains(err.Error(), "p-props-correct") {
		t.Errorf("error %q does not cite p-props-correct", err)
	}
}

func TestParseSimpleTypeRestriction(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="small">
	    <xs:restriction base="xs:int">
	      <xs:minInclusive value="1"/>
	      <xs:maxInclusive value="10"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`)

	st := s.Types[xdm.QName{Local: "small"}].(*SimpleType)
	if st.Variety != VarietyAtomic {
		t.Errorf("variety is %s, want atomic", st.Variety)
	}
	if st.Facets.MinInclusive == nil || *st.Facets.MinInclusive != "1" {
		t.Error("minInclusive was not read")
	}
	// The primitive is inherited through xs:int's chain up to xs:decimal.
	if st.Primitive == nil || st.Primitive.Name.Local != "decimal" {
		t.Errorf("primitive is %v, want xs:decimal", st.Primitive)
	}
}

// TestParseUnionKeepsMemberOrder guards the rule that a union takes the first
// member accepting a value, not the best match. Order must survive parsing,
// including when members are named by QName and resolved later.
func TestParseUnionKeepsMemberOrder(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="u">
	    <xs:union memberTypes="xs:int xs:string xs:boolean"/>
	  </xs:simpleType>
	</xs:schema>`)

	st := s.Types[xdm.QName{Local: "u"}].(*SimpleType)
	if st.Variety != VarietyUnion {
		t.Fatalf("variety is %s, want union", st.Variety)
	}
	want := []string{"int", "string", "boolean"}
	if len(st.MemberTypes) != len(want) {
		t.Fatalf("got %d members, want %d", len(st.MemberTypes), len(want))
	}
	for i, w := range want {
		if st.MemberTypes[i] == nil {
			t.Fatalf("member %d was not resolved", i)
		}
		if got := st.MemberTypes[i].Name.Local; got != w {
			t.Errorf("member %d is xs:%s, want xs:%s", i, got, w)
		}
	}
}

func TestParseListType(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="ints"><xs:list itemType="xs:int"/></xs:simpleType>
	</xs:schema>`)

	st := s.Types[xdm.QName{Local: "ints"}].(*SimpleType)
	if st.Variety != VarietyList {
		t.Fatalf("variety is %s, want list", st.Variety)
	}
	if st.ItemType == nil || st.ItemType.Name.Local != "int" {
		t.Errorf("item type is %v, want xs:int", st.ItemType)
	}
	// A list must collapse whitespace: the items are whitespace-separated,
	// so preserving it would make the separator ambiguous.
	if st.Facets.WhiteSpace == nil || *st.Facets.WhiteSpace != WhiteCollapse {
		t.Error("a list type should fix whiteSpace at collapse")
	}
}

func TestParseForwardReference(t *testing.T) {
	// The type is referenced before it is declared. Forward references are
	// idiomatic in schemas, so this must work rather than merely not crash.
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root" type="later"/>
	  <xs:simpleType name="later">
	    <xs:restriction base="xs:string"/>
	  </xs:simpleType>
	</xs:schema>`)

	d := s.Elements[xdm.QName{Local: "root"}]
	if d == nil || d.Type == nil {
		t.Fatal("the forward reference was not resolved")
	}
	if d.Type.TypeName().Local != "later" {
		t.Errorf("resolved to %v", d.Type.TypeName())
	}
}

// An element declaration naming a type that does not exist is an error only
// where the declaration is used. The suite says so in as many words —
// missing001 is "Error only if the element declaration is needed for
// validation" — and expects the schema itself to load, so that every other
// declaration in it still works.
func TestParseUnresolvedReference(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="good" type="xs:integer"/>
	  <xs:element name="bad" type="missing"/>
	</xs:schema>`
	if _, err := parseSchemaString(t, schema); err != nil {
		t.Fatalf("the schema did not load: %v", err)
	}
	if err := validateString(t, schema, `<good>1</good>`); err != nil {
		t.Errorf("the sound declaration beside it failed: %v", err)
	}
	err := validateString(t, schema, `<bad/>`)
	if err == nil {
		t.Fatal("using the declaration with the missing type was accepted")
	}
	if !strings.Contains(err.Error(), "src-resolve") {
		t.Errorf("error %q does not cite src-resolve", err)
	}
}

func TestParseRejectsDuplicateGlobal(t *testing.T) {
	_, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="a" type="xs:string"/>
	  <xs:element name="a" type="xs:int"/>
	</xs:schema>`)
	if err == nil {
		t.Fatal("two global elements with one name should be rejected")
	}
}

func TestParseWildcard(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
	  <xs:complexType name="t">
	    <xs:sequence>
	      <xs:any namespace="##other" processContents="lax"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`)

	ct := s.Types[xdm.QName{URI: "urn:t", Local: "t"}].(*ComplexType)
	g := ct.Particle.Term.(*ModelGroup)
	w := g.Particles[0].Term.(*Wildcard)

	if w.ProcessContents != ProcessLax {
		t.Errorf("processContents is %s, want lax", w.ProcessContents)
	}
	if w.Kind != NSNot {
		t.Errorf("##other should give a negated constraint, got %v", w.Kind)
	}
	if w.Allows("urn:t") {
		t.Error("##other should exclude the target namespace")
	}
	if w.Allows("") {
		t.Error("##other should exclude unqualified names")
	}
	if !w.Allows("urn:other") {
		t.Error("##other should allow another namespace")
	}
}

func TestParseIdentityConstraint(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="item" maxOccurs="unbounded">
	          <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	    <xs:key name="itemKey">
	      <xs:selector xpath=".//item"/>
	      <xs:field xpath="@id"/>
	    </xs:key>
	  </xs:element>
	</xs:schema>`)

	d := s.Elements[xdm.QName{Local: "root"}]
	if len(d.IdentityConstraints) != 1 {
		t.Fatalf("got %d identity constraints, want 1", len(d.IdentityConstraints))
	}
	ic := d.IdentityConstraints[0]
	if ic.Kind != ICKey {
		t.Errorf("kind is %s, want key", ic.Kind)
	}
	alt := ic.Selector.Alternatives[0]
	if !alt.DescendantOrSelf {
		t.Error(`the leading ".//" was not recorded`)
	}
	if len(alt.Steps) != 1 || alt.Steps[0].Name.Local != "item" {
		t.Errorf("selector steps are %v", alt.Steps)
	}
	if ic.Fields[0].Alternatives[0].Attribute == nil {
		t.Error("the field's attribute step was not recorded")
	}
}

func TestICPathRejectsOutOfSubset(t *testing.T) {
	// Each of these is valid XPath but outside the subset the spec permits.
	// Accepting them would accept schemas that conforming processors
	// reject, so they must fail here.
	cases := []struct {
		expr  string
		field bool
	}{
		{"item[1]", false},      // predicates are not in the subset
		{"/item", false},        // an absolute path is not
		{"parent::item", false}, // no axis other than child
		{"@id", false},          // a selector may not select attributes
		{"@id/more", true},      // an attribute step must be last
		{"item//sub", false},    // "//" only as a leading ".//"
		{"//", false},           // a bare "//" is not in the subset
	}
	for _, c := range cases {
		if _, err := parseICPath(c.expr, c.field); err == nil {
			t.Errorf("parseICPath(%q) should have failed", c.expr)
		}
	}
}

func TestICPathAccepts(t *testing.T) {
	cases := []struct {
		expr  string
		field bool
	}{
		{".", false},
		{".//item", false},
		{"a/b/c", false},
		{"*", false},
		{"a|b", false},
		{".//a|.//b", false},
		{"@id", true},
		// NameTest admits "*", so "@*" is grammatical. Whether such a
		// field selects exactly one node is decided per instance
		// document, not at parse time.
		{"@*", true},
		{"@ns:*", true},
		// Clause 2.2 permits the unabbreviated child axis wherever the
		// abbreviated form is legal.
		{"child::a", false},
		{"child::a/child::b", false},
		{"a/@id", true},
		{".//a/@id", true},
		{".", true},
	}
	for _, c := range cases {
		if _, err := parseICPath(c.expr, c.field); err != nil {
			t.Errorf("parseICPath(%q) failed: %v", c.expr, err)
		}
	}
}

func TestBuiltinTypesPresent(t *testing.T) {
	s := NewSchema()
	for _, name := range []string{
		"anyType", "anySimpleType", "string", "decimal", "integer",
		"int", "boolean", "date", "dateTime", "QName", "NMTOKENS",
	} {
		if s.Types[xsName(name)] == nil {
			t.Errorf("xs:%s is missing from a new schema", name)
		}
	}

	// xs:anyType is its own base. The self-loop is deliberate in the spec
	// and every base-chain walk has to survive it.
	any := s.anyType()
	if any.BaseType() != any {
		t.Error("xs:anyType should be its own base type definition")
	}

	// Erratum E1-51: the ur-type's attribute wildcard is lax, not strict.
	ct := any.(*ComplexType)
	if ct.AttributeWildcard == nil ||
		ct.AttributeWildcard.ProcessContents != ProcessLax {
		t.Error("the ur-type's attribute wildcard should be lax (E1-51)")
	}
}

func TestBuiltinIntegerBounds(t *testing.T) {
	// The bounds are held lexically because the value space is arbitrary
	// precision: xs:unsignedLong's maximum does not fit in an int64.
	ul := BuiltinType("unsignedLong")
	if ul == nil || ul.Facets.MaxInclusive == nil {
		t.Fatal("xs:unsignedLong has no maxInclusive")
	}
	if *ul.Facets.MaxInclusive != "18446744073709551615" {
		t.Errorf("xs:unsignedLong max is %q", *ul.Facets.MaxInclusive)
	}
}

func TestParseRejectsRedefiningBuiltin(t *testing.T) {
	_, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           targetNamespace="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="string"><xs:restriction base="xs:int"/></xs:simpleType>
	</xs:schema>`)
	if err == nil {
		t.Fatal("redefining a built-in type should be rejected")
	}
}

func TestPatternIsAnchored(t *testing.T) {
	// The pattern facet is anchored while fn:matches is a containment
	// test. Sharing the translation without anchoring would accept every
	// value that merely contains a match.
	p, err := compilePattern("a+")
	if err != nil {
		t.Fatalf("compilePattern: %v", err)
	}
	if !p.Matches("aaa") {
		t.Error(`"aaa" should match a+`)
	}
	if p.Matches("baaa") {
		t.Error(`"baaa" should not match a+: the pattern facet is anchored`)
	}
	if p.Matches("aaab") {
		t.Error(`"aaab" should not match a+: the pattern facet is anchored`)
	}
}

func TestPatternAlternationIsGrouped(t *testing.T) {
	// A top-level alternation must bind inside the anchors: without the
	// non-capturing group, \Aa|b\z would mean "starts with a" or "ends
	// with b".
	p, err := compilePattern("a|b")
	if err != nil {
		t.Fatalf("compilePattern: %v", err)
	}
	if !p.Matches("a") || !p.Matches("b") {
		t.Error("a|b should match both alternatives")
	}
	if p.Matches("ab") {
		t.Error(`"ab" should not match a|b`)
	}
	if p.Matches("xa") {
		t.Error(`"xa" should not match a|b`)
	}
}

func TestSplitFieldsUsesXMLWhitespace(t *testing.T) {
	// strings.Fields splits on every Unicode space. XML whitespace is only
	// these four characters, so a namespace list containing U+00A0 must
	// stay one token rather than becoming two that match nothing.
	got := splitFields("a b\tc\nd\re")
	want := []string{"a", "b", "c", "d", "e"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if n := len(splitFields("a b")); n != 1 {
		t.Errorf("U+00A0 split the value into %d tokens, want 1", n)
	}
}

// defaultAttributes naming a group that does not exist is an error, and the
// error has to be reportable. The message quotes the spelling the schema used,
// which used to be read from p.doc inside the deferred fixup — but a fixup runs
// after every document has been read, when p.doc no longer points at the
// document the attribute came from. The one input that reached the branch, an
// unresolvable group name, therefore crashed rather than reporting itself.
func TestUnresolvedDefaultAttributesIsAnErrorNotACrash(t *testing.T) {
	for _, src := range []string{
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		            defaultAttributes="dad">
		  <xs:element name="doc"><xs:complexType/></xs:element>
		</xs:schema>`,
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		            xmlns:other="http://other.example/"
		            defaultAttributes="other:da">
		  <xs:element name="doc"><xs:complexType/></xs:element>
		</xs:schema>`,
	} {
		_, err := parseSchemaString(t, src)
		if err == nil {
			t.Error("want an error naming the missing attribute group, got none")
		}
	}
}

// TestSourceModelRejectsBadShape covers the schema for schemas itself: the
// readers pick out the children they need by name, so before this check a
// document could carry two <simpleContent> children, or an <annotation> after
// the content model, and still load. Each case below is drawn from the
// msData complexType, attribute and element suites.
func TestSourceModelRejectsBadShape(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"two annotations", `
		  <xs:complexType name="t">
		    <xs:annotation><xs:documentation>a</xs:documentation></xs:annotation>
		    <xs:annotation><xs:documentation>b</xs:documentation></xs:annotation>
		  </xs:complexType>`},
		{"annotation after content", `
		  <xs:complexType name="t">
		    <xs:simpleContent><xs:extension base="xs:string"/></xs:simpleContent>
		    <xs:annotation><xs:documentation>a</xs:documentation></xs:annotation>
		  </xs:complexType>`},
		{"two simpleContent", `
		  <xs:complexType name="t">
		    <xs:simpleContent><xs:extension base="xs:string"/></xs:simpleContent>
		    <xs:simpleContent><xs:extension base="xs:string"/></xs:simpleContent>
		  </xs:complexType>`},
		{"simpleContent and complexContent", `
		  <xs:complexType name="t">
		    <xs:simpleContent><xs:extension base="xs:string"/></xs:simpleContent>
		    <xs:complexContent><xs:restriction base="xs:anyType"/></xs:complexContent>
		  </xs:complexType>`},
		{"two particles in an extension", `
		  <xs:group name="g"><xs:sequence><xs:element name="e" type="xs:string"/></xs:sequence></xs:group>
		  <xs:complexType name="b"><xs:sequence/></xs:complexType>
		  <xs:complexType name="t">
		    <xs:complexContent>
		      <xs:extension base="b">
		        <xs:group ref="g"/>
		        <xs:all><xs:element name="x" type="xs:string"/></xs:all>
		      </xs:extension>
		    </xs:complexContent>
		  </xs:complexType>`},
		{"annotation after a particle in a restriction", `
		  <xs:group name="g"><xs:sequence><xs:element name="e" type="xs:string"/></xs:sequence></xs:group>
		  <xs:complexType name="b"><xs:sequence><xs:any/></xs:sequence></xs:complexType>
		  <xs:complexType name="t">
		    <xs:complexContent>
		      <xs:restriction base="b">
		        <xs:group ref="g"/>
		        <xs:annotation><xs:documentation>a</xs:documentation></xs:annotation>
		      </xs:restriction>
		    </xs:complexContent>
		  </xs:complexType>`},
		{"attribute with two simpleTypes", `
		  <xs:attribute name="a">
		    <xs:simpleType><xs:restriction base="xs:string"/></xs:simpleType>
		    <xs:simpleType><xs:restriction base="xs:string"/></xs:simpleType>
		  </xs:attribute>`},
		{"element with both simpleType and complexType", `
		  <xs:element name="e">
		    <xs:simpleType><xs:restriction base="xs:string"/></xs:simpleType>
		    <xs:complexType><xs:sequence/></xs:complexType>
		  </xs:element>`},
		{"unique without a selector", `
		  <xs:element name="e">
		    <xs:complexType><xs:sequence/></xs:complexType>
		    <xs:unique name="u"><xs:field xpath="@a"/></xs:unique>
		  </xs:element>`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseSchemaString(t, `
			<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`+
				c.src+`</xs:schema>`)
			if err == nil {
				t.Error("this schema breaks the schema for schemas, " +
					"so loading it should have failed")
			}
		})
	}
}

// TestSourceModelAcceptsRefForms covers the shapes the check must not reject:
// a <group>, <attributeGroup> or identity constraint written as a reference
// carries no children of its own beyond an annotation. Requiring a selector
// on every <unique> rejected the saxonData Id id040 and id043 schemas.
func TestSourceModelAcceptsRefForms(t *testing.T) {
	mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           targetNamespace="urn:t" xmlns:t="urn:t">
	  <xs:element name="outer">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="inner">
	          <xs:complexType><xs:sequence/></xs:complexType>
	          <xs:unique name="u">
	            <xs:selector xpath="."/>
	            <xs:field xpath="@a"/>
	          </xs:unique>
	        </xs:element>
	        <xs:element name="other">
	          <xs:complexType><xs:sequence/></xs:complexType>
	          <xs:unique ref="t:u"/>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)
}


// TestParseOccursAcceptsHugeValues covers msData's particlesZ033_a, whose
// minOccurs is 79228162514244337593543950335.
//
// xs:nonNegativeInteger has no upper bound, so a value past what an int can
// hold is still a well-formed occurrence bound and the schema must load.
// Saturating is safe because no document can supply that many children, so a
// bound at or above the saturation point behaves the same during validation.
func TestParseOccursAcceptsHugeValues(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:sequence>
	      <xs:element name="a" type="xs:string"
	                  minOccurs="79228162514244337593543950335"
	                  maxOccurs="79228162514264337593543950335"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`)

	ct := s.Types[xdm.QName{Local: "t"}].(*ComplexType)
	g := ct.Particle.Term.(*ModelGroup)
	if got := g.Particles[0].MinOccurs; got != occursHuge {
		t.Errorf("minOccurs saturated to %d, want %d", got, occursHuge)
	}
	if got := g.Particles[0].MaxOccurs; got != occursHuge {
		t.Errorf("maxOccurs saturated to %d, want %d", got, occursHuge)
	}
}

// TestParseOccursRejectsNonIntegers keeps the saturation from swallowing values
// that are not xs:nonNegativeInteger at all.
func TestParseOccursRejectsNonIntegers(t *testing.T) {
	for _, bad := range []string{"-1", "1.5", "abc", "1e9", ""} {
		_, err := parseSchemaString(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="t">
		    <xs:sequence>
		      <xs:element name="a" type="xs:string" minOccurs="`+bad+`"/>
		    </xs:sequence>
		  </xs:complexType>
		</xs:schema>`)
		if bad == "" {
			// An empty attribute is absent for this purpose and takes
			// the default of 1, so it is not a fault.
			continue
		}
		if err == nil {
			t.Errorf("minOccurs=%q was accepted", bad)
		}
	}
}

// loadVersion parses a schema at a chosen XSD version, for the constraints
// whose answer differs between 1.0 and 1.1.
func loadVersion(t *testing.T, src string, v Version) error {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test schema as XML: %v", err)
	}
	_, err = Load(tree.Root, "s.xsd", Options{Version: v})
	return err
}

// TestAttributeSchemaConstraints covers the schema-validity constraints on
// attribute declarations, attribute groups and attribute uses (XSD Part 1
// §3.2, §3.4.6 and §3.6). Each case names the suite test that pins it.
func TestAttributeSchemaConstraints(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{{
		// src-attribute.2, attKb004: use=required with default.
		"required with default",
		`<xs:complexType name="c">
		   <xs:attribute name="a" type="xs:string" use="required" default="x"/>
		 </xs:complexType>`,
	}, {
		// src-attribute.2, attKb005: prohibited is equally not optional.
		"prohibited with default",
		`<xs:complexType name="c">
		   <xs:attribute name="a" type="xs:string" use="prohibited" default="x"/>
		 </xs:complexType>`,
	}, {
		// a-props-correct.2, attO002: fixed must be valid for the type.
		"fixed invalid for type",
		`<xs:attribute name="a" type="xs:integer" fixed="abc"/>`,
	}, {
		// a-props-correct.2, attKc004.
		"default invalid for type",
		`<xs:complexType name="c">
		   <xs:attribute name="a" type="xs:integer" default="abc"/>
		 </xs:complexType>`,
	}, {
		// ct-props-correct.4, attQ009: the same name twice, once
		// directly and once through a group.
		"duplicate attribute through group",
		`<xs:attributeGroup name="g">
		   <xs:attribute name="a"/>
		 </xs:attributeGroup>
		 <xs:complexType name="c">
		   <xs:attributeGroup ref="g"/>
		   <xs:attribute name="a"/>
		 </xs:complexType>`,
	}, {
		// ct-props-correct.4, attQ013: two groups colliding.
		"duplicate attribute across two groups",
		`<xs:attributeGroup name="g1"><xs:attribute name="a"/></xs:attributeGroup>
		 <xs:attributeGroup name="g2"><xs:attribute name="a"/></xs:attributeGroup>
		 <xs:complexType name="c">
		   <xs:attributeGroup ref="g1"/>
		   <xs:attributeGroup ref="g2"/>
		 </xs:complexType>`,
	}, {
		// topLevelAttribute prohibits form, attA001.
		"form on a global declaration",
		`<xs:attribute name="a" form="unqualified"/>`,
	}, {
		// topLevelAttribute prohibits use, attF009.
		"use on a global declaration",
		`<xs:attribute name="a" use="required"/>`,
	}, {
		// form is an enumeration of two values, attA003.
		"form not in the enumeration",
		`<xs:complexType name="c">
		   <xs:attribute name="a" form="foo"/>
		 </xs:complexType>`,
	}, {
		// use is an enumeration of three, attF011.
		"use not in the enumeration",
		`<xs:complexType name="c">
		   <xs:attribute name="a" use="foo"/>
		 </xs:complexType>`,
	}, {
		// {name} is an NCName, attC007.
		"attribute name is not an NCName",
		`<xs:complexType name="c">
		   <xs:attribute name="a:b"/>
		 </xs:complexType>`,
	}, {
		// src-attribute.3.1, attC004.
		"local attribute with neither name nor ref",
		`<xs:complexType name="c">
		   <xs:attribute name=""/>
		 </xs:complexType>`,
	}, {
		// src-attribute.3.2, attKb013: ref with type.
		"ref with a type attribute",
		`<xs:attribute name="a" type="xs:integer"/>
		 <xs:complexType name="c">
		   <xs:attribute ref="a" type="xs:string"/>
		 </xs:complexType>`,
	}, {
		// src-attribute.3.2, attKb012: ref with form.
		"ref with a form attribute",
		`<xs:attribute name="a" type="xs:integer"/>
		 <xs:complexType name="c">
		   <xs:attribute ref="a" form="qualified"/>
		 </xs:complexType>`,
	}, {
		// src-attribute.3.2, attKb011: ref with an inline simpleType.
		"ref with an inline simpleType",
		`<xs:attribute name="a" type="xs:integer"/>
		 <xs:complexType name="c">
		   <xs:attribute ref="a">
		     <xs:simpleType><xs:restriction base="xs:string"/></xs:simpleType>
		   </xs:attribute>
		 </xs:complexType>`,
	}, {
		// id is an xs:ID, so an NCName, attgA002.
		"id is not an NCName",
		`<xs:attributeGroup name="g" id="0"><xs:attribute name="a"/></xs:attributeGroup>`,
	}, {
		// xs:ID is unique per document, attgA005.
		"duplicate id in one document",
		`<xs:attributeGroup name="g1" id="dup"/>
		 <xs:attributeGroup name="g2" id="dup"/>`,
	}, {
		// derivation-ok-restriction.2.1.1, attZ006.
		"restriction relaxes required to optional",
		`<xs:complexType name="b">
		   <xs:attribute name="a" type="xs:string" use="required"/>
		 </xs:complexType>
		 <xs:complexType name="d">
		   <xs:complexContent>
		     <xs:restriction base="b">
		       <xs:attribute name="a" type="xs:string" use="optional"/>
		     </xs:restriction>
		   </xs:complexContent>
		 </xs:complexType>`,
	}, {
		// derivation-ok-restriction.3, attZ012: prohibiting a required
		// attribute removes it altogether.
		"restriction prohibits a required attribute",
		`<xs:complexType name="b">
		   <xs:attribute name="a" use="required"/>
		 </xs:complexType>
		 <xs:complexType name="d">
		   <xs:complexContent>
		     <xs:restriction base="b">
		       <xs:attribute name="a" use="prohibited"/>
		     </xs:restriction>
		   </xs:complexContent>
		 </xs:complexType>`,
	}, {
		// derivation-ok-restriction.2.2: an attribute the base neither
		// declares nor admits through a wildcard.
		"restriction adds an attribute the base forbids",
		`<xs:complexType name="b">
		   <xs:attribute name="a"/>
		 </xs:complexType>
		 <xs:complexType name="d">
		   <xs:complexContent>
		     <xs:restriction base="b">
		       <xs:attribute name="a"/>
		       <xs:attribute name="added"/>
		     </xs:restriction>
		   </xs:complexContent>
		 </xs:complexType>`,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
				c.src + `</xs:schema>`
			if _, err := parseSchemaString(t, src); err == nil {
				t.Error("schema loaded, want a schema error")
			}
		})
	}
}

// TestAttributeConstraintsRelaxedIn11 covers the three attribute constraints
// XSD 1.1 dropped. Each is an error under 1.0 and must load clean under 1.1;
// applying the 1.0 rule to 1.1 rejected schemas the suite expects to load.
func TestAttributeConstraintsRelaxedIn11(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{{
		// a-props-correct.3, saxonData/Id/id010.xsd: "an ID attribute
		// in XSD 1.1 can have a default value".
		"default on an xs:ID attribute",
		`<xs:complexType name="c">
		   <xs:attribute name="a" type="xs:ID" default="x"/>
		 </xs:complexType>`,
	}, {
		// ct-props-correct.5, saxonData/Id/id001.xsd: "an element in
		// XSD 1.1 can have more than one ID attribute".
		"two xs:ID attributes on one type",
		`<xs:complexType name="c">
		   <xs:attribute name="a" type="xs:ID"/>
		   <xs:attribute name="b" type="xs:ID"/>
		 </xs:complexType>`,
	}, {
		// src-attribute_group.3, attgC020, which carries both answers
		// explicitly: "XSD 1.1 allows circular attribute group
		// definitions" (bug 15795).
		"circular attribute group reference",
		`<xs:complexType name="c"><xs:attributeGroup ref="g"/></xs:complexType>
		 <xs:attributeGroup name="g"><xs:attributeGroup ref="g"/></xs:attributeGroup>`,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
				c.src + `</xs:schema>`
			if err := loadVersion(t, src, Version10); err == nil {
				t.Error("1.0 loaded the schema, want a schema error")
			}
			if err := loadVersion(t, src, Version11); err != nil {
				t.Errorf("1.1 rejected the schema: %v", err)
			}
		})
	}
}

// TestProhibitedAttributeNeedsWildcard covers the distinction between removing
// an attribute *use* and forbidding the *name*.
//
// use="prohibited" takes the use out of the type's {attribute uses}. It does
// not blacklist the name: an attribute wildcard on the same type is still free
// to admit it. attF001 (prohibited, no wildcard, instance expected invalid) and
// attZ002 (prohibited plus anyAttribute, instance expected valid) are the pair
// that pins the difference, and a fix that only deletes the use — or only keeps
// it — gets one of them wrong.
func TestProhibitedAttributeNeedsWildcard(t *testing.T) {
	validate := func(t *testing.T, schema, instance string) error {
		t.Helper()
		s := mustParseSchema(t, schema)
		tree, err := xdm.ParseString(instance, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing the instance: %v", err)
		}
		return s.Validate(tree.Root, ValidateOptions{})
	}

	t.Run("no wildcard rejects the attribute", func(t *testing.T) {
		err := validate(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="e">
		    <xs:complexType>
		      <xs:attribute name="a" use="prohibited"/>
		    </xs:complexType>
		  </xs:element>
		</xs:schema>`, `<e a="1"/>`)
		if err == nil {
			t.Error("a prohibited attribute was accepted with no wildcard to admit it")
		}
	})

	t.Run("a wildcard still admits the name", func(t *testing.T) {
		err := validate(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="e">
		    <xs:complexType>
		      <xs:attribute name="a" use="prohibited"/>
		      <xs:anyAttribute namespace="##local" processContents="lax"/>
		    </xs:complexType>
		  </xs:element>
		</xs:schema>`, `<e a="1"/>`)
		if err != nil {
			t.Errorf("the wildcard should admit the name: %v", err)
		}
	})
}

// A value the base fixes may not be changed, in either of the two places a
// value constraint can be written.
//
// Attribute Use Correct clause 2 (§3.5.6) covers a use referring to a
// declaration that fixes a value; derivation-ok-restriction.2.1.3 covers a
// restriction against a base that fixes one. Both are written over the
// *effective* value constraint — the use's own if present, otherwise the
// declaration's — because the two may each carry one.
//
// A base that only supplies a *default* constrains nothing, so the restriction
// is free; that half is the guard against a check that just compares values.
func TestFixedValueMayNotBeChanged(t *testing.T) {
	// au-props-correct.2: attO025 writes fixed="456" over a declaration
	// that fixes "123".
	if _, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           targetNamespace="urn:t" xmlns:t="urn:t">
	  <xs:attribute name="foo" fixed="123"/>
	  <xs:complexType name="c"><xs:attribute ref="t:foo" fixed="456"/></xs:complexType>
	</xs:schema>`); err == nil {
		t.Error("a use may not give a value the declaration fixes differently")
	}
	// Repeating the same fixed value is legal, and so is inheriting it.
	for _, use := range []string{`fixed="123"`, ``} {
		if _, err := parseSchemaString(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           targetNamespace="urn:t" xmlns:t="urn:t">
		  <xs:attribute name="foo" fixed="123"/>
		  <xs:complexType name="c"><xs:attribute ref="t:foo" `+use+`/></xs:complexType>
		</xs:schema>`); err != nil {
			t.Errorf("use %q should be legal: %v", use, err)
		}
	}

	// derivation-ok-restriction.2.1.3: attZ008_e restricts a base that
	// fixes "not_fixed" and names "fixed" instead.
	restriction := func(baseAttr, derivedAttr string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="B"><xs:sequence/>` + baseAttr + `</xs:complexType>
		  <xs:complexType name="D"><xs:complexContent><xs:restriction base="B">
		    <xs:sequence/>` + derivedAttr + `</xs:restriction></xs:complexContent></xs:complexType>
		</xs:schema>`
	}
	if _, err := parseSchemaString(t, restriction(
		`<xs:attribute name="a" type="xs:string" fixed="one"/>`,
		`<xs:attribute name="a" type="xs:string" fixed="two"/>`)); err == nil {
		t.Error("a restriction may not change a value the base fixes")
	}
	if _, err := parseSchemaString(t, restriction(
		`<xs:attribute name="a" type="xs:string" fixed="one"/>`,
		`<xs:attribute name="a" type="xs:string" fixed="one"/>`)); err != nil {
		t.Errorf("repeating the base's fixed value is legal: %v", err)
	}
	// A base that only defaults constrains nothing.
	if _, err := parseSchemaString(t, restriction(
		`<xs:attribute name="a" type="xs:string" default="one"/>`,
		`<xs:attribute name="a" type="xs:string" fixed="two"/>`)); err != nil {
		t.Errorf("a base default does not fix the value: %v", err)
	}
}


// TestSchemaIDMustBeAnNCName covers the id= attribute the schema for schemas
// gives nearly every XSD element as `id = ID`. ID means NCName, so a value that
// is not one is a representation fault. MS-IdentityConstraint idA006 writes the
// empty string and MS-Notations notatA005 writes "25".
func TestSchemaIDMustBeAnNCName(t *testing.T) {
	for _, id := range []string{"", "25", "foo:bar", "-2.5foo", " "} {
		src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element id="` + id + `" name="e" type="xs:string"/>
		</xs:schema>`
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Load(tree.Root, "s.xsd", Options{}); err == nil {
			t.Errorf("id=%q is not an NCName and should be refused", id)
		}
	}
}

// TestSchemaIDMustBeUniqueInItsDocument covers the other half of xs:ID.
//
// Two elements in one document may not share an id. MS-IdentityConstraint
// idA002 puts the same id on an element declaration and on the constraint
// beside it, which is the case this reproduces.
func TestSchemaIDMustBeUniqueInItsDocument(t *testing.T) {
	const src = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element id="foo123" name="a" type="xs:string"/>
	      </xs:sequence>
	    </xs:complexType>
	    <xs:unique id="foo123" name="u">
	      <xs:selector xpath=".//a"/>
	      <xs:field xpath="."/>
	    </xs:unique>
	  </xs:element>
	</xs:schema>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(tree.Root, "s.xsd", Options{}); err == nil {
		t.Fatal("one id used twice in a document should be refused")
	}
}

// TestSchemaIDIsScopedToOneDocument is the boundary the uniqueness check must
// not overrun. ID uniqueness is an XML document property, so the same id may
// appear once in an including document and once in what it includes.
// MS-ComplexType ctA029 and MS-Group groupA006 are both exactly this, and
// scoping the check to the assembled schema wrongly rejected them.
func TestSchemaIDIsScopedToOneDocument(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.xsd")
	other := filepath.Join(dir, "other.xsd")

	if err := os.WriteFile(other, []byte(
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		   <xs:complexType id="foo123" name="otherType"/>
		 </xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte(
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		   <xs:include schemaLocation="other.xsd"/>
		   <xs:complexType id="foo123" name="mainType"/>
		 </xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadFiles([]string{main}, Options{}); err != nil {
		t.Fatalf("the same id in two documents is not a clash:\n%v", err)
	}
}

// TestNotationRepresentation covers the XML Representation Summary in §3.12.2,
// which is the whole of what an <xs:notation> may be written as:
//
//	<notation id = ID  name = NCName  public = token  system = anyURI
//	          {any attributes with non-schema namespace . . .}>
//	  Content: (annotation?)
//	</notation>
//
// None of it was enforced, which let the MS-Notations set through almost
// entirely. Each case below names the test that pins it.
func TestNotationRepresentation(t *testing.T) {
	cases := []struct {
		name, decl string
	}{
		{"notatB008: name is a QName, not an NCName",
			`<xsd:notation name="foo:bar" public="p" system="s"/>`},
		{"notatB009: name begins with a colon",
			`<xsd:notation name=":bar" public="p" system="s"/>`},
		{"notatB013: name begins with a digit-ish character",
			`<xsd:notation name="-2.5foo" public="p" system="s"/>`},
		{"notatB001: neither a public nor a system identifier",
			`<xsd:notation name="foo"/>`},
		{"notatE003: an attribute in no namespace that is not one of the four",
			`<xsd:notation foo="bar" name="jpeg" public="p" system="s"/>`},
		{"notatE002: an attribute in the schema namespace",
			`<xsd:notation xmlns:a="http://www.w3.org/2001/XMLSchema" a:b="c" ` +
				`name="jpeg" public="p" system="s"/>`},
		{"notatG001: character content",
			`<xsd:notation name="jpeg" public="p" system="s">Some Text</xsd:notation>`},
		{"notatG002: an element child that is not an annotation",
			`<xsd:notation name="jpeg" public="p" system="s"><a/></xsd:notation>`},
	}

	for _, c := range cases {
		src := `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">` +
			c.decl + `</xsd:schema>`
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, err := Load(tree.Root, "s.xsd", Options{}); err == nil {
			t.Errorf("%s: should be a schema error", c.name)
		}
	}
}

// TestNotationWellFormedIsAccepted is the boundary: the checks above must not
// refuse the shape the spec's own example uses, with or without an annotation
// and with either identifier alone.
func TestNotationWellFormedIsAccepted(t *testing.T) {
	for _, decl := range []string{
		`<xsd:notation name="jpeg" public="image/jpeg" system="viewer.exe"/>`,
		`<xsd:notation name="jpeg" public="image/jpeg"/>`,
		`<xsd:notation name="jpeg" system="viewer.exe"/>`,
		`<xsd:notation id="n1" name="jpeg" public="image/jpeg"/>`,
		`<xsd:notation xmlns:o="urn:other" o:x="y" name="jpeg" public="image/jpeg"/>`,
		`<xsd:notation name="jpeg" public="image/jpeg">` +
			`<xsd:annotation><xsd:documentation>ok</xsd:documentation></xsd:annotation>` +
			`</xsd:notation>`,
	} {
		src := `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">` +
			decl + `</xsd:schema>`
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("%s: %v", decl, err)
		}
		if _, err := Load(tree.Root, "s.xsd", Options{}); err != nil {
			t.Errorf("%s should load:\n%v", decl, err)
		}
	}
}


// wrapAny puts attrs on an <xs:any> inside a content model, so that the tests
// below differ only in the wildcard attributes under test.
func wrapAny(attrs string) string {
	return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="foo"><xs:complexType><xs:sequence>
	    <xs:any ` + attrs + `/>
	  </xs:sequence></xs:complexType></xs:element>
	</xs:schema>`
}

// TestNamespaceListSyntax covers the xs:namespaceList grammar of §3.10.2,
// `(##any | ##other) | List of (anyURI | ##targetNamespace | ##local)`.
//
// The union's first branch is one token, so ##any and ##other are complete
// values that may not be combined with anything; every member of the list
// branch is either an anyURI or one of the two remaining keywords. A
// misspelled keyword is neither, and used to be taken as a namespace name
// nothing could ever be in — accepting the schema and silently matching no
// element.
func TestNamespaceListSyntax(t *testing.T) {
	valid := []string{
		`namespace="##any"`,
		`namespace="##other"`,
		`namespace="##targetNamespace"`,
		`namespace="##local"`,
		`namespace="##targetNamespace ##local http://example.com/a"`,
		`namespace="http://a http://b"`,
		// A list type collapses surrounding whitespace, so this is
		// still the single token ##any and not a two-member list.
		`namespace="  ##any  "`,
	}
	for _, attrs := range valid {
		if _, err := parseSchemaString(t, wrapAny(attrs)); err != nil {
			t.Errorf("%s should be accepted: %v", attrs, err)
		}
	}

	invalid := []string{
		`namespace="##any ##other"`,          // wildC049
		`namespace="##any http://example.com"`, // wildC066
		`namespace="##other http://a"`,       // wildF006
		`namespace="##target"`,               // wildC035, a misspelling
		`namespace="##anyAttribute"`,         // wildK002
		`namespace="##anyAttribute ##other"`, // wildK020
	}
	for _, attrs := range invalid {
		if _, err := parseSchemaString(t, wrapAny(attrs)); err == nil {
			t.Errorf("%s should be rejected", attrs)
		}
	}
}

// TestSchemaIDMustBeUniqueNCName covers the `id` attribute the schema for
// schemas declares as xs:ID on nearly every XSD element.
//
// Nothing in assembly reads id — it exists for external reference — so the
// constraint had no other enforcement point and went unchecked entirely.
func TestSchemaIDMustBeUniqueNCName(t *testing.T) {
	if _, err := parseSchemaString(t, wrapAny(`id="ok-name"`)); err != nil {
		t.Errorf("a valid NCName id should be accepted: %v", err)
	}
	// wildA003/A004/A005: a bare number is not an NCName, which may not
	// start with a digit.
	if _, err := parseSchemaString(t, wrapAny(`id="25"`)); err == nil {
		t.Error(`id="25" should be rejected: an NCName may not start with a digit`)
	}
	// wildA006/A007: "non-colonized" is the whole point of an NCName.
	if _, err := parseSchemaString(t, wrapAny(`id="foo:bar"`)); err == nil {
		t.Error(`id="foo:bar" should be rejected: an NCName has no colon`)
	}
	// wildA008: xs:ID is unique across the document, so an element and a
	// wildcard beneath it may not share one.
	dup := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="foo" id="dup"><xs:complexType><xs:sequence>
	    <xs:any id="dup"/>
	  </xs:sequence></xs:complexType></xs:element>
	</xs:schema>`
	if _, err := parseSchemaString(t, dup); err == nil {
		t.Error("a duplicate id should be rejected")
	}
}

// TestUnknownAttributeIsRejected covers the attribute lists in the XML
// Representation Summary boxes of §3.
//
// The readers pick out the attributes they know by name, so one they do not
// know was simply invisible — a typo in a schema was silently ignored rather
// than reported. An attribute in a foreign namespace stays legal: every
// summary box ends with "{any attributes with non-schema namespace}".
func TestUnknownAttributeIsRejected(t *testing.T) {
	// wildI003: an unprefixed name that is not an XSD attribute.
	if _, err := parseSchemaString(t, wrapAny(`foo="bar"`)); err == nil {
		t.Error(`foo="bar" on xs:any should be rejected`)
	}
	// wildI002: the same document, except the attribute is prefixed into a
	// namespace of its own, which the wildcard admits.
	ok := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:a="http://foo">
	  <xs:element name="foo"><xs:complexType><xs:sequence>
	    <xs:any namespace="##other" processContents="lax" a:b="c"/>
	  </xs:sequence></xs:complexType></xs:element>
	</xs:schema>`
	if _, err := parseSchemaString(t, ok); err != nil {
		t.Errorf("a foreign-namespace attribute should be accepted: %v", err)
	}
	// wildQ002/Q003: an attribute wildcard has no occurrence range at all —
	// it is not a particle — so minOccurs and maxOccurs are not among its
	// attributes even though they are perfectly good names elsewhere.
	bad := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="foo"><xs:complexType><xs:simpleContent>
	    <xs:extension base="xs:string"><xs:anyAttribute minOccurs="2"/></xs:extension>
	  </xs:simpleContent></xs:complexType></xs:element>
	</xs:schema>`
	if _, err := parseSchemaString(t, bad); err == nil {
		t.Error("minOccurs on xs:anyAttribute should be rejected")
	}
}

// TestEmptyAttributeValueIsNotTheDefault covers attributes written with an
// empty value, which is not the same as leaving them out.
//
// The readers fetched these through AttrValue, which returns "" for both the
// absent and the present-but-empty case, so an empty value silently took the
// attribute's declared default. But "" is not a member of the lexical space of
// xs:nonNegativeInteger, nor of the NMTOKEN enumeration processContents
// restricts, so each of these is a fault.
func TestEmptyAttributeValueIsNotTheDefault(t *testing.T) {
	for _, attrs := range []string{
		`maxOccurs=""`,       // wildB014
		`minOccurs=""`,       // wildB022
		`processContents=""`, // wildD071
	} {
		if _, err := parseSchemaString(t, wrapAny(attrs)); err == nil {
			t.Errorf("%s should be rejected", attrs)
		}
	}
}

// TestNameMustBeNCName covers the `name` attribute, an xs:NCName wherever the
// schema for schemas uses it.
//
// A component takes its {name} from this attribute verbatim, so a value that
// is not an NCName declares a component under a name no reference could be
// written for — the schema loads and then nothing can ever use it.
func TestNameMustBeNCName(t *testing.T) {
	bad := []string{
		`<xs:group name="1"><xs:sequence/></xs:group>`,     // groupA010
		`<xs:group name="a:b"><xs:sequence/></xs:group>`,   // groupA012
		`<xs:element name="x y"/>`,
	}
	for _, decl := range bad {
		src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
			decl + `</xs:schema>`
		if _, err := parseSchemaString(t, src); err == nil {
			t.Errorf("%s should be rejected", decl)
		}
	}

	// addB193 pins the other side: NCName derives from xs:token, whose
	// whiteSpace facet is a fixed "collapse", so surrounding space is gone
	// before the value is matched and this name is simply "sub2-elem".
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="sub2-elem "/></xs:schema>`
	if _, err := parseSchemaString(t, src); err != nil {
		t.Errorf("a name with trailing whitespace should be accepted: %v", err)
	}
}

// TestElementRefExcludesDeclarationAttributes covers src-element.2.2 (§3.3.3):
// a local <element> with ref may carry only minOccurs, maxOccurs and id.
//
// A reference *is* the declaration it names, so an attribute beside it that
// would describe a declaration is not a modification of the referenced one for
// this use — it is simply ignored. The schema then does not mean what it
// appears to say, which the author only discovers if it is reported.
func TestElementRefExcludesDeclarationAttributes(t *testing.T) {
	local := func(attrs string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="head" type="xs:string"/>
		  <xs:element name="doc"><xs:complexType><xs:sequence>
		    <xs:element ref="head" ` + attrs + `/>
		  </xs:sequence></xs:complexType></xs:element>
		</xs:schema>`
	}

	// Only the occurrence attributes and id survive beside a ref.
	for _, attrs := range []string{``, `minOccurs="0"`, `maxOccurs="3"`, `id="r1"`} {
		if _, err := parseSchemaString(t, local(attrs)); err != nil {
			t.Errorf("ref with %q should be accepted: %v", attrs, err)
		}
	}

	for _, attrs := range []string{
		`name="Local"`,        // name00401m3/m4/m5: clause 2.1
		`type="xs:boolean"`,   // name00501m12
		`block="#all"`,        // name00501m10
		`form="qualified"`,
		`nillable="true"`,
		`default="x"`,
		`fixed="x"`,
	} {
		if _, err := parseSchemaString(t, local(attrs)); err == nil {
			t.Errorf("ref with %q should be rejected", attrs)
		}
	}

	// The excluded children, which belong to a declaration rather than to a
	// use of one.
	withChild := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="head" type="xs:string"/>
	  <xs:element name="doc"><xs:complexType><xs:sequence>
	    <xs:element ref="head"><xs:simpleType>
	      <xs:restriction base="xs:string"/>
	    </xs:simpleType></xs:element>
	  </xs:sequence></xs:complexType></xs:element>
	</xs:schema>`
	if _, err := parseSchemaString(t, withChild); err == nil {
		t.Error("a ref with an inline simpleType should be rejected")
	}
}

// TestElementDefaultMustBeValidForItsType covers e-props-correct.2 (§3.3.6):
// a default or fixed value must be valid against the declaration's type.
//
// The value was previously stored without ever being validated, so a schema
// could promise a default its own type could not represent — and the fault
// only surfaced, if at all, as a confusing failure against an instance.
func TestElementDefaultMustBeValidForItsType(t *testing.T) {
	decl := func(d string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="root" ` + d + `/></xs:schema>`
	}
	for _, d := range []string{
		`type="xsd:decimal" default="XII"`, // valueConstraint00101m2
		`type="xs:boolean" default="Yes"`,  // valueConstraint00401m2
		`type="xs:float" fixed="1.0F-2"`,   // valueConstraint00601m2
	} {
		// The first case deliberately uses an unbound prefix, so build
		// it against the real schema prefix instead.
		src := decl(strings.ReplaceAll(d, "xsd:", "xs:"))
		if _, err := parseSchemaString(t, src); err == nil {
			t.Errorf("%s should be rejected", d)
		}
	}
	for _, d := range []string{
		`type="xs:decimal" default="12"`,
		`type="xs:boolean" default="true"`,
		`type="xs:float" fixed="1.0E-2"`,
	} {
		if _, err := parseSchemaString(t, decl(d)); err != nil {
			t.Errorf("%s should be accepted: %v", d, err)
		}
	}

	// A union member type is filled in by its own fixup, so this check has
	// to run after the fixups have drained rather than among them. stE050
	// is the case that caught it: read too early the union has no members
	// yet and every value "matches none of them".
	union := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root" fixed="1"><xs:simpleType>
	    <xs:union memberTypes="xs:boolean xs:int xs:string"/>
	  </xs:simpleType></xs:element>
	</xs:schema>`
	if _, err := parseSchemaString(t, union); err != nil {
		t.Errorf("a fixed value valid for a union member should be accepted: %v", err)
	}

	// Clause 2.2: character data has nowhere to go in an element-only type.
	elementOnly := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root" default="x"><xs:complexType><xs:sequence>
	    <xs:element name="a" type="xs:string"/>
	  </xs:sequence></xs:complexType></xs:element>
	</xs:schema>`
	if _, err := parseSchemaString(t, elementOnly); err == nil {
		t.Error("a default on an element-only type should be rejected")
	}
}

// TestIDTypedElementValueConstraintIsVersioned covers e-props-correct.5, which
// XSD 1.0 imposes and XSD 1.1 dropped.
//
// Under 1.0 an xs:ID-typed element may carry no default or fixed value at all:
// an ID is unique across the document, so a schema-supplied value would
// collide with itself on the second element that used it. 1.1 removes the
// restriction, and the suite pins both halves — valueConstraint01001m2 is
// expected invalid under 1.0 and valid under 1.1.
func TestIDTypedElementValueConstraintIsVersioned(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root"/>
	  <xs:element name="ID" type="xs:ID" fixed="alpha"/>
	</xs:schema>`

	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test schema as XML: %v", err)
	}
	if _, err := Load(tree.Root, "s.xsd", Options{}); err == nil {
		t.Error("XSD 1.0 should reject a fixed value on an xs:ID-typed element")
	}
	if _, err := Load(tree.Root, "s.xsd", Options{Version: Version11}); err != nil {
		t.Errorf("XSD 1.1 should accept it: %v", err)
	}
}

// TestAllGroupOccursLimited covers All Group Limited clause 1.2 (§3.8.6): the
// particle whose term is an xs:all group must have maxOccurs=1.
//
// "Each of these once, in any order" is defined against the members' own
// bounds, so repeating the group as a whole has no meaning the spec assigns —
// it confines the group to one occurrence instead.
func TestAllGroupOccursLimited(t *testing.T) {
	inType := func(attrs string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="foo"><xs:all ` + attrs + `>
		    <xs:element name="t1"/>
		  </xs:all></xs:complexType>
		</xs:schema>`
	}
	for _, attrs := range []string{
		`maxOccurs="2"`,          // mgAb004
		`maxOccurs="9999999999"`, // mgAb006
		`maxOccurs="unbounded"`,  // mgAb007
		`minOccurs="0" maxOccurs="2"`, // mgO003
	} {
		if _, err := parseSchemaString(t, inType(attrs)); err == nil {
			t.Errorf("xs:all with %q should be rejected", attrs)
		}
	}
	// An optional all group is explicitly allowed, and §3.4.2.3.3 relies on
	// it when merging an optional base into an extension.
	for _, attrs := range []string{``, `maxOccurs="1"`, `minOccurs="0"`} {
		if _, err := parseSchemaString(t, inType(attrs)); err != nil {
			t.Errorf("xs:all with %q should be accepted: %v", attrs, err)
		}
	}
}

// TestNamedGroupDefinitionProhibitsOccurs covers the xs:namedGroup type in the
// schema for schemas, which marks ref, minOccurs and maxOccurs "prohibited" on
// a <group name="..."> and both occurrence attributes on the <all> inside it.
//
// A definition is not a use, so it has no occurrence range to state. Neither
// attribute is read on this path — the definition's group is not built as a
// particle — so both were silently discarded, and mgO019's <all maxOccurs="0">
// inside a named group loaded clean while the identical group written inline
// was rejected.
func TestNamedGroupDefinitionProhibitsOccurs(t *testing.T) {
	group := func(gAttrs, allAttrs string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:group name="g" ` + gAttrs + `><xs:all ` + allAttrs + `>
		    <xs:element name="e1"/>
		  </xs:all></xs:group>
		</xs:schema>`
	}
	if _, err := parseSchemaString(t, group(``, ``)); err != nil {
		t.Fatalf("a plain named group should be accepted: %v", err)
	}
	for _, g := range []string{`minOccurs="0"`, `maxOccurs="2"`, `ref="g"`} {
		if _, err := parseSchemaString(t, group(g, ``)); err == nil {
			t.Errorf("a named group with %q should be rejected", g)
		}
	}
	// mgO019: the occurrence attributes are prohibited on the inner all
	// outright, not merely constrained to 1.
	for _, a := range []string{`maxOccurs="0" minOccurs="0"`, `maxOccurs="1"`, `minOccurs="0"`} {
		if _, err := parseSchemaString(t, group(``, a)); err == nil {
			t.Errorf("the xs:all of a named group with %q should be rejected", a)
		}
	}
}

// TestFinalAndBlockTokensDiffer covers the two derivation-set types in the
// schema for schemas, which are not interchangeable.
//
// blockSet — block and blockDefault — is "#all or a subset of {substitution,
// extension, restriction}". derivationSet — final on an element — is "#all or a
// subset of {extension, restriction}", with no substitution: preventing
// substitution is what block does, and final="substitution" reads as though it
// did the same thing while having no such meaning. elemF004 and the elemF006-8
// series each pin one spelling of that mistake.
func TestFinalAndBlockTokensDiffer(t *testing.T) {
	decl := func(attrs string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="foo" ` + attrs + `/></xs:schema>`
	}
	for _, attrs := range []string{
		`final="substitution"`,                        // elemF004
		`final="restriction substitution"`,            // elemF006
		`final="substitution extension"`,              // elemF007
		`final="extension restriction substitution"`,  // elemF008
	} {
		if _, err := parseSchemaString(t, decl(attrs)); err == nil {
			t.Errorf("%s should be rejected", attrs)
		}
	}
	// The same token is perfectly good in block, and #all covers everything
	// in either attribute.
	for _, attrs := range []string{
		`block="substitution"`, `block="#all"`,
		`final="#all"`, `final="extension restriction"`,
	} {
		if _, err := parseSchemaString(t, decl(attrs)); err != nil {
			t.Errorf("%s should be accepted: %v", attrs, err)
		}
	}
}

// TestBooleanAttributeRejectsEmptyValue covers xs:boolean-typed attributes
// written with an empty value.
//
// As with the occurrence attributes, the value was read through AttrValue,
// which cannot tell an absent attribute from one written as "" — so the empty
// value quietly took the declared default instead of being reported. "" is not
// in the lexical space of xs:boolean. elemB005 and elemK003 pin it.
func TestBooleanAttributeRejectsEmptyValue(t *testing.T) {
	for _, attrs := range []string{`abstract=""`, `nillable=""`, `abstract="yes"`} {
		src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="foo" ` + attrs + `/></xs:schema>`
		if _, err := parseSchemaString(t, src); err == nil {
			t.Errorf("%s should be rejected", attrs)
		}
	}
}

// TestFormDefaultIsAnEnumeration covers elementFormDefault and
// attributeFormDefault, whose type is the two-token xs:formChoice.
//
// The value was compared against "qualified" and anything else taken as
// unqualified, so a misspelling silently reversed the meaning of every local
// declaration in the document rather than being reported. elemH004
// ("Qualified") and elemH005 ("Unqualified") are that mistake; elemH003 ("")
// and elemH006 (two tokens) are values outside the enumeration.
func TestFormDefaultIsAnEnumeration(t *testing.T) {
	for _, attrs := range []string{
		`elementFormDefault=""`,                       // elemH003
		`elementFormDefault="Qualified"`,              // elemH004
		`elementFormDefault="Unqualified"`,            // elemH005
		`elementFormDefault="qualified unqualified"`,  // elemH006
		`attributeFormDefault="Qualified"`,
	} {
		src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" ` + attrs + `>
		  <xs:element name="myElem" type="xs:string"/></xs:schema>`
		if _, err := parseSchemaString(t, src); err == nil {
			t.Errorf("%s should be rejected", attrs)
		}
	}
	for _, attrs := range []string{
		`elementFormDefault="qualified"`, `elementFormDefault="unqualified"`,
		`attributeFormDefault="qualified"`,
	} {
		src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" ` + attrs + `>
		  <xs:element name="myElem" type="xs:string"/></xs:schema>`
		if _, err := parseSchemaString(t, src); err != nil {
			t.Errorf("%s should be accepted: %v", attrs, err)
		}
	}
}

// TestSubstitutionGroupExclusions covers e-props-correct.4 (§3.3.6): a
// member's type must be validly derived from the head's, given the head's
// {substitution group exclusions}.
//
// final= on a head is how it fixes the shape its substitutes may have —
// final="extension" says no element whose type extends mine may stand in for
// me. The set was parsed and then never read, so substGrpExcl00202m2, whose
// member extends a head declared final="extension", loaded clean.
func TestSubstitutionGroupExclusions(t *testing.T) {
	schema := func(final, memberType string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:element name="Head" type="HeadType" ` + final + `/>
		  <xs:complexType name="HeadType"><xs:sequence>
		    <xs:element name="Ear"/>
		  </xs:sequence></xs:complexType>
		  <xs:element name="Member" substitutionGroup="Head">
		    <xs:complexType><xs:complexContent>
		      <xs:` + memberType + ` base="HeadType"><xs:sequence>
		        <xs:element name="Nose"/>
		      </xs:sequence></xs:` + memberType + `>
		    </xs:complexContent></xs:complexType>
		  </xs:element>
		</xs:schema>`
	}

	// substGrpExcl00202m2: the member extends a head that excludes extension.
	if _, err := parseSchemaString(t, schema(`final="extension"`, "extension")); err == nil {
		t.Error("extending a head declared final=\"extension\" should be rejected")
	}
	// substGrpExcl00303m2: "restriction extension" excludes both.
	if _, err := parseSchemaString(t, schema(`final="restriction extension"`, "extension")); err == nil {
		t.Error(`final="restriction extension" should exclude an extending member`)
	}
	// The exclusion is per method: a head that excludes only restriction
	// still accepts a member that extends it.
	if _, err := parseSchemaString(t, schema(`final="restriction"`, "extension")); err != nil {
		t.Errorf(`final="restriction" should still admit an extending member: %v`, err)
	}
	// And with no final at all, either derivation is fine.
	if _, err := parseSchemaString(t, schema(``, "extension")); err != nil {
		t.Errorf("a head with no final should admit an extending member: %v", err)
	}
}

// A chain of extensions must flatten all the way down, not one link.
//
// An extension's content model is the base's followed by its own, and the
// splice is deferred because the base may not be resolved when the type is
// read. But a type's {base type definition} is itself filled in by a fixup, so
// running the splice during the fixup pass could find a base whose own splice
// had not happened — or whose base was still nil — and copy a half-built model,
// losing every link below it.
//
// elemZ010 is the suite's case: four types across four documents, a extends b
// extends c extends d, each adding one element. The content model of a is
// d, c, b, a, and before this each type came out with only its own element and
// its immediate base's.
func TestExtensionChainFlattensCompletely(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="d">
	    <xs:sequence><xs:element name="d" type="xs:string"/></xs:sequence>
	  </xs:complexType>
	  <!-- Declared out of derivation order on purpose: the splice must not
	       depend on the order the types happen to be read in. -->
	  <xs:complexType name="a">
	    <xs:complexContent><xs:extension base="b">
	      <xs:sequence><xs:element name="a" type="xs:string"/></xs:sequence>
	    </xs:extension></xs:complexContent>
	  </xs:complexType>
	  <xs:complexType name="c">
	    <xs:complexContent><xs:extension base="d">
	      <xs:sequence><xs:element name="c" type="xs:string"/></xs:sequence>
	    </xs:extension></xs:complexContent>
	  </xs:complexType>
	  <xs:complexType name="b">
	    <xs:complexContent><xs:extension base="c">
	      <xs:sequence><xs:element name="b" type="xs:string"/></xs:sequence>
	    </xs:extension></xs:complexContent>
	  </xs:complexType>
	  <xs:element name="root" type="a"/>
	</xs:schema>`)

	var leaves func(p *Particle) []string
	leaves = func(p *Particle) []string {
		if p == nil {
			return nil
		}
		switch term := p.Term.(type) {
		case *ElementDecl:
			return []string{term.Name.Local}
		case *ModelGroup:
			var out []string
			for _, q := range term.Particles {
				out = append(out, leaves(q)...)
			}
			return out
		}
		return nil
	}

	for _, want := range []struct {
		typ   string
		order string
	}{
		{"d", "d"},
		{"c", "d c"},
		{"b", "d c b"},
		{"a", "d c b a"},
	} {
		ct, ok := s.Types[xdm.QName{Local: want.typ}].(*ComplexType)
		if !ok {
			t.Fatalf("type %q missing", want.typ)
		}
		if got := strings.Join(leaves(ct.Particle), " "); got != want.order {
			t.Errorf("type %q content model = %q, want %q", want.typ, got, want.order)
		}
	}
}
