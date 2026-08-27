package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// fn:copy-of and fn:snapshot, sections 18.3 and 18.4.
//
// Both exist because a streamed node cannot be returned from the instruction
// that is reading it, so a stylesheet has to say "give me a copy I can keep".
// Neither is about streaming, though: 18.3 defines fn:copy-of($input) as
// "$input ! internal:copy-item(.)" where internal:copy-item is an xsl:copy-of
// with copy-namespaces="yes", copy-accumulators="yes" and
// validation="preserve", and that is a perfectly ordinary deep copy.
//
// The difference between the two is what happens above the node. fn:copy-of
// returns a parentless copy of the subtree; fn:snapshot returns the same
// subtree with a copy of the ancestor spine attached, so that an expression
// evaluated against it can still walk upwards and read ancestor names and
// attributes.

// registerCopyFuncs adds fn:copy-of and fn:snapshot to a running stylesheet's
// library.
//
// The instruction's base URI is not available here — a function call has no
// instruction — so a detached copy keeps the base URI it had, which is what
// xsl:copy-of does for a copy whose source carries an absolute one.
func registerCopyFuncs(l *xpath.Library) {
	// Since XPath31: both were introduced by XSLT 3.0, and a version="3.0"
	// stylesheet is what compiles as XPath 3.1. A 2.0 stylesheet calling one
	// must get XPST0017 -- and, through the same library,
	// function-available() must answer false for it -- rather than have it
	// quietly work because this engine implements 3.0 as well.
	for _, arity := range []int{0, 1} {
		l.Add(xpath.Function{
			Name: xdm.QName{URI: xdm.NSFN, Local: "copy-of"}, Arity: arity,
			Since: xpath.XPath31,
			Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
				return mapItems(ctx, args, copyItem)
			},
		})
		l.Add(xpath.Function{
			Name: xdm.QName{URI: xdm.NSFN, Local: "snapshot"}, Arity: arity,
			Since: xpath.XPath31,
			Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
				return mapItems(ctx, args, snapshotItem)
			},
		})
	}
}

// mapItems applies f to each item of the argument, or to the context item when
// the argument is absent.
func mapItems(ctx *xpath.Context, args []xdm.Sequence,
	f func(xdm.Item) xdm.Item) (xdm.Sequence, error) {

	in := xdm.Sequence(nil)
	if len(args) == 0 {
		if ctx.Item == nil {
			// Both functions are focus-dependent in their zero-argument
			// form, so an absent context item is XPDY0002 exactly as it is
			// for "." itself.
			return nil, fmt.Errorf("XPDY0002: no context item")
		}
		in = xdm.One(ctx.Item)
	} else {
		in = args[0]
	}
	out := make(xdm.Sequence, 0, len(in))
	for _, it := range in {
		out = append(out, f(it))
	}
	return out, nil
}

// copyItem is internal:copy-item: a deep copy of a node, anything else
// unchanged.
func copyItem(it xdm.Item) xdm.Item {
	n, ok := it.(*xdm.Node)
	if !ok {
		// "If the supplied item is an atomic value or a function item
		// (including maps and arrays), then it returns that item unchanged."
		return it
	}
	switch n.Kind {
	case xdm.KindDocument:
		return copyDocumentNode(n)
	case xdm.KindAttribute, xdm.KindNamespace:
		// A parentless attribute or namespace node. deepCopy would produce
		// one too, but only these two kinds have no children to walk, and
		// spelling them out keeps the parentlessness deliberate rather than
		// incidental.
		return &xdm.Node{
			Kind:           n.Kind,
			Name:           n.Name,
			Value:          n.Value,
			TypeAnnotation: n.TypeAnnotation,
			IsID:           n.IsID,
			IsIDREFS:       n.IsIDREFS,
		}
	}
	c := deepCopy(n)
	if n.Kind == xdm.KindElement {
		// copy-namespaces="yes": the copy is lifted out of the tree whose
		// ancestors declared the prefixes its own names use, so those
		// declarations come with it.
		inheritNamespaces(c, n)
	}
	return c
}

// snapshotItem is internal:snapshot-item: copyItem, plus a copy of the
// ancestor spine.
//
// 18.4 defines the ancestors precisely: each has "the same name, node-kind and
// base URI", copies of the attributes and namespaces, "a type annotation of
// xs:anyType", nilled/is-id/is-idref false, "and no children other than the
// child that is a copy of N or one of its ancestors". So the result is a chain
// of one-child elements under a document node, with the copied subtree at the
// bottom.
func snapshotItem(it xdm.Item) xdm.Item {
	n, ok := it.(*xdm.Node)
	if !ok {
		return it
	}
	if n.Kind == xdm.KindDocument {
		return copyDocumentNode(n)
	}
	var spine []*xdm.Node
	for a := n.Parent; a != nil; a = a.Parent {
		spine = append(spine, a)
	}
	if len(spine) == 0 {
		return copyItem(n)
	}

	tree := xdm.NewTree()
	tree.Root.BaseURI = spine[len(spine)-1].BaseURI
	parent := tree.Root
	// Outermost first: spine was built from the node upwards.
	for i := len(spine) - 1; i >= 0; i-- {
		a := spine[i]
		if a.Kind != xdm.KindElement {
			continue
		}
		c := &xdm.Node{
			Kind:    xdm.KindElement,
			Name:    a.Name,
			BaseURI: a.BaseURI,
			// "a type annotation of xs:anyType": the ancestor's own
			// annotation described a node with all its children, and this
			// copy has only one of them, so the annotation would be a claim
			// about content that is no longer there.
			TypeAnnotation: "anyType",
		}
		for _, ns := range a.Namespaces {
			c.AddNamespace(ns.Name.Local, ns.Value)
		}
		for _, at := range a.Attrs {
			c.AddAttr(&xdm.Node{Kind: xdm.KindAttribute, Name: at.Name,
				Value: at.Value, TypeAnnotation: at.TypeAnnotation,
				IsID: at.IsID, IsIDREFS: at.IsIDREFS})
		}
		parent.AppendChild(c)
		parent = c
	}

	var bottom *xdm.Node
	switch n.Kind {
	case xdm.KindAttribute:
		// An attribute's snapshot hangs off its own element rather than
		// becoming a child of it: an attribute is not in the child axis, and
		// appending it as one would put it where nothing looks for it.
		a := copyItem(n).(*xdm.Node)
		parent.AddAttr(a)
		tree.Finalize()
		return a
	case xdm.KindNamespace:
		ns := copyItem(n).(*xdm.Node)
		parent.AddNamespace(ns.Name.Local, ns.Value)
		tree.Finalize()
		return ns
	default:
		bottom = copyItem(n).(*xdm.Node)
	}
	parent.AppendChild(bottom)
	tree.Finalize()
	return bottom
}
