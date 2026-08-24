package xpath

import (
	"math"

	"github.com/knroy/go-xml/xdm"
)

// XPath 1.0 compatibility mode.
//
// XPath 2.0 appendix B.1 lists the places where an expression evaluated with
// the compatibility flag set behaves as XPath 1.0 did. The rules are all
// coercions: where 2.0 raises XPTY0004 because a value has the wrong type or
// the wrong cardinality, 1.0 silently converted it, so every one of them
// admits an expression that 2.0 refuses rather than changing the answer to one
// 2.0 already gives. That asymmetry is what makes the flag safe to leave off:
// nothing here is reachable unless Context.Compat is set, and only a Compiled
// carrying the static flag ever sets it.

// compatNumber applies fn:number to an atomic value, yielding NaN where the
// conversion fails rather than an error.
//
// B.1 rule 2: where an operand of an arithmetic operator, or of a relational
// general comparison, is not numeric, 1.0 applied number() to it. "'apple' + 1"
// is NaN in 1.0 and XPTY0004 in 2.0.
func compatNumber(a *xdm.Atomic) *xdm.Atomic {
	if a == nil {
		return xdm.NewDouble(math.NaN())
	}
	if a.Type == xdm.TypeDouble {
		return a
	}
	conv, err := CastAtomic(a, xdm.TypeDouble)
	if err != nil {
		return xdm.NewDouble(math.NaN())
	}
	return conv
}

// compatNumberSeq is compatNumber over a whole sequence, applying the
// cardinality rule first: 1.0 had no sequences, so a multi-item operand is
// reduced to its first item rather than raising XPTY0004. The empty sequence
// becomes NaN, which is what number(()) gives.
func compatNumberSeq(seq xdm.Sequence) *xdm.Atomic {
	atoms := xdm.Atomize(seq)
	if len(atoms) == 0 {
		return xdm.NewDouble(math.NaN())
	}
	return compatNumber(atoms[0].(*xdm.Atomic))
}

// compatFirst truncates a sequence to its first item.
//
// B.1 rule 1: where the expected type of a function argument is xs:string,
// xs:double or a node, and the supplied value has more than one item, 1.0 took
// the first and discarded the rest. A node-set was the only multi-valued thing
// 1.0 could produce, and string() and number() of a node-set were defined on
// its first node in document order.
func compatFirst(seq xdm.Sequence) xdm.Sequence {
	if len(seq) > 1 {
		return seq[:1]
	}
	return seq
}

// compatGeneralCompare applies the XPath 1.0 conversion rules to the operands
// of a general comparison, returning the pair to compare and whether the rules
// applied at all.
//
// XPath 1.0 section 3.4 states them in terms of node-sets, but the effect once
// the operands are atomized is a precedence over the operand types:
//
//   - if either operand is a boolean, both become booleans (via the effective
//     boolean value, so "” = true()" is false and "'false' = true()" is true —
//     a non-empty string is true, its content notwithstanding);
//   - otherwise, if the operator is relational (<, <=, >, >=), both become
//     doubles, because 1.0 defined those four on numbers only. "'10' > '2'" is
//     true in 1.0 and false in 2.0, which backwards-031 checks;
//   - otherwise, if either operand is numeric, both become doubles;
//   - otherwise both are compared as strings, which is what 2.0 does anyway.
//
// The boolean rule takes precedence over the relational one: 1.0 converted to
// boolean whenever either side was one, whatever the operator.
func compatGeneralCompare(l, r, la, ra xdm.Sequence, op string) (xdm.Sequence, xdm.Sequence, bool) {
	if hasType(la, xdm.TypeBoolean) || hasType(ra, xdm.TypeBoolean) {
		lb, err1 := EffectiveBooleanValue(l)
		rb, err2 := EffectiveBooleanValue(r)
		if err1 != nil || err2 != nil {
			return la, ra, false
		}
		return xdm.One(xdm.NewBoolean(lb)), xdm.One(xdm.NewBoolean(rb)), true
	}
	// The relational operators convert to number whatever the operand types
	// are -- 1.0 defined <, <=, > and >= on numbers only, which is why
	// "'10' > '2'" is true there and false under 2.0 string ordering
	// (backwards-031). The existential quantification is unaffected: the
	// comparison still holds if some pair compares true, so each operand is
	// converted item by item rather than being reduced to its first.
	// boolean-069 compares two two-item sequences and needs three of the four
	// operators to find their pair.
	if op == "<" || op == "<=" || op == ">" || op == ">=" {
		return numberEach(la), numberEach(ra), true
	}
	// For "=" and "!=" the node-set side keeps its cardinality: 1.0's rule is
	// that the comparison holds if *some* node of the set, converted to a
	// number, compares true. "3 = following-sibling::*" in predicate-003 has
	// to try every following sibling, and truncating to the first would answer
	// on the wrong one; "(1 to 5) = ('apple', ..., '5.00e0')" in
	// backwards-033 needs both sides converted item by item, so that the one
	// string that is a number matches.
	if isNumericSeq(la) || isNumericSeq(ra) {
		return numberEach(la), numberEach(ra), true
	}
	return la, ra, false
}

// hasType reports whether any item of an atomized sequence has type t.
func hasType(seq xdm.Sequence, t xdm.TypeCode) bool {
	for _, it := range seq {
		if a, ok := it.(*xdm.Atomic); ok && a.Type == t {
			return true
		}
	}
	return false
}

// isNumericSeq reports whether any item of an atomized sequence is numeric.
// xs:untypedAtomic is deliberately excluded: it is what a node atomizes to,
// and a node-set compared against a string compares as a string in 1.0 too.
func isNumericSeq(seq xdm.Sequence) bool {
	for _, it := range seq {
		if a, ok := it.(*xdm.Atomic); ok && a.Type.IsNumeric() {
			return true
		}
	}
	return false
}

// temporalOperands reports whether either atomized operand is a date, a time
// or a duration, in which case the compatibility coercions for arithmetic do
// not apply. See the note in evalArithmetic.
func temporalOperands(la, ra xdm.Sequence) bool {
	for _, seq := range []xdm.Sequence{la, ra} {
		for _, it := range seq {
			a, ok := it.(*xdm.Atomic)
			if !ok {
				continue
			}
			if isDateLike(a.Type) || isDurationLike(a.Type) {
				return true
			}
		}
	}
	return false
}

// seqParamFuncs names the fn: functions whose arguments are declared with an
// occurrence indicator, and whose arguments therefore must NOT be reduced to
// a first item under XPath 1.0 compatibility.
//
// B.1 rule 1 applies only where the expected type is xs:string, xs:double or a
// single node. Everything else keeps 2.0 cardinality, and truncating a
// sequence-valued argument would silently change what the function computes:
// count(), sum() and string-join() would all answer on one item.
//
// fn:id and fn:idref are here for a different reason: their argument really is
// declared xs:string*, and xpath-compat-0501 exists to check that a 1.0 scope
// does *not* make id() take only its first node. The suite is explicit that
// this is the one place the truncation rule does not reach.
var seqParamFuncs = map[string]bool{
	"count": true, "sum": true, "avg": true, "max": true, "min": true,
	"string-join": true, "distinct-values": true, "index-of": true,
	"insert-before": true, "remove": true, "reverse": true,
	"subsequence": true, "unordered": true, "deep-equal": true,
	"empty": true, "exists": true, "zero-or-one": true, "one-or-more": true,
	"exactly-one": true, "data": true, "boolean": true, "not": true,
	"id": true, "idref": true, "trace": true, "error": true,
	"concat": true, "last": true, "position": true,
	// fn:doc and friends take a single string, but their result is a sequence
	// and the argument is xs:string?, so the empty case must survive; the
	// truncation below leaves a zero- or one-item sequence untouched anyway.
}

// compatCoerceArgs applies B.1 rule 1 to an evaluated argument list.
//
// The rule is stated per-parameter in terms of the expected type, but this
// engine's builtins are registered as plain Go closures with no machine-
// readable signature, so the choice is made per-function instead: everything
// but the sequence-taking functions above expects a string, a number or a
// single node in every position, and for those a multi-item argument is
// XPTY0004 under 2.0 and the first item under 1.0.
//
// It is deliberately confined to the fn: namespace. A user's own xsl:function
// declares its parameter types explicitly with @as, and section 3.8 does not
// put those under the compatibility rules; coercing them would discard items a
// stylesheet passed on purpose.
func compatCoerceArgs(name xdm.QName, args []xdm.Sequence) []xdm.Sequence {
	if name.URI != xdm.NSFN {
		return args
	}
	var out []xdm.Sequence
	// The truncation half of the rule. A sequence-taking function is exempt
	// from it but not from the conversions below: fn:string-join takes a
	// sequence in position 0 and an xs:string separator in position 1, and
	// xpath-compat-0303 passes a node-set and an integer for that separator.
	if !seqParamFuncs[name.Local] {
		for i, a := range args {
			if len(a) <= 1 {
				continue
			}
			if out == nil {
				out = make([]xdm.Sequence, len(args))
				copy(out, args)
			}
			out[i] = a[:1]
		}
	}

	// The second half of rule 1: where the expected type is xs:string or
	// xs:double and the supplied item is neither, 1.0 applied string() or
	// number() to it rather than raising XPTY0004. Which positions those are
	// is a property of the signature, so it comes from a table.
	conv := func(pos []int, to xdm.TypeCode) {
		for _, i := range pos {
			if i >= len(args) {
				continue
			}
			src := args
			if out != nil {
				src = out
			}
			atoms := xdm.Atomize(src[i])
			if len(atoms) == 0 {
				// number(()) is NaN in 1.0, so a path selecting no nodes
				// makes round() and floor() answer NaN rather than the empty
				// sequence 2.0 gives. version-021 and xpath-compat-0302 both
				// turn on that. A missing xs:string argument stays missing:
				// string(()) is "", which is what the callee already does
				// with an empty sequence.
				if to == xdm.TypeDouble && i < len(args) {
					if out == nil {
						out = make([]xdm.Sequence, len(args))
						copy(out, args)
					}
					out[i] = xdm.One(xdm.NewDouble(math.NaN()))
				}
				continue
			}
			// A sequence-taking function was exempted from the blanket
			// truncation above, but a position the table declares xs:string
			// or xs:double is a singleton position whatever the rest of the
			// signature is, so the first item is taken here.
			a, ok := atoms[0].(*xdm.Atomic)
			if !ok {
				continue
			}
			if a.Type == to && len(src[i]) == 1 {
				continue
			}
			var repl xdm.Item
			if to == xdm.TypeDouble {
				repl = compatNumber(a)
			} else {
				if (a.Type == xdm.TypeUntypedAtomic || a.Type == xdm.TypeAnyURI) &&
					len(src[i]) == 1 {
					continue
				}
				repl = xdm.NewString(a.String())
			}
			if out == nil {
				out = make([]xdm.Sequence, len(args))
				copy(out, args)
			}
			out[i] = xdm.One(repl)
		}
	}
	conv(stringParams[name.Local], xdm.TypeString)
	conv(doubleParams[name.Local], xdm.TypeDouble)

	if out == nil {
		return args
	}
	return out
}

// stringParams names, per fn: function, the argument positions declared
// xs:string (or xs:string?) whose supplied value 1.0 would have converted with
// string().
//
// B.1 rule 1 is stated as "if the expected type is xs:string ... fn:string is
// applied", and the expected type is per-parameter, so a table is the only way
// to express it against builtins registered as untyped closures. Only the
// functions the compatibility tests actually reach are listed: adding a
// position that is not really xs:string would make a genuine type error into
// a silent conversion, which is a worse failure than the XPTY0004 it replaces.
//
// The positions are 0-based. A function whose every parameter is xs:string
// lists all of them.
var stringParams = map[string][]int{
	"substring-before":     {0, 1},
	"substring-after":      {0, 1},
	"substring":            {0},
	"contains":             {0, 1},
	"starts-with":          {0, 1},
	"ends-with":            {0, 1},
	"compare":              {0, 1},
	"codepoint-equal":      {0, 1},
	"string-length":        {0},
	"normalize-space":      {0},
	"upper-case":           {0},
	"lower-case":           {0},
	"translate":            {0, 1, 2},
	"matches":              {0, 1},
	"replace":              {0, 1, 2},
	"tokenize":             {0, 1},
	"string-join":          {1},
	"encode-for-uri":       {0},
	"iri-to-uri":           {0},
	"escape-html-uri":      {0},
	"resolve-uri":          {0, 1},
	"normalize-unicode":    {0, 1},
	"string-to-codepoints": {0},
	"lang":                 {0},
}

// doubleParams is stringParams for the positions declared xs:double, where 1.0
// applied number() instead. xpath-compat-0401 passes '4' and '2.00001' as the
// position and length of fn:subsequence, which 2.0 refuses.
var doubleParams = map[string][]int{
	"subsequence":        {1, 2},
	"substring":          {1, 2},
	"round":              {0},
	"ceiling":            {0},
	"floor":              {0},
	"round-half-to-even": {0},
	"abs":                {0},
}

// numberEach applies number() to every item of an atomized sequence, which is
// what the 1.0 rule for "=" and "!=" against a node-set amounts to once both
// sides are atomized: each candidate pair is compared as numbers, and an item
// that is not a number becomes NaN and matches nothing.
func numberEach(seq xdm.Sequence) xdm.Sequence {
	out := make(xdm.Sequence, 0, len(seq))
	for _, it := range seq {
		a, ok := it.(*xdm.Atomic)
		if !ok {
			continue
		}
		out = append(out, compatNumber(a))
	}
	return out
}
