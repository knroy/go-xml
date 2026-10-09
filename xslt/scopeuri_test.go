package xslt

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestScopeURIMatchesInScopeNamespaces: fixupNamespaces reads bindings with
// scopeURI instead of building InScopeNamespaces, so the two must agree on
// every prefix -- including the shapes namespace fixup itself creates: one
// element carrying the same prefix twice (the last declaration wins), an
// undeclaration shadowing an outer binding, and the implicit xml prefix.
func TestScopeURIMatchesInScopeNamespaces(t *testing.T) {
	tree := xdm.NewTree()
	outer := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "outer"}, Parent: tree.Root}
	outer.AddNamespace("", "urn:default")
	outer.AddNamespace("a", "urn:a1")
	outer.AddNamespace("b", "urn:b")
	inner := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "inner"}, Parent: outer}
	inner.AddNamespace("a", "urn:a2")
	inner.AddNamespace("a", "urn:a3") // the later one is in scope
	inner.AddNamespace("", "")        // undeclares the outer default
	inner.AddNamespace("c", "urn:c")

	scope := inner.InScopeNamespaces()
	for _, p := range []string{"", "a", "b", "c", "xml", "unbound"} {
		if got, want := scopeURI(inner, p), scope[p]; got != want {
			t.Errorf("prefix %q: scopeURI %q, InScopeNamespaces %q", p, got, want)
		}
	}
	if got := scopeURI(inner, "a"); got != "urn:a3" {
		t.Errorf(`prefix "a": got %q, want the last declaration "urn:a3"`, got)
	}
}
