package xslt

import (
	"reflect"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// During a compilation, elements that declare no namespace share their
// nearest declaring ancestor's in-scope map instead of building one each;
// an element that declares one gets its own; and a change to
// the tree's namespaces is seen once forgetSharedNS has run.
func TestInScopeNamespacesShared(t *testing.T) {
	// <a xmlns:p="urn:p"><b/><c xmlns:p="urn:q"><d/></c><e/></a>, built
	// rather than parsed so that a stays open for the declaration added below.
	tree := xdm.NewTree()
	a := tree.Root.AppendElement(xdm.QName{Local: "a"})
	a.AddNamespace("p", "urn:p")
	b := a.AppendElement(xdm.QName{Local: "b"})
	c := a.AppendElement(xdm.QName{Local: "c"})
	c.AddNamespace("p", "urn:q")
	d := c.AppendElement(xdm.QName{Local: "d"})
	e := a.AppendElement(xdm.QName{Local: "e"})

	setSharedNS(map[*xdm.Node]map[string]string{})
	defer setSharedNS(nil)
	ptr := func(m map[string]string) uintptr { return reflect.ValueOf(m).Pointer() }

	mb, me := inScopeNamespacesShared(b), inScopeNamespacesShared(e)
	if ptr(mb) != ptr(me) || ptr(mb) != ptr(inScopeNamespacesShared(a)) {
		t.Error("b, e and a do not share a's map")
	}
	if mb["p"] != "urn:p" {
		t.Errorf("b: p = %q, want urn:p", mb["p"])
	}
	for _, n := range []*xdm.Node{c, d} {
		got := inScopeNamespacesShared(n)
		if !reflect.DeepEqual(got, n.InScopeNamespaces()) || got["p"] != "urn:q" {
			t.Errorf("%s: got %v, want %v", n.Name().Local, got, n.InScopeNamespaces())
		}
	}

	// Add a declaration to a.
	a.AddNamespace("x", "urn:x")
	forgetSharedNS()
	if got := inScopeNamespacesShared(b); got["x"] != "urn:x" {
		t.Errorf("after a declares x: b sees x = %q, want urn:x", got["x"])
	}

	// Outside a compilation nothing is cached.
	setSharedNS(nil)
	if ptr(inScopeNamespacesShared(b)) == ptr(inScopeNamespacesShared(b)) {
		t.Error("maps shared outside a compilation")
	}
}
