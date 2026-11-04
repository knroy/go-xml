package xslt

// Expression shapes of the streamed split (lane K2): set operators whose
// foreign operand sits inside a path or a simple map.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

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

// valueFor reports whether a for or quantified expression binds only atomic
// values (each binding's last step makes values: P/node-name(),
// P/xs:date(.)) and its return or test reads nothing but its variables:
// then no streamed node reaches it, and the outer evaluates it once, over
// the values, as the tree does. burstSafe's list of functions does not
// matter there; focusFree's does not know || or the xs constructors.
func valueFor(bs []xpath.Binding, ret xpath.Expr) bool {
	for _, b := range bs {
		if !valueTail(b.Seq) {
			return false
		}
	}
	return varsOnly(ret)
}

// varsOnly reports, conservatively, whether e reads neither the focus nor
// a document nor a stylesheet function: literals, variables, and operators
// and fn or xs functions over them.
func varsOnly(e xpath.Expr) bool {
	all := func(es ...xpath.Expr) bool {
		for _, x := range es {
			if !varsOnly(x) {
				return false
			}
		}
		return true
	}
	switch x := e.(type) {
	case *xpath.Literal, *xpath.VarRef:
		return true
	case *xpath.FuncCall:
		switch {
		case x.Name.URI == xdm.NSXS && len(x.Args) == 1:
		case x.Name.URI == fnNS && !focusDependent[keyOf(x.Name, len(x.Args))] && !docFns[x.Name.Local]:
		default:
			return false
		}
		return all(x.Args...)
	case *xpath.StringConcat:
		return all(x.Left, x.Right)
	case *xpath.BinaryOp:
		return all(x.Left, x.Right)
	case *xpath.UnaryOp:
		return all(x.Operand)
	case *xpath.IfExpr:
		return all(x.Cond, x.Then, x.Else)
	case *xpath.SequenceExpr:
		return all(x.Items...)
	case *xpath.CastExpr:
		return all(x.Operand)
	case *xpath.InstanceOfExpr:
		return all(x.Operand)
	case *xpath.TreatExpr:
		return all(x.Operand)
	case *xpath.LetExpr:
		return all(append(bindingExprs(x.Bindings), x.Return)...)
	case *xpath.ForExpr:
		return all(append(bindingExprs(x.Bindings), x.Return)...)
	case *xpath.QuantifiedExpr:
		return all(append(bindingExprs(x.Bindings), x.Test)...)
	}
	return false
}

// docFns read a document or a resource: not a value of the variables alone.
var docFns = map[string]bool{"doc": true, "document": true, "doc-available": true, "collection": true,
	"uri-collection": true, "unparsed-text": true, "unparsed-text-lines": true,
	"unparsed-text-available": true, "json-doc": true, "parse-xml": true, "parse-xml-fragment": true,
	"transform": true, "load-xquery-module": true}

// hoistMapChoice rewrites (if (c) then A else B) ! R, c motionless at the
// focus, as if (c) then A ! R else B ! R: R maps over whichever branch c
// chooses, and c is evaluated first in both. The choice is then the
// split's own (splitStreamIn), each branch's split run only when chosen.
// Both branches share R: the split writes into the consuming path's place
// only, which is in A or B, never in R (viewCheck's has-children rewrite
// in place is the same either time).
func hoistMapChoice(e xpath.Expr, focus vfocus, key bool) xpath.Expr {
	m, ok := e.(*xpath.SimpleMap)
	if !ok {
		return e
	}
	x, ok := hoistMapChoice(m.Left, focus, key).(*xpath.IfExpr)
	if !ok || !(viewCheck{kids: true, key: key}).safe(x.Cond, focus, true) {
		return e
	}
	return &xpath.IfExpr{Cond: x.Cond,
		Then: &xpath.SimpleMap{Left: x.Then, Right: m.Right},
		Else: &xpath.SimpleMap{Left: x.Else, Right: m.Right}}
}

// seqOfValues reports whether e is a sequence whose every item makes only
// atomic values by its last step: (@nr/string(), chtitle/string()). The
// tail then gives values only (no XPTY0018), and holding them costs what
// the result does.
func seqOfValues(e xpath.Expr) bool {
	x, ok := e.(*xpath.SequenceExpr)
	if !ok || len(x.Items) == 0 {
		return false
	}
	for _, it := range x.Items {
		if !valueTail(it) {
			return false
		}
	}
	return true
}
