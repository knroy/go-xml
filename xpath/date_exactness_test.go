package xpath

import "testing"

// The tests here pin four places where an exact value was narrowed to a
// machine integer without first proving it fit. Each asserts the RESULT, not
// merely that no error escaped: the failure mode of a silent narrowing is a
// confidently wrong answer, so a test that only checks for an error would
// have passed against every one of these bugs.

// TestYearMonthDurationCompareNearIntLimits covers the comparison of two
// year-month durations whose month counts sit near opposite ends of the int
// range.
//
// The ordering was decided by subtracting one month count from the other and
// taking the sign. The difference between the two extremes is twice the
// range, so it wrapped, and the larger duration compared as the smaller: the
// engine reported that P768614336404564650Y was NOT greater than its own
// negation. Ordering is a question about the two operands, and comparing them
// directly cannot overflow.
func TestYearMonthDurationCompareNearIntLimits(t *testing.T) {
	// 768614336404564650 years is 9223372036854775800 months, eight short of
	// the int64 maximum. The two operands together span nearly twice the
	// range, which is what the subtraction could not hold.
	const big = `xs:yearMonthDuration("P768614336404564650Y")`
	const negBig = `xs:yearMonthDuration("-P768614336404564650Y")`

	cases := []struct {
		expr string
		want string
	}{
		{big + " gt " + negBig, "true"},
		{big + " lt " + negBig, "false"},
		{negBig + " lt " + big, "true"},
		{negBig + " gt " + big, "false"},
		{big + " ge " + negBig, "true"},
		{negBig + " le " + big, "true"},
		{big + " eq " + big, "true"},
		{big + " eq " + negBig, "false"},
		// A pair that never needed the extra range still has to agree.
		{`xs:yearMonthDuration("P2Y") gt xs:yearMonthDuration("P1Y")`, "true"},
		{`xs:yearMonthDuration("-P2Y") lt xs:yearMonthDuration("-P1Y")`, "true"},
	}
	for _, c := range cases {
		if got := evalOne(t, NewContext(nil, Builtins()), c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// max() orders through the same comparison, so it pins the fix from the
	// other side: the larger of the pair has to be the one selected.
	if got := evalOne(t, NewContext(nil, Builtins()),
		`string(max(( `+big+`, `+negBig+` )))`); got != "P768614336404564650Y" {
		t.Errorf("max of the pair = %q, want P768614336404564650Y", got)
	}
}

// TestDurationComponentsWithHugeMagnitudes covers the component accessors on
// durations whose second count is far outside int64.
//
// Every accessor but days-from-duration reduces its quotient modulo its own
// unit, so the answer is small no matter how large the duration is. The
// quotient was narrowed to int64 BEFORE that reduction, which discarded
// exactly the high bits the reduction needed. The modulo is now taken in big
// arithmetic and only the reduced value is converted.
func TestDurationComponentsWithHugeMagnitudes(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		// 10000000000000000000 hours exceeds int64 (max ~9.22e18). Truncated
		// to int64 the count became 553255926290448384, whose remainder mod
		// 24 is 0 — so the engine answered "0 hours" for a duration with
		// sixteen of them.
		{`hours-from-duration(xs:dayTimeDuration("PT10000000000000000000H"))`, "16"},
		{`hours-from-duration(xs:dayTimeDuration("-PT10000000000000000000H"))`, "-16"},
		// Likewise for minutes and seconds.
		{`minutes-from-duration(xs:dayTimeDuration("PT10000000000000000000M"))`, "40"},
		{`minutes-from-duration(xs:dayTimeDuration("-PT10000000000000000000M"))`, "-40"},
		{`seconds-from-duration(xs:dayTimeDuration("PT10000000000000000000S"))`, "40"},
		// The seconds accessor must keep the fraction the truncation drops,
		// and take it against the exact quotient rather than the reduced one.
		{`seconds-from-duration(xs:dayTimeDuration("PT10000000000000000000.25S"))`, "40.25"},
		{`seconds-from-duration(xs:dayTimeDuration("-PT10000000000000000000.25S"))`, "-40.25"},
		// The day count has no modulo to shrink it, so it must come back as
		// the exact arbitrary-precision integer it is. Narrowed to int64 it
		// wrapped negative: a positive duration reported negative days.
		{`days-from-duration(xs:dayTimeDuration("P10000000000000000000D"))`,
			"10000000000000000000"},
		{`days-from-duration(xs:dayTimeDuration("-P10000000000000000000D"))`,
			"-10000000000000000000"},
		// A day count far beyond any machine width stays exact.
		{`days-from-duration(xs:dayTimeDuration("P100000000000000000000000000000D"))`,
			"100000000000000000000000000000"},
		// Ordinary magnitudes are unchanged.
		{`days-from-duration(xs:dayTimeDuration("P3DT4H5M6S"))`, "3"},
		{`hours-from-duration(xs:dayTimeDuration("P3DT4H5M6S"))`, "4"},
		{`minutes-from-duration(xs:dayTimeDuration("P3DT4H5M6S"))`, "5"},
		{`seconds-from-duration(xs:dayTimeDuration("P3DT4H5M6.5S"))`, "6.5"},
		{`hours-from-duration(xs:dayTimeDuration("-P3DT4H5M6S"))`, "-4"},
		{`seconds-from-duration(xs:dayTimeDuration("-P3DT4H5M6.5S"))`, "-6.5"},
	}
	for _, c := range cases {
		if got := evalOne(t, NewContext(nil, Builtins()), c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestTimezoneOffsetRangeCheckPrecedesNarrowing covers the ±14:00 bound on the
// second argument of the adjust-*-to-timezone functions.
//
// The offset was narrowed to int64 and divided by 60 before being compared
// against the bound, so a second count whose low 64 bits happened to land
// inside the range was accepted. An offset of four trillion years was
// silently taken as +05:00 — not an error, an ordinary-looking wrong answer.
// The comparison is now made on the exact value.
func TestTimezoneOffsetRangeCheckPrecedesNarrowing(t *testing.T) {
	ctx := NewContext(nil, Builtins())

	// 129127208515966879312 = 7*2^64 + 18000. Truncated to int64 it is 18000
	// seconds, which is exactly +05:00 and passes the bound.
	const wrapping = `xs:dayTimeDuration("PT129127208515966879312S")`
	for _, expr := range []string{
		`adjust-dateTime-to-timezone(xs:dateTime("2000-01-01T00:00:00Z"), ` + wrapping + `)`,
		`adjust-date-to-timezone(xs:date("2000-01-01Z"), ` + wrapping + `)`,
		`adjust-time-to-timezone(xs:time("00:00:00Z"), ` + wrapping + `)`,
		`adjust-dateTime-to-timezone(xs:dateTime("2000-01-01T00:00:00Z"), ` +
			`xs:dayTimeDuration("-PT129127208515966879312S"))`,
	} {
		if _, err := evalString(t, expr); err == nil {
			t.Errorf("%s: accepted an out-of-range timezone offset", expr)
		} else if !contains(err.Error(), "FODT0003") {
			t.Errorf("%s: error %v, want FODT0003", expr, err)
		}
	}

	// Offsets just inside and just outside the bound decide correctly.
	inRange := []struct{ expr, want string }{
		{`adjust-dateTime-to-timezone(xs:dateTime("2000-01-01T00:00:00Z"), xs:dayTimeDuration("PT14H"))`,
			"2000-01-01T14:00:00+14:00"},
		{`adjust-dateTime-to-timezone(xs:dateTime("2000-01-01T00:00:00Z"), xs:dayTimeDuration("-PT14H"))`,
			"1999-12-31T10:00:00-14:00"},
		{`adjust-dateTime-to-timezone(xs:dateTime("2000-01-01T00:00:00Z"), xs:dayTimeDuration("PT5H"))`,
			"2000-01-01T05:00:00+05:00"},
		{`adjust-dateTime-to-timezone(xs:dateTime("2000-01-01T00:00:00Z"), xs:dayTimeDuration("PT0S"))`,
			"2000-01-01T00:00:00Z"},
	}
	for _, c := range inRange {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// One minute past the bound in either direction is refused.
	for _, expr := range []string{
		`adjust-dateTime-to-timezone(xs:dateTime("2000-01-01T00:00:00Z"), xs:dayTimeDuration("PT14H1M"))`,
		`adjust-dateTime-to-timezone(xs:dateTime("2000-01-01T00:00:00Z"), xs:dayTimeDuration("-PT14H1M"))`,
	} {
		if _, err := evalString(t, expr); err == nil {
			t.Errorf("%s: accepted an offset past +/-14:00", expr)
		}
	}
}

// TestScaledDurationOverflowUsesRoundedValue covers the month count produced
// by scaling a year-month duration.
//
// roundRat returned an int64 from an unbounded quotient. The overflow guard
// now runs on the exact rounded big.Int, which also closes a gap of one: the
// guard used to test the TRUNCATED value, so a product half a month below the
// limit truncated inside the range and rounded outside it.
func TestScaledDurationOverflowUsesRoundedValue(t *testing.T) {
	ctx := NewContext(nil, Builtins())

	// Products far outside the range are refused rather than wrapped.
	for _, expr := range []string{
		`xs:yearMonthDuration("P768614336404564650Y") * 1000`,
		`xs:yearMonthDuration("P768614336404564650Y") * -1000`,
		`xs:yearMonthDuration("P1M") * 1e300`,
		`xs:yearMonthDuration("P768614336404564650Y") div 0.0000001`,
	} {
		if _, err := evalString(t, expr); err == nil {
			t.Errorf("%s: accepted an overflowing month count", expr)
		}
	}

	// Ordinary scaling still rounds half toward positive infinity.
	cases := []struct{ expr, want string }{
		{`string(xs:yearMonthDuration("P5M") div 2)`, "P3M"},
		{`string(xs:yearMonthDuration("P5M") div -2)`, "-P2M"},
		{`string(xs:yearMonthDuration("P1Y") * 3)`, "P3Y"},
		{`string(xs:yearMonthDuration("P2Y6M") * 2)`, "P5Y"},
		{`string(xs:dayTimeDuration("PT1H") * 3)`, "PT3H"},
	}
	for _, c := range cases {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
