package xdm

import (
	"math/big"
	"testing"
)

// SameKey is the W3C op:same-key relation written out directly, as the oracle
// that MapKeyOf is an optimisation of.
//
// MapKeyOf encodes the relation as a string so that a lookup is one map access
// instead of a scan. That is the right implementation, but it means the
// relation itself is never stated anywhere an assertion can reach: the only
// description of "two values are the same key" was the shape of the strings
// MapKeyOf happened to build. Every recent defect in this area had that form --
// a property that was true of the encoding rather than of the relation, so
// nothing could contradict it.
//
// So the relation is spelled here, from the specification rather than from the
// code, and TestMapKeyOfMatchesSameKey asserts that the two agree on every
// pair. If MapKeyOf and SameKey ever disagree, exactly one of them is wrong and
// the test says which pair to look at.
//
// op:same-key(A, B) is true when A and B are both:
//   - NaN of any numeric type; or
//   - numeric and equal as exact values; or
//   - in one type family and equal under "eq" WITHOUT the implicit timezone.
//
// The last clause is the one that matters and the one most easily got wrong.
// op:same-key is deliberately NOT the "eq" operator: "eq" substitutes the
// implicit timezone into an unzoned value, so xs:date("2015-04-08") and
// xs:date("2015-04-08Z") are equal under "eq" in UTC. They are NOT the same
// key -- same-key-013, -014 and -015 build a three-entry map from exactly that
// pair and require all three entries to survive. A map key is decided by the
// value alone, and a value with no timezone is a different value from one
// carrying a timezone; a key that depended on the implicit timezone would make
// the same map have different sizes in different dynamic contexts.
//
// Two zoned values, on the other hand, name one instant regardless of how they
// spell the offset, and so are one key (same-key-027).
func SameKey(a, b *Atomic) bool {
	if a == nil || b == nil {
		return a == b
	}

	// Numerics form one family across all subtypes: an xs:integer and the
	// xs:double spelling of the same value are one key (same-key-012).
	if a.Type.IsNumeric() && b.Type.IsNumeric() {
		af, bf := a.Float64(), b.Float64()
		if isNaNf(af) || isNaNf(bf) {
			return isNaNf(af) && isNaNf(bf)
		}
		if isInff(af) || isInff(bf) {
			return af == bf
		}
		ra, rb := exactRat(a), exactRat(b)
		if ra == nil || rb == nil {
			return af == bf
		}
		return ra.Cmp(rb) == 0
	}
	if a.Type.IsNumeric() != b.Type.IsNumeric() {
		return false
	}

	// The three duration types are one family, compared over the
	// (months, seconds) pair rather than the lexical form (map-get-017).
	if isDur(a.Type) && isDur(b.Type) {
		da, db := a.DurationVal(), b.DurationVal()
		if da == nil || db == nil {
			return a.String() == b.String()
		}
		return da.SignedMonths() == db.SignedMonths() &&
			da.SignedSeconds().Cmp(db.SignedSeconds()) == 0
	}
	if isDur(a.Type) != isDur(b.Type) {
		return false
	}

	// The eight calendar types each stand alone -- a gYear never collides with
	// a gMonth -- but within one type, two ZONED values naming the same instant
	// are one key, and an unzoned value keys on its own spelling.
	if isCal(a.Type) && isCal(b.Type) {
		if a.Type != b.Type {
			return false
		}
		da, db := a.DateTimeVal(), b.DateTimeVal()
		if da == nil || db == nil {
			return a.String() == b.String()
		}
		if da.HasTZ != db.HasTZ {
			// No implicit timezone is applied: see the commentary above.
			return false
		}
		if !da.HasTZ {
			return a.String() == b.String()
		}
		return da.ToSeconds(0).Cmp(db.ToSeconds(0)) == 0
	}
	if isCal(a.Type) != isCal(b.Type) {
		return false
	}

	if a.Type == TypeBoolean && b.Type == TypeBoolean {
		return a.Bool() == b.Bool()
	}
	if a.Type == TypeQName && b.Type == TypeQName {
		qa, qb := a.QName(), b.QName()
		if qa == nil || qb == nil {
			return qa == qb
		}
		return qa.URI == qb.URI && qa.Local == qb.Local
	}

	// Everything else is its type family plus its lexical value; xs:string,
	// xs:anyURI and xs:untypedAtomic share one family.
	return typeFamilyOf(a) == typeFamilyOf(b) && a.String() == b.String()
}

func isDur(t TypeCode) bool {
	return t == TypeDuration || t == TypeYearMonthDuration || t == TypeDayTimeDuration
}

func isCal(t TypeCode) bool {
	return t == TypeDate || t == TypeTime || t == TypeDateTime || IsGregorian(t)
}

func isNaNf(f float64) bool { return f != f }

func isInff(f float64) bool {
	return f > 1.7976931348623157e308 || f < -1.7976931348623157e308
}

func exactRat(a *Atomic) *big.Rat {
	if r := a.Rat(); r != nil {
		return r
	}
	return new(big.Rat).SetFloat64(a.Float64())
}

// sameKeyCorpus builds a spread of atomic values across every family that has
// a non-trivial key rule, including the pairs the QT3 same-key cases turn on.
func sameKeyCorpus(t *testing.T) []*Atomic {
	t.Helper()
	var out []*Atomic
	add := func(a *Atomic) {
		if a != nil {
			out = append(out, a)
		}
	}

	// Numerics: equal values spelled across subtypes, and the pair that must
	// NOT collide (map-put-023), plus NaN and both infinities.
	add(NewInteger(1))
	add(NewInteger(0))
	add(NewInteger(-1))
	add(NewDouble(1))
	add(NewFloat(1))
	add(NewDouble(1.00000000001))
	if r, ok := new(big.Rat).SetString("1.0000000000100000000001"); ok {
		add(NewDecimal(r))
	}
	add(NewDouble(nan()))
	add(NewFloat(nan()))
	add(NewDouble(inf(1)))
	add(NewDouble(inf(-1)))

	add(NewBoolean(true))
	add(NewBoolean(false))
	add(NewString("1"))
	add(NewString("foo"))
	add(NewUntypedAtomic("foo"))
	add(NewAnyURI("foo"))
	add(NewUntypedAtomic("12"))
	add(NewQNameValue(QName{URI: "u", Local: "a"}))
	add(NewQNameValue(QName{URI: "u", Local: "b"}))
	add(NewQNameValue(QName{URI: "v", Local: "a"}))

	// Durations: one family over (months, seconds); P1Y and P12M collide.
	for _, d := range []struct {
		s string
		t TypeCode
	}{
		{"P1Y", TypeDuration}, {"P12M", TypeYearMonthDuration},
		{"P0Y", TypeYearMonthDuration}, {"P0D", TypeDayTimeDuration},
		{"PT1S", TypeDayTimeDuration}, {"-P1Y", TypeDuration},
	} {
		if dv, err := ParseDuration(d.s, d.t); err == nil {
			add(NewDuration(dv, d.t))
		}
	}

	// The eight calendar types, each with no timezone, with Z, and with two
	// offsets that name the same instant as Z for the zoned forms.
	cal := map[TypeCode][]string{
		TypeDateTime:   {"2015-04-08T01:30:00", "2015-04-08T02:30:00"},
		TypeDate:       {"2015-04-08", "2015-04-09"},
		TypeTime:       {"01:30:00", "17:00:00", "12:00:00"},
		TypeGYear:      {"2015", "2014"},
		TypeGYearMonth: {"2015-10", "2015-11"},
		TypeGMonth:     {"--10", "--11"},
		TypeGMonthDay:  {"--10-10", "--11-11"},
		TypeGDay:       {"---10", "---11"},
	}
	for typ, bases := range cal {
		for _, b := range bases {
			for _, off := range []string{"", "Z", "+00:00", "-05:00", "+05:30"} {
				s := b + off
				switch typ {
				case TypeDate, TypeTime, TypeDateTime:
					if dt, err := ParseDateTime(s, typ); err == nil {
						add(NewDateTime(dt, typ))
					}
				default:
					if dt, err := ParseGregorian(s, typ); err == nil {
						add(NewGregorian(dt, typ))
					}
				}
			}
		}
	}
	return out
}

// TestMapKeyOfMatchesSameKey is the property the encoding exists to implement:
// two values share a canonical key exactly when op:same-key holds of them.
//
// It is two-sided on purpose. "same key implies SameKey" alone is satisfied by
// an encoding that gives every value a distinct key, and "SameKey implies same
// key" alone by one that gives every value the same key; only the biconditional
// rules both out. That asymmetry is what let an earlier one-sided soundness
// property accept a budget that skipped its own check.
func TestMapKeyOfMatchesSameKey(t *testing.T) {
	vals := sameKeyCorpus(t)
	if len(vals) < 50 {
		t.Fatalf("corpus is only %d values; it is meant to be a spread, not a sample", len(vals))
	}

	keys := make([]string, len(vals))
	for i, v := range vals {
		k, err := MapKeyOf(v)
		if err != nil {
			t.Fatalf("MapKeyOf(%s %q): %v", v.Type, v.String(), err)
		}
		keys[i] = k
	}

	bad := 0
	for i := range vals {
		for j := range vals {
			want := SameKey(vals[i], vals[j])
			got := keys[i] == keys[j]
			if want == got {
				continue
			}
			bad++
			if bad <= 12 {
				t.Errorf("op:same-key disagrees with the canonical key:\n"+
					"  a = %-16s %q  key %q\n"+
					"  b = %-16s %q  key %q\n"+
					"  SameKey = %v, keys equal = %v",
					vals[i].Type, vals[i].String(), keys[i],
					vals[j].Type, vals[j].String(), keys[j],
					want, got)
			}
		}
	}
	if bad > 12 {
		t.Errorf("... and %d further disagreeing pairs", bad-12)
	}
	t.Logf("%d values, %d ordered pairs agree", len(vals), len(vals)*len(vals)-bad)
}

// TestSameKeyIsAnEquivalence guards the oracle itself. A relation that is not
// reflexive, symmetric and transitive cannot be implemented by ANY canonical
// key, so if this fails the oracle is wrong and the test above is measuring
// against nothing.
func TestSameKeyIsAnEquivalence(t *testing.T) {
	vals := sameKeyCorpus(t)
	for i := range vals {
		if !SameKey(vals[i], vals[i]) {
			// NaN is the deliberate exception under "eq" but NOT under
			// op:same-key, which requires every NaN to be one key.
			t.Errorf("not reflexive: %s %q", vals[i].Type, vals[i].String())
		}
		for j := range vals {
			if SameKey(vals[i], vals[j]) != SameKey(vals[j], vals[i]) {
				t.Errorf("not symmetric: %s %q vs %s %q",
					vals[i].Type, vals[i].String(), vals[j].Type, vals[j].String())
			}
			if !SameKey(vals[i], vals[j]) {
				continue
			}
			for k := range vals {
				if SameKey(vals[j], vals[k]) && !SameKey(vals[i], vals[k]) {
					t.Errorf("not transitive: %q ~ %q ~ %q but not %q ~ %q",
						vals[i].String(), vals[j].String(), vals[k].String(),
						vals[i].String(), vals[k].String())
				}
			}
		}
	}
}

func nan() float64 { var z float64; return z / z }
func inf(s int) float64 {
	if s < 0 {
		return -1 / zerof()
	}
	return 1 / zerof()
}
func zerof() float64 { var z float64; return z }
