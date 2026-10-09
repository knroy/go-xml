package xpath

import "github.com/knroy/go-xml/v2/xdm"

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
		for c := range n.Children() {
			if !visit(c) {
				return
			}
		}

	case AxisAttribute:
		for a := range n.Attrs() {
			if !visit(a) {
				return
			}
		}

	case AxisNamespace:
		// The namespace axis exposes every in-scope binding, not just those
		// declared on this element, so inherited declarations are included.
		//
		// The bindings are held in a map, and the order they come out in
		// is observable: as the comment above says, predicates number
		// positions along the axis, so namespace::*[1] would name a
		// different prefix from one run to the next. XPath leaves the
		// order of this axis implementation-dependent, so any stable
		// order conforms — an unstable one does not, in the sense that
		// matters to a caller.
		// Only elements have namespace nodes. Every other kind has an empty
		// namespace axis — including the text and document nodes of a
		// temporary tree, which inherit no bindings because they have no
		// name to put in a namespace.
		for ns := range n.NamespaceNodes() {
			if !visit(ns) {
				return
			}
		}

	case AxisParent:
		if p := n.Parent(); p != nil {
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
		for p := n.Parent(); p != nil; p = p.Parent() {
			if !visit(p) {
				return
			}
		}

	case AxisAncestorOrSelf:
		if !visit(n) {
			return
		}
		for p := n.Parent(); p != nil; p = p.Parent() {
			if !visit(p) {
				return
			}
		}

	case AxisFollowingSibling:
		for s := n.NextSibling(); s != nil; s = s.NextSibling() {
			if !visit(s) {
				return
			}
		}

	case AxisPrecedingSibling:
		// Reverse axis: nearest sibling first.
		for s := n.PrevSibling(); s != nil; s = s.PrevSibling() {
			if !visit(s) {
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
	for c := range n.Descendants() {
		if !visit(c) {
			return false
		}
	}
	return true
}

// appendNamedDescendants appends to out the element descendants of n that t
// matches, in document order. It is walkDescendants with the name test
// inlined, for the hot shape "//name": no visit callback per node.
func appendNamedDescendants(out xdm.Sequence, n *xdm.Node, t *NameTest) xdm.Sequence {
	for c := range n.Descendants() {
		if c.Kind() != xdm.KindElement {
			continue
		}
		if (t.AnyURI || c.Name().URI == t.Name.URI) && (t.AnyLocal || c.Name().Local == t.Name.Local) {
			out = append(out, c)
		}
	}
	return out
}

// walkFollowing visits every node after n in document order, excluding n's own
// descendants. Implemented by climbing to each ancestor and taking its
// following siblings' subtrees, which yields document order without needing a
// full-tree scan.
func walkFollowing(n *xdm.Node, visit func(*xdm.Node) bool) {
	// An attribute or namespace node comes before its element's children in
	// document order, so those children follow it. They are not descendants
	// of the attribute — an attribute has none — so the exclusion the axis
	// makes for descendants does not reach them, and starting the walk at the
	// owner element's siblings skipped the whole subtree.
	if n.Kind() == xdm.KindAttribute || n.Kind() == xdm.KindNamespace {
		if n.Parent() != nil {
			if !walkDescendants(n.Parent(), visit) {
				return
			}
		}
	}
	// NextSibling and PrevSibling are nil for an attribute or namespace node:
	// they have no siblings on the sibling axes, per the spec.
	for cur := n; cur != nil; cur = cur.Parent() {
		for s := cur.NextSibling(); s != nil; s = s.NextSibling() {
			if !visit(s) {
				return
			}
			if !walkDescendants(s, visit) {
				return
			}
		}
	}
}

// walkPreceding visits every node before n in document order, excluding n's
// ancestors. It is a reverse axis, so nodes are yielded nearest first: within
// each preceding sibling's subtree the deepest, last node comes first.
func walkPreceding(n *xdm.Node, visit func(*xdm.Node) bool) {
	for cur := n; cur != nil; cur = cur.Parent() {
		for s := cur.PrevSibling(); s != nil; s = s.PrevSibling() {
			if !walkSubtreeReverse(s, visit) {
				return
			}
		}
	}
}

// walkSubtreeReverse visits a subtree in reverse document order.
func walkSubtreeReverse(n *xdm.Node, visit func(*xdm.Node) bool) bool {
	for c := n.LastChild(); c != nil; c = c.PrevSibling() {
		if !walkSubtreeReverse(c, visit) {
			return false
		}
	}
	return visit(n)
}
