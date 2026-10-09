package relaxng

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xdmbuild"
)

// Construction of the XML-syntax tree that the compact syntax maps onto.
//
// The compact syntax is defined by its correspondence to the XML syntax, so
// the parser's output is an XML-syntax tree and not a second internal model.
// That is the whole economy of this feature: once the tree exists, checkSyntax,
// the section 7 restriction pass, the compiler and the validator all apply
// unchanged, and there is no second implementation of any rule that could
// drift from the first.
//
// The tree is built with the xdm node constructors rather than by emitting XML
// text and reparsing it. Text would have to escape correctly in attribute
// values, in element content and inside literals that may hold any character
// the \x{} escape can name — three escaping rules to get right, each a way for
// a schema to mean something other than what it says. Building nodes has no
// such surface.

// builder assembles the XML-syntax tree.
//
// A pattern is parsed bottom-up — an operand exists before the operator that
// combines it — so nodes are made detached and grafted together as the parse
// reduces. The owning tree pointer is therefore not known when a node is made,
// and is propagated to the whole tree in one walk by finish. Nothing in this
// package asks a node for its document order, but a caller handed a tree might,
// and a half-attached tree is a trap rather than an economy.
type builder struct{ tree *xdm.Tree }

func newBuilder() *builder { return &builder{tree: xdm.NewTree()} }

// el makes a RELAX NG element.
//
// The name is in the RELAX NG namespace with no prefix. Nothing downstream
// looks at the prefix — every check is on Name.URI == NS — and leaving it
// empty keeps the tree from implying a binding the source never wrote.
func (b *builder) el(local string) *xdm.Node {
	return xdmbuild.NewElement(xdm.QName{URI: NS, Local: local})
}

// attr sets a RELAX NG attribute.
//
// These are always in no namespace: syntax.go treats an attribute in the
// RELAX NG namespace as an error and one in any other namespace as a foreign
// annotation to be ignored, so a name= that carried a URI would either be
// rejected or silently dropped.
func (b *builder) attr(n *xdm.Node, local, value string) {
	n.AddAttr(xdmbuild.NewAttribute(xdm.QName{Local: local}, value))
}

// text gives an element character content.
//
// Only the elements syntax.go marks textOnly — <name>, <value>, <param> — may
// have any, and a whitespace-only text node elsewhere would be harmless but
// pointless, so this is called exactly where content is meant.
func (b *builder) text(n *xdm.Node, s string) {
	n.AppendChild(xdmbuild.NewText(s))
}

// finish roots the document at n and returns the document node.
//
// Finalize assigns document order over the whole tree, but it is reached only
// through nodes already linked to it, so the tree pointer is threaded down
// first. AppendChild propagates it one level at a time and the tree was built
// detached, so that propagation has to be repeated here rather than assumed.
func (b *builder) finish(n *xdm.Node) *xdm.Node {
	b.tree.Root.AppendChild(n)
	b.adopt(n)
	b.tree.Finalize()
	return b.tree.Root
}

// adopt relinks a detached subtree so every node names the parent and tree it
// belongs to.
func (b *builder) adopt(n *xdm.Node) {
	for a := range n.Attrs() {
		a.SetParent(n)
	}
	for ns := range n.NamespaceDecls() {
		ns.SetParent(n)
	}
	for c := range n.Children() {
		c.SetParent(n)
		b.adopt(c)
	}
}
