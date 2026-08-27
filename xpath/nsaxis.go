package xpath

import "github.com/knroy/go-xml/xdm"

// NamespaceNodesOf returns the nodes on n's namespace axis, which is every
// in-scope binding rather than only those declared on n itself.
//
// The nodes are synthesized, as they are for a namespace:: step: the tree
// stores declarations, and the axis exposes the scope they accumulate to. It
// is exported for xsl:key, whose index walk has to visit the same nodes a
// pattern can match.
func NamespaceNodesOf(n *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	walkAxis(n, AxisNamespace, func(ns *xdm.Node) bool {
		out = append(out, ns)
		return true
	})
	return out
}
