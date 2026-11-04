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

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

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

// sharedTail takes apart a union of two paths with one tail of axis steps,
// A/T union B/T, as (A union B)/T: a path's last steps apply to each node
// of what precedes them, so they distribute over a union (not over
// intersect or except). It returns T and the operands without it, or the
// operands as they are.
func sharedTail(op string, l, r splitPathParts) ([]xpath.Expr, splitPathParts, splitPathParts) {
	if op != "union" || len(l.tail) == 0 || len(l.tail) != len(r.tail) || l.mapR != nil || r.mapR != nil ||
		l.skip != 0 || r.skip != 0 {
		return nil, l, r
	}
	for i := range l.tail {
		if _, ok := l.tail[i].(*xpath.Step); !ok || l.tail[i].String() != r.tail[i].String() {
			return nil, l, r
		}
	}
	tail := l.tail
	l.tail, r.tail = nil, nil
	return tail, l, r
}

// climbedOuter reports whether p, the argument of outermost() or
// innermost(), is a path whose tail climbs (P/ancestor::*): the function is
// then not P's (pathOf's oi) but the outer's, over the climbed nodes, which
// the split takes once each off the matches' views; their identities are
// the originals' (xdm Identity), what fn:outermost compares.
func climbedOuter(p splitPathParts) bool {
	if p.oi != "" || p.ctxItem || p.mapR != nil || p.skip != 0 || len(p.tail) == 0 {
		return false
	}
	var t xpath.Expr = &xpath.PathExpr{Steps: p.tail}
	if len(p.tail) == 1 {
		t = p.tail[0]
	}
	return climbs(t)
}

// outerOfClimbed takes x, outermost(P/T) or innermost(P/T) with T climbing,
// as the holder of P/T; done says x is one.
func (w *splitter) outerOfClimbed(x *xpath.FuncCall) (done, ok bool) {
	if !outerInner(x) || w.pe != nil {
		return false, false
	}
	if inner, ok := w.pathOf(x.Args[0]); !ok || !climbedOuter(inner) {
		return false, false
	}
	return true, w.slot(x.Args[0], func(y xpath.Expr) { x.Args[0] = y }, useKeep)
}

// pathOverClimbed takes e, outermost(P/T)/R (what P ! R of it is read as):
// its first step holds P/T as outerOfClimbed takes it, and R runs in the outer on the nodes, which
// are views: the split's climbing check keeps it to what a view has.
func (w *splitter) pathOverClimbed(e xpath.Expr) (done, ok bool) {
	pe, isPath := e.(*xpath.PathExpr)
	if !isPath || pe.Root || len(pe.Steps) < 2 {
		return false, false
	}
	f, isCall := pe.Steps[0].(*xpath.FuncCall)
	if !isCall {
		return false, false
	}
	return w.outerOfClimbed(f)
}

// distributedPath rewrites (A, B)/T and insert-before(A, n, B)/T, n a
// literal integer, as A/T union B/T: a path sorts and de-duplicates what
// its last steps give, so the order of the sequence on its left is lost,
// and T, axis steps, gives nodes of each item alone. The union is then a
// set operator the split takes (of two paths of the stream, or of one and a
// variable's nodes: foreignUnion). nil when e is not such a path.
func distributedPath(e xpath.Expr) xpath.Expr {
	pe, ok := e.(*xpath.PathExpr)
	if !ok || pe.Root || len(pe.Steps) < 2 {
		return nil
	}
	rest := pe.Steps[1:]
	for _, st := range rest {
		if _, ok := st.(*xpath.Step); !ok {
			return nil
		}
	}
	var items []xpath.Expr
	switch x := pe.Steps[0].(type) {
	case *xpath.SequenceExpr:
		items = x.Items
	case *xpath.FuncCall:
		if !isFn(x, "insert-before", 3) {
			return nil
		}
		if l, ok := x.Args[1].(*xpath.Literal); !ok || l.Val.Type != xdm.TypeInteger {
			return nil
		}
		items = []xpath.Expr{x.Args[0], x.Args[2]}
	}
	if len(items) < 2 {
		return nil
	}
	var u xpath.Expr
	for _, it := range items {
		var p *xpath.PathExpr
		if ip, ok := it.(*xpath.PathExpr); ok {
			p = &xpath.PathExpr{Root: ip.Root, Steps: append(ip.Steps[:len(ip.Steps):len(ip.Steps)], rest...)}
		} else {
			p = &xpath.PathExpr{Steps: append([]xpath.Expr{it}, rest...)}
		}
		if u == nil {
			u = p
		} else {
			u = &xpath.BinaryOp{Op: "union", Left: u, Right: p}
		}
	}
	return u
}
