package xpath

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// CastAtomic converts an atomic value to a target type, per the XPath 2.0
// casting table.
//
// Casting is stricter than the string conversions of XPath 1.0: "abc" cast to
// xs:double is an error (FORG0001), not NaN. Silent NaN is how a validator
// ends up reporting a document as valid because a numeric comparison quietly
// became false.
func CastAtomic(a *xdm.Atomic, target xdm.TypeCode) (*xdm.Atomic, error) {
	if a.Type == target {
		return a, nil
	}

	if err := castPermitted(a.Type, target); err != nil {
		return nil, err
	}

	switch target {
	case xdm.TypeString:
		return xdm.NewString(a.String()), nil

	case xdm.TypeUntypedAtomic:
		return xdm.NewUntypedAtomic(a.String()), nil

	case xdm.TypeAnyURI:
		if a.Type == xdm.TypeString || a.Type == xdm.TypeUntypedAtomic {
			// xs:anyURI has whiteSpace="collapse", so leading and trailing
			// whitespace is not part of the value: xs:anyURI(" ") is the empty
			// URI, and concat("b", xs:anyURI(" "), "b") is "bb".
			v := strings.TrimSpace(a.Str())
			if err := validAnyURI(v); err != nil {
				return nil, err
			}
			return xdm.NewAnyURI(v), nil
		}
		return nil, xdm.ErrCast("cannot cast %s to xs:anyURI", a.TypeName())

	case xdm.TypeBoolean:
		return castToBoolean(a)

	case xdm.TypeInteger, xdm.TypeDecimal, xdm.TypeDouble, xdm.TypeFloat:
		return castToNumeric(a, target)

	case xdm.TypeDate, xdm.TypeTime, xdm.TypeDateTime:
		return castToDateTime(a, target)

	case xdm.TypeGYear, xdm.TypeGYearMonth, xdm.TypeGMonth,
		xdm.TypeGMonthDay, xdm.TypeGDay:
		return castToGregorian(a, target)

	case xdm.TypeDuration, xdm.TypeYearMonthDuration, xdm.TypeDayTimeDuration:
		return castToDuration(a, target)

	case xdm.TypeHexBinary, xdm.TypeBase64Binary:
		return castToBinary(a, target)

	case xdm.TypeQName:
		// Only the lexical shape can be checked here: a QName's namespace
		// comes from the static context, which this function does not have.
		// A prefixed name reaching this path therefore casts to a QName with
		// no URI, which is why xs:QName() is folded in the parser instead —
		// that path has the namespace bindings. What this case exists for is
		// "castable as xs:QName", which asks only whether the lexical form is
		// a legal QName.
		if !isStringLike(a.Type) {
			return nil, xdm.ErrCast("cannot cast %s to xs:QName", a.TypeName())
		}
		q, err := parseLexicalQName(a.Str())
		if err != nil {
			return nil, err
		}
		return xdm.NewQNameValue(q), nil
	}
	return nil, xdm.ErrType("cannot cast %s to %s", a.TypeName(), target)
}

func castToBoolean(a *xdm.Atomic) (*xdm.Atomic, error) {
	switch {
	case a.Type.IsNumeric():
		// Zero and NaN are false; everything else is true.
		if a.IsNaN() {
			return xdm.NewBoolean(false), nil
		}
		return xdm.NewBoolean(!isZero(a)), nil

	case a.Type == xdm.TypeString || a.Type == xdm.TypeUntypedAtomic:
		// Only the four canonical lexical forms are accepted; "yes" is an
		// error rather than true.
		switch strings.TrimSpace(a.Str()) {
		case "true", "1":
			return xdm.NewBoolean(true), nil
		case "false", "0":
			return xdm.NewBoolean(false), nil
		}
		return nil, xdm.ErrCast("invalid xs:boolean value %q", a.Str())
	}
	return nil, xdm.ErrCast("cannot cast %s to xs:boolean", a.TypeName())
}

func castToNumeric(a *xdm.Atomic, target xdm.TypeCode) (*xdm.Atomic, error) {
	switch {
	case a.Type == xdm.TypeBoolean:
		v := int64(0)
		if a.Bool() {
			v = 1
		}
		return numericOf(new(big.Rat).SetInt64(v), float64(v), target), nil

	case a.Type.IsNumeric():
		if target == xdm.TypeInteger {
			if a.IsNaN() || math.IsInf(a.Float64(), 0) {
				// FOCA0002 rather than FORG0001: the lexical form is a
				// perfectly good double, and it is the *numeric* operation
				// that has no answer, which is what FOCA covers.
				return nil, xdm.Errorf("FOCA0002",
					"cannot cast %s to xs:integer", a.String())
			}
			r := ratOf(a)
			t := new(big.Int).Quo(r.Num(), r.Denom()) // truncates toward zero
			return xdm.NewIntegerFromRat(new(big.Rat).SetInt(t)), nil
		}
		if target == xdm.TypeDecimal {
			if a.IsNaN() || math.IsInf(a.Float64(), 0) {
				return nil, xdm.Errorf("FOCA0002",
					"cannot cast %s to xs:decimal", a.String())
			}
			return xdm.NewDecimal(ratOf(a)), nil
		}
		return makeFloat(a.Float64(), target), nil

	case isStringLike(a.Type):
		return parseNumericLexical(strings.TrimSpace(a.Str()), target)
	}
	return nil, xdm.ErrCast("cannot cast %s to %s", a.TypeName(), target)
}

// parseNumericLexical parses a lexical form into a numeric type, rejecting
// anything the schema grammar does not allow.
func parseNumericLexical(s string, target xdm.TypeCode) (*xdm.Atomic, error) {
	if s == "" {
		return nil, xdm.ErrCast("empty string is not a valid %s", target)
	}

	if target == xdm.TypeDouble || target == xdm.TypeFloat {
		// INF, -INF and NaN are valid double/float lexical forms, and only
		// in these exact spellings.
		switch s {
		case "INF", "+INF":
			// "+INF" is XSD 1.1; 1.0 allowed only "INF". The suite tests
			// against 1.1, and accepting it costs nothing.
			return makeFloat(math.Inf(1), target), nil
		case "-INF":
			return makeFloat(math.Inf(-1), target), nil
		case "NaN":
			return makeFloat(math.NaN(), target), nil
		}
		// Go's scanner also accepts "Inf", "Infinity" and "+Inf", none of
		// which are XML Schema lexical forms — only the three spellings above
		// are. Checking the shape first is what keeps xs:double("Inf") an
		// error rather than positive infinity.
		if !isSchemaFloatLexical(s) {
			return nil, xdm.ErrCast("invalid %s value %q", target, s)
		}
		var f float64
		if _, err := fmt.Sscanf(s, "%g", &f); err != nil || !isFullNumber(s) {
			return nil, xdm.ErrCast("invalid %s value %q", target, s)
		}
		return makeFloat(f, target), nil
	}

	// Integer and decimal have no exponent in their lexical space.
	if strings.ContainsAny(s, "eE") {
		return nil, xdm.ErrCast("invalid %s value %q: exponent not allowed", target, s)
	}
	// big.Rat.SetString reads Go's numeric syntax, which is much wider than
	// the schema's: "0x0" is a hex literal to it, and "1/2" a fraction. Both
	// parsed happily and gave a value the lexical form does not denote, so the
	// shape is checked first.
	if !isSchemaDecimalLexical(s) {
		return nil, xdm.ErrCast("invalid %s value %q", target, s)
	}
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return nil, xdm.ErrCast("invalid %s value %q", target, s)
	}
	if target == xdm.TypeInteger {
		// The *lexical* form must be an integer, not merely a value that
		// happens to be whole: "3.0" is a valid xs:decimal and not a valid
		// xs:integer, and big.Rat cannot tell the two apart after parsing.
		if strings.ContainsRune(s, '.') || !r.IsInt() {
			return nil, xdm.ErrCast("invalid xs:integer value %q", s)
		}
		return xdm.NewIntegerFromRat(r), nil
	}
	return xdm.NewDecimal(r), nil
}

// isSchemaFloatLexical reports whether s has the shape XML Schema defines for
// xs:double and xs:float: an optionally-signed decimal with an optional
// exponent. The three special values are handled by the caller, so anything
// alphabetic reaching here is invalid.
func isSchemaFloatLexical(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	// A mantissa with no digits at all ("." or "+") is not a number.
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		expDigits := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			expDigits++
		}
		if expDigits == 0 {
			return false
		}
	}
	return i == len(s)
}

// isFullNumber rejects strings like "12abc" that Sscanf would accept a prefix
// of. Sscanf stops at the first bad character and reports success, so the
// length check is what makes the cast strict.
func isFullNumber(s string) bool {
	var f float64
	var tail string
	n, _ := fmt.Sscanf(s, "%g%s", &f, &tail)
	return n == 1
}

func numericOf(r *big.Rat, f float64, target xdm.TypeCode) *xdm.Atomic {
	switch target {
	case xdm.TypeInteger:
		return xdm.NewIntegerFromRat(r)
	case xdm.TypeDecimal:
		return xdm.NewDecimal(r)
	case xdm.TypeFloat:
		return xdm.NewFloat(f)
	default:
		return xdm.NewDouble(f)
	}
}

func castToDateTime(a *xdm.Atomic, target xdm.TypeCode) (*xdm.Atomic, error) {
	// Casting between date/time types truncates rather than re-parsing:
	// xs:dateTime to xs:date drops the time, keeping the timezone.
	if isDateLike(a.Type) && a.DateTimeVal() != nil {
		src := a.DateTimeVal()
		out := *src
		switch target {
		case xdm.TypeDate:
			if a.Type == xdm.TypeTime {
				return nil, xdm.ErrCast("cannot cast xs:time to xs:date")
			}
			out.Hour, out.Minute = 0, 0
			out.Second = new(big.Rat)
		case xdm.TypeTime:
			if a.Type == xdm.TypeDate {
				return nil, xdm.ErrCast("cannot cast xs:date to xs:time")
			}
		case xdm.TypeDateTime:
			if a.Type == xdm.TypeTime {
				return nil, xdm.ErrCast("cannot cast xs:time to xs:dateTime")
			}
			// xs:date to xs:dateTime yields midnight on that date.
		}
		return xdm.NewDateTime(&out, target), nil
	}
	if !isStringLike(a.Type) {
		return nil, xdm.ErrCast("cannot cast %s to %s", a.TypeName(), target)
	}
	dt, err := xdm.ParseDateTime(a.Str(), target)
	if err != nil {
		return nil, err
	}
	return xdm.NewDateTime(dt, target), nil
}

func castToDuration(a *xdm.Atomic, target xdm.TypeCode) (*xdm.Atomic, error) {
	if isDurationLike(a.Type) && a.DurationVal() != nil {
		// Casting between duration types drops the components the target
		// cannot hold, rather than erroring.
		src := a.DurationVal()
		out := xdm.Duration{Negative: src.Negative, Seconds: new(big.Rat)}
		switch target {
		case xdm.TypeYearMonthDuration:
			out.Months = src.Months
		case xdm.TypeDayTimeDuration:
			out.Seconds.Set(src.Seconds)
		default:
			out.Months = src.Months
			out.Seconds.Set(src.Seconds)
		}
		return xdm.NewDuration(&out, target), nil
	}
	if !isStringLike(a.Type) {
		return nil, xdm.ErrCast("cannot cast %s to %s", a.TypeName(), target)
	}
	d, err := xdm.ParseDuration(a.Str(), target)
	if err != nil {
		return nil, err
	}
	return xdm.NewDuration(d, target), nil
}

// --- Temporal arithmetic ----------------------------------------------------

// temporalArithmetic implements the date/duration operator table: date minus
// date yields a duration, date plus duration yields a date, duration times a
// number yields a duration, and most other combinations are errors.
func temporalArithmetic(a, b *xdm.Atomic, op string) (*xdm.Atomic, error) {
	switch {
	// duration + duration, duration - duration
	case isDurationLike(a.Type) && isDurationLike(b.Type):
		if op == "div" {
			return divideDurations(a, b)
		}
		if op != "+" && op != "-" {
			return nil, xdm.ErrType("operator %q is not defined on two durations", op)
		}
		return addDurations(a, b, op == "-")

	// duration * number, duration div number
	case isDurationLike(a.Type) && b.Type.IsNumeric():
		return scaleDuration(a, b, op)

	// number * duration
	case a.Type.IsNumeric() && isDurationLike(b.Type) && op == "*":
		return scaleDuration(b, a, "*")

	// duration div duration yields a decimal ratio
	case isDurationLike(a.Type) && isDurationLike(b.Type) && op == "div":
		return divideDurations(a, b)

	// date - date yields a dayTimeDuration
	case isDateLike(a.Type) && isDateLike(b.Type):
		if op != "-" {
			return nil, xdm.ErrType("operator %q is not defined on two dates", op)
		}
		if a.Type != b.Type {
			return nil, xdm.ErrType("cannot subtract %s from %s", b.TypeName(), a.TypeName())
		}
		diff := new(big.Rat).Sub(a.DateTimeVal().ToSeconds(0), b.DateTimeVal().ToSeconds(0))
		d := &xdm.Duration{Seconds: new(big.Rat).Abs(diff), Negative: diff.Sign() < 0}
		return xdm.NewDuration(d, xdm.TypeDayTimeDuration), nil

	// date +/- duration
	case isDateLike(a.Type) && isDurationLike(b.Type):
		if op != "+" && op != "-" {
			return nil, xdm.ErrType("operator %q is not defined on a date and a duration", op)
		}
		return addDurationToDate(a, b, op == "-")

	// duration + date
	case isDurationLike(a.Type) && isDateLike(b.Type) && op == "+":
		return addDurationToDate(b, a, false)
	}
	return nil, xdm.ErrType("operator %q is not defined on %s and %s",
		op, a.TypeName(), b.TypeName())
}

func addDurations(a, b *xdm.Atomic, subtract bool) (*xdm.Atomic, error) {
	if a.Type != b.Type {
		return nil, xdm.ErrType("cannot combine %s with %s", a.TypeName(), b.TypeName())
	}
	da, db := a.DurationVal(), b.DurationVal()
	// The month count is added in big.Int and range-checked. Adding two
	// months counts near the int64 limit wrapped silently, so
	// P768614336404564650Y plus P1Y came back *negative* — an overflow
	// reported as an ordinary result, which is FODT0002.
	ma := big.NewInt(int64(da.SignedMonths()))
	mb := big.NewInt(int64(db.SignedMonths()))
	total := new(big.Int)
	secs := da.SignedSeconds()
	if subtract {
		total.Sub(ma, mb)
		secs.Sub(secs, db.SignedSeconds())
	} else {
		total.Add(ma, mb)
		secs.Add(secs, db.SignedSeconds())
	}
	if !total.IsInt64() || total.Int64() > math.MaxInt || total.Int64() < math.MinInt {
		return nil, fmt.Errorf("FODT0002: duration overflow")
	}
	return durationFromSigned(int(total.Int64()), secs, a.Type), nil
}

func scaleDuration(d, n *xdm.Atomic, op string) (*xdm.Atomic, error) {
	if op != "*" && op != "div" {
		return nil, xdm.ErrType("operator %q is not defined on a duration and a number", op)
	}
	// Only the two subtypes are multiplicable. xs:duration carries both a
	// month count and a second count, and the two have no common unit — the
	// number of seconds in a month is not fixed — so scaling it is undefined
	// rather than applied componentwise.
	if d.Type != xdm.TypeYearMonthDuration && d.Type != xdm.TypeDayTimeDuration {
		return nil, xdm.ErrType(
			"operator %q is not defined on %s and a number", op, d.TypeName())
	}
	if n.IsNaN() {
		return nil, fmt.Errorf("FOCA0005: cannot scale a duration by NaN")
	}
	// An infinity has no rational form, so ratOf() gives zero for one. Both
	// operators have to recognise it before that conversion: without this a
	// duration divided by INF looked like a division by zero, and a duration
	// multiplied by INF was scaled by zero and returned P0M.
	if math.IsInf(n.Float64(), 0) {
		if op == "div" {
			// Dividing by an infinity shrinks the duration to nothing.
			return durationFromSigned(0, new(big.Rat), d.Type), nil
		}
		// Multiplying by one overflows every duration field there is.
		return nil, fmt.Errorf(
			"FODT0002: overflow scaling a duration by %s", n.String())
	}
	factor := ratOf(n)
	if op == "div" {
		if factor.Sign() == 0 {
			return nil, fmt.Errorf("FODT0002: division of a duration by zero")
		}
		factor = new(big.Rat).Inv(factor)
	}

	dv := d.DurationVal()
	// Months must stay integral, so the scaled month count is rounded to the
	// nearest month rather than silently truncated.
	// roundRat returns an int64 from an unbounded big.Int, and narrowing that
	// to int wrapped: a positive duration times a positive number came back
	// negative. addDurations range-checks the same quantity; this path did
	// not, so the check is made on the exact value before it is narrowed.
	scaled := new(big.Rat).Mul(new(big.Rat).SetInt64(int64(dv.SignedMonths())), factor)
	if !ratFitsInt(scaled) {
		return nil, fmt.Errorf("FODT0002: duration overflow")
	}
	months := roundRat(scaled)
	if months > math.MaxInt || months < math.MinInt {
		return nil, fmt.Errorf("FODT0002: duration overflow")
	}
	secs := new(big.Rat).Mul(dv.SignedSeconds(), factor)
	return durationFromSigned(int(months), secs, d.Type), nil
}

func divideDurations(a, b *xdm.Atomic) (*xdm.Atomic, error) {
	if a.Type != b.Type {
		return nil, xdm.ErrType("cannot divide %s by %s", a.TypeName(), b.TypeName())
	}
	da, db := a.DurationVal(), b.DurationVal()
	if a.Type == xdm.TypeYearMonthDuration {
		if db.SignedMonths() == 0 {
			// The result of duration div duration is a number, not a
			// duration, so this is ordinary numeric division by zero.
			return nil, fmt.Errorf("FOAR0001: division by a zero duration")
		}
		return xdm.NewDecimal(big.NewRat(
			int64(da.SignedMonths()), int64(db.SignedMonths()))), nil
	}
	if db.SignedSeconds().Sign() == 0 {
		return nil, fmt.Errorf("FOAR0001: division by a zero duration")
	}
	return xdm.NewDecimal(new(big.Rat).Quo(da.SignedSeconds(), db.SignedSeconds())), nil
}

// addDurationToDate adds (or subtracts) a duration from a date/time value.
//
// The month component is applied first and clamped to the end of the target
// month, then the second component is added. That order is required by the
// spec: 2024-01-31 plus one month is 2024-02-29, not 2024-03-02.
func addDurationToDate(d, dur *xdm.Atomic, subtract bool) (*xdm.Atomic, error) {
	src := d.DateTimeVal()
	dv := dur.DurationVal()
	months, secs := dv.SignedMonths(), dv.SignedSeconds()
	if subtract {
		months = -months
		secs = new(big.Rat).Neg(secs)
	}

	// A time has no date, so a month component has nothing to apply to: only
	// xs:dayTimeDuration combines with one. Adding a year to 08:01:23 was
	// returning the time unchanged rather than reporting the type error.
	if d.Type == xdm.TypeTime && dur.Type != xdm.TypeDayTimeDuration {
		return nil, xdm.ErrType("cannot combine %s with %s",
			d.TypeName(), dur.TypeName())
	}

	out := *src
	if months != 0 {
		// The month total is computed in big.Int and range-checked: doing it
		// in int wrapped, so adding a large positive duration to a positive
		// date produced a BCE one.
		bigTotal := new(big.Int).Add(
			new(big.Int).Mul(big.NewInt(int64(out.Year)), big.NewInt(12)),
			big.NewInt(int64(out.Month-1)))
		bigTotal.Add(bigTotal, big.NewInt(int64(months)))
		if !bigTotal.IsInt64() || bigTotal.Int64() > math.MaxInt ||
			bigTotal.Int64() < math.MinInt {
			return nil, fmt.Errorf("FODT0001: date overflow")
		}
		total := int(bigTotal.Int64())
		out.Year = total / 12
		out.Month = total%12 + 1
		if out.Month < 1 {
			out.Month += 12
			out.Year--
		}
		// Clamp the day to the last valid day of the resulting month.
		if maxDay := daysInMonthOf(out.Year, out.Month); out.Day > maxDay {
			out.Day = maxDay
		}
	}

	if secs.Sign() != 0 {
		base := out.ToSeconds(0)
		base.Add(base, secs)
		res, err := dateTimeFromSeconds(base, out.HasTZ, out.TZOffset)
		if err != nil {
			return nil, err
		}
		// xs:date arithmetic keeps a date result; the time part is discarded.
		if d.Type == xdm.TypeDate {
			res.Hour, res.Minute = 0, 0
			res.Second = new(big.Rat)
		}
		// A time is a point within a day, so the result wraps rather than
		// carrying into a date that does not exist: 08:12:32 minus 23 days
		// and change is a time on the clock, not a negative one.
		if d.Type == xdm.TypeTime {
			res.Year, res.Month, res.Day = src.Year, src.Month, src.Day
		}
		out = *res
	}
	return xdm.NewDateTime(&out, d.Type), nil
}

func durationFromSigned(months int, secs *big.Rat, t xdm.TypeCode) *xdm.Atomic {
	neg := months < 0 || (months == 0 && secs.Sign() < 0)
	d := &xdm.Duration{
		Negative: neg,
		Months:   abs(months),
		Seconds:  new(big.Rat).Abs(secs),
	}
	return xdm.NewDuration(d, t)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func roundRat(r *big.Rat) int64 {
	// Half-up — toward positive infinity — which is what fn:round is defined
	// to do and what the duration operators inherit. The rule is only visible
	// on values that land exactly on a half, and there it is asymmetric:
	// P5M div -2 is -2.5 and gives -P2M, while P5M div 2 is 2.5 and gives
	// P3M. Rounding half away from zero agrees on the positive side and
	// differs on the negative one.
	num, den := r.Num(), r.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if rem.Sign() == 0 {
		return q.Int64()
	}

	// Compare |rem| against half the denominator by doubling rather than
	// halving, so the test stays in integers.
	twice := new(big.Int).Abs(rem)
	twice.Lsh(twice, 1)
	cmp := twice.Cmp(den)

	if r.Sign() < 0 {
		// QuoRem truncates toward zero, so a negative value is already at the
		// larger neighbour; it moves down only when strictly past the half.
		if cmp > 0 {
			q.Sub(q, big.NewInt(1))
		}
		return q.Int64()
	}
	if cmp >= 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.Int64()
}

// dateTimeFromSeconds converts a UTC-normalised second count back to a
// calendar value, re-applying the timezone offset.
func dateTimeFromSeconds(total *big.Rat, hasTZ bool, tz int) (*xdm.DateTime, error) {
	if hasTZ {
		total = new(big.Rat).Add(total, new(big.Rat).SetInt64(int64(tz)*60))
	}
	secsInt := new(big.Int).Quo(total.Num(), total.Denom())
	frac := new(big.Rat).Sub(total, new(big.Rat).SetInt(secsInt))
	if frac.Sign() < 0 {
		secsInt.Sub(secsInt, big.NewInt(1))
		frac.Add(frac, big.NewRat(1, 1))
	}

	// The second count is narrowed to int64 here, so a value that does not fit
	// has to be refused rather than wrapped: adding 1e17 days to a date
	// silently landed 2.7e14 years away.
	if !secsInt.IsInt64() {
		return nil, xdm.Errorf("FODT0001", "date overflow")
	}
	s := secsInt.Int64()
	days := floorDiv(s, 86400)
	rem := s - days*86400

	y, m, d := civilFromDays(days)
	dt := &xdm.DateTime{
		Year: y, Month: m, Day: d,
		Hour:   int(rem / 3600),
		Minute: int((rem % 3600) / 60),
		Second: new(big.Rat).Add(new(big.Rat).SetInt64(rem%60), frac),
		HasTZ:  hasTZ,
	}
	if hasTZ {
		dt.TZOffset = tz
	}
	return dt, nil
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// civilFromDays is the inverse of the days-from-civil algorithm used in the
// xdm package.
func civilFromDays(z int64) (int, int, int) {
	z += 719468
	era := z / 146097
	if z < 0 {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	m := mp + 3
	if mp >= 10 {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return int(y), int(m), int(d)
}

func daysInMonthOf(y, m int) int {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		yy := y
		if yy < 0 {
			yy++
		}
		if yy%4 == 0 && (yy%100 != 0 || yy%400 == 0) {
			return 29
		}
		return 28
	}
	return 30
}

// castToGregorian converts to one of the five Gregorian types.
//
// Casting from a date or dateTime extracts the relevant components, which is
// how a stylesheet reduces an invoice date to its tax period; casting from a
// string parses the type's own lexical form.
func castToGregorian(a *xdm.Atomic, target xdm.TypeCode) (*xdm.Atomic, error) {
	if src := a.DateTimeVal(); src != nil &&
		(isDateLike(a.Type) || xdm.IsGregorian(a.Type)) {
		// Only a widening extraction is defined: xs:date to xs:gYear drops
		// components, but xs:gYear to xs:date would have to invent them.
		if xdm.IsGregorian(a.Type) && a.Type != target {
			return nil, xdm.ErrCast("cannot cast %s to %s", a.TypeName(), target)
		}
		if a.Type == xdm.TypeTime {
			return nil, xdm.ErrCast("cannot cast xs:time to %s", target)
		}
		// The components the target does not name must be reset to the same
		// defaults ParseGregorian uses. Copying the whole value left the
		// month and day of the source dateTime in place, so a gYear extracted
		// from a dateTime compared unequal to the identical gYear parsed from
		// a string — they serialised the same and differed underneath.
		out := *src
		normalizeGregorianFields(&out, target)
		return xdm.NewGregorian(&out, target), nil
	}
	if !isStringLike(a.Type) {
		return nil, xdm.ErrCast("cannot cast %s to %s", a.TypeName(), target)
	}
	dt, err := xdm.ParseGregorian(a.Str(), target)
	if err != nil {
		return nil, err
	}
	return xdm.NewGregorian(dt, target), nil
}

// castToBinary casts to xs:hexBinary or xs:base64Binary.
//
// The two are different encodings of the same octets, so casting between them
// decodes and re-encodes rather than reinterpreting the lexical form: the
// hexBinary "0FB7" is the base64Binary "D7c=", and treating "D7c=" as hex
// digits is simply an error. Casting from a string parses that string in the
// target encoding, which is the ordinary lexical case.
func castToBinary(a *xdm.Atomic, target xdm.TypeCode) (*xdm.Atomic, error) {
	var octets []byte
	var err error

	switch a.Type {
	case xdm.TypeHexBinary:
		octets, err = hex.DecodeString(a.Str())
	case xdm.TypeBase64Binary:
		octets, err = base64.StdEncoding.DecodeString(a.Str())
	default:
		if !isStringLike(a.Type) {
			return nil, xdm.ErrCast("cannot cast %s to %s", a.TypeName(), target)
		}
		// A string is read in the encoding it is being cast *to*.
		if target == xdm.TypeHexBinary {
			octets, err = hex.DecodeString(strings.TrimSpace(a.Str()))
		} else {
			// XML Schema's base64Binary permits whitespace *between* the
			// characters, not merely around them — the canonical form groups
			// them in fours — so it is removed throughout rather than
			// trimmed. "aaa a" and " AQID " are both valid.
			lex := strings.Join(strings.Fields(a.Str()), "")
			if !validBase64Lexical(lex) {
				return nil, xdm.ErrCast("invalid %s %q", target, a.Str())
			}
			octets, err = base64.StdEncoding.DecodeString(lex)
		}
	}
	if err != nil {
		return nil, xdm.ErrCast("invalid %s %q", target, a.Str())
	}

	if target == xdm.TypeHexBinary {
		// The canonical form of xs:hexBinary is upper case.
		return xdm.NewBinary(strings.ToUpper(hex.EncodeToString(octets)), target), nil
	}
	return xdm.NewBinary(base64.StdEncoding.EncodeToString(octets), target), nil
}

// castPermitted enforces the source side of the XPath casting table.
//
// The individual castToX functions look mostly at the *lexical* form, so
// without this gate a value would be cast by re-parsing its string: the
// base64Binary "10010101" would become the double 10010101, and
// "10010101 castable as xs:float" would answer true. The spec's table forbids
// those conversions outright, so the source type is checked once here rather
// than repeated in each conversion.
//
// Only the genuinely-forbidden combinations are listed. Anything not named
// stays permitted, so this cannot silently narrow a conversion that already
// worked.
func castPermitted(from, to xdm.TypeCode) error {
	if from == to {
		return nil
	}
	// Every type casts to and from xs:string and xs:untypedAtomic, which is
	// what makes the lexical-form conversions legal in the first place.
	//
	// xs:QName is the one exception. A QName is a (namespace, local) pair, and
	// the namespace comes from the static context — an untyped string has no
	// context to draw it from, so the spec forbids the conversion outright
	// rather than letting it produce a QName in no namespace. Casting from a
	// *literal* string is still allowed, because the parser folds that case
	// where the prefix bindings are in scope.
	if from == xdm.TypeUntypedAtomic && to == xdm.TypeQName {
		return xdm.ErrType("cannot cast %s to %s", from, to)
	}
	if to == xdm.TypeString || to == xdm.TypeUntypedAtomic ||
		from == xdm.TypeString || from == xdm.TypeUntypedAtomic {
		return nil
	}

	// A cast the table forbids is a *type* error, not a cast error. The two
	// codes mean different things: FORG0001 says the value was wrong for the
	// type ("abc" as xs:integer), XPTY0004 says the conversion is not defined
	// between those types at all (xs:date as xs:integer), and no value of the
	// source type would make it succeed.
	bad := func() error {
		return xdm.ErrType("cannot cast %s to %s", from, to)
	}

	switch {
	// xs:anyURI is reached only from a string. It is not a general lexical
	// wrapper: casting a double to it would give the URI "1e5", which is
	// syntactically a URI and semantically nothing.
	case to == xdm.TypeAnyURI:
		return bad()

	// The same asymmetry in the other direction. A URI is an opaque
	// identifier, not a lexical box holding some other value: the fact that
	// "42" is a legal relative URI does not make xs:anyURI("42") an integer.
	// Only the string targets are defined, and the early return above has
	// already taken those.
	case from == xdm.TypeAnyURI:
		return bad()

	// A time has no date, so it cannot widen to one; the other direction
	// (dateTime to time) drops components and is permitted.
	case from == xdm.TypeTime && to != xdm.TypeTime:
		return bad()
	case to == xdm.TypeTime && from != xdm.TypeDateTime:
		return bad()

	// The binary types convert only to each other. Their lexical form looks
	// like a number often enough that this is the case most worth pinning.
	case isBinaryType(from):
		if !isBinaryType(to) {
			return bad()
		}
	case isBinaryType(to):
		return bad()

	// A QName carries a namespace binding that no other type holds, so it
	// converts only from a string — which the early return above covers.
	case from == xdm.TypeQName || to == xdm.TypeQName:
		return bad()

	// A Gregorian type is a *partial* date — a year, a month-day — so it has
	// nothing to widen into. xs:date can be narrowed to xs:gYear by dropping
	// components, but xs:gYear cannot become an xs:date without inventing the
	// month and day, and it cannot become another Gregorian type either
	// because the components the target needs are simply absent.
	case xdm.IsGregorian(from) && from != to:
		return bad()

	// Dates, durations and booleans occupy separate value spaces: a duration
	// is not a point in time and a date is not a number.
	case isCalendarType(from):
		if !isCalendarType(to) {
			return bad()
		}
	case isCalendarType(to):
		return bad()
	case isDurationType(from):
		if !isDurationType(to) {
			return bad()
		}
	case isDurationType(to):
		return bad()
	}
	return nil
}

func isBinaryType(t xdm.TypeCode) bool {
	return t == xdm.TypeHexBinary || t == xdm.TypeBase64Binary
}

func isCalendarType(t xdm.TypeCode) bool {
	switch t {
	case xdm.TypeDate, xdm.TypeTime, xdm.TypeDateTime,
		xdm.TypeGYear, xdm.TypeGYearMonth, xdm.TypeGMonth,
		xdm.TypeGMonthDay, xdm.TypeGDay:
		return true
	}
	return false
}

func isDurationType(t xdm.TypeCode) bool {
	switch t {
	case xdm.TypeDuration, xdm.TypeYearMonthDuration, xdm.TypeDayTimeDuration:
		return true
	}
	return false
}

// parseLexicalQName validates the lexical form of a QName and splits it.
//
// The namespace is left empty: resolving a prefix needs the static context.
func parseLexicalQName(s string) (xdm.QName, error) {
	s = strings.TrimSpace(s)
	prefix, local := "", s
	if i := strings.Index(s, ":"); i >= 0 {
		prefix, local = s[:i], s[i+1:]
	}
	if !isNCName(prefix) && prefix != "" {
		return xdm.QName{}, xdm.ErrCast("invalid xs:QName %q: bad prefix", s)
	}
	if !isNCName(local) {
		return xdm.QName{}, xdm.ErrCast("invalid xs:QName %q: bad local part", s)
	}
	return xdm.QName{Prefix: prefix, Local: local}, nil
}

// isNCName reports whether s is an XML non-colonised name.
func isNCName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !isNameStartRune(r) {
				return false
			}
			continue
		}
		if !isNameRune(r) {
			return false
		}
	}
	return true
}

func isNameStartRune(r rune) bool {
	switch {
	case r == '_':
		return true
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		return true
	case r >= 0xC0 && r != 0xD7 && r != 0xF7:
		// The spec's NameStartChar ranges above Latin-1 are contiguous enough
		// that excluding the two punctuation characters covers them.
		return true
	}
	return false
}

func isNameRune(r rune) bool {
	if isNameStartRune(r) {
		return true
	}
	switch {
	case r >= '0' && r <= '9':
		return true
	case r == '-', r == '.', r == 0xB7:
		return true
	}
	return false
}

// --- Derived-type facets ----------------------------------------------------
//
// The engine's type codes stop at the primitives, so xs:byte and xs:token both
// arrive here as xs:integer and xs:string. That is fine for arithmetic and
// comparison, which is what the codes exist for, but a cast has to apply the
// facet as well: xs:byte(128) is not a byte and xs:token("a\tb") is not a
// token. CastToDerived is the one place that knows the difference.

// CastToDerived casts to a derived type named by its local name, applying the
// facet that the type code cannot carry. An unknown name falls back to a plain
// cast to the primitive.
func CastToDerived(a *xdm.Atomic, target xdm.TypeCode, facet string) (*xdm.Atomic, error) {
	out, err := CastAtomic(a, target)
	if err != nil {
		return nil, err
	}
	if facet == "" {
		return out, nil
	}
	if hasRangeFacet(facet) {
		out, err = applyRangeFacet(out, facet)
	} else if hasStringFacet(facet) {
		out, err = applyStringFacet(out, facet)
	} else if facet == "dateTimeStamp" {
		// xs:dateTimeStamp is xs:dateTime with
		// explicitTimezone="required". A value without one is outside
		// the type's value space, so the cast fails and "castable as"
		// is false — the facet has to be enforced here as well as in
		// the constructor, since a cast does not go through that.
		if dt := out.DateTimeVal(); dt == nil || !dt.HasTZ {
			return nil, xdm.ErrCast(
				"FORG0001: xs:dateTimeStamp requires a timezone")
		}
	} else {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	// The value keeps a note of the type it was built as, which is what makes
	// "xs:int(0) instance of xs:int" true while the plain literal 1 is not an
	// xs:int. Nothing else reads it: arithmetic and comparison work on the
	// primitive.
	return out.WithDerived(facet), nil
}

// derivedSubtypeOf reports whether the derived type "sub" is at or below
// "want" in the XML Schema hierarchy.
//
// The chains are short and fixed, so they are written out rather than derived
// from a general graph: xs:int is a subtype of xs:long, so an xs:int value is
// an instance of xs:long but not the other way round.
func derivedSubtypeOf(sub, want string) bool {
	if sub == want {
		return true
	}
	parents := map[string]string{
		"byte":               "short",
		"short":              "int",
		"int":                "long",
		"long":               "integer",
		"unsignedByte":       "unsignedShort",
		"unsignedShort":      "unsignedInt",
		"unsignedInt":        "unsignedLong",
		"unsignedLong":       "nonNegativeInteger",
		"positiveInteger":    "nonNegativeInteger",
		"nonNegativeInteger": "integer",
		"negativeInteger":    "nonPositiveInteger",
		"nonPositiveInteger": "integer",
		"token":              "normalizedString",
		"normalizedString":   "string",
		"language":           "token",
		"Name":               "token",
		"NMTOKEN":            "token",
		"NCName":             "Name",
		"ID":                 "NCName",
		"IDREF":              "NCName",
		"ENTITY":             "NCName",
	}
	// Walk up from sub, with a bound so a mistake in the table cannot loop.
	for i := 0; i < 8; i++ {
		p, ok := parents[sub]
		if !ok {
			return false
		}
		if p == want {
			return true
		}
		sub = p
	}
	return false
}

type intRange struct{ min, max *big.Int }

var integerFacets = map[string]intRange{
	"long":               {big.NewInt(math.MinInt64), big.NewInt(math.MaxInt64)},
	"int":                {big.NewInt(math.MinInt32), big.NewInt(math.MaxInt32)},
	"short":              {big.NewInt(-32768), big.NewInt(32767)},
	"byte":               {big.NewInt(-128), big.NewInt(127)},
	"unsignedLong":       {big.NewInt(0), unsignedLongMax()},
	"unsignedInt":        {big.NewInt(0), big.NewInt(4294967295)},
	"unsignedShort":      {big.NewInt(0), big.NewInt(65535)},
	"unsignedByte":       {big.NewInt(0), big.NewInt(255)},
	"nonNegativeInteger": {big.NewInt(0), nil},
	"positiveInteger":    {big.NewInt(1), nil},
	"nonPositiveInteger": {nil, big.NewInt(0)},
	"negativeInteger":    {nil, big.NewInt(-1)},
}

func unsignedLongMax() *big.Int {
	v, _ := new(big.Int).SetString("18446744073709551615", 10)
	return v
}

func hasRangeFacet(name string) bool {
	_, ok := integerFacets[name]
	return ok
}

func applyRangeFacet(a *xdm.Atomic, name string) (*xdm.Atomic, error) {
	r, ok := integerFacets[name]
	if !ok || a.Rat() == nil {
		return a, nil
	}
	n := new(big.Int).Quo(a.Rat().Num(), a.Rat().Denom())
	if r.min != nil && n.Cmp(r.min) < 0 || r.max != nil && n.Cmp(r.max) > 0 {
		return nil, xdm.ErrCast("FORG0001: %s is out of range for xs:%s", n, name)
	}
	return a, nil
}

func hasStringFacet(name string) bool {
	switch name {
	case "normalizedString", "token", "language", "Name", "NCName",
		"ID", "IDREF", "ENTITY", "NMTOKEN":
		return true
	}
	return false
}

func applyStringFacet(a *xdm.Atomic, name string) (*xdm.Atomic, error) {
	s := a.String()
	// xs:normalizedString replaces the three whitespace characters; every
	// other subtype additionally collapses runs and trims.
	replaced := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
	if name == "normalizedString" {
		return xdm.NewString(replaced), nil
	}
	// Collapse runs of the *XML* whitespace characters and trim. strings.Fields
	// splits on every Unicode space, which swallowed U+00A0 — a non-breaking
	// space is an ordinary character to XML Schema and has to survive.
	v := collapseXMLSpace(replaced)

	var ok bool
	switch name {
	case "token":
		ok = true
	case "language":
		ok = isLanguageTag(v)
	case "Name":
		ok = isXMLName(v)
	case "NCName", "ID", "IDREF", "ENTITY":
		ok = isNCName(v)
	case "NMTOKEN":
		ok = isNmtoken(v)
	}
	if !ok {
		return nil, xdm.ErrCast("FORG0001: %q is not a valid xs:%s", s, name)
	}
	return xdm.NewString(v), nil
}

// normalizeGregorianFields clears the components a Gregorian type does not
// name, so that two equal values are equal field-by-field as well.
func normalizeGregorianFields(dt *xdm.DateTime, target xdm.TypeCode) {
	// ParseGregorian starts from Month 1, Day 1 and a zero time.
	dt.Hour, dt.Minute = 0, 0
	dt.Second = new(big.Rat)
	switch target {
	case xdm.TypeGYear:
		dt.Month, dt.Day = 1, 1
	case xdm.TypeGYearMonth:
		dt.Day = 1
	case xdm.TypeGMonth:
		dt.Year, dt.Day = 0, 1
	case xdm.TypeGMonthDay:
		dt.Year = 0
	case xdm.TypeGDay:
		dt.Year, dt.Month = 0, 1
	}
}

// validBase64Lexical applies the padding rule XML Schema imposes on
// xs:base64Binary beyond what a decoder checks.
//
// Go's decoder accepts "AP9=" and "Ay==", but the bits a padded group cannot
// represent must be zero: with one "=" the final character encodes only four
// significant bits, so it must come from [AQgw]; with two, only two, so it
// must come from [AEIMQUYcgkosw048]. Without this "AP9=" round-trips to
// something that is not what it says it is.
func validBase64Lexical(s string) bool {
	if len(s)%4 != 0 {
		return false
	}
	if s == "" {
		return true
	}
	switch {
	case strings.HasSuffix(s, "=="):
		// Two padding characters: only 8 of the 12 bits in the two real
		// characters are significant, so the second one's low 4 bits must be
		// zero — index divisible by 16.
		return strings.ContainsRune("AQgw", rune(s[len(s)-3]))
	case strings.HasSuffix(s, "="):
		// One padding character: 16 of 18 bits are significant, so the third
		// character's low 2 bits must be zero — index divisible by 4.
		return strings.ContainsRune("AEIMQUYcgkosw048", rune(s[len(s)-2]))
	}
	return true
}

// validAnyURI rejects the lexical forms that are not URIs.
//
// The type is deliberately permissive — "true" is a valid relative reference,
// and so is almost any string — so this checks only what the grammar actually
// forbids: a percent sign that does not introduce a two-digit hex escape.
// "%" and "%zz" are not URIs, while "http://example.com/~b%C3%A9b%C3%A9" is.
func validAnyURI(s string) error {
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+2 >= len(s) || !isHexDigit(s[i+1]) || !isHexDigit(s[i+2]) {
			return xdm.ErrCast(
				"invalid xs:anyURI %q: %% must introduce a two-digit escape", s)
		}
		i += 2
	}
	return nil
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// isSchemaDecimalLexical reports whether s has the shape XML Schema defines
// for xs:decimal and xs:integer: an optional sign, then digits, with at most
// one decimal point among them.
//
// This exists because big.Rat.SetString accepts Go's syntax rather than the
// schema's — hex literals, fractions, exponents and underscores all parse —
// and each of those turned a malformed lexical form into a plausible value.
func isSchemaDecimalLexical(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	digits, dots := 0, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			digits++
		case s[i] == '.':
			dots++
		default:
			return false
		}
	}
	return digits > 0 && dots <= 1
}

// collapseXMLSpace replaces runs of XML whitespace with a single space and
// trims the ends, which is the whiteSpace="collapse" facet.
//
// XML's whitespace is exactly space, tab, carriage return and newline.
// strings.Fields uses unicode.IsSpace, which is a wider set — U+00A0 among
// others — and collapsing those changes characters the schema treats as
// ordinary text.
func collapseXMLSpace(s string) string {
	isSpace := func(b byte) bool {
		return b == ' ' || b == '\t' || b == '\n' || b == '\r'
	}
	var sb strings.Builder
	sb.Grow(len(s))
	pending := false
	for i := 0; i < len(s); i++ {
		if isSpace(s[i]) {
			pending = sb.Len() > 0
			continue
		}
		if pending {
			sb.WriteByte(' ')
			pending = false
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// ratFitsInt reports whether r, once rounded, will fit an int.
//
// It is the guard the duration operators share: a big.Rat scaled by an
// arbitrary factor can be any size, and every path that narrows one to an int
// has to establish this first or it wraps silently.
func ratFitsInt(r *big.Rat) bool {
	q := new(big.Int).Quo(r.Num(), r.Denom())
	return q.IsInt64() && q.Int64() < math.MaxInt && q.Int64() > math.MinInt
}
