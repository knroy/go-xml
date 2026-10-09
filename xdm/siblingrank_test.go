package xdm

import "testing"

// detachedSample builds a tree that was never finalized, with declared and
// inherited namespaces, attributes, text and a synthesized namespace node,
// and returns every node in it.
func detachedSample() []*Node {
	var all []*Node
	el := func(parent *Node, name string, ns ...string) *Node {
		e := &Node{kind: KindElement, name: QName{Local: name}}
		if parent != nil {
			parent.AppendChild(e)
		}
		for i := 0; i < len(ns); i += 2 {
			e.AddNamespace(ns[i], ns[i+1])
		}
		e.AddAttr(&Node{name: QName{Local: "a"}, value: "1"})
		e.AddAttr(&Node{name: QName{Local: "b"}, value: "2"})
		all = append(all, e)
		all = append(all, e.namespaces...)
		all = append(all, e.attrs...)
		return e
	}
	r := el(nil, "r", "p", "u1", "q", "u2")
	b := el(r, "b", "s", "u3")
	r.AppendChild(&Node{kind: KindText, value: "t"})
	all = append(all, r.children[1])
	c := el(r, "c")
	el(b, "d", "", "u4")
	el(c, "e")
	// The namespace axis synthesizes inherited bindings: c has no node for p.
	all = append(all, &Node{kind: KindNamespace, name: QName{Local: "p"}, value: "u1", parent: c})
	return all
}

// refCompareDetached and refSiblingRank are compareDetached and siblingRank
// as they were, ranking every pair with the namespace base included.
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
	if n.kind == KindNamespace {
		for i, ns := range p.namespaces {
			if ns == n {
				return i
			}
		}
		i := 0
		for prefix := range p.InScopeNamespaces() {
			if prefix < n.name.Local {
				i++
			}
		}
		return i
	}
	base := len(p.namespaces)
	if m := len(p.InScopeNamespaces()); m > base {
		base = m
	}
	if n.kind == KindAttribute {
		for i, a := range p.attrs {
			if a == n {
				return base + i
			}
		}
		return base + len(p.attrs)
	}
	base += len(p.attrs)
	for i, c := range p.children {
		if c == n {
			return base + i
		}
	}
	return base + len(p.children)
}

// Skipping the namespace base for two non-namespace siblings must not change
// any answer.
func TestCompareDetachedSkipsNamespaceBase(t *testing.T) {
	all := detachedSample()
	for _, a := range all {
		for _, b := range all {
			if a == b {
				continue
			}
			if got, want := a.Compare(b), refCompareDetached(a, b); got != want {
				t.Fatalf("%v %q vs %v %q: %d, want %d", a.kind, a.name.Local, b.kind, b.name.Local, got, want)
			}
		}
	}
}
