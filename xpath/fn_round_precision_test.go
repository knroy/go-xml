package xpath

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func evalRatForRound(t *testing.T, expr string) *big.Rat {
	t.Helper()
	s, err := Eval(expr, NewContext(nil, Builtins()), nil)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	if len(s) != 1 {
		t.Fatalf("%s: returned %d items", expr, len(s))
	}
	a, ok := s[0].(*xdm.Atomic)
	if !ok {
		t.Fatalf("%s: not an atomic value", expr)
	}
	return a.Rat()
}

// Rounding to at least as many places as a value's own scale cannot change it.
// A fixed ceiling on the precision argument broke that: a value with 5000
// fraction digits rounded to 5000 places came back as zero, because the
// precision was silently reduced to 4096 first and every digit was discarded.
//
// The bound has to come from the value being rounded, not from a constant.
func TestRoundPrecisionAboveScaleIsIdentity(t *testing.T) {
	// 10^-5000, written as a literal: 5000 fraction digits.
	small := "0." + strings.Repeat("0", 4999) + "1"
	in := evalRatForRound(t, small)
	if in.Sign() == 0 {
		t.Fatal("the literal itself lost its value; this test cannot say anything")
	}

	for _, places := range []int{5000, 5001, 6000, 100000} {
		got := evalRatForRound(t, fmt.Sprintf("round-half-to-even(%s, %d)", small, places))
		if got.Cmp(in) != 0 {
			t.Errorf("round-half-to-even(10^-5000, %d) changed the value "+
				"(zero=%v); rounding at or above a value's scale is the identity",
				places, got.Sign() == 0)
		}
	}
}

// Below the value's scale the rounding is real and must still happen. These
// straddle where the old ceiling sat, so a reintroduced clamp cannot pass by
// making everything the identity.
func TestRoundPrecisionBelowScaleStillRounds(t *testing.T) {
	small := "0." + strings.Repeat("0", 4999) + "1"
	for _, places := range []int{0, 1, 4095, 4096, 4097, 4999} {
		got := evalRatForRound(t, fmt.Sprintf("round-half-to-even(%s, %d)", small, places))
		if got.Sign() != 0 {
			t.Errorf("round-half-to-even(10^-5000, %d) = nonzero, want 0: "+
				"every significant digit is below this precision", places)
		}
	}
}

// A value whose scale is well below the precision, at precisions on both sides
// of the old ceiling. All of these are the identity.
func TestRoundPrecisionSmallScale(t *testing.T) {
	want := big.NewRat(1234, 1000)
	for _, places := range []int{3, 4, 4095, 4096, 4097, 5000, 1 << 20, 1 << 24} {
		got := evalRatForRound(t, fmt.Sprintf("round-half-to-even(1.234, %d)", places))
		if got.Cmp(want) != 0 {
			t.Errorf("round-half-to-even(1.234, %d) = %v, want 1.234", places, got)
		}
	}
}

// The negative direction. xs:decimal and xs:integer are unbounded in XPath, so
// "past -maxRoundPlaces everything is zero" was not true of them: 10^5000
// rounded to -5000 places keeps its leading digit.
func TestRoundPrecisionNegative(t *testing.T) {
	huge := "1" + strings.Repeat("0", 5000) // 10^5000, an xs:integer
	in := evalRatForRound(t, huge)

	// Rounding away fewer digits than the value has leaves it unchanged here,
	// because every discarded digit is already zero.
	for _, places := range []int{-4095, -4096, -4097, -5000} {
		got := evalRatForRound(t, fmt.Sprintf("round-half-to-even(%s, %d)", huge, places))
		if got.Cmp(in) != 0 {
			t.Errorf("round-half-to-even(10^5000, %d) = %v (zero=%v), want the value unchanged",
				places, got, got.Sign() == 0)
		}
	}
	// Past every digit the answer really is zero.
	for _, places := range []int{-5001, -5002, -6000, -100000} {
		got := evalRatForRound(t, fmt.Sprintf("round-half-to-even(%s, %d)", huge, places))
		if got.Sign() != 0 {
			t.Errorf("round-half-to-even(10^5000, %d) = %v, want 0", places, got)
		}
	}
}

// The ordinary cases must be untouched by any of this.
func TestRoundPrecisionOrdinary(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`round-half-to-even(2.5, 0)`, "2"},
		{`round-half-to-even(3.5, 0)`, "4"},
		{`round-half-to-even(-2.5, 0)`, "-2"},
		{`round-half-to-even(1.125, 2)`, "1.12"},
		{`round-half-to-even(35612.25, -2)`, "35600"},
		{`round-half-to-even(0.0, 5)`, "0"},
		{`round(2.5)`, "3"},
		{`round(-2.5)`, "-2"},
	}
	for _, c := range cases {
		s, err := Eval(c.expr, NewContext(nil, Builtins()), nil)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got := fmt.Sprint(s[0]); got != c.want {
			t.Errorf("%s = %s, want %s", c.expr, got, c.want)
		}
	}
}

// An enormous precision must not be answered by building an enormous bignum.
// This is the reason a bound existed at all, and it still has to hold.
func TestRoundPrecisionHugeIsFast(t *testing.T) {
	// 4294967296 places: the value that made this hang before any bound.
	for _, expr := range []string{
		`round-half-to-even(1.234, 4294967296)`,
		`round-half-to-even(1.234, -4294967296)`,
		`round-half-to-even(1.5e0, 4294967296)`,
	} {
		if _, err := Eval(expr, NewContext(nil, Builtins()), nil); err != nil {
			t.Errorf("%s: %v", expr, err)
		}
	}
}
