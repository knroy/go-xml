package xdm

import (
	"math/big"
	"strings"
	"testing"
)

// A decimal with N fraction digits must survive parse and re-serialise
// byte-identically, at every N.
//
// This is the property a scale ceiling breaks. The ceiling was 18, then 1024;
// each time, a value just past it rendered as a *different value* rather than
// as a refusal — 1/10^5000 printed "0" while comparing unequal to zero. The
// scale now follows the value, so there is no N at which the two disagree.
func TestDecimalRoundTripAtEveryScale(t *testing.T) {
	for _, n := range []int{0, 1, 18, 64, 1023, 1024, 1025, 2000, 5000, 10000} {
		var lit string
		if n == 0 {
			lit = "7"
		} else {
			// 1 followed by n-1 zeros then 1: exercises leading fraction
			// zeros and a significant last digit at the far end of the scale.
			lit = "0." + strings.Repeat("0", n-1) + "1"
		}
		r, ok := new(big.Rat).SetString(lit)
		if !ok {
			t.Fatalf("N=%d: literal did not parse", n)
		}
		got := formatDecimal(r)
		if got != lit {
			t.Errorf("N=%d: round trip lost the value (got %d chars, want %d)",
				n, len(got), len(lit))
			continue
		}
		// The rendered form must also parse back to the same value, not just
		// look right.
		back, ok := new(big.Rat).SetString(got)
		if !ok || back.Cmp(r) != 0 {
			t.Errorf("N=%d: rendered form does not equal its source value", n)
		}
	}
}

// The shapes that a scale-derived renderer is most likely to get wrong.
func TestDecimalEdgeShapes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1.500", "1.5"},     // trailing zeros are not canonical
		{"0.0001", "0.0001"}, // leading fraction zeros are significant
		{"0", "0"},
		{"-0.0", "0"},
		{"-0.25", "-0.25"},
		{"19.95", "19.95"},
	}
	for _, c := range cases {
		r, ok := new(big.Rat).SetString(c.in)
		if !ok {
			t.Fatalf("%q did not parse", c.in)
		}
		if got := formatDecimal(r); got != c.want {
			t.Errorf("%q rendered %q, want %q", c.in, got, c.want)
		}
	}

	// A very large integer is exact too; the scale logic must not touch it.
	big10k := new(big.Int).Exp(big.NewInt(10), big.NewInt(5000), nil)
	want := "1" + strings.Repeat("0", 5000)
	if got := formatDecimal(new(big.Rat).SetInt(big10k)); got != want {
		t.Errorf("10^5000 rendered %d chars, want %d", len(got), len(want))
	}
	// And its negation.
	if got := formatDecimal(new(big.Rat).SetInt(new(big.Int).Neg(big10k))); got != "-"+want {
		t.Errorf("-10^5000 rendered %d chars, want %d", len(got), len(want)+1)
	}
}

// A rational with no finite decimal expansion has no exact lexical form, so
// it gets a precision policy rather than an exact rendering. That is a
// different case from a terminating value and must stay bounded.
func TestDecimalNonTerminatingStaysBounded(t *testing.T) {
	cases := []struct{ num, den int64 }{
		{1, 3}, {2, 7}, {1, 11},
	}
	for _, c := range cases {
		got := formatDecimal(new(big.Rat).SetFrac(big.NewInt(c.num), big.NewInt(c.den)))
		if len(got) > 32 {
			t.Errorf("%d/%d rendered %d chars; a non-terminating value must stop",
				c.num, c.den, len(got))
		}
	}
	// A denominator carrying a huge power of two *and* a factor of seven is
	// still non-terminating: the 2s must not be mistaken for termination.
	den := new(big.Int).Mul(
		new(big.Int).Exp(big.NewInt(2), big.NewInt(4000), nil), big.NewInt(7))
	if got := formatDecimal(new(big.Rat).SetFrac(big.NewInt(1), den)); len(got) > 32 {
		t.Errorf("1/(2^4000*7) rendered %d chars; it does not terminate", len(got))
	}
}

// decimalScale must classify termination correctly, including the mixed case
// where the two exponents differ and the larger one wins.
func TestDecimalScaleClassification(t *testing.T) {
	mixed := new(big.Int).Mul(
		new(big.Int).Exp(big.NewInt(2), big.NewInt(300), nil),
		new(big.Int).Exp(big.NewInt(5), big.NewInt(700), nil))
	n, term := decimalScale(new(big.Rat).SetFrac(big.NewInt(1), mixed))
	if !term || n != 700 {
		t.Errorf("1/(2^300*5^700): scale=%d terminating=%v, want 700 true", n, term)
	}
	if _, term := decimalScale(new(big.Rat).SetFrac(big.NewInt(1), big.NewInt(3))); term {
		t.Error("1/3 reported as terminating")
	}
}
