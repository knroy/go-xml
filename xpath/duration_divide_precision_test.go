package xpath

import (
	"math/big"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// evalDurDiv evaluates an expression expected to yield one atomic value.
func evalDurDiv(t *testing.T, expr string) *xdm.Atomic {
	t.Helper()
	s, err := Eval(expr, NewContext(nil, Builtins()), nil)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	if len(s) != 1 {
		t.Fatalf("%s: returned %d items, want 1", expr, len(s))
	}
	a, ok := s[0].(*xdm.Atomic)
	if !ok {
		t.Fatalf("%s: not an atomic value", expr)
	}
	return a
}

func evalDurDivBool(t *testing.T, expr string) bool {
	t.Helper()
	a := evalDurDiv(t, expr)
	if a.Type != xdm.TypeBoolean {
		t.Fatalf("%s: got %s, want xs:boolean", expr, a.TypeName())
	}
	return a.Bool()
}

// Terminating ratios must stay EXACT. This is the regression a precision
// normalization on the duration path most plausibly causes, so it is asserted
// separately from the fix it guards: PT1S div PT2S is 0.5 and nothing else --
// not 0.500000000000000000 padded out, and not a value that has drifted.
func TestDurationDivideTerminatingRatiosStayExact(t *testing.T) {
	cases := []struct {
		expr string
		want string // exact rational
	}{
		{`xs:dayTimeDuration("PT1S") div xs:dayTimeDuration("PT2S")`, "1/2"},
		{`xs:dayTimeDuration("PT1S") div xs:dayTimeDuration("PT4S")`, "1/4"},
		{`xs:dayTimeDuration("PT1S") div xs:dayTimeDuration("PT8S")`, "1/8"},
		{`xs:dayTimeDuration("PT3S") div xs:dayTimeDuration("PT4S")`, "3/4"},
		{`xs:dayTimeDuration("P1D") div xs:dayTimeDuration("PT1S")`, "86400"},
		{`xs:dayTimeDuration("-PT1S") div xs:dayTimeDuration("PT2S")`, "-1/2"},
		{`xs:yearMonthDuration("P1Y") div xs:yearMonthDuration("P1M")`, "12"},
		{`xs:yearMonthDuration("P3Y4M") div xs:yearMonthDuration("-P1Y4M")`, "-5/2"},
		{`xs:yearMonthDuration("P1M") div xs:yearMonthDuration("P8M")`, "1/8"},
	}
	for _, c := range cases {
		got := evalDurDiv(t, c.expr).Rat()
		want, ok := new(big.Rat).SetString(c.want)
		if !ok {
			t.Fatalf("bad want %q", c.want)
		}
		if got.Cmp(want) != 0 {
			t.Errorf("%s = %s, want exactly %s", c.expr, got.RatString(), c.want)
		}
	}

	// The spec's own worked example: the number of seconds in a duration.
	if got := evalDurDiv(t,
		`xs:dayTimeDuration("P2DT53M11S") div xs:dayTimeDuration("PT1S")`).Rat(); got.Cmp(big.NewRat(175991, 1)) != 0 {
		t.Errorf("P2DT53M11S div PT1S = %s, want 175991", got.RatString())
	}
}

// A non-terminating duration ratio must be the same xs:decimal as the same
// mathematical quotient computed by ordinary division. Two subsystems of one
// processor must not disagree about what 1 div 3 is.
func TestDurationDivideMatchesNumericDivide(t *testing.T) {
	cases := []struct{ dur, num string }{
		{`xs:dayTimeDuration("PT1S") div xs:dayTimeDuration("PT3S")`, `1 div 3`},
		{`xs:dayTimeDuration("PT2S") div xs:dayTimeDuration("PT3S")`, `2 div 3`},
		{`xs:dayTimeDuration("PT1S") div xs:dayTimeDuration("PT7S")`, `1 div 7`},
		{`xs:dayTimeDuration("-PT1S") div xs:dayTimeDuration("PT3S")`, `-1 div 3`},
		{`xs:yearMonthDuration("P1M") div xs:yearMonthDuration("P3M")`, `1 div 3`},
		{`xs:yearMonthDuration("P1Y") div xs:yearMonthDuration("P7M")`, `12 div 7`},
		{`xs:yearMonthDuration("-P1M") div xs:yearMonthDuration("P3M")`, `-1 div 3`},
	}
	for _, c := range cases {
		d := evalDurDiv(t, c.dur)
		n := evalDurDiv(t, c.num)
		if d.Rat().Cmp(n.Rat()) != 0 {
			t.Errorf("%s = %s but %s = %s; one processor, two answers for the "+
				"same quotient", c.dur, d.Rat().RatString(), c.num, n.Rat().RatString())
		}
		if ds, ns := d.String(), n.String(); ds != ns {
			t.Errorf("%s prints %q but %s prints %q", c.dur, ds, c.num, ns)
		}
	}
}

// The user-visible symptoms: values that print identically compare unequal,
// and a difference that prints "0" is not zero.
func TestDurationDivideComparesEqualToNumericDivide(t *testing.T) {
	eqs := []string{
		`(xs:dayTimeDuration("PT1S") div xs:dayTimeDuration("PT3S")) eq (1 div 3)`,
		`((xs:dayTimeDuration("PT1S") div xs:dayTimeDuration("PT3S")) - (1 div 3)) eq 0`,
		`(xs:yearMonthDuration("P1M") div xs:yearMonthDuration("P3M")) eq (1 div 3)`,
		`((xs:yearMonthDuration("P1M") div xs:yearMonthDuration("P3M")) - (1 div 3)) eq 0`,
		// Terminating ratios were always equal; they must remain so.
		`(xs:dayTimeDuration("PT1S") div xs:dayTimeDuration("PT2S")) eq 0.5`,
		`(xs:yearMonthDuration("P1Y") div xs:yearMonthDuration("P1M")) eq 12`,
	}
	for _, e := range eqs {
		if !evalDurDivBool(t, e) {
			t.Errorf("%s = false, want true", e)
		}
	}

	// Round-tripping through multiplication must behave like the numeric path
	// too: rounded thirds do not recover 1.
	dur := evalDurDiv(t,
		`(xs:dayTimeDuration("PT1S") div xs:dayTimeDuration("PT3S")) * 3`).String()
	num := evalDurDiv(t, `(1 div 3) * 3`).String()
	if dur != num {
		t.Errorf("(PT1S div PT3S)*3 = %q but (1 div 3)*3 = %q", dur, num)
	}
}

// Very large and very small ratios: the normalization must not overflow, lose
// a large integral quotient, or flush a small one to zero when it is
// representable at the supported precision.
func TestDurationDivideExtremeRatios(t *testing.T) {
	// A huge exact ratio: 1000 days in seconds.
	if got := evalDurDiv(t,
		`xs:dayTimeDuration("P1000D") div xs:dayTimeDuration("PT1S")`).Rat(); got.Cmp(big.NewRat(86400000, 1)) != 0 {
		t.Errorf("P1000D div PT1S = %s, want 86400000", got.RatString())
	}
	// Its reciprocal is far below 1e-18 and rounds to zero at the supported
	// precision -- exactly as the same quotient does through ordinary
	// division, which is the property under test.
	small := evalDurDiv(t, `xs:dayTimeDuration("PT1S") div xs:dayTimeDuration("P1000D")`)
	ref := evalDurDiv(t, `1 div 86400000`)
	if small.Rat().Cmp(ref.Rat()) != 0 {
		t.Errorf("PT1S div P1000D = %s but 1 div 86400000 = %s",
			small.Rat().RatString(), ref.Rat().RatString())
	}
	// A sub-second exact ratio keeps full exactness.
	if got := evalDurDiv(t,
		`xs:dayTimeDuration("PT0.001S") div xs:dayTimeDuration("PT0.002S")`).Rat(); got.Cmp(big.NewRat(1, 2)) != 0 {
		t.Errorf("PT0.001S div PT0.002S = %s, want 1/2", got.RatString())
	}
	// Large yearMonth ratio.
	if got := evalDurDiv(t,
		`xs:yearMonthDuration("P100000Y") div xs:yearMonthDuration("P1M")`).Rat(); got.Cmp(big.NewRat(1200000, 1)) != 0 {
		t.Errorf("P100000Y div P1M = %s, want 1200000", got.RatString())
	}
}
