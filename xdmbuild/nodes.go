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
// Each returns a parentless node; a tree is grown from it with xdm.Node's
// Append calls, top-down, in document order.

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

// DeepCopyPruned is DeepCopy leaving out, with its subtree, every descendant
// of n for which drop reports true.
func DeepCopyPruned(n *xdm.Node, drop func(*xdm.Node) bool) *xdm.Node {
	return xdm.CopyPruned(n, drop)
}
