package xdm

import "testing"

// detachedSample builds a tree that was never finalized, with declared and
// inherited namespaces, attributes, text and a synthesized namespace node,
// and returns every node in it.
func detachedSample() []*Node {
	var all []*Node
	el := func(parent *Node, name string, ns ...string) *Node {
		var e *Node
		if parent != nil {
			e = parent.AppendElement(QName{Local: name})
		} else {
			e = NewNode(KindElement, QName{Local: name}, "")
		}
		for i := 0; i < len(ns); i += 2 {
			e.AddNamespace(ns[i], ns[i+1])
		}
		e.AppendAttr(QName{Local: "a"}, "1")
		e.AppendAttr(QName{Local: "b"}, "2")
		all = append(all, e)
		all = append(all, nsOf(e)...)
		all = append(all, attrsOf(e)...)
		return e
	}
	r := el(nil, "r", "p", "u1", "q", "u2")
	b := el(r, "b", "s", "u3")
	el(b, "d", "", "u4")
	all = append(all, r.AppendText("t"))
	c := el(r, "c")
	el(c, "e")
	// The namespace axis has the inherited bindings: c declares no p.
	for ns := range c.NamespaceNodes() {
		if ns.Name().Local == "p" {
			all = append(all, ns)
		}
	}
	return all
}

// ancestorChain returns n's ancestors root-first, ending with n itself.
func ancestorChain(n *Node) []*Node {
	var up []*Node
	for c := n; c != nil; c = c.Parent() {
		up = append(up, c)
	}
	for i, j := 0, len(up)-1; i < j; i, j = i+1, j-1 {
		up[i], up[j] = up[j], up[i]
	}
	return up
}

// refCompareDetached and refSiblingRank order two nodes of one tree by
// walking it: the data model's rule, against which the record order is held.
func refCompareDetached(n, o *Node) int {
	na, oa := ancestorChain(n), ancestorChain(o)
	i := 0
	for i < len(na) && i < len(oa) && na[i] == oa[i] {
		i++
	}
	switch {
	case i == len(na):
		return -1
	case i == len(oa):
		return 1
	}
	ra, rb := refSiblingRank(na[i-1], na[i]), refSiblingRank(na[i-1], oa[i])
	switch {
	case ra < rb:
		return -1
	case ra > rb:
		return 1
	}
	return 0
}

func refSiblingRank(p, n *Node) int {
	if n.Kind() == KindNamespace {
		for i, ns := range nsOf(p) {
			if ns == n {
				return i
			}
		}
		i := 0
		for prefix := range p.InScopeNamespaces() {
			if prefix < n.Name().Local {
				i++
			}
		}
		return i
	}
	base := len(nsOf(p))
	if m := len(p.InScopeNamespaces()); m > base {
		base = m
	}
	if n.Kind() == KindAttribute {
		for i, a := range attrsOf(p) {
			if a == n {
				return base + i
			}
		}
		return base + len(attrsOf(p))
	}
	base += len(attrsOf(p))
	for i, c := range kids(p) {
		if c == n {
			return base + i
		}
	}
	return base + len(kids(p))
}

// The record order of a constructed tree agrees with the structural rule:
// namespace nodes, then attributes, then children.
func TestCompareMatchesStructuralOrder(t *testing.T) {
	all := detachedSample()
	for _, a := range all {
		for _, b := range all {
			if a == b {
				continue
			}
			if got, want := a.Compare(b), refCompareDetached(a, b); got != want {
				t.Fatalf("%v %q vs %v %q: %d, want %d", a.Kind(), a.Name().Local, b.Kind(), b.Name().Local, got, want)
			}
		}
	}
}
