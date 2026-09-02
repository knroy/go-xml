package xdm

import "testing"

// Order must tell apart the nodes of a tree that was never finalized, not
// only the roots of two such trees.
//
// fn:generate-id() is built on Order, and a result tree assembled by a
// sequence constructor never goes through Tree.Finalize, so every node under
// one root kept the zero order it was built with and generate-id() answered
// the same for all of them. Reported against SchXslt2, whose transpiler keys
// a map on generate-id() of each sch:assert and sch:report and so raised
// XTDE3365 for a duplicate key that was really two different nodes.
func TestOrderDistinguishesUnfinalizedNodes(t *testing.T) {
	root := &Node{Kind: KindElement, Name: QName{Local: "rule"}}
	a := &Node{Kind: KindElement, Name: QName{Local: "assert"}, Parent: root}
	b := &Node{Kind: KindElement, Name: QName{Local: "report"}, Parent: root}
	deep := &Node{Kind: KindElement, Name: QName{Local: "text"}, Parent: b}
	b.Children = []*Node{deep}
	root.Children = []*Node{a, b}

	seen := map[int]string{}
	for _, n := range []struct {
		name string
		node *Node
	}{{"rule", root}, {"assert", a}, {"report", b}, {"text", deep}} {
		got := n.node.Order()
		if prev, dup := seen[got]; dup {
			t.Fatalf("%s and %s share Order()=%d", prev, n.name, got)
		}
		seen[got] = n.name
	}

	// The answer must not move between calls: an identity that changed when
	// asked twice would break every use of it.
	if first, second := a.Order(), a.Order(); first != second {
		t.Fatalf("Order() is not stable: %d then %d", first, second)
	}
}

// Two unfinalized trees still may not collide with each other.
func TestOrderSeparatesUnfinalizedTrees(t *testing.T) {
	mk := func() *Node {
		r := &Node{Kind: KindElement, Name: QName{Local: "r"}}
		c := &Node{Kind: KindElement, Name: QName{Local: "c"}, Parent: r}
		r.Children = []*Node{c}
		return r
	}
	x, y := mk(), mk()
	if x.Order() == y.Order() {
		t.Fatalf("two detached roots share Order()=%d", x.Order())
	}
	if x.Children[0].Order() == y.Children[0].Order() {
		t.Fatalf("children of two detached roots share Order()=%d",
			x.Children[0].Order())
	}
}
