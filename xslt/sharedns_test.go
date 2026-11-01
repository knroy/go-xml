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

// SchXslt redeclares one prefix on each of the CEN rules' svrl:failed-assert
// elements. Elements that each declare the same bindings share one map, one
// resolver and one binding list in the compiled stylesheet, rather than a copy
// per element.
func TestNamespacesInternedByContent(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:template name="t">
  <p:out xmlns:p="urn:p" a="{1}"/>
  <p:out xmlns:p="urn:p" a="{2}"/>
</xsl:template>
</xsl:stylesheet>`
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var outs []*xdm.Node
	for n := range doc.Root.Descendants() {
		if n.Kind() == xdm.KindElement && n.Name().Local == "out" {
			outs = append(outs, n)
		}
	}
	setSharedNS(map[*xdm.Node]map[string]string{})
	ptr := func(m map[string]string) uintptr { return reflect.ValueOf(m).Pointer() }
	if ptr(inScopeNamespacesShared(outs[0])) != ptr(inScopeNamespacesShared(outs[1])) {
		t.Error("two elements declaring the same bindings do not share a map")
	}
	if newNSResolver(outs[0], "") != newNSResolver(outs[1], "") {
		t.Error("two elements with the same static context do not share a resolver")
	}
	setSharedNS(nil)

	st, err := Compile(doc.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var lits []*literalElemInstr
	for _, in := range st.named["t"].Body {
		if l, ok := in.(*literalElemInstr); ok {
			lits = append(lits, l)
		}
	}
	if len(lits) != 2 || len(lits[0].namespaces) == 0 || &lits[0].namespaces[0] != &lits[1].namespaces[0] {
		t.Error("two literal elements with the same bindings do not share one list")
	}
}
