package xdmbuild

import "github.com/knroy/go-xml/v2/xdm"

// Node constructors and edits for code that builds or rewrites a tree outside
// a Builder.
//
// A Builder fits a sequence constructor: it merges text, fixes up namespaces
// and charges a budget. Other producers -- the RELAX NG compact-syntax
// translator, fn:json-to-xml, fn:analyze-string, the XSD assertion trees, the
// XSLT pattern and prefix rewrites -- make plain nodes and link them
// themselves, often bottom-up. These functions are the one place such code
// creates a node or writes a structural field, so that a change to the node
// layout has a single place to land rather than a literal in every package.
//
// They do exactly what the field write they replace did, nothing more: no
// re-parenting, no tree pointer, no document order. Linking a child, an
// attribute or a namespace still goes through xdm.Node's AppendChild, AddAttr
// and AddNamespace.

// NewElement returns a detached element node named name.
func NewElement(name xdm.QName) *xdm.Node {
	return &xdm.Node{Kind: xdm.KindElement, Name: name}
}

// NewAttribute returns a detached attribute node.
func NewAttribute(name xdm.QName, value string) *xdm.Node {
	return &xdm.Node{Kind: xdm.KindAttribute, Name: name, Value: value}
}

// NewText returns a detached text node.
func NewText(value string) *xdm.Node {
	return &xdm.Node{Kind: xdm.KindText, Value: value}
}

// NewComment returns a detached comment node.
func NewComment(value string) *xdm.Node {
	return &xdm.Node{Kind: xdm.KindComment, Value: value}
}

// NewPI returns a detached processing-instruction node.
func NewPI(target, value string) *xdm.Node {
	return &xdm.Node{Kind: xdm.KindPI, Name: xdm.QName{Local: target}, Value: value}
}

// NewDocument returns a document node that belongs to no xdm.Tree.
func NewDocument(baseURI string) *xdm.Node {
	return &xdm.Node{Kind: xdm.KindDocument, BaseURI: baseURI}
}

// ShallowCopy returns a copy of n sharing its children, attributes and
// namespace nodes, and keeping its tree, document order and typing: the copy
// compares, and generates ids, as n does.
func ShallowCopy(n *xdm.Node) *xdm.Node {
	c := *n
	return &c
}

// SetParent sets n's parent link and nothing else.
func SetParent(n, parent *xdm.Node) { n.Parent = parent }

// SetChildren replaces n's children. The children are not re-parented.
func SetChildren(n *xdm.Node, kids []*xdm.Node) { n.Children = kids }

// SetAttrs replaces n's attributes. The attributes are not re-parented.
func SetAttrs(n *xdm.Node, attrs []*xdm.Node) { n.Attrs = attrs }

// SetNamespaces replaces n's namespace nodes. They are not re-parented.
func SetNamespaces(n *xdm.Node, ns []*xdm.Node) { n.Namespaces = ns }

// SetName renames n.
func SetName(n *xdm.Node, name xdm.QName) { n.Name = name }

// SetBaseURI sets n's own base URI, leaving its descendants as they are.
func SetBaseURI(n *xdm.Node, base string) { n.BaseURI = base }

// ReplaceChild puts c in place of parent's i'th child and makes parent its
// parent. The child it replaces keeps its own parent link.
func ReplaceChild(parent *xdm.Node, i int, c *xdm.Node) {
	c.Parent = parent
	parent.Children[i] = c
}

// PrependChild makes c the first child of parent, in a new child slice, and
// sets c's parent link.
func PrependChild(parent, c *xdm.Node) {
	c.Parent = parent
	parent.Children = append([]*xdm.Node{c}, parent.Children...)
}

// DeepCopyPruned is DeepCopy leaving out, with its subtree, every descendant
// of n for which drop reports true.
func DeepCopyPruned(n *xdm.Node, drop func(*xdm.Node) bool) *xdm.Node {
	c := &xdm.Node{Kind: n.Kind, Name: n.Name, Value: n.Value, BaseURI: n.BaseURI}
	c.CopyTypingFrom(n)
	for _, ns := range n.Namespaces {
		c.AddNamespace(ns.Name.Local, ns.Value)
	}
	for _, a := range n.Attrs {
		ac := NewAttribute(a.Name, a.Value)
		ac.CopyTypingFrom(a)
		c.AddAttr(ac)
	}
	for _, ch := range n.Children {
		if !drop(ch) {
			c.AppendChild(DeepCopyPruned(ch, drop))
		}
	}
	return c
}
