package xpath

import (
	"math/big"
	"strings"
	"testing"
)

// TestFormatNumberWidePaddingIsLinear: the integer and exponent pads grew the
// string one "0" at a time, re-copying it on every step, so a picture of n
// zero-digit signs cost O(n²) and one allocation per digit. The allocation
// count is the deterministic witness: it must not scale with the width.
func TestFormatNumberWidePaddingIsLinear(t *testing.T) {
	df := DefaultDecimalFormat()
	val := decimalOf(t, "1")
	const n = 20000
	for _, tc := range []struct{ name, pic string }{
		{"integer pad", strings.Repeat("0", n)},
		{"exponent pad", "0e" + strings.Repeat("0", n)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out string
			allocs := testing.AllocsPerRun(1, func() {
				var err error
				if out, err = FormatNumberVersion(val, tc.pic, df, XPath31); err != nil {
					t.Fatal(err)
				}
			})
			if len(out) < n {
				t.Fatalf("format-number(1, %q…) = %d bytes, want at least %d", tc.pic[:4], len(out), n)
			}
			if allocs > 1000 {
				t.Errorf("%s: %.0f allocations for a %d-digit pad; the pad is per-digit", tc.name, allocs, n)
			}
		})
	}
}

// TestFormatNumberManySeparatorsAfterPassiveText: splitExponent rescanned the
// prefix for every exponent-separator, so "x…xe…e" cost O(n²). The answer is
// unchanged: none of the separators is preceded by an active character, so the
// picture still has no digit and is refused.
func TestFormatNumberManySeparatorsAfterPassiveText(t *testing.T) {
	pic := strings.Repeat("x", 100) + strings.Repeat("e", 100)
	if _, err := FormatNumberVersion(decimalOf(t, "1"), pic, DefaultDecimalFormat(), XPath31); err == nil ||
		!strings.Contains(err.Error(), "FODF1310") {
		t.Errorf("format-number(1, %q) error = %v, want FODF1310", pic, err)
	}
}

// TestFormatNumberWideFractionWorksAtTheValuesLength: the picture's count of
// fractional digit signs was the exponent of the 10^n that roundToPlaces and
// splitRat worked at, so format-number(0.1, '.' followed by 16M '#') spent
// seconds on 53M-bit rationals to print ".1". A value with no more places than
// the picture asks for is now returned by roundToPlaces as it is, and splitRat
// writes the value's own digits and pads the rest.
func TestFormatNumberWideFractionWorksAtTheValuesLength(t *testing.T) {
	const n = 1 << 20
	v := decimalOf(t, "0.125")
	r := formatRatOf(v)
	if got := roundToPlaces(r, n); got != r {
		t.Errorf("roundToPlaces(0.125, %d) built a new rational; want the value itself", n)
	}
	if got := roundToPlaces(r, 2); got.FloatString(3) != "0.120" {
		t.Errorf("roundToPlaces(0.125, 2) = %s, want 0.120 (half to even)", got.FloatString(3))
	}
	if i, f := splitRat(r, 6); i != "0" || f != "125000" {
		t.Errorf("splitRat(0.125, 6) = %q, %q; want \"0\", \"125000\"", i, f)
	}
	for _, tc := range []struct{ pic, want string }{
		{"." + strings.Repeat("#", n), ".125"},
		{"0.00000", "0.12500"},
		{"#.#", ".1"},
	} {
		got, err := FormatNumberVersion(v, tc.pic, DefaultDecimalFormat(), XPath31)
		if err != nil || got != tc.want {
			t.Errorf("format-number(0.125, %.8q…) = %q, %v; want %q", tc.pic, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		s    string
		want int
	}{
		{"1", 0}, {"0.5", 1}, {"0.04", 2}, {"0.125", 3}, {"3.0000001", 7},
	} {
		if got := decimalPlaces(formatRatOf(decimalOf(t, tc.s))); got != tc.want {
			t.Errorf("decimalPlaces(%s) = %d, want %d", tc.s, got, tc.want)
		}
	}
	if got := decimalPlaces(big.NewRat(1, 3)); got != -1 {
		t.Errorf("decimalPlaces(1/3) = %d, want -1", got)
	}
}
