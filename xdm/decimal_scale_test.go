package xdm

import (
	"math/big"
	"strings"
	"testing"
)

// A terminating decimal renders in full, so its lexical form says what the
// value is.
//
// The scale was capped unconditionally at 18, which made the two disagree: a
// literal with 360 fractional digits printed as "0" while comparing unequal to
// zero. Whichever answer a caller trusted, the other contradicted it.
func TestDecimalRendersExactly(t *testing.T) {
	cases := []struct {
		num, den int64
		want     string
	}{
		{1995, 100, "19.95"},
		{1, 8, "0.125"},
		{-1, 4, "-0.25"},
		{3, 1, "3"},
	}
	for _, c := range cases {
		got := formatDecimal(new(big.Rat).SetFrac(
			big.NewInt(c.num), big.NewInt(c.den)))
		if got != c.want {
			t.Errorf("%d/%d = %q, want %q", c.num, c.den, got, c.want)
		}
	}

	// 1/10^360 — the case that exposed the disagreement.
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(360), nil)
	got := formatDecimal(new(big.Rat).SetFrac(big.NewInt(1), den))
	want := "0." + strings.Repeat("0", 359) + "1"
	if got != want {
		t.Errorf("1/10^360 rendered %d chars, want the full %d", len(got), len(want))
	}
}

// A value the lexical form can state exactly must compare equal to that form,
// which is the property that was broken.
func TestDecimalStringRoundTrips(t *testing.T) {
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(360), nil)
	r := new(big.Rat).SetFrac(big.NewInt(1), den)
	s := formatDecimal(r)
	back, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("rendered form %q does not parse back", s[:32]+"...")
	}
	if back.Cmp(r) != 0 {
		t.Error("rendered form does not equal the value it came from")
	}
}

// A non-terminating rational — 1/3 is the everyday case, arising from division
// — is rendered at the required minimum precision rather than looping.
func TestDecimalNonTerminating(t *testing.T) {
	got := formatDecimal(new(big.Rat).SetFrac(big.NewInt(1), big.NewInt(3)))
	if !strings.HasPrefix(got, "0.333333333333333333") {
		t.Errorf("1/3 = %q, want 18 digits of precision", got)
	}
	if len(got) > 32 {
		t.Errorf("1/3 rendered %d chars; it should stop at the minimum", len(got))
	}
}

// A denominator past the bound is rendered at the minimum too, so formatting
// cannot be made to allocate without limit.
func TestDecimalBeyondBound(t *testing.T) {
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(maxDecimalScale+10), nil)
	got := formatDecimal(new(big.Rat).SetFrac(big.NewInt(1), den))
	if len(got) > 32 {
		t.Errorf("a value past the bound rendered %d chars, want the minimum", len(got))
	}
}
