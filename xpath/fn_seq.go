package xpath

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// registerSeqFuncs adds the sequence and aggregate functions.
func registerSeqFuncs(l *Library) {
	l.registerFn("count", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		return intSeq(int64(len(args[0]))), nil
	})

	l.registerFn("empty", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		return boolSeq(len(args[0]) == 0), nil
	})

	l.registerFn("exists", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		return boolSeq(len(args[0]) > 0), nil
	})

	l.registerFn("boolean", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		b, err := EffectiveBooleanValue(args[0])
		if err != nil {
			return nil, err
		}
		return boolSeq(b), nil
	})

	l.registerFn("not", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		b, err := EffectiveBooleanValue(args[0])
		if err != nil {
			return nil, err
		}
		return boolSeq(!b), nil
	})

	l.registerFn("true", []int{0}, func(*Context, []xdm.Sequence) (xdm.Sequence, error) {
		return boolSeq(true), nil
	})

	l.registerFn("false", []int{0}, func(*Context, []xdm.Sequence) (xdm.Sequence, error) {
		return boolSeq(false), nil
	})

	l.registerFn("data", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		return xdm.Atomize(args[0]), nil
	})

	l.registerFn("position", []int{0}, func(ctx *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
		if ctx.Position == 0 {
			return nil, fmt.Errorf("XPDY0002: position() with no context position")
		}
		return intSeq(int64(ctx.Position)), nil
	})

	l.registerFn("last", []int{0}, func(ctx *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
		if ctx.Size == 0 {
			return nil, fmt.Errorf("XPDY0002: last() with no context size")
		}
		return intSeq(int64(ctx.Size)), nil
	})

	l.registerFn("reverse", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		in := args[0]
		out := make(xdm.Sequence, len(in))
		for i, it := range in {
			out[len(in)-1-i] = it
		}
		return out, nil
	})

	l.registerFn("subsequence", []int{2, 3}, fnSubsequence)

	l.registerFn("insert-before", []int{3}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		pos, err := argNumber(args, 1)
		if err != nil {
			return nil, err
		}
		// $position is declared xs:integer, not xs:integer?, so an empty
		// sequence is a type error rather than a defaulted position.
		if pos == nil {
			return nil, fmt.Errorf(
				"XPTY0004: an empty sequence is not allowed as the second " +
					"argument of fn:insert-before()")
		}
		target := args[0]
		// A position outside int64 is simply past one end or the other, which
		// the clamps below handle — but only if they see the real magnitude.
		// Int64() truncates, so 2^64+2 arrived as 2 and inserted into the
		// middle of the sequence instead of after it.
		at := clampPosition(pos, len(target)+1)
		if at < 1 {
			at = 1
		}
		if at > len(target)+1 {
			at = len(target) + 1
		}
		out := make(xdm.Sequence, 0, len(target)+len(args[2]))
		out = append(out, target[:at-1]...)
		out = append(out, args[2]...)
		out = append(out, target[at-1:]...)
		return out, nil
	})

	l.registerFn("remove", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The position is declared xs:integer, so a decimal is a type error
		// rather than a value to truncate: remove(1 to 10, 1.0) is XPTY0004.
		if err := requireIntegerArg("remove", args, 1); err != nil {
			return nil, err
		}
		pos, err := argNumber(args, 1)
		if err != nil {
			return nil, err
		}
		if pos == nil {
			return nil, fmt.Errorf(
				"XPTY0004: an empty sequence is not allowed as the second " +
					"argument of fn:remove()")
		}
		in := args[0]
		at := clampPosition(pos, len(in)+1)
		if at < 1 || at > len(in) {
			return in, nil
		}
		out := make(xdm.Sequence, 0, len(in)-1)
		out = append(out, in[:at-1]...)
		out = append(out, in[at:]...)
		return out, nil
	})

	// The collation argument of these four is not merely validated: it
	// selects how their string comparisons are made. Binding it into the
	// context is what carries it down to compareValues, which is where the
	// comparison actually happens.
	l.registerFn("distinct-values", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "distinct-values", args, 1)
		if err != nil {
			return nil, err
		}
		return fnDistinctValues(withCollation(ctx, coll), args)
	})
	l.registerFn("index-of", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "index-of", args, 2)
		if err != nil {
			return nil, err
		}
		return fnIndexOf(withCollation(ctx, coll), args)
	})

	l.registerFn("sum", []int{1, 2}, fnSum)
	l.registerFn("min", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "min", args, 1)
		if err != nil {
			return nil, err
		}
		return minMax(withCollation(ctx, coll), args[0], true)
	})
	l.registerFn("max", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "max", args, 1)
		if err != nil {
			return nil, err
		}
		return minMax(withCollation(ctx, coll), args[0], false)
	})
	l.registerFn("avg", []int{1}, fnAvg)

	l.registerFn("zero-or-one", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args[0]) > 1 {
			return nil, fmt.Errorf("FORG0003: zero-or-one() got %d items", len(args[0]))
		}
		return args[0], nil
	})
	l.registerFn("one-or-more", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args[0]) == 0 {
			return nil, fmt.Errorf("FORG0004: one-or-more() got an empty sequence")
		}
		return args[0], nil
	})
	l.registerFn("exactly-one", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args[0]) != 1 {
			return nil, fmt.Errorf("FORG0005: exactly-one() got %d items", len(args[0]))
		}
		return args[0], nil
	})
}

// fnSubsequence implements fn:subsequence, which uses the same rounded,
// out-of-range-tolerant interval arithmetic as fn:substring.
func fnSubsequence(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	in := args[0]
	startA, err := argNumber(args, 1)
	if err != nil {
		return nil, err
	}
	if startA == nil {
		return xdm.Empty, nil
	}
	start := roundHalfEven(startA.Float64())
	n := float64(len(in))

	end := n + 1
	if len(args) > 2 {
		lenA, err := argNumber(args, 2)
		if err != nil {
			return nil, err
		}
		if lenA == nil {
			return xdm.Empty, nil
		}
		end = start + roundHalfEven(lenA.Float64())
	}
	if isNaNf(start) || isNaNf(end) {
		return xdm.Empty, nil
	}
	lo, hi := start, end
	if lo < 1 {
		lo = 1
	}
	if hi > n+1 {
		hi = n + 1
	}
	if hi <= lo {
		return xdm.Empty, nil
	}
	return in[int(lo)-1 : int(hi)-1], nil
}

// fnDistinctValues removes duplicates by value equality.
//
// Values of different types can be equal (1 and 1.0), so the key is the
// compared value rather than the lexical form. Numerics therefore share a key
// space, while strings and untypedAtomic share another — matching the rule
// that distinct-values compares with eq semantics under a single collation.
func fnDistinctValues(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	atoms := xdm.Atomize(args[0])
	var out xdm.Sequence
	seen := map[string]bool{}

	// Equality between two numerics is decided in their promoted type, so the
	// precision the keys are taken at depends on the sequence as a whole: with
	// an xs:float present, a decimal is compared *as a float*, which makes
	// xs:decimal("1.2") and xs:float("1.2") the same value. A per-item key
	// cannot see that, so the sequence is scanned first.
	narrowToFloat := false
	for _, it := range atoms {
		if a, ok := it.(*xdm.Atomic); ok && a.Type == xdm.TypeFloat {
			narrowToFloat = true
			break
		}
	}

	for _, it := range atoms {
		a := it.(*xdm.Atomic)
		key, err := valueKey(ctx, a)
		if err != nil {
			return nil, err
		}
		if narrowToFloat && a.Type.IsNumeric() && !a.IsNaN() {
			key = "n\x00" + strconv.FormatFloat(
				float64(float32(a.Float64())), 'g', 17, 32)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
	}
	return out, nil
}

// valueKey produces a string that is equal exactly when two atomic values are
// the same value for the purposes of fn:distinct-values.
//
// That is not quite "eq": distinct-values is defined on value identity, so it
// collapses two NaNs into one where "eq" would call them unequal. Saxon agrees,
// and TestDistinctValuesNumericPromotion pins it.
func valueKey(ctx *Context, a *xdm.Atomic) (string, error) {
	switch {
	case a.IsNaN():
		// One key for every NaN, so they collapse rather than accumulate.
		return "NaN", nil
	case a.Type == xdm.TypeFloat:
		// A float is compared against anything else by promoting that other
		// value to float, so the key has to be at float precision.
		// Formatting it at 64 bits gave xs:float("1.2") the key 1.2000000476837158
		// where xs:decimal("1.2") keyed as 1.2, and distinct-values kept two
		// values the spec calls equal.
		return "n\x00" + strconv.FormatFloat(
			float64(float32(a.Float64())), 'g', 17, 32), nil
	case a.Type == xdm.TypeDouble:
		// A double and a decimal are compared by promoting the decimal, so
		// the key for both has to be the double. Keying a double on its exact
		// rational instead would give xs:double(1.2) the binary expansion
		// 5404319552844595/4503599627370496 while the literal 1.2 keyed as
		// 6/5, and distinct-values would keep two values the spec calls equal.
		return "n\x00" + strconv.FormatFloat(a.Float64(), 'g', 17, 64), nil
	case a.Type.IsNumeric():
		// An integer or decimal keys on the same double when one exists, so
		// that it collides with an equal double; the exact rational is kept
		// as a suffix so two decimals that differ below double precision are
		// still distinguished.
		f, _ := ratOf(a).Float64()
		return "n\x00" + strconv.FormatFloat(f, 'g', 17, 64), nil
	case a.Type == xdm.TypeBoolean:
		return fmt.Sprintf("b\x00%t", a.Bool()), nil
	case isStringLike(a.Type):
		// Two strings are the same value under the collation in force, not
		// under codepoint equality: with a case-blind collation "THou" and
		// "though" key alike, so distinct-values collapses them. The
		// collation's own key is what makes that work as a hash key.
		if ctx != nil && a.Type != xdm.TypeAnyURI {
			// Key is optional: a host-supplied collation need not offer one,
			// and without it there is no sound way to hash by that
			// collation, so the raw string stands.
			if k, ok := ctx.collation.(interface{ Key(string) string }); ok {
				return "s\x00" + k.Key(a.Str()), nil
			}
		}
		return "s\x00" + a.Str(), nil
	case isDurationLike(a.Type):
		// Two durations are the same value when their months and seconds
		// agree, whatever subtype each is: xs:yearMonthDuration("P0Y") and
		// xs:dayTimeDuration("P0D") are both the zero duration. Keying on the
		// type as well kept them apart, because their canonical forms are
		// "P0M" and "PT0S".
		d := a.DurationVal()
		if d == nil {
			break
		}
		return fmt.Sprintf("d\x00%d\x00%s",
			d.SignedMonths(), d.SignedSeconds().RatString()), nil
	case isCalendarLike(a.Type) && !xdm.IsGregorian(a.Type):
		// Two dates or times are the same value when they name the same
		// instant, which the lexical form does not show: a dateTime with no
		// timezone takes the implicit one, so "2008-01-01T13:00:00" and
		// "2008-01-01T13:00:00Z" are one value under the default UTC. Keying
		// on the lexical form kept them apart.
		//
		// The Gregorian types are excluded because they are partial — a
		// gMonthDay names no instant — and keep their lexical key.
		dt := a.DateTimeVal()
		if dt == nil {
			break
		}
		return "t\x00" + a.TypeName() + "\x00" +
			dt.ToSeconds(ctxImplicitTimezone(ctx)).RatString(), nil
	}
	return "o\x00" + a.TypeName() + "\x00" + a.String(), nil
}

// ctxImplicitTimezone is the implicit timezone, or UTC when there is no
// context.
func ctxImplicitTimezone(ctx *Context) int {
	if ctx == nil {
		return 0
	}
	return ctx.ImplicitTimezone
}

func fnIndexOf(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	target := xdm.Atomize(args[1])
	if len(target) != 1 {
		return nil, xdm.ErrType("index-of: search value must be a single item")
	}
	t := target[0].(*xdm.Atomic)
	var out xdm.Sequence
	for i, it := range xdm.Atomize(args[0]) {
		eq, err := compareValues(ctx, it.(*xdm.Atomic), t, "eq", true)
		if err != nil {
			continue // incomparable types simply do not match
		}
		if eq {
			out = append(out, xdm.NewInteger(int64(i+1)))
		}
	}
	return out, nil
}

// fnSum adds a sequence.
//
// The zero argument matters: fn:sum(()) is 0, but fn:sum((), ()) is the empty
// sequence. That is the mechanism a stylesheet uses to distinguish "no values"
// from "values summing to zero".
func fnSum(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	atoms := xdm.Atomize(args[0])
	if len(atoms) == 0 {
		if len(args) > 1 {
			return args[1], nil
		}
		return intSeq(0), nil
	}

	// A plain xs:duration is not summable, for the same reason it is not
	// orderable: it carries both months and seconds, and the two have no
	// common unit. With a single item the accumulator loop never runs, so
	// nothing checked the type and the value was returned as its own sum.
	if first := atoms[0].(*xdm.Atomic); first.Type == xdm.TypeDuration {
		return nil, xdm.Errorf("FORG0006",
			"fn:sum: %s is not summable", first.TypeName())
	}

	acc, err := toNumericOrDuration(atoms[0].(*xdm.Atomic))
	if err != nil {
		return nil, err
	}
	for _, it := range atoms[1:] {
		operand, err := toNumericOrDuration(it.(*xdm.Atomic))
		if err != nil {
			return nil, err
		}
		acc, err = arithmetic(acc, operand, "+")
		if err != nil {
			// Mixing types the aggregate cannot combine — a duration with a
			// number, or two different duration subtypes — is FORG0006,
			// "invalid argument type", rather than the general XPTY0004 that
			// the underlying operator raises for any bad operand pair.
			//
			// An arithmetic failure is not a type failure, though: the
			// operands were combinable and the *result* did not fit. Those
			// codes pass through, or an overflow would be reported as bad
			// input.
			if code := xdm.ErrorCode(err); strings.HasPrefix(code, "FODT") ||
				strings.HasPrefix(code, "FOAR") {
				return nil, err
			}
			return nil, xdm.Errorf("FORG0006",
				"fn:sum: operands cannot be combined: %v", err)
		}
	}
	return xdm.One(acc), nil
}

func fnAvg(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	atoms := xdm.Atomize(args[0])
	if len(atoms) == 0 {
		return xdm.Empty, nil
	}
	sum, err := fnSum(ctx, []xdm.Sequence{args[0]})
	if err != nil {
		return nil, err
	}
	total := sum[0].(*xdm.Atomic)
	res, err := arithmetic(total, xdm.NewInteger(int64(len(atoms))), "div")
	if err != nil {
		return nil, xdm.Errorf("FORG0006", "fn:avg: %v", err)
	}
	return xdm.One(res), nil
}

// toNumericOrDuration converts an aggregate operand: durations are summable
// as themselves, everything else must be numeric.
func toNumericOrDuration(a *xdm.Atomic) (*xdm.Atomic, error) {
	if isDurationLike(a.Type) {
		return a, nil
	}
	v, err := toNumeric(a)
	if err != nil {
		// The aggregates have their own code for an operand of the wrong
		// type: FORG0006, "invalid argument type", rather than the general
		// XPTY0004. The distinction matters to a caller deciding whether the
		// stylesheet is wrong or the data is.
		//
		// xs:untypedAtomic is the exception, and it is the common case:
		// element content is untyped, so the conversion to double *is*
		// defined and what failed was the value, not the type. That is
		// FORG0001 — avg() over an element containing "Jane Doe" is bad data
		// in a well-formed expression.
		if a.Type == xdm.TypeUntypedAtomic {
			return nil, err
		}
		return nil, xdm.Errorf("FORG0006",
			"invalid argument type %s for an aggregate", a.TypeName())
	}
	return v, nil
}

// minMax implements fn:min and fn:max.
//
// Both return the empty sequence for an empty input and propagate NaN: if any
// value is NaN the result is NaN, because no total order exists over the
// sequence otherwise.
func minMax(ctx *Context, seq xdm.Sequence, wantMin bool) (xdm.Sequence, error) {
	atoms := xdm.Atomize(seq)
	if len(atoms) == 0 {
		return xdm.Empty, nil
	}

	// Every item is converted first, so a sequence containing something
	// incomparable is an error even when an earlier item is NaN. Returning
	// NaN the moment one was seen skipped validating the rest, so
	// "min((xs:float('NaN'), 1, 'a string'))" answered NaN where the spec
	// requires FORG0006.
	vals := make([]*xdm.Atomic, 0, len(atoms))
	sawNaN := false
	for _, it := range atoms {
		a := it.(*xdm.Atomic)
		// untypedAtomic is cast to double for min/max, matching the rule for
		// arithmetic rather than the string comparison used elsewhere.
		if a.Type == xdm.TypeUntypedAtomic {
			conv, err := CastAtomic(a, xdm.TypeDouble)
			if err != nil {
				return nil, err
			}
			a = conv
		}
		if a.IsNaN() {
			sawNaN = true
		}
		vals = append(vals, a)
	}

	// The sequence must be comparable as a whole: numerics with numerics,
	// strings with strings. Mixing them is FORG0006, "invalid argument type",
	// rather than the XPTY0004 the comparison operator would raise.
	// Some types have no ordering at all, so there is no smallest or largest
	// one even in a sequence of a single item. A QName is a pair of names, and
	// xs:duration mixes months with seconds — the two have no common unit, so
	// P1M and P30D are simply not comparable. Both were being returned as
	// their own maximum.
	if k := comparisonKind(vals[0]); k == cmpOther ||
		vals[0].Type == xdm.TypeDuration {
		return nil, xdm.Errorf("FORG0006",
			"fn:min/fn:max: %s is not ordered", vals[0].TypeName())
	}

	kind := comparisonKind(vals[0])
	for _, a := range vals[1:] {
		if comparisonKind(a) != kind {
			return nil, xdm.Errorf("FORG0006",
				"fn:min/fn:max: sequence mixes %s with %s",
				vals[0].TypeName(), a.TypeName())
		}
	}
	if sawNaN {
		// NaN is returned in the sequence's promoted type, not always as a
		// double: min((3, xs:float("NaN"))) is an xs:float, because the
		// integer promotes to float to be compared with it.
		if kind == cmpNumeric && promotedNumericType(vals) == xdm.TypeFloat {
			return xdm.One(xdm.NewFloat(math.NaN())), nil
		}
		return xdm.One(xdm.NewDouble(math.NaN())), nil
	}

	best := vals[0]
	for _, a := range vals[1:] {
		op := "gt"
		if wantMin {
			op = "lt"
		}
		better, err := compareValues(ctx, a, best, op, false)
		if err != nil {
			return nil, xdm.Errorf("FORG0006", "fn:min/fn:max: %v", err)
		}
		if better {
			best = a
		}
	}
	// The result type is the promoted type of the sequence, not the type of
	// whichever item happened to win: min((1, xs:float(2))) is an xs:float,
	// because the integer promotes to compare with the float in the first
	// place.
	if kind == cmpNumeric {
		if t := promotedNumericType(vals); t != best.Type {
			conv, err := CastAtomic(best, t)
			if err == nil {
				best = conv
			}
		}
	}
	// The same rule on the string side: xs:anyURI promotes to xs:string to be
	// compared with one, so a sequence mixing the two yields an xs:string
	// whichever item wins.
	if kind == cmpString && best.Type != xdm.TypeString {
		for _, a := range vals {
			if a.Type == xdm.TypeString {
				if conv, err := CastAtomic(best, xdm.TypeString); err == nil {
					best = conv
				}
				break
			}
		}
	}
	return xdm.One(best), nil
}

// comparisonKind groups types into the families that can be compared with each
// other. Two values of different families have no ordering.
type cmpKind int

const (
	cmpNumeric cmpKind = iota
	cmpString
	cmpBoolean
	cmpDate
	cmpDuration
	cmpOther
)

func comparisonKind(a *xdm.Atomic) cmpKind {
	switch {
	case a.Type.IsNumeric():
		return cmpNumeric
	case isStringLike(a.Type):
		return cmpString
	case a.Type == xdm.TypeBoolean:
		return cmpBoolean
	case isCalendarLike(a.Type):
		return cmpDate
	case isDurationLike(a.Type):
		return cmpDuration
	}
	return cmpOther
}

// promotedNumericType returns the type the numeric lattice promotes a mixed
// sequence to: integer → decimal → float → double.
func promotedNumericType(vals []*xdm.Atomic) xdm.TypeCode {
	rank := func(t xdm.TypeCode) int {
		switch t {
		case xdm.TypeInteger:
			return 0
		case xdm.TypeDecimal:
			return 1
		case xdm.TypeFloat:
			return 2
		case xdm.TypeDouble:
			return 3
		}
		return 0
	}
	best := xdm.TypeInteger
	for _, a := range vals {
		if rank(a.Type) > rank(best) {
			best = a.Type
		}
	}
	return best
}

// roundHalfEven rounds to the nearest integer, ties away from zero, which is
// the rounding fn:round and the substring functions specify.
func roundHalfEven(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return f
	}
	return math.Floor(f + 0.5)
}

func isNaNf(f float64) bool { return math.IsNaN(f) }

// exactRound rounds an exact rational to a given number of decimal places,
// used by fn:round and fn:round-half-to-even on decimal input where going
// through float64 would introduce error.
func exactRound(r *big.Rat, places int, halfToEven bool) *big.Rat {
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(places))), nil))
	if places < 0 {
		scale = new(big.Rat).Inv(scale)
	}
	scaled := new(big.Rat).Mul(r, scale)

	num, den := scaled.Num(), scaled.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))

	// Compare |rem|*2 with den to find which side of the halfway point we are.
	twice := new(big.Int).Abs(new(big.Int).Mul(rem, big.NewInt(2)))
	cmp := twice.Cmp(den)

	roundAway := cmp > 0
	if cmp == 0 {
		if halfToEven {
			roundAway = q.Bit(0) == 1
		} else {
			// Ties round toward positive infinity, per fn:round.
			roundAway = scaled.Sign() > 0
		}
	}
	if roundAway {
		if scaled.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return new(big.Rat).Quo(new(big.Rat).SetInt(q), scale)
}

// requireIntegerArg rejects a numeric argument whose type is not xs:integer.
//
// argNumber accepts any numeric type and the callers then truncate, which is
// right for a parameter declared xs:double but wrong for one declared
// xs:integer: there the spec wants a type error, not a rounded value.
func requireIntegerArg(fn string, args []xdm.Sequence, i int) error {
	if i >= len(args) || len(args[i]) == 0 {
		return nil
	}
	atoms := xdm.Atomize(args[i])
	if len(atoms) != 1 {
		return nil // arity is the caller's problem
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return nil
	}
	if a.Type != xdm.TypeInteger && a.Type != xdm.TypeUntypedAtomic {
		return xdm.ErrType("fn:%s: argument %d is %s, not xs:integer",
			fn, i+1, a.TypeName())
	}
	return nil
}

// clampPosition narrows a position argument to an int without wrapping.
//
// Atomic.Int64 truncates, so a position outside int64 arrived as an unrelated
// small number: fn:remove((1,2,3), 2^64+2) deleted the second item rather than
// leaving the sequence alone. A position past either end is clamped to just
// past it, which is what every caller's own bounds check then acts on.
func clampPosition(pos *xdm.Atomic, past int) int {
	if pos.FitsInt64() {
		if v := pos.Int64(); v >= int64(math.MinInt) && v <= int64(math.MaxInt) {
			return int(v)
		}
	}
	if r := pos.Rat(); r != nil && r.Sign() < 0 {
		return 0
	}
	return past
}

// GroupingKey returns a string that is identical for two atomic values that
// compare equal, and different otherwise.
//
// XSLT needs this for xsl:for-each-group, whose grouping keys are compared by
// value rather than by lexical form: xs:dateTime("2000-01-01T00:00:00Z") and
// xs:dateTime("2000-01-01T01:00:00+01:00") name the same instant and belong in
// one group, but their string forms differ. Keying on the string put them in
// two.
//
// coll may be nil, in which case strings key on themselves.
func GroupingKey(a *xdm.Atomic, coll Collation, implicitTZ int) (string, error) {
	ctx := &Context{ImplicitTimezone: implicitTZ}
	if coll != nil {
		ctx.collation = coll
	}
	return valueKey(ctx, a)
}
