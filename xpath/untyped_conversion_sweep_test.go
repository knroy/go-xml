package xpath

// Untyped-atomic function conversion, XPath 3.1 3.1.5.2.
//
// The function conversion rules atomize each argument and then cast every
// xs:untypedAtomic item in the result to the parameter's declared type. An
// attribute in an untyped tree atomizes to xs:untypedAtomic, so passing one
// where a function declares xs:date, xs:duration, xs:double and so on must
// convert, not refuse.
//
// This file is the full 80-row sweep, and on this branch it is EXPECTED TO
// PASS: it arrived from branch untyped-sweep as the specification of a fix
// that had not landed, and all three defect classes it names have since been
// fixed -- c1 (the twelve missing casts) at c0b1e1b, c2 (the four wrong error
// codes) and c3 (the one cast to the wrong target type) here.
//
// It is kept as the regression guard rather than deleted. Do NOT skip these
// tests, loosen an assertion, or change an expectation to match new output in
// order to make a branch green: every row states what the Recommendation
// requires, so a row that goes red is a regression in the engine, not a stale
// test. The controls matter most -- sixty-three pairs convert correctly today
// and the way this area regresses is a central change that breaks them.
//
// Naming note: the four identifiers below carry a "sweep" prefix because
// untyped_function_conversion_test.go, which landed with c1, already declares
// untypedConvDoc in this package. The incoming family was renamed rather than
// the landed constant -- see that file.
//
// Measurement method. The 255-row table in funcspec_table.go declares 80
// (function, parameter) pairs whose type is one of the castable atomic types
// (xs:dateTime, xs:date, xs:time, xs:duration and its two subtypes, xs:QName,
// xs:anyURI, xs:decimal, xs:integer, xs:double, xs:boolean, in any occurrence
// spelling), across 71 distinct functions. Every one of the 80 is represented
// below.
//
// Telling a missing conversion apart from a conversion that then failed on the
// value matters, because both surface as an error. They are separated here by
// running each expression twice, against a document whose attribute holds a
// VALID lexical for the declared type and against one whose attribute holds
// "zzz". A function that converts reports a different diagnostic for the two
// -- it succeeds on the first and cites FORG0001, or quotes "zzz", on the
// second. A function that never converted reports the SAME refusal for both,
// because it rejected the type before ever looking at the value.

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// sweepConvDoc is an untyped tree: every attribute atomizes to
// xs:untypedAtomic, and every one holds a valid lexical for the type the
// parameter under test declares.
const sweepConvDoc = `<?xml version="1.0"?>
<r dt="2026-08-26T12:34:56" d="2026-08-26" t="12:34:56" dur="P1Y2M3DT4H"
   dtd="PT1H" ymd="P1Y2M" qn="r" u="http://example.com/a" dec="1.5"
   i="2" dbl="0.5" b="true"/>`

// sweepConvBadDoc is the same shape with lexicals that are invalid for
// every one of those types. It is the discriminator described above.
const sweepConvBadDoc = `<?xml version="1.0"?>
<r dt="zzz" d="zzz" t="zzz" dur="zzz"
   dtd="zzz" ymd="zzz" qn="1bad" u="::::" dec="zzz"
   i="zzz" dbl="zzz" b="zzz"/>`

// sweepConvNS resolves the four namespaces the expressions below name.
type sweepConvNS struct{}

func (sweepConvNS) ResolvePrefix(p string) (string, bool) {
	switch p {
	case "xs":
		return xdm.NSXS, true
	case "fn":
		return xdm.NSFN, true
	case "math":
		return xdm.NSMath, true
	case "array":
		return xdm.NSArray, true
	}
	return "", false
}
func (sweepConvNS) DefaultElementNamespace() string  { return "" }
func (sweepConvNS) DefaultFunctionNamespace() string { return xdm.NSFN }

// evalSweepConv evaluates expr at XPath 3.1 against doc, through the same
// compile-and-evaluate path a stylesheet takes. The library is the XPath
// builtins plus the XSLT additions, because fn:format-date, fn:format-time
// and fn:format-dateTime live there.
func evalSweepConv(t *testing.T, doc, expr string) (string, error) {
	t.Helper()
	root := mustParse(t, doc)
	lib := NewLibrary(Builtins())
	RegisterXSLTFuncs(lib)
	ctx := NewContext(root, lib)
	ctx.Version = XPath31
	comp, err := CompileVersion(expr, sweepConvNS{}, XPath31)
	if err != nil {
		return "", err
	}
	seq, err := comp.Eval(ctx)
	if err != nil {
		return "", err
	}
	return renderSeq(seq), nil
}

// TestUntypedConversionMissingCast covers the twelve (function, parameter)
// pairs that never apply the cast: six duration accessors and the three
// format-* functions at both of their arities. Each refuses an attribute with
// XPTY0004 even though the attribute's value is a valid lexical for the
// declared type, and refuses an invalid lexical with exactly the same message,
// which is what shows the value was never examined.
//
// Fixed; this is now a regression guard.
func TestUntypedConversionMissingCast(t *testing.T) {
	for _, c := range []struct{ name, ptype, expr, want string }{
		{"days-from-duration/1 p1", "xs:duration?", "days-from-duration(/r/@dur)", "3"},
		{"format-date/2 p1", "xs:date?", "format-date(/r/@d, \"[Y]\")", "2026"},
		{"format-date/5 p1", "xs:date?", "format-date(/r/@d, \"[Y]\", \"en\", (), ())", "2026"},
		{"format-dateTime/2 p1", "xs:dateTime?", "format-dateTime(/r/@dt, \"[Y]\")", "2026"},
		{"format-dateTime/5 p1", "xs:dateTime?", "format-dateTime(/r/@dt, \"[Y]\", \"en\", (), ())", "2026"},
		{"format-time/2 p1", "xs:time?", "format-time(/r/@t, \"[H01]\")", "12"},
		{"format-time/5 p1", "xs:time?", "format-time(/r/@t, \"[H01]\", \"en\", (), ())", "12"},
		{"hours-from-duration/1 p1", "xs:duration?", "hours-from-duration(/r/@dur)", "4"},
		{"minutes-from-duration/1 p1", "xs:duration?", "minutes-from-duration(/r/@dur)", "0"},
		{"months-from-duration/1 p1", "xs:duration?", "months-from-duration(/r/@dur)", "2"},
		{"seconds-from-duration/1 p1", "xs:duration?", "seconds-from-duration(/r/@dur)", "0"},
		{"years-from-duration/1 p1", "xs:duration?", "years-from-duration(/r/@dur)", "1"},
	} {
		got, err := evalSweepConv(t, sweepConvDoc, c.expr)
		if err != nil {
			t.Errorf("%s (%s): %s\n  raised %v\n  want %q -- the function conversion rules require the untypedAtomic to be cast to %s",
				c.name, c.ptype, c.expr, err, c.want, c.ptype)
			continue
		}
		if got != c.want {
			t.Errorf("%s (%s): %s = %q, want %q", c.name, c.ptype, c.expr, got, c.want)
		}
	}
}

// TestUntypedConversionMissingCastSeesTheValue is the other half of the same
// defect, and it is what proves the diagnosis rather than merely the symptom.
// Once the conversion is applied, an INVALID lexical must produce a diagnostic
// about the value -- FORG0001, or a message quoting the offending string --
// rather than the type refusal these functions give today for every input
// alike.
//
// Fixed; this is now a regression guard.
func TestUntypedConversionMissingCastSeesTheValue(t *testing.T) {
	for _, c := range []struct{ name, ptype, expr, want string }{
		{"days-from-duration/1 p1", "xs:duration?", "days-from-duration(/r/@dur)", "3"},
		{"format-date/2 p1", "xs:date?", "format-date(/r/@d, \"[Y]\")", "2026"},
		{"format-date/5 p1", "xs:date?", "format-date(/r/@d, \"[Y]\", \"en\", (), ())", "2026"},
		{"format-dateTime/2 p1", "xs:dateTime?", "format-dateTime(/r/@dt, \"[Y]\")", "2026"},
		{"format-dateTime/5 p1", "xs:dateTime?", "format-dateTime(/r/@dt, \"[Y]\", \"en\", (), ())", "2026"},
		{"format-time/2 p1", "xs:time?", "format-time(/r/@t, \"[H01]\")", "12"},
		{"format-time/5 p1", "xs:time?", "format-time(/r/@t, \"[H01]\", \"en\", (), ())", "12"},
		{"hours-from-duration/1 p1", "xs:duration?", "hours-from-duration(/r/@dur)", "4"},
		{"minutes-from-duration/1 p1", "xs:duration?", "minutes-from-duration(/r/@dur)", "0"},
		{"months-from-duration/1 p1", "xs:duration?", "months-from-duration(/r/@dur)", "2"},
		{"seconds-from-duration/1 p1", "xs:duration?", "seconds-from-duration(/r/@dur)", "0"},
		{"years-from-duration/1 p1", "xs:duration?", "years-from-duration(/r/@dur)", "1"},
	} {
		_, err := evalSweepConv(t, sweepConvBadDoc, c.expr)
		if err == nil {
			t.Errorf("%s (%s): %s on an invalid lexical should have failed", c.name, c.ptype, c.expr)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "FORG0001") && !strings.Contains(msg, "zzz") {
			t.Errorf("%s (%s): %s on an invalid lexical raised %q;\n  want a diagnostic about the VALUE (FORG0001, or one quoting \"zzz\").\n  A message that names neither means the cast was never attempted.",
				c.name, c.ptype, c.expr, msg)
		}
	}
}

// TestUntypedConversionWrongErrorCode covers the four pairs whose declared
// type is namespace-sensitive. XPath 3.1 3.1.5.2 is explicit that casting
// xs:untypedAtomic to a namespace-sensitive type is err:XPTY0117, not the
// general type error: the cast would need the in-scope namespaces of the
// expression rather than of the node, so the spec gives the case a code of its
// own. fn:prefix-from-QName and its two siblings already report XPTY0117 (see
// the controls below, and fn_qname.go); fn:error and fn:function-lookup reach
// the same situation through argAtomicOptional and argQName and report
// XPTY0004 instead.
//
// This is a wrong code rather than a missing conversion -- these calls are
// required to fail either way -- but it is the same gap in the same rule, and
// a conformance suite distinguishes the two codes.
//
// Fixed; this is now a regression guard.
func TestUntypedConversionWrongErrorCode(t *testing.T) {
	for _, c := range []struct{ name, ptype, expr string }{
		{"error/1 p1", "xs:QName?", "error(/r/@qn)"},
		{"error/2 p1", "xs:QName?", "error(/r/@qn, \"[Y]\")"},
		{"error/3 p1", "xs:QName?", "error(/r/@qn, \"[Y]\", ())"},
		{"function-lookup/2 p1", "xs:QName", "function-lookup(/r/@qn, 1)"},
	} {
		_, err := evalSweepConv(t, sweepConvDoc, c.expr)
		if err == nil {
			t.Errorf("%s (%s): %s should have failed", c.name, c.ptype, c.expr)
			continue
		}
		if !strings.Contains(err.Error(), "XPTY0117") {
			t.Errorf("%s (%s): %s raised %q, want XPTY0117 -- casting xs:untypedAtomic to a namespace-sensitive type has a code of its own",
				c.name, c.ptype, c.expr, err)
		}
	}
}

// TestUntypedConversionControlsSucceed is the control for the sixty pairs that
// convert correctly today. They MUST keep converting after the fix: applying
// the conversion centrally without noticing that most implementations already
// do it in their own body is the way this fix regresses working functions.
//
// These pass on this branch.
func TestUntypedConversionControlsSucceed(t *testing.T) {
	for _, c := range []struct{ name, ptype, expr, want string }{
		{"adjust-date-to-timezone/1 p1", "xs:date?", "adjust-date-to-timezone(/r/@d)", "2026-08-26Z"},
		{"adjust-date-to-timezone/2 p1", "xs:date?", "adjust-date-to-timezone(/r/@d, xs:dayTimeDuration(\"PT1H\"))", "2026-08-26+01:00"},
		{"adjust-date-to-timezone/2 p2", "xs:dayTimeDuration?", "adjust-date-to-timezone(xs:date(\"2026-08-26\"), /r/@dtd)", "2026-08-26+01:00"},
		{"adjust-dateTime-to-timezone/1 p1", "xs:dateTime?", "adjust-dateTime-to-timezone(/r/@dt)", "2026-08-26T12:34:56Z"},
		{"adjust-dateTime-to-timezone/2 p1", "xs:dateTime?", "adjust-dateTime-to-timezone(/r/@dt, xs:dayTimeDuration(\"PT1H\"))", "2026-08-26T12:34:56+01:00"},
		{"adjust-dateTime-to-timezone/2 p2", "xs:dayTimeDuration?", "adjust-dateTime-to-timezone(xs:dateTime(\"2026-08-26T12:34:56\"), /r/@dtd)", "2026-08-26T12:34:56+01:00"},
		{"adjust-time-to-timezone/1 p1", "xs:time?", "adjust-time-to-timezone(/r/@t)", "12:34:56Z"},
		{"adjust-time-to-timezone/2 p1", "xs:time?", "adjust-time-to-timezone(/r/@t, xs:dayTimeDuration(\"PT1H\"))", "12:34:56+01:00"},
		{"adjust-time-to-timezone/2 p2", "xs:dayTimeDuration?", "adjust-time-to-timezone(xs:time(\"12:34:56\"), /r/@dtd)", "12:34:56+01:00"},
		{"array:get/2 p2", "xs:integer", "array:get([1,2,3], /r/@i)", "2"},
		{"array:insert-before/3 p2", "xs:integer", "array:insert-before([1,2,3], /r/@i, ())", ""},
		{"array:put/3 p2", "xs:integer", "array:put([1,2,3], /r/@i, ())", ""},
		{"array:remove/2 p2", "xs:integer*", "array:remove([1,2,3], /r/@i)", ""},
		{"array:subarray/2 p2", "xs:integer", "array:subarray([1,2,3], /r/@i)", ""},
		{"array:subarray/3 p2", "xs:integer", "array:subarray([1,2,3], /r/@i, 1)", ""},
		{"array:subarray/3 p3", "xs:integer", "array:subarray([1,2,3], 1, /r/@i)", ""},
		{"codepoints-to-string/1 p1", "xs:integer*", "codepoints-to-string(/r/@i)", "\u0002"},
		{"dateTime/2 p1", "xs:date?", "dateTime(/r/@d, xs:time(\"12:34:56\"))", "2026-08-26T12:34:56"},
		{"dateTime/2 p2", "xs:time?", "dateTime(xs:date(\"2026-08-26\"), /r/@t)", "2026-08-26T12:34:56"},
		{"day-from-date/1 p1", "xs:date?", "day-from-date(/r/@d)", "26"},
		{"day-from-dateTime/1 p1", "xs:dateTime?", "day-from-dateTime(/r/@dt)", "26"},
		{"format-integer/2 p1", "xs:integer?", "format-integer(/r/@i, \"1\")", "2"},
		{"format-integer/3 p1", "xs:integer?", "format-integer(/r/@i, \"1\", \"en\")", "2"},
		{"hours-from-dateTime/1 p1", "xs:dateTime?", "hours-from-dateTime(/r/@dt)", "12"},
		{"hours-from-time/1 p1", "xs:time?", "hours-from-time(/r/@t)", "12"},
		{"insert-before/3 p2", "xs:integer", "insert-before((), /r/@i, ())", ""},
		{"math:acos/1 p1", "xs:double?", "math:acos(/r/@dbl)", "1.0471975511965976"},
		{"math:asin/1 p1", "xs:double?", "math:asin(/r/@dbl)", "0.5235987755982989"},
		{"math:atan/1 p1", "xs:double?", "math:atan(/r/@dbl)", "0.4636476090008061"},
		{"math:atan2/2 p1", "xs:double", "math:atan2(/r/@dbl, 1)", "0.4636476090008061"},
		{"math:atan2/2 p2", "xs:double", "math:atan2(1, /r/@dbl)", "1.1071487177940904"},
		{"math:cos/1 p1", "xs:double?", "math:cos(/r/@dbl)", "0.8775825618903728"},
		{"math:exp/1 p1", "xs:double?", "math:exp(/r/@dbl)", "1.6487212707001282"},
		{"math:exp10/1 p1", "xs:double?", "math:exp10(/r/@dbl)", "3.1622776601683795"},
		{"math:log/1 p1", "xs:double?", "math:log(/r/@dbl)", "-0.6931471805599453"},
		{"math:log10/1 p1", "xs:double?", "math:log10(/r/@dbl)", "-0.3010299956639812"},
		{"math:pow/2 p1", "xs:double?", "math:pow(/r/@dbl, 2)", "0.25"},
		{"math:sin/1 p1", "xs:double?", "math:sin(/r/@dbl)", "0.479425538604203"},
		{"math:sqrt/1 p1", "xs:double?", "math:sqrt(/r/@dbl)", "0.7071067811865476"},
		{"math:tan/1 p1", "xs:double?", "math:tan(/r/@dbl)", "0.5463024898437905"},
		{"minutes-from-dateTime/1 p1", "xs:dateTime?", "minutes-from-dateTime(/r/@dt)", "34"},
		{"minutes-from-time/1 p1", "xs:time?", "minutes-from-time(/r/@t)", "34"},
		{"month-from-date/1 p1", "xs:date?", "month-from-date(/r/@d)", "8"},
		{"month-from-dateTime/1 p1", "xs:dateTime?", "month-from-dateTime(/r/@dt)", "8"},
		{"remove/2 p2", "xs:integer", "remove((), /r/@i)", ""},
		{"round-half-to-even/2 p2", "xs:integer", "round-half-to-even(1, /r/@i)", "1"},
		{"round/2 p2", "xs:integer", "round(1, /r/@i)", "1"},
		{"seconds-from-dateTime/1 p1", "xs:dateTime?", "seconds-from-dateTime(/r/@dt)", "56"},
		{"seconds-from-time/1 p1", "xs:time?", "seconds-from-time(/r/@t)", "56"},
		{"subsequence/2 p2", "xs:double", "subsequence((), /r/@dbl)", ""},
		{"subsequence/3 p2", "xs:double", "subsequence((), /r/@dbl, 1)", ""},
		{"subsequence/3 p3", "xs:double", "subsequence((), 1, /r/@dbl)", ""},
		{"substring/2 p2", "xs:double", "substring(\"en\", /r/@dbl)", "en"},
		{"substring/3 p2", "xs:double", "substring(\"en\", /r/@dbl, 1)", "e"},
		{"substring/3 p3", "xs:double", "substring(\"en\", 1, /r/@dbl)", "e"},
		{"timezone-from-date/1 p1", "xs:date?", "timezone-from-date(/r/@d)", ""},
		{"timezone-from-dateTime/1 p1", "xs:dateTime?", "timezone-from-dateTime(/r/@dt)", ""},
		{"timezone-from-time/1 p1", "xs:time?", "timezone-from-time(/r/@t)", ""},
		{"year-from-date/1 p1", "xs:date?", "year-from-date(/r/@d)", "2026"},
		{"year-from-dateTime/1 p1", "xs:dateTime?", "year-from-dateTime(/r/@dt)", "2026"},
	} {
		got, err := evalSweepConv(t, sweepConvDoc, c.expr)
		if err != nil {
			t.Errorf("%s (%s): %s raised %v, want %q -- this function converts today and must keep doing so",
				c.name, c.ptype, c.expr, err, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("%s (%s): %s = %q, want %q", c.name, c.ptype, c.expr, got, c.want)
		}
	}
}

// TestUntypedConversionControlsNamespaceSensitive is the control for the three
// QName accessors that already report XPTY0117. They are the behaviour the
// four in TestUntypedConversionWrongErrorCode should be brought in line with,
// so a fix that changed THEM to XPTY0004 would be moving in the wrong
// direction.
//
// These pass on this branch.
func TestUntypedConversionControlsNamespaceSensitive(t *testing.T) {
	for _, c := range []struct{ name, ptype, expr string }{
		{"local-name-from-QName/1 p1", "xs:QName?", "local-name-from-QName(/r/@qn)"},
		{"namespace-uri-from-QName/1 p1", "xs:QName?", "namespace-uri-from-QName(/r/@qn)"},
		{"prefix-from-QName/1 p1", "xs:QName?", "prefix-from-QName(/r/@qn)"},
	} {
		_, err := evalSweepConv(t, sweepConvDoc, c.expr)
		if err == nil {
			t.Errorf("%s (%s): %s should have failed with XPTY0117", c.name, c.ptype, c.expr)
			continue
		}
		if !strings.Contains(err.Error(), "XPTY0117") {
			t.Errorf("%s (%s): %s raised %q, want XPTY0117", c.name, c.ptype, c.expr, err)
		}
	}
}

// TestUntypedConversionCastsToTheDeclaredType is the (c3) row: the cast HAPPENS
// but goes to the wrong target type.
//
// fn:function-lookup declares $arity as xs:integer, and the value is read
// through argNumber, which yields an xs:double; the call is then refused for
// not being an integer. So this is neither a missing cast nor a value error --
// it is a cast to the wrong type, and it is listed separately so a fix that
// merely makes SOMETHING convert does not tick it off.
//
// Fixed; this is now a regression guard. It was
// originally filed among the controls, which was wrong in a way worth
// recording: a failing "control" reads as a broken harness rather than as the
// defect being pinned, and the controls are the group that must stay green to
// prove the fix regressed nothing.
func TestUntypedConversionCastsToTheDeclaredType(t *testing.T) {
	for _, c := range []struct{ name, ptype, expr string }{
		{"function-lookup/2 p2", "xs:integer", "function-lookup(xs:QName(\"err\"), /r/@i)"},
	} {
		_, err := evalSweepConv(t, sweepConvDoc, c.expr)
		if err != nil {
			t.Errorf("%s (%s): %s raised %v; an untypedAtomic \"2\" converts to the xs:integer 2, so the call should reach the lookup",
				c.name, c.ptype, c.expr, err)
		}
	}
}
