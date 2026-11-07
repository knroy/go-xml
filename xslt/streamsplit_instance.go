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
	if !kindStar(t) {
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

// kindStar reports whether t is "K*", K a kind test a match's node decides
// at its start tag: its kind and name, no type annotation or content.
func kindStar(t xpath.SequenceType) bool {
	kt, ok := t.ItemType.(*xpath.KindTest)
	return t.Occurrence == "*" && !t.Empty && ok && !t.HasAtomicType && !t.IsFunctionTest &&
		!t.IsArrayTest && !t.IsMapTest && kt.TypeName == "" && kt.Content == nil
}

// treatEach takes x, "O treat as T*" with T as kindStar says, item by item
// (§19.8.8.6: the operand's posture and sweep are the expression's): O's one
// consuming item P is walked as x's holder uses it, each match tested
// against T as it comes (streamSplit.each), in document order, which is
// P's; each motionless item V becomes "V treat as T*", in the outer and as
// a check run before the walk (streamSplit.checks), so a failing V raises
// without P being read or held first (sx-treat-102). A match not of T
// raises the tree's XPDY0050, whose message names only T: whichever item
// fails first, the error is the same. P must be a bare path, perhaps of
// branches or under outermost(): its matches are its items. done is false
// for any other x, which is then held whole.
func (w *splitter) treatEach(x *xpath.TreatExpr, put func(xpath.Expr), use splitUse) (done, ok bool) {
	t := x.Type
	if !kindStar(t) {
		return false, false
	}
	items := []xpath.Expr{x.Operand}
	sq, isSeq := x.Operand.(*xpath.SequenceExpr)
	if isSeq {
		items = sq.Items
	}
	out := make([]xpath.Expr, len(items))
	var p xpath.Expr
	var checks []xpath.Expr
	for i, it := range items {
		if (viewCheck{kids: true, key: w.key}).safe(it, w.focus, false) {
			out[i] = &xpath.TreatExpr{Operand: it, Type: t}
			checks = append(checks, out[i])
			continue
		}
		probe := *w
		pp, isPath := probe.pathOf(it)
		if p != nil || !isPath || len(pp.tail) > 0 || pp.ctxItem || pp.group || pp.oi == "innermost" ||
			pp.mapR != nil || pp.skip > 0 || pp.br != nil && (pp.br.self != 0 || pp.br.climb != nil) {
			return false, false
		}
		p, out[i] = it, it
	}
	if p == nil {
		return false, false
	}
	y := out[0]
	if isSeq {
		y = &xpath.SequenceExpr{Items: out}
	}
	put(y)
	if !w.walk(y, put, use) {
		return true, false
	}
	if w.pe != p {
		return true, w.refuse("treat as over a path walked as another")
	}
	w.treat, w.treatChecks = &t, checks
	return true, true
}

// withLastPredicate is e, a step or a path ending in one, with pred appended
// to the last step's predicates; or nil. e is not changed: what changes is
// copied. A union is the union of its operands so filtered: pred reads only
// its own item, so (A | B)[pred] is A[pred] | B[pred]. Not intersect or
// except, whose right operand's nodes pred may not see.
func withLastPredicate(e, pred xpath.Expr) xpath.Expr {
	switch x := e.(type) {
	case *xpath.BinaryOp:
		if x.Op != "union" {
			return nil
		}
		l, r := withLastPredicate(x.Left, pred), withLastPredicate(x.Right, pred)
		if l == nil || r == nil {
			return nil
		}
		return &xpath.BinaryOp{Op: x.Op, Left: l, Right: r}
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
