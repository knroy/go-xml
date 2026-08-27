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
