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
	root := NewNode(KindElement, QName{Local: "rule"}, "")
	a := root.AppendElement(QName{Local: "assert"})
	b := root.AppendElement(QName{Local: "report"})
	deep := b.AppendElement(QName{Local: "text"})

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
		r := NewNode(KindElement, QName{Local: "r"}, "")
		r.AppendElement(QName{Local: "c"})
		return r
	}
	x, y := mk(), mk()
	if x.generateID() == y.generateID() {
		t.Fatalf("two detached roots share id %s", x.generateID())
	}
	if kids(x)[0].generateID() == kids(y)[0].generateID() {
		t.Fatalf("children of two detached roots share id %s",
			kids(x)[0].generateID())
	}
}
