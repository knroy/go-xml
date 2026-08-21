package xpath

import (
	"strings"
	"testing"
)

// These cover the functions added after auditing the library against the
// XPath 2.0 / F&O required-function list, which found 24 missing.

func TestDeepEqual(t *testing.T) {
	doc := `<r><a><b>x</b></a><a><b>x</b></a><a><b>y</b></a></r>`
	cases := []struct{ expr, want string }{
		{`deep-equal((1,2,3), (1,2,3))`, "true"},
		{`deep-equal((1,2), (1,2,3))`, "false"},
		{`deep-equal((), ())`, "true"},
		{`deep-equal(1, 1.0)`, "true"}, // equal by value across numeric types
		{`deep-equal('a', 'a')`, "true"},
		{`deep-equal('a', 'b')`, "false"},
		// Identical subtrees compare equal; differing content does not.
		{`deep-equal(//a[1], //a[2])`, "true"},
		{`deep-equal(//a[1], //a[3])`, "false"},
		{`deep-equal(//a[1], //a[1])`, "true"},
	}
	for _, c := range cases {
		if got := evalStr(t, doc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestDeepEqualIgnoresCommentsAndAttributeOrder(t *testing.T) {
	// Comments are not part of the compared content, and attributes are
	// matched by name rather than position.
	doc := `<r><a p="1" q="2"><!--c-->x</a><a q="2" p="1">x</a></r>`
	if got := evalStr(t, doc, `deep-equal(//a[1], //a[2])`); got != "true" {
		t.Errorf("got %q, want true (comments and attribute order are not significant)", got)
	}
}

func TestDeepEqualNaN(t *testing.T) {
	// NaN is deep-equal to NaN, unlike under "eq" — the spec makes this
	// exception so identical sequences compare equal.
	if got := evalStr(t, testDoc, `deep-equal(number('x'), number('x'))`); got != "true" {
		t.Errorf("deep-equal(NaN, NaN) = %q, want true", got)
	}
	if got := evalStr(t, testDoc, `number('x') eq number('x')`); got != "false" {
		t.Errorf("NaN eq NaN = %q, want false", got)
	}
}

func TestQNameFunctions(t *testing.T) {
	doc := `<r xmlns:p="urn:p"><e/></r>`
	cases := []struct{ expr, want string }{
		{`local-name-from-QName(QName('urn:x','pre:local'))`, "local"},
		{`namespace-uri-from-QName(QName('urn:x','pre:local'))`, "urn:x"},
		{`prefix-from-QName(QName('urn:x','pre:local'))`, "pre"},
		{`prefix-from-QName(QName('urn:x','local'))`, ""}, // no prefix: empty sequence
		{`namespace-uri-for-prefix('p', //e)`, "urn:p"},
		{`local-name-from-QName(resolve-QName('p:z', //e))`, "z"},
		{`namespace-uri-from-QName(resolve-QName('p:z', //e))`, "urn:p"},
	}
	for _, c := range cases {
		if got := evalStr(t, doc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestInScopePrefixes(t *testing.T) {
	doc := `<r xmlns:p="urn:p" xmlns:q="urn:q"><e/></r>`
	got := evalStr(t, doc, `count(in-scope-prefixes(//e))`)
	// p, q and the always-present xml.
	if got != "3" {
		t.Errorf("count(in-scope-prefixes()) = %q, want 3", got)
	}

	// The order is pinned as well as the count. In-scope namespaces are
	// held in a map, and Go randomises map iteration, so before they were
	// sorted this function returned a different order from run to run —
	// four distinct orders over forty runs of one four-prefix document.
	// XPath leaves the order implementation-dependent, so the unsorted
	// answer conformed; it was just useless for anything a caller wants to
	// compare, print or test against.
	wide := `<r xmlns:a="urn:a" xmlns:b="urn:b" xmlns:c="urn:c" xmlns:d="urn:d"><e/></r>`
	for i := 0; i < 20; i++ {
		got := evalStr(t, wide, `string-join(in-scope-prefixes(//e), ',')`)
		if want := "a,b,c,d,xml"; got != want {
			t.Fatalf("in-scope-prefixes order = %q, want %q", got, want)
		}
	}
}

func TestResolveURI(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`resolve-uri('b.xml', 'http://x/a/c.xml')`, "http://x/a/b.xml"},
		{`resolve-uri('../b.xml', 'http://x/a/c/d.xml')`, "http://x/a/b.xml"},
		{`resolve-uri('http://y/z', 'http://x/a/')`, "http://y/z"},
		{`resolve-uri((), 'http://x/')`, ""},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestURIEscaping(t *testing.T) {
	cases := []struct{ expr, want string }{
		// encode-for-uri escapes everything outside the unreserved set...
		{`encode-for-uri('a/b c')`, "a%2Fb%20c"},
		// ...while iri-to-uri leaves URI syntax alone.
		{`iri-to-uri('http://x/a b')`, "http://x/a%20b"},
		// fn:escape-html-uri escapes only characters outside #x20-#x7E, so a
		// space survives: the function reproduces what a browser does with an
		// href rather than producing a valid URI. Verified against Saxon.
		{`escape-html-uri('http://x/a b')`, "http://x/a b"},
		{`escape-html-uri('http://x/aéb')`, "http://x/a%C3%A9b"},
		{`codepoint-equal('a','a')`, "true"},
		{`codepoint-equal('a','b')`, "false"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestDateTimeConstructor(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`dateTime(xs:date('2024-01-15'), xs:time('13:45:00'))`, "2024-01-15T13:45:00"},
		{`dateTime(xs:date('2024-01-15Z'), xs:time('13:45:00'))`, "2024-01-15T13:45:00Z"},
		{`dateTime((), xs:time('13:45:00'))`, ""},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestDateTimeConstructorRejectsConflictingTimezones(t *testing.T) {
	err := evalErr(t, testDoc, `dateTime(xs:date('2024-01-15Z'), xs:time('13:45:00+05:00'))`)
	if !strings.Contains(err.Error(), "FORG0008") {
		t.Errorf("err = %v, want FORG0008", err)
	}
}

func TestFormatDateTime(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`format-dateTime(xs:dateTime('2024-01-05T09:07:03'), '[Y]-[M01]-[D01]')`, "2024-01-05"},
		{`format-dateTime(xs:dateTime('2024-01-05T09:07:03'), '[H01]:[m01]:[s01]')`, "09:07:03"},
		{`format-date(xs:date('2024-01-05'), '[D]/[M]/[Y]')`, "5/1/2024"},
		{`format-time(xs:time('13:45:00'), '[h]:[m01] [P]')`, "1:45 pm"},
		{`format-dateTime(xs:dateTime('2024-01-05T09:07:03'), '[[literal]]')`, "[literal]"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestFormatDateTimeRejectsUnknownComponent(t *testing.T) {
	// An unrecognised marker must not be emitted literally, or the output
	// silently contains picture syntax.
	if err := evalErrXSLT(t, testDoc, `format-date(xs:date('2024-01-05'), '[Q]')`); err == nil {
		t.Error("an unknown picture component should error")
	}
}

func TestIDLookup(t *testing.T) {
	doc := `<r><a xml:id="one"/><b id="two"/><c idref="one"/></r>`
	if got := evalStr(t, doc, `local-name(id('one', /r))`); got != "a" {
		t.Errorf("id('one') = %q, want a", got)
	}
	if got := evalStr(t, doc, `local-name(id('two', /r))`); got != "b" {
		t.Errorf("id('two') = %q, want b", got)
	}
	if got := evalStr(t, doc, `local-name(idref('one', /r))`); got != "c" {
		t.Errorf("idref('one') = %q, want c", got)
	}
	if got := evalStr(t, doc, `count(id('missing', /r))`); got != "0" {
		t.Errorf("id of a missing name = %q, want 0", got)
	}
}

func TestUnsupportedFunctionsFailLoudly(t *testing.T) {
	// Each of these would produce a silently wrong result if it returned a
	// plausible value instead of refusing.
	for _, expr := range []string{
		`collection()`,
		// FULLY-NORMALIZED needs the UAX #15 construction rules beyond the
		// four standard forms, so it is refused rather than approximated.
		`normalize-unicode('abc', 'FULLY-NORMALIZED')`,
		// fn:unparsed-text is an XSLT function, so a plain XPath expression
		// must not find it at all. That it also refuses to read a file once a
		// stylesheet *does* have it is checked in the xslt package.
		`unparsed-text('x.txt')`,
		`unparsed-text-available('x.txt')`,
	} {
		if err := evalErr(t, testDoc, expr); err == nil {
			t.Errorf("%s should fail rather than return a wrong answer", expr)
		}
	}
	// An empty normalisation form is a no-op and must succeed.
	if got := evalStr(t, testDoc, `normalize-unicode('abc', '')`); got != "abc" {
		t.Errorf("normalize-unicode with an empty form = %q, want abc", got)
	}
}

func TestUnorderedPreservesItems(t *testing.T) {
	if got := evalStr(t, testDoc, `count(unordered((1,2,3)))`); got != "3" {
		t.Errorf("unordered dropped items: %q", got)
	}
}

func TestDefaultCollation(t *testing.T) {
	got := evalStr(t, testDoc, `default-collation()`)
	if !strings.Contains(got, "codepoint") {
		t.Errorf("default-collation() = %q, want the codepoint collation", got)
	}
}

func TestGregorianTypes(t *testing.T) {
	// The five Gregorian types denote a recurring or partial calendar point.
	// Each has its own leading-hyphen convention, which exists so the forms
	// cannot be confused with a truncated date.
	cases := []struct{ expr, want string }{
		{`xs:gYear('2024')`, "2024"},
		{`xs:gYear('-0044')`, "-0044"},
		{`xs:gYearMonth('2024-01')`, "2024-01"},
		{`xs:gMonth('--01')`, "--01"},
		{`xs:gMonthDay('--01-15')`, "--01-15"},
		{`xs:gDay('---15')`, "---15"},
		{`xs:gYear('2024Z')`, "2024Z"},
		{`xs:gYearMonth('2024-01+05:30')`, "2024-01+05:30"},
		// Extraction from a date drops the components the target omits.
		{`xs:date('2024-03-15') cast as xs:gYear`, "2024"},
		{`xs:date('2024-03-15') cast as xs:gYearMonth`, "2024-03"},
		{`'2024' castable as xs:gYear`, "true"},
		{`'--01-15' instance of xs:gMonthDay`, "false"}, // a string, not the type
		{`xs:gMonthDay('--01-15') instance of xs:gMonthDay`, "true"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestGregorianRejectsInvalid(t *testing.T) {
	for _, expr := range []string{
		`xs:gMonth('01')`,         // missing the "--" prefix
		`xs:gDay('--15')`,         // needs three hyphens
		`xs:gMonth('--13')`,       // month out of range
		`xs:gDay('---32')`,        // day out of range
		`xs:gYear('00')`,          // fewer than four digits
		`xs:gMonthDay('--02-30')`, // February never has 30 days
	} {
		// Note that "0000" is *not* in this list. XSD 1.1 admits year zero,
		// meaning 1 BCE, where 1.0 excluded it; the conformance suite tests
		// against 1.1.
		if err := evalErr(t, testDoc, expr); err == nil {
			t.Errorf("%s should be rejected", expr)
		}
	}
	// --02-29 is legal: with the year unspecified it is validated against a
	// leap year.
	if got := evalStr(t, testDoc, `xs:gMonthDay('--02-29')`); got != "--02-29" {
		t.Errorf("--02-29 = %q, want it accepted", got)
	}
}

func TestGregorianEqualityButNotOrdering(t *testing.T) {
	// Equality is defined; ordering is not, because without a year "is
	// --01-15 before --02-01" has no answer that holds for every year.
	if got := evalStr(t, testDoc, `xs:gYear('2024') eq xs:gYear('2024')`); got != "true" {
		t.Errorf("gYear equality = %q, want true", got)
	}
	if got := evalStr(t, testDoc, `xs:gMonth('--01') eq xs:gMonth('--02')`); got != "false" {
		t.Errorf("gMonth inequality = %q, want false", got)
	}
	if err := evalErr(t, testDoc, `xs:gMonth('--01') lt xs:gMonth('--02')`); err == nil {
		t.Error("ordering Gregorian values should raise a type error")
	}
	// Different Gregorian types are not comparable to each other.
	if err := evalErr(t, testDoc, `xs:gYear('2024') eq xs:gMonth('--01')`); err == nil {
		t.Error("comparing gYear with gMonth should raise a type error")
	}
}

// The four normalisation forms, checked against Saxon-HE 12.4. "e" followed by
// a combining acute must compose to a single "é" under NFC and decompose back
// under NFD; the compatibility forms additionally fold ligatures and
// superscripts.
func TestNormalizeUnicode(t *testing.T) {
	const decomposed = "é" // e + COMBINING ACUTE ACCENT
	const composed = "é"    // é

	cases := []struct{ expr, want string }{
		{`normalize-unicode('` + decomposed + `', 'NFC')`, composed},
		{`normalize-unicode('` + composed + `', 'NFD')`, decomposed},
		// The default form is NFC.
		{`normalize-unicode('` + decomposed + `')`, composed},
		// Lowercase and surrounding space in the form name are accepted.
		{`normalize-unicode('` + decomposed + `', ' nfc ')`, composed},
		// Compatibility forms fold the ligature and the superscript.
		{`normalize-unicode('ﬁ', 'NFKC')`, "fi"},
		{`normalize-unicode('⁵', 'NFKD')`, "5"},
		// An empty form name means no normalisation at all.
		{`normalize-unicode('` + decomposed + `', '')`, decomposed},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// $position on fn:insert-before and fn:remove is declared xs:integer, not
// xs:integer?, so an empty sequence is a type error. Before this was checked
// the nil returned by the argument helper was dereferenced and the process
// crashed — found by the W3C QT3 suite, which is exactly the kind of edge a
// hand-written test set does not think to cover.
func TestEmptyPositionArgIsTypeError(t *testing.T) {
	for _, expr := range []string{
		`insert-before((1,2,3), (), (9))`,
		`remove((1,2,3), ())`,
	} {
		err := evalErr(t, testDoc, expr)
		if err == nil {
			t.Errorf("%s: expected XPTY0004, got no error", expr)
			continue
		}
		if !strings.Contains(err.Error(), "XPTY0004") {
			t.Errorf("%s: error = %v, want XPTY0004", expr, err)
		}
	}
}

// A huge precision argument to fn:round / fn:round-half-to-even must not be
// used as a literal exponent of ten. Before this was clamped,
// round-half-to-even(3.567812, 4294967296) built a bignum with four billion
// digits and the process stopped responding — found by the W3C QT3 suite,
// where it hung the whole run rather than failing one case.
//
// Rounding to more places than a value has is the identity, which is also what
// Saxon-HE 12.4 returns for these.
func TestRoundPrecisionIsBounded(t *testing.T) {
	cases := []struct{ expr, want string }{
		// fn:round takes one argument in XPath 2.0 — the precision parameter
		// is a 3.0 addition — so the clamp is exercised through
		// fn:round-half-to-even, which does take two.
		{`round-half-to-even(3.567812, 4294967296)`, "3.567812"},
		{`round-half-to-even(1.5, -4294967296)`, "0"},
		// The ordinary cases must be unaffected by the clamp.
		{`round-half-to-even(3.567812, 2)`, "3.57"},
		{`round-half-to-even(1.5)`, "2"},
		{`round-half-to-even(2.5)`, "2"},
		{`round(2.5)`, "3"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// xs:hexBinary and xs:base64Binary are two encodings of the same octets, so a
// cast between them must decode and re-encode. Previously the lexical form was
// passed through as a plain xs:string, which lost the encoding and made
// hexBinary-to-base64Binary either an error or silently wrong. Found by the
// W3C QT3 suite; expectations match Saxon-HE 12.4.
func TestBinaryTypeCasts(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`xs:base64Binary(xs:hexBinary('0FB7'))`, "D7c="},
		{`xs:hexBinary(xs:base64Binary('D7c='))`, "0FB7"},
		{`string(xs:hexBinary('0FB7'))`, "0FB7"},
		// The canonical form of xs:hexBinary is upper case.
		{`string(xs:hexBinary('0fb7'))`, "0FB7"},
		// Round-tripping must be the identity.
		{`xs:hexBinary(xs:base64Binary(xs:hexBinary('DEADBEEF')))`, "DEADBEEF"},
		// A cast to the same type is a no-op.
		{`string(xs:base64Binary(xs:base64Binary('D7c=')))`, "D7c="},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// A lexical form that is not valid in the target encoding is an error rather
// than being reinterpreted: "D7c=" is base64, and reading it as hex digits
// would be nonsense.
func TestBinaryCastRejectsWrongEncoding(t *testing.T) {
	for _, expr := range []string{
		`xs:hexBinary('D7c=')`,
		`xs:hexBinary('zzzz')`,
		`xs:base64Binary('!!!!')`,
	} {
		if err := evalErr(t, testDoc, expr); err == nil {
			t.Errorf("%s was accepted; it is not valid in that encoding", expr)
		}
	}
}

// Rounding a negative double toward zero yields negative zero, which IEEE 754
// keeps distinct from positive zero and which survives serialisation as "-0".
// floor(-0.2 + 0.5) is floor(0.3), a positive zero, so the sign has to be
// restored. xs:decimal has no negative zero, so the decimal case stays "0".
// Found by the W3C QT3 suite; expectations match Saxon-HE 12.4.
func TestRoundKeepsNegativeZero(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`round(xs:double('-0.2'))`, "-0"},
		{`round(xs:double('-0.5'))`, "-0"},
		{`round-half-to-even(xs:double('-0.2'))`, "-0"},
		{`round(xs:double('0.2'))`, "0"},
		// A decimal has no signed zero.
		{`round(-0.2)`, "0"},
		// A negative value that does not round to zero is unaffected.
		{`round(xs:double('-1.5'))`, "-1"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// The casting table forbids conversions between unrelated value spaces. The
// cast functions look mostly at the lexical form, so without a source-type
// gate the base64Binary "10010101" cast happily to the double 10010101 and
// "castable as xs:float" answered true. Found by the W3C QT3 suite.
func TestCastTablePermissions(t *testing.T) {
	forbidden := []string{
		`xs:base64Binary('10010101') cast as xs:float`,
		`xs:base64Binary('10010101') cast as xs:decimal`,
		`xs:hexBinary('0FB7') cast as xs:integer`,
		`xs:date('2024-01-01') cast as xs:integer`,
		`xs:dayTimeDuration('PT1H') cast as xs:date`,
	}
	for _, expr := range forbidden {
		if err := evalErr(t, testDoc, expr); err == nil {
			t.Errorf("%s was permitted; the casting table forbids it", expr)
		}
	}
	// Conversions the table does permit must keep working.
	for _, c := range []struct{ expr, want string }{
		{`xs:base64Binary(xs:hexBinary('0FB7'))`, "D7c="},
		{`xs:string(xs:hexBinary('0FB7'))`, "0FB7"},
		{`xs:double('1.5')`, "1.5"},
		{`xs:dayTimeDuration(xs:duration('PT1H'))`, "PT1H"},
	} {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// "castable as xs:QName" asks only whether the lexical form is a legal QName.
func TestQNameCastable(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		{`'ABC' castable as xs:QName`, "true"},
		{`'a:b:c' castable as xs:QName`, "false"},
		{`'1abc' castable as xs:QName`, "false"},
		{`'' castable as xs:QName`, "false"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// The string subtypes apply whitespace facets: xs:normalizedString replaces
// tab/CR/LF with spaces and xs:token additionally collapses and trims. They
// had been mapped straight onto xs:string, making every one a no-op.
func TestStringSubtypeFacets(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`string-to-codepoints(xs:normalizedString(codepoints-to-string((32,9,48,13,10,48))))`,
			"32,32,48,32,32,48"},
		{`string-to-codepoints(xs:token(codepoints-to-string((32,9,48,13,10,48))))`, "48,32,48"},
		{`xs:token('  a   b  ')`, "a b"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	// The name-like subtypes validate as well as normalise.
	for _, expr := range []string{`xs:NCName('a:b')`, `xs:Name('1abc')`, `xs:language('toolongtag')`} {
		if err := evalErr(t, testDoc, expr); err == nil {
			t.Errorf("%s was accepted; it does not match the type's pattern", expr)
		}
	}
}

// XML Schema requires a digit on each side of a decimal point, so "PT.5S" and
// "PT30.S" are not durations. big.Rat.SetString accepts both.
func TestDurationLexicalValidation(t *testing.T) {
	for _, expr := range []string{
		`'PT.5S' cast as xs:duration`,
		`'PT30.S' cast as xs:duration`,
		`'PT1.2.3S' cast as xs:duration`,
	} {
		if err := evalErr(t, testDoc, expr); err == nil {
			t.Errorf("%s was accepted; it is not a valid duration", expr)
		}
	}
	if got := evalStr(t, testDoc, `'PT0.5S' cast as xs:duration`); got != "PT0.5S" {
		t.Errorf("PT0.5S = %q, want PT0.5S", got)
	}
}

// fn:codepoints-to-string must reject codepoints XML cannot represent.
// WriteRune silently substituted U+FFFD instead.
func TestCodepointsToStringValidates(t *testing.T) {
	for _, expr := range []string{
		`codepoints-to-string(0)`,
		`codepoints-to-string(55296)`,
		`codepoints-to-string(1114112)`,
	} {
		err := evalErr(t, testDoc, expr)
		if err == nil {
			t.Errorf("%s was accepted; it is not a valid XML character", expr)
			continue
		}
		if !strings.Contains(err.Error(), "FOCH0001") {
			t.Errorf("%s: error = %v, want FOCH0001", expr, err)
		}
	}
	if got := evalStr(t, testDoc, `codepoints-to-string((65,66))`); got != "AB" {
		t.Errorf("codepoints-to-string((65,66)) = %q, want AB", got)
	}
}

// Dividing two durations of the same subtype yields their ratio as an
// xs:decimal. The operator existed but was never reachable from the dispatch.
func TestDurationDivision(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`xs:dayTimeDuration('PT2H') div xs:dayTimeDuration('PT1H')`, "2"},
		{`xs:yearMonthDuration('P2Y') div xs:yearMonthDuration('P1Y')`, "2"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	// A zero-length divisor, and mixing the two subtypes, are both errors.
	for _, expr := range []string{
		`xs:dayTimeDuration('PT1H') div xs:dayTimeDuration('PT0S')`,
		`xs:dayTimeDuration('PT1H') div xs:yearMonthDuration('P1Y')`,
	} {
		if err := evalErr(t, testDoc, expr); err == nil {
			t.Errorf("%s was accepted", expr)
		}
	}
}

// In multi-line mode Go treats the position after a trailing newline as an
// empty final line; XML Schema does not, so "^$" must not match "abcd\ndefg\n".
func TestMatchesMultilineDollar(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`matches(concat('abcd', codepoints-to-string(10), 'defg', codepoints-to-string(10)), '^$', 'm')`, "false"},
		{`matches(concat('abcd', codepoints-to-string(10), codepoints-to-string(10), 'defg'), '^$', 'm')`, "true"},
		// Without the m flag the subject is untouched.
		{`matches('abc', '^abc$')`, "true"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// The derived types carry facets the primitive type codes cannot express, and
// a cast has to apply them: xs:byte(128) is not a byte. Both the constructor
// and "cast as"/"castable as" go through the check, since the parser erases
// the subtype into its primitive.
func TestDerivedTypeFacets(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`128 castable as xs:byte`, "false"},
		{`-129 castable as xs:byte`, "false"},
		{`127 castable as xs:byte`, "true"},
		{`-128 castable as xs:byte`, "true"},
		{`256 castable as xs:unsignedByte`, "false"},
		{`-1 castable as xs:unsignedByte`, "false"},
		{`0 castable as xs:positiveInteger`, "false"},
		{`1 castable as xs:positiveInteger`, "true"},
		{`-1 castable as xs:nonNegativeInteger`, "false"},
		// The string subtypes validate through the same path.
		{`'a:b' castable as xs:NCName`, "false"},
		{`'ab' castable as xs:NCName`, "true"},
		{`'a:b' castable as xs:Name`, "true"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// The constructor form enforces the same bounds.
	for _, expr := range []string{`xs:byte(128)`, `xs:unsignedByte(-1)`, `xs:positiveInteger(0)`} {
		if err := evalErr(t, testDoc, expr); err == nil {
			t.Errorf("%s was accepted; it is out of range for that type", expr)
		}
	}
	if got := evalStr(t, testDoc, `xs:byte(100)`); got != "100" {
		t.Errorf("xs:byte(100) = %q, want 100", got)
	}
	// Arithmetic on a subtype value still behaves as xs:integer.
	if got := evalStr(t, testDoc, `xs:byte(100) + 1`); got != "101" {
		t.Errorf("xs:byte(100) + 1 = %q, want 101", got)
	}
}

// The operands of "to" are declared xs:integer. A decimal is a type error
// rather than something to truncate: reading "1.1 to 3" as "1 to 3" invents a
// range the author did not write.
func TestRangeRequiresIntegers(t *testing.T) {
	for _, expr := range []string{`1.1 to 3`, `3 to 1.1`, `xs:double(1) to 3`} {
		err := evalErr(t, testDoc, expr)
		if err == nil {
			t.Errorf("%s was accepted; the operands must be xs:integer", expr)
			continue
		}
		if !strings.Contains(err.Error(), "XPTY0004") {
			t.Errorf("%s: error = %v, want XPTY0004", expr, err)
		}
	}
	if got := evalStr(t, testDoc, `count(1 to 3)`); got != "3" {
		t.Errorf("count(1 to 3) = %q, want 3", got)
	}
	// Untyped values from a document still convert, which is the usual rule.
	if got := evalStr(t, testDoc, `count(xs:untypedAtomic('1') to 3)`); got != "3" {
		t.Errorf("untypedAtomic range = %q, want 3", got)
	}
}

// fn:QName must reject a local part that is not an NCName; otherwise it builds
// a QName whose lexical form cannot be written in any document.
func TestQNameValidatesParts(t *testing.T) {
	for _, expr := range []string{
		`QName('http://x', '1person')`,
		`QName('http://x', '@person')`,
		`QName('http://x', '-person')`,
	} {
		if err := evalErr(t, testDoc, expr); err == nil {
			t.Errorf("%s was accepted; the local part is not an NCName", expr)
		}
	}
	if got := evalStr(t, testDoc, `local-name-from-QName(QName('http://x', 'p:person'))`); got != "person" {
		t.Errorf("valid QName = %q, want person", got)
	}
}

// A derived type is narrower than the primitive it erases to, so "instance of"
// runs the opposite way from a cast: a value built as xs:int is an xs:int and
// an xs:long, but a plain xs:integer literal is neither — it is their parent.
// The engine stores both as xs:integer, so the constructed value carries a
// note of the type it was built as. Expectations match Saxon-HE 12.4.
func TestDerivedTypeInstanceOf(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`xs:int(0) instance of xs:int`, "true"},
		{`xs:int(0) instance of xs:long`, "true"},
		{`xs:int(0) instance of xs:integer`, "true"},
		{`xs:long(0) instance of xs:int`, "false"},
		{`1 instance of xs:int`, "false"},
		{`12678967543233 instance of xs:int`, "false"},
		{`xs:byte(1) instance of xs:short`, "true"},
		{`xs:token('a') instance of xs:string`, "true"},
		{`xs:NCName('a') instance of xs:Name`, "true"},
		{`xs:Name('a') instance of xs:NCName`, "false"},
		// Every atomic value is an instance of the hierarchy root.
		{`false() instance of xs:anyAtomicType`, "true"},
		{`1 instance of xs:anyAtomicType`, "true"},
		{`'x' instance of xs:anyAtomicType`, "true"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	// The annotation must not disturb the value: arithmetic and comparison
	// work on the primitive.
	if got := evalStr(t, testDoc, `xs:int(2) + 3`); got != "5" {
		t.Errorf("xs:int(2) + 3 = %q, want 5", got)
	}
	if got := evalStr(t, testDoc, `xs:int(2) eq 2`); got != "true" {
		t.Errorf("xs:int(2) eq 2 = %q, want true", got)
	}
}

// XML Schema spells infinity "INF"; Go's scanner also accepts "Inf" and
// "Infinity". And an xs:integer needs integer *lexical* syntax, so "3.0" is a
// valid xs:decimal but not a valid xs:long.
func TestNumericLexicalForms(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`'INF' castable as xs:double`, "true"},
		{`'Inf' castable as xs:double`, "false"},
		{`'Infinity' castable as xs:double`, "false"},
		{`'NaN' castable as xs:double`, "true"},
		{`'3.0' castable as xs:long`, "false"},
		{`'3' castable as xs:long`, "true"},
		{`'3.0' castable as xs:decimal`, "true"},
		{`'1e5' castable as xs:double`, "true"},
		{`'1e' castable as xs:double`, "false"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// A zero-magnitude duration has no direction, so it carries no sign. Keeping
// the minus made two equal durations serialise differently.
func TestZeroDurationHasNoSign(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`string(xs:duration('-PT0S'))`, "PT0S"},
		{`string(xs:yearMonthDuration('-P0M'))`, "P0M"},
		{`string(xs:dayTimeDuration('-PT0S'))`, "PT0S"},
		// A non-zero duration keeps its sign.
		{`string(xs:dayTimeDuration('-PT1S'))`, "-PT1S"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Casting a dateTime to a Gregorian type must clear the components the target
// does not name. Copying the whole value left the source's month and day in
// place, so the result serialised correctly but compared unequal to the same
// value parsed from a string.
func TestGregorianCastNormalizesFields(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`xs:gYear(xs:dateTime('2002-11-23T23:12:23.867-13:37')) eq xs:gYear('2002-13:37')`, "true"},
		{`xs:gYear(xs:dateTime('2002-11-23T23:12:23Z')) eq xs:gYear('2002Z')`, "true"},
		{`string(xs:gYear(xs:dateTime('2002-11-23T23:12:23Z')))`, "2002Z"},
		{`xs:gMonth(xs:date('2002-11-23')) eq xs:gMonth('--11')`, "true"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Character-class subtraction, "[a-z-[aeiou]]", has no RE2 syntax, so both
// classes are expanded into codepoint ranges and the difference is emitted as
// an ordinary class. Expectations match Saxon-HE 12.4.
func TestRegexClassSubtraction(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`matches('b', '^[a-z-[aeiou]]$')`, "true"},
		{`matches('a', '^[a-z-[aeiou]]$')`, "false"},
		{`matches('z', '^[a-z-[aeiou]]$')`, "true"},
		// A subtraction that removes an interior range splits the class.
		{`matches('0', '^[0-9-[3-5]]$')`, "true"},
		{`matches('4', '^[0-9-[3-5]]$')`, "false"},
		{`matches('9', '^[0-9-[3-5]]$')`, "true"},
		// A hyphen between two classes is not subtraction and is unaffected.
		{`matches('x-y', '[a-z]-[a-z]')`, "true"},
		{`matches('abc', '^[a-c]+$')`, "true"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	// The multi-character escapes have fixed, small definitions, so a
	// subtraction involving one is computed rather than refused.
	for _, c := range []struct{ expr, want string }{
		{`matches('4', '^[\d-[357]]+$')`, "true"},
		{`matches('3', '^[\d-[357]]+$')`, "false"},
		{`matches('a', '^[\w-[b-y]]+$')`, "true"},
		{`matches('c', '^[\w-[b-y]]+$')`, "false"},
		{`matches('z', '^[\w-[b-y]]+$')`, "true"},
	} {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// \d is \p{Nd} in XML Schema — every decimal digit in Unicode, not the
	// ASCII ten. RE2 reads it as ASCII, so a pattern of "\d" silently
	// rejected digits the spec accepts.
	for _, c := range []struct{ expr, want string }{
		{`matches('0', '^\d+$')`, "true"}, // Fullwidth digit zero
		{`matches('᠙', '^\d+$')`, "true"}, // Mongolian digit nine
		{`matches('០', '^\d+$')`, "true"}, // Khmer digit zero
		{`matches('a', '^\d+$')`, "false"},
		// The same inside a class, which is a separate path.
		{`matches('0', '^[\d]+$')`, "true"},
		{`matches('0', '^[\D]+$')`, "false"},
	} {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// Subtraction from an arbitrary Unicode category is computed from Go's
	// own tables, so the left operand may be any category or block rather
	// than only the few the translator itself produces.
	for _, c := range []struct {
		expr string
		want string
	}{
		{`matches('a', '[\p{L}-[b]]')`, "true"},
		{`matches('b', '[\p{L}-[b]]')`, "false"},
		{`matches('!', '[\p{L}-[b]]')`, "false"},
		{`matches('a', '[\w-[\p{Ll}]]')`, "false"},
		{`matches('A', '[\w-[\p{Ll}]]')`, "true"},
	} {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// A double and a decimal are compared by promoting the decimal, so
// distinct-values must key both on the double. Keying a double on its exact
// rational gave xs:double(1.2) the binary expansion while the literal 1.2
// keyed as 6/5, and two values the spec calls equal were both kept.
func TestDistinctValuesNumericPromotion(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`count(distinct-values((1.2, xs:double(1.2))))`, "1"},
		{`count(distinct-values((1, xs:double(1))))`, "1"},
		{`count(distinct-values((1, 1.0, xs:double(1), xs:float(1))))`, "1"},
		// Genuinely different values are still distinguished.
		{`count(distinct-values((1, 2, 3)))`, "3"},
		{`count(distinct-values((1.2, 1.3)))`, "2"},
		// fn:distinct-values is defined on the *identity* of values rather
		// than on "eq", so unlike a comparison it collapses two NaNs into one.
		// Saxon agrees.
		{`count(distinct-values((xs:double('NaN'), xs:double('NaN'))))`, "1"},
	}
	for _, c := range cases {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// The namespace axis yields its bindings in a stable order.
//
// Axis order is observable — predicates number positions along the axis, so
// namespace::*[1] names whichever binding comes first — and the bindings are
// held in a map, whose iteration Go randomises. Before they were sorted this
// axis produced four distinct orders over forty runs of one four-prefix
// document, which made namespace::*[1] name a different prefix run to run.
//
// XPath leaves the order of this axis implementation-dependent, so any stable
// order conforms. An unstable one does not, in the sense a caller cares about.
func TestNamespaceAxisOrderIsStable(t *testing.T) {
	doc := `<r xmlns:a="urn:a" xmlns:b="urn:b" xmlns:c="urn:c" xmlns:d="urn:d"><e/></r>`
	for i := 0; i < 20; i++ {
		got := evalStr(t, doc, `string-join(for $n in /r/namespace::* return name($n), ',')`)
		if want := "a,b,c,d"; got != want {
			t.Fatalf("namespace axis order = %q, want %q", got, want)
		}
		// The positional predicate is the reason the order matters.
		if got := evalStr(t, doc, `name(/r/namespace::*[1])`); got != "a" {
			t.Fatalf("namespace::*[1] = %q, want %q", got, "a")
		}
	}
}
