package xslt

// Lane g5: function calls the streamed split reads beside burstConsumers
// (streamsplit.go walk), and the function items an outer or a motionless
// operand may hold (viewCheck.safe).

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// splitWholeFns take their sequence argument whole, as burstConsumers do:
// the split collects P's items and the outer calls them once, at the
// context's end. The other arguments are motionless (a function item, a
// collation, a zero). splitWholeAtomizers atomize that argument first.
var (
	splitWholeFns = map[string]bool{"fold-left": true, "fold-right": true, "filter": true,
		"for-each": true, "distinct-values": true, "codepoints-to-string": true, "snapshot": true,
		// fn:trace returns its argument and prints nothing (xpath/fn_node.go).
		"trace": true}
	splitWholeAtomizers = map[string]bool{"distinct-values": true, "codepoints-to-string": true}
)

// funcItemSafe reports whether a function item expression reads nothing of
// focus f: a named reference to a function that does not depend on the
// focus (the item is made where it is written, so a focus-dependent one
// would capture f), or an inline function, whose body has no focus.
func (vc viewCheck) funcItemSafe(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.NamedFunctionRef:
		switch x.Name.URI {
		case fnNS:
			n := x.Name.Local
			return !focusDependent[keyOf(x.Name, x.Arity)] && !burstFnBanned[n] && !(vc.noIdent && identFns[n]) &&
				n != "copy-of" && n != "snapshot" && n != "current" && n != "has-children"
		case xdm.NSXS, xdm.NSMath, xdm.NSMap, xdm.NSArray:
			return true
		}
		// A stylesheet function: no focus; its arguments are what the call
		// passes, checked where it is called.
		return x.Name.URI != "" && x.Cast == nil
	case *xpath.InlineFunctionExpr:
		return vc.safe(x.Body, vNone, true)
	}
	return false
}

// copyTail reports whether a tail is a copy of the match itself: copy-of()
// or snapshot(), perhaps with "." written; it gives nodes, one per match.
func copyTail(e xpath.Expr) bool {
	f, ok := e.(*xpath.FuncCall)
	if !ok || f.Name.URI != fnNS || f.Name.Local != "copy-of" && f.Name.Local != "snapshot" {
		return false
	}
	return len(f.Args) == 0 || len(f.Args) == 1 && isContextItem(f.Args[0])
}

// traceValue is the value of fn:trace(v) or fn:trace(v, 'label'), v: the
// function returns its argument and prints nothing (xpath/fn_node.go), and a
// string literal label passes its one check. Read as v, a trace around a
// loop's select or a reducer's argument splits as that select or argument.
func traceValue(x *xpath.FuncCall) xpath.Expr {
	if x.Name.URI != fnNS || x.Name.Local != "trace" || len(x.Args) == 0 || len(x.Args) > 2 {
		return nil
	}
	if len(x.Args) == 2 {
		l, ok := x.Args[1].(*xpath.Literal)
		if !ok || l.Val == nil || l.Val.Type != xdm.TypeString {
			return nil
		}
	}
	return x.Args[0]
}
