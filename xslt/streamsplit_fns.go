package xslt

// Lane g5: function calls the streamed split reads beside burstConsumers
// (streamsplit.go walk), and the function items an outer or a motionless
// operand may hold (viewCheck.safe).

import (
	"reflect"

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

// constructorFocusFree is focusFree that also reads map and array
// constructors (map{}, [1, 2]), which focusFree, the analysis's, leaves out:
// for an xsl:iterate parameter's default (iterateParamOK).
func constructorFocusFree(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.MapConstructor:
		for i := range x.Keys {
			if !constructorFocusFree(x.Keys[i]) || !constructorFocusFree(x.Values[i]) {
				return false
			}
		}
		return true
	case *xpath.ArrayConstructor:
		for _, m := range x.Members {
			if !constructorFocusFree(m) {
				return false
			}
		}
		return true
	}
	return focusFree(e)
}

// fallbackIgnored reports whether el is an xsl:fallback in an XSLT
// instruction: one this processor recognises, so the fallback is never
// instantiated and compiles to nothing (compile_instr.go). Under an
// extension instruction it is what runs.
func fallbackIgnored(el *xdm.Node) bool {
	p := el.Parent()
	return isXSL(el, "fallback") && p != nil && p.Kind() == xdm.KindElement && p.Name().URI == xdm.NSXSL
}

// freeFilter reports whether a filter reads nothing of the focus: its base
// is focus-free and holds no node of the stream, so the predicates' focus is each of the base's items -- a
// value, or a node of a variable, never one of the stream (§19.8.4.41) --
// and they read nothing else of the instruction's focus (no current(), no
// accumulator, no function burst execution answers differently).
func freeFilter(x *xpath.FilterExpr) bool {
	// burstSafe too: current-group() and its kind are focus-free calls that
	// return nodes of the stream.
	if !constructorFocusFree(x.Base) || !burstSafe(x.Base, false) {
		return false
	}
	for _, p := range x.Predicates {
		if !burstSafe(p, false) || callsCurrent(p) {
			return false
		}
	}
	return true
}

// callsCurrent reports whether e calls fn:current().
func callsCurrent(e xpath.Expr) bool {
	found := false
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) {
		if f, ok := v.Interface().(*xpath.FuncCall); ok && isCurrentCall(f) {
			found = true
		}
	})
	return found
}
