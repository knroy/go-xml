package xdm

import (
	"iter"
	"sort"
)

// Tree construction.
//
// A tree is built top-down, in document order, by appending to an open node:
// an element, or a document node, whose subtree is still being written. A
// node is open from the call that made it until something is appended to a
// node that is not it or one of its descendants; after that it is closed,
// and appending to it again is a programming error. Attributes are appended
// to an element before its first child. Nothing is ever inserted before or
// between existing nodes, removed, or moved to another parent: code that
// needs a tree with something changed builds a copy with the change made,
// through the same calls.
//
// What may change after a node is made is its scalar state: SetValue and
// AppendValue (merging adjacent text), SetName, SetBaseURI, the typing
// setters, and the namespace declarations of an element that is still open.
//
// The order is the one a parser produces and the one the stored layout
// keeps, so a tree built this way is laid out as a parsed one is.

// AppendElement appends a new element named name as the last child of p and
// returns it, open.
func (p *Node) AppendElement(name QName) *Node {
	c := &Node{kind: KindElement, name: name}
	p.AppendChild(c)
	return c
}

// AppendText appends a text node holding value as the last child of p. It
// does not merge with a text node already there; see AppendValue.
func (p *Node) AppendText(value string) *Node {
	c := &Node{kind: KindText, value: value}
	p.AppendChild(c)
	return c
}

// AppendComment appends a comment node as the last child of p.
func (p *Node) AppendComment(value string) *Node {
	c := &Node{kind: KindComment, value: value}
	p.AppendChild(c)
	return c
}

// AppendPI appends a processing instruction as the last child of p.
func (p *Node) AppendPI(target, value string) *Node {
	c := &Node{kind: KindPI, name: QName{Local: target}, value: value}
	p.AppendChild(c)
	return c
}

// AppendAttr appends an attribute to p, which must be an element with no
// children yet, and returns it.
func (p *Node) AppendAttr(name QName, value string) *Node {
	a := &Node{kind: KindAttribute, name: name, value: value}
	p.AddAttr(a)
	return a
}

// AppendValue appends s to the value of n, which must be the last node
// appended to its tree: the text node a builder is still extending.
func (n *Node) AppendValue(s string) {
	if s != "" {
		n.value += s
	}
}

// AppendCopy appends a deep copy of src as the last child of p (or, for an
// attribute, as an attribute of p) and returns the copy. See Copy for what
// travels.
func (p *Node) AppendCopy(src *Node) *Node {
	c := copyPruned(src, nil)
	if c.kind == KindAttribute {
		p.AddAttr(c)
	} else {
		p.AppendChild(c)
	}
	return c
}

// AppendShallowCopy appends a copy of src without its children, attributes or
// namespace declarations, and returns it open: the start of a copy the caller
// completes, changing what it needs to on the way.
func (p *Node) AppendShallowCopy(src *Node) *Node {
	c := shallowCopy(src)
	if c.kind == KindAttribute {
		p.AddAttr(c)
	} else {
		p.AppendChild(c)
	}
	return c
}

// Copy returns a deep copy of n with no parent.
//
// The copy keeps n's name, value, base URI and typing, its namespace
// declarations, its attributes (with their typing) and its children. It is a
// new node: it has its own identity and is in no tree until it is appended.
func Copy(n *Node) *Node { return copyPruned(n, nil) }

// CopyPruned is Copy leaving out, with its subtree, every descendant of n for
// which drop reports true.
func CopyPruned(n *Node, drop func(*Node) bool) *Node {
	return copyPruned(n, drop)
}

// ShallowCopy returns a parentless copy of n without its children, attributes
// or namespace declarations: the root of a copy the caller completes.
func ShallowCopy(n *Node) *Node { return shallowCopy(n) }

func shallowCopy(n *Node) *Node {
	c := &Node{kind: n.kind, name: n.name, value: n.value, baseURI: n.baseURI}
	c.CopyTypingFrom(n)
	return c
}

func copyPruned(n *Node, drop func(*Node) bool) *Node {
	c := shallowCopy(n)
	for _, ns := range n.namespaces {
		c.AddNamespace(ns.name.Local, ns.value)
	}
	for _, a := range n.attrs {
		ac := &Node{kind: KindAttribute, name: a.name, value: a.value}
		ac.CopyTypingFrom(a)
		c.AddAttr(ac)
	}
	for _, ch := range n.children {
		if drop == nil || !drop(ch) {
			c.AppendChild(copyPruned(ch, drop))
		}
	}
	return c
}

// RemoveNamespaceDecls drops the namespace declarations held on n, an element
// still being built.
func (n *Node) RemoveNamespaceDecls(drop func(prefix, uri string) bool) {
	kept := n.namespaces[:0:0]
	for _, ns := range n.namespaces {
		if !drop(ns.name.Local, ns.value) {
			kept = append(kept, ns)
		}
	}
	n.namespaces = kept
}

// NamespaceNodes iterates over the namespace axis of n: one namespace node
// for every binding in scope on an element, the inherited ones included,
// ordered by prefix. Other kinds have none.
//
// Asking twice yields nodes that are the same node by Is, Identity, Compare
// and generate-id, whether or not they are the same pointer.
func (n *Node) NamespaceNodes() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		if n.kind != KindElement {
			return
		}
		scope := n.InScopeNamespaces()
		prefixes := make([]string, 0, len(scope))
		for prefix := range scope {
			prefixes = append(prefixes, prefix)
		}
		sort.Strings(prefixes)
		for i, prefix := range prefixes {
			ns := &Node{kind: KindNamespace, name: QName{Local: prefix}, value: scope[prefix], parent: n}
			ns.SetSynthesizedOrder(n, i)
			if !yield(ns) {
				return
			}
		}
	}
}

// ReplaceLastChild puts c, a parentless node, in place of p's last child,
// which must be the last subtree appended to the tree: the one place where a
// built subtree may still be exchanged, used to swap a constructed element for
// its validated, typed copy. It returns the node now in that place.
func (p *Node) ReplaceLastChild(c *Node) *Node {
	c.parent = p
	c.tree = p.tree
	p.children[len(p.children)-1] = c
	return c
}
