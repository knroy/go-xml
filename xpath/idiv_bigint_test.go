package xpath

import (
	"math/big"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// pow10Lit returns the literal "1" followed by n zeros.
func pow10Lit(n int) string { return "1" + strings.Repeat("0", n) }

// TestIdivArbitraryPrecision pins that idiv answers from the exact value and
// never from a float64 projection of it.
//
// idiv's NaN/infinite guard used to ask math.IsInf(a.Float64(), 0). Float64()
// on an arbitrary-precision xs:integer overflows to +Inf above the float64
// range, so a finite exact integer of 10^309 was reported as infinite and
// raised FOAR0002; and on the divisor side the same test sent a finite huge
// divisor down the "infinite divisor truncates to zero" path, so
// 3*10^400 idiv 10^400 answered 0 instead of 3. The transition sat exactly on
// the float64 exponent limit -- 10^308 fine, 10^309 not -- which is not a
// boundary the XPath data model has.
//
// Every case asserts the VALUE. Merely asserting that no error is raised
// would still pass with the divisor bug in place.
func TestIdivArbitraryPrecision(t *testing.T) {
	big3 := func(n int) string { return "3" + strings.Repeat("0", n) }

	cases := []struct{ name, expr, want string }{
		// Around the int64 boundary, where nothing exotic happens yet.
		{"10^17 idiv 1", "xs:integer('" + pow10Lit(17) + "') idiv 1", pow10Lit(17)},
		{"10^18 idiv 1", "xs:integer('" + pow10Lit(18) + "') idiv 1", pow10Lit(18)},

		// The float64 exponent limit. 10^308 always worked; 10^309 is the
		// first value the old guard called infinite.
		{"10^308 idiv 1", "xs:integer('" + pow10Lit(308) + "') idiv 1", pow10Lit(308)},
		{"10^309 idiv 1", "xs:integer('" + pow10Lit(309) + "') idiv 1", pow10Lit(309)},
		{"10^400 idiv 1", "xs:integer('" + pow10Lit(400) + "') idiv 1", pow10Lit(400)},
		{"10^4096 idiv 1", "xs:integer('" + pow10Lit(4096) + "') idiv 1", pow10Lit(4096)},

		// Huge dividend, small divisor.
		{"10^400 idiv 4", "xs:integer('" + pow10Lit(400) + "') idiv 4", "25" + strings.Repeat("0", 398)},

		// Huge DIVISOR whose correct answer is NOT zero. This is the case
		// that separates a real fix from one that only happens to agree with
		// the old code: 7 idiv 10^400 is 0 either way.
		{"3*10^400 idiv 10^400", "xs:integer('" + big3(400) + "') idiv xs:integer('" + pow10Lit(400) + "')", "3"},
		{"10^500 idiv 10^400", "xs:integer('" + pow10Lit(500) + "') idiv xs:integer('" + pow10Lit(400) + "')", pow10Lit(100)},
		{"10^4096 idiv 10^4095", "xs:integer('" + pow10Lit(4096) + "') idiv xs:integer('" + pow10Lit(4095) + "')", "10"},

		// Huge divisor where zero IS right, kept so the truncation direction
		// stays pinned.
		{"7 idiv 10^400", "7 idiv xs:integer('" + pow10Lit(400) + "')", "0"},

		// Negatives, in both operand positions. idiv truncates toward zero.
		{"-10^400 idiv 1", "-xs:integer('" + pow10Lit(400) + "') idiv 1", "-" + pow10Lit(400)},
		{"-3*10^400 idiv 10^400", "-xs:integer('" + big3(400) + "') idiv xs:integer('" + pow10Lit(400) + "')", "-3"},
		{"3*10^400 idiv -10^400", "xs:integer('" + big3(400) + "') idiv -xs:integer('" + pow10Lit(400) + "')", "-3"},
		{"-3*10^400 idiv -10^400", "-xs:integer('" + big3(400) + "') idiv -xs:integer('" + pow10Lit(400) + "')", "3"},

		// A huge xs:decimal is exact and unbounded too.
		{"decimal 10^400 idiv 10^399", "xs:decimal('" + pow10Lit(400) + "') idiv xs:decimal('" + pow10Lit(399) + "')", "10"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := evalString(t, c.expr)
			if err != nil {
				t.Fatalf("%s: unexpected error %v", c.expr, err)
			}
			if got != c.want {
				t.Errorf("%s\n got %s\nwant %s", c.name, abbrev(got), abbrev(c.want))
			}
		})
	}
}

// TestIdivStillRejectsNaNAndInfinity is the regression guard on the fix
// above: the false reject must not have been cured by dropping the check.
// Only xs:double and xs:float can be NaN or infinite, and those must still
// raise FOAR0002 in the dividend position.
func TestIdivStillRejectsNaNAndInfinity(t *testing.T) {
	mustFail := []string{
		"xs:double('INF') idiv 1",
		"xs:double('-INF') idiv 1",
		"xs:double('NaN') idiv 1",
		"xs:float('INF') idiv 1",
		"xs:float('NaN') idiv 1",
		"1 idiv xs:double('NaN')",
		"xs:double('INF') idiv xs:double('INF')",
		// A huge finite integer dividend by an infinity is not an error, but
		// an infinite dividend still is even when the divisor is huge.
		"xs:double('INF') idiv xs:integer('" + pow10Lit(400) + "')",
	}
	for _, e := range mustFail {
		if _, err := evalString(t, e); err == nil {
			t.Errorf("%s: expected FOAR0002, got success", e)
		} else if !strings.Contains(err.Error(), "FOAR0002") {
			t.Errorf("%s: expected FOAR0002, got %v", e, err)
		}
	}

	// An infinite DIVISOR is not an error: a finite value divided by an
	// infinity truncates to zero.
	for _, c := range []struct{ expr, want string }{
		{"1 idiv xs:double('INF')", "0"},
		{"xs:integer('" + pow10Lit(400) + "') idiv xs:double('INF')", "0"},
		{"-1 idiv xs:double('INF')", "0"},
	} {
		got, err := evalString(t, c.expr)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.expr, err)
		} else if got != c.want {
			t.Errorf("%s: got %s want %s", c.expr, got, c.want)
		}
	}

	// Division by zero stays the more specific complaint.
	if _, err := evalString(t, "xs:double('INF') idiv 0"); err == nil ||
		!strings.Contains(err.Error(), "FOAR0001") {
		t.Errorf("INF idiv 0: expected FOAR0001, got %v", err)
	}
}

// TestFormatNumberKeepsExactDecimalDigits pins that fn:format-number formats
// from the exact rational, not from a double. A decimal carrying far more
// than the ~17 significant digits a double holds must print every digit.
func TestFormatNumberKeepsExactDecimalDigits(t *testing.T) {
	df := DefaultDecimalFormat()
	mustDec := func(s string) *xdm.Atomic {
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			t.Fatalf("bad decimal %q", s)
		}
		return xdm.NewDecimal(r)
	}
	pow10 := func(n int64) *xdm.Atomic {
		return xdm.NewIntegerFromRat(new(big.Rat).SetInt(
			new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil)))
	}

	cases := []struct {
		name string
		num  *xdm.Atomic
		pic  string
		want string
	}{
		{"34 significant digits",
			mustDec("123456789012345678901234.5678901234"), "#0.0000000000",
			"123456789012345678901234.5678901234"},
		{"23 fraction digits",
			mustDec("0.12345678901234567890123"), "0.00000000000000000000000",
			"0.12345678901234567890123"},
		{"integer past the float64 range",
			pow10(400), "#0", pow10Lit(400)},
		{"negative, exact",
			mustDec("-99999999999999999999.00000000000000000001"), "#0.00000000000000000000",
			"-99999999999999999999.00000000000000000001"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FormatNumberVersion(c.num, c.pic, df, XPath30)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if got != c.want {
				t.Errorf("got %s\nwant %s", abbrev(got), abbrev(c.want))
			}
		})
	}
}

func abbrev(s string) string {
	if len(s) <= 80 {
		return s
	}
	return s[:40] + "..." + s[len(s)-20:] + " (len " + strings.TrimSpace(big.NewInt(int64(len(s))).String()) + ")"
}
