package xpath

import (
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
