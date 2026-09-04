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

// There is no scale past which a terminating value stops being rendered
// exactly.
//
// This test previously asserted the opposite: that a value past a 1024-digit
// bound was rendered at the 18-digit minimum. That was the bug written down as
// an expectation — 1/10^1034 does not need "the minimum", it needs 1034
// digits, and printing fewer states a different number. The bound is gone, so
// the assertion is inverted.
func TestDecimalBeyondFormerBound(t *testing.T) {
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(1034), nil)
	r := new(big.Rat).SetFrac(big.NewInt(1), den)
	got := formatDecimal(r)
	want := "0." + strings.Repeat("0", 1033) + "1"
	if got != want {
		t.Errorf("1/10^1034 rendered %d chars, want the full %d", len(got), len(want))
	}
	back, ok := new(big.Rat).SetString(got)
	if !ok || back.Cmp(r) != 0 {
		t.Error("rendered form does not equal the value it came from")
	}
}
