package xpath

import (
	"math/big"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestFormatNumberRoundsHalfToEven pins the rounding mode of fn:format-number.
// F&O 3.1 §4.7.5 defines the rounded value by "calling the function
// fn:round-half-to-even with this converted number as the first argument and
// the maximum-fractional-part-size as the second argument", so an exact tie
// goes to the even digit, not away from zero.
//
// Neither conformance corpus exercises this: every format-number case in
// testdata/qt3tests/fn/format-number.xml and in
// testdata/xslt30-test/tests/fn/format-number/ sits strictly off the halfway
// point (.4857, .588, …). The counted pass figures therefore do not move with
// this behaviour in either direction, and this test is the only thing holding
// it. It must fail if roundToPlaces reverts to half-away-from-zero.
func TestFormatNumberRoundsHalfToEven(t *testing.T) {
	df := DefaultDecimalFormat()
	dec := func(s string) *xdm.Atomic {
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			t.Fatalf("bad decimal %q", s)
		}
		return xdm.NewDecimal(r)
	}

	cases := []struct {
		name string
		val  string
		pic  string
		want string
	}{
		// Exact ties at the integer position: each rounds to the even
		// neighbour, so they alternate down/up rather than all going away
		// from zero.
		{"tie 0.5 down to even 0", "0.5", "0", "0"},
		{"tie 1.5 up to even 2", "1.5", "0", "2"},
		{"tie 2.5 down to even 2", "2.5", "0", "2"},
		{"tie 3.5 up to even 4", "3.5", "0", "4"},
		{"tie 4.5 down to even 4", "4.5", "0", "4"},

		// Negative ties round to the even neighbour by magnitude too. The
		// minus sign on -0.5 is not from the rounding: format-number picks
		// the negative sub-picture from the sign of the input, before the
		// value is rounded, so a tie that rounds to zero still prints "-0".
		{"tie -0.5 to even zero, sign kept", "-0.5", "0", "-0"},
		{"tie -1.5 to even -2", "-1.5", "0", "-2"},
		{"tie -2.5 to even -2", "-2.5", "0", "-2"},
		{"tie -3.5 to even -4", "-3.5", "0", "-4"},

		// Ties at a fractional position, both directions.
		{"tie 0.125 down to even 0.12", "0.125", "0.00", "0.12"},
		{"tie 0.135 up to even 0.14", "0.135", "0.00", "0.14"},
		{"tie 2.675 down to even 2.68", "2.675", "0.00", "2.68"},
		{"tie -0.125 to even -0.12", "-0.125", "0.00", "-0.12"},

		// Non-ties must be untouched: these round to the nearest value
		// regardless of parity, proving the fix did not simply swap one
		// rounding mode for another that happens to satisfy the ties.
		{"below half 0.4", "0.4", "0", "0"},
		{"above half 0.6", "0.6", "0", "1"},
		{"below half 2.4", "2.4", "0", "2"},
		{"above half 2.6 to odd 3", "2.6", "0", "3"},
		{"above half 1.6 to even 2", "1.6", "0", "2"},
		{"below half 3.4 to odd 3", "3.4", "0", "3"},
		{"negative above half -2.6 to odd -3", "-2.6", "0", "-3"},
		{"just past half 2.5000001", "2.5000001", "0", "3"},
		{"just under half 2.4999999", "2.4999999", "0", "2"},
		{"fraction non-tie 0.124", "0.124", "0.00", "0.12"},
		{"fraction non-tie 0.126", "0.126", "0.00", "0.13"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FormatNumberVersion(dec(c.val), c.pic, df, XPath30)
			if err != nil {
				t.Fatalf("format-number(%s, %q): unexpected error %v", c.val, c.pic, err)
			}
			if got != c.want {
				t.Errorf("format-number(%s, %q) = %s, want %s",
					c.val, c.pic, got, c.want)
			}
		})
	}
}
