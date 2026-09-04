package xpath

import (
	"strings"
	"testing"
)

// evalBig evaluates expr under XPath 3.1 and reports its string value.
func evalBig(t *testing.T, expr string) (string, error) {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(seq))
	for _, it := range seq {
		parts = append(parts, it.(interface{ String() string }).String())
	}
	return strings.Join(parts, " "), nil
}

// TestArrayLookupExactPosition covers the dynamic lookup "$a?($k)".
//
// The positions here are chosen so that a float64 round trip changes the
// value: 2^53+1 rounds down to 2^53, 10^18+1 loses its last digit, and
// anything past 2^63 saturates to maxint. The assertion is on the reported
// position, not merely on the presence of an error, because narrowing is
// silent — an error-only test passes against the broken code.
func TestArrayLookupExactPosition(t *testing.T) {
	cases := []struct {
		expr string
		// want is the value when the lookup succeeds.
		want string
		// wantPos is the position the FOAY0001 message must name.
		wantPos string
	}{
		{`let $a := [10,20,30] return $a?(1)`, "10", ""},
		{`let $a := [10,20,30] return $a?(3)`, "30", ""},
		{`let $a := [10,20,30] return $a?(4)`, "", "4"},
		{`let $a := [10,20,30] return $a?(0)`, "", "0"},
		{`let $a := [10,20,30] return $a?(-1)`, "", "-1"},
		{`let $a := [10,20,30] return $a?(2147483648)`, "", "2147483648"},
		{`let $a := [10,20,30] return $a?(4294967296)`, "", "4294967296"},
		{`let $a := [10,20,30] return $a?(4294967297)`, "", "4294967297"},
		// 2^53 and 2^53+1 are the pair a float64 cannot tell apart.
		{`let $a := [10,20,30] return $a?(9007199254740992)`, "", "9007199254740992"},
		{`let $a := [10,20,30] return $a?(9007199254740993)`, "", "9007199254740993"},
		{`let $a := [10,20,30] return $a?(1000000000000000001)`, "", "1000000000000000001"},
		// 2^63-1 and 2^63: the second does not fit an int64 at all.
		{`let $a := [10,20,30] return $a?(9223372036854775807)`, "", "9223372036854775807"},
		{`let $a := [10,20,30] return $a?(9223372036854775808)`, "", "9223372036854775808"},
		{`let $a := [10,20,30] return $a?(-9223372036854775809)`, "", "-9223372036854775809"},
		{`let $a := [10,20,30] return $a?(` + strings.Repeat("0", 0) + `100000000000000000000000000000000)`, "", "100000000000000000000000000000000"},
		{`let $a := [10,20,30] return $a?(1` + strings.Repeat("0", 1000) + `)`, "", "1" + strings.Repeat("0", 1000)},
		{`let $a := [10,20,30] return $a?(-9007199254740993)`, "", "-9007199254740993"},
	}
	for _, c := range cases {
		got, err := evalBig(t, c.expr)
		if c.wantPos == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.expr, err)
			} else if got != c.want {
				t.Errorf("%s = %q, want %q", c.expr, got, c.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s = %q, want FOAY0001", c.expr, got)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "FOAY0001") {
			t.Errorf("%s: error %q, want FOAY0001", c.expr, msg)
		}
		// The exact digits must survive to the message. A float64 round trip
		// would report a neighbouring value instead.
		if !strings.Contains(msg, c.wantPos) {
			t.Errorf("%s: error %q does not name position %s", c.expr, msg, c.wantPos)
		}
	}
}

// TestArrayLookupLiteralExactPosition is the same for the literal form "$a?3",
// which the parser turns into a LookupExpr.Index rather than an expression.
func TestArrayLookupLiteralExactPosition(t *testing.T) {
	cases := []struct{ expr, want, wantPos string }{
		{`let $a := [10,20,30] return $a?1`, "10", ""},
		{`let $a := [10,20,30] return $a?3`, "30", ""},
		{`let $a := [10,20,30] return $a?4`, "", "4"},
		{`let $a := [10,20,30] return $a?0`, "", "0"},
		{`let $a := [10,20,30] return $a?2147483648`, "", "2147483648"},
		{`let $a := [10,20,30] return $a?4294967297`, "", "4294967297"},
		{`let $a := [10,20,30] return $a?9007199254740993`, "", "9007199254740993"},
		{`let $a := [10,20,30] return $a?1000000000000000001`, "", "1000000000000000001"},
		{`let $a := [10,20,30] return $a?9223372036854775807`, "", "9223372036854775807"},
		{`let $a := [10,20,30] return $a?9223372036854775808`, "", "9223372036854775808"},
		{`let $a := [10,20,30] return $a?100000000000000000000000000000000`, "", "100000000000000000000000000000000"},
		{`let $a := [10,20,30] return $a?1` + strings.Repeat("0", 1000), "", "1" + strings.Repeat("0", 1000)},
	}
	for _, c := range cases {
		got, err := evalBig(t, c.expr)
		if c.wantPos == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.expr, err)
			} else if got != c.want {
				t.Errorf("%s = %q, want %q", c.expr, got, c.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s = %q, want FOAY0001", c.expr, got)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, "FOAY0001") ||
			!strings.Contains(msg, c.wantPos) {
			t.Errorf("%s: error %q, want FOAY0001 naming %s", c.expr, msg, c.wantPos)
		}
	}
}

// TestLookupLiteralRoundTrips checks that a literal position survives being
// printed back, which is what String() is for. "%d" on a narrowed int would
// print the neighbouring value.
func TestLookupLiteralRoundTrips(t *testing.T) {
	for _, lit := range []string{
		"1", "2147483648", "9007199254740993", "1000000000000000001",
		"9223372036854775808", "100000000000000000000000000000000",
	} {
		c, err := CompileVersion("$a?"+lit, nil, XPath31)
		if err != nil {
			t.Fatalf("compile ?%s: %v", lit, err)
		}
		if got := c.expr.String(); !strings.Contains(got, lit) {
			t.Errorf("?%s printed as %q, which does not contain %s", lit, got, lit)
		}
	}
}

// TestFunctionLookupExactArity covers fn:function-lookup's $arity.
//
// F&O 3.0 16.1.1 has a single catch-all: "if no known function can be
// identified by name and arity, an empty sequence is returned". There is no
// error condition for a negative or an enormous arity, so an arity that
// cannot name any function is empty rather than a failure. The narrowing this
// replaces mattered because 10^32 saturated to maxint rather than being
// recognised as unmatchable.
func TestFunctionLookupExactArity(t *testing.T) {
	const abs = `QName('http://www.w3.org/2005/xpath-functions','abs')`
	cases := []struct {
		arity string
		want  string
	}{
		{"1", "true"},
		{"0", "false"},
		{"2", "false"},
		{"-1", "false"},
		{"2147483648", "false"},
		{"4294967296", "false"},
		{"9007199254740992", "false"},
		{"9007199254740993", "false"},
		{"9223372036854775807", "false"},
		{"9223372036854775808", "false"},
		{"-9223372036854775809", "false"},
		{"100000000000000000000000000000000", "false"},
		{"1" + strings.Repeat("0", 1000), "false"},
	}
	for _, c := range cases {
		expr := "exists(function-lookup(" + abs + ", " + c.arity + "))"
		got, err := evalBig(t, expr)
		if err != nil {
			t.Errorf("arity %s: unexpected error %v", c.arity, err)
			continue
		}
		if got != c.want {
			t.Errorf("function-lookup(fn:abs, %s) exists = %s, want %s",
				c.arity, got, c.want)
		}
	}
}

// TestFunctionLookupHugeArityIsNotMaxInt is the sensitivity anchor for the
// arity fix.
//
// A function really registered at a large-but-representable arity must not be
// found by a huge arity that a float64 would have saturated onto it. Rather
// than register one, this asserts the weaker property that no arity beyond
// int32 matches anything, which is what the saturating conversion violated.
func TestFunctionLookupHugeArityIsNotMaxInt(t *testing.T) {
	// fn:concat is variadic and is the most likely function to be registered
	// at a very high arity, so it is the sharpest probe available.
	const concat = `QName('http://www.w3.org/2005/xpath-functions','concat')`
	for _, arity := range []string{
		"9223372036854775807", "100000000000000000000000000000000",
	} {
		expr := "exists(function-lookup(" + concat + ", " + arity + "))"
		got, err := evalBig(t, expr)
		if err != nil {
			t.Fatalf("arity %s: %v", arity, err)
		}
		if got != "false" {
			t.Errorf("function-lookup(fn:concat, %s) exists = %s, want false",
				arity, got)
		}
	}
}

// TestArrayCallExactPosition covers the array-as-function form "$a(n)", which
// reaches integerPosition through arrayCallIndex.
func TestArrayCallExactPosition(t *testing.T) {
	cases := []struct{ expr, want, wantPos string }{
		{`let $a := [10,20,30] return $a(2)`, "20", ""},
		{`let $a := [10,20,30] return $a(4)`, "", "4"},
		{`let $a := [10,20,30] return $a(9007199254740993)`, "", "9007199254740993"},
		{`let $a := [10,20,30] return $a(100000000000000000000000000000000)`, "", "100000000000000000000000000000000"},
	}
	for _, c := range cases {
		got, err := evalBig(t, c.expr)
		if c.wantPos == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.expr, err)
			} else if got != c.want {
				t.Errorf("%s = %q, want %q", c.expr, got, c.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s = %q, want FOAY0001", c.expr, got)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, "FOAY0001") ||
			!strings.Contains(msg, c.wantPos) {
			t.Errorf("%s: error %q, want FOAY0001 naming %s", c.expr, msg, c.wantPos)
		}
	}
}

// TestArrayLookupKeyTypeStillChecked guards the fix's blast radius: routing
// the key through integerPosition must not start accepting a non-integer.
// Lookup-119 is the case that made this matter.
func TestArrayLookupKeyTypeStillChecked(t *testing.T) {
	for _, expr := range []string{
		`let $a := [10,20,30] return $a?(1.0)`,
		`let $a := [10,20,30] return $a?(1e0)`,
		`let $a := [10,20,30] return $a?('1')`,
	} {
		got, err := evalBig(t, expr)
		if err == nil {
			t.Errorf("%s = %q, want XPTY0004", expr, got)
			continue
		}
		if !strings.Contains(err.Error(), "XPTY0004") {
			t.Errorf("%s: error %q, want XPTY0004", expr, err)
		}
	}
}

// TestSubarrayPositionsCannotOverflow pins the invariant that keeps
// maxArrayIndex narrow: array:subarray adds two accepted positions, and that
// sum must not wrap. If maxArrayIndex were widened to math.MaxInt, "start+n"
// would go negative and the bounds guard would read false, silently accepting
// an out-of-range request instead of raising FOAY0001.
func TestSubarrayPositionsCannotOverflow(t *testing.T) {
	const maxInt = int(^uint(0) >> 1)
	if maxArrayIndex <= 0 || maxArrayIndex > maxInt/2 {
		t.Fatalf("maxArrayIndex = %d must stay at or below maxInt/2 = %d so "+
			"array:subarray's start+n cannot overflow", maxArrayIndex, maxInt/2)
	}
	if minArrayIndex != -maxArrayIndex {
		t.Fatalf("minArrayIndex = %d, want -%d", minArrayIndex, maxArrayIndex)
	}
}
