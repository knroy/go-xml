package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// descendant::name and descendant-or-self::name walk the tree directly with
// the name test inlined. They must select exactly what the generic axis walk
// selects, in the same order, from every kind of context node.
func TestNamedDescendantWalk(t *testing.T) {
	tree, err := xdm.ParseString(
		`<a xmlns:p="urn:p" id="0"><b id="1"><c id="2"/><b id="3"><c id="4">t<c/></c></b></b>`+
			`<c id="5"/>t<!--c--><?c x?><p:c id="6"><p:d/></p:c></a>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var contexts []*xdm.Node
	walkAxis(tree.Root, AxisDescendantOrSelf, func(n *xdm.Node) bool {
		contexts = append(contexts, n)
		contexts = append(contexts, n.Attrs...)
		return true
	})
	p := "urn:p"
	tests := []*NameTest{
		{Name: xdm.QName{Local: "c"}},
		{Name: xdm.QName{Local: "b"}},
		{Name: xdm.QName{Local: "zz"}},
		{Name: xdm.QName{URI: p, Local: "c"}},
		{AnyURI: true, Name: xdm.QName{Local: "c"}},
		{AnyLocal: true, Name: xdm.QName{URI: p}},
		{AnyURI: true, AnyLocal: true},
	}
	for _, axis := range []Axis{AxisDescendant, AxisDescendantOrSelf} {
		for _, nt := range tests {
			step := &Step{Axis: axis, Test: nt}
			for _, n := range contexts {
				var want xdm.Sequence
				walkAxis(n, axis, func(m *xdm.Node) bool {
					if nt.Matches(m, xdm.KindElement) {
						want = append(want, m)
					}
					return true
				})
				got, err := step.evalFrom(NewContext(n, Builtins()), n)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(want) {
					t.Fatalf("%v::%s from %s %q: %d nodes, want %d", axis, nt, n.Kind, n.Name.Local, len(got), len(want))
				}
				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("%v::%s from %s %q: node %d differs", axis, nt, n.Kind, n.Name.Local, i)
					}
				}
			}
		}
	}

	// Predicates still apply after the walk, counting positions in it.
	v, err := MustCompile("/descendant::c[2]/@id, count(/a/descendant-or-self::c[last()])", nil).
		Eval(NewContext(tree.Root, Builtins()))
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 2 || v[0].(*xdm.Node).StringValue() != "4" || v[1].(*xdm.Atomic).String() != "1" {
		t.Errorf("predicates after the walk: got %v", v)
	}
}
