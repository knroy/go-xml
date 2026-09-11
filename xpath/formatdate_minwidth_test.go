package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// F&O 3.0 section 9.8.4.1: "If the full representation of the value is shorter
// than the specified minimum width, then the processor should pad the value to
// the specified width." Zero digits are prepended for a decimal number, and
// "in other cases, it should be done by appending spaces."
//
// The minimum was honoured only for a decimal pattern and for roman numerals.
// Every other presentation modifier -- the spelled-out words w/W/Ww, the
// alphabetic sequences a/A, and the by-name forms N/n/Nn -- parsed the width,
// validated it, threaded it all the way down, and then returned the
// unpadded value.
func TestFormatDateMinWidthPadsNonDecimalPresentations(t *testing.T) {
	cases := []struct{ expr, want string }{
		// Spelled-out words: September is month nine, four characters, so a
		// minimum of eight appends four spaces.
		{`format-date(xs:date("2003-09-07"), "[Mw,8]")`, "nine    "},
		{`format-date(xs:date("2003-09-07"), "[MW,8]")`, "NINE    "},
		{`format-date(xs:date("2003-09-07"), "[MWw,8]")`, "Nine    "},
		// Alphabetic sequences: the ninth letter is "i".
		{`format-date(xs:date("2003-09-07"), "[Ma,8]")`, "i       "},
		{`format-date(xs:date("2003-09-07"), "[MA,8]")`, "I       "},
		// A by-name presentation is the documented exception: the suite case
		// date-064 pins "[PNn,4-4]" at "Am" rather than "Am  ", so a name is
		// taken to be full at whatever width its abbreviation lands on.
		{`format-date(xs:date("2003-09-07"), "[Fn,20]")`, "sunday"},
		{`format-date(xs:date("2003-09-07"), "[FN,4]")`, "SUN"},
		{`format-time(xs:time("00:00:00"), "[PNn,4-4]")`, "Am"},
		// The two presentations that already padded must keep doing so.
		{`format-date(xs:date("2003-09-07"), "[Mi,8]")`, "ix      "},
		{`format-date(xs:date("2003-09-07"), "[MI,8]")`, "IX      "},
		// A decimal pattern pads with zeroes, not spaces, and is unaffected.
		{`format-date(xs:date("2003-09-07"), "[M01,8]")`, "00000009"},
	}
	for _, tc := range cases {
		if got := formatDateMinWidthEval(t, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// formatDateMinWidthEval evaluates a single-item string expression.
func formatDateMinWidthEval(t *testing.T, expr string) string {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	if len(seq) != 1 {
		t.Fatalf("%s returned %d items, want 1", expr, len(seq))
	}
	return seq[0].(*xdm.Atomic).String()
}
