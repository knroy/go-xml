package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The 80-row sweep in untyped_conversion_sweep_test.go also needed a document
// constant and arrived naming it untypedConvDoc. This one landed first and is
// referenced by the three tests below, so the incoming family was given a
// "sweep" prefix rather than churning landed code; see that file's header.
//
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

// evalAtVersion compiles and evaluates expr at an explicit XPath version,
// against the library a stylesheet sees.
//
// The version must be set on BOTH the compile and the Context: the compiler
// decides which functions are in scope (fn:function-lookup is 3.0+), and the
// Context is what a function body consults at run time to choose between
// XPTY0117 and XPTY0004. Setting only one of the two silently tests the
// default version, which is exactly the hole these tests exist to cover.
func evalAtVersion(t *testing.T, doc, expr string, v Version) (string, error) {
	t.Helper()
	root := mustParse(t, doc)
	lib := NewLibrary(Builtins())
	RegisterXSLTFuncs(lib)
	ctx := NewContext(root, lib)
	ctx.Version = v
	comp, err := CompileVersion(expr, sweepConvNS{}, v)
	if err != nil {
		return "", err
	}
	seq, err := comp.Eval(ctx)
	if err != nil {
		return "", err
	}
	return renderSeq(seq), nil
}

func evalStrVersion(t *testing.T, doc, expr string, v Version) string {
	t.Helper()
	got, err := evalAtVersion(t, doc, expr, v)
	if err != nil {
		t.Fatalf("eval %q at %v: %v", expr, v, err)
	}
	return got
}

func evalErrVersion(t *testing.T, doc, expr string, v Version) error {
	t.Helper()
	_, err := evalAtVersion(t, doc, expr, v)
	return err
}

// TestUntypedQNameErrorCodeIsVersionGated pins the half of the XPTY0117 rule
// that is easiest to get wrong in the fixing direction.
//
// XPath 3.0 introduced XPTY0117 for casting xs:untypedAtomic to a
// namespace-sensitive type. XPath 2.0 has no such code and reports the general
// type error, and QT3 asserts both in cases that differ only by version. A fix
// that raised XPTY0117 unconditionally would be correct at 3.0 and wrong at
// 2.0, and nothing in the 80-row sweep -- which runs entirely at 3.1 -- would
// notice.
func TestUntypedQNameErrorCodeIsVersionGated(t *testing.T) {
	const doc = `<r qn="r"/>`
	for _, c := range []struct {
		version Version
		expr    string
		want    string
	}{
		// fn:function-lookup does not exist before 3.0, so only fn:error and
		// the QName accessors can be asked the 2.0 question.
		{XPath20, `error(/r/@qn)`, "XPTY0004"},
		{XPath20, `prefix-from-QName(/r/@qn)`, "XPTY0004"},
		{XPath30, `error(/r/@qn)`, "XPTY0117"},
		{XPath30, `prefix-from-QName(/r/@qn)`, "XPTY0117"},
		{XPath30, `function-lookup(/r/@qn, 1)`, "XPTY0117"},
		{XPath31, `error(/r/@qn)`, "XPTY0117"},
		{XPath31, `function-lookup(/r/@qn, 1)`, "XPTY0117"},
	} {
		err := evalErrVersion(t, doc, c.expr, c.version)
		if err == nil {
			t.Errorf("%s at %v was accepted; an untyped value is not an xs:QName",
				c.expr, c.version)
			continue
		}
		if got := xdm.ErrorCode(err); got != c.want {
			t.Errorf("%s at %v: code %q, want %q (%v)",
				c.expr, c.version, got, c.want, err)
		}
	}
}

// TestWrongTypeForQNameStaysXPTY0004 pins the other boundary of the same rule.
//
// XPath 3.1 3.1.5.2 gives XPTY0117 to xs:untypedAtomic and to nothing else: a
// value of a genuinely wrong type is never cast at all, and falls through to
// the clause that closes the section -- "if the resulting value does not match
// the expected type ... a type error is raised [err:XPTY0004]". Reporting a
// conversion failure for a value no conversion was ever attempted on would
// describe the wrong fault, and is the obvious over-reach when widening the
// untypedAtomic case.
func TestWrongTypeForQNameStaysXPTY0004(t *testing.T) {
	const doc = `<r qn="r"/>`
	for _, expr := range []string{
		`error(42)`,
		`error("err:FORG0001")`,
		`function-lookup(42, 1)`,
		`function-lookup("fn:abs", 1)`,
		`prefix-from-QName(42)`,
	} {
		err := evalErrVersion(t, doc, expr, XPath31)
		if err == nil {
			t.Errorf("%s was accepted; the declared type is xs:QName", expr)
			continue
		}
		if got := xdm.ErrorCode(err); got != "XPTY0004" {
			t.Errorf("%s: code %q, want XPTY0004 -- XPTY0117 is for "+
				"xs:untypedAtomic only, and no cast is attempted here", expr, got)
		}
	}
}

// TestFunctionLookupArityCastsToInteger pins the c3 fix: $arity is declared
// xs:integer, so an untypedAtomic is cast to xs:integer rather than to the
// xs:double argNumber used to produce. A lexical that is not a valid
// xs:integer is a cast that was attempted and failed, which is FORG0001 -- the
// value is at fault, not the type.
func TestFunctionLookupArityCastsToInteger(t *testing.T) {
	const doc = `<r i="1" half="1.5" bad="zzz"/>`
	// The valid lexical converts and the lookup proceeds.
	if got := evalStrVersion(t, doc,
		`function-lookup(xs:QName("fn:abs"), /r/@i)(-3)`, XPath31); got != "3" {
		t.Errorf(`function-lookup(..., /r/@i)(-3) = %q, want "3"`, got)
	}
	for _, c := range []struct{ expr, want string }{
		{`function-lookup(xs:QName("fn:abs"), /r/@half)`, "FORG0001"},
		{`function-lookup(xs:QName("fn:abs"), /r/@bad)`, "FORG0001"},
		// An xs:double really is the wrong type for an xs:integer parameter,
		// and stays a type error: only untypedAtomic is cast.
		{`function-lookup(xs:QName("fn:abs"), 1.5e0)`, "XPTY0004"},
	} {
		err := evalErrVersion(t, doc, c.expr, XPath31)
		if err == nil {
			t.Errorf("%s was accepted; %s expected", c.expr, c.want)
			continue
		}
		if got := xdm.ErrorCode(err); got != c.want {
			t.Errorf("%s: code %q, want %q (%v)", c.expr, got, c.want, err)
		}
	}
}
