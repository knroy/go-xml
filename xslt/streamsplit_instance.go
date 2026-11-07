package xslt

// "P instance of T*" as a count (T1, §27.5).
//
// The split took the operand of "instance of" as kept (useKeep): every match
// held, as a grounded copy, for the outer to test. At the document node that
// is the tree, and a path matching the document element gives up there
// (noRoot), so sx-instance-of-001 and the two over a sequence with a
// variable (-102, -103) fell back to the tree. With the occurrence "*" the
// answer needs no match held: it is true exactly when no item fails the item
// test, which is empty(P[not(. instance of T)]), the test on each match's
// view as a predicate of P's last step. A predicate that reads only its own
// item gives the same nodes on the last step as on the whole path, so the
// rewrite is exact; "instance of" raises no error, so no error moves.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// instanceAllOf rewrites x, "O instance of T*" with T a kind test a view
// decides, as a count over O's consuming path: empty(P[not(. instance of
// T)]), and-ed before "V instance of T*" for each motionless item V when O
// is a sequence. It returns nil for any other x.
func instanceAllOf(x *xpath.InstanceOfExpr, focus vfocus, key bool) xpath.Expr {
	t := x.Type
	kt, ok := t.ItemType.(*xpath.KindTest)
	if t.Occurrence != "*" || t.Empty || !ok || t.HasAtomicType || t.IsFunctionTest ||
		t.IsArrayTest || t.IsMapTest || kt.TypeName != "" || kt.Content != nil {
		return nil
	}
	one := t
	one.Occurrence = ""
	pred := &xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: "not"}, Args: []xpath.Expr{
		&xpath.InstanceOfExpr{Operand: &xpath.ContextItem{}, Type: one}}}
	items := []xpath.Expr{x.Operand}
	if sq, ok := x.Operand.(*xpath.SequenceExpr); ok {
		items = sq.Items
	}
	var consuming xpath.Expr
	var rest []xpath.Expr
	for _, it := range items {
		if (viewCheck{kids: true, key: key}).safe(it, focus, false) {
			rest = append(rest, &xpath.InstanceOfExpr{Operand: it, Type: t})
			continue
		}
		f := withLastPredicate(it, pred)
		if f == nil || consuming != nil {
			return nil
		}
		consuming = &xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: "empty"}, Args: []xpath.Expr{f}}
	}
	if consuming == nil {
		return nil
	}
	for _, r := range rest {
		consuming = &xpath.BinaryOp{Op: "and", Left: consuming, Right: r}
	}
	return consuming
}

// withLastPredicate is e, a step or a path ending in one, with pred appended
// to the last step's predicates; or nil. e is not changed: what changes is
// copied. A union is left out: the split takes no union whose branches end
// in predicates at the document node (sx-instance-of-010 would stop
// streaming), so it keeps giving up there.
func withLastPredicate(e, pred xpath.Expr) xpath.Expr {
	switch x := e.(type) {
	case *xpath.Step:
		s := *x
		s.Predicates = append(append([]xpath.Expr(nil), x.Predicates...), pred)
		return &s
	case *xpath.PathExpr:
		if len(x.Steps) == 0 {
			return nil
		}
		last := withLastPredicate(x.Steps[len(x.Steps)-1], pred)
		if _, ok := last.(*xpath.Step); !ok {
			return nil
		}
		p := *x
		p.Steps = append(append([]xpath.Expr(nil), x.Steps[:len(x.Steps)-1]...), last)
		return &p
	}
	return nil
}
