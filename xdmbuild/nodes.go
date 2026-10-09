package xdmbuild

import "github.com/knroy/go-xml/v2/xdm"

// Node constructors and edits for code that builds or rewrites a tree outside
// a Builder.
//
// A Builder fits a sequence constructor: it merges text, fixes up namespaces
// and charges a budget. Other producers -- the RELAX NG compact-syntax
// translator, fn:json-to-xml, fn:analyze-string, the XSD assertion trees, the
// XSLT pattern and prefix rewrites -- make plain nodes and link them
// themselves, often bottom-up. These functions and xdm.Node's setters are the
// only places such code creates a node or writes a structural property, so
// that a change to the node layout has a single place to land rather than a
// literal in every package.
//
// They do exactly what the field writes they replaced did, nothing more: no
// re-parenting, no tree pointer, no document order. Linking a child, an
// attribute or a namespace still goes through xdm.Node's AppendChild, AddAttr
// and AddNamespace.

// NewElement returns a detached element node named name.
func NewElement(name xdm.QName) *xdm.Node {
	return xdm.NewNode(xdm.KindElement, name, "")
}

// NewAttribute returns a detached attribute node.
func NewAttribute(name xdm.QName, value string) *xdm.Node {
	return xdm.NewNode(xdm.KindAttribute, name, value)
}

// NewText returns a detached text node.
func NewText(value string) *xdm.Node {
	return xdm.NewNode(xdm.KindText, xdm.QName{}, value)
}

// NewComment returns a detached comment node.
func NewComment(value string) *xdm.Node {
	return xdm.NewNode(xdm.KindComment, xdm.QName{}, value)
}

// NewPI returns a detached processing-instruction node.
func NewPI(target, value string) *xdm.Node {
	return xdm.NewNode(xdm.KindPI, xdm.QName{Local: target}, value)
}

// NewDocument returns a document node that belongs to no xdm.Tree.
func NewDocument(baseURI string) *xdm.Node {
	n := xdm.NewNode(xdm.KindDocument, xdm.QName{}, "")
	n.SetBaseURI(baseURI)
	return n
}

// ShallowCopy returns a copy of n sharing its children, attributes and
// namespace nodes, and keeping its tree, document order and typing: the copy
// compares, and generates ids, as n does.
func ShallowCopy(n *xdm.Node) *xdm.Node {
	c := *n
	return &c
}

// ReplaceChild puts c in place of parent's i'th child and makes parent its
// parent, in a new child slice. The child it replaces keeps its own parent
// link.
func ReplaceChild(parent *xdm.Node, i int, c *xdm.Node) {
	c.SetParent(parent)
	kids := make([]*xdm.Node, parent.NumChildren())
	for j := range kids {
		kids[j] = parent.ChildAt(j)
	}
	kids[i] = c
	parent.SetChildren(kids)
}

// PrependChild makes c the first child of parent, in a new child slice, and
// sets c's parent link.
func PrependChild(parent, c *xdm.Node) {
	c.SetParent(parent)
	kids := make([]*xdm.Node, 1+parent.NumChildren())
	kids[0] = c
	for j := 1; j < len(kids); j++ {
		kids[j] = parent.ChildAt(j - 1)
	}
	parent.SetChildren(kids)
}

// DeepCopyPruned is DeepCopy leaving out, with its subtree, every descendant
// of n for which drop reports true.
func DeepCopyPruned(n *xdm.Node, drop func(*xdm.Node) bool) *xdm.Node {
	c := xdm.NewNode(n.Kind(), n.Name(), n.Value())
	c.SetBaseURI(n.BaseURI())
	c.CopyTypingFrom(n)
	for ns := range n.NamespaceDecls() {
		c.AddNamespace(ns.Name().Local, ns.Value())
	}
	for a := range n.Attrs() {
		ac := NewAttribute(a.Name(), a.Value())
		ac.CopyTypingFrom(a)
		c.AddAttr(ac)
	}
	for ch := range n.Children() {
		if !drop(ch) {
			c.AppendChild(DeepCopyPruned(ch, drop))
		}
	}
	return c
}
