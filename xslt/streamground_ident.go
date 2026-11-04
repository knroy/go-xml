package xslt

// Node identity in a grounded loop body (lane K3).
//
// burstBody refused is, <<, >>, union, intersect and except: the burst
// plans it was written for copied each item without its identity. The core,
// the only user of burstBody since M1.6, runs such a body on a grounded copy
// out of the stream driver, which carries its original's identity and order
// (xdm streamview.go: fOrigin, for the match, its descendants and the copies
// of its ancestors), so these operators answer on the copy as on the tree.
// What the operands read is still checked: identityOperands rewrites each
// operator into the sequence of its operands, so burstSafe sees exactly
// what they read. A comparison reads its operands' identity and order only,
// so they may end on an ancestor (`.. is current()`), as a name-only
// function's argument may; a set operator's nodes go on to be read, so its
// operands are checked as any other expression.

import (
	"reflect"

	"github.com/knroy/go-xml/v2/xpath"
)

// identityOperands rewrites in place every node comparison and set operator
// in e, a fresh parse, into the sequence of its operands (wrapped in
// exists(), the name-only check, for a comparison), and returns e.
func identityOperands(e xpath.Expr) xpath.Expr {
	if e == nil {
		return nil
	}
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) { // streamfork.go
		if v.Kind() != reflect.Interface || !v.CanSet() {
			return
		}
		b, ok := v.Interface().(*xpath.BinaryOp)
		if !ok {
			return
		}
		var items []xpath.Expr
		switch b.Op {
		case "is", "<<", ">>":
			items = []xpath.Expr{fnCall("exists", b.Left), fnCall("exists", b.Right)}
		case "union", "intersect", "except":
			items = []xpath.Expr{b.Left, b.Right}
		default:
			return
		}
		v.Set(reflect.ValueOf(xpath.Expr(&xpath.SequenceExpr{Items: items})))
	})
	return e
}
