package relaxng

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The ns= of an <include> or <externalRef> is inherited lexically (sections
// 4.7 and 4.8): it reaches the elements written in the schema it brings in,
// and nothing else. A definition used to be compiled under the ns in force at
// the <ref> that reached it, so a definition of the including grammar
// referred to from an included one took the include's ns, the parts of a
// definition combined across an include all took one ns, and a nested
// <grammar> lost its document's. Every verdict here matches Jing 20241231
// and xmllint.
func TestIncludeNsIsLexical(t *testing.T) {
	docs := map[string]string{
		"inc1.rng": `<grammar` + rngNS + `><define name="x"><element name="x">
			<optional><ref name="y"/></optional></element></define></grammar>`,
		"inc3.rng": `<grammar` + rngNS + `><define name="d" combine="choice">
			<element name="i"><empty/></element></define></grammar>`,
		"inc4.rng": `<grammar` + rngNS + `><define name="d" combine="interleave">
			<element name="i"><empty/></element></define></grammar>`,
		"inc5.rng": `<grammar` + rngNS + `><start><element name="s"><ref name="d"/></element></start>
			<define name="d"><element name="old"><empty/></element></define></grammar>`,
		"inc6.rng": `<grammar` + rngNS + `><define name="x"><element name="x"><grammar><start>
			<element name="inner"><parentRef name="y"/></element></start></grammar></element></define>
			<define name="y"><element name="y"><empty/></element></define></grammar>`,
		"inc7.rng": `<grammar` + rngNS + `><define name="x"><element name="x"><grammar><start>
			<element name="inner"><parentRef name="y"/></element></start></grammar></element></define></grammar>`,
		"outer8.rng": `<grammar` + rngNS + `><include href="inc1.rng" ns="urn:b"/>
			<start><ref name="x"/></start>
			<define name="y"><element name="y"><empty/></element></define></grammar>`,
	}
	schemas := map[string]string{
		// y belongs to the including grammar, reached eagerly and lazily.
		"eager": `<grammar` + rngNS + `><include href="inc1.rng" ns="urn:x"/><start><ref name="x"/></start>
			<define name="y"><element name="y"><empty/></element></define></grammar>`,
		"lazy": `<grammar` + rngNS + `><include href="inc1.rng" ns="urn:x"/><start><ref name="x"/></start>
			<define name="y"><element name="y"><optional><ref name="x"/></optional></element></define></grammar>`,
		// d combined across the include boundary: each part keeps its own ns.
		"choice": `<grammar` + rngNS + `><include href="inc3.rng" ns="urn:x"/>
			<start><element name="r"><ref name="d"/></element></start>
			<define name="d" combine="choice"><element name="m"><empty/></element></define></grammar>`,
		"inter": `<grammar` + rngNS + `><include href="inc4.rng" ns="urn:x"/>
			<start><element name="r"><ref name="d"/></element></start>
			<define name="d" combine="interleave"><element name="m"><empty/></element></define></grammar>`,
		// An override inside <include> sits under its ns= lexically.
		"override": `<grammar` + rngNS + `><include href="inc5.rng" ns="urn:x">
			<define name="d"><element name="new"><empty/></element></define></include></grammar>`,
		// A nested grammar keeps its document's ns; parentRef reaches a
		// definition that keeps its own.
		"parent": `<grammar` + rngNS + `><include href="inc6.rng" ns="urn:x"/><start><ref name="x"/></start></grammar>`,
		"parent2": `<grammar` + rngNS + `><include href="inc7.rng" ns="urn:x"/><start><ref name="x"/></start>
			<define name="y"><element name="y"><empty/></element></define></grammar>`,
		// An include inside a schema an externalRef brought in with its own ns.
		"ext": `<element name="w"` + rngNS + `><externalRef href="outer8.rng" ns="urn:a"/></element>`,
	}
	for _, c := range []struct {
		schema, doc string
		valid       bool
	}{
		{"eager", `<x xmlns="urn:x"><y xmlns=""/></x>`, true},
		{"eager", `<x xmlns="urn:x"><y/></x>`, false},
		{"lazy", `<x xmlns="urn:x"><y xmlns=""><x xmlns="urn:x"><y xmlns=""/></x></y></x>`, true},
		{"lazy", `<x xmlns="urn:x"><y xmlns=""><x xmlns="urn:x"><y xmlns="urn:x"/></x></y></x>`, false},
		{"choice", `<r><m/></r>`, true},
		{"choice", `<r><i xmlns="urn:x"/></r>`, true},
		{"choice", `<r><i/></r>`, false},
		{"choice", `<r><m xmlns="urn:x"/></r>`, false},
		{"inter", `<r><m/><i xmlns="urn:x"/></r>`, true},
		{"inter", `<r><i xmlns="urn:x"/><m/></r>`, true},
		{"inter", `<r><m/><i/></r>`, false},
		{"inter", `<r><m xmlns="urn:x"/><i xmlns="urn:x"/></r>`, false},
		{"override", `<s xmlns="urn:x"><new/></s>`, true},
		{"override", `<s xmlns="urn:x"><new xmlns=""/></s>`, false},
		{"override", `<s xmlns="urn:x"><old/></s>`, false},
		{"parent", `<x xmlns="urn:x"><inner><y/></inner></x>`, true},
		{"parent", `<x xmlns="urn:x"><inner><y xmlns=""/></inner></x>`, false},
		{"parent", `<x xmlns="urn:x"><inner xmlns=""><y xmlns="urn:x"/></inner></x>`, false},
		{"parent2", `<x xmlns="urn:x"><inner><y xmlns=""/></inner></x>`, true},
		{"parent2", `<x xmlns="urn:x"><inner><y/></inner></x>`, false},
		{"ext", `<w><x xmlns="urn:b"><y xmlns="urn:a"/></x></w>`, true},
		{"ext", `<w><x xmlns="urn:b"><y xmlns="urn:b"/></x></w>`, false},
		{"ext", `<w><x xmlns="urn:a"/></w>`, false},
	} {
		s, err := compileWith(t, schemas[c.schema], Options{Resolver: &mapResolver{docs: docs}})
		if err != nil {
			t.Fatalf("compile %s: %v", c.schema, err)
		}
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(doc.Root); (err == nil) != c.valid {
			t.Errorf("%s: %s: valid = %v, want %v (err %v)", c.schema, c.doc, err == nil, c.valid, err)
		}
	}
}

// An ns="" that is present is a value, not an absence (section 4.8): it puts
// the names below it in no namespace even where an <externalRef> or <include>
// supplies one. It used to read as absent, so the inherited ns won. An
// attribute's name= with no ns= of its own stays in no namespace either way
// (section 4.10). Every verdict here matches Jing 20241231; xmllint differs
// only on the unprefixed QName value under a default namespace.
func TestExplicitEmptyNsBeatsInheritedNs(t *testing.T) {
	const xsd = ` datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes"`
	docs := map[string]string{
		"own.rng":      `<element name="e" ns=""` + rngNS + `><empty/></element>`,
		"anc.rng":      `<element name="o"` + rngNS + `><group ns=""><element name="i"><empty/></element></group></element>`,
		"attrname.rng": `<element name="o"` + rngNS + `><attribute ns=""><name>a</name></attribute></element>`,
		"attrinh.rng":  `<element name="o"` + rngNS + `><attribute><name>a</name></attribute></element>`,
		"attrattr.rng": `<element name="o"` + rngNS + `><attribute name="a"/></element>`,
		"nsname.rng":   `<element ns=""` + rngNS + `><nsName/><empty/></element>`,
		"nsinh.rng":    `<element` + rngNS + `><nsName/><empty/></element>`,
		"qn.rng":       `<element name="v"` + rngNS + `><value type="QName"` + xsd + ` ns="">foo</value></element>`,
		"inc11.rng": `<grammar` + rngNS + `><start><element name="o"><ref name="d"/></element></start>
			<define name="d"><element name="i" ns=""><empty/></element></define></grammar>`,
	}
	for _, c := range []struct {
		href, doc string
		valid     bool
	}{
		{"own.rng", `<w><e/></w>`, true},
		{"own.rng", `<w><e xmlns="urn:a"/></w>`, false},
		{"anc.rng", `<w><o xmlns="urn:a"><i xmlns=""/></o></w>`, true},
		{"anc.rng", `<w><o xmlns="urn:a"><i/></o></w>`, false},
		{"attrname.rng", `<w><o xmlns="urn:a" a="1"/></w>`, true},
		{"attrname.rng", `<w><o xmlns="urn:a" xmlns:p="urn:a" p:a="1"/></w>`, false},
		{"attrinh.rng", `<w><o xmlns="urn:a" xmlns:p="urn:a" p:a="1"/></w>`, true},
		{"attrinh.rng", `<w><o xmlns="urn:a" a="1"/></w>`, false},
		{"attrattr.rng", `<w><o xmlns="urn:a" a="1"/></w>`, true},
		{"attrattr.rng", `<w><o xmlns="urn:a" xmlns:p="urn:a" p:a="1"/></w>`, false},
		{"nsname.rng", `<w><foo/></w>`, true},
		{"nsname.rng", `<w><foo xmlns="urn:a"/></w>`, false},
		{"nsinh.rng", `<w><foo xmlns="urn:a"/></w>`, true},
		{"nsinh.rng", `<w><foo/></w>`, false},
		{"qn.rng", `<w><p:v xmlns:p="urn:a">foo</p:v></w>`, true},
		{"qn.rng", `<w><v xmlns="urn:a">foo</v></w>`, false},
		{"qn.rng", `<w><p:v xmlns:p="urn:a">p:foo</p:v></w>`, false},
		{"inc", `<o xmlns="urn:x"><i xmlns=""/></o>`, true},
		{"inc", `<o xmlns="urn:x"><i/></o>`, false},
	} {
		schema := `<element name="w"` + rngNS + `><externalRef href="` + c.href + `" ns="urn:a"/></element>`
		if c.href == "inc" {
			schema = `<grammar` + rngNS + `><include href="inc11.rng" ns="urn:x"/></grammar>`
		}
		s, err := compileWith(t, schema, Options{Resolver: &mapResolver{docs: docs}})
		if err != nil {
			t.Fatalf("compile %s: %v", c.href, err)
		}
		doc, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(doc.Root); (err == nil) != c.valid {
			t.Errorf("%s: %s: valid = %v, want %v (err %v)", c.href, c.doc, err == nil, c.valid, err)
		}
	}
}
