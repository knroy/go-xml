package xpath

import (
	"math/big"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The two halves of the F&O 3.1 §4.7.3 exponent rule, neither of which any
// conformance corpus exercises. Every format-number case in
// testdata/qt3tests/fn/format-number.xml and in
// testdata/xslt30-test/tests/fn/format-number/ uses an exponent picture whose
// separator is preceded by digits and followed by digits and passive text
// only, so the counted pass figures do not move with this behaviour in either
// direction. These tests are the only thing holding it.
//
// Scientific notation is XPath 3.1, so every case runs under XPath31.

// TestFormatNumberExponentSeparatorNeedsPrecedingActive covers the first half:
// "A character that matches the exponent-separator property is treated as an
// exponent-separator-sign if it is both preceded and followed within the
// sub-picture by an active character. Otherwise, it is treated as a passive
// character."
//
// Splitting on what followed alone handed parsePicture a mantissa that was
// empty or held no digit, and the resulting error named a picture the caller
// never wrote. Where nothing active precedes the separator it is passive, so
// it is simply part of the prefix.
func TestFormatNumberExponentSeparatorNeedsPrecedingActive(t *testing.T) {
	df := DefaultDecimalFormat()
	val := decimalOf(t, "1234")

	cases := []struct {
		name string
		pic  string
		want string
	}{
		// The separator is the very first character: nothing at all precedes
		// it, so it is passive and "e0" is the prefix "e" over the digit
		// region "0".
		{"separator first", "e0", "e1234"},
		// Preceded only by passive text, which is not an active character
		// either.
		{"one passive before", "xe0", "xe1234"},
		{"several passive before", "abce0", "abce1234"},
		// The percent and per-mille characters are explicitly passive, so
		// they do not license the split either. The "e" stays passive, so
		// there is no exponent and hence no exponent/percent conflict: the
		// whole "%e" is prefix and the value is scaled by a hundred.
		{"percent before is passive", "%e0", "%e123400"},

		// The contrast: one active character before the separator is enough
		// to make it a sign, and then the picture really is scientific.
		{"digit before", "0e0", "1e3"},
		// The optional-digit sign is active, so it licenses the split too.
		// It leaves the minimum integer size at zero, which is why the
		// mantissa here is 0.1 rather than 1 (the same rule as "#e0" on 0.2
		// in numberformat231).
		{"optional digit before", "#e0", "0.1e4"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FormatNumberVersion(val, c.pic, df, XPath31)
			if err != nil {
				t.Fatalf("format-number(1234, %q): unexpected error %v", c.pic, err)
			}
			if got != c.want {
				t.Errorf("format-number(1234, %q) = %q, want %q", c.pic, got, c.want)
			}
		})
	}
}

// TestFormatNumberActiveCharacterAfterExponentDigits covers the second half:
// "If a sub-picture contains a character treated as an exponent-separator-sign
// then this must be followed by one or more characters that are members of the
// decimal digit family, and it must not be followed by any active character
// that is not a member of the decimal digit family."
//
// Only a second separator-plus-digits was rejected before; every other active
// character fell through and was kept as the exponent suffix.
func TestFormatNumberActiveCharacterAfterExponentDigits(t *testing.T) {
	df := DefaultDecimalFormat()
	val := decimalOf(t, "1234")

	for _, c := range []struct{ name, pic string }{
		{"decimal separator after exponent", "0e0.0"},
		{"optional digit after exponent", "0e0#"},
		{"grouping separator after exponent", "0e00,0"},
		// The digits need not be adjacent to the offending character for the
		// rule to bite: "followed" means anywhere later in the sub-picture.
		{"active character beyond passive text", "0e0xy#"},
		// A second separator-plus-digits was already caught, and still is.
		{"second exponent part", "9.99e99e99"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := FormatNumberVersion(val, c.pic, df, XPath31)
			if err == nil {
				t.Fatalf("format-number(1234, %q) = %q, want error FODF1310",
					c.pic, got)
			}
			if !strings.Contains(err.Error(), "FODF1310") {
				t.Errorf("format-number(1234, %q) error = %v, want FODF1310",
					c.pic, err)
			}
		})
	}
}

// TestFormatNumberExponentPassiveSuffixStillFormats is the control on the
// rejection above: only *active* characters after the exponent digits are an
// error. Passive ones are an ordinary suffix and must keep working, or the
// fix for the active case has simply been made too broad.
func TestFormatNumberExponentPassiveSuffixStillFormats(t *testing.T) {
	df := DefaultDecimalFormat()
	val := decimalOf(t, "1234")

	for _, c := range []struct{ name, pic, want string }{
		// x, y and z are passive, so this genuinely is a suffix.
		{"passive suffix", "0e0xyz", "1e3xyz"},
		{"passive suffix on wide picture", "9.9999e99end", "1.2340e03end"},
		// A trailing exponent-separator with nothing after it cannot itself
		// be a sign, so it is passive rather than an active character that
		// would make the picture an error.
		{"trailing separator is passive", "9.9999e99e", "1.2340e03e"},
		// The separator followed by non-digits introduces no exponent at all,
		// and the whole "eDog" is passive suffix text.
		{"separator without digits", "9.9999eDog", "1234.0000eDog"},

		// Ordinary exponent pictures, proving the normal path is untouched.
		{"fraction and exponent", "0.0e0", "1.2e3"},
		{"optional digits and exponent", "#.##e0", "0.12e4"},
		{"bare exponent", "0e0", "1e3"},
		{"two exponent digits", "0.0e00", "1.2e03"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := FormatNumberVersion(val, c.pic, df, XPath31)
			if err != nil {
				t.Fatalf("format-number(1234, %q): unexpected error %v", c.pic, err)
			}
			if got != c.want {
				t.Errorf("format-number(1234, %q) = %q, want %q", c.pic, got, c.want)
			}
		})
	}
}

func decimalOf(t *testing.T, s string) *xdm.Atomic {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad decimal %q", s)
	}
	return xdm.NewDecimal(r)
}
