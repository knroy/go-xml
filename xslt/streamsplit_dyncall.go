package xslt

// Dynamic calls the split reads as the calls they are (SH2 follow-up;
// §19.8.8.11 with §19.1). The analysis gives a dynamic call's arguments the
// usages of its base's signature where §19.1 makes one available: "for a
// NamedFunctionRef, for an InlineFunctionExpr, for a MapConstructor, for a
// FunctionCall whose static type is U{function(*)}" (streamexprs.go,
// dynamicCallUsages). The split took no dynamic call but an accumulator's
// lookup, so a container holding one ran on the tree. It now reads:
//
//   - f#n(A...), f a built-in, as the call f(A...): the same function on the
//     same arguments with the same focus; f a stylesheet function, as its
//     marked reference says (markSheetRefs): a values function given its
//     arguments atomized, or a burst one given what a grounded copy has;
//   - function($p as T, ...) {B}(A...), every T atomic: B has no focus, and
//     is given A atomized;
//   - map{...}(A): a lookup by A atomized (xs:anyAtomicType);
//   - g(...)(A...), g a stylesheet function whose declared result is a
//     function, map or array type with atomic parameters: the item g
//     returns is coerced to that type (function coercion, XPath 3.1
//     §3.1.5.3), so it is given A atomized.
//
// The base itself is an operand like any other, checked as one; a partial
// application (an argument "?") is not a call and is left out.

import "github.com/knroy/go-xml/v2/xpath"

// dynCallOf classifies x: static is the call x is the same as, or nil;
// base, when static is nil, is the expression making the function item,
// whose arguments are atomized; ok is false for any other dynamic call.
func dynCallOf(x *xpath.DynamicCall) (static *xpath.FuncCall, base xpath.Expr, ok bool) {
	for _, a := range x.Args {
		if _, ph := a.(*xpath.ArgumentPlaceholder); ph {
			return nil, nil, false
		}
	}
	switch t := x.Target.(type) {
	case *xpath.NamedFunctionRef:
		if t.Cast != nil || t.Arity != len(x.Args) {
			return nil, nil, false
		}
		if stylesheetFn(t.Name) {
			sc, ok := sheetRefOf(t)
			if !ok || !sc.values {
				return nil, nil, false
			}
			return nil, &xpath.Literal{}, true // nothing to check of the reference
		}
		return &xpath.FuncCall{Name: t.Name, Args: x.Args}, nil, true
	case *xpath.InlineFunctionExpr:
		if len(t.Params) != len(x.Args) || !(viewCheck{}).funcItemSafe(t) {
			return nil, nil, false
		}
		for _, p := range t.Params {
			if p.Type == nil || !p.Type.HasAtomicType {
				return nil, nil, false
			}
		}
		return nil, &xpath.Literal{}, true // its body has no focus: funcItemSafe
	case *xpath.MapConstructor:
		return nil, t, len(x.Args) == 1
	case *xpath.FuncCall:
		sc, ok := sheetCallOf(t)
		return nil, t, ok && sc.fnValues
	}
	return nil, nil, false
}

// dynCallBurstSafe is burstSafe for a dynamic call.
func dynCallBurstSafe(x *xpath.DynamicCall, noPos bool) bool {
	static, base, ok := dynCallOf(x)
	switch {
	case !ok:
		return false
	case static != nil:
		return callSafe(static, noPos)
	}
	return burstSafe(base, noPos) && allBurstSafe(x.Args, noPos)
}

// dynCallSafe is viewCheck.safe for a dynamic call: its operands read of f
// only what f has, each argument atomized.
func (vc viewCheck) dynCallSafe(x *xpath.DynamicCall, f vfocus) bool {
	static, base, ok := dynCallOf(x)
	switch {
	case !ok:
		return false
	case static != nil:
		return vc.call(static, f, false)
	}
	if !vc.safe(base, f, false) {
		return false
	}
	for _, a := range x.Args {
		if !vc.safe(a, f, false) {
			return false
		}
	}
	return true
}
