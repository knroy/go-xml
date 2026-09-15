package xpath

import (
	"strings"
	"testing"
)

// untypedConvDoc is the shape the bug was reported against: an XSpec report
// whose report/@date carries a date as ordinary attribute content. An
// unvalidated document's attributes atomize to xs:untypedAtomic.
const untypedConvDoc = `<report dt="2026-08-26T10:00:00" d="2026-08-26" ` +
	`t="10:00:00" dur="P5D" bad="abc"/>`

// TestUntypedAtomicFunctionConversion covers XPath 3.1 §3.1.5.2: an argument
// of type xs:untypedAtomic supplied to a parameter whose declared type is an
// atomic type is CAST to that type, not refused.
//
// fn:format-dateTime, fn:format-date, fn:format-time and the duration
// component accessors read their argument through helpers that atomize but do
// not cast, so passing an attribute node raised XPTY0004 where the rule
// requires a conversion. The component accessors (year-from-dateTime and
// friends) already cast via dateAccessorArg and are included here so the fix
// cannot be mistaken for the reason they work.
func TestUntypedAtomicFunctionConversion(t *testing.T) {
	for _, c := range []struct{ expr, want string }{
		// The four that were broken.
		{`format-dateTime(/report/@dt,"[Y]")`, "2026"},
		{`format-date(/report/@d,"[Y]")`, "2026"},
		{`format-time(/report/@t,"[H01]")`, "10"},
		{`days-from-duration(/report/@dur)`, "5"},
		// The other duration components go through the same helper.
		{`years-from-duration(/report/@dur)`, "0"},
		{`hours-from-duration(/report/@dur)`, "0"},
		// Already working; these must not regress.
		{`year-from-dateTime(/report/@dt)`, "2026"},
		{`string(adjust-dateTime-to-timezone(/report/@dt,()))`,
			"2026-08-26T10:00:00"},
		{`string(dateTime(/report/@d,/report/@t))`, "2026-08-26T10:00:00"},
	} {
		if got := evalStrXSLT(t, untypedConvDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestUntypedAtomicConversionRefusesOtherTypes pins the half of §3.1.5.2 that
// is a refusal rather than a conversion.
//
// The clause casts xs:untypedAtomic and promotes numerics and xs:anyURI. An
// xs:string is neither, so a string supplied where xs:dateTime? is declared
// stays XPTY0004. Without this the fix would read as "accept anything
// string-shaped", which would make format-dateTime("...") legal and silently
// widen every declared signature it touches.
func TestUntypedAtomicConversionRefusesOtherTypes(t *testing.T) {
	for _, expr := range []string{
		`format-dateTime("2026-08-26T10:00:00","[Y]")`,
		`format-date("2026-08-26","[Y]")`,
		`format-time("10:00:00","[H01]")`,
		`days-from-duration("P5D")`,
		// A genuinely wrong type, not merely a string. (format-dateTime
		// accepts an xs:date today, a separate pre-existing leniency this
		// change neither introduces nor fixes, so it is not asserted here.)
		`days-from-duration(xs:dateTime("2026-08-26T10:00:00"))`,
	} {
		err := evalErrXSLT(t, untypedConvDoc, expr)
		if err == nil {
			t.Errorf("%s was accepted; the declared type forbids it", expr)
			continue
		}
		if !strings.Contains(err.Error(), "XPTY0004") {
			t.Errorf("%s: error %q does not cite XPTY0004", expr, err)
		}
	}
}

// TestUntypedAtomicConversionFailureIsFORG0001 pins the error code of a cast
// that is attempted and fails.
//
// The conversion IS defined for xs:untypedAtomic, so when it fails what was
// wrong is the value, not the type: FORG0001, not XPTY0004. QT3 pins the same
// distinction for the same rule on fn:min -- K-SeqMINFunc-35 requires
// min(xs:untypedAtomic("three")) to be FORG0001 -- and the two codes tell a
// caller whether the stylesheet or the data is at fault.
func TestUntypedAtomicConversionFailureIsFORG0001(t *testing.T) {
	for _, expr := range []string{
		`format-dateTime(/report/@bad,"[Y]")`,
		`format-date(/report/@bad,"[Y]")`,
		`format-time(/report/@bad,"[H01]")`,
		`days-from-duration(/report/@bad)`,
	} {
		err := evalErrXSLT(t, untypedConvDoc, expr)
		if err == nil {
			t.Errorf("%s was accepted; %q is not a valid value", expr, "abc")
			continue
		}
		if !strings.Contains(err.Error(), "FORG0001") {
			t.Errorf("%s: error %q does not cite FORG0001; a failed cast is "+
				"a bad value, not a type mismatch", expr, err)
		}
	}
}
