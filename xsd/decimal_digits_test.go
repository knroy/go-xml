package xsd

import (
	"math/big"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

func digitsRat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("SetString(%q) failed", s)
	}
	return r
}

// The leading zeros of a pure fraction count toward totalDigits: 0.001 is
// total=3 frac=3, not total=1 frac=3. This is not an accident to be tidied
// away. XSD Part 2 §4.3.12.4 requires fractionDigits <= totalDigits — this
// repo enforces it in facet_check.go — so a total of 1 with a fraction of 3
// would make 0.001 unrepresentable by any conforming schema. Do not "fix" it.
//
// TestCountDigits in facet_test.go covers the general table; this pins the
// specific claim, at two scales, against a future "simplification".
func TestCountDigitsLeadingZerosAreSignificant(t *testing.T) {
	cases := []struct {
		in          string
		total, frac uint64
	}{
		{"0.001", 3, 3},
		{"0.000001", 6, 6},
		{"-0.000001", 6, 6},
	}
	for _, c := range cases {
		total, frac := countDigits(c.in)
		if total != c.total || frac != c.frac {
			t.Errorf("countDigits(%s) = %d,%d; want %d,%d (§4.3.12.4)",
				c.in, total, frac, c.total, c.frac)
		}
		if frac > total {
			t.Errorf("countDigits(%s): fractionDigits %d > totalDigits %d "+
				"violates XSD Part 2 §4.3.12.4", c.in, frac, total)
		}
	}
}

// A fraction longer than any fixed bound must report its true length: an
// earlier implementation stopped counting at a bound and so let a value pass a
// fractionDigits facet it violates.
func TestCountDigitsLargeScale(t *testing.T) {
	total, frac := countDigits("0." + strings.Repeat("0", 4999) + "1")
	if total != 5000 || frac != 5000 {
		t.Errorf("10^-5000: %d,%d; want 5000,5000", total, frac)
	}
	total, frac = countDigits("1" + strings.Repeat("0", 5000))
	if total != 5001 || frac != 0 {
		t.Errorf("10^5000: %d,%d; want 5001,0", total, frac)
	}
}

// §4.3.12.4 as a live decision, not just a count: a fractionDigits facet must
// see 0.001 as three fraction digits, and a totalDigits of 3 must admit it.
// With a totalDigits of 1 the value would be unrepresentable, which is exactly
// why countDigits counts the leading zeros.
func TestFractionDigitsFacetOnLeadingZeros(t *testing.T) {
	n := func(u uint64) *uint64 { return &u }
	for _, c := range []struct {
		total, frac *uint64
		valid       bool
	}{
		{n(3), n(3), true},
		{n(2), n(3), false}, // only two total digits: 0.001 does not fit
		{nil, n(2), false},  // three fraction digits exceeds two
		{n(1), nil, false},  // a totalDigits of 1 cannot hold 0.001
	} {
		steps := []facetStep{{typ: &SimpleType{}, facets: &FacetSet{
			TotalDigits: c.total, FractionDigits: c.frac}}}
		err := checkDigitFacets(steps, "0.001")
		if (err == nil) != c.valid {
			t.Errorf("0.001 against total=%v frac=%v: err=%v, want valid=%v",
				c.total, c.frac, err, c.valid)
		}
	}
}

// countDigitsRat is the digit count as it was computed before countDigits read
// it off the literal: parse to a big.Rat, reduce to a coefficient and a scale,
// and widen the total to the scale. It is kept as the reference the lexical
// count is checked against.
func countDigitsRat(v *big.Rat) (total, frac uint64, ok bool) {
	if v.Sign() == 0 {
		return 1, 0, true
	}
	m, ok := xdm.DecimalMagnitudeOf(v)
	if !ok {
		return 0, 0, false
	}
	digits := uint64(len(m.Coefficient.String()))
	if digits < uint64(m.Scale) {
		digits = uint64(m.Scale)
	}
	return digits, uint64(m.Scale), true
}

// TestCountDigitsMatchesRational checks the lexical count against the rational
// one over every literal built from a set of awkward parts: signs, empty and
// all-zero integer parts, leading zeros on either side, trailing zeros, a bare
// point at either end. Any literal the decimal grammar admits must count the
// same both ways.
func TestCountDigitsMatchesRational(t *testing.T) {
	ints := []string{"", "0", "00", "1", "10", "007", "120", "1000000000000000000000"}
	fracs := []string{"", "0", "00", "5", "50", "05", "001", "100", "0000000000000000000001"}
	n := 0
	for _, sign := range []string{"", "+", "-"} {
		for _, ip := range ints {
			for _, point := range []string{"", "."} {
				for _, fp := range fracs {
					if point == "" && fp != "" {
						continue
					}
					lex := sign + ip + point + fp
					if !isDecimalLexical(lex) {
						continue
					}
					n++
					wantTotal, wantFrac, ok := countDigitsRat(digitsRat(t, lex))
					if !ok {
						t.Fatalf("%q: no terminating expansion", lex)
					}
					total, frac := countDigits(lex)
					if total != wantTotal || frac != wantFrac {
						t.Errorf("countDigits(%q) = %d,%d; the value has %d,%d",
							lex, total, frac, wantTotal, wantFrac)
					}
				}
			}
		}
	}
	if n < 200 {
		t.Fatalf("only %d literals generated", n)
	}
}

// TestDigitFacetsAllocateNothing pins the reason countDigits reads the literal:
// checking a digit facet allocates nothing, where the rational allocated for
// the parse and again for the coefficient.
func TestDigitFacetsAllocateNothing(t *testing.T) {
	four, two := uint64(4), uint64(2)
	steps := []facetStep{{typ: &SimpleType{}, facets: &FacetSet{
		TotalDigits: &four, FractionDigits: &two}}}
	allocs := testing.AllocsPerRun(100, func() {
		if err := checkDigitFacets(steps, "-0012.3400"); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("checkDigitFacets allocated %v times, want 0", allocs)
	}
}
