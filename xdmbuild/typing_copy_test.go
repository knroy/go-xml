package xdmbuild

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// DeepCopy is what xsl:copy-of, fn:snapshot and xsl:merge build result trees
// with, so it is the point where a result tree either keeps the typing its
// source was validated with or silently loses it.
//
// Losing it would not show up as an error. The copy would keep the type NAME
// and have to ask the process-global derivation registries what that name
// means -- and those are keyed by QName alone, so they answer for whichever
// schema loaded last rather than for the schema that validated the source.
// That is the same bug the resolved fields exist to prevent, reintroduced one
// layer up, which is why the propagation is pinned here directly rather than
// only through a schema-driven test.

// TestDeepCopyPropagatesResolvedTyping pins the fields on the element, on its
// attributes, and on a descendant, since each is a separate construction site
// inside DeepCopy and a field can be forgotten at any one of them.
func TestDeepCopyPropagatesResolvedTyping(t *testing.T) {
	src := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "e"}}
	src.SetTypeAnnotationResolved("{urn:x}T", "decimal", "")
	src.AddAttr(&xdm.Node{
		Kind: xdm.KindAttribute, Name: xdm.QName{Local: "a"}, Value: "1 2",
		TypeAnnotation: "{urn:x}L", DerivedPrimitive: "", ListItem: "integer",
	})
	kid := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "k"}}
	kid.SetTypeAnnotationResolved("{urn:x}K", "double", "")
	src.AppendChild(kid)

	c := DeepCopy(src)

	if c.DerivedPrimitive != "decimal" {
		t.Errorf("element lost DerivedPrimitive: %q, want %q",
			c.DerivedPrimitive, "decimal")
	}
	if got := c.Attrs[0].ListItem; got != "integer" {
		t.Errorf("attribute lost ListItem: %q, want %q", got, "integer")
	}
	if got := c.Attrs[0].TypeAnnotation; got != "{urn:x}L" {
		t.Errorf("attribute lost TypeAnnotation: %q", got)
	}
	if got := c.Children[0].DerivedPrimitive; got != "double" {
		t.Errorf("descendant lost DerivedPrimitive: %q, want %q", got, "double")
	}
}

// TestDeepCopyTypingBeatsTheRegistry is the property that matters rather than
// the field plumbing: a copy must atomise from what it carries, even when the
// registry has since been told something else about the same name.
func TestDeepCopyTypingBeatsTheRegistry(t *testing.T) {
	const name = "{urn:copyprobe}T"
	xdm.RegisterDerivedType(name, "decimal")

	src := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "e"}}
	src.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: "10"})
	src.SetTypeAnnotationResolved(name, "decimal", "")

	c := DeepCopy(src)
	if got := c.Atomize().Type; got != xdm.TypeDecimal {
		t.Fatalf("precondition: copy atomised as %v, want %v",
			got, xdm.TypeDecimal)
	}

	// Something else redefines the same QName, exactly as a second schema
	// load would. The copy was already made and must not change.
	xdm.RegisterDerivedType(name, "string")

	if got := c.Atomize().Type; got != xdm.TypeDecimal {
		t.Errorf("a copied node followed the registry instead of its own "+
			"recorded typing: atomised as %v, want %v", got, xdm.TypeDecimal)
	}
}

// TestDeepCopyListTypingBeatsTheRegistry is the same for the list registry,
// where the corruption changes the ITEM type while leaving the item count
// intact -- so a test that only counted items would not see it.
func TestDeepCopyListTypingBeatsTheRegistry(t *testing.T) {
	const name = "{urn:copyprobe}L"
	xdm.RegisterListType(name, "decimal")

	src := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "l"}}
	src.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: "10 20"})
	src.SetTypeAnnotationResolved(name, "", "decimal")

	c := DeepCopy(src)
	items, ok := c.AtomizeList()
	if !ok || len(items) != 2 {
		t.Fatalf("precondition: ok=%v n=%d", ok, len(items))
	}

	xdm.RegisterListType(name, "string")

	items, ok = c.AtomizeList()
	if !ok {
		t.Fatal("copied list stopped being a list")
	}
	if len(items) != 2 {
		t.Errorf("item count changed to %d, want 2", len(items))
	}
	if got := items[0].(*xdm.Atomic).Type; got != xdm.TypeDecimal {
		t.Errorf("a copied list followed the registry for its item type: "+
			"item atomised as %v, want %v", got, xdm.TypeDecimal)
	}
}

// TestDeepCopyOfUnresolvedNodeStaysUnresolved guards the fallback direction: a
// copy of a node that carries no resolved typing must not invent any, or it
// would freeze whatever the registry happened to say at copy time into a node
// that should keep consulting it.
func TestDeepCopyOfUnresolvedNodeStaysUnresolved(t *testing.T) {
	src := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "e"}}
	src.SetTypeAnnotation("{urn:x}Unresolved")

	c := DeepCopy(src)
	if c.DerivedPrimitive != "" || c.ListItem != "" {
		t.Errorf("DeepCopy invented resolved typing: %q %q",
			c.DerivedPrimitive, c.ListItem)
	}
}

// TestClearedAnnotationDropsResolvedTyping is the regression test for the bug
// this descriptor introduced on its first attempt, which the XSLT 2.0 suite
// caught as as-3002 and as-1811.
//
// A node that has been stripped of its annotation must atomise as untyped: ONE
// item holding the whole string. The resolved fields describe what an
// annotation means, so when there is no annotation there is nothing for them
// to describe -- and a node that kept a list item type without a list type
// still split into tokens. The assertion "elem = 'one two three'" then
// compared three NMTOKENs against the whole string and answered false, on a
// result tree that had been correctly stripped.
func TestClearedAnnotationDropsResolvedTyping(t *testing.T) {
	n := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "lb"}}
	n.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: "one two three"})
	n.SetTypeAnnotationResolved("NMTOKENS", "anySimpleType", "NMTOKEN")

	if got := len(xdm.Atomize(xdm.One(n))); got != 3 {
		t.Fatalf("precondition: an annotated list atomised to %d items, want 3", got)
	}

	// Clearing the annotation must take its meaning with it.
	n.SetTypeAnnotation("")
	if n.ListItem != "" || n.DerivedPrimitive != "" {
		t.Errorf("clearing the annotation left resolved fields: %q %q",
			n.DerivedPrimitive, n.ListItem)
	}
	if got := len(xdm.Atomize(xdm.One(n))); got != 1 {
		t.Errorf("an unannotated node atomised to %d items, want 1 "+
			"(it is untyped, so its typed value is the whole string)", got)
	}

	// Even a node whose fields were set behind the setter's back must not
	// atomise as a list while carrying no annotation, since a copy built by a
	// struct literal elsewhere can reach that state.
	m := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "lb"},
		ListItem: "NMTOKEN"}
	m.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: "one two three"})
	if got := len(xdm.Atomize(xdm.One(m))); got != 1 {
		t.Errorf("a node with no annotation but a stray ListItem atomised to "+
			"%d items, want 1", got)
	}
}
