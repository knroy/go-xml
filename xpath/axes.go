package xpath

import "github.com/knroy/go-xml/xdm"

// walkAxis calls visit for each node on the axis from n, in axis order.
//
// Axis order is not always document order: reverse axes yield nodes nearest
// first, and that ordering is observable because predicates number positions
// along the axis. The caller re-sorts into document order after applying
// predicates, never before.
//
// visit returns false to stop early, which lets a positional predicate like
// [1] avoid materialising an entire descendant axis.
func walkAxis(n *xdm.Node, axis Axis, visit func(*xdm.Node) bool) {
	switch axis {
	case AxisSelf:
		visit(n)

	case AxisChild:
		for _, c := range n.Children {
			if !visit(c) {
				return
			}
		}

	case AxisAttribute:
		for _, a := range n.Attrs {
			if !visit(a) {
				return
			}
		}

	case AxisNamespace:
		// The namespace axis exposes every in-scope binding, not just those
		// declared on this element, so inherited declarations are included.
		for prefix, uri := range n.InScopeNamespaces() {
			ns := &xdm.Node{
				Kind:   xdm.KindNamespace,
				Name:   xdm.QName{Local: prefix},
				Value:  uri,
				Parent: n,
			}
			if !visit(ns) {
				return
			}
		}

	case AxisParent:
		if p := n.Parent; p != nil {
			visit(p)
		}

	case AxisDescendant:
		walkDescendants(n, visit)

	case AxisDescendantOrSelf:
		if !visit(n) {
			return
		}
		walkDescendants(n, visit)

	case AxisAncestor:
		for p := n.Parent; p != nil; p = p.Parent {
			if !visit(p) {
				return
			}
		}

	case AxisAncestorOrSelf:
		if !visit(n) {
			return
		}
		for p := n.Parent; p != nil; p = p.Parent {
			if !visit(p) {
				return
			}
		}

	case AxisFollowingSibling:
		sibs, i := siblingsOf(n)
		for j := i + 1; j < len(sibs); j++ {
			if !visit(sibs[j]) {
				return
			}
		}

	case AxisPrecedingSibling:
		// Reverse axis: nearest sibling first.
		sibs, i := siblingsOf(n)
		for j := i - 1; j >= 0; j-- {
			if !visit(sibs[j]) {
				return
			}
		}

	case AxisFollowing:
		walkFollowing(n, visit)

	case AxisPreceding:
		walkPreceding(n, visit)
	}
}

// walkDescendants visits children depth-first in document order. Attributes
// and namespace nodes are not descendants of their element.
func walkDescendants(n *xdm.Node, visit func(*xdm.Node) bool) bool {
	for _, c := range n.Children {
		if !visit(c) {
			return false
		}
		if !walkDescendants(c, visit) {
			return false
		}
	}
	return true
}

// siblingsOf returns the parent's children and n's index within them.
// Attributes have no siblings on the sibling axes, per the spec.
func siblingsOf(n *xdm.Node) ([]*xdm.Node, int) {
	if n.Parent == nil || n.Kind == xdm.KindAttribute || n.Kind == xdm.KindNamespace {
		return nil, -1
	}
	sibs := n.Parent.Children
	for i, s := range sibs {
		if s == n {
			return sibs, i
		}
	}
	return nil, -1
}

// walkFollowing visits every node after n in document order, excluding n's own
// descendants. Implemented by climbing to each ancestor and taking its
// following siblings' subtrees, which yields document order without needing a
// full-tree scan.
func walkFollowing(n *xdm.Node, visit func(*xdm.Node) bool) {
	for cur := n; cur != nil; cur = cur.Parent {
		sibs, i := siblingsOf(cur)
		if i < 0 {
			continue
		}
		for j := i + 1; j < len(sibs); j++ {
			if !visit(sibs[j]) {
				return
			}
			if !walkDescendants(sibs[j], visit) {
				return
			}
		}
	}
}

// walkPreceding visits every node before n in document order, excluding n's
// ancestors. It is a reverse axis, so nodes are yielded nearest first: within
// each preceding sibling's subtree the deepest, last node comes first.
func walkPreceding(n *xdm.Node, visit func(*xdm.Node) bool) {
	for cur := n; cur != nil; cur = cur.Parent {
		sibs, i := siblingsOf(cur)
		if i < 0 {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if !walkSubtreeReverse(sibs[j], visit) {
				return
			}
		}
	}
}

// walkSubtreeReverse visits a subtree in reverse document order.
func walkSubtreeReverse(n *xdm.Node, visit func(*xdm.Node) bool) bool {
	for i := len(n.Children) - 1; i >= 0; i-- {
		if !walkSubtreeReverse(n.Children[i], visit) {
			return false
		}
	}
	return visit(n)
}
