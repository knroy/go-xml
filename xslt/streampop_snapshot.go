package xslt

// snapshot(P) as a loop's population (lane J1).
//
// fn:snapshot of a node copies it with its ancestors, each shallow (name,
// attributes, namespaces). What the stream hands a population for P has
// those ancestors already: a grounded copy of an element match (its subtree
// under shallow copies of its ancestors), an attribute of a match's view
// (P/@a: the view's own ancestors are shallow copies). The population maps
// snapshot over each item handed, as it maps copy-of: the result is the
// tree's snapshot, new nodes in trees of their own, as in tree mode.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// snapshot makes snapshot(P), x, p's stream part.
func (pb *popBuilder) snapshot(p *streamPop, x *xpath.FuncCall) bool {
	p.fn = pb.part(&xpath.FuncCall{Name: x.Name, Args: []xpath.Expr{&xpath.VarRef{Name: splitItems}}})
	return pb.stream(p, x.Args[0]) && (p.ground || p.off)
}

// snapshotOf reports whether fn is snapshot($splitItems): a kept leaf's copy
// is under copies of its ancestors (streamleaf.go), what its snapshot needs.
func snapshotOf(fn *xpath.Compiled) bool {
	f, ok := fn.Expr().(*xpath.FuncCall)
	return ok && isFn(f, "snapshot", 1)
}

// snapshotTail reads P/snapshot(.) (or P/snapshot()) as snapshot(P): each
// match's snapshot, in trees made in P's order, which the path's sort keeps.
func snapshotTail(e xpath.Expr) (*xpath.FuncCall, bool) {
	pe, ok := e.(*xpath.PathExpr)
	if !ok || len(pe.Steps) < 2 {
		return nil, false
	}
	f, ok := pe.Steps[len(pe.Steps)-1].(*xpath.FuncCall)
	if !ok || !isFn(f, "snapshot", len(f.Args)) || len(f.Args) > 1 {
		return nil, false
	}
	if len(f.Args) == 1 {
		if _, ok := f.Args[0].(*xpath.ContextItem); !ok {
			return nil, false
		}
	}
	rest := &xpath.PathExpr{Root: pe.Root, Steps: pe.Steps[:len(pe.Steps)-1]}
	return &xpath.FuncCall{Name: f.Name, Args: []xpath.Expr{rest}}, true
}

// A loop whose select the analysis assessed as grounded (snapshot(P),
// copy-of(P), values) hands its body items that are not the stream's: new
// trees, or values. Its body runs on them as the tree runs it (the
// population is read off the cursor, the body off it: readyPop), whatever
// it reads of them -- their ancestors and root, for a snapshot -- so loopOp
// takes it as grounded (b.groundedSelect, streamgroup.go) even when the
// burst rules (burstBody) would refuse it.

// rangeWindow reads (P)[position() = L to H], L <= H integer literals, as
// subsequence(P, L, H - L + 1): the general comparison holds at exactly
// the positions L to H. nil when e is not that.
func rangeWindow(e xpath.Expr) xpath.Expr {
	f, ok := e.(*xpath.FilterExpr)
	if !ok || len(f.Predicates) != 1 {
		return nil
	}
	c, ok := f.Predicates[0].(*xpath.BinaryOp)
	if !ok || c.Op != "=" {
		return nil
	}
	if pos, ok := c.Left.(*xpath.FuncCall); !ok || !isFn(pos, "position", 0) {
		return nil
	}
	r, ok := c.Right.(*xpath.BinaryOp)
	if !ok || r.Op != "to" {
		return nil
	}
	lo, ok1 := r.Left.(*xpath.Literal)
	hi, ok2 := r.Right.(*xpath.Literal)
	if !ok1 || !ok2 || lo.Val.Type != xdm.TypeInteger || hi.Val.Type != xdm.TypeInteger || !lo.Val.FitsInt64() || !hi.Val.FitsInt64() {
		return nil
	}
	l, h := lo.Val.Int64(), hi.Val.Int64()
	if l < 1 || h < l {
		return nil
	}
	return fnCall("subsequence", f.Base, &xpath.Literal{Val: xdm.NewInteger(l)}, &xpath.Literal{Val: xdm.NewInteger(h - l + 1)})
}
