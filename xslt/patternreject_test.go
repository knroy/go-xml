package xslt

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// Pattern.matches rejects a candidate that no alternative's last step accepts
// on its node test before it binds current() and the output URI, because the
// two context copies were most of what a failed match cost. The early answer
// has to agree with the full walk on every form a pattern can take: an
// attribute or namespace step, a document node, an id()/key() alternative and
// an XSLT 3.0 general pattern are not walked from a last step that the node
// test alone settles.
func TestPatternRejectsOnNodeTestFirst(t *testing.T) {
	tree, err := xdm.ParseString(`<doc xmlns:z="urn:z" a="1"><p xml:id="x" id="x"/><q/></doc>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc := tree.Root
	el := doc.Children[0]
	attr := el.Attrs[0]
	p, q := el.Children[0], el.Children[1]
	var ns *xdm.Node
	for _, n := range el.Namespaces {
		if n.Name.Local == "z" {
			ns = n
		}
	}
	if ns == nil {
		t.Fatal("no namespace node for prefix z")
	}
	ctx := xpath.NewContext(doc, xpath.Builtins())

	cases := []struct {
		pattern string
		node    *xdm.Node
		want    bool
	}{
		{"p", p, true},
		{"p", q, false},
		{"doc/p", p, true},
		{"q|p", p, true},
		{"q|r", p, false},
		{"p[@id = 'x']", p, true},
		{"q[@id = 'x']", p, false},
		{"@a", attr, true},
		{"@a", el, false},
		{"a", attr, false},
		{"node()", attr, false},
		{"node()", doc, false},
		{"/", doc, true},
		{"/", el, false},
		{"id('x')", p, true},
		{"id('x')", q, false},
		{"doc//q", q, true},
		{"document-node(element(doc))", doc, true},
		{"document-node(element(doc))", el, false},
		{"document-node(element(q))", doc, false},
		{"*[2]", q, true},
		{"*[2]", p, false},
		{"q[last()]", q, true},
		{"doc/descendant::q[1]", q, true},
		{"doc/descendant::q[1]", p, false},
		{"q|@*", attr, true},
		{"text()", attr, false},
		{"namespace-node()", ns, true},
		{"namespace-node()", el, false},
		{"namespace::z", ns, true},
		{"*", ns, false},
		{"node()", ns, false},
		{".[true()]", q, true},
		{".[self::q]", q, true},
	}
	for _, c := range cases {
		pat, err := CompilePattern(c.pattern, nil)
		if err != nil {
			t.Fatalf("CompilePattern(%q): %v", c.pattern, err)
		}
		got, err := pat.Matches(c.node, ctx)
		if err != nil {
			t.Errorf("%q against %s: %v", c.pattern, c.node.Name.Local, err)
		} else if got != c.want {
			t.Errorf("%q against <%s> kind %v = %v, want %v",
				c.pattern, c.node.Name.Local, c.node.Kind, got, c.want)
		}
	}

	// A rejection on the node test binds nothing, so it allocates nothing.
	pat, err := CompilePattern("q|r[@id]", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(100, func() {
		if ok, _ := pat.Matches(p, ctx); ok {
			t.Fatal("q|r[@id] matched <p>")
		}
	}); n != 0 {
		t.Errorf("rejecting <p> allocated %v times, want 0", n)
	}
}
