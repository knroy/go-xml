package xsd

import (
	"context"

	"github.com/knroy/go-xml/v2/internal/xdmclone"
	"github.com/knroy/go-xml/v2/xdm"
)

// ValidateCopy validates root and returns a typed copy of it, leaving root and
// the tree it belongs to untouched.
//
// Validate answers only whether root is valid. ValidateCopy also produces the
// post-schema-validation infoset: the copy carries every node's typing (the
// annotation, union member, resolved primitive and list item type, is-id,
// is-idrefs, nilled, the absent typed value, mixed content, and the schema's
// type environment), the attributes the schema supplies by default (with the
// namespace declarations XSD 1.1 namespace fixup adds for them), and, when
// root is a document node, none of the ignorable whitespace its element-only
// content held. The returned error is the one Validate would have returned
// for root, positions included.
//
// What the copy holds depends on where root sits:
//   - a document node, a node without a parent, or the element of a document:
//     the whole tree is copied (with its DOCTYPE, unparsed entities, XML
//     version and document URI), and the returned node is root's counterpart
//     in it;
//   - any other node: the copy is root's subtree alone and the returned node
//     has no parent. Validation still sees the namespaces root's ancestors
//     bind, exactly as it would in place, so a QName or an xsi:type resolves
//     the same way.
//
// The copy is a new tree: "is" between it and the original is false. The copy
// is returned whether or not it is valid, annotated as far as the assessment
// got. When root is nil the result is nil with Validate's error.
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
	if ctx == nil {
		ctx = context.Background()
	}
	return s.typedCopy(ctx, root, opts, (*validator).run)
}

// typedCopy copies root, runs check over the copy with annotation on, and
// returns the typed result: the copy itself, or, when the assessment recorded
// structural edits (or root's ancestors were copied only for context), a
// second copy with the edits applied.
//
// The edits are recorded rather than made because a built tree is never
// edited: see the defaults, fixups and dropped fields of validator.
func (s *Schema) typedCopy(ctx context.Context, root *xdm.Node, opts ValidateOptions,
	check func(*validator, *xdm.Node) error) (*xdm.Node, error) {
	whole := root.Kind() == xdm.KindDocument || root.Parent() == nil ||
		root.Parent().Kind() == xdm.KindDocument
	v := s.newValidator(ctx, opts)
	v.typed = true
	// A whole tree is cloned in bulk, with its source positions, so a
	// failure reports its line and column without a map back to the
	// original. The ancestors-only copy is made node by node.
	var twin *xdm.Node
	top := topOf(root)
	t := top.Tree()
	positions := t != nil && t.Root == top && t.HasPositions()
	if whole {
		if m := xdmclone.Clone(top, xdmclone.Options{Positions: positions}); m != nil {
			twin = m(root).(*xdm.Node)
		}
	}
	if twin == nil {
		c := copier{root: root, whole: whole, track: true}
		c.copyTree(top)
		twin, v.twins = c.twin, c.twins
		positions = false
	}
	err := check(v, twin)
	if positions {
		// The per-node copy the result stands in for had no positions.
		xdmclone.DropPositions(twin)
	}
	return v.typedResult(twin, whole), err
}

// typedResult builds the tree a typed copy returns from the validated working
// copy, applying the recorded edits, and returns twin's counterpart in it.
func (v *validator) typedResult(twin *xdm.Node, whole bool) *xdm.Node {
	edited := len(v.defaults) > 0 || len(v.fixups) > 0 || len(v.dropped) > 0
	top := twin
	if whole {
		if !edited {
			return twin
		}
		top = topOf(twin)
	} else if !edited && twin.Parent() == nil {
		return twin
	}
	// The bulk clone renumbers records around dropped text and added
	// attributes; added namespace declarations are left to the per-node copy.
	if len(v.fixups) == 0 {
		var o xdmclone.Options
		if len(v.dropped) > 0 {
			o.Drop = func(n any) bool { return v.dropped[n.(*xdm.Node)] }
		}
		if len(v.defaults) > 0 {
			o.Add = func(n any) []any {
				as := v.defaults[n.(*xdm.Node)]
				if len(as) == 0 {
					return nil
				}
				out := make([]any, len(as))
				for i, a := range as {
					out[i] = a
				}
				return out
			}
		}
		if m := xdmclone.Clone(top, o); m != nil {
			return m(twin).(*xdm.Node)
		}
	}
	c := copier{v: v, root: twin, whole: true}
	c.copyTree(top)
	return c.twin
}

func topOf(n *xdm.Node) *xdm.Node {
	for n.Parent() != nil {
		n = n.Parent()
	}
	return n
}

// copier copies a tree top-down through the append API.
type copier struct {
	// v supplies the edits an assessment recorded: attributes to add,
	// namespace declarations to add, text to leave out. Nil for none.
	v *validator
	// root is the node whose counterpart is wanted; twin receives it.
	root, twin *xdm.Node
	// whole copies every node under the top. Otherwise root's ancestors are
	// copied with their namespace declarations and attributes but none of
	// their other children: enough context for validation, at the cost of
	// the ancestor chain rather than of the document.
	whole bool
	// track asks for twins when the source tree retained its text: a map
	// from each copied node back to its original, which is how a failure on
	// the copy reports a position. The copy has no source text of its own.
	track bool
	twins map[*xdm.Node]*xdm.Node
	// onPath maps each of root's ancestors, when whole is false, to its
	// child on the way down to root (root itself for root's parent).
	onPath map[*xdm.Node]*xdm.Node
}

// copyTree copies the tree under top. The walk is iterative because the copy
// happens before the validator's MaxDepth check, and a tree the caller parsed
// with no depth limit must not overflow the stack here.
func (c *copier) copyTree(top *xdm.Node) *xdm.Node {
	if !c.whole {
		c.onPath = map[*xdm.Node]*xdm.Node{}
		for n := c.root; n.Parent() != nil; n = n.Parent() {
			c.onPath[n.Parent()] = n
		}
	}
	var tree *xdm.Tree
	var dst *xdm.Node
	if t := top.Tree(); t != nil && t.Root == top {
		tree = xdm.NewTree()
		tree.CopyDTDFrom(t)
		tree.XMLVersion = t.XMLVersion
		dst = tree.Root
		if c.track && t.HasPositions() {
			c.twins = map[*xdm.Node]*xdm.Node{}
		}
	} else {
		dst = xdm.NewNode(top.Kind(), top.Name(), top.Value())
	}
	c.fill(dst, top)

	// next is the source child to copy next; started records that the frame
	// has copied one, which on the path to root is all it copies.
	type frame struct {
		src, dst, next *xdm.Node
		started        bool
	}
	stack := []frame{{top, dst, top.FirstChild(), false}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		var src *xdm.Node
		if next, ok := c.onPath[f.src]; ok {
			// An ancestor: only the way down to root is copied. An
			// attribute root came with its parent's attributes.
			if f.started || next.Kind() == xdm.KindAttribute {
				stack = stack[:len(stack)-1]
				continue
			}
			src = next
		} else {
			if f.next == nil {
				stack = stack[:len(stack)-1]
				continue
			}
			src = f.next
			f.next = src.NextSibling()
		}
		parent := f.dst
		f.started = true
		if c.v != nil && c.v.dropped[src] {
			continue
		}
		cc := parent.AppendShallowCopy(src)
		c.fill(cc, src)
		stack = append(stack, frame{src, cc, src.FirstChild(), false})
	}
	if tree != nil {
		tree.Finalize()
	}
	return dst
}

// fill completes dst, just appended as the copy of src: its properties, its
// namespace declarations and its attributes, with the recorded ones after
// those src carries, in the order an in-place edit would have added them.
func (c *copier) fill(dst, src *xdm.Node) {
	c.note(dst, src)
	for prefix, uri := range src.DeclaredNamespaces() {
		dst.AddNamespace(prefix, uri)
	}
	if c.v != nil {
		for _, ns := range c.v.fixups[src] {
			dst.AddNamespace(ns.prefix, ns.uri)
		}
	}
	for a := range src.Attrs() {
		c.note(dst.AppendAttr(a.Name(), a.Value()), a)
	}
	if c.v != nil {
		for _, a := range c.v.defaults[src] {
			copyNodeProps(dst.AppendAttr(a.Name(), a.Value()), a)
		}
	}
}

func (c *copier) note(dst, src *xdm.Node) {
	copyNodeProps(dst, src)
	if src == c.root {
		c.twin = dst
	}
	if c.twins != nil {
		c.twins[dst] = src
	}
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
