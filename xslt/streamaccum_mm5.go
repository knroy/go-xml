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
