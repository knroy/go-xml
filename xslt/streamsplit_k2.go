package xslt

// Expression shapes of the streamed split (lane K2): set operators whose
// foreign operand sits inside a path or a simple map.

import "github.com/knroy/go-xml/v2/xpath"

// foreignPath rewrites (F union P)/T and (P union F)/T, F foreign and T axis
// steps whose predicates cannot fail, as (F union ())/T union P/T: a path's
// last steps apply to each node of what precedes them, so they distribute
// over a union. F union () keeps the operator's type check of F (XPTY0004,
// not the path's XPTY0019); F is a variable's nodes, which raise nothing
// else. The union is then foreignUnion's (streamsplit_foreign.go). nil when
// e is not such a path.
func foreignPath(e xpath.Expr) xpath.Expr {
	pe, ok := e.(*xpath.PathExpr)
	if !ok || pe.Root || len(pe.Steps) < 2 {
		return nil
	}
	u, ok := pe.Steps[0].(*xpath.BinaryOp)
	if !ok || u.Op != "union" {
		return nil
	}
	rest := pe.Steps[1:]
	for _, st := range rest {
		s, ok := st.(*xpath.Step)
		if !ok {
			return nil
		}
		for _, p := range s.Predicates {
			if !splitCannotFail(p) {
				return nil
			}
		}
	}
	f, p := u.Left, u.Right
	if !foreignVar(f) {
		f, p = p, f
	}
	if !foreignVar(f) || foreign(p) || !unionNodes(p) {
		return nil
	}
	path := func(x xpath.Expr) xpath.Expr {
		if ip, ok := x.(*xpath.PathExpr); ok {
			return &xpath.PathExpr{Root: ip.Root, Steps: append(ip.Steps[:len(ip.Steps):len(ip.Steps)], rest...)}
		}
		return &xpath.PathExpr{Steps: append([]xpath.Expr{x}, rest...)}
	}
	ff := path(&xpath.BinaryOp{Op: "union", Left: f, Right: &xpath.SequenceExpr{}})
	if f == u.Left {
		return &xpath.BinaryOp{Op: "union", Left: ff, Right: path(p)}
	}
	return &xpath.BinaryOp{Op: "union", Left: path(p), Right: ff}
}

// foreignVar reports whether e is a variable reference: foreign, and
// evaluating it raises nothing.
func foreignVar(e xpath.Expr) bool {
	_, ok := e.(*xpath.VarRef)
	return ok
}

// mapSetSafe reports whether R, mapped over a grounded copy of a match, is a
// set operator of a variable's nodes and an operand mapSafe takes that
// gives nodes: * union $v, $v except *. The copy's nodes are the
// originals to order and identity (xdm streamview.go), so the operator
// sorts and compares them against the variable's as the tree does.
func mapSetSafe(r xpath.Expr) bool {
	x, ok := r.(*xpath.BinaryOp)
	if !ok || x.Op != "union" && x.Op != "intersect" && x.Op != "except" {
		return false
	}
	l, o := x.Left, x.Right
	if !foreignVar(l) {
		l, o = o, l
	}
	return foreignVar(l) && !foreign(o) && unionNodes(o) && (mapSafe(o) || mapSetSafe(o))
}
