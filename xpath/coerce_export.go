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
// The occurrence indicator is honoured, so "(function(A) as B)*" coerces every
// item of the sequence: the clause is about the item type, and a declaration
// admitting several function items has to admit each of them on the same terms
// as one.
//
// ok is false when st is not a typed function test, when the cardinality is
// wrong, or when any item is not a function item of the declared arity; the
// caller then reports the mismatch in whatever code its context requires.
func CoerceFunctionItem(v xdm.Sequence, st SequenceType) (xdm.Sequence, bool) {
	if !st.IsFunctionTest || !st.HasFunctionArity {
		return nil, false
	}
	switch st.Occurrence {
	case "", "+":
		if len(v) == 0 {
			return nil, false
		}
	}
	switch st.Occurrence {
	case "", "?":
		if len(v) > 1 {
			return nil, false
		}
	}
	out := make(xdm.Sequence, 0, len(v))
	for _, it := range v {
		fn := functionItemView(it)
		if fn == nil || fn.Arity != st.FunctionArity {
			return nil, false
		}
		out = append(out, coerceFunctionItem(fn, st))
	}
	return out, true
}

// CastToUnion applies the function conversion rules for a *pure union type*
// declared with "as".
//
// XPath 3.1 2.5.5 already makes a value whose type is one of the union's
// members an instance of the union, so such a value is returned untouched and
// this is not reached for it. What is left is the one case the conversion
// rules do convert: an xs:untypedAtomic, which is cast to the first member
// type that accepts its lexical form — exactly as a cast to the union would.
//
// import-schema-192 declares a variable "as=dateUnion" over a union of
// xs:date, xs:time and xs:dateTime and selects xs:untypedAtomic('12:00:00'),
// then asserts the result is an instance of xs:time. Without the conversion
// the value stayed untyped, matched no member, and every such variable raised
// XTTE0570 on a value the rules exist to convert for it.
//
// ok is false when st is not a pure union, so the caller keeps whatever answer
// its ordinary path gave.
func CastToUnion(a *xdm.Atomic, st SequenceType) (*xdm.Atomic, bool) {
	if len(st.SchemaUnionMembers) == 0 {
		return nil, false
	}
	out, err := castToUnion(a, st)
	if err != nil {
		return nil, false
	}
	return out, true
}
