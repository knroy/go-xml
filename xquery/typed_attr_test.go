package xquery_test

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xquery"
)

// An attribute that reaches element content as a value -- "<e>{$a}</e>", where
// $a is an attribute node -- is added through the builder's attribute entry
// point rather than appended as a child. That call used to take an annotation
// NAME and nothing else, so the attribute arrived in the constructed element
// with everything its assessment concluded beyond the name discarded: the
// union member, the built-in the type erases to, the item type of a list. Each
// of those then had to be re-derived from xdm's process-global registries,
// which are keyed by QName alone across the whole process and answer for
// whichever schema registered the name most recently. This is audit finding
// 24, and this test pins the XQuery half of the fix.
//
// No schema is imported and no type is registered anywhere. That is the point:
// the annotation names a type nothing in the process can resolve, so if the
// constructed attribute still atomises as a list of decimals, the answer can
// only have come from the metadata carried on the node itself.
func TestConstructedAttributeKeepsResolvedTyping(t *testing.T) {
	const ns = "urn:go-xml:finding24:xquery"

	// An attribute as a validator would leave it: a list type whose item type
	// the schema resolved to xs:decimal, recorded on the node.
	src := &xdm.Node{Kind: xdm.KindAttribute,
		Name: xdm.QName{Local: "a"}, Value: "10 20"}
	src.ApplyTyping(xdm.Typing{
		TypeAnnotation:   xdm.AnnotationName(ns, "L"),
		DerivedPrimitive: "anySimpleType",
		ListItem:         "decimal",
	})

	// The attribute is reached as the context item, which is the shortest
	// route to putting a pre-assessed node into a query.
	q, err := xquery.Compile(`<e>{.}</e>`, xquery.Options{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	ctx := xpath.NewContext(src, xpath.Builtins())
	seq, err := q.Eval(ctx)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if len(seq) != 1 {
		t.Fatalf("query produced %d items, want 1", len(seq))
	}
	el, ok := seq[0].(*xdm.Node)
	if !ok || el.Kind != xdm.KindElement {
		t.Fatalf("query produced %T, want an element", seq[0])
	}
	if len(el.Attrs) != 1 {
		t.Fatalf("constructed element carries %d attributes, want 1",
			len(el.Attrs))
	}
	got := el.Attrs[0]

	if want := xdm.TypingOf(src); xdm.TypingOf(got) != want {
		t.Fatalf("constructed attribute carries %+v, want %+v",
			xdm.TypingOf(got), want)
	}
	// The resolved item type is what makes the value a list of decimals. With
	// only the name the node has nothing to split on, and nothing in the
	// process can tell it what {urn:...}L means.
	items, ok := got.AtomizeList()
	if !ok {
		t.Fatal("the constructed attribute did not atomise as a list; the " +
			"builder dropped ListItem and no registry can supply it")
	}
	if len(items) != 2 {
		t.Fatalf("atomised into %d items, want 2", len(items))
	}
	for i, it := range items {
		a, ok := it.(*xdm.Atomic)
		if !ok {
			t.Fatalf("item %d is %T, want an atomic value", i, it)
		}
		if a.Type != xdm.TypeDecimal {
			t.Fatalf("item %d is %s, want xs:decimal", i, a.TypeName())
		}
	}
}
