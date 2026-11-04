package xslt

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// accumTypeHoldsNoNode reports whether a value of type t can hold no node:
// an atomic type, or a map or array type whose values or members are of
// such a type. A value @as is applied to is converted to it or refused
// (convertAccum; a map or array test is an instance check, deep, with no
// coercion), so a rule's value of such a type keeps nothing of the parse.
func accumTypeHoldsNoNode(t *xpath.SequenceType) bool {
	switch {
	case t == nil:
		return false
	case t.Empty, t.HasAtomicType, t.IsNumericType:
		return true
	case t.IsMapTest:
		return accumTypeHoldsNoNode(t.MapValue)
	case t.IsArrayTest:
		return accumTypeHoldsNoNode(t.ArrayMember)
	}
	return false
}

// accumValueCallOK reports whether x, a dynamic call, is
// accumulator-before('n')(args) or accumulator-after('n')(args) that reads
// of the focus only what f has (accumulator-077s: a lookup in a map-typed
// accumulator's value). The value a streamed run reads of an accumulator
// holds no node of the parse (accumStreamWhy): a map or an array, which
// looks the atomized argument up, an atomic value (XPTY0004 in both runs),
// or a function item made outside the rules, whose body has no focus of the
// parse or the global one. So with arguments that are atomic values it
// reaches nothing of the stream, whatever the value is.
func accumValueCallOK(vc viewCheck, x *xpath.DynamicCall, f vfocus) bool {
	t, ok := x.Target.(*xpath.FuncCall)
	if !ok || t.Name.URI != fnNS || t.Name.Local != "accumulator-before" && t.Name.Local != "accumulator-after" ||
		!vc.call(t, f, false) {
		return false
	}
	for _, a := range x.Args {
		if !atomicValued(a) || !vc.safe(a, f, false) {
			return false
		}
	}
	return true
}

// asRefusesNodes reports whether the @as of n, a variable or parameter, has
// a map or an array item type: no node is an instance of it, and none is
// converted to one (a map or array test is an instance check, deep, with no
// coercion: convertAsLazy). A built-in rule's node in its value -- the
// source's own in the tree, a copy of it off the stream -- is then the
// same type error in both modes, whose message names the variable and the
// type only, and is never kept (si-fork-812).
func asRefusesNodes(n *xdm.Node) bool {
	st, err := compileSequenceType(n.AttrValue("as"), newNSResolver(n, ""))
	return err == nil && st != nil && (st.stype.IsMapTest || st.stype.IsArrayTest)
}
