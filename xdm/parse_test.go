package xdm

import (
	"strings"
	"testing"
)

func TestParseBasicTree(t *testing.T) {
	tree, err := ParseString(`<a><b>x</b><c/></a>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Root
	if root.Kind != KindDocument {
		t.Fatalf("root kind = %v, want document", root.Kind)
	}
	els := root.ChildElements()
	if len(els) != 1 || els[0].Name.Local != "a" {
		t.Fatalf("expected single element child 'a', got %v", els)
	}
	if got := els[0].StringValue(); got != "x" {
		t.Errorf("string value = %q, want %q", got, "x")
	}
}

func TestNamespacesBecomeNamespaceNodesNotAttributes(t *testing.T) {
	// The attribute axis must not return xmlns declarations.
	tree, err := ParseString(`<a xmlns="urn:d" xmlns:p="urn:p" p:k="v" plain="w"/>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Root.ChildElements()[0]
	if len(a.Attrs) != 2 {
		t.Errorf("got %d attributes, want 2 (xmlns must not be an attribute)", len(a.Attrs))
		for _, at := range a.Attrs {
			t.Logf("  attr %s", at.Name.Clark())
		}
	}
	if len(a.Namespaces) != 2 {
		t.Errorf("got %d namespace nodes, want 2", len(a.Namespaces))
	}
	if a.Name.URI != "urn:d" {
		t.Errorf("element URI = %q, want urn:d", a.Name.URI)
	}
	if at := a.Attr("urn:p", "k"); at == nil || at.Value != "v" {
		t.Error("prefixed attribute p:k not found under its resolved URI")
	}
	// An unprefixed attribute is in no namespace, even with a default xmlns.
	if at := a.Attr("", "plain"); at == nil || at.Value != "w" {
		t.Error("unprefixed attribute should be in no namespace, not the default one")
	}
}

func TestPrefixIsPreservedForSerialisation(t *testing.T) {
	tree, err := ParseString(`<p:a xmlns:p="urn:p"/>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Root.ChildElements()[0]
	if a.Name.Prefix != "p" {
		t.Errorf("prefix = %q, want %q (needed to serialise as the author wrote it)", a.Name.Prefix, "p")
	}
	if got := a.Name.Lexical(); got != "p:a" {
		t.Errorf("lexical = %q, want p:a", got)
	}
}

func TestAdjacentTextNodesAreMerged(t *testing.T) {
	// The XDM forbids adjacent text nodes; entity references must not split one.
	tree, err := ParseString(`<a>x&amp;y</a>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Root.ChildElements()[0]
	texts := 0
	for _, c := range a.Children {
		if c.Kind == KindText {
			texts++
		}
	}
	if texts != 1 {
		t.Errorf("got %d text nodes, want 1 merged node", texts)
	}
	if got := a.StringValue(); got != "x&y" {
		t.Errorf("string value = %q, want %q", got, "x&y")
	}
}

func TestDOCTYPERejectedByDefault(t *testing.T) {
	// XXE and billion-laughs both enter through the DOCTYPE.
	doc := `<!DOCTYPE a [<!ENTITY x "boom">]><a/>`
	if _, err := ParseString(doc, ParseOptions{}); err == nil {
		t.Fatal("DOCTYPE accepted by default; it must be rejected")
	}
	if _, err := ParseString(doc, ParseOptions{AllowDOCTYPE: true}); err != nil {
		t.Fatalf("DOCTYPE rejected even with AllowDOCTYPE: %v", err)
	}
}

func TestDepthLimit(t *testing.T) {
	deep := strings.Repeat("<a>", 200) + strings.Repeat("</a>", 200)
	if _, err := ParseString(deep, ParseOptions{MaxDepth: 50}); err == nil {
		t.Fatal("depth limit not enforced")
	}
	if _, err := ParseString(deep, ParseOptions{MaxDepth: 500}); err != nil {
		t.Fatalf("depth 200 rejected under limit 500: %v", err)
	}
}

func TestMalformedRejected(t *testing.T) {
	for _, doc := range []string{
		`<a><b></a>`, // mismatched
		`<a/><b/>`,   // two roots
		`text<a/>`,   // chardata outside root
		``,           // no root
		`<a>`,        // unclosed
	} {
		if _, err := ParseString(doc, ParseOptions{}); err == nil {
			t.Errorf("malformed input %q was accepted", doc)
		}
	}
}

func TestDocumentOrder(t *testing.T) {
	tree, err := ParseString(`<a><b/><c><d/></c></a>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Root.ChildElements()[0]
	b := a.ChildElements()[0]
	c := a.ChildElements()[1]
	d := c.ChildElements()[0]

	// Document order: a, b, c, d
	if !(a.Compare(b) < 0 && b.Compare(c) < 0 && c.Compare(d) < 0) {
		t.Errorf("document order wrong: a=%d b=%d c=%d d=%d",
			a.Order(), b.Order(), c.Order(), d.Order())
	}
	if a.Compare(a) != 0 {
		t.Error("node should compare equal to itself")
	}
	if d.Compare(b) <= 0 {
		t.Error("reverse comparison should be positive")
	}
}

func TestSortDocumentOrderDedups(t *testing.T) {
	tree, _ := ParseString(`<a><b/><c/></a>`, ParseOptions{})
	a := tree.Root.ChildElements()[0]
	b := a.ChildElements()[0]
	c := a.ChildElements()[1]

	got := SortDocumentOrder(Sequence{c, b, c, a, b})
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3 after dedup", len(got))
	}
	if got[0] != Item(a) || got[1] != Item(b) || got[2] != Item(c) {
		t.Error("not sorted into document order")
	}
}

func TestStripSpace(t *testing.T) {
	doc := `<a>  <b>keep me</b>  </a>`
	stripAll := func(QName) bool { return true }
	tree, err := ParseString(doc, ParseOptions{StripSpace: stripAll})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Root.ChildElements()[0]
	for _, c := range a.Children {
		if c.Kind == KindText {
			t.Errorf("whitespace-only text node survived stripping: %q", c.Value)
		}
	}
	// Non-whitespace text must survive.
	if got := a.StringValue(); got != "keep me" {
		t.Errorf("string value = %q, want %q", got, "keep me")
	}
}

func TestXMLSpacePreserveOverridesStrip(t *testing.T) {
	doc := `<a xml:space="preserve">  <b/>  </a>`
	stripAll := func(QName) bool { return true }
	tree, err := ParseString(doc, ParseOptions{StripSpace: stripAll})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Root.ChildElements()[0]
	found := false
	for _, c := range a.Children {
		if c.Kind == KindText {
			found = true
		}
	}
	if !found {
		t.Error("xml:space=preserve did not protect whitespace from stripping")
	}
}

func TestInScopeNamespacesShadowing(t *testing.T) {
	tree, err := ParseString(
		`<a xmlns:p="urn:outer"><b xmlns:p="urn:inner" xmlns:q="urn:q"/></a>`,
		ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Root.ChildElements()[0]
	b := a.ChildElements()[0]

	ns := b.InScopeNamespaces()
	if ns["p"] != "urn:inner" {
		t.Errorf("inner p = %q, want urn:inner (inner must shadow outer)", ns["p"])
	}
	if ns["q"] != "urn:q" {
		t.Errorf("q = %q, want urn:q", ns["q"])
	}
	if got, _ := a.LookupPrefix("p"); got != "urn:outer" {
		t.Errorf("outer p = %q, want urn:outer", got)
	}
}

func TestCommentsAndPIs(t *testing.T) {
	tree, err := ParseString(`<a><!--c--><?pi data?>text</a>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Root.ChildElements()[0]
	var kinds []NodeKind
	for _, c := range a.Children {
		kinds = append(kinds, c.Kind)
	}
	want := []NodeKind{KindComment, KindPI, KindText}
	if len(kinds) != len(want) {
		t.Fatalf("got kinds %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("child %d kind = %v, want %v", i, kinds[i], want[i])
		}
	}
	// Comments and PIs contribute nothing to an ancestor's string value.
	if got := a.StringValue(); got != "text" {
		t.Errorf("string value = %q, want %q (comments/PIs must not contribute)", got, "text")
	}
}
