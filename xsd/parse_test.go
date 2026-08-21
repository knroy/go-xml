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
