package xpath

import (
	"strings"
	"testing"
)

// TestDateTimeStampConstructor covers xs:dateTimeStamp, which was missing
// altogether: the constructor function, the type name in cast and "instance
// of" position, and the explicitTimezone="required" facet that distinguishes
// it from xs:dateTime.
//
// The facet has to be enforced in two places. The constructor is one; a cast
// does not go through it, so "castable as xs:dateTimeStamp" answered true for
// a value with no timezone until the cast path learned the facet too.
func TestDateTimeStampConstructor(t *testing.T) {
	for _, c := range []struct{ expr, want string }{
		// The constructor keeps the timezone it was given.
		{`string(xs:dateTimeStamp("2011-07-28T12:34:56-08:00"))`,
			"2011-07-28T12:34:56-08:00"},
		{`exists(timezone-from-dateTime(xs:dateTimeStamp("2011-07-28T12:34:56-08:00")))`,
			"true"},
		// Casting from xs:date carries the date's timezone over.
		{`string(xs:dateTimeStamp(xs:date("2011-07-28+01:00")))`,
			"2011-07-28T00:00:00+01:00"},
		// It is a subtype of xs:dateTime, and remembers what it was built as.
		{`xs:dateTimeStamp("2011-07-28T12:34:56Z") instance of xs:dateTime`, "true"},
		{`xs:dateTimeStamp("2011-07-28T12:34:56Z") instance of xs:dateTimeStamp`, "true"},
		// The facet decides castability, in both directions.
		{`xs:dateTime("2011-07-28T12:34:56-08:00") castable as xs:dateTimeStamp`, "true"},
		{`xs:dateTime("2011-07-28T12:34:56") castable as xs:dateTimeStamp`, "false"},
	} {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// A value with no timezone is outside the value space, which is
	// FORG0001 rather than a type error.
	for _, expr := range []string{
		`xs:dateTimeStamp("2011-07-28T12:34:56")`,
		`xs:dateTimeStamp(xs:dateTime("2011-07-28T12:34:56"))`,
		`xs:dateTime("2011-07-28T12:34:56") cast as xs:dateTimeStamp`,
	} {
		err := evalErr(t, testDoc, expr)
		if err == nil {
			t.Errorf("%s should have failed", expr)
			continue
		}
		if !strings.Contains(err.Error(), "FORG0001") {
			t.Errorf("%s: error %q does not cite FORG0001", expr, err)
		}
	}
}
