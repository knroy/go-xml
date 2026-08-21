package xsd

import (
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
