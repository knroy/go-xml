package xslt

// Dynamic calls of accumulator-before and -after on a streamed input (lane
// L4; XSLT 3.0 §10.3.6).
//
// §10.3.6: a function item for a context-dependent function made where the
// context item is a node of a streamed input document cannot save that
// node: "the context is saved with an absent focus, so the call on F will
// fail with a dynamic error saying that there is no context item
// available" -- for accumulator-before/-after, XTDE3350 (accumulator-061:
// a global accumulator-after#1, made at the global context item, the
// document node of the input).
//
// A document counts as streamed when the stylesheet asked for it in
// streamable form, whether or not this engine then streams it: the
// principal input supplied to TransformReader (its streamed run and its
// fallback to a tree alike), and a document xsl:source-document
// streamable="yes" reads (streamedTrees, as XTDE3362 counts it). So the
// streamed run and its tree run give the same answer. A tree supplied to
// Transform is not streamed: its function items keep their focus.

import (
	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// readerRootOf is the runtime's readerRoot: root when the input was
// supplied to TransformReader.
func readerRootOf(root *xdm.Node, opts TransformOptions) *xdm.Node {
	if opts.readerInput {
		return root
	}
	return nil
}

// savesAbsentFocus reports whether a function item for the host-bound
// function local, made at ctx, saves an absent focus (§10.3.6): it is
// accumulator-before or -after and ctx's context item is a node of a
// streamed document.
func (rt *runtime) savesAbsentFocus(ctx *xpath.Context, local string) bool {
	if local != "accumulator-before" && local != "accumulator-after" || rt.transformState == nil {
		return false
	}
	n, ok := ctx.Item.(*xdm.Node)
	if !ok || n == nil {
		return false
	}
	root := n.Root()
	return xdmburst.FromStream(n) || rt.readerRoot != nil && root == rt.readerRoot || rt.streamedTrees[root]
}
