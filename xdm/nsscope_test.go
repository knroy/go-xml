package xdm

import (
	"strings"
	"testing"
)

// nsNames lists every element and attribute of the tree in document order as
// prefix:local={uri}, attributes marked with @, and each element's own
// namespace declaration count as #n when it has any.
func nsNames(tree *Tree) string {
	var b strings.Builder
	var walk func(n *Node)
	walk = func(n *Node) {
		for c := range n.Children() {
			if c.Kind() != KindElement {
				continue
			}
			q := c.Name()
			b.WriteString(" " + q.Lexical() + "={" + q.URI + "}")
			if d := c.NumNamespaceDecls(); d > 0 {
				b.WriteString("#" + string(rune('0'+d)))
			}
			for a := range c.Attrs() {
				q := a.Name()
				b.WriteString(" @" + q.Lexical() + "={" + q.URI + "}")
			}
			walk(c)
		}
	}
	walk(tree.Root)
	return strings.TrimSpace(b.String())
}

// TestParseNamespaceScope checks that each element and attribute resolves
// against the bindings in scope where it is written: a sibling does not see
// an earlier sibling's declarations, a default namespace can be undeclared
// and comes back after the undeclaring element ends, a redeclared prefix
// holds only inside the redeclaring element, and xml is bound without a
// declaration.
func TestParseNamespaceScope(t *testing.T) {
	for _, c := range []struct{ name, doc, want string }{
		{"siblings",
			`<r><a xmlns="urn:a" xmlns:p="urn:p"><p:x/></a><b p:y="1" xmlns:p="urn:q"/><c/></r>`,
			`r={} a={urn:a}#2 p:x={urn:p} b={}#1 @p:y={urn:q} c={}`},
		{"undeclare default",
			`<r xmlns="urn:d"><a xmlns=""><b/></a><c/></r>`,
			`r={urn:d}#1 a={}#1 b={} c={urn:d}`},
		{"redeclared prefix",
			`<p:r xmlns:p="urn:1"><p:a xmlns:p="urn:2" p:at="v"><p:b/></p:a><p:c p:at="w"/></p:r>`,
			`p:r={urn:1}#1 p:a={urn:2}#1 @p:at={urn:2} p:b={urn:2} p:c={urn:1} @p:at={urn:1}`},
		{"unprefixed attribute ignores the default",
			`<r xmlns="urn:d" a="1"/>`,
			`r={urn:d}#1 @a={}`},
		{"xml prefix needs no declaration",
			`<r xml:lang="en"><xml:e/></r>`,
			`r={} @xml:lang={` + NSXML + `} xml:e={` + NSXML + `}`},
		{"xml prefix declared as itself",
			`<r xmlns:xml="` + NSXML + `" xml:space="preserve"/>`,
			`r={}#1 @xml:space={` + NSXML + `}`},
		{"same URI declared twice, one name",
			`<r><a xmlns="urn:x"/><a xmlns="urn:x"/></r>`,
			`r={} a={urn:x}#1 a={urn:x}#1`},
		{"same local name, element and attribute",
			`<id xmlns="urn:x" id="1"><id id="2"/></id>`,
			`id={urn:x}#1 @id={} id={urn:x} @id={}`},
		{"many attributes resolve through the map",
			`<r xmlns:p="urn:p"><e xmlns:q="urn:q" a="1" b="2" c="3" d="4" p:a="5" q:a="6" p:b="7" q:b="8" e="9"/><p:f/></r>`,
			`r={}#1 e={}#1 @a={} @b={} @c={} @d={} @p:a={urn:p} @q:a={urn:q} @p:b={urn:p} @q:b={urn:q} @e={} p:f={urn:p}`},
		{"deep nesting pops back",
			`<a xmlns:p="urn:1"><b xmlns:p="urn:2"><c xmlns:p="urn:3"><p:x/></c><p:y/></b><p:z/></a>`,
			`a={}#1 b={}#1 c={}#1 p:x={urn:3} p:y={urn:2} p:z={urn:1}`},
	} {
		tree, err := ParseString(c.doc, ParseOptions{})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := nsNames(tree); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}

	// Equal names written in different storage are one name.
	tree, err := ParseString(`<r><a xmlns="urn:x"/><a xmlns="urn:x"/></r>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := tree.Root.FirstChild()
	if a, b := r.FirstChild(), r.LastChild(); a.name != b.name {
		t.Errorf("name indexes %d and %d, want one", a.name, b.name)
	}
}

// TestParseNamespaceErrors pins the messages of the namespace
// well-formedness errors, which a scope change must not move.
func TestParseNamespaceErrors(t *testing.T) {
	for _, c := range []struct{ doc, want string }{
		{`<p:r/>`, `parse XML: no namespace declaration is in scope for prefix "p"`},
		{`<r p:a="1"/>`, `parse XML: no namespace declaration is in scope for prefix "p"`},
		{`<r><a xmlns:p="urn:p"/><p:b/></r>`, `parse XML: no namespace declaration is in scope for prefix "p"`},
		{`<r><a xmlns:p="urn:p"/><b p:c="1"/></r>`, `parse XML: no namespace declaration is in scope for prefix "p"`},
		{`<xmlns:r/>`, `parse XML: no namespace declaration is in scope for prefix "xmlns"`},
		{`<r xmlns:xmlns="urn:x"/>`, `parse XML: xmlns prefix must not be declared`},
		{`<r xmlns:p="` + NSXMLNS + `"/>`, `parse XML: namespace URI "` + NSXMLNS + `" is reserved for xmlns`},
		{`<r xmlns:xml="urn:x"/>`, `parse XML: xml prefix must be bound to "` + NSXML + `"`},
		{`<r xmlns:p="` + NSXML + `"/>`, `parse XML: only xml prefix may be bound to "` + NSXML + `"`},
		{`<r xmlns:p="urn:p"><a xmlns:p=""/></r>`, `parse XML: XML 1.0 does not allow undeclaring prefix "p"`},
		{`<r xmlns:p="urn:a" xmlns:p="urn:b"/>`, `parse XML: duplicate namespace declaration for prefix "p"`},
		{`<r xmlns:p="urn:a" xmlns:q="urn:a" p:x="1" q:x="2"/>`, `parse XML: duplicate attribute {urn:a}x`},
		{`<?xml version="1.1"?><r xmlns:p="urn:p"><a xmlns:p=""><p:b/></a></r>`, `parse XML: no namespace declaration is in scope for prefix "p"`},
	} {
		_, err := ParseString(c.doc, ParseOptions{})
		if err == nil || err.Error() != c.want {
			t.Errorf("%s:\n got  %v\n want %s", c.doc, err, c.want)
		}
	}
}
