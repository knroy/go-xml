package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func fiOne(t *testing.T, expr string) string {
	t.Helper()
	got := str30(t, expr)
	if len(got) != 1 {
		t.Fatalf("%s returned %d items, want 1", expr, len(got))
	}
	return got[0]
}

// Every example the spec gives for fn:format-integer.
func TestFormatIntegerSpecExamples(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`format-integer(123, '0000')`, "0123"},
		{`format-integer(21, '1;o', 'en')`, "21st"},
		{`format-integer(7, 'a')`, "g"},
		{`format-integer(57, 'I')`, "LVII"},
		// The token is everything before the *last* semicolon, so this is the
		// token "#;##0" with a semicolon as its grouping separator.
		{`format-integer(1234, '#;##0;')`, "1;234"},
		{`format-integer(123, 'w')`, "one hundred and twenty-three"},
	}
	for _, tc := range cases {
		if got := fiOne(t, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestFormatIntegerSequences(t *testing.T) {
	cases := []struct{ expr, want string }{
		// A token of "1" counts 1, 2, 3; "01" pads to two digits.
		{`format-integer(5, '1')`, "5"},
		{`format-integer(5, '01')`, "05"},
		{`format-integer(5, '001')`, "005"},
		// The digits within a token are interchangeable: only the count of
		// mandatory signs matters, so "999" is the same as "000".
		{`format-integer(5, '999')`, "005"},
		{`format-integer(4, 'I')`, "IV"},
		{`format-integer(4, 'i')`, "iv"},
		{`format-integer(27, 'A')`, "AA"},
		{`format-integer(27, 'a')`, "aa"},
		{`format-integer(1, 'W')`, "ONE"},
		{`format-integer(1, 'Ww')`, "One"},
	}
	for _, tc := range cases {
		if got := fiOne(t, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// A grouping separator at a regular interval repeats leftwards; irregular
// positions apply only where they fall.
func TestFormatIntegerGrouping(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`format-integer(1234, '#,##0')`, "1,234"},
		{`format-integer(1234567, '#,##0')`, "1,234,567"},
		{`format-integer(123, '#,##0')`, "123"},
		// A different separator character is used as written.
		{`format-integer(1234567, '#.##0')`, "1.234.567"},
	}
	for _, tc := range cases {
		if got := fiOne(t, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// A negative value formats its absolute value with a minus prepended,
// whichever numbering sequence is in use.
func TestFormatIntegerNegative(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`format-integer(-3, '000')`, "-003"},
		{`format-integer(-4, 'I')`, "-IV"},
		{`format-integer(-1234, '#,##0')`, "-1,234"},
	}
	for _, tc := range cases {
		if got := fiOne(t, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// An empty $value is the zero-length string: not an error, and not "0".
func TestFormatIntegerEmpty(t *testing.T) {
	if got := fiOne(t, `format-integer((), '0000')`); got != "" {
		t.Errorf(`format-integer((), '0000') = %q, want ""`, got)
	}
}

// A malformed digit pattern is FODF1310. An unrecognised but well-formed
// token is not an error — the spec is explicit that a construct one processor
// knows and another does not falls back rather than failing.
//
// The code is asserted rather than the mere presence of an error. These
// pictures are rejected by several different checks, and a check that stopped
// running would leave the picture to be rejected further downstream under some
// other code -- or by the argument conversion, which never reaches the picture
// parser at all. "Some error" cannot tell those apart from the FODF1310 the
// spec names.
func TestFormatIntegerErrors(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath30
	for _, expr := range []string{
		// A separator at the edge, and two adjacent.
		`format-integer(1, ',000')`,
		`format-integer(1, '000,')`,
		`format-integer(1, '0,,000')`,
		// An optional-digit-sign after a mandatory one.
		`format-integer(1, '0#')`,
		// A zero-length primary format token.
		`format-integer(1, '')`,
	} {
		_, err := Eval(expr, ctx, nil)
		if err == nil {
			t.Errorf("%s succeeded, want FODF1310", expr)
			continue
		}
		if code := xdm.ErrorCode(err); code != "FODF1310" {
			t.Errorf("%s: error code %s (%v), want FODF1310", expr, code, err)
		}
	}

	// A malformed format modifier is an error too.
	for _, expr := range []string{
		`format-integer(1, '1;o(-er)z')`,
		`format-integer(1234, 'Ww;o(')`,
		`format-integer(1234, 'Ww;o()(')`,
	} {
		_, err := Eval(expr, ctx, nil)
		if err == nil {
			t.Errorf("%s succeeded, want FODF1310", expr)
			continue
		}
		if code := xdm.ErrorCode(err); code != "FODF1310" {
			t.Errorf("%s: error code %s (%v), want FODF1310", expr, code, err)
		}
	}

	// Fallback rather than error for a token nobody recognises. The spec's
	// test is whether the token contains a Unicode digit: without one it is
	// an unrecognised sequence, not a malformed digit pattern. "#" and "#a"
	// therefore fall back rather than failing, which the suite asserts.
	for _, tc := range []struct{ expr, want string }{
		{`format-integer(12, 'zz')`, "12"},
		{`format-integer(1500000, '#')`, "1500000"},
		{`format-integer(1500000, '#a')`, "1500000"},
	} {
		if got := fiOne(t, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want the fallback %q", tc.expr, got, tc.want)
		}
	}
}

// fn:format-integer is a 3.0 function, so a 2.0 expression must not see it.
//
// XPST0017 is "no such function", which is what hiding a function means. An
// err-only assertion also passes when the function IS visible and merely
// rejects its arguments -- the opposite of what this pins.
func TestFormatIntegerHiddenFromXPath20(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	_, err := Eval(`format-integer(1, '0')`, ctx, nil)
	if err == nil {
		t.Fatal("XPath20 resolved fn:format-integer, want XPST0017")
	}
	if code := xdm.ErrorCode(err); code != "XPST0017" {
		t.Errorf("error code %s (%v), want XPST0017 -- the function must be "+
			"unknown at 2.0, not merely fail", code, err)
	}
}
