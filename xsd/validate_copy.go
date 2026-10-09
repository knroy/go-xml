package xsd

import (
	"context"

	"github.com/knroy/go-xml/v2/xdm"
)

// ValidateCopy validates a copy of root and returns the annotated copy,
// leaving root and the tree it belongs to untouched.
//
// It is Validate with ValidateOptions.AnnotateInPlace forced on, run over a
// copy, and it is the way to get a typed tree from a document you were handed.
// The copy answers every question the in-place run would have answered on
// the original: each node's typing (annotation, union member, resolved
// primitive and list item type, is-id, is-idrefs, nilled, the absent typed
// value, mixed content, and the schema's type environment), the defaulted
// attributes, the stripped ignorable whitespace, the base URI, the document
// URI, the DOCTYPE and its unparsed entities, the XML version, and the line
// and column of every failure. The returned error is the one Validate would
// have returned for root.
//
// The copy is a whole tree: when root is an element inside a document, the
// document is copied too and the returned node is root's counterpart in it,
// so it keeps its ancestors and in-scope namespaces just as root does. When
// root is nil the result is nil with Validate's error.
//
// What differs is identity. The copy is a new tree: "is" between it and the
// original is false, and so is doc(document-uri($copy)) is $copy until the
// caller registers the copy under that URI in place of the original. Because
// it never writes to the input, one tree may be validated from several
// goroutines at once. ValidateOptions.AnnotateInPlace is the alternative for a
// tree the caller has just built and owns outright.
//
// The copy is returned whether or not it is valid, annotated as far as the
// in-place run would have annotated the original.
func (s *Schema) ValidateCopy(root *xdm.Node, opts ValidateOptions) (*xdm.Node, error) {
	return s.ValidateCopyContext(context.Background(), root, opts)
}

// ValidateCopyContext is ValidateCopy with a cancellable context, as
// ValidateContext is to Validate.
func (s *Schema) ValidateCopyContext(ctx context.Context, root *xdm.Node,
	opts ValidateOptions) (*xdm.Node, error) {
	if root == nil {
		return nil, s.ValidateContext(ctx, nil, opts)
	}
	twin, twins := copyForValidation(root)
	opts.AnnotateInPlace = true
	return twin, s.validateContext(ctx, twin, opts, twins)
}

// copyForValidation copies the whole tree root belongs to and returns root's
// counterpart. When the original tree retained its source text, it also
// returns a map from each copied element and attribute back to its original,
// which is how a failure on the copy reports a position: the copy has no
// source text of its own.
//
// The walk is iterative because the copy happens before the validator's
// MaxDepth check, and a tree the caller parsed with no depth limit must not
// overflow the stack here.
func copyForValidation(root *xdm.Node) (twin *xdm.Node, twins map[*xdm.Node]*xdm.Node) {
	top := root
	for top.Parent() != nil {
		top = top.Parent()
	}
	var tree *xdm.Tree
	var topCopy *xdm.Node
	if t := top.Tree(); t != nil && t.Root == top {
		tree = xdm.NewTree()
		tree.CopyDTDFrom(t)
		tree.XMLVersion = t.XMLVersion
		topCopy = tree.Root
		if t.HasPositions() {
			twins = map[*xdm.Node]*xdm.Node{}
		}
	} else {
		topCopy = xdm.NewNode(top.Kind(), xdm.QName{}, "")
	}

	type pair struct{ dst, src *xdm.Node }
	stack := []pair{{topCopy, top}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		dst, src := p.dst, p.src
		copyNodeProps(dst, src)
		if src == root {
			twin = dst
		}
		if twins != nil {
			twins[dst] = src
		}
		for ns := range src.NamespaceDecls() {
			dst.AddNamespace(ns.Name().Local, ns.Value())
		}
		for a := range src.Attrs() {
			ac := xdm.NewNode(a.Kind(), xdm.QName{}, "")
			copyNodeProps(ac, a)
			dst.AddAttr(ac)
			if a == root {
				twin = ac
			}
			if twins != nil {
				twins[ac] = a
			}
		}
		for c := range src.Children() {
			cc := xdm.NewNode(c.Kind(), xdm.QName{}, "")
			dst.AppendChild(cc)
			stack = append(stack, pair{cc, c})
		}
	}
	if tree != nil {
		tree.Finalize()
	}
	return twin, twins
}

// copyNodeProps copies the properties of src that are not its links or its
// kind, which dst was created with.
func copyNodeProps(dst, src *xdm.Node) {
	dst.SetName(src.Name())
	dst.SetValue(src.Value())
	dst.SetBaseURI(src.BaseURI())
	dst.SetDocumentURI(src.DocumentURI())
	dst.CopyTypingFrom(src)
	dst.SetTypeEnv(src.TypeEnv())
}
