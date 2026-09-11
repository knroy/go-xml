package xpath

import "testing"

// TestAdjustTimezoneRequiresIntegralMinutes covers the second argument of the
// three fn:adjust-*-to-timezone functions.
//
// F&O 3.0 (functions-and-operators-rec30.xml lines 16654, 16807 and 16955):
// err:FODT0003 is raised if $timezone "is less than -PT14H or greater than
// PT14H or is not an integral number of minutes".
//
// The check tested integral SECONDS (secs.IsInt()) and then truncated the
// remainder away, so PT1M30S — a whole number of seconds, not of minutes —
// was accepted and silently became +00:01. The error message already said
// "must be a whole number of minutes"; the message was right and the
// condition was wrong.
func TestAdjustTimezoneRequiresIntegralMinutes(t *testing.T) {
	ctx := NewContext(nil, Builtins())

	// The three functions and the value each adjusts, so every variant is
	// covered rather than only the dateTime one they share code with.
	variants := []struct{ fn, arg string }{
		{"adjust-dateTime-to-timezone", `xs:dateTime("2002-03-07T10:00:00Z")`},
		{"adjust-date-to-timezone", `xs:date("2002-03-07Z")`},
		{"adjust-time-to-timezone", `xs:time("10:00:00Z")`},
	}

	// A whole number of seconds that is not a whole number of minutes.
	for _, v := range variants {
		expr := v.fn + `(` + v.arg + `, xs:dayTimeDuration("PT1M30S"))`
		if _, err := evalString(t, expr); err == nil {
			t.Errorf("%s: accepted a timezone that is not an integral number of minutes", expr)
		} else if !contains(err.Error(), "FODT0003") {
			t.Errorf("%s: error %v, want FODT0003", expr, err)
		}
	}

	// Fractional seconds were already refused; they must stay refused.
	for _, v := range variants {
		expr := v.fn + `(` + v.arg + `, xs:dayTimeDuration("PT30.5S"))`
		if _, err := evalString(t, expr); err == nil {
			t.Errorf("%s: accepted a fractional-second timezone", expr)
		} else if !contains(err.Error(), "FODT0003") {
			t.Errorf("%s: error %v, want FODT0003", expr, err)
		}
	}

	// Controls: a whole minute is accepted, and the ±PT14H bound stays
	// inclusive. Narrowing the condition must not narrow what is legal.
	accepted := []struct{ expr, want string }{
		{`adjust-dateTime-to-timezone(xs:dateTime("2002-03-07T10:00:00Z"), xs:dayTimeDuration("PT1M"))`,
			"2002-03-07T10:01:00+00:01"},
		{`adjust-dateTime-to-timezone(xs:dateTime("2002-03-07T10:00:00Z"), xs:dayTimeDuration("PT14H"))`,
			"2002-03-08T00:00:00+14:00"},
		{`adjust-dateTime-to-timezone(xs:dateTime("2002-03-07T10:00:00Z"), xs:dayTimeDuration("-PT14H"))`,
			"2002-03-06T20:00:00-14:00"},
		{`adjust-time-to-timezone(xs:time("10:00:00Z"), xs:dayTimeDuration("PT1M"))`,
			"10:01:00+00:01"},
	}
	for _, c := range accepted {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// The range check must stay BEFORE the narrowing: this offset is an
	// exact number of minutes, so it reaches the bound rather than being
	// caught by the remainder test, and its low 64 bits are +05:00.
	const wrapping = `xs:dayTimeDuration("PT129127208515966879312S")`
	for _, v := range variants {
		expr := v.fn + `(` + v.arg + `, ` + wrapping + `)`
		if _, err := evalString(t, expr); err == nil {
			t.Errorf("%s: accepted an out-of-range timezone offset", expr)
		} else if !contains(err.Error(), "FODT0003") {
			t.Errorf("%s: error %v, want FODT0003", expr, err)
		}
	}
}
