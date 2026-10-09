package xdm

import "testing"

// TestAppendBuildsInDocumentOrder builds a small tree top-down and checks the
// shape, the attribute placement, and that Copy reproduces it as a new tree.
func TestAppendBuildsInDocumentOrder(t *testing.T) {
	tree := NewTree()
	a := tree.Root.AppendElement(QName{Local: "a"})
	a.AddNamespace("p", "urn:p")
	a.AppendAttr(QName{Local: "x"}, "1")
	b := a.AppendElement(QName{Prefix: "p", URI: "urn:p", Local: "b"})
	b.AppendText("hi")
	a.AppendComment("c")
	tree.Finalize()

	if got := a.StringValue(); got != "hi" {
		t.Fatalf("string value %q", got)
	}
	if a.NumAttrs() != 1 || a.AttrAt(0).Parent() != a || a.NumChildren() != 2 {
		t.Fatalf("shape: attrs %d children %d", a.NumAttrs(), a.NumChildren())
	}
	if b.Compare(a.AttrAt(0)) <= 0 || a.Compare(b) >= 0 {
		t.Fatalf("document order wrong")
	}
	c := Copy(a)
	if c == a || c.Parent() != nil || c.StringValue() != "hi" || c.NumChildren() != 2 ||
		c.Attr("", "x").Value() != "1" {
		t.Fatalf("copy differs")
	}
	if uri, ok := c.FirstChild().LookupPrefix("p"); !ok || uri != "urn:p" {
		t.Fatalf("copy lost a namespace declaration")
	}
	var prefixes []string
	for ns := range b.NamespaceNodes() {
		prefixes = append(prefixes, ns.Name().Local)
	}
	if len(prefixes) != 2 || prefixes[0] != "p" || prefixes[1] != "xml" {
		t.Fatalf("namespace axis %v", prefixes)
	}
}
