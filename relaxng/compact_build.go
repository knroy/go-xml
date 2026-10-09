package relaxng

import (
	"github.com/knroy/go-xml/v2/xdm"
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
// combines it — while an xdm tree is built top-down, each node appended to an
// open parent in document order. The parser therefore builds cnodes, a plain
// mirror of the elements it means, and finish emits the xdm tree from them in
// one walk once the parse is complete.
type builder struct{ tree *xdm.Tree }

func newBuilder() *builder { return &builder{tree: xdm.NewTree()} }

// cnode is an element, or a text node when isText, of the tree being parsed.
type cnode struct {
	name   xdm.QName
	ns     [][2]string // prefix, uri
	attrs  [][2]string // local name (no namespace), value
	kids   []*cnode
	text   string
	isText bool
}

func (n *cnode) add(c *cnode)             { n.kids = append(n.kids, c) }
func (n *cnode) addNS(prefix, uri string) { n.ns = append(n.ns, [2]string{prefix, uri}) }
func (n *cnode) prepend(c *cnode)         { n.kids = append([]*cnode{c}, n.kids...) }

// el makes a RELAX NG element.
//
// The name is in the RELAX NG namespace with no prefix. Nothing downstream
// looks at the prefix — every check is on Name.URI == NS — and leaving it
// empty keeps the tree from implying a binding the source never wrote.
func (b *builder) el(local string) *cnode {
	return &cnode{name: xdm.QName{URI: NS, Local: local}}
}

// attr sets a RELAX NG attribute.
//
// These are always in no namespace: syntax.go treats an attribute in the
// RELAX NG namespace as an error and one in any other namespace as a foreign
// annotation to be ignored, so a name= that carried a URI would either be
// rejected or silently dropped.
func (b *builder) attr(n *cnode, local, value string) {
	n.attrs = append(n.attrs, [2]string{local, value})
}

// text gives an element character content.
//
// Only the elements syntax.go marks textOnly — <name>, <value>, <param> — may
// have any, and a whitespace-only text node elsewhere would be harmless but
// pointless, so this is called exactly where content is meant.
func (b *builder) text(n *cnode, s string) {
	n.add(&cnode{text: s, isText: true})
}

// finish emits the document rooted at n and returns the document node.
func (b *builder) finish(n *cnode) *xdm.Node {
	emit(b.tree.Root, n)
	b.tree.Finalize()
	return b.tree.Root
}

// emit appends n, and then its subtree, to p.
func emit(p *xdm.Node, n *cnode) {
	if n.isText {
		p.AppendText(n.text)
		return
	}
	e := p.AppendElement(n.name)
	for _, ns := range n.ns {
		e.AddNamespace(ns[0], ns[1])
	}
	for _, a := range n.attrs {
		e.AppendAttr(xdm.QName{Local: a[0]}, a[1])
	}
	for _, k := range n.kids {
		emit(e, k)
	}
}
