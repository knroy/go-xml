package xdm

import (
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"strings"
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
//
// SameKey returns the relation's verdict AND whether the oracle is entitled to
// one. ok is false when either operand has a type oracleFamily does not model;
// the verdict is then meaningless and every call site must fail the test by
// name rather than use it. That is the fail-closed contract: an oracle that
// guessed at an unmodelled type would agree with whatever MapKeyOf did and the
// corpus would stay green over a real defect.
func SameKey(a, b *Atomic) (same bool, ok bool) {
	if a == nil || b == nil {
		return a == b, true
	}
	if _, ok := oracleFamilyOf(a); !ok {
		return false, false
	}
	if _, ok := oracleFamilyOf(b); !ok {
		return false, false
	}

	// Numerics form one family across all subtypes: an xs:integer and the
	// xs:double spelling of the same value are one key (same-key-012).
	if a.Type.IsNumeric() && b.Type.IsNumeric() {
		af, bf := a.Float64(), b.Float64()
		if isNaNf(af) || isNaNf(bf) {
			return isNaNf(af) && isNaNf(bf), true
		}
		if isInff(af) || isInff(bf) {
			return af == bf, true
		}
		ra, rb := exactRat(a), exactRat(b)
		if ra == nil || rb == nil {
			return af == bf, true
		}
		return ra.Cmp(rb) == 0, true
	}
	if a.Type.IsNumeric() != b.Type.IsNumeric() {
		return false, true
	}

	// The three duration types are one family, compared over the
	// (months, seconds) pair rather than the lexical form (map-get-017).
	if isDur(a.Type) && isDur(b.Type) {
		da, db := a.DurationVal(), b.DurationVal()
		if da == nil || db == nil {
			// A duration type with no parsed (months, seconds) has no
			// oracle representation. Refuse rather than compare
			// spellings: finding 3 is exactly this fallback.
			return false, false
		}
		return da.SignedMonths() == db.SignedMonths() &&
			da.SignedSeconds().Cmp(db.SignedSeconds()) == 0, true
	}
	if isDur(a.Type) != isDur(b.Type) {
		return false, true
	}

	// The eight calendar types each stand alone -- a gYear never collides with
	// a gMonth -- but within one type, two ZONED values naming the same instant
	// are one key, and an unzoned value keys on its own spelling.
	if isCal(a.Type) && isCal(b.Type) {
		if a.Type != b.Type {
			return false, true
		}
		da, db := a.DateTimeVal(), b.DateTimeVal()
		if da == nil || db == nil {
			// No parsed calendar value means no timezone-presence fact
			// and no instant, so the oracle has nothing to judge with.
			return false, false
		}
		if da.HasTZ != db.HasTZ {
			// No implicit timezone is applied: see the commentary above.
			return false, true
		}
		if !da.HasTZ {
			// An unzoned calendar value keys on its own canonical
			// spelling. This is not the fail-open tail: the type is
			// modelled, both operands are the same calendar type, and
			// the spelling IS the value space here.
			return a.String() == b.String(), true
		}
		return da.ToSeconds(0).Cmp(db.ToSeconds(0)) == 0, true
	}
	if isCal(a.Type) != isCal(b.Type) {
		return false, true
	}

	if a.Type == TypeBoolean && b.Type == TypeBoolean {
		return a.Bool() == b.Bool(), true
	}
	if a.Type == TypeQName && b.Type == TypeQName {
		qa, qb := a.QName(), b.QName()
		if qa == nil || qb == nil {
			// A QName with no resolved (URI, local) pair has no oracle
			// representation; its lexical prefix is not the value.
			return false, false
		}
		return qa.URI == qb.URI && qa.Local == qb.Local, true
	}

	// The two binary types are each their own family, but within a type the
	// value is the OCTET SEQUENCE, not the spelling. XSD Part 2 §3.2.15 gives
	// xs:hexBinary the value space of finite octet sequences and a lexical
	// space in which each octet is "two hexadecimal digits" -- case is not
	// part of the value, so "0F" and "0f" denote the same octet and are one
	// key. §3.2.16 likewise lets xs:base64Binary carry whitespace between its
	// characters without changing the octets. xpath/operators.go:511
	// (binaryOctets) already compares these by decoding for "eq"; the key has
	// to agree, or a value would fail to find itself in a map.
	if isBin(a.Type) && isBin(b.Type) {
		if a.Type != b.Type {
			return false, true
		}
		oa, oka := decodeBinary(a)
		ob, okb := decodeBinary(b)
		if !oka || !okb {
			// The value of a binary is its octet sequence. A lexical
			// form that will not decode has no octets, so the oracle
			// has no representation to compare.
			return false, false
		}
		return string(oa) == string(ob), true
	}
	if isBin(a.Type) != isBin(b.Type) {
		return false, true
	}

	// Two cases reach here, and both are decided, not guessed.
	//
	// The branches above each handle one family and then return false for a
	// pair that straddles it -- but only for the families whose rule needed a
	// branch. A pair like xs:boolean("true") vs xs:string("1") straddles two
	// families neither of which has a straddle test above, so it arrives
	// here: different families, therefore different keys. That is a real
	// verdict from the F&O rule that same-key holds only WITHIN a family.
	//
	// The other case is a same-family pair in the string family -- xs:string,
	// xs:anyURI and xs:untypedAtomic -- whose value space IS the character
	// sequence, so comparing String() is the specified rule and not a
	// fallback.
	//
	// What CANNOT reach here is an unmodelled type: those were refused at the
	// top. That is the difference from the version this finding was about,
	// whose tail applied String() to any type at all.
	//
	// The family comparison is intentionally independent of MapKeyOf's
	// typeFamilyOf: this predicate is the guard that catches an erroneous
	// production family grouping (finding 2).
	fa, oka := oracleFamilyOf(a)
	fb, okb := oracleFamilyOf(b)
	if !oka || !okb {
		// Unreachable: refused at the top of the function. Asserted rather
		// than assumed, so a future edit that moves the entry guard cannot
		// silently restore the fail-open tail this finding was about.
		return false, false
	}
	if fa != fb {
		return false, true
	}
	if fa != "string-family" {
		// A same-family pair in a MODELLED family that no branch above
		// decided. Every such family has a branch, so this is an oracle
		// bug rather than a verdict: refuse rather than fall back to
		// comparing spellings, which is precisely the fail-open tail.
		return false, false
	}
	return a.String() == b.String(), true
}

// oracleFamily names the op:same-key equality category of a type, and reports
// whether the oracle models that type at all.
//
// The switch is EXHAUSTIVE by construction and fails closed: there is no
// default arm that invents a family. The earlier version ended in
//
//	default: return "type:" + a.Type.String()
//
// which made an unmodelled type fall through to a comparison of a type NAME
// plus String(). That is fail-OPEN -- a type nobody taught the oracle got a
// plausible-looking verdict, and if MapKeyOf were wrong about it the oracle
// would agree and the corpus would stay green. An oracle with no opinion must
// say so; TestOracleModelsEveryCorpusType and the !ok branches at the call
// sites turn that admission into a named failure.
//
// Every family name here is written from F&O 3.1 op:same-key directly. It is
// deliberately independent of MapKeyOf's typeFamilyOf: this predicate is the
// guard that catches an erroneous production family grouping (finding 2), so
// it must not be derived from the thing it checks.
func oracleFamily(t TypeCode) (string, bool) {
	switch t {
	// F&O casts an untyped map key to xs:string, and xs:anyURI compares with
	// xs:string under "eq"; the three share one family.
	case TypeString, TypeAnyURI, TypeUntypedAtomic:
		return "string-family", true

	// The four numerics are one family across subtypes: xs:integer(1) and
	// xs:double(1) are one key (same-key-012). The numeric branch of SameKey
	// handles the value comparison; this is only the grouping.
	case TypeInteger, TypeDecimal, TypeDouble, TypeFloat:
		return "numeric-family", true

	// The three duration types are one family, compared over (months,
	// seconds) rather than lexically (map-get-017).
	case TypeDuration, TypeYearMonthDuration, TypeDayTimeDuration:
		return "duration-family", true

	// The eight calendar types each stand alone -- a gYear never collides
	// with a gMonth -- so each is its own family. Timezone presence is a
	// property of the VALUE, not of the family, and is handled in SameKey.
	case TypeDate:
		return "calendar:date", true
	case TypeTime:
		return "calendar:time", true
	case TypeDateTime:
		return "calendar:dateTime", true
	case TypeGYear:
		return "calendar:gYear", true
	case TypeGYearMonth:
		return "calendar:gYearMonth", true
	case TypeGMonth:
		return "calendar:gMonth", true
	case TypeGMonthDay:
		return "calendar:gMonthDay", true
	case TypeGDay:
		return "calendar:gDay", true

	// The two binary types are separate families: "010203" and "AQID" decode
	// to the same three octets and must not be one key.
	case TypeHexBinary:
		return "binary:hexBinary", true
	case TypeBase64Binary:
		return "binary:base64Binary", true

	// Singleton families: equality is over the value, not the spelling, and
	// nothing else shares the family.
	case TypeBoolean:
		return "boolean", true
	case TypeQName:
		return "qname", true
	}
	// Unmodelled. Every TypeCode declared in atomic.go is named above, so
	// reaching here means a new type code was added without teaching the
	// oracle its op:same-key category. Say so rather than guess.
	//
	// UNMODELLED TYPES: none at present. xs:NOTATION, which the plan lists,
	// has no TypeCode in this implementation -- see the comment at the head
	// of atomic.go on why derived and rarely-used types are not distinct
	// codes. If one is ever added it lands here and fails the test by name.
	return "", false
}

// oracleFamilyOf is the *Atomic form, kept so the call sites read as before.
func oracleFamilyOf(a *Atomic) (string, bool) { return oracleFamily(a.Type) }

func isBin(t TypeCode) bool { return t == TypeHexBinary || t == TypeBase64Binary }

// decodeBinary reads a binary value's octets. It decodes rather than
// consulting the lexical form for the reason above: the spelling is not the
// value. Whitespace is stripped throughout for base64 (§3.2.16), and hex is
// folded to one case before decoding.
func decodeBinary(a *Atomic) ([]byte, bool) {
	if a.Type == TypeHexBinary {
		b, err := hex.DecodeString(strings.ToLower(a.String()))
		return b, err == nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(a.String()), ""))
	return b, err == nil
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
	// An xs:decimal and the xs:double spelling of the same exact value.
	// NewDecimal canonicalises its rational at construction, so "1.0" and
	// "1.00" are not two distinct Atomics and cannot be the witness; a
	// value with a surviving fractional part is. The decimal prints "1.5"
	// where the key is the rational 3/2, so an encoder that keyed on the
	// spelling would split this pair while op:same-key holds it together
	// (mutation lane: numeric-key-is-lexical).
	if r, ok := new(big.Rat).SetString("1.5"); ok {
		add(NewDecimal(r))
	}
	add(NewDouble(1.5))
	if r, ok := new(big.Rat).SetString("1.0000000000100000000001"); ok {
		add(NewDecimal(r))
	}
	add(NewDouble(nan()))
	add(NewFloat(nan()))
	add(NewDouble(inf(1)))
	add(NewDouble(inf(-1)))
	// The infinities, like NaN, take an early return in MapKeyOf that is
	// shared across the numeric types. With only the xs:double spellings
	// present, a key that appended the type to "num:INF" would have been
	// indistinguishable from the correct one -- the mutation lane caught
	// exactly that gap (infinities-per-numeric-type).
	add(NewFloat(inf(1)))
	add(NewFloat(inf(-1)))

	// Large exact integers. With only 1/0/-1 the Rat()-nil branch at
	// maparray.go:116 is never reached by a value big enough for exactness to
	// matter: 2^53+1 and 2^53 are ONE float64 and two integers, so an
	// encoding that went through Float64 would merge them. 2^63 is past
	// int64, which is why it is built from a rational rather than NewInteger.
	add(NewInteger(1 << 53))
	add(NewInteger(1<<53 + 1))
	add(NewDouble(1 << 53))
	if r, ok := new(big.Rat).SetString("9223372036854775808"); ok {
		add(NewIntegerFromRat(r))
	}
	if r, ok := new(big.Rat).SetString("9223372036854775809"); ok {
		add(NewIntegerFromRat(r))
	}

	// Negative zero: equal to +0 as a value, distinct as a spelling. The
	// numeric branch keys on the exact rational, where the sign of zero does
	// not survive, so these two must collide however they print.
	add(NewDouble(negZerof()))
	add(NewFloat(negZerof()))

	// Float boundaries, and the float32/float64 seam. NewFloat rounds to
	// float32 on construction, so NewFloat(1.1) and NewDouble(1.1) are
	// DIFFERENT values and must not share a key; NewDouble of the widened
	// float32 must share one with the float.
	add(NewDouble(maxFloat64()))
	add(NewDouble(smallestNonzeroFloat64()))
	add(NewFloat(1.1))
	add(NewDouble(1.1))
	add(NewDouble(float64(float32(1.1))))

	add(NewBoolean(true))
	add(NewBoolean(false))
	add(NewString("1"))
	add(NewString("foo"))
	add(NewUntypedAtomic("foo"))
	add(NewAnyURI("foo"))
	// map-get-008: the function conversion rules cast an untyped key to
	// xs:string, never to a number, so these two must stay distinct. The
	// xs:integer(12) is the half that was missing -- without it no pair
	// could contradict an encoder that cast untyped to the numeric family
	// (mutation lane: merge-untypedAtomic-into-numeric).
	add(NewUntypedAtomic("12"))
	add(NewInteger(12))
	add(NewString("12"))
	add(NewQNameValue(QName{URI: "u", Local: "a"}))
	add(NewQNameValue(QName{URI: "u", Local: "b"}))
	add(NewQNameValue(QName{URI: "v", Local: "a"}))

	// The two binary types, which had no key coverage at all. Both key
	// through the typeFamilyOf+String() tail, and both have lexical forms
	// that spell ONE value more than one way -- hexBinary is case-insensitive
	// (XSD Part 2 §3.2.15: the value is the octet sequence, and "0F" and "0f"
	// denote the same octet), and base64Binary admits whitespace between its
	// characters (§3.2.16). Equal-value/different-spelling pairs are the
	// whole point of including them; a key built from the raw lexical form
	// splits values that eq holds identical. The same-type/different-octets
	// and cross-type pairs come free from the Cartesian product.
	for _, b := range []struct {
		s string
		t TypeCode
	}{
		{"0F", TypeHexBinary}, {"0f", TypeHexBinary},
		{"DEADBEEF", TypeHexBinary}, {"deadbeef", TypeHexBinary},
		{"DeadBeef", TypeHexBinary}, {"00", TypeHexBinary},
		{"AQID", TypeBase64Binary}, {"AQ ID", TypeBase64Binary},
		{"AAAA", TypeBase64Binary},
		// The two binary types are separate families, and nothing in the
		// corpus said so until these: "010203" and "AQID" decode to the
		// SAME three octets, and "000000" and "AAAA" to the same three
		// zeroes, so a key built over the decoded octets alone -- dropping
		// the type -- would merge them. Only an equal-octets/different-type
		// pair can contradict that (mutation lane: merge-binary-types).
		{"010203", TypeHexBinary}, {"000000", TypeHexBinary},
	} {
		add(NewBinary(b.s, b.t))
	}

	// Durations: one family over (months, seconds); P1Y and P12M collide.
	for _, d := range []struct {
		s string
		t TypeCode
	}{
		{"P1Y", TypeDuration}, {"P12M", TypeYearMonthDuration},
		{"P0Y", TypeYearMonthDuration}, {"P0D", TypeDayTimeDuration},
		{"PT1S", TypeDayTimeDuration}, {"-P1Y", TypeDuration},
		// Negative dayTimeDuration: only positive and zero were covered, and
		// the key is built from SignedMonths/SignedSeconds, where a dropped
		// sign would merge -PT1S with PT1S. -PT24H and -P1D are one value
		// spelled two ways and must collide.
		{"-PT1S", TypeDayTimeDuration}, {"-P1D", TypeDayTimeDuration},
		{"-PT24H", TypeDayTimeDuration}, {"-PT0S", TypeDayTimeDuration},
		{"-P1DT12H", TypeDuration},
	} {
		if dv, err := ParseDuration(d.s, d.t); err == nil {
			add(NewDuration(dv, d.t))
		}
	}

	// The eight calendar types, each with no timezone, with Z, and with two
	// offsets that name the same instant as Z for the zoned forms.
	//
	// Every base here used to be mid-range, so no offset ever crossed an
	// edge. The near-midnight dateTimes below cross a day, a month and a year
	// boundary at once: 2001-01-01T00:30:00+01:00 is 2000-12-31T23:30:00Z,
	// one instant with two spellings in two different years, and the pair is
	// one key only if the key is the instant rather than the calendar fields.
	// 23:30 on the last day of a month crosses forward the same way.
	cal := map[TypeCode][]string{
		TypeDateTime: {
			"2015-04-08T01:30:00", "2015-04-08T02:30:00",
			"2001-01-01T00:30:00", "2000-12-31T23:30:00",
			"2000-02-29T23:30:00", "2000-03-01T00:30:00",
		},
		TypeDate:       {"2015-04-08", "2015-04-09", "2001-01-01", "2000-12-31"},
		TypeTime:       {"01:30:00", "17:00:00", "12:00:00", "00:30:00", "23:30:00"},
		TypeGYear:      {"2015", "2014"},
		TypeGYearMonth: {"2015-10", "2015-11"},
		TypeGMonth:     {"--10", "--11"},
		TypeGMonthDay:  {"--10-10", "--11-11"},
		TypeGDay:       {"---10", "---11"},
	}
	// The eight calendar types stand alone, and until this pair nothing in
	// the corpus said so. Every base above is mid-range and no two types
	// share a spelling, so a key that dropped the type name and kept only
	// the normalised instant -- one "calendar" family -- produced no
	// collision and went undetected by the mutation lane
	// (merge-calendar-types). A zoned xs:date and the zoned xs:dateTime at
	// midnight of the same day normalise to the SAME instant, so they are
	// one key under that fault and two under the correct encoding; the
	// unzoned pair does the same job through the lexical tail.
	if dt, err := ParseDateTime("2015-04-08T00:00:00Z", TypeDateTime); err == nil {
		add(NewDateTime(dt, TypeDateTime))
	}
	if dt, err := ParseDateTime("2015-04-08T00:00:00", TypeDateTime); err == nil {
		add(NewDateTime(dt, TypeDateTime))
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
			want, ok := SameKey(vals[i], vals[j])
			if !ok {
				// Fail closed. The oracle has no op:same-key model for one
				// of these types, so it is not entitled to a verdict and
				// MUST NOT supply a guessed one: a corpus judged by a
				// guessing oracle stays green over a real MapKeyOf defect.
				t.Fatalf("unsupported oracle representation: no op:same-key model for "+
					"%s %q vs %s %q.\nThe oracle cannot judge this pair, so this test "+
					"is measuring nothing for it. Teach oracleFamily and SameKey the "+
					"type's F&O equality category, or remove the value from the corpus "+
					"with a stated reason -- do not let it fall through to a lexical "+
					"comparison.",
					vals[i].Type, vals[i].String(), vals[j].Type, vals[j].String())
			}
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
	// same is SameKey with the fail-closed contract enforced: a pair the
	// oracle cannot judge aborts the test by name rather than contributing a
	// guessed verdict to an algebraic property.
	same := func(a, b *Atomic) bool {
		v, ok := SameKey(a, b)
		if !ok {
			t.Fatalf("unsupported oracle representation: no op:same-key model for "+
				"%s %q vs %s %q; the equivalence properties cannot be checked over "+
				"a pair the oracle has no opinion about",
				a.Type, a.String(), b.Type, b.String())
		}
		return v
	}
	for i := range vals {
		if !same(vals[i], vals[i]) {
			// NaN is the deliberate exception under "eq" but NOT under
			// op:same-key, which requires every NaN to be one key.
			t.Errorf("not reflexive: %s %q", vals[i].Type, vals[i].String())
		}
		for j := range vals {
			if same(vals[i], vals[j]) != same(vals[j], vals[i]) {
				t.Errorf("not symmetric: %s %q vs %s %q",
					vals[i].Type, vals[i].String(), vals[j].Type, vals[j].String())
			}
			if !same(vals[i], vals[j]) {
				continue
			}
			for k := range vals {
				if same(vals[j], vals[k]) && !same(vals[i], vals[k]) {
					t.Errorf("not transitive: %q ~ %q ~ %q but not %q ~ %q",
						vals[i].String(), vals[j].String(), vals[k].String(),
						vals[i].String(), vals[k].String())
				}
			}
		}
	}
}

// TestOracleModelsEveryCorpusType is the fail-closed contract stated directly,
// separately from any comparison with MapKeyOf.
//
// The tests above would catch an unmodelled type only on the pair that happens
// to contain it, in a message about that pair. This one names the type itself,
// once, and answers the question the finding actually asks: is there anything
// in the corpus the oracle has no op:same-key model for? It is also the test
// that fails first when someone adds a TypeCode to atomic.go without teaching
// oracleFamily its F&O equality category.
func TestOracleModelsEveryCorpusType(t *testing.T) {
	unmodelled := map[TypeCode]string{}
	for _, v := range sameKeyCorpus(t) {
		if _, ok := oracleFamilyOf(v); !ok {
			unmodelled[v.Type] = v.String()
		}
	}
	for typ, example := range unmodelled {
		t.Errorf("unsupported oracle representation: oracleFamily has no op:same-key "+
			"family for %s (example value %q).\nThe oracle fails closed, so every pair "+
			"containing this type is unjudged and the corpus proves nothing about it. "+
			"Add an explicit case to oracleFamily naming the type's F&O equality "+
			"category.", typ, example)
	}
}

// TestOracleFamilyIsExhaustive walks every TypeCode declared in atomic.go and
// requires a family for each, independently of what the corpus happens to
// contain. The corpus could shrink; the type set is the real obligation.
func TestOracleFamilyIsExhaustive(t *testing.T) {
	all := []TypeCode{
		TypeUntypedAtomic, TypeString, TypeBoolean, TypeDecimal, TypeInteger,
		TypeDouble, TypeFloat, TypeQName, TypeAnyURI, TypeDate, TypeTime,
		TypeDateTime, TypeDuration, TypeYearMonthDuration, TypeDayTimeDuration,
		TypeHexBinary, TypeBase64Binary, TypeGYear, TypeGYearMonth, TypeGMonth,
		TypeGMonthDay, TypeGDay,
	}
	// TypeGDay is the last code in the iota run; if a new one is appended,
	// this catches the omission even before oracleFamily is consulted.
	if int(TypeGDay) != len(all)-1 {
		t.Fatalf("the TypeCode run has %d codes but this test lists %d; "+
			"a type was added to atomic.go without being taught to the oracle",
			int(TypeGDay)+1, len(all))
	}
	for _, typ := range all {
		if _, ok := oracleFamily(typ); !ok {
			t.Errorf("unsupported oracle representation: %s has no op:same-key family", typ)
		}
	}
	// And the switch really does fail closed for something outside the run.
	if _, ok := oracleFamily(TypeCode(len(all) + 100)); ok {
		t.Error("oracleFamily invented a family for a type code that does not exist; " +
			"the switch has a guessing default arm again")
	}
}

// The float boundary constants are spelled out rather than imported from math
// so that the oracle keeps depending only on the value accessors.
func negZerof() float64 { z := zerof(); return -z }

func maxFloat64() float64 { return 1.7976931348623157e308 }

func smallestNonzeroFloat64() float64 { return 5e-324 }

func nan() float64 { var z float64; return z / z }
func inf(s int) float64 {
	if s < 0 {
		return -1 / zerof()
	}
	return 1 / zerof()
}
func zerof() float64 { var z float64; return z }
