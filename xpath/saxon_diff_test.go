package xpath

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The cases in this file were each run through Saxon-HE 12.4 and this engine
// side by side, and the "want" column is Saxon's actual output rather than a
// reading of the spec. Every one of them corresponds to a bug the W3C QT3
// suite surfaced; keeping them here means the fixes stay pinned without the
// 18MB suite or the GOXSLT_QT3 environment variable.
//
// To reproduce any single row by hand:
//
//	cat > /tmp/t.xsl <<'EOF'
//	<xsl:stylesheet version="2.0"
//	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
//	    xmlns:xs="http://www.w3.org/2001/XMLSchema">
//	  <xsl:output method="text"/>
//	  <xsl:template match="/"><xsl:value-of select="EXPRESSION"/></xsl:template>
//	</xsl:stylesheet>
//	EOF
//	echo '<r/>' > /tmp/r.xml
//	go run ./cmd/go-xml -xsl /tmp/t.xsl /tmp/r.xml
//	java -cp saxon.jar:xmlresolver.jar net.sf.saxon.Transform -s:/tmp/r.xml -xsl:/tmp/t.xsl

// saxonCase is one differential expectation.
type saxonCase struct {
	// why names the bug the row pins, so a failure says what broke.
	why  string
	expr string
	want string
}

var saxonAgreed = []saxonCase{
	// --- Casting table ------------------------------------------------------
	{"binary is not castable to a numeric",
		`xs:base64Binary('10010101') castable as xs:float`, "false"},
	{"binary is not castable to a numeric",
		`xs:base64Binary('10010101') castable as xs:decimal`, "false"},
	{"hexBinary and base64Binary are two encodings of the same octets",
		`xs:base64Binary(xs:hexBinary('0FB7'))`, "D7c="},
	{"hexBinary and base64Binary are two encodings of the same octets",
		`xs:hexBinary(xs:base64Binary('D7c='))`, "0FB7"},
	{"the canonical form of xs:hexBinary is upper case",
		`string(xs:hexBinary('0fb7'))`, "0FB7"},
	{"a string is castable to xs:QName if it is a legal lexical QName",
		`'ABC' castable as xs:QName`, "true"},
	{"a two-colon name is not a QName",
		`'a:b:c' castable as xs:QName`, "false"},

	// --- Derived type facets ------------------------------------------------
	{"xs:byte carries a range facet", `128 castable as xs:byte`, "false"},
	{"xs:byte carries a range facet", `-129 castable as xs:byte`, "false"},
	{"xs:byte carries a range facet", `127 castable as xs:byte`, "true"},
	{"xs:unsignedByte is bounded below at zero",
		`-1 castable as xs:unsignedByte`, "false"},
	{"xs:positiveInteger excludes zero",
		`0 castable as xs:positiveInteger`, "false"},
	{"xs:NCName forbids a colon", `'a:b' castable as xs:NCName`, "false"},
	{"xs:Name permits a colon", `'a:b' castable as xs:Name`, "true"},
	{"xs:normalizedString replaces tab, CR and LF with spaces",
		`string-to-codepoints(xs:normalizedString(codepoints-to-string((32,9,48,13,10,48))))`,
		"32,32,48,32,32,48"},
	{"xs:token additionally collapses runs and trims",
		`string-to-codepoints(xs:token(codepoints-to-string((32,9,48,13,10,48))))`,
		"48,32,48"},
	{"xs:token collapses interior whitespace", `xs:token('  a   b  ')`, "a b"},

	// --- instance of over derived types -------------------------------------
	{"a value built as xs:int is an xs:int", `xs:int(0) instance of xs:int`, "true"},
	{"xs:int is a subtype of xs:long", `xs:int(0) instance of xs:long`, "true"},
	{"xs:long is not a subtype of xs:int", `xs:long(0) instance of xs:int`, "false"},
	{"a plain integer literal is the parent of xs:int, not an instance",
		`1 instance of xs:int`, "false"},
	{"a value too large for xs:int is not one",
		`12678967543233 instance of xs:int`, "false"},
	{"every atomic value is an xs:anyAtomicType",
		`false() instance of xs:anyAtomicType`, "true"},
	{"the annotation must not disturb arithmetic", `xs:int(2) + 3`, "5"},
	{"the annotation must not disturb comparison", `xs:int(2) eq 2`, "true"},

	// --- Numeric lexical forms ----------------------------------------------
	{"XML Schema spells infinity INF", `'INF' castable as xs:double`, "true"},
	{"Go's scanner accepts Inf; XML Schema does not",
		`'Inf' castable as xs:double`, "false"},
	{"Go's scanner accepts Infinity; XML Schema does not",
		`'Infinity' castable as xs:double`, "false"},
	{"an xs:integer needs integer lexical syntax",
		`'3.0' castable as xs:long`, "false"},
	{"3.0 is a valid xs:decimal", `'3.0' castable as xs:decimal`, "true"},
	{"an exponent needs digits", `'1e' castable as xs:double`, "false"},

	// --- Rounding -----------------------------------------------------------
	{"rounding a negative double toward zero yields negative zero",
		`round(xs:double('-0.2'))`, "-0"},
	{"xs:decimal has no signed zero", `round(-0.2)`, "0"},
	{"a huge precision must not be used as a literal exponent of ten",
		`round-half-to-even(3.567812, 4294967296)`, "3.567812"},
	{"round-half-to-even breaks ties toward even", `round-half-to-even(2.5)`, "2"},
	{"round breaks ties upward", `round(2.5)`, "3"},

	// --- Durations ----------------------------------------------------------
	{"a zero-magnitude duration carries no sign",
		`string(xs:duration('-PT0S'))`, "PT0S"},
	{"a zero-magnitude duration carries no sign",
		`string(xs:yearMonthDuration('-P0M'))`, "P0M"},
	{"a non-zero duration keeps its sign",
		`string(xs:dayTimeDuration('-PT1S'))`, "-PT1S"},
	{"dividing two durations yields their ratio",
		`xs:dayTimeDuration('PT2H') div xs:dayTimeDuration('PT1H')`, "2"},
	{"dividing two durations yields their ratio",
		`xs:yearMonthDuration('P2Y') div xs:yearMonthDuration('P1Y')`, "2"},

	// --- Dates --------------------------------------------------------------
	{"24:00:00 normalises to midnight the next day",
		`string(xs:dateTime('1999-12-31T24:00:00'))`, "2000-01-01T00:00:00"},
	{"24:00:00 normalises into a leap day",
		`string(xs:dateTime('2000-02-28T24:00:00'))`, "2000-02-29T00:00:00"},
	{"an xs:time has no date to roll over into",
		`string(xs:time('24:00:00'))`, "00:00:00"},
	{"a Gregorian cast clears the components the target does not name",
		`xs:gYear(xs:dateTime('2002-11-23T23:12:23Z')) eq xs:gYear('2002Z')`, "true"},
	{"a Gregorian cast keeps the timezone",
		`string(xs:gYear(xs:dateTime('2002-11-23T23:12:23Z')))`, "2002Z"},

	// --- Sequences ----------------------------------------------------------
	{"a double and a decimal compare by promotion",
		`count(distinct-values((1.2, xs:double(1.2))))`, "1"},
	{"distinct-values collapses equal numerics across types",
		`count(distinct-values((1, 1.0, xs:double(1), xs:float(1))))`, "1"},
	{"distinct-values is defined on identity, so two NaNs collapse",
		`count(distinct-values((xs:double('NaN'), xs:double('NaN'))))`, "1"},
	{"the operands of \"to\" must be integers", `count(1 to 3)`, "3"},

	// --- Strings and URIs ---------------------------------------------------
	{"escape-html-uri escapes only outside #x20-#x7E",
		`escape-html-uri('http://x/a b')`, "http://x/a b"},
	{"escape-html-uri escapes non-ASCII",
		`escape-html-uri('http://x/aéb')`, "http://x/a%C3%A9b"},
	{"iri-to-uri does escape a space",
		`iri-to-uri('http://x/a b')`, "http://x/a%20b"},

	// --- Regular expressions ------------------------------------------------
	{"character-class subtraction removes the named characters",
		`matches('b', '^[a-z-[aeiou]]$')`, "true"},
	{"character-class subtraction removes the named characters",
		`matches('a', '^[a-z-[aeiou]]$')`, "false"},
	{"subtraction can split a class in two",
		`matches('4', '^[0-9-[3-5]]$')`, "false"},
	{"a hyphen between two classes is not subtraction",
		`matches('x-y', '[a-z]-[a-z]')`, "true"},
	{"in multi-line mode the position after a trailing newline is not a line",
		`matches(concat('abcd', codepoints-to-string(10), 'defg', codepoints-to-string(10)), '^$', 'm')`,
		"false"},
	{"a genuinely empty line does match",
		`matches(concat('abcd', codepoints-to-string(10), codepoints-to-string(10), 'defg'), '^$', 'm')`,
		"true"},
}

// TestSaxonAgreement runs every expression whose answer was confirmed against
// Saxon-HE 12.4.
func TestSaxonAgreement(t *testing.T) {
	for _, c := range saxonAgreed {
		got := evalStr(t, testDoc, c.expr)
		if got != c.want {
			t.Errorf("%s\n  expr: %s\n  got:  %q\n  want: %q (Saxon-HE 12.4)",
				c.why, c.expr, got, c.want)
		}
	}
}

// saxonRejected holds expressions Saxon refuses. The engine must refuse them
// too: each one previously returned a plausible wrong answer, and two of them
// crashed the process.
var saxonRejected = []struct {
	why, expr, code string
}{
	{"an empty position argument is a type error, not a nil dereference",
		`insert-before((1,2,3), (), (9))`, "XPTY0004"},
	{"an empty position argument is a type error, not a nil dereference",
		`remove((1,2,3), ())`, "XPTY0004"},
	{"the operands of \"to\" must be xs:integer, not truncated",
		`1.1 to 3`, "XPTY0004"},
	{"a codepoint XML cannot represent is an error, not U+FFFD",
		`codepoints-to-string(0)`, "FOCH0001"},
	{"a surrogate is not an XML character",
		`codepoints-to-string(55296)`, "FOCH0001"},
	{"a duration needs a digit on each side of the point",
		`'PT.5S' cast as xs:duration`, ""},
	{"a duration needs a digit on each side of the point",
		`'PT30.S' cast as xs:duration`, ""},
	{"fn:QName requires an NCName local part",
		`QName('http://x', '1person')`, "FOCA0002"},
	{"a base64 lexical form is not hex", `xs:hexBinary('D7c=')`, ""},
	{"a value out of range for the type is an error", `xs:byte(128)`, "FORG0001"},
	{"subtraction from a shorthand class cannot be expanded exactly",
		`matches('a', '[\d-[5]]')`, "FORX0002"},
}

func TestSaxonRejection(t *testing.T) {
	for _, c := range saxonRejected {
		err := evalErr(t, testDoc, c.expr)
		if err == nil {
			t.Errorf("%s\n  expr: %s\n  got no error; Saxon-HE 12.4 refuses it", c.why, c.expr)
			continue
		}
		if c.code != "" && !strings.Contains(err.Error(), c.code) {
			t.Errorf("%s\n  expr: %s\n  error: %v\n  want code %s",
				c.why, c.expr, err, c.code)
		}
	}
}

// Error codes are part of an expression's meaning: the spec defines a distinct
// code per condition, and a caller branching on the code — or a conformance
// suite comparing it — needs the right one. These were all found by tightening
// the QT3 harness to compare codes rather than accept any error.
func TestSpecErrorCodes(t *testing.T) {
	ns := testResolver{
		"xs":  xdm.NSXS,
		"err": "http://www.w3.org/2005/xqt-errors",
		"p":   "http://example.com/p",
	}
	cases := []struct{ why, expr, code string }{
		// A cast the table forbids is a type error, not a cast error:
		// FORG0001 means the value was wrong, XPTY0004 that the conversion is
		// not defined between those types at all.
		{"anyURI is reached only from a string",
			`xs:double("1e5") cast as xs:anyURI`, "XPTY0004"},
		{"a time cannot widen to a dateTime",
			`xs:time("13:20:00") cast as xs:dateTime`, "XPTY0004"},
		{"a partial date has nothing to widen into",
			`xs:gYearMonth("1999-05") cast as xs:date`, "XPTY0004"},
		{"nor into another partial date",
			`xs:gYearMonth("1999-05") cast as xs:gYear`, "XPTY0004"},

		// The parser has narrower codes than the generic XPST0003, and they
		// were being buried under it.
		{"an unbound prefix is its own static error", `$zz:x`, "XPST0081"},
		{"so is an unknown type", `1 instance of xs:nosuchtype`, "XPST0051"},
		{"an abstract type cannot be a cast target",
			`"x" castable as xs:NOTATION`, "XPST0080"},

		// fn:error raises the error its QName names.
		{"fn:error uses its QName argument",
			`error(QName('http://www.w3.org/2005/xqt-errors', 'err:FORG0009'))`,
			"FORG0009"},
		{"and defaults to FOER0000 without one", `error()`, "FOER0000"},
	}
	for _, c := range cases {
		var err error
		if compiled, cerr := Compile(c.expr, ns); cerr != nil {
			err = cerr
		} else {
			_, err = compiled.Eval(NewContext(nil, Builtins()))
		}
		if err == nil {
			t.Errorf("%s: %s produced no error, want %s", c.why, c.expr, c.code)
			continue
		}
		if got := xdm.ErrorCode(err); got != c.code {
			t.Errorf("%s: %s gave %s, want %s (%v)", c.why, c.expr, got, c.code, err)
		}
	}
}

// A parameter declared xs:string accepts only the string-like types and
// xs:untypedAtomic. Calling String() on whatever atomic arrived instead made
// "encode-for-uri(12)" quietly encode "12" where the spec requires XPTY0004,
// and the same for every function with a declared xs:string parameter.
func TestStringParametersAreTypeChecked(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS}
	refused := []string{
		`encode-for-uri(12)`,
		`iri-to-uri(12)`,
		`escape-html-uri(12)`,
		`compare(123, 456)`,
		`codepoint-equal("aa", xs:integer(1))`,
		`upper-case(1)`,
		`normalize-space(1)`,
	}
	for _, expr := range refused {
		var err error
		if c, cerr := Compile(expr, ns); cerr != nil {
			err = cerr
		} else {
			_, err = c.Eval(NewContext(nil, Builtins()))
		}
		if err == nil {
			t.Errorf("%s was accepted; an xs:string parameter does not take a number", expr)
			continue
		}
		if got := xdm.ErrorCode(err); got != "XPTY0004" {
			t.Errorf("%s gave %s, want XPTY0004", expr, got)
		}
	}

	// fn:concat is the documented exception: it takes xs:anyAtomicType, so
	// "concat('a', 1)" is legal and the corpora rely on it.
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want string }{
		{`concat('a', 1)`, "a1"},
		{`concat('PT', 5, 'H')`, "PT5H"},
		// A string-typed parameter still accepts anyURI and untypedAtomic.
		{`upper-case(xs:untypedAtomic('ab'))`, "AB"},
		{`string-length(xs:anyURI('http://x'))`, "8"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Lexical forms that look plausible but are not valid must be refused. All of
// these agree with Saxon-HE 12.4.
func TestStrictLexicalForms(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS}
	ctx := NewContext(nil, Builtins())
	cases := []struct{ why, expr, want string }{
		// A year may exceed four digits, but only when it needs to: extra
		// digits reached by zero-padding are not a lexical form.
		{"zero-padded year", `'02004' castable as xs:gYear`, "false"},
		{"zero-padded dateTime year",
			`'02004-08-01T12:44:05' castable as xs:dateTime`, "false"},
		{"a genuinely five-digit year is fine",
			`'12004' castable as xs:gYear`, "true"},
		{"and four digits is the ordinary case",
			`'2004' castable as xs:gYear`, "true"},

		// base64 padding: the bits a padded group cannot represent must be
		// zero, which Go's decoder does not check.
		{"one-pad with non-zero low bits", `'AP9=' castable as xs:base64Binary`, "false"},
		{"two-pad with non-zero low bits", `'Ay==' castable as xs:base64Binary`, "false"},
		{"a correctly padded value", `'D7c=' castable as xs:base64Binary`, "true"},
		{"and an unpadded one", `'AAAA' castable as xs:base64Binary`, "true"},
	}
	for _, c := range cases {
		compiled, err := Compile(c.expr, ns)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		seq, err := compiled.Eval(ctx)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got := seq[0].(interface{ String() string }).String(); got != c.want {
			t.Errorf("%s: %s = %q, want %q", c.why, c.expr, got, c.want)
		}
	}
}

// A path step's left operand must be nodes. This is XPTY0019 — "the step
// operand is not a node" — and is distinct from XPTY0020, which is about the
// context item.
func TestPathStepOperandMustBeNodes(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, expr := range []string{`(10)/child::*`, `(10)/self::*`, `(10)/parent::*`} {
		_, err := Eval(expr, ctx, nil)
		if err == nil {
			t.Errorf("%s was accepted", expr)
			continue
		}
		if got := xdm.ErrorCode(err); got != "XPTY0019" {
			t.Errorf("%s gave %s, want XPTY0019", expr, got)
		}
	}
}

// A *value* comparison casts untypedAtomic to xs:string and nothing else,
// which is what makes it exact. A *general* comparison casts to the other
// operand's type, which is what makes it forgiving about untyped input. The
// two were treated alike, so every "eq" against untyped data silently had the
// semantics of "=".
func TestValueComparisonUntypedIsString(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS}
	refused := []string{
		`xs:untypedAtomic("1") eq xs:integer(1)`,
		`xs:integer(1) eq xs:untypedAtomic("1")`,
		`xs:untypedAtomic("0") ne xs:double(1)`,
		`xs:untypedAtomic("0") lt xs:float(1)`,
	}
	for _, expr := range refused {
		c, err := Compile(expr, ns)
		if err == nil {
			_, err = c.Eval(NewContext(nil, Builtins()))
		}
		if err == nil {
			t.Errorf("%s was accepted; eq compares a string with a number", expr)
			continue
		}
		if got := xdm.ErrorCode(err); got != "XPTY0004" {
			t.Errorf("%s gave %s, want XPTY0004", expr, got)
		}
	}

	// The general comparison must keep working — real rule sets depend on
	// "@indicator = true()" over an unvalidated document.
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want string }{
		{`xs:untypedAtomic("1") = xs:integer(1)`, "true"},
		{`xs:untypedAtomic("true") = true()`, "true"},
		{`xs:untypedAtomic("1") eq "1"`, "true"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// fn:min and fn:max return the *promoted* type of the sequence, not the type
// of whichever item won, and a sequence mixing incomparable types is FORG0006
// even when an earlier item is NaN.
func TestMinMaxPromotionAndValidation(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS}
	ctx := NewContext(nil, Builtins())

	for _, c := range []struct{ expr, want string }{
		{`min((1, xs:float(2), xs:decimal(3))) instance of xs:float`, "true"},
		{`min((3, xs:float("NaN"))) instance of xs:float`, "true"},
		{`max((1, 2, 3))`, "3"},
		{`min((1, 2, 3))`, "1"},
		{`min(("a", "b"))`, "a"},
	} {
		compiled, err := Compile(c.expr, ns)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		seq, err := compiled.Eval(ctx)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got := seq[0].(interface{ String() string }).String(); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// Mixing families is FORG0006, and the check must happen even when a NaN
	// appears first — returning NaN early skipped validating the rest.
	for _, expr := range []string{
		`min((xs:float("NaN"), 1, "a string"))`,
		`min(("a string", 1))`,
		`max((1, "a string"))`,
	} {
		compiled, cerr := Compile(expr, ns)
		var err error = cerr
		if cerr == nil {
			_, err = compiled.Eval(ctx)
		}
		if err == nil {
			t.Errorf("%s was accepted; the sequence mixes incomparable types", expr)
			continue
		}
		if got := xdm.ErrorCode(err); got != "FORG0006" {
			t.Errorf("%s gave %s, want FORG0006", expr, got)
		}
	}
}

// A cast target is a SingleType: an atomic type with an optional "?". An
// occurrence indicator is a syntax error, and a node kind is not an atomic
// type.
func TestCastTargetMustBeSingleAtomicType(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS}
	cases := []struct{ expr, code string }{
		{`'string' castable as xs:string*`, "XPST0003"},
		{`'string' castable as xs:string+`, "XPST0003"},
		// A kind test in cast position is XPST0003, not XPST0080: the
		// grammar's cast production admits only an atomic type name, so this
		// does not parse at all. XPST0080 is for a name that is a type but is
		// not allowed as a target — the abstract types below.
		{`'string' castable as node()`, "XPST0003"},
		{`'string' castable as element()`, "XPST0003"},
		{`'string' castable as empty-sequence()`, "XPST0003"},
		{`'string' castable as xs:NOTATION`, "XPST0080"},
		{`'string' castable as xs:anyAtomicType`, "XPST0080"},
	}
	for _, c := range cases {
		_, err := Compile(c.expr, ns)
		if err == nil {
			t.Errorf("%s compiled; it is not a valid cast target", c.expr)
			continue
		}
		if got := xdm.ErrorCode(err); got != c.code {
			t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
		}
	}
	// The legal forms must still compile.
	for _, expr := range []string{`'1' castable as xs:integer`, `'1' castable as xs:integer?`} {
		if _, err := Compile(expr, ns); err != nil {
			t.Errorf("%s was refused: %v", expr, err)
		}
	}
}

// xs:integer is arbitrary-precision, so a range bound can exceed int64.
// Int64() wrapped it silently and produced a range of the wrong numbers.
//
// What bounds a range is its *length*, not the magnitude of its endpoints:
// "10^21 to 10^21+3" is an ordinary four-item sequence. Refusing it because
// the endpoints do not fit an int64 was the first fix and was wrong; QT3
// op-to/RangeExpr-409 asserts the four values explicitly.
func TestRangeBoundBeyondInt64(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want string }{
		{`string-join(for $i in 1000000000000000000000 to 1000000000000000000003 return string($i), ",")`,
			"1000000000000000000000,1000000000000000000001," +
				"1000000000000000000002,1000000000000000000003"},
		{`count(1000000000000000000000 to 1000000000000000000003)`, "4"},
		{`(1000000000000000000000 to 1000000000000000000003)[2]`, "1000000000000000000001"},
		{`count(18446744073709551616 to 18446744073709551620)`, "5"},
		// A range whose length is genuinely unbounded is still refused, and
		// for the length rather than for the size of the numbers.
		{`count(-1000000000000000000003 to -1000000000000000000000)`, "4"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	// A range too long to hold is refused, however small its endpoints.
	if _, err := Eval(`1 to 100000000000`, ctx, nil); err == nil {
		t.Error("1 to 100000000000 was accepted; it exceeds the item limit")
	}
	if got := evalOne(t, ctx, `count(1 to 10)`); got != "10" {
		t.Errorf("count(1 to 10) = %q, want 10", got)
	}
}

// Two collations are implemented: codepoint and the ASCII case-insensitive one
// the spec defines, which needs no locale data. The functions that take a
// collation argument must *use* it rather than only validate it. Relative URIs
// are accepted because the suite and some stylesheets write them. All
// expectations match Saxon-HE 12.4.
func TestCollationsAreApplied(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS}
	ctx := NewContext(nil, Builtins())
	const ci = `'http://www.w3.org/2005/xpath-functions/collation/html-ascii-case-insensitive'`

	cases := []struct{ expr, want string }{
		{`compare('ABC', 'abc', ` + ci + `)`, "0"},
		{`compare('ABC', 'abc')`, "-1"},
		{`contains('Hello', 'ELL', ` + ci + `)`, "true"},
		{`starts-with('Hello', 'HE', ` + ci + `)`, "true"},
		{`ends-with('Hello', 'LO', ` + ci + `)`, "true"},
		{`substring-after('aXbXc', 'xb', ` + ci + `)`, "Xc"},
		{`substring-before('aXbXc', 'xb', ` + ci + `)`, "a"},
		// The relative form of the codepoint URI resolves rather than failing.
		{`compare('a', 'b', 'collation/codepoint')`, "-1"},
	}
	for _, c := range cases {
		compiled, err := Compile(c.expr, ns)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		seq, err := compiled.Eval(ctx)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got := seq[0].(interface{ String() string }).String(); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// An unsupported collation is still refused loudly.
	if _, err := Eval(`compare('a', 'b', 'http://example.com/swedish')`, ctx, nil); err == nil {
		t.Error("an unknown collation was accepted")
	}
}

// XML Schema regular expressions have Unicode *block* escapes — \p{IsGreek} —
// which RE2 does not know; it has categories such as \p{L}, which it does.
// Blocks are translated into their codepoint range, categories pass through.
// Verified against Saxon-HE 12.4.
func TestUnicodeBlockEscapes(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	cases := []struct{ expr, want string }{
		{`matches('abc', '^\p{IsBasicLatin}+$')`, "true"},
		{`matches('日本', '^\p{IsBasicLatin}+$')`, "false"},
		{`matches('abc', '^\P{IsBasicLatin}+$')`, "false"},
		{`matches('日本', '^\P{IsBasicLatin}+$')`, "true"},
		// A category is RE2's own and must still work.
		{`matches('abc', '^\p{L}+$')`, "true"},
		{`matches('123', '^\p{Nd}+$')`, "true"},
	}
	for _, c := range cases {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	// An unknown block is refused rather than compiled to an empty class,
	// because a pattern that silently matches nothing is worse than one that
	// will not compile.
	if _, err := Eval(`matches('a', '\p{IsNotARealBlock}')`, ctx, nil); err == nil {
		t.Error("an unknown Unicode block was accepted")
	}
}

// Three expressions that panicked rather than evaluating. A panic is reachable
// from any stylesheet or query, so for an embedding server each of these was a
// denial of service, not just a wrong answer.
//
// The causes were unrelated: a parser that skipped a fixed token count past
// the end of the input, a NaN produced by adding opposite infinities that
// survived every clamp because comparisons against NaN are false, and an
// infinite divisor that looked like zero because an infinity has no rational
// form.
func TestNoPanicOnPathologicalInput(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// A missing ")" is a syntax error, not a panic and not an accepted
		// expression.
		{`3 treat as item(`, "", "XPST0003"},
		{`3 treat as item()`, "3", ""},
		// substring with infinities: start + length is NaN, and the result is
		// the empty string.
		{`count(substring("12345", -1 div 0E0, 1 div 0E0))`, "1", ""},
		{`substring("12345", -1 div 0E0, 1 div 0E0) eq ""`, "true", ""},
		{`substring("12345", 1 div 0E0, 1 div 0E0) eq ""`, "true", ""},
		// A finite value divided by an infinity truncates to zero. Only an
		// infinite dividend is an error.
		{`xs:float("3") idiv xs:float("INF") eq xs:float(0)`, "true", ""},
		{`xs:double("3") idiv xs:double("INF") eq xs:double(0)`, "true", ""},
		{`xs:double("3") idiv xs:double("0")`, "", "FOAR0001"},
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked: %v", c.expr, r)
				}
			}()
			v, err := Eval(c.expr, ctx, nil)
			if c.code != "" {
				if err == nil {
					t.Errorf("%s was accepted, want %s", c.expr, c.code)
					return
				}
				if got := xdm.ErrorCode(err); got != c.code {
					t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
				}
				return
			}
			if err != nil {
				t.Errorf("%s: %v", c.expr, err)
				return
			}
			got := ""
			if len(v) == 1 {
				got = v[0].(interface{ String() string }).String()
			}
			if got != c.want {
				t.Errorf("%s = %q, want %q", c.expr, got, c.want)
			}
		}()
	}
}

// Duration arithmetic, where four separate rules were wrong.
//
// Three of them come from the same place: an infinity has no rational form,
// so converting one gives zero, and a duration scaled by INF was scaled by
// zero instead. The fourth is the rounding rule for the month count.
func TestDurationArithmetic(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// Dividing by an infinity shrinks a duration to nothing; multiplying
		// by one overflows it.
		{`xs:yearMonthDuration("P3Y36M") div xs:double("INF") eq xs:yearMonthDuration("P0M")`, "true", ""},
		{`xs:yearMonthDuration("P3Y36M") div xs:double("-INF") eq xs:yearMonthDuration("P0M")`, "true", ""},
		{`xs:yearMonthDuration("P3Y3M") * xs:double("INF")`, "", "FODT0002"},
		{`xs:dayTimeDuration("P3DT4H3M3.100S") * xs:double("-INF")`, "", "FODT0002"},

		// Only the two subtypes scale. xs:duration holds both months and
		// seconds, which have no common unit.
		{`xs:duration("P1Y3M") * 3`, "", "XPTY0004"},
		{`xs:duration("P1Y3M") div 3`, "", "XPTY0004"},

		// duration div duration yields a number, so a zero divisor is
		// ordinary numeric division by zero rather than a duration error.
		{`xs:yearMonthDuration("P1Y") div xs:yearMonthDuration("P0M")`, "", "FOAR0001"},

		// The month count rounds half up, toward positive infinity. The rule
		// is asymmetric and only visible on exact halves.
		{`xs:yearMonthDuration("P10Y01M") div -2.0`, "-P5Y", ""},
		{`xs:yearMonthDuration("P5M") div 2`, "P3M", ""},
		{`xs:yearMonthDuration("P5M") div -2`, "-P2M", ""},
		{`xs:yearMonthDuration("P5M") div 10`, "P1M", ""},
		{`xs:yearMonthDuration("P5M") div -10`, "P0M", ""},
		{`xs:yearMonthDuration("P2Y11M") * 2.3`, "P6Y9M", ""},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Values that overflowed their internal representation and were accepted
// anyway. Each produced a wrong value rather than an error, which is the worse
// failure: "P768614336404564651Y" parsed as a *negative* duration of a similar
// magnitude, because the year count multiplied by 12 wrapped an int64.
func TestLexicalOverflowIsRejected(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct {
		expr string
		want string
	}{
		// The month count is accumulated in big.Int and range-checked once.
		{`"P768614336404564651Y" castable as xs:duration`, "false"},
		{`"-P768614336404564651Y" castable as xs:duration`, "false"},
		{`"P768614336404564651Y" castable as xs:yearMonthDuration`, "false"},
		{`"P100Y" castable as xs:duration`, "true"},

		// A year is bounded by the range daysFromCivil can convert without
		// its own int64 arithmetic wrapping. The two directions differ by
		// one, because the era division rounds toward negative infinity.
		{`"25252734927766555-07-29" castable as xs:date`, "false"},
		{`"-25252734927766554-06-06" castable as xs:date`, "false"},
		{`"25252734927766555-07-29T00:00:00Z" castable as xs:dateTime`, "false"},
		{`"25252734927766554-07-29" castable as xs:date`, "true"},
		{`"2024-07-29" castable as xs:date`, "true"},
		{`"12004-07-29" castable as xs:date`, "true"},

		// xs:anyURI is deliberately permissive — "true" is a valid relative
		// reference — so only a malformed percent-escape is rejected.
		{`"%" castable as xs:anyURI`, "false"},
		{`"%zz" castable as xs:anyURI`, "false"},
		{`"true" castable as xs:anyURI`, "true"},
		{`"http://www.example.com/~b%C3%A9b%C3%A9" castable as xs:anyURI`, "true"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Regular expression rules where XML Schema's dialect differs from RE2's, and
// where passing the pattern through unchanged gave the wrong answer rather
// than an error.
func TestRegexDialectRules(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// XML Schema's escape set is closed. RE2 reads "\0" as a NUL byte and
		// "\ " as a space, so both compiled and matched instead of failing.
		{`matches("abcd", "(asd)[asd\0]")`, "", "FORX0002"},
		{`matches("hello world", "hello\ sworld")`, "", "FORX0002"},
		// Under the x flag, though, whitespace is removed *before* escapes
		// are interpreted, so the backslash does not protect the space:
		// "hello\ sworld" becomes "hello\sworld" and matches.
		{`matches("hello world", "hello\ sworld", "x")`, "true", ""},
		{`matches("helloworld", "hello world", "x")`, "true", ""},
		// The s flag makes "." match the newlines it otherwise excludes.
		{`matches(concat("Mary", codepoints-to-string(10), "Jones"), "Mary.Jones", "s")`, "true", ""},
		{`matches("abc", "a\nb")`, "false", ""},
		{`matches("a.c", "a\.c")`, "true", ""},

		// A backreference is valid XML Schema but RE2 does not implement one,
		// so it is refused rather than mis-compiled.
		{`matches("abab", "(a)\1")`, "", "FORX0002"},

		// "." excludes both newline characters. RE2's excludes only \n, so a
		// carriage return matched where it should not have.
		{`matches(concat("Mary", codepoints-to-string(13), "Jones"), "Mary.Jones")`, "false", ""},
		{`matches(concat("Mary", codepoints-to-string(10), "Jones"), "Mary.Jones")`, "false", ""},
		{`matches("MaryXJones", "Mary.Jones")`, "true", ""},

		// The i flag must not reach inside a category: asking for an
		// uppercase letter and getting a lowercase one is not case folding,
		// it is the wrong answer.
		{`matches("m", "\p{Lu}", "i")`, "false", ""},
		{`matches("m", "\P{Lu}", "i")`, "true", ""},
		{`matches("M", "\p{Lu}")`, "true", ""},
		{`matches("abc", "\p{L}+")`, "true", ""},
		// Block escapes still translate, and still fold nothing.
		{`matches("abc", "^\p{IsBasicLatin}+$")`, "true", ""},

		// The flags and replacement parameters are declared xs:string, not
		// xs:string?, so an explicit empty sequence is a type error. Omitting
		// the argument is still fine.
		{`matches("input", "pattern", ())`, "", "XPTY0004"},
		{`replace("input", "pattern", ())`, "", "XPTY0004"},
		{`matches("input", "in")`, "true", ""},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Static errors in type and kind-test position, where the code we reported
// said the wrong thing about what was actually wrong.
func TestStaticErrorCodes(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS}
	for _, c := range []struct{ expr, code string }{
		// A reserved name introduces a kind test or a type, so it cannot name
		// a function however the expression continues. That makes it a syntax
		// error rather than a call to a function nobody defined.
		{`item()`, "XPST0003"},
		{`empty-sequence()`, "XPST0003"},
		// namespace-node() takes no argument, so with one it is not a kind
		// test at all but a call to a function nobody defined: XPST0017.
		{`namespace-node(1)`, "XPST0017"},

		// item() is a sequence type, not a node test — it matches atomic
		// values, which no axis yields — and the schema- forms need the name
		// they declare. Both parsed as path steps and failed at evaluation
		// with "no context item", which describes the wrong problem.
		{`item()`, "XPST0003"},
		{`schema-attribute()`, "XPST0003"},
		{`schema-element()`, "XPST0003"},
		// A wildcard names no declaration either.
		{`schema-element(*)`, "XPST0003"},
		{`schema-attribute(*)`, "XPST0003"},
		// With a name, the declaration has to exist. This engine imports no
		// schemas, so none does: XPST0008 says the name is not in scope.
		{`schema-element(e)`, "XPST0008"},
		{`schema-attribute(a)`, "XPST0008"},

		// An unbound prefix and an unknown local name are different errors.
		// The prefix cannot be resolved at all, which comes first.
		{`3 treat as prefixDoesNotExist:integer`, "XPST0081"},
		{`document-node(element(notBound:ncname))`, "XPST0081"},
		{`document-node(schema-element(notBound:ncname))`, "XPST0081"},
		{`3 treat as xs:noSuchType`, "XPST0051"},
	} {
		_, err := Compile(c.expr, ns)
		if err == nil {
			t.Errorf("%s compiled, want %s", c.expr, c.code)
			continue
		}
		if got := xdm.ErrorCode(err); got != c.code {
			t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
		}
	}
	// document-node() may constrain its root element. The inner test was not
	// parsed at all, so the whole form was a syntax error.
	for _, expr := range []string{
		`document-node(element(e))`,
		`document-node()`,
		`. instance of document-node(element(invoice))`,
		// item() is still a type, where it always was one.
		`3 instance of item()`,
		`3 treat as item()`,
		`3 instance of item()*`,
	} {
		if _, err := Compile(expr, ns); err != nil {
			t.Errorf("%s was refused: %v", expr, err)
		}
	}
}

// fn:resolve-uri, where net/url is both too permissive about what it accepts
// as a base and too eager to normalise what it returns.
func TestResolveURIStrictness(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// url.Parse reads "b.html" as a URI with an empty scheme and
		// "http:%%" as one with an opaque body. Neither can serve as a base.
		{`fn:string(fn:resolve-uri("a.html", "b.html"))`, "", "FORG0002"},
		{`fn:resolve-uri("examples", "http:%%")`, "", "FORG0002"},
		// A fragment identifies a place within a resource, so it cannot be
		// resolved against.
		{`resolve-uri("b.html", "http://www.example.com/a.html#fragment")`, "", "FORG0002"},

		// An already-absolute reference is returned as it stands and the base
		// is never consulted, so an unusable base is not an error there.
		{`string(resolve-uri("http://www.example.com/a.html", "b.html"))`,
			"http://www.example.com/a.html", ""},

		// The result is the reference resolved, not normalised: net/url
		// percent-escapes a space and lower-cases the scheme, and neither is
		// wanted.
		{`string(resolve-uri("this doc.html", "http://www.example.com/that doc.html"))`,
			"http://www.example.com/this doc.html", ""},
		{`string(resolve-uri(upper-case("examples"), upper-case("http://www.examples.com/")))`,
			"HTTP://WWW.EXAMPLES.COM/EXAMPLES", ""},

		// The ordinary case still works.
		{`string(resolve-uri("b.html", "http://www.example.com/a.html"))`,
			"http://www.example.com/b.html", ""},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Lexical forms that a Go standard-library parser accepted and XML Schema does
// not. Each produced a plausible value from a malformed string, which is worse
// than an error because nothing downstream can tell.
func TestSchemaLexicalForms(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// strconv.Atoi accepts a leading sign, so every fixed-width date and
		// time field did too: "11:+1:11" parsed as 11:01:11.
		{`xs:time("11:+1:11")`, "", "FORG0001"},
		{`xs:time("+1:11:11")`, "", "FORG0001"},
		{`xs:time("11:11:11++1:11")`, "", "FORG0001"},
		{`xs:time("11:11:11+11:-1")`, "", "FORG0001"},
		{`xs:date("1111-+1-11")`, "", "FORG0001"},
		{`xs:date("1111-11-+1")`, "", "FORG0001"},
		{`string(xs:time("11:11:11"))`, "11:11:11", ""},
		{`string(xs:date("1111-11-11"))`, "1111-11-11", ""},

		// big.Rat.SetString reads Go's numeric syntax: "0x0" is a hex literal
		// to it and "1/2" a fraction.
		{`xs:unsignedLong("0x0")`, "", "FORG0001"},
		{`xs:positiveInteger("0x1")`, "", "FORG0001"},
		{`xs:integer("1/2")`, "", "FORG0001"},
		{`xs:integer("42")`, "42", ""},
		{`string(xs:decimal("1.5"))`, "1.5", ""},

		// "+INF" is XSD 1.1, which the suite tests against. The other
		// spellings Go accepts — "Inf", "Infinity" — remain invalid.
		{`string(xs:double("+INF"))`, "INF", ""},
		{`string(xs:float("+INF"))`, "INF", ""},
		{`xs:double("Infinity")`, "", "FORG0001"},

		// Year zero is legal in XSD 1.1, where 1.0 excluded it.
		{`string(xs:gYear("0000"))`, "0000", ""},
		{`xs:gYear("00")`, "", "FORG0001"},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// A batch of unrelated rules, each of which returned a plausible answer where
// the spec requires an error or a different one.
func TestAssortedOperatorRules(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// Some types have no ordering, so there is no largest one even in a
		// sequence of one item. A QName is a pair of names; xs:duration mixes
		// months with seconds, which have no common unit.
		{`max(QName("example.com/", "ncname"))`, "", "FORG0006"},
		{`max(xs:duration("P1Y1M1D"))`, "", "FORG0006"},
		{`min(xs:duration("P1Y1M1D"))`, "", "FORG0006"},
		{`max(xs:dayTimeDuration("P1D"))`, "P1D", ""},

		// xs:anyURI promotes to xs:string to be compared with one, so a mixed
		// sequence yields a string whichever item wins.
		{`max((xs:anyURI("http://c.com"), "http://b.com")) instance of xs:string`, "true", ""},
		{`min((xs:anyURI("http://a.com"), "http://b.com")) instance of xs:string`, "true", ""},

		// A zero divisor is the more specific complaint than an infinite
		// dividend.
		{`xs:float("INF") idiv xs:float("0")`, "", "FOAR0001"},
		{`xs:double("INF") idiv xs:double("0")`, "", "FOAR0001"},
		{`xs:float("INF") idiv xs:float("2")`, "", "FOAR0002"},

		// A time is a point within a day, so arithmetic wraps rather than
		// carrying into a date.
		{`xs:time("08:12:32") - xs:dayTimeDuration("P23DT09H32M59S") eq xs:time("22:39:33")`, "true", ""},
		// And a month has nothing to apply to in a time-of-day.
		{`xs:yearMonthDuration("P1Y") + xs:time("08:01:23")`, "", "XPTY0004"},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	// A numeric literal must be separated from a following name: "10div 3" is
	// not "10 div 3".
	ns := testResolver{"xs": xdm.NSXS}
	for _, expr := range []string{`10div 3`, `10idiv 3`, `1eq 2`} {
		if _, err := Compile(expr, ns); xdm.ErrorCode(err) != "XPST0003" {
			t.Errorf("%s gave %v, want XPST0003", expr, err)
		}
	}
}

// Four more rules, unrelated to each other.
func TestAssortedStaticAndStringRules(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS}
	ctx := NewContext(nil, Builtins())

	// "/" is defined on node sequences whatever the step does with them. A
	// step that never dereferences the operand was exempted from the check,
	// which only hid the error: "1/3" is XPTY0019, not 3.
	// A static error comes first, though: an unknown function is XPST0017
	// whatever the operand is, so the placeholder here has to be a function
	// that exists.
	for _, expr := range []string{`1/3`, `(10)/true()`, `'a'/1`} {
		if _, err := Eval(expr, ctx, nil); xdm.ErrorCode(err) != "XPTY0019" {
			t.Errorf("%s gave %v, want XPTY0019", expr, err)
		}
	}
	// An unknown function is XPST0017 whatever the operand is, because a
	// static error is reported before a dynamic one. This engine resolves
	// functions at evaluation, so the check is made explicitly.
	for _, expr := range []string{`(1 to 10)/count()`, `(10)/f()`, `count()`} {
		if _, err := Eval(expr, ctx, nil); xdm.ErrorCode(err) != "XPST0017" {
			t.Errorf("%s gave %v, want XPST0017", expr, err)
		}
	}

	// An unterminated comment silently discarded the rest of the expression,
	// so "1(: unterminated" evaluated to 1.
	for _, expr := range []string{`1(: this comment does not end`, `(: nor this`} {
		if _, err := Compile(expr, ns); xdm.ErrorCode(err) != "XPST0003" {
			t.Errorf("%s gave %v, want XPST0003", expr, err)
		}
	}
	// A closed comment is still skipped, and they nest.
	for _, expr := range []string{`(:***:)1`, `1 (: a (: nested :) comment :)`} {
		if _, err := Compile(expr, ns); err != nil {
			t.Errorf("%s was refused: %v", expr, err)
		}
	}

	// fn:upper-case uses Unicode's full case mapping, which can change the
	// string's length. strings.ToUpper applies the simple mapping and left
	// both of these unchanged.
	for _, c := range []struct{ expr, want string }{
		{`upper-case("ß")`, "SS"},
		{`upper-case("straße")`, "STRASSE"},
		{`upper-case("abc")`, "ABC"},
		{`lower-case("ABC")`, "abc"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Argument types and arities that were accepted too loosely.
func TestFunctionArgumentTypes(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// XPTY0020 is the code for an axis step applied to a non-node. A
		// function whose argument defaults to the context item is not a step,
		// so a non-node focus there is XPTY0004.
		{`(1 to 100)[fn:local-name()]`, "", "XPTY0004"},
		{`1[fn:lang("en-us")]`, "", "XPTY0004"},
		{`(1 to 5)[fn:id("argument1")]`, "", "XPTY0004"},

		// fn:remove's position is declared xs:integer, so a decimal is a type
		// error rather than a value to truncate.
		{`remove(1 to 10, 1.0)`, "", "XPTY0004"},
		{`count(remove(1 to 10, 1))`, "9", ""},

		// The separator is declared xs:string, not xs:string?.
		{`string-join("a string", ())`, "", "XPTY0004"},
		{`string-join(("a", "b"), "-")`, "a-b", ""},

		// fn:round is single-argument in XPath 2.0; the precision parameter
		// is a 3.0 addition. fn:round-half-to-even does take one.
		{`round(1, 2)`, "", "XPST0017"},
		{`round(2.5)`, "3", ""},
		{`round-half-to-even(1.2345, 2)`, "1.23", ""},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Four functions whose parameter declarations were read too loosely, and one
// precision bug.
func TestOptionalArgumentsAndPrecision(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// fn:doc's argument is xs:string?, so an empty sequence is not an
		// error — it is an empty result.
		{`count(fn:doc(()))`, "0", ""},
		{`fn:doc-available(())`, "false", ""},

		// A collation parameter is xs:string, not xs:string?, so an explicit
		// empty sequence there is a type error. Omitting it is fine.
		{`index-of((1, 2, 3), 1, ())`, "", "XPTY0004"},
		{`index-of((1, 2, 3), 1)`, "1", ""},

		// fn:iri-to-uri escapes RFC 3986's excluded delimiters as well as
		// everything outside ASCII. Passing all printable ASCII through left
		// them in place, so the result was not a URI.
		{`iri-to-uri("<>")`, "%3C%3E", ""},
		{`iri-to-uri("a b")`, "a%20b", ""},
		{`iri-to-uri("http://example.com/a")`, "http://example.com/a", ""},
		// fn:escape-html-uri deliberately does not: it reproduces what a
		// browser does with an href, not a valid URI.
		{`escape-html-uri("<>")`, "<>", ""},

		// Equality between numerics is decided in the promoted type, so with
		// an xs:float present a decimal is compared as a float.
		{`count(distinct-values((xs:decimal("1.2"), xs:float("1.2"))))`, "1", ""},
		{`string-join(for $v in distinct-values((xs:decimal("1.2"), xs:float("1.2"))) return string($v), " ")`, "1.2", ""},
		{`count(distinct-values((xs:double(0), xs:float(0), 0)))`, "1", ""},
		// Without a float, a decimal keeps its own precision.
		{`count(distinct-values((xs:decimal("1.2"), xs:double("1.2"))))`, "1", ""},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Two type rules that answered the wrong way round.
func TestAbstractTypeAndXMLWhitespace(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want string }{
		// xs:NOTATION is abstract: no value has it as its type, so nothing is
		// an instance of one. Asking is legal — only casting is forbidden —
		// and the answer is always false. Falling through to the primitive
		// resolved it to xs:string and made every string match.
		{`"a string" instance of xs:NOTATION`, "false"},
		{`not("a string" instance of xs:NOTATION)`, "true"},
		{`1 instance of xs:NOTATION`, "false"},
		// The other abstract types still match everything atomic, which is
		// what being the root of the hierarchy means.
		{`"a string" instance of xs:anyAtomicType`, "true"},

		// whiteSpace="collapse" collapses runs of *XML* whitespace — space,
		// tab, carriage return, newline. strings.Fields splits on every
		// Unicode space, which swallowed U+00A0; a non-breaking space is
		// ordinary text to XML Schema and has to survive.
		{`string-to-codepoints(xs:token(codepoints-to-string((32, 9, 48, 13, 10, 48, 160, 32, 9))))`,
			"48 32 48 160"},
		{`string-to-codepoints(xs:token("  a	b  "))`, "97 32 98"},
		// normalizedString replaces rather than collapses, so runs survive.
		{`string-length(xs:normalizedString("a  b"))`, "4"},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		parts := make([]string, 0, len(v))
		for _, it := range v {
			parts = append(parts, it.(interface{ String() string }).String())
		}
		if got := strings.Join(parts, " "); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// A date whose year leaves the representable range is FODT0001 — overflow —
// not FORG0001, which says the lexical form was wrong. The form is fine; the
// value is too large.
func TestDateOverflowCode(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, expr := range []string{
		`xs:date("-25252734927766554-12-31") - xs:date("25252734927766554-12-31")`,
		`xs:date("-25252734927766555-06-07+02:00") = xs:date("25252734927766555-07-28")`,
		`xs:date("25252734927766555-07-28") > xs:date("-25252734927766555-06-07+02:00")`,
	} {
		_, err := Eval(expr, ctx, nil)
		if err == nil {
			t.Errorf("%s was accepted; the year overflows", expr)
			continue
		}
		if got := xdm.ErrorCode(err); got != "FODT0001" {
			t.Errorf("%s gave %s, want FODT0001", expr, got)
		}
	}
	// An ordinary date is unaffected, and so is one merely very large.
	for _, c := range []struct{ expr, want string }{
		{`string(xs:date("2024-07-29"))`, "2024-07-29"},
		{`string(xs:date("25252734927766554-07-29"))`, "25252734927766554-07-29"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Comparison at the promoted type, and three aggregates over types that do
// not support them.
func TestFloatPrecisionAndUnsummableTypes(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// When the promoted type is xs:float, both operands are compared as
		// floats. Comparing at double precision kept a difference that only
		// exists below float precision.
		{`xs:decimal(1.01) eq xs:float(1.01)`, "true", ""},
		{`xs:float(1.01) eq xs:decimal(1.01)`, "true", ""},
		{`xs:float(3.1) eq 3.1`, "true", ""},
		{`deep-equal(xs:decimal(1.01), xs:float(1.01))`, "true", ""},
		// A double keeps its own precision, and unequal values stay unequal.
		{`xs:decimal(1.01) eq xs:double(1.01)`, "true", ""},
		{`xs:float(1.01) eq xs:float(1.02)`, "false", ""},
		{`1 eq 2`, "false", ""},

		// A plain xs:duration carries both months and seconds, which have no
		// common unit, so it is not summable any more than it is orderable.
		// With one item the accumulator loop never ran, so the value was
		// returned as its own sum.
		{`sum(xs:duration("P1Y1M1D"))`, "", "FORG0006"},
		{`avg(xs:duration("P1Y1M1D"))`, "", "FORG0006"},
		{`string(sum(xs:dayTimeDuration("P1D")))`, "P1D", ""},

		// Only fn:translate's first argument is xs:string?; the two mapping
		// arguments are xs:string.
		{`fn:translate("arg", (), "transString")`, "", "XPTY0004"},
		{`fn:translate("arg", "a", ())`, "", "XPTY0004"},
		{`fn:translate("bar", "ab", "AB")`, "BAr", ""},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// A host application can add a collation. The spec requires only codepoint and
// the HTML ASCII case-insensitive one and leaves the rest
// implementation-defined, so without a registration hook an embedder had no
// way to supply a locale-aware or case-blind comparison. The QT3 harness uses
// it for the collation the test catalog defines.
func TestRegisterCollation(t *testing.T) {
	const uri = "http://example.com/collation/test-reverse"
	if _, err := ResolveCollation(uri); err == nil {
		t.Fatal("the test collation was already registered")
	}
	RegisterCollation(uri, codepointCollation{})
	c, err := ResolveCollation(uri)
	if err != nil {
		t.Fatalf("after registering: %v", err)
	}
	if c.Compare("a", "b") >= 0 {
		t.Error("the registered collation was not the one returned")
	}
	// The two required collations are unaffected.
	if _, err := ResolveCollation(CodepointCollation); err != nil {
		t.Errorf("codepoint collation: %v", err)
	}
}

// Three arguments that were accepted where the declaration forbids them, and
// one overflow in the float rounding path.
func TestArgumentValidationAndRoundingOverflow(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// fn:error's first parameter is xs:QName?. Ignoring a non-QName
		// silently turned a string passed where a code belongs into the
		// generic FOER0000.
		{`error('Wrong Argument Type')`, "", "XPTY0004"},
		{`error(QName("http://x", "MYERR"))`, "", "MYERR"},
		{`error()`, "", "FOER0000"},

		// A colon with nothing before it splits to an empty prefix, so the
		// prefix check was skipped and the colon vanished into the name.
		{`QName("http://www.example.com/example", ":person")`, "", "FOCA0002"},
		{`QName("http://www.example.com/example", "person:")`, "", "FOCA0002"},
		{`string(QName("http://www.example.com/example", "p:person"))`, "p:person", ""},

		// math.Pow(10, places) is finite well past the point where f*shift is
		// not, and Inf/Inf is NaN. A precision beyond a double's ~17
		// significant digits is the identity.
		{`string(round-half-to-even(3.567812E+3, 4294967296))`, "3567.812", ""},
		{`string(round-half-to-even(3.567812E+3, 2))`, "3567.81", ""},
		{`string(round-half-to-even(3.567812E+3, -4294967296))`, "0", ""},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// "*" and "?" are ambiguous between a sequence-type occurrence indicator and
// an operator, and the lexer resolves them by whether an operand precedes.
// A type name is not an operand, so both spellings have to be handled.
func TestOccurrenceIndicatorAmbiguity(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS}
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want string }{
		// An occurrence indicator followed by multiplication. Both of these
		// failed to parse: the indicator left the lexer expecting an operand,
		// so the second "*" came back as a wildcard.
		{`(3 treat as xs:integer ? * 3) eq 9`, "true"},
		{`(3 treat as xs:integer * * 3) eq 9`, "true"},
		{`(3 treat as xs:integer + * 3) eq 9`, "true"},
		// The indicators themselves still work.
		{`3 instance of xs:integer?`, "true"},
		{`3 instance of xs:integer*`, "true"},
		{`3 instance of xs:integer+`, "true"},
		{`() instance of xs:integer?`, "true"},
		{`() instance of xs:integer+`, "false"},
		// And "*" is still a wildcard where a wildcard belongs, still
		// multiplication where that belongs.
		{`count(/*)`, "1"},
		{`3 * 3`, "9"},
		{`3 * 3 * 2`, "18"},
		// A binary "+" now leaves the operand flag set, so the "*" after one
		// must still be multiplication rather than a wildcard.
		{`1 + 2 * 3`, "7"},
		{`(1 + 2) * 3`, "9"},
		{`1 + 2 + 3`, "6"},
		{`-1 + 2`, "1"},
		{`count(/* | /*)`, "1"},
	} {
		root := mustParse(t, `<r/>`)
		compiled, err := Compile(c.expr, ns)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		v, err := compiled.Eval(NewContext(root, Builtins()))
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	_ = ctx
}

// Five more parameter declarations that were read as "anything stringifiable"
// rather than as the types they name.
func TestParameterTypesAreEnforced(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS}
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// fn:doc and fn:doc-available take xs:string?. A type error in the
		// argument is still an error — the function answers whether the
		// document is available, not whether the call was well formed.
		{`doc-available(xs:integer(2))`, "", "XPTY0004"},
		{`doc-available(())`, "false", ""},

		// fn:codepoints-to-string takes xs:integer*. Casting first reported
		// FORG0001, which says the value was wrong for a conversion that
		// should never have been attempted.
		{`codepoints-to-string('hello')`, "", "XPTY0004"},
		{`codepoints-to-string((72, 105))`, "Hi", ""},

		// fn:normalize-unicode's form is xs:string, not xs:string?. An empty
		// *string* still means no normalisation.
		{`normalize-unicode('', ())`, "", "XPTY0004"},
		{`normalize-unicode('abc', '')`, "abc", ""},

		// fn:string-join takes xs:string*. Calling String() on each item
		// accepted anything: string-join(1 to 5, "") gave "12345".
		{`string-join(1 to 5, "")`, "", "XPTY0004"},
		{`string-join(("a", "b"), "-")`, "a-b", ""},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// A name followed by "(" that is not a kind test is a grammar error, not
	// an unknown type: "document()" is a misspelling of "document-node()",
	// and the name is not in type position at all.
	for _, c := range []struct{ expr, code string }{
		{`1 instance of document()`, "XPST0003"},
		{`1 instance of document(*)`, "XPST0003"},
		{`1 instance of xs:noSuchType`, "XPST0051"},
		{`1 instance of document-node()`, ""},
	} {
		_, err := Compile(c.expr, ns)
		if c.code == "" {
			if err != nil {
				t.Errorf("%s was refused: %v", c.expr, err)
			}
			continue
		}
		if got := xdm.ErrorCode(err); got != c.code {
			t.Errorf("%s gave %v, want %s", c.expr, err, c.code)
		}
	}
}

// fn:concat is variadic and fn:deep-equal takes a collation. Neither was true
// of the implementation.
func TestConcatArityAndDeepEqualCollation(t *testing.T) {
	ctx := NewContext(nil, Builtins())

	// The spec gives fn:concat a two-or-more signature. Lookup is keyed by
	// arity, so it was registered for 2..10 and a thirteen-argument call came
	// back as an unknown function.
	if got := evalOne(t, ctx,
		`concat('a','b','c',(),'d','e','f','g','h',' ','i','j','k l')`); got != "abcdefgh ijk l" {
		t.Errorf("13-argument concat = %q", got)
	}
	if got := evalOne(t, ctx,
		`concat('a','b','c','d','e','f','g','h','i','j','k','l','m','n','o','p','q','r','s','t')`); got != "abcdefghijklmnopqrst" {
		t.Errorf("20-argument concat = %q", got)
	}
	if got := evalOne(t, ctx, `concat('a','b')`); got != "ab" {
		t.Errorf("2-argument concat = %q", got)
	}
	// One argument is still an error: the minimum is two.
	if _, err := Eval(`concat('a')`, ctx, nil); xdm.ErrorCode(err) != "XPST0017" {
		t.Errorf("concat('a') gave %v, want XPST0017", err)
	}

	// The collation applies to every string comparison the traversal makes.
	// It was validated nowhere and applied nowhere.
	const ci = HTMLASCIICaseInsensitive
	for _, c := range []struct {
		expr string
		want string
	}{
		{`deep-equal(("a","A"), ("A","a"), "` + ci + `")`, "true"},
		{`deep-equal(("a","A"), ("A","a"))`, "false"},
		{`deep-equal(("a","b"), ("A","B"), "` + ci + `")`, "true"},
		{`deep-equal(("a","b"), ("A","c"), "` + ci + `")`, "false"},
		// A collation decides string equality only; numbers are unaffected.
		{`deep-equal((1, 2), (1, 2), "` + ci + `")`, "true"},
		{`deep-equal((1, 2), (1, 3), "` + ci + `")`, "false"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	// An unsupported collation is an error rather than being ignored.
	if _, err := Eval(`deep-equal(("a"), ("a"), "http://example.com/nope")`, ctx, nil); err == nil {
		t.Error("an unknown collation was accepted")
	}
}

// A numeric literal too large for a double is not a syntax error.
func TestOversizedNumericLiterals(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	big400 := strings.Repeat("9", 400)
	for _, c := range []struct{ expr, want string }{
		// An xs:integer literal is arbitrary-precision, so a 400-digit one is
		// an ordinary value. It was refused outright, because the lexer's
		// convenience float conversion overflowed and reported the error.
		{big400, big400},
		{"-" + big400, "-" + big400},
		{`999999999999999999999999`, "999999999999999999999999"},

		// An xs:double literal that overflows is INF, which is what IEEE 754
		// produces and what the suite asserts.
		{`1e400`, "INF"},
		{`-1e400`, "-INF"},
		{`999999999E100000000000000000000000000000000`, "INF"},
		{`1e-400`, "0"},

		// The ordinary cases are unaffected.
		{`1e2`, "100"},
		{`1.5`, "1.5"},
		{`42`, "42"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("literal %.30s… = %q, want %.30q", c.expr, got, c.want)
		}
	}
	// A malformed literal is still XPST0003.
	for _, expr := range []string{`1e`, `1.2.3`} {
		if _, err := Compile(expr, testResolver{}); err == nil {
			t.Errorf("%s compiled; it is not a valid literal", expr)
		}
	}
}

// An xs:time has no date, and two more argument rules.
func TestTimeComparisonAndArgumentRules(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want, code string }{
		// Timezone adjustment can roll a time past midnight — 10:00:00-07:00
		// in +10:00 is 03:00:00 the following day — and the date its
		// representation carries is not part of the value. Comparing it made
		// the result unequal to the identical literal.
		{`adjust-time-to-timezone(xs:time("10:00:00-07:00"), xs:dayTimeDuration("PT10H")) eq xs:time("03:00:00+10:00")`, "true", ""},
		{`string(adjust-time-to-timezone(xs:time("10:00:00-07:00"), xs:dayTimeDuration("PT10H")))`, "03:00:00+10:00", ""},
		{`xs:time("03:00:00+10:00") eq xs:time("03:00:00+10:00")`, "true", ""},
		{`xs:time("01:00:00Z") lt xs:time("02:00:00Z")`, "true", ""},
		{`xs:time("01:00:00Z") eq xs:time("02:00:00Z")`, "false", ""},
		// A date still compares by its date.
		{`xs:date("2024-01-01") lt xs:date("2024-01-02")`, "true", ""},

		// The zero-argument form is what means "no code"; passing the empty
		// sequence to the one-argument form identifies nothing.
		{`error(())`, "", "XPTY0004"},
		{`error()`, "", "FOER0000"},

		// An unusable URI is FODC0005, reported before access is attempted:
		// ":/" is not a URI, so whether a resolver is configured is moot.
		{`doc(':/')`, "", "FODC0005"},
		{`doc('%')`, "", "FODC0005"},
		{`doc('nonesuch.xml')`, "", "FODC0002"},
	} {
		v, err := Eval(c.expr, ctx, nil)
		if c.code != "" {
			if err == nil {
				t.Errorf("%s was accepted, want %s", c.expr, c.code)
				continue
			}
			if got := xdm.ErrorCode(err); got != c.code {
				t.Errorf("%s gave %s, want %s", c.expr, got, c.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Two more places where the XML Schema regex flavour and RE2 differ, both in
// what they accept rather than what they match.
func TestReplacementGroupsAndRepeatCounts(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want string }{
		// A group reference takes as many digits as name a group that exists;
		// the rest are literal text. "$1520" against fifteen groups is group
		// 15 followed by "20". Taking every digit made the whole reference
		// vanish, since no such group could match.
		{`replace("abracadabra", "((((( ((((( (((((a))))) ))))) )))))", "$1520", "x")`,
			"a20bra20ca20da20bra20"},
		{`replace("abracadabra", "((((( ((((( (((((a)(b))))) ))))) )))))", "($14.$15.$16.$17)", "x")`,
			"(ab.a.b.ab7)racad(ab.a.b.ab7)ra"},
		{`replace("abc", "(a)(b)", "$2$1")`, "bac"},
		{`replace("abc", "b", "X")`, "aXc"},

		// A repeat count above RE2's limit is a valid XML Schema pattern that
		// simply matches nothing. Reporting FORX0002 said it was malformed
		// when it was merely enormous.
		{`matches("aaa", "a{2147483647}")`, "false"},
		{`matches("aaa", "a{2}")`, "true"},
		{`matches("aaa", "a{2,3}")`, "true"},
		{`matches("aaa", "a{5}")`, "false"},
		{`matches("aaa", "a{1,2147483647}")`, "true"},
		// An escaped brace is still a literal.
		{`matches("a{b", "a\{b")`, "true"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// A general comparison against a bare range is answered from its bounds
// rather than by building it: the range may name more integers than the item
// limit allows, and materialising ten million of them to find out whether one
// value is among them is the wrong shape of work regardless.
//
// The short-circuit has to agree with the ordinary path exactly, so every
// operator is cross-checked against the materialised answer on a range small
// enough to build. Wrapping the range in a predicate is what forces that path
// — the shortcut only recognises a bare "lo to hi".
func TestRangeComparisonShortCircuit(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ops := []string{"=", "!=", "<", "<=", ">", ">="}
	for _, n := range []int{0, 1, 2, 3, 5, 6, 7} {
		for _, op := range ops {
			short := fmt.Sprintf("%d %s 2 to 5", n, op)
			// Force the general path by wrapping the range so it is not bare.
			long := fmt.Sprintf("%d %s (2 to 5)[true()]", n, op)
			a := evalOne(t, ctx, short)
			b := evalOne(t, ctx, long)
			if a != b {
				t.Errorf("%-14s short=%s materialised=%s", short, a, b)
			}
		}
	}
	// A descending (empty) range.
	for _, op := range ops {
		short := fmt.Sprintf("1 %s 5 to 2", op)
		long := fmt.Sprintf("1 %s (5 to 2)[true()]", op)
		if a, b := evalOne(t, ctx, short), evalOne(t, ctx, long); a != b {
			t.Errorf("%-14s short=%s materialised=%s", short, a, b)
		}
	}
}

// A value has to equal its own lexical form, and two dates are the same value
// when they name the same instant.
func TestDecimalPrecisionAndInstantIdentity(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want string }{
		// Division is the only operation producing a non-terminating
		// rational. Keeping it exact made a value that rendered at 18 digits
		// but compared as the fraction, so it did not equal what it printed.
		{`string(xs:positiveInteger("1") div xs:positiveInteger("999999999999999999"))`,
			"0.000000000000000001"},
		{`xs:positiveInteger("1") div xs:positiveInteger("999999999999999999") eq 0.000000000000000001`,
			"true"},
		{`xs:integer("-1") div xs:integer("999999999999999999") eq -0.000000000000000001`,
			"true"},
		// Terminating divisions are exact and unaffected.
		{`string(1 div 8)`, "0.125"},
		{`1 div 8 eq 0.125`, "true"},
		{`string(10 div 4)`, "2.5"},

		// A dateTime with no timezone takes the implicit one, so it is the
		// same value as the same instant written with one. Keying
		// fn:distinct-values on the lexical form kept them apart.
		{`count(distinct-values((xs:dateTime("2008-01-01T13:00:00"), adjust-dateTime-to-timezone(xs:dateTime("2008-01-01T13:00:00")))))`,
			"1"},
		{`count(distinct-values((xs:dateTime("2008-01-01T13:00:00Z"), xs:dateTime("2008-01-01T14:00:00+01:00"))))`,
			"1"},
		{`count(distinct-values((xs:dateTime("2008-01-01T13:00:00Z"), xs:dateTime("2008-01-01T14:00:00Z"))))`,
			"2"},
		// A Gregorian type names no instant, so it keeps its lexical key.
		{`count(distinct-values((xs:gMonthDay("--01-15"), xs:gMonthDay("--01-16"))))`, "2"},

		// Two durations are the same value when their months and seconds
		// agree, whatever subtype each is. Keying on the type as well kept
		// the two zero durations apart, since their canonical forms are
		// "P0M" and "PT0S".
		{`count(distinct-values((xs:yearMonthDuration("P0Y"), xs:dayTimeDuration("P0D"))))`, "1"},
		{`count(distinct-values((xs:yearMonthDuration("P1Y"), xs:yearMonthDuration("P12M"))))`, "1"},
		{`count(distinct-values((xs:dayTimeDuration("P1D"), xs:dayTimeDuration("PT24H"))))`, "1"},
		{`count(distinct-values((xs:yearMonthDuration("P1Y"), xs:dayTimeDuration("P1D"))))`, "2"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// fn:document-uri is defined on a document node, and returns the empty
// sequence for anything else — walking to the node's root gave an element the
// URI of the document containing it, which is fn:base-uri's answer.
func TestDocumentURIOnlyOnDocuments(t *testing.T) {
	root := mustParse(t, `<works><employee name="x">3</employee></works>`)
	ctx := NewContext(root, Builtins())
	for _, c := range []struct{ expr, want string }{
		{`count(document-uri(/works[1]/employee[1]))`, "0"},
		{`count(document-uri(/works[1]/employee[1]/@name))`, "0"},
		{`count(document-uri(/works))`, "0"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// fn:static-base-uri returns the base URI of the expression, not of the
// context node. The two differ whenever a stylesheet is applied to a document
// from somewhere else, which is the ordinary case.
func TestStaticBaseURI(t *testing.T) {
	root := mustParse(t, `<r/>`)
	root.BaseURI = "file:///docs/input.xml"

	// With none supplied, the context node's is the best available answer.
	ctx := NewContext(root, Builtins())
	if got := evalOne(t, ctx, `string(static-base-uri())`); got != "file:///docs/input.xml" {
		t.Errorf("with no static base URI = %q", got)
	}

	// When one is supplied it wins, and it is not the document's.
	ctx.StaticBaseURI = "http://www.example.com"
	if got := evalOne(t, ctx, `string(static-base-uri())`); got != "http://www.example.com" {
		t.Errorf("static-base-uri() = %q, want the declared one", got)
	}

	// With neither, the result is the empty sequence rather than "".
	bare := NewContext(nil, Builtins())
	if got := evalOne(t, bare, `count(static-base-uri())`); got != "0" {
		t.Errorf("with no base URI at all, count = %q, want 0", got)
	}
}

// An aggregate over untyped element content fails on the value, not the type.
func TestAggregateOverUntypedContent(t *testing.T) {
	root := mustParse(t, `<works><employee>Jane Doe</employee><n>10</n></works>`)
	ctx := NewContext(root, Builtins())

	// Element content is untyped, so the conversion to xs:double is defined
	// and what failed was the data: FORG0001, not FORG0006. Reporting the
	// type error said the expression was wrong when the document was.
	for _, expr := range []string{`avg(/works/employee)`, `sum(/works/employee)`} {
		_, err := Eval(expr, ctx, testNS{})
		if got := xdm.ErrorCode(err); got != "FORG0001" {
			t.Errorf("%s gave %v (%s), want FORG0001", expr, err, got)
		}
	}
	// Numeric content still works, and a genuinely wrong *type* is still
	// FORG0006.
	if got := evalOne(t, ctx, `sum(/works/n)`); got != "10" {
		t.Errorf("sum over numeric content = %q", got)
	}
	if _, err := Eval(`sum((xs:QName("a")))`, ctx, testNS{}); xdm.ErrorCode(err) != "FORG0006" {
		t.Errorf("sum over a QName gave %v, want FORG0006", err)
	}
}

// A cast to xs:QName has to resolve its prefix, which only the parser can do.
func TestQNameCastResolvesPrefix(t *testing.T) {
	ns := testResolver{"xs": xdm.NSXS, "myPrefix": "http://example.com/"}
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, want string }{
		// CastAtomic sees a string and a target type and nothing else, so a
		// prefixed name reaching it became a QName with no namespace.
		// xs:QName() was already folded in the parser; "cast as" was not.
		{`namespace-uri-from-QName("myPrefix:ncname" cast as xs:QName)`, "http://example.com/"},
		{`local-name-from-QName("myPrefix:ncname" cast as xs:QName)`, "ncname"},
		{`"myPrefix:ncname" cast as xs:QName eq QName("http://example.com/", "anotherPrefix:ncname")`, "true"},
		// An unprefixed name has no prefix to resolve and was already right.
		{`local-name-from-QName("ncname" cast as xs:QName)`, "ncname"},
	} {
		v, err := Eval(c.expr, ctx, ns)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(v) == 1 {
			got = v[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	// An unbound prefix is FONS0004 — the namespace-function code — rather
	// than the static XPST0081, because the prefix arrives as a string
	// argument rather than written in the expression.
	if _, err := Compile(`"nope:x" cast as xs:QName`, ns); xdm.ErrorCode(err) != "FONS0004" {
		t.Errorf("unbound prefix in a cast gave %v, want FONS0004", err)
	}
	// Both halves must be NCNames, and an unusable lexical form is FORG0001:
	// a bad value for the target type, not an argument of the wrong type.
	// Checking only for an empty local part let ":x" through, where the
	// prefix is empty, the local part is not, and the colon vanished.
	for _, expr := range []string{
		`xs:QName("")`, `xs:QName(":x")`, `xs:QName("a:")`,
		`xs:QName("1bad")`, `"" cast as xs:QName`, `":x" cast as xs:QName`,
	} {
		if _, err := Compile(expr, ns); xdm.ErrorCode(err) != "FORG0001" {
			t.Errorf("%s gave %v, want FORG0001", expr, err)
		}
	}
	// And the legal forms still compile.
	for _, expr := range []string{
		`xs:QName("ncname")`, `xs:QName("myPrefix:ncname")`,
		`"ncname" cast as xs:QName`,
	} {
		if _, err := Compile(expr, ns); err != nil {
			t.Errorf("%s was refused: %v", expr, err)
		}
	}
}

// A duration whose month count fits is a legal value however large; it is the
// arithmetic that overflows.
func TestDurationMonthOverflow(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	// Refusing this at parse reported FORG0001 — "the lexical form is wrong"
	// — for a form that is written perfectly well.
	if got := evalOne(t, ctx, `string(xs:yearMonthDuration("P768614336404564650Y"))`); got != "P768614336404564650Y" {
		t.Errorf("large duration = %q", got)
	}
	// Adding to it overflows, which is FODT0002 and has to survive fn:sum's
	// wrapper: an overflow is not an invalid argument type.
	for _, expr := range []string{
		`avg((xs:yearMonthDuration("P768614336404564650Y"), xs:yearMonthDuration("P1Y")))`,
		`sum((xs:yearMonthDuration("P768614336404564650Y"), xs:yearMonthDuration("P1Y")))`,
	} {
		_, err := Eval(expr, ctx, nil)
		if got := xdm.ErrorCode(err); got != "FODT0002" {
			t.Errorf("%s gave %v (%s), want FODT0002", expr, err, got)
		}
	}
	// A genuinely uncombinable pair is still FORG0006.
	if _, err := Eval(`sum((xs:yearMonthDuration("P1Y"), xs:dayTimeDuration("P1D")))`, ctx, nil); xdm.ErrorCode(err) != "FORG0006" {
		t.Errorf("mixed duration subtypes gave %v, want FORG0006", err)
	}
	// And ordinary duration arithmetic is unaffected.
	if got := evalOne(t, ctx, `string(sum((xs:yearMonthDuration("P1Y"), xs:yearMonthDuration("P6M"))))`); got != "P1Y6M" {
		t.Errorf("sum of two durations = %q", got)
	}
}

// Values that overflowed a machine word at the *arithmetic*, having parsed
// perfectly well. Each produced a wrong answer rather than an error, and one
// produced a panic.
//
// The shape recurs: a big.Int or big.Rat narrowed with .Int64() or int(...) at
// a site whose sibling path already range-checks. FitsInt64/IsInt64 are the
// established guard; these are the call sites that had not adopted it.
func TestArithmeticOverflowIsRefused(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	for _, c := range []struct{ expr, code string }{
		// The range shortcut checked each endpoint for fitting int64 but not
		// the span between them, which needs one bit more. The count wrapped
		// to zero and avg() divided by it — a panic.
		{`avg(-9223372036854775808 to 9223372036854775807)`, "*"},
		{`count(-4611686018427387904 to 4611686018427387904)`, "*"},
		{`sum(-9223372036854775808 to 9223372036854775807)`, "*"},

		// A duration scaled by a number: roundRat returns an int64 from an
		// unbounded big.Int and scaleDuration narrowed it with int(...), so a
		// positive duration times a positive number came back negative.
		{`xs:yearMonthDuration("P768614336404564650Y") * 4`, "FODT0002"},
		{`xs:yearMonthDuration("P2Y") * 1e18`, "FODT0002"},
		{`xs:yearMonthDuration("P768614336404564650Y") div 0.0000001`, "FODT0002"},

		// Date plus duration: the month total was computed in int, and the
		// second count narrowed with Int64() unchecked.
		{`xs:date("2000-01-01") + xs:yearMonthDuration("P768614336404564650Y")`, "FODT0001"},
		{`xs:date("2000-01-01") + xs:dayTimeDuration("P100000000000000000D")`, "FODT0001"},

		// The year bound below zero is not -maxYear: the era division rounds
		// toward negative infinity, which costs about 1970 years. Years in
		// that gap parsed and then compared as *positive* day counts, so a
		// BCE date ordered after the year 2000.
		{`xs:date("-25252734927766553-01-01") lt xs:date("2000-01-01")`, "FODT0001"},
	} {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked: %v", c.expr, r)
				}
			}()
			_, err = Eval(c.expr, ctx, nil)
		}()
		if err == nil {
			t.Errorf("%s was accepted; it overflows", c.expr)
			continue
		}
		if c.code != "*" && xdm.ErrorCode(err) != c.code {
			t.Errorf("%s gave %s, want %s", c.expr, xdm.ErrorCode(err), c.code)
		}
	}

	// A position outside int64 is past one end, which the callers' own bounds
	// checks handle — but Int64() truncates, so 2^64+2 arrived as 2 and
	// inserted into the middle of the sequence instead of after it.
	for _, c := range []struct{ expr, want string }{
		{`string-join(for $i in insert-before((1,2,3), 18446744073709551618, 99) return string($i), ",")`, "1,2,3,99"},
		{`string-join(for $i in remove((1,2,3), 18446744073709551618) return string($i), ",")`, "1,2,3"},
		{`string-join(for $i in insert-before((1,2,3), 2, 99) return string($i), ",")`, "1,99,2,3"},
		{`string-join(for $i in remove((1,2,3), 2) return string($i), ",")`, "1,3"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// The shortcut still answers a huge range arithmetically, which is what
	// it exists for: the bound is what the closed forms can represent, not
	// the item limit.
	for _, c := range []struct{ expr, want string }{
		{`sum(1 to 4000000000)`, "8000000002000000000"},
		{`count(1 to 10000000)`, "10000000"},
		{`avg(1 to 10)`, "5.5"},
		{`string(xs:date("2000-01-01") + xs:yearMonthDuration("P1Y"))`, "2001-01-01"},
		{`string(xs:yearMonthDuration("P2Y") * 3)`, "P6Y"},
	} {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}
