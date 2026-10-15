package xdm

// Helpers for tests that read a node's children, attributes or namespace
// declarations as a slice.

func kids(n *Node) []*Node {
	var out []*Node
	for c := range n.Children() {
		out = append(out, c)
	}
	return out
}

func attrsOf(n *Node) []*Node {
	var out []*Node
	for a := range n.Attrs() {
		out = append(out, a)
	}
	return out
}

func nsOf(n *Node) []*Node {
	var out []*Node
	for ns := range n.NamespaceDecls() {
		out = append(out, ns)
	}
	return out
}
