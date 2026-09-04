package xsd

import (
	"math/big"
	"testing"
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
		total, frac, ok := countDigits(digitsRat(t, c.in))
		if !ok || total != c.total || frac != c.frac {
			t.Errorf("countDigits(%s) = %d,%d,%v; want %d,%d,true (§4.3.12.4)",
				c.in, total, frac, ok, c.total, c.frac)
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
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(5000), nil)
	small := new(big.Rat).SetFrac(big.NewInt(1), pow)
	total, frac, ok := countDigits(small)
	if !ok || total != 5000 || frac != 5000 {
		t.Errorf("10^-5000: %d,%d,%v; want 5000,5000,true", total, frac, ok)
	}
	large := new(big.Rat).SetInt(pow)
	total, frac, ok = countDigits(large)
	if !ok || total != 5001 || frac != 0 {
		t.Errorf("10^5000: %d,%d,%v; want 5001,0,true", total, frac, ok)
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
		err := checkDigitFacets(steps, digitsRat(t, "0.001"))
		if (err == nil) != c.valid {
			t.Errorf("0.001 against total=%v frac=%v: err=%v, want valid=%v",
				c.total, c.frac, err, c.valid)
		}
	}
}
