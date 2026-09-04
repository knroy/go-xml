package xdm

import (
	"fmt"
	"math"
	"math/big"
	"strings"
)

// Duration represents xs:duration and its two subtypes.
//
// XML Schema durations have two independent components — months and seconds —
// that cannot be converted into one another, because the number of days in a
// month is not fixed. That is why xs:duration is only partially ordered and
// why the two totally-ordered subtypes (xs:yearMonthDuration and
// xs:dayTimeDuration) exist. Keeping the components separate rather than
// normalising to a single scalar is what makes the ordering rules
// implementable at all.
type Duration struct {
	Negative bool
	Months   int      // years*12 + months
	Seconds  *big.Rat // days*86400 + hours*3600 + minutes*60 + seconds
}

// ParseDuration parses the lexical form of xs:duration,
// xs:yearMonthDuration or xs:dayTimeDuration, rejecting components the
// requested subtype does not permit.
func ParseDuration(s string, t TypeCode) (*Duration, error) {
	orig := s
	s = strings.TrimSpace(s)
	d := &Duration{Seconds: new(big.Rat)}
	if strings.HasPrefix(s, "-") {
		d.Negative = true
		s = s[1:]
	}
	if !strings.HasPrefix(s, "P") {
		return nil, ErrCast("invalid duration %q: must start with P", orig)
	}
	s = s[1:]

	datePart, timePart, hasT := strings.Cut(s, "T")
	if hasT && timePart == "" {
		return nil, ErrCast("invalid duration %q: T with no time components", orig)
	}
	if datePart == "" && !hasT {
		return nil, ErrCast("invalid duration %q: no components", orig)
	}

	var sawAny bool
	// The month count is accumulated in big.Int and range-checked once at the
	// end. Doing it in int silently wrapped: "P768614336404564651Y" multiplied
	// by 12 overflowed and parsed as a *negative* duration of a similar
	// magnitude, so an out-of-range value became a wrong value rather than an
	// error.
	months := new(big.Int)
	// Date components must appear in the order Y, M, D.
	for _, spec := range []struct {
		unit  byte
		apply func(*big.Rat)
	}{
		{'Y', func(v *big.Rat) {
			months.Add(months, new(big.Int).Mul(ratBig(v), big.NewInt(12)))
		}},
		{'M', func(v *big.Rat) { months.Add(months, ratBig(v)) }},
		{'D', func(v *big.Rat) { d.Seconds.Add(d.Seconds, new(big.Rat).Mul(v, big.NewRat(86400, 1))) }},
	} {
		v, rest, ok, err := takeComponent(datePart, spec.unit)
		if err != nil {
			return nil, err
		}
		if ok {
			if !v.IsInt() {
				return nil, ErrCast("invalid duration %q: %c must be an integer", orig, spec.unit)
			}
			spec.apply(v)
			sawAny = true
			datePart = rest
		}
	}
	if datePart != "" {
		return nil, ErrCast("invalid duration %q: unexpected %q", orig, datePart)
	}
	// The bound is what int can hold, not a smaller policy limit. A value
	// that fits is a legal duration however large — "P768614336404564650Y" is
	// 9.2e18 months, which int64 holds — and refusing it at parse reported
	// FORG0001, "the lexical form is wrong", for a form that is perfectly
	// well written. Arithmetic that overflows it is FODT0002, raised where
	// the overflow happens.
	if !months.IsInt64() || months.Int64() > math.MaxInt || months.Int64() < math.MinInt {
		return nil, Errorf("FODT0002",
			"duration %q overflows the month count", orig)
	}
	d.Months = int(months.Int64())

	if hasT {
		for _, spec := range []struct {
			unit  byte
			scale *big.Rat
			frac  bool
		}{
			{'H', big.NewRat(3600, 1), false},
			{'M', big.NewRat(60, 1), false},
			{'S', big.NewRat(1, 1), true},
		} {
			v, rest, ok, err := takeComponent(timePart, spec.unit)
			if err != nil {
				return nil, err
			}
			if ok {
				if !spec.frac && !v.IsInt() {
					return nil, ErrCast("invalid duration %q: %c must be an integer", orig, spec.unit)
				}
				d.Seconds.Add(d.Seconds, new(big.Rat).Mul(v, spec.scale))
				sawAny = true
				timePart = rest
			}
		}
		if timePart != "" {
			return nil, ErrCast("invalid duration %q: unexpected %q", orig, timePart)
		}
	}
	if !sawAny {
		return nil, ErrCast("invalid duration %q: no components", orig)
	}

	switch t {
	case TypeYearMonthDuration:
		if d.Seconds.Sign() != 0 {
			return nil, ErrCast("xs:yearMonthDuration %q must not have day/time components", orig)
		}
	case TypeDayTimeDuration:
		if d.Months != 0 {
			return nil, ErrCast("xs:dayTimeDuration %q must not have year/month components", orig)
		}
	}
	return d, nil
}

// takeComponent pulls a leading numeric component terminated by unit from s.
func takeComponent(s string, unit byte) (val *big.Rat, rest string, ok bool, err error) {
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != unit {
		return nil, s, false, nil
	}
	lex := s[:i]
	// big.Rat.SetString accepts ".5" and "30.", which XML Schema does not: the
	// grammar requires at least one digit on each side of the point, and at
	// most one point. Without this check "PT.5S" parses as half a second
	// instead of being rejected.
	if !validDecimalLexical(lex) {
		return nil, s, false, ErrCast("invalid duration component %q", lex)
	}
	r := new(big.Rat)
	if _, good := r.SetString(lex); !good {
		return nil, s, false, ErrCast("invalid duration component %q", lex)
	}
	return r, s[i+1:], true, nil
}

func ratInt(r *big.Rat) int64 {
	return new(big.Int).Quo(r.Num(), r.Denom()).Int64()
}

// ratBig is ratInt without the narrowing, for callers that range-check the
// result themselves rather than accepting a wrapped one.
func ratBig(r *big.Rat) *big.Int {
	return new(big.Int).Quo(r.Num(), r.Denom())
}

// Lexical returns the canonical lexical form for the given duration type.
func (d *Duration) Lexical(t TypeCode) string {
	var sb strings.Builder
	// A zero-magnitude duration has no direction, so it carries no sign:
	// "-PT0S" and "PT0S" are the same value and share one canonical form.
	// Keeping the minus made two equal durations serialise differently, which
	// then made string comparison of them disagree with "eq".
	if d.Negative && (d.Months != 0 || d.Seconds.Sign() != 0) {
		sb.WriteByte('-')
	}
	sb.WriteByte('P')

	months := d.Months
	years := months / 12
	months = months % 12
	if years != 0 {
		fmt.Fprintf(&sb, "%dY", years)
	}
	if months != 0 {
		fmt.Fprintf(&sb, "%dM", months)
	}

	secs := new(big.Rat).Set(d.Seconds)
	day := big.NewRat(86400, 1)
	days := new(big.Int).Quo(new(big.Int).Mul(secs.Num(), big.NewInt(1)), new(big.Int).Mul(secs.Denom(), big.NewInt(86400)))
	if days.Sign() != 0 {
		fmt.Fprintf(&sb, "%sD", days.String())
		secs.Sub(secs, new(big.Rat).Mul(new(big.Rat).SetInt(days), day))
	}

	if secs.Sign() != 0 {
		sb.WriteByte('T')
		hourR := big.NewRat(3600, 1)
		h := new(big.Int).Quo(new(big.Int).Mul(secs.Num(), big.NewInt(1)), new(big.Int).Mul(secs.Denom(), big.NewInt(3600)))
		if h.Sign() != 0 {
			fmt.Fprintf(&sb, "%sH", h.String())
			secs.Sub(secs, new(big.Rat).Mul(new(big.Rat).SetInt(h), hourR))
		}
		minR := big.NewRat(60, 1)
		m := new(big.Int).Quo(new(big.Int).Mul(secs.Num(), big.NewInt(1)), new(big.Int).Mul(secs.Denom(), big.NewInt(60)))
		if m.Sign() != 0 {
			fmt.Fprintf(&sb, "%sM", m.String())
			secs.Sub(secs, new(big.Rat).Mul(new(big.Rat).SetInt(m), minR))
		}
		if secs.Sign() != 0 {
			sb.WriteString(strings.TrimSuffix(strings.TrimRight(secs.FloatString(secondsScale(secs)), "0"), "."))
			sb.WriteByte('S')
		}
	}

	// A zero duration still needs a component; which one depends on the type.
	s := sb.String()
	if strings.HasSuffix(s, "P") {
		if t == TypeYearMonthDuration {
			return s + "0M"
		}
		return s + "T0S"
	}
	return s
}

// SignedMonths returns the month component with the sign applied.
func (d *Duration) SignedMonths() int {
	if d.Negative {
		return -d.Months
	}
	return d.Months
}

// SignedSeconds returns the second component with the sign applied.
func (d *Duration) SignedSeconds() *big.Rat {
	if d.Negative {
		return new(big.Rat).Neg(d.Seconds)
	}
	return new(big.Rat).Set(d.Seconds)
}

// validDecimalLexical reports whether s is a bare unsigned decimal: one or
// more digits, optionally followed by a point and one or more further digits.
func validDecimalLexical(s string) bool {
	if s == "" {
		return false
	}
	digits, dots, afterDot := 0, 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
			if dots > 0 {
				afterDot++
			}
		case c == '.':
			dots++
			// A point before any digit, or a second point, is invalid.
			if dots > 1 || digits == 0 {
				return false
			}
		default:
			return false
		}
	}
	// A trailing point has no digits after it.
	return digits > 0 && (dots == 0 || afterDot > 0)
}
