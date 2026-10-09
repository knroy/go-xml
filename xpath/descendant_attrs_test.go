package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// "//@a" is walked in one pass (fuseDescendantAttrs). It must select exactly
// what the two steps select when run as written, in the same order, and raise
// the same errors; the reference spells each path with a self::node() step in
// between, which defeats the fusion.
func TestDescendantAttrsFusion(t *testing.T) {
	tree, err := xdm.ParseString(
		`<a xmlns:m="urn:meta" id="0" m:id="p0" xml:lang="en"><b id="1"><c id="2" x="y"/>t<b id="3">`+
			`<c id="4" m:id="p4">t<c/></c></b></b><!--c--><?c x?><m:c id="6"><m:d xml:lang="fr"/></m:c></a>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ns := testNS{}
	for _, src := range []string{
		"//@id", "//@*", "//@m:*", "//@*:id", "//@xml:lang", "//attribute()", "//node()",
		"/a//@id", "//b//@id", "(//b, //c)//@id", "//c//@*", "//@id/..", "count(//@*)",
		"//@nothere", "/a/@id//@id", "//comment()//@*", "(1)//@id", "//b/(.//@id)",
	} {
		ref := strings.ReplaceAll(src, "//", "/descendant-or-self::node()/self::node()/")
		for _, ctxNode := range []*xdm.Node{tree.Root, tree.Root.FirstChild(), tree.Root.FirstChild().AttrAt(0)} {
			got, gerr := MustCompile(src, ns).Eval(NewContext(ctxNode, Builtins()))
			want, werr := MustCompile(ref, ns).Eval(NewContext(ctxNode, Builtins()))
			if (gerr == nil) != (werr == nil) || gerr != nil && gerr.Error() != werr.Error() {
				t.Fatalf("%s from %s: error %v, want %v", src, ctxNode.Kind, gerr, werr)
			}
			if len(got) != len(want) {
				t.Fatalf("%s from %s: %d items, want %d", src, ctxNode.Kind, len(got), len(want))
			}
			for i := range got {
				ga, gok := got[i].(*xdm.Atomic)
				wa, wok := want[i].(*xdm.Atomic)
				if got[i] != want[i] && !(gok && wok && ga.String() == wa.String()) {
					t.Fatalf("%s from %s: item %d differs", src, ctxNode.Kind, i)
				}
			}
		}
	}

	// The fusion fires on parsed nodes, and not on a constructed tree, whose
	// root a sort numbers on first sight, nor on a non-node operand.
	p := MustCompile("//@id", nil).Expr().(*PathExpr)
	if fuseDescendantAttrs(p.Steps, 0, xdm.One(tree.Root)) == nil {
		t.Error("//@id over a parsed tree is not fused")
	}
	if fuseDescendantAttrs(p.Steps, 0, xdm.One(xdm.NewNode(xdm.KindElement, xdm.QName{}, ""))) != nil {
		t.Error("//@id over a constructed tree is fused")
	}
	if fuseDescendantAttrs(p.Steps, 0, xdm.One(xdm.NewString("x"))) != nil {
		t.Error("//@id over a string is fused")
	}
	if q := MustCompile("//@id[1]", nil).Expr().(*PathExpr); fuseDescendantAttrs(q.Steps, 0, xdm.One(tree.Root)) != nil {
		t.Error("//@id[1] is fused")
	}
}
