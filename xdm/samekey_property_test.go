package xdm

import (
	"encoding/base64"
	"math/big"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

// The generated property lane for op:same-key.
//
// samekey_oracle_test.go already compares MapKeyOf against the SameKey oracle
// over a FIXED, hand-enumerated corpus, exhaustively: every ordered pair and
// every ordered triple of it. That exhaustiveness is the corpus's strength and
// this file does not try to improve on it. Randomising over the same values
// would only sample what the product already covers completely.
//
// What a fixed corpus cannot do is reach a value class nobody thought to
// enumerate. The classes below are the ones the enumeration misses, and each
// is generated rather than listed because the interesting part is the
// COMBINATION -- a negative zero at float32 width, a timezone offset that
// crosses a year boundary, a duration spelled in months against the same value
// spelled in years -- and the combinations outnumber what anyone writes by
// hand.
//
// Determinism is a requirement, not a nicety. A property test that cannot be
// re-run on the input that failed is a bug report nobody can act on, so:
//
//   - the seed is a fixed constant, so CI is reproducible run to run;
//   - GOXSLT_SAMEKEY_SEED overrides it, to widen the search deliberately;
//   - the seed is PRINTED on any failure, and under -v on success, so a red
//     run always carries the one fact needed to reproduce it.
//
// The properties asserted are the three that any canonical key must satisfy
// (reflexivity, symmetry, transitivity), plus agreement with the independent
// oracle, plus the map behaviour the key exists to support: a generated value
// must insert, be found again, replace itself rather than duplicate, and
// remove cleanly.

// samekeyPropertySeed is the default seed. Fixed so that CI runs the same
// values every time and a regression is reproducible without a recorded seed.
const samekeyPropertySeed = 20260911

// propertySeed reports the seed this run uses and where it came from.
//
// The value is returned rather than logged here so that every failure message
// in the lane can carry it: a seed printed only at the top of a verbose log is
// not attached to the failure when the log is truncated to the failing line.
func propertySeed(t *testing.T) int64 {
	t.Helper()
	if s := os.Getenv("GOXSLT_SAMEKEY_SEED"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("GOXSLT_SAMEKEY_SEED=%q is not an integer: %v", s, err)
		}
		return n
	}
	return samekeyPropertySeed
}

// binarySpellCounter cycles the binary class through the spellings of one
// octet sequence. It is reset at the top of every generateValues call, so a
// run is reproducible from its seed alone and does not depend on which tests
// ran before it.
var binarySpellCounter int

// genValue is a generated value together with the class it came from.
//
// The class is carried so that a failure names the RULE that broke rather than
// only the value, and so that TestSameKeyGeneratorReachesEveryClass can assert
// the generator actually produces each one. A generator that silently stopped
// emitting a class would leave the lane green while covering less -- which is
// the failure mode the sabotage check at the end of this file exists to catch.
type genValue struct {
	class string
	val   *Atomic
}

// generatedClasses are the value classes the fixed corpus does not enumerate.
//
// Each entry returns one value per call, drawn from rnd. They are deliberately
// small generators over a wide space rather than wide generators over a small
// one: the point is to reach combinations, not to produce novelty.
var generatedClasses = []struct {
	name string
	gen  func(rnd *rand.Rand) *Atomic
}{
	// Negative zero. +0 and -0 are one value under op:same-key -- the numeric
	// branch keys on the exact rational, where the sign of zero does not
	// survive -- but they print differently, so an encoder that went through
	// the lexical form would split them. Generated at both float widths and
	// through the integer and decimal spellings, which the fixed corpus covers
	// only as two hand-written doubles.
	// The zero spellings all key alike ON PURPOSE, so the class also emits
	// small nonzero values: a class whose every value is one key cannot
	// witness a SPLIT, only a merge, and the generator guard below rightly
	// treats a single-key class as a constant.
	{"negative-zero", func(rnd *rand.Rand) *Atomic {
		z := zerof()
		switch rnd.Intn(6) {
		case 0:
			return NewDouble(z)
		case 1:
			return NewDouble(-z)
		case 2:
			return NewFloat(-z)
		case 3:
			return NewInteger(0)
		case 4:
			// The neighbours of zero at both float widths, so a pair exists
			// that must NOT collide with it.
			return NewDouble(smallestNonzeroFloat64())
		default:
			return NewDouble(-smallestNonzeroFloat64())
		}
	}},

	// hexBinary and base64Binary. The value is the OCTET SEQUENCE, not the
	// spelling: hex is case-insensitive and base64 admits internal whitespace.
	// The generator emits the same octets in several spellings so that an
	// encoder keying on the lexical form is contradicted by a pair, and emits
	// both types over EQUAL octets so that a key dropping the type is caught
	// too.
	// The octets are drawn from a SMALL pool and each is then spelled in a
	// deterministic set of ways -- lower, upper, and mixed for hex; with and
	// without internal whitespace for base64. Drawing a fresh random value per
	// call, as an earlier version did, essentially never produced the same
	// octets twice in two different spellings, so no pair could witness a
	// case-sensitive key and the sabotage for it stayed green. A class that
	// must catch a SPLIT has to generate equal values that are spelled
	// differently, which means correlating the draws rather than independent
	// sampling.
	// The octet sequence is drawn from a small pool, and the SPELLING is then
	// chosen by a counter rather than by rnd, so that successive draws of one
	// pool entry walk every spelling of it in turn. Sampling the style
	// randomly was not enough: over a class's draws each octet sequence
	// reached exactly one spelling, so no same-octets/different-case pair ever
	// existed and the case-folding sabotage stayed green while the class
	// looked well covered. A class that must catch a SPLIT needs the equal
	// values to be spelled differently by construction, not by luck.
	//
	// Only pool entries containing a-f can differ under case at all, which is
	// why they dominate the pool.
	// Both the octet sequence and its spelling are derived from a COUNTER, not
	// from rnd. Draw k emits pool entry k/4 in spelling k%4, so all four
	// spellings of one sequence -- lower hex, upper hex, alternating hex, and
	// base64 -- are emitted consecutively and an equal-value/different-
	// spelling pair is guaranteed to exist.
	//
	// Two earlier versions sampled the spelling randomly and both failed the
	// same way: over a class's draws each octet sequence reached exactly one
	// spelling, so no pair could witness a case-sensitive key and the
	// case-folding sabotage stayed green while the class looked well covered.
	// A class that must catch a SPLIT has to emit the equal values in
	// different spellings by construction. rnd is deliberately unused here;
	// the class is a systematic enumeration, and the seed still selects which
	// values the other classes pair it against.
	{"binary", func(rnd *rand.Rand) *Atomic {
		pool := [][]byte{
			{0x0f}, {0xde, 0xad, 0xbe, 0xef}, {0xff}, {0xa0, 0x0b},
			{0x00}, {0x01, 0x02, 0x03},
		}
		k := binarySpellCounter
		binarySpellCounter++
		oct := pool[(k/4)%len(pool)]
		style := k % 4
		if style == 3 {
			// The same octets in base64, with the internal whitespace
			// §3.2.16 permits between characters.
			s := base64.StdEncoding.EncodeToString(oct)
			if len(s) > 2 {
				s = s[:2] + " " + s[2:]
			}
			return NewBinary(s, TypeBase64Binary)
		}
		const hexd = "0123456789abcdef"
		const hexu = "0123456789ABCDEF"
		s := ""
		for i, b := range oct {
			up := style == 1 || (style == 2 && i%2 == 0)
			tab := hexd
			if up {
				tab = hexu
			}
			s += string(tab[b>>4]) + string(tab[b&0xf])
		}
		return NewBinary(s, TypeHexBinary)
	}},

	// Large exact integers. Past 2^53 a float64 stops distinguishing adjacent
	// integers, so an encoding that routed through Float64 would merge values
	// that op:same-key holds apart. The fixed corpus has five such values; the
	// generator reaches the whole neighbourhood, including past int64 where
	// the value has to be built from a rational.
	{"large-integer", func(rnd *rand.Rand) *Atomic {
		switch rnd.Intn(3) {
		case 0:
			return NewInteger(1<<53 + int64(rnd.Intn(8)))
		case 1:
			return NewInteger(1<<62 + int64(rnd.Intn(8)))
		default:
			r, ok := new(big.Rat).SetString("9223372036854775808")
			if !ok {
				return nil
			}
			r.Add(r, new(big.Rat).SetInt64(int64(rnd.Intn(8))))
			return NewIntegerFromRat(r)
		}
	}},

	// Timezone boundary crossings. A zoned calendar value keys on the INSTANT,
	// so 2001-01-01T00:30:00+01:00 and 2000-12-31T23:30:00Z are one key across
	// a day, month and year boundary at once. The generator picks offsets that
	// push a near-midnight time over an edge, which is where a key built from
	// the calendar fields rather than the instant goes wrong.
	{"timezone-boundary", func(rnd *rand.Rand) *Atomic {
		bases := []string{
			"2001-01-01T00:30:00", "2000-12-31T23:30:00",
			"2000-02-29T23:30:00", "2000-03-01T00:30:00",
			"2015-04-08T00:15:00", "2015-04-07T23:45:00",
		}
		offs := []string{"", "Z", "+00:00", "-05:00", "+05:30", "+14:00", "-14:00"}
		s := bases[rnd.Intn(len(bases))] + offs[rnd.Intn(len(offs))]
		dt, err := ParseDateTime(s, TypeDateTime)
		if err != nil {
			return nil
		}
		return NewDateTime(dt, TypeDateTime)
	}},

	// Decimal / float / double spellings of one exact value, and of values
	// that merely round together. The four numerics are ONE family, so
	// xs:integer(1) and xs:double(1) are one key -- but an xs:decimal that
	// only rounds to the same double is a DIFFERENT key (map-put-023). Both
	// sides are generated, since a corpus with only the first is satisfied by
	// an encoder that merges everything numeric.
	{"numeric-alias", func(rnd *rand.Rand) *Atomic {
		whole := int64(rnd.Intn(9) - 4)
		switch rnd.Intn(5) {
		case 0:
			return NewInteger(whole)
		case 1:
			return NewDouble(float64(whole))
		case 2:
			return NewFloat(float64(whole))
		case 3:
			return NewDecimal(new(big.Rat).SetInt64(whole))
		default:
			// A value with a surviving fractional part, where the
			// float32/float64 seam separates the float from the double.
			r := new(big.Rat).SetFrac64(whole*10+1, 10)
			if rnd.Intn(2) == 0 {
				return NewDecimal(r)
			}
			f, _ := r.Float64()
			if rnd.Intn(2) == 0 {
				return NewFloat(f)
			}
			return NewDouble(f)
		}
	}},

	// Duration aliases. The three duration types are one family compared over
	// (months, seconds), so P1Y and P12M are one key and the type is not part
	// of it. The generator spells one month-count several ways and across all
	// three types, including negatives, where a dropped sign merges a duration
	// with its own negation.
	// One underlying duration, spelled several ways across the three types.
	// The alias pairs are the point -- P1Y against P12M, P1D against PT24H --
	// and they only exist if the SAME value is emitted in more than one
	// spelling. An earlier version drew the components and the type
	// independently, so a matching pair appeared only by coincidence and the
	// sabotage that keyed a duration on its spelling stayed green.
	{"duration-alias", func(rnd *rand.Rand) *Atomic {
		neg := ""
		if rnd.Intn(2) == 0 {
			neg = "-"
		}
		var s string
		var typ TypeCode
		if rnd.Intn(2) == 0 {
			// Month side: n years is 12n months, spelled three ways.
			n := rnd.Intn(3) // whole years, so the alias is exact
			switch rnd.Intn(3) {
			case 0:
				s, typ = neg+"P"+strconv.Itoa(n)+"Y", TypeYearMonthDuration
			case 1:
				s, typ = neg+"P"+strconv.Itoa(n*12)+"M", TypeYearMonthDuration
			default:
				s, typ = neg+"P"+strconv.Itoa(n)+"Y", TypeDuration
			}
		} else {
			// Day/time side: n days is 24n hours is 1440n minutes.
			n := 1 + rnd.Intn(3)
			switch rnd.Intn(3) {
			case 0:
				s, typ = neg+"P"+strconv.Itoa(n)+"D", TypeDayTimeDuration
			case 1:
				s, typ = neg+"PT"+strconv.Itoa(24*n)+"H", TypeDayTimeDuration
			default:
				s, typ = neg+"PT"+strconv.Itoa(1440*n)+"M", TypeDayTimeDuration
			}
		}
		d, err := ParseDuration(s, typ)
		if err != nil {
			return nil
		}
		return NewDuration(d, typ)
	}},

	// QName namespace differences. The key is the (URI, local) pair and the
	// prefix is not part of it, so two QNames differing only in namespace must
	// not collide however similar their local names are. The fixed corpus has
	// three QNames; the generator crosses a small URI set with a small local
	// set so that every same-local/different-URI pair appears.
	{"qname-namespace", func(rnd *rand.Rand) *Atomic {
		uris := []string{"", "u", "v", "http://example.com/n"}
		locals := []string{"a", "b", "local"}
		return NewQNameValue(QName{
			URI:   uris[rnd.Intn(len(uris))],
			Local: locals[rnd.Intn(len(locals))],
		})
	}},

	// Malformed internal representations. NewBinary stores its argument
	// verbatim and does not validate, so a value whose lexical form will not
	// decode is reachable -- from a document, this is what node.go builds
	// straight from element text. MapKeyOf must not panic on one, and the
	// oracle must REFUSE to judge it rather than guess: SameKey returns
	// ok=false for a binary with no octets. The lane below treats an unjudged
	// pair as a skip for that pair rather than a verdict, which is the whole
	// reason this class is safe to generate.
	{"malformed-binary", func(rnd *rand.Rand) *Atomic {
		bad := []string{"0", "zz", "0g", "!!!!", "A", "==="}
		s := bad[rnd.Intn(len(bad))]
		if rnd.Intn(2) == 0 {
			return NewBinary(s, TypeHexBinary)
		}
		return NewBinary(s, TypeBase64Binary)
	}},
}

// generateValues draws n values per class from a seeded source.
//
// Every class contributes, so the returned slice is a spread across all of
// them rather than a random walk that might starve one. A generator returning
// nil (a spelling that would not parse) is skipped rather than retried, since
// a class that cannot produce anything is caught by
// TestSameKeyGeneratorReachesEveryClass rather than hidden by a retry loop.
func generateValues(rnd *rand.Rand, perClass int) []genValue {
	binarySpellCounter = 0
	var out []genValue
	for _, c := range generatedClasses {
		for i := 0; i < perClass; i++ {
			if v := c.gen(rnd); v != nil {
				out = append(out, genValue{class: c.name, val: v})
			}
		}
	}
	return out
}

// TestSameKeyGeneratorReachesEveryClass is the guard on the lane itself.
//
// A property test is only as good as the values it reaches, and a generator
// that quietly stopped producing a class would leave every property below
// green while covering strictly less. This asserts each class yields values,
// and that they are not all the same value -- a class collapsed to a constant
// satisfies "yields values" while testing nothing.
func TestSameKeyGeneratorReachesEveryClass(t *testing.T) {
	seed := propertySeed(t)
	rnd := rand.New(rand.NewSource(seed))
	vals := generateValues(rnd, 40)

	seen := map[string]map[string]bool{}
	for _, gv := range vals {
		if seen[gv.class] == nil {
			seen[gv.class] = map[string]bool{}
		}
		k, err := MapKeyOf(gv.val)
		if err != nil {
			t.Fatalf("seed %d: MapKeyOf(%s %q) from class %q: %v",
				seed, gv.val.Type, gv.val.String(), gv.class, err)
		}
		seen[gv.class][k] = true
	}
	for _, c := range generatedClasses {
		switch n := len(seen[c.name]); {
		case n == 0:
			t.Errorf("seed %d: class %q generated no values at all; the lane "+
				"reports on a class it never reaches", seed, c.name)
		case n < 2:
			t.Errorf("seed %d: class %q collapsed to a single key; it is a "+
				"constant, not a generator, and no pair from it can witness "+
				"anything", seed, c.name)
		}
	}
	if t.Failed() || testing.Verbose() {
		t.Logf("seed %d: %d values over %d classes", seed, len(vals), len(generatedClasses))
	}
}

// TestSameKeyPropertiesOverGeneratedValues is the lane proper.
//
// It asserts, over generated values, the three properties any canonical key
// must have, and agreement with the independent oracle on every pair. The
// oracle is the one in samekey_oracle_test.go, reused rather than rewritten:
// a second oracle written here would be a second chance to make the same
// mistake, and the fail-closed contract is already stated there.
//
// A pair the oracle cannot judge (a malformed binary, by construction) is
// SKIPPED rather than counted either way. That is not the fail-open tail the
// oracle's comments warn about: an unjudged pair contributes no verdict to any
// property, and the count of skips is reported so that a lane which skipped
// everything cannot look like a lane that passed.
func TestSameKeyPropertiesOverGeneratedValues(t *testing.T) {
	seed := propertySeed(t)
	rnd := rand.New(rand.NewSource(seed))
	vals := generateValues(rnd, 12)

	keys := make([]string, len(vals))
	for i, gv := range vals {
		k, err := MapKeyOf(gv.val)
		if err != nil {
			t.Fatalf("seed %d: MapKeyOf(%s %q) from class %q: %v",
				seed, gv.val.Type, gv.val.String(), gv.class, err)
		}
		keys[i] = k
	}

	// same is the oracle with the fail-closed contract applied: judged is
	// false when the oracle has no opinion, and the caller must then skip.
	same := func(i, j int) (verdict bool, judged bool) {
		return SameKey(vals[i].val, vals[j].val)
	}

	// describe names a value for a failure message, with its class, so the
	// message says which rule to look at and not merely which bytes differed.
	describe := func(i int) string {
		return vals[i].class + " " + vals[i].val.Type.String() +
			" " + strconv.Quote(vals[i].val.String()) + " key " + strconv.Quote(keys[i])
	}

	skipped, bad := 0, 0
	for i := range vals {
		// Reflexivity. Every value is its own key, NaN included: op:same-key
		// carves NaN out of "eq" precisely so that a NaN finds its own entry.
		if v, judged := same(i, i); judged && !v {
			t.Errorf("seed %d: not reflexive: %s\n"+
				"  a value that is not the same key as itself cannot be "+
				"looked up in a map it was just inserted into.\n"+
				"  reproduce with GOXSLT_SAMEKEY_SEED=%d", seed, describe(i), seed)
		}
		for j := range vals {
			wantIJ, judgedIJ := same(i, j)
			if !judgedIJ {
				skipped++
				continue
			}
			// Symmetry of the oracle itself.
			if wantJI, judgedJI := same(j, i); judgedJI && wantIJ != wantJI {
				t.Errorf("seed %d: not symmetric:\n  a = %s\n  b = %s\n"+
					"  SameKey(a,b) = %v but SameKey(b,a) = %v\n"+
					"  reproduce with GOXSLT_SAMEKEY_SEED=%d",
					seed, describe(i), describe(j), wantIJ, wantJI, seed)
			}
			// Agreement with the encoding, in both directions. One direction
			// alone is vacuous -- see the oracle's commentary.
			if got := keys[i] == keys[j]; wantIJ != got {
				bad++
				if bad <= 10 {
					t.Errorf("seed %d: op:same-key disagrees with the canonical key:\n"+
						"  a = %s\n  b = %s\n  SameKey = %v, keys equal = %v\n"+
						"  reproduce with GOXSLT_SAMEKEY_SEED=%d",
						seed, describe(i), describe(j), wantIJ, got, seed)
				}
			}
			if !wantIJ {
				continue
			}
			// Transitivity, over the values that are already related.
			for k := range vals {
				wantJK, judgedJK := same(j, k)
				if !judgedJK || !wantJK {
					continue
				}
				wantIK, judgedIK := same(i, k)
				if judgedIK && !wantIK {
					t.Errorf("seed %d: not transitive:\n  a = %s\n  b = %s\n  c = %s\n"+
						"  a~b and b~c but not a~c; no canonical key can implement this\n"+
						"  reproduce with GOXSLT_SAMEKEY_SEED=%d",
						seed, describe(i), describe(j), describe(k), seed)
				}
			}
		}
	}
	if bad > 10 {
		t.Errorf("seed %d: ... and %d further disagreeing pairs", seed, bad-10)
	}
	// A lane that skipped every pair would otherwise report success.
	if judged := len(vals)*len(vals) - skipped; judged < len(vals) {
		t.Fatalf("seed %d: only %d of %d pairs were judged; the oracle refused "+
			"almost everything and this run measured nothing",
			seed, judged, len(vals)*len(vals))
	}
	if t.Failed() || testing.Verbose() {
		t.Logf("seed %d: %d values, %d ordered pairs, %d unjudged (malformed by construction)",
			seed, len(vals), len(vals)*len(vals), skipped)
	}
}

// TestGeneratedValuesRoundTripThroughAMap exercises the operations the key
// exists to support, over the generated values.
//
// Agreement with the oracle is a statement about strings. This is the
// statement about behaviour: a value must insert, be found again under an
// equal-but-differently-spelled value, replace rather than duplicate, and
// remove cleanly. A key can satisfy the string property and still be wrong
// here if the map layer mishandles it, which is why this is a separate test
// rather than an assertion folded into the one above.
func TestGeneratedValuesRoundTripThroughAMap(t *testing.T) {
	seed := propertySeed(t)
	rnd := rand.New(rand.NewSource(seed))
	vals := generateValues(rnd, 10)

	for _, gv := range vals {
		v := gv.val
		where := gv.class + " " + v.Type.String() + " " + strconv.Quote(v.String())

		// Insertion, then lookup of the value itself.
		m, err := NewMap().Put(v, One(NewString("first")))
		if err != nil {
			t.Fatalf("seed %d: Put(%s): %v\n  reproduce with GOXSLT_SAMEKEY_SEED=%d",
				seed, where, err, seed)
		}
		if m.Len() != 1 {
			t.Errorf("seed %d: after one Put of %s the map holds %d entries, want 1\n"+
				"  reproduce with GOXSLT_SAMEKEY_SEED=%d", seed, where, m.Len(), seed)
		}
		got, ok, err := m.Get(v)
		if err != nil || !ok {
			t.Errorf("seed %d: %s was inserted and then not found (ok=%v, err=%v); "+
				"a value must find its own entry\n  reproduce with GOXSLT_SAMEKEY_SEED=%d",
				seed, where, ok, err, seed)
		} else if len(got) != 1 {
			t.Errorf("seed %d: %s looked up to %d items, want 1", seed, where, len(got))
		}

		// Replacement: putting the same key again must overwrite, not append.
		m2, err := m.Put(v, One(NewString("second")))
		if err != nil {
			t.Fatalf("seed %d: replacing Put(%s): %v", seed, where, err)
		}
		if m2.Len() != 1 {
			t.Errorf("seed %d: re-putting %s gave %d entries, want 1; the key "+
				"did not match itself on replacement\n  reproduce with GOXSLT_SAMEKEY_SEED=%d",
				seed, where, m2.Len(), seed)
		}

		// Removal.
		m3, err := m2.Remove(v)
		if err != nil {
			t.Fatalf("seed %d: Remove(%s): %v", seed, where, err)
		}
		if m3.Len() != 0 {
			t.Errorf("seed %d: removing %s left %d entries, want 0\n"+
				"  reproduce with GOXSLT_SAMEKEY_SEED=%d", seed, where, m3.Len(), seed)
		}
		// The original must be untouched: maps are immutable in the data model.
		if m.Len() != 1 {
			t.Errorf("seed %d: the original map changed when a derived one was "+
				"modified (%s)", seed, where)
		}
	}

	// Values the oracle says are one key must share ONE map entry, and values
	// it says are distinct must occupy two. This is the map-level restatement
	// of the agreement property, and it is what a user actually observes.
	for i := range vals {
		for j := range vals {
			want, judged := SameKey(vals[i].val, vals[j].val)
			if !judged {
				continue
			}
			m, err := NewMap().Put(vals[i].val, One(NewInteger(1)))
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			m, err = m.Put(vals[j].val, One(NewInteger(2)))
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			wantLen := 2
			if want {
				wantLen = 1
			}
			if m.Len() != wantLen {
				t.Errorf("seed %d: a two-key map built from\n  a = %s %s %q\n  b = %s %s %q\n"+
					"  holds %d entries, want %d (op:same-key says same=%v)\n"+
					"  reproduce with GOXSLT_SAMEKEY_SEED=%d",
					seed,
					vals[i].class, vals[i].val.Type, vals[i].val.String(),
					vals[j].class, vals[j].val.Type, vals[j].val.String(),
					m.Len(), wantLen, want, seed)
			}
		}
	}
	if t.Failed() || testing.Verbose() {
		t.Logf("seed %d: %d values round-tripped through insert/lookup/replace/remove",
			seed, len(vals))
	}
}
