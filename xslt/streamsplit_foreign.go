package xslt

// A union of a streamed path with a foreign operand under any holder (lane J1).
//
// foreignSets (streamsplit_union.go) rewrites P intersect F, P except F and
// count(P union F); a union under any other holder (copy-of, sum, a filter, a
// map) is left as it is. The split takes it as it is: P, the side that reads
// the stream, is the consuming path, handed to the outer as its items, and
// the outer evaluates $items union F. The items are views or copies that are
// the originals to identity and order (xdm streamview.go: a copy out of the
// stream orders as its original, and its tree has the parse tree's id among
// the others), so the union sorts and de-duplicates them against F's nodes as
// the tree does against the parse tree's. The items are collected for the
// outer, as any held split's: under the cap at the document node
// (streamhold.go).

import "github.com/knroy/go-xml/v2/xpath"

// foreignUnion takes x, P union F with F foreign (no focus, no document) on
// either side, as the holder of P; done says x is such a union.
func (w *splitter) foreignUnion(x *xpath.BinaryOp) (done, ok bool) {
	if x.Op != "union" {
		return false, false
	}
	switch {
	case foreign(x.Right) && !foreign(x.Left) && unionNodes(x.Left):
		return true, w.slot(x.Left, func(y xpath.Expr) { x.Left = y }, useKeep)
	case foreign(x.Left) && !foreign(x.Right) && unionNodes(x.Right):
		return true, w.slot(x.Right, func(y xpath.Expr) { x.Right = y }, useKeep)
	}
	return false, false
}

// unionNodes reports whether e gives nodes only, for the operator's type
// check: a path, or copy-of of one (copies, new nodes in both modes).
func unionNodes(e xpath.Expr) bool {
	if f, ok := e.(*xpath.FuncCall); ok && isFn(f, "copy-of", 1) {
		return nodeValued(f.Args[0])
	}
	return nodeValued(e)
}
