package xpath

import "github.com/knroy/go-xml/xdm"

// CoerceFunctionItem applies the function coercion clause of the function
// conversion rules (section 3.1.5) to a value supplied where st, a typed
// function test, is declared.
//
// XSLT's own "as" checking lives in a different package but faces the same
// rule: an xsl:param declared as="function(xs:string) as xs:string" given an
// inline function whose signature is not a subtype of that must still bind,
// with the arguments and the result converted at call time. Only the coercion
// clause is exported — the atomic half of the conversion rules is already
// implemented on the XSLT side, and duplicating it here would give two answers
// to the same question.
//
// ok is false when st is not a typed function test, or when the value is not a
// single function item of the declared arity; the caller then reports the
// mismatch in whatever code its context requires.
func CoerceFunctionItem(v xdm.Sequence, st SequenceType) (xdm.Sequence, bool) {
	if !st.IsFunctionTest || !st.HasFunctionArity || len(v) != 1 {
		return nil, false
	}
	fn := functionItemView(v[0])
	if fn == nil || fn.Arity != st.FunctionArity {
		return nil, false
	}
	return xdm.One(coerceFunctionItem(fn, st)), true
}
