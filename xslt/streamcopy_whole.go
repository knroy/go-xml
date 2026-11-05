package xslt

// A select at the document node that reads the stream only through copies
// of the whole document or of the document element (lane MM6):
// f(copy-of(.)) (function-5001), innermost(snapshot(/chapter)//section)
// (sf-innermost-001), snapshot(/) and snapshot(/*) (sf-snapshot-0101a/b).
//
// Each copy needs the whole document before the select can make anything,
// so the stream builds it, as docCopySplit does for copy-of(.) itself
// (docCopyEval: one document built off the cursor), and the select runs on
// it: copy-of(.), snapshot(.), copy-of(/) and snapshot(/) are that document;
// copy-of(/E) and snapshot(/E) are of its element E. A document built from
// the events is the tree's document copied, so what the select reads of it
// is what it reads of the tree's, and what it returns of it are copies,
// which are new nodes in both runs. The built document costs what the
// tree's own does, a snapshot of its element what the tree's snapshot
// does: no memory is saved.
//
// The rest of the select reads no focus (focusFree, a stylesheet call
// counted by its arguments), no variable it does not bind, no function
// burstFnBanned names, and calls stylesheet functions only where the split
// would on copies (marked copies, burst or values).

import (
	"reflect"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// wholeDocSplit is the split of sel at the document node when it reads the
// stream only through such copies; nil otherwise.
func (c *compiler) wholeDocSplit(sel *xpath.Compiled, ns xpath.NamespaceResolver) *streamSplit {
	e, probe := reparse(sel, ns), reparse(sel, ns)
	if e == nil || probe == nil {
		return nil
	}
	items := func() xpath.Expr { return &xpath.VarRef{Name: splitItems} }
	n := wholeCopies(&e, items, false)
	if n == 0 || wholeCopies(&probe, items, false) != n {
		return nil
	}
	markSheetCalls(e, c.pkgStreamFuncs()) // streamsplit_calls.go
	ok := true
	bound := map[xdm.QName]bool{splitItems: true}
	walkExprNodes(e, func(v any) {
		switch x := v.(type) {
		case *xpath.FuncCall:
			if stylesheetFn(x.Name) {
				sc, marked := sheetCallOf(x)
				ok = ok && marked && (sc.copies || sc.burst || sc.values)
			} else if x.Name.URI == fnNS && (burstFnBanned[x.Name.Local] || x.Name.Local == "current") {
				ok = false
			}
		case *xpath.ForExpr:
			markBound(bound, x.Bindings)
		case *xpath.LetExpr:
			markBound(bound, x.Bindings)
		case *xpath.QuantifiedExpr:
			markBound(bound, x.Bindings)
		case *xpath.InlineFunctionExpr, *xpath.DynamicCall:
			ok = false
		}
	})
	walkExprVars(e, func(r *xpath.VarRef) { ok = ok && bound[r.Name] })
	if !ok {
		return nil
	}
	// A stylesheet function has no focus: the probe counts its arguments.
	exprWalk(reflect.ValueOf(&probe).Elem(), func(v reflect.Value) {
		if v.Kind() != reflect.Interface || !v.CanSet() {
			return
		}
		if x, isCall := v.Interface().(*xpath.FuncCall); isCall && (stylesheetFn(x.Name) || x.Name.URI == xdm.NSXS || x.Name.URI == xdm.NSMath) {
			v.Set(reflect.ValueOf(xpath.Expr(&xpath.SequenceExpr{Items: x.Args})))
		}
	})
	if !focusFree(probe) {
		return nil
	}
	s := &streamSplit{doc: true, ctxItem: true, docCopy: true, outerVar: splitItems,
		outer: xpathleaf.WithExpr(sel, e).(*xpath.Compiled)}
	// snapshot(/E), the only copy: a document whose only child is E is
	// that snapshot already (a document node with E's copy, its base URI
	// and DTD), so the stream holds one copy where the tree holds its
	// document and the snapshot (audit A2-7: 1.26-1.32x the tree's live
	// heap when it held the built document and a snapshot of it).
	if sole := reparse(sel, ns); n == 1 && sole != nil && wholeCopies(&sole, items, true) == 1 {
		markSheetCalls(sole, c.pkgStreamFuncs())
		s.wholeSole = xpathleaf.WithExpr(sel, sole).(*xpath.Compiled)
	}
	return s
}

// wholeCopies rewrites, in *e and only where the focus is the outer one
// (the document node), each copy-of(X) or snapshot(X) of the whole document
// (X ".", "/") as the built document, and of the document element (X "/E",
// E one child step without predicates) as that call on E of it. It returns
// how many it rewrote; the context or the root read anywhere else is left
// for the probe's focusFree to find. sole rewrites only snapshot(/E), as
// E of the built document itself, and counts only those.
func wholeCopies(e *xpath.Expr, items func() xpath.Expr, sole bool) int {
	n := 0
	var at func(p *xpath.Expr)
	all := func(ps []xpath.Expr) {
		for i := range ps {
			at(&ps[i])
		}
	}
	binds := func(bs []xpath.Binding) {
		for i := range bs {
			at(&bs[i].Seq)
		}
	}
	at = func(p *xpath.Expr) {
		switch x := (*p).(type) {
		case *xpath.FuncCall:
			if x.Name.URI == fnNS && (x.Name.Local == "copy-of" || x.Name.Local == "snapshot") && len(x.Args) == 1 && !sole {
				switch a := x.Args[0].(type) {
				case *xpath.ContextItem:
					*p = items()
					n++
					return
				case *xpath.PathExpr:
					if a.Root && len(a.Steps) == 0 {
						*p = items()
						n++
						return
					}
					if st, ok := oneChildStep(a); ok {
						x.Args[0] = &xpath.PathExpr{Steps: []xpath.Expr{items(), st}}
						n++
						return
					}
				}
			}
			if sole && x.Name.URI == fnNS && x.Name.Local == "snapshot" && len(x.Args) == 1 {
				if a, ok := x.Args[0].(*xpath.PathExpr); ok {
					if st, ok := oneChildStep(a); ok {
						*p = &xpath.PathExpr{Steps: []xpath.Expr{items(), st}}
						n++
						return
					}
				}
			}
			all(x.Args)
		case *xpath.BinaryOp:
			at(&x.Left)
			at(&x.Right)
		case *xpath.StringConcat:
			at(&x.Left)
			at(&x.Right)
		case *xpath.UnaryOp:
			at(&x.Operand)
		case *xpath.IfExpr:
			at(&x.Cond)
			at(&x.Then)
			at(&x.Else)
		case *xpath.SequenceExpr:
			all(x.Items)
		case *xpath.FilterExpr:
			at(&x.Base)
		case *xpath.SimpleMap:
			at(&x.Left)
		case *xpath.PathExpr:
			if !x.Root && len(x.Steps) > 0 {
				at(&x.Steps[0])
			}
		case *xpath.LetExpr:
			binds(x.Bindings)
			at(&x.Return)
		case *xpath.ForExpr:
			binds(x.Bindings)
			at(&x.Return)
		case *xpath.QuantifiedExpr:
			binds(x.Bindings)
			at(&x.Test)
		case *xpath.CastExpr:
			at(&x.Operand)
		case *xpath.InstanceOfExpr:
			at(&x.Operand)
		case *xpath.TreatExpr:
			at(&x.Operand)
		}
	}
	at(e)
	return n
}

// oneChildStep is E of the path /E, E a child step without predicates.
func oneChildStep(a *xpath.PathExpr) (*xpath.Step, bool) {
	if !a.Root || len(a.Steps) != 1 {
		return nil, false
	}
	st, ok := a.Steps[0].(*xpath.Step)
	return st, ok && st.Axis == xpath.AxisChild && len(st.Predicates) == 0
}

func markBound(bound map[xdm.QName]bool, bs []xpath.Binding) {
	for _, b := range bs {
		bound[b.Var] = true
	}
}
