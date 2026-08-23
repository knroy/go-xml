package xpath

import (
	"fmt"
	"math"
	"math/big"

	"github.com/knroy/go-xml/xdm"
)

// Eval implements Expr for all infix operators.
func (e *BinaryOp) Eval(ctx *Context) (xdm.Sequence, error) {
	switch e.Op {
	// Logical operators short-circuit and take effective boolean values.
	case "and", "or":
		return e.evalLogical(ctx)

	// Sequence operators work on nodes and never atomise.
	case "union", "intersect", "except":
		return e.evalNodeSetOp(ctx)

	// Node comparisons compare identity and document position.
	case "is", "<<", ">>":
		return e.evalNodeComparison(ctx)

	// Value comparisons require singleton operands.
	case "eq", "ne", "lt", "le", "gt", "ge":
		return e.evalValueComparison(ctx)

	// General comparisons are existentially quantified over both operands.
	case "=", "!=", "<", "<=", ">", ">=":
		return e.evalGeneralComparison(ctx)

	case "to":
		return e.evalRange(ctx)
	}
	return e.evalArithmetic(ctx)
}

func (e *BinaryOp) evalLogical(ctx *Context) (xdm.Sequence, error) {
	l, err := e.Left.Eval(ctx)
	if err != nil {
		return nil, err
	}
	lb, err := EffectiveBooleanValue(l)
	if err != nil {
		return nil, err
	}
	// Short-circuit: the right operand is not evaluated when the result is
	// already determined, so "$n != 0 and 10 div $n > 1" is safe.
	if e.Op == "and" && !lb {
		return xdm.One(xdm.NewBoolean(false)), nil
	}
	if e.Op == "or" && lb {
		return xdm.One(xdm.NewBoolean(true)), nil
	}
	r, err := e.Right.Eval(ctx)
	if err != nil {
		return nil, err
	}
	rb, err := EffectiveBooleanValue(r)
	if err != nil {
		return nil, err
	}
	return xdm.One(xdm.NewBoolean(rb)), nil
}

func (e *BinaryOp) evalNodeSetOp(ctx *Context) (xdm.Sequence, error) {
	l, err := e.Left.Eval(ctx)
	if err != nil {
		return nil, err
	}
	r, err := e.Right.Eval(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range []xdm.Sequence{l, r} {
		for _, it := range s {
			if _, ok := it.(*xdm.Node); !ok {
				return nil, xdm.ErrType("operand of %q must contain only nodes, got %s",
					e.Op, it.TypeName())
			}
		}
	}
	switch e.Op {
	case "union":
		return xdm.Union(l, r), nil
	case "intersect":
		return xdm.Intersect(l, r), nil
	default:
		return xdm.Except(l, r), nil
	}
}

func (e *BinaryOp) evalNodeComparison(ctx *Context) (xdm.Sequence, error) {
	l, err := e.Left.Eval(ctx)
	if err != nil {
		return nil, err
	}
	r, err := e.Right.Eval(ctx)
	if err != nil {
		return nil, err
	}
	// An empty operand yields the empty sequence, not false.
	if len(l) == 0 || len(r) == 0 {
		return xdm.Empty, nil
	}
	ln, err := singleNode(l, e.Op)
	if err != nil {
		return nil, err
	}
	rn, err := singleNode(r, e.Op)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case "is":
		// Identity, not value equality: two elements with identical content
		// are not "is"-equal.
		return xdm.One(xdm.NewBoolean(ln == rn)), nil
	case "<<":
		return xdm.One(xdm.NewBoolean(ln.Compare(rn) < 0)), nil
	default:
		return xdm.One(xdm.NewBoolean(ln.Compare(rn) > 0)), nil
	}
}

func singleNode(s xdm.Sequence, op string) (*xdm.Node, error) {
	it, err := s.Single()
	if err != nil {
		return nil, fmt.Errorf("operand of %q: %w", op, err)
	}
	n, ok := it.(*xdm.Node)
	if !ok {
		return nil, xdm.ErrType("operand of %q must be a node, got %s", op, it.TypeName())
	}
	return n, nil
}

// evalValueComparison implements eq, ne, lt, le, gt, ge.
//
// These require exactly one atomic value on each side. That is the whole point
// of having them alongside =, !=, and friends: "$a eq $b" tells the reader the
// operands are singletons, and raises an error rather than silently doing an
// existential match when they are not.
func (e *BinaryOp) evalValueComparison(ctx *Context) (xdm.Sequence, error) {
	l, err := e.Left.Eval(ctx)
	if err != nil {
		return nil, err
	}
	r, err := e.Right.Eval(ctx)
	if err != nil {
		return nil, err
	}
	la, ra := xdm.Atomize(l), xdm.Atomize(r)
	if len(la) == 0 || len(ra) == 0 {
		return xdm.Empty, nil
	}
	lit, err := la.Single()
	if err != nil {
		return nil, err
	}
	rit, err := ra.Single()
	if err != nil {
		return nil, err
	}
	res, err := compareValues(ctx, lit.(*xdm.Atomic), rit.(*xdm.Atomic), e.Op, false)
	if err != nil {
		return nil, err
	}
	return xdm.One(xdm.NewBoolean(res)), nil
}

// evalGeneralComparison implements =, !=, <, <=, >, >=.
//
// True if *some* pair of items, one from each operand, compares true. This is
// the XPath 1.0 behaviour, kept for compatibility, and it is why "$a != $b" is
// not the negation of "$a = $b" when either side has more than one item.
func (e *BinaryOp) evalGeneralComparison(ctx *Context) (xdm.Sequence, error) {
	// A comparison against a bare range is answered from its bounds. The
	// range may name more integers than the item limit allows, and building
	// them to find out whether one value is among them is the wrong shape of
	// work regardless.
	valueOpFor := map[string]string{
		"=": "eq", "!=": "ne", "<": "lt", "<=": "le", ">": "gt", ">=": "ge",
	}
	if got, ok, err := rangeContains(ctx, e, valueOpFor[e.Op]); err != nil {
		return nil, err
	} else if ok {
		return xdm.One(xdm.NewBoolean(got)), nil
	}

	l, err := e.Left.Eval(ctx)
	if err != nil {
		return nil, err
	}
	r, err := e.Right.Eval(ctx)
	if err != nil {
		return nil, err
	}
	la, ra := xdm.Atomize(l), xdm.Atomize(r)

	valueOp := map[string]string{
		"=": "eq", "!=": "ne", "<": "lt", "<=": "le", ">": "gt", ">=": "ge",
	}[e.Op]

	for _, li := range la {
		for _, ri := range ra {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// untypedAtomic operands are cast per the general-comparison
			// rules, which differ from value comparison: against a numeric
			// operand they become doubles, not strings.
			ok, err := compareValues(ctx, li.(*xdm.Atomic), ri.(*xdm.Atomic), valueOp, true)
			if err != nil {
				return nil, err
			}
			if ok {
				return xdm.One(xdm.NewBoolean(true)), nil
			}
		}
	}
	return xdm.One(xdm.NewBoolean(false)), nil
}

// compareValues compares two atomic values with a value-comparison operator.
//
// general reports whether this is a general comparison, which changes how
// untypedAtomic operands are converted: in a general comparison an
// untypedAtomic is cast to the *other* operand's type (double if that is
// numeric), while in a value comparison it is treated as a string.
func compareValues(ctx *Context, a, b *xdm.Atomic, op string, general bool) (bool, error) {
	a, b, err := harmonize(a, b, general)
	if err != nil {
		return false, err
	}

	cmp, ordered, err := rawCompare(ctx, a, b)
	if err != nil {
		return false, err
	}

	// NaN is unequal to everything including itself, and unordered.
	if a.IsNaN() || b.IsNaN() {
		return op == "ne", nil
	}

	switch op {
	case "eq":
		return cmp == 0, nil
	case "ne":
		return cmp != 0, nil
	}
	if !ordered {
		return false, xdm.ErrType("values of type %s are not ordered", a.TypeName())
	}
	switch op {
	case "lt":
		return cmp < 0, nil
	case "le":
		return cmp <= 0, nil
	case "gt":
		return cmp > 0, nil
	case "ge":
		return cmp >= 0, nil
	}
	return false, fmt.Errorf("unknown comparison operator %q", op)
}

// harmonize converts a pair of operands to a comparable pair of types.
func harmonize(a, b *xdm.Atomic, general bool) (*xdm.Atomic, *xdm.Atomic, error) {
	au, bu := a.Type == xdm.TypeUntypedAtomic, b.Type == xdm.TypeUntypedAtomic
	switch {
	case au && bu:
		// Two untyped operands compare as strings.
		return xdm.NewString(a.Str()), xdm.NewString(b.Str()), nil
	case au:
		conv, err := castUntypedFor(a, b.Type, general)
		return conv, b, err
	case bu:
		conv, err := castUntypedFor(b, a.Type, general)
		return a, conv, err
	}
	return a, b, nil
}

// castUntypedFor converts an untypedAtomic operand for comparison against
// target.
func castUntypedFor(u *xdm.Atomic, target xdm.TypeCode, general bool) (*xdm.Atomic, error) {
	if general && target.IsNumeric() {
		// General comparison against a number: cast to double. A value that
		// does not look like a number is an error, not a false result.
		return CastAtomic(u, xdm.TypeDouble)
	}
	if target == xdm.TypeString || target == xdm.TypeAnyURI {
		return xdm.NewString(u.Str()), nil
	}
	if !general {
		// A *value* comparison casts untypedAtomic to xs:string and nothing
		// else. "xs:untypedAtomic('1') eq xs:integer(1)" is therefore a type
		// error rather than true: eq compares a string with an integer, which
		// has no answer.
		//
		// This differs from the general comparison below, and deliberately:
		// "=" exists to be forgiving about untyped input from an unvalidated
		// document, "eq" exists to be exact. Treating them alike made every
		// "eq" against untyped data silently succeed with the semantics of
		// "=".
		return xdm.NewString(u.Str()), nil
	}
	// In a *general* comparison the untyped value takes the other operand's
	// type. This is what makes "@indicator = true()" work on an unvalidated
	// document, where the attribute is untypedAtomic and the literal is
	// xs:boolean — a comparison real rule sets rely on.
	return CastAtomic(u, target)
}

// rawCompare compares two same-family atomic values, returning the sign of the
// comparison and whether the type is ordered at all.
func rawCompare(ctx *Context, a, b *xdm.Atomic) (int, bool, error) {
	switch {
	case a.Type.IsNumeric() && b.Type.IsNumeric():
		return compareNumeric(a, b), true, nil

	case a.Type == xdm.TypeBoolean && b.Type == xdm.TypeBoolean:
		// false < true.
		ai, bi := 0, 0
		if a.Bool() {
			ai = 1
		}
		if b.Bool() {
			bi = 1
		}
		return ai - bi, true, nil

	case isStringLike(a.Type) && isStringLike(b.Type):
		// String comparison uses the default collation from the static
		// context, which [xsl:]default-collation sets. Comparing with the
		// Go operators instead hard-wired codepoint order, so a stylesheet
		// that declared a case-blind default still found "Adele" and "ADELE"
		// unequal.
		//
		// xs:anyURI is the exception the specification carves out: URIs are
		// always compared by codepoint, whatever the default collation is.
		if ctx != nil && ctx.collation != nil &&
			a.Type != xdm.TypeAnyURI && b.Type != xdm.TypeAnyURI {
			return sign(ctx.collation.Compare(a.Str(), b.Str())), true, nil
		}
		switch {
		case a.Str() < b.Str():
			return -1, true, nil
		case a.Str() > b.Str():
			return 1, true, nil
		}
		return 0, true, nil

	case isCalendarLike(a.Type) && isCalendarLike(b.Type):
		if a.Type != b.Type {
			return 0, false, xdm.ErrType("cannot compare %s with %s", a.TypeName(), b.TypeName())
		}
		// The Gregorian types support equality but not ordering: without a
		// year, "is --01-15 before --02-01" has no answer that holds for
		// every year.
		if xdm.IsGregorian(a.Type) {
			tz := 0
			if ctx != nil {
				tz = ctx.ImplicitTimezone
			}
			return xdm.CompareDT(a.DateTimeVal(), b.DateTimeVal(), tz), false, nil
		}
		tz := 0
		if ctx != nil {
			tz = ctx.ImplicitTimezone
		}
		av, bv := a.DateTimeVal(), b.DateTimeVal()
		// An xs:time has no date, so the one its representation carries is
		// not part of the value: the spec compares two times by placing both
		// on a fixed reference date. Timezone adjustment can roll a time past
		// midnight — adjusting 10:00:00-07:00 to +10:00 gives 03:00:00 on the
		// following day — and comparing the dates as well made that unequal
		// to the identical literal xs:time("03:00:00+10:00").
		if a.Type == xdm.TypeTime {
			ac, bc := *av, *bv
			ac.Year, ac.Month, ac.Day = 1972, 12, 31
			bc.Year, bc.Month, bc.Day = 1972, 12, 31
			av, bv = &ac, &bc
		}
		return xdm.CompareDT(av, bv, tz), true, nil

	case isDurationLike(a.Type) && isDurationLike(b.Type):
		return compareDurations(a, b)

	case a.Type == xdm.TypeQName && b.Type == xdm.TypeQName:
		// QNames support equality only; there is no ordering on them.
		if a.QName().Equal(*b.QName()) {
			return 0, false, nil
		}
		return 1, false, nil
	}
	return 0, false, xdm.ErrType("cannot compare %s with %s", a.TypeName(), b.TypeName())
}

func isStringLike(t xdm.TypeCode) bool {
	return t == xdm.TypeString || t == xdm.TypeAnyURI || t == xdm.TypeUntypedAtomic ||
		t == xdm.TypeHexBinary || t == xdm.TypeBase64Binary
}

func isDateLike(t xdm.TypeCode) bool {
	return t == xdm.TypeDate || t == xdm.TypeTime || t == xdm.TypeDateTime
}

// isCalendarLike covers the date/time types plus the five Gregorian ones,
// which share the DateTime representation and the same timezone rules.
func isCalendarLike(t xdm.TypeCode) bool {
	return isDateLike(t) || xdm.IsGregorian(t)
}

func isDurationLike(t xdm.TypeCode) bool {
	return t == xdm.TypeDuration || t == xdm.TypeYearMonthDuration ||
		t == xdm.TypeDayTimeDuration
}

// compareNumeric compares two numeric values after promotion. Decimal and
// integer operands compare exactly through big.Rat; anything involving a
// double or float goes through float64, where NaN is handled by the caller.
func compareNumeric(a, b *xdm.Atomic) int {
	common := xdm.NumericPromote(a.Type, b.Type)
	if common == xdm.TypeInteger || common == xdm.TypeDecimal {
		return a.Rat().Cmp(b.Rat())
	}
	af, bf := a.Float64(), b.Float64()
	// When the promoted type is xs:float, both operands are compared *as
	// floats*: xs:decimal("1.01") and xs:float("1.01") are equal, because the
	// decimal is converted to float to be compared at all. Comparing at
	// double precision kept a difference that only exists below float
	// precision, so the two came out unequal.
	if common == xdm.TypeFloat {
		af, bf = float64(float32(af)), float64(float32(bf))
	}
	switch {
	case af < bf:
		return -1
	case af > bf:
		return 1
	}
	return 0
}

// compareDurations orders two durations.
//
// Only the two totally-ordered subtypes can be compared; xs:duration itself is
// partially ordered because months and seconds are not interconvertible, so
// comparing two of them is an error rather than an approximation.
func compareDurations(a, b *xdm.Atomic) (int, bool, error) {
	da, db := a.DurationVal(), b.DurationVal()
	if da == nil || db == nil {
		return 0, false, xdm.ErrType("invalid duration operand")
	}
	switch {
	case a.Type == xdm.TypeYearMonthDuration && b.Type == xdm.TypeYearMonthDuration:
		return sign(da.SignedMonths() - db.SignedMonths()), true, nil
	case a.Type == xdm.TypeDayTimeDuration && b.Type == xdm.TypeDayTimeDuration:
		return da.SignedSeconds().Cmp(db.SignedSeconds()), true, nil
	}
	// Equality between arbitrary durations is defined; ordering is not.
	eq := da.SignedMonths() == db.SignedMonths() &&
		da.SignedSeconds().Cmp(db.SignedSeconds()) == 0
	if eq {
		return 0, false, nil
	}
	return 1, false, nil
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// evalRange implements "a to b", which yields consecutive integers.
func (e *BinaryOp) evalRange(ctx *Context) (xdm.Sequence, error) {
	lo, err := bigInteger(ctx, e.Left)
	if err != nil {
		return nil, err
	}
	hi, err := bigInteger(ctx, e.Right)
	if err != nil {
		return nil, err
	}
	if lo == nil || hi == nil {
		return xdm.Empty, nil
	}
	// A descending range is empty, not reversed.
	if lo.Cmp(hi) > 0 {
		return xdm.Empty, nil
	}
	// What bounds a range is the number of items in it, not how large the
	// numbers are. xs:integer is arbitrary-precision, so
	// "1000000000000000000000 to 1000000000000000000003" is a perfectly
	// ordinary four-item sequence that happens to sit outside int64; refusing
	// it for the magnitude of its bounds was wrong.
	//
	// The count is what gets checked, and it is computed in big arithmetic so
	// the subtraction itself cannot overflow.
	var n big.Int
	n.Sub(hi, lo)
	n.Add(&n, big.NewInt(1))

	const maxRange = MaxItems
	if !n.IsInt64() || n.Int64() > maxRange {
		return nil, fmt.Errorf(
			"range %s to %s exceeds the %d item limit", lo, hi, maxRange)
	}
	count := int(n.Int64())
	if err := ctx.countItems(count); err != nil {
		return nil, err
	}
	out := make(xdm.Sequence, 0, count)
	// Stepping a big.Int keeps the arbitrary precision through the whole
	// range: the values are what the expression named, not a wrapped
	// approximation of them.
	cur := new(big.Int).Set(lo)
	one := big.NewInt(1)
	for i := 0; i < count; i++ {
		out = append(out, xdm.NewIntegerFromRat(new(big.Rat).SetInt(cur)))
		cur = new(big.Int).Add(cur, one)
	}
	return out, nil
}

// bigInteger evaluates e to a single xs:integer without narrowing it to int64.
//
// It shares the operand type rules with singleInteger — only xs:integer and
// xs:untypedAtomic convert — and differs only in keeping the arbitrary
// precision that xs:integer is defined to have.
func bigInteger(ctx *Context, e Expr) (*big.Int, error) {
	v, err := e.Eval(ctx)
	if err != nil {
		return nil, err
	}
	atoms := xdm.Atomize(v)
	if len(atoms) == 0 {
		return nil, nil
	}
	it, err := atoms.Single()
	if err != nil {
		return nil, err
	}
	src, ok := it.(*xdm.Atomic)
	if !ok {
		return nil, xdm.ErrType("the operands of \"to\" must be xs:integer")
	}
	if src.Type != xdm.TypeInteger && src.Type != xdm.TypeUntypedAtomic {
		return nil, xdm.ErrType(
			"the operands of \"to\" must be xs:integer, got %s", src.TypeName())
	}
	a, err := CastAtomic(src, xdm.TypeInteger)
	if err != nil {
		return nil, err
	}
	r := a.Rat()
	if r == nil || !r.IsInt() {
		return nil, xdm.ErrType("the operands of \"to\" must be integers")
	}
	return new(big.Int).Set(r.Num()), nil
}

func singleInteger(ctx *Context, e Expr) (*int64, error) {
	v, err := e.Eval(ctx)
	if err != nil {
		return nil, err
	}
	atoms := xdm.Atomize(v)
	if len(atoms) == 0 {
		return nil, nil
	}
	it, err := atoms.Single()
	if err != nil {
		return nil, err
	}
	// The operands of "to" are declared xs:integer, so a decimal or double is
	// a type error rather than something to truncate: "1.1 to 3" has no
	// meaning, and silently reading it as "1 to 3" invents a range the author
	// did not write. Only untypedAtomic converts, which is the usual rule for
	// values coming from an untyped document.
	src := it.(*xdm.Atomic)
	if src.Type != xdm.TypeInteger && src.Type != xdm.TypeUntypedAtomic {
		return nil, xdm.ErrType(
			"the operands of \"to\" must be xs:integer, got %s", src.TypeName())
	}
	a, err := CastAtomic(src, xdm.TypeInteger)
	if err != nil {
		return nil, err
	}
	// xs:integer is arbitrary-precision here, so a bound can legitimately
	// exceed int64 — "1000000000000000000000 to ..." is a valid expression.
	// Int64() wraps silently, which turned that bound into 3875820019684212736
	// and produced a range of the wrong numbers rather than an error.
	//
	// A bound outside int64 cannot produce a materialisable range anyway: the
	// item budget stops far below it. Reporting that is both correct and more
	// useful than a wrapped value.
	if r := a.Rat(); r != nil && !r.IsInt() {
		return nil, xdm.ErrType("the operands of \"to\" must be integers")
	}
	if !a.FitsInt64() {
		return nil, xdm.Errorf("FOAR0002",
			"range bound %s is too large to enumerate", a.String())
	}
	n := a.Int64()
	return &n, nil
}

// --- Arithmetic -------------------------------------------------------------

// evalArithmetic implements +, -, *, div, idiv and mod.
func (e *BinaryOp) evalArithmetic(ctx *Context) (xdm.Sequence, error) {
	l, err := e.Left.Eval(ctx)
	if err != nil {
		return nil, err
	}
	r, err := e.Right.Eval(ctx)
	if err != nil {
		return nil, err
	}
	la, ra := xdm.Atomize(l), xdm.Atomize(r)
	// Arithmetic on an empty operand yields the empty sequence.
	if len(la) == 0 || len(ra) == 0 {
		return xdm.Empty, nil
	}
	lit, err := la.Single()
	if err != nil {
		return nil, err
	}
	rit, err := ra.Single()
	if err != nil {
		return nil, err
	}
	res, err := arithmetic(lit.(*xdm.Atomic), rit.(*xdm.Atomic), e.Op)
	if err != nil {
		return nil, err
	}
	return xdm.One(res), nil
}

// arithmetic applies a binary arithmetic operator to two atomic values.
func arithmetic(a, b *xdm.Atomic, op string) (*xdm.Atomic, error) {
	// Date and duration arithmetic is a separate rule table from numeric.
	if isDateLike(a.Type) || isDurationLike(a.Type) ||
		isDateLike(b.Type) || isDurationLike(b.Type) {
		return temporalArithmetic(a, b, op)
	}

	an, err := toNumeric(a)
	if err != nil {
		return nil, err
	}
	bn, err := toNumeric(b)
	if err != nil {
		return nil, err
	}

	common := xdm.NumericPromote(an.Type, bn.Type)
	exact := common == xdm.TypeInteger || common == xdm.TypeDecimal

	switch op {
	case "idiv":
		return integerDivide(an, bn)
	case "div":
		return divide(an, bn, common, exact)
	case "mod":
		return modulo(an, bn, common, exact)
	}

	if exact {
		x, y := an.Rat(), bn.Rat()
		z := new(big.Rat)
		switch op {
		case "+":
			z.Add(x, y)
		case "-":
			z.Sub(x, y)
		case "*":
			z.Mul(x, y)
		default:
			return nil, fmt.Errorf("unknown operator %q", op)
		}
		if common == xdm.TypeInteger {
			return xdm.NewIntegerFromRat(z), nil
		}
		return xdm.NewDecimal(z), nil
	}

	x, y := an.Float64(), bn.Float64()
	var z float64
	switch op {
	case "+":
		z = x + y
	case "-":
		z = x - y
	case "*":
		z = x * y
	default:
		return nil, fmt.Errorf("unknown operator %q", op)
	}
	return makeFloat(z, common), nil
}

func divide(a, b *xdm.Atomic, common xdm.TypeCode, exact bool) (*xdm.Atomic, error) {
	if exact {
		if b.Rat().Sign() == 0 {
			// Exact types have no infinity to produce, so this is an error
			// rather than INF — unlike double division.
			return nil, fmt.Errorf("FOAR0001: division by zero")
		}
		z := new(big.Rat).Quo(a.Rat(), b.Rat())
		// Dividing two integers yields a decimal, never an integer. The
		// result is rounded to the precision xs:decimal supports rather than
		// kept as an exact rational: 1 div 999999999999999999 is stored
		// exactly as that fraction, which *renders* as 0.000000000000000001
		// but does not compare equal to it — the value and its own lexical
		// form disagreed.
		return xdm.NewDecimal(roundToDecimalPrecision(z)), nil
	}
	return makeFloat(a.Float64()/b.Float64(), common), nil
}

func integerDivide(a, b *xdm.Atomic) (*xdm.Atomic, error) {
	// The operand checks come before the zero test, because an infinity has no
	// rational form: ratOf(INF) is zero, so an infinite divisor looked like a
	// division by zero, and once past that test it divided by a zero
	// denominator and panicked.
	//
	// Only an infinite *dividend* is an error. A finite value divided by an
	// infinity truncates to zero, which is an ordinary result.
	// A zero divisor is reported before an infinite dividend: INF idiv 0 is
	// division by zero, which is the more specific complaint.
	if b.Type.IsNumeric() && isZero(b) && !b.IsNaN() {
		return nil, fmt.Errorf("FOAR0001: integer division by zero")
	}
	if a.IsNaN() || b.IsNaN() || math.IsInf(a.Float64(), 0) {
		return nil, fmt.Errorf("FOAR0002: idiv operand is NaN or infinite")
	}
	if math.IsInf(b.Float64(), 0) {
		return xdm.NewInteger(0), nil
	}
	q := new(big.Rat).Quo(ratOf(a), ratOf(b))
	// idiv truncates toward zero.
	t := new(big.Int).Quo(q.Num(), q.Denom())
	return xdm.NewIntegerFromRat(new(big.Rat).SetInt(t)), nil
}

func modulo(a, b *xdm.Atomic, common xdm.TypeCode, exact bool) (*xdm.Atomic, error) {
	if exact {
		if b.Rat().Sign() == 0 {
			return nil, fmt.Errorf("FOAR0001: modulo by zero")
		}
		// XPath mod takes the sign of the dividend, matching math.Mod rather
		// than Euclidean remainder.
		q := new(big.Rat).Quo(a.Rat(), b.Rat())
		t := new(big.Int).Quo(q.Num(), q.Denom())
		z := new(big.Rat).Sub(a.Rat(), new(big.Rat).Mul(new(big.Rat).SetInt(t), b.Rat()))
		if common == xdm.TypeInteger {
			return xdm.NewIntegerFromRat(z), nil
		}
		return xdm.NewDecimal(z), nil
	}
	return makeFloat(math.Mod(a.Float64(), b.Float64()), common), nil
}

func makeFloat(v float64, t xdm.TypeCode) *xdm.Atomic {
	if t == xdm.TypeFloat {
		return xdm.NewFloat(v)
	}
	return xdm.NewDouble(v)
}

func isZero(a *xdm.Atomic) bool {
	if a.Rat() != nil {
		return a.Rat().Sign() == 0
	}
	return a.Float64() == 0
}

func ratOf(a *xdm.Atomic) *big.Rat {
	if a.Rat() != nil {
		return a.Rat()
	}
	r := new(big.Rat)
	r.SetFloat64(a.Float64())
	return r
}

// toNumeric converts an operand for arithmetic. untypedAtomic becomes double,
// which is the rule for arithmetic (unlike comparison, where it depends on the
// other operand).
func toNumeric(a *xdm.Atomic) (*xdm.Atomic, error) {
	switch {
	case a.Type.IsNumeric():
		return a, nil
	case a.Type == xdm.TypeUntypedAtomic:
		return CastAtomic(a, xdm.TypeDouble)
	}
	return nil, xdm.ErrType("%s is not a numeric type", a.TypeName())
}

func negate(a *xdm.Atomic) *xdm.Atomic {
	switch a.Type {
	case xdm.TypeInteger:
		return xdm.NewIntegerFromRat(new(big.Rat).Neg(a.Rat()))
	case xdm.TypeDecimal:
		return xdm.NewDecimal(new(big.Rat).Neg(a.Rat()))
	case xdm.TypeFloat:
		return xdm.NewFloat(-a.Float64())
	default:
		return xdm.NewDouble(-a.Float64())
	}
}

// roundToDecimalPrecision rounds an exact rational to the fractional precision
// xs:decimal is required to support.
//
// Division is the only operation that produces a non-terminating rational, and
// leaving it exact makes a value that does not equal its own lexical form:
// 1 div 999999999999999999 renders at 18 digits but compares as the fraction.
// The spec allows an implementation to round to its supported precision, so
// the rounding is done once, here, rather than at each place a decimal is
// displayed.
func roundToDecimalPrecision(r *big.Rat) *big.Rat {
	if r.IsInt() {
		return r
	}
	const digits = 18
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(digits), nil)
	// Scale, round half away from zero, and scale back.
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(scale))
	num, den := scaled.Num(), scaled.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	twice := new(big.Int).Abs(rem)
	twice.Lsh(twice, 1)
	if twice.Cmp(den) >= 0 {
		if r.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return new(big.Rat).SetFrac(q, scale)
}
