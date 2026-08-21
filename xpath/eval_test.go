package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// testDoc is the document most evaluation tests run against. It is deliberately
// small but covers namespaces, attributes, mixed content and repeated elements.
const testDoc = `<?xml version="1.0"?>
<catalog xmlns:m="urn:meta" count="3">
  <book id="b1" price="30.50"><title>Go</title><author>Alan</author></book>
  <book id="b2" price="9.99"><title>XML</title><author>Beth</author></book>
  <book id="b3" price="45.00"><title>XSLT</title><author>Alan</author>
    <m:note>rare</m:note></book>
  <!-- a comment -->
</catalog>`

func mustParse(t *testing.T, doc string) *xdm.Node {
	t.Helper()
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tree.Root
}

// testNS resolves the prefixes used in the test expressions.
type testNS struct{ defaultElem string }

func (n testNS) ResolvePrefix(p string) (string, bool) {
	switch p {
	case "m":
		return "urn:meta", true
	case "xs":
		return xdm.NSXS, true
	case "fn":
		return xdm.NSFN, true
	case "xml":
		return xdm.NSXML, true
	}
	return "", false
}
func (n testNS) DefaultElementNamespace() string  { return n.defaultElem }
func (n testNS) DefaultFunctionNamespace() string { return xdm.NSFN }

// evalStr evaluates expr against doc and returns the result rendered as a
// comma-joined string, which makes sequence results easy to assert on.
func evalStr(t *testing.T, doc, expr string) string {
	t.Helper()
	root := mustParse(t, doc)
	ctx := NewContext(root, Builtins())
	seq, err := Eval(expr, ctx, testNS{})
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return renderSeq(seq)
}

// evalStrXSLT is evalStr against the library a stylesheet sees: the XPath
// builtins plus the functions XSLT adds. fn:format-date and fn:unparsed-text
// live there rather than in xpath.Builtins, because a plain XPath 2.0
// processor is required to report XPST0017 for them.
func evalStrXSLT(t *testing.T, doc, expr string) string {
	t.Helper()
	root := mustParse(t, doc)
	lib := NewLibrary(Builtins())
	RegisterXSLTFuncs(lib)
	seq, err := Eval(expr, NewContext(root, lib), testNS{})
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return renderSeq(seq)
}

// evalErrXSLT is evalErr against the same library.
func evalErrXSLT(t *testing.T, doc, expr string) error {
	t.Helper()
	root := mustParse(t, doc)
	lib := NewLibrary(Builtins())
	RegisterXSLTFuncs(lib)
	_, err := Eval(expr, NewContext(root, lib), testNS{})
	return err
}

func renderSeq(seq xdm.Sequence) string {
	parts := make([]string, 0, len(seq))
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			parts = append(parts, v.StringValue())
		case *xdm.Atomic:
			parts = append(parts, v.String())
		}
	}
	return strings.Join(parts, ",")
}

// evalErr expects evaluation to fail and returns the error.
func evalErr(t *testing.T, doc, expr string) error {
	t.Helper()
	root := mustParse(t, doc)
	ctx := NewContext(root, Builtins())
	_, err := Eval(expr, ctx, testNS{})
	if err == nil {
		t.Fatalf("eval %q: expected an error, got none", expr)
	}
	return err
}

func TestPathNavigation(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`/catalog/book/title`, "Go,XML,XSLT"},
		{`/catalog/book[1]/title`, "Go"},
		{`/catalog/book[last()]/title`, "XSLT"},
		{`/catalog/book[2]/@id`, "b2"},
		{`//title`, "Go,XML,XSLT"},
		{`//book[@id='b2']/title`, "XML"},
		{`/catalog/@count`, "3"},
		{`count(//book)`, "3"},
		{`//m:note`, "rare"},
		{`/catalog/book[3]/*[local-name()='note']`, "rare"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestAxes(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`/catalog/book[1]/child::title`, "Go"},
		{`/catalog/book[1]/attribute::id`, "b1"},
		{`(//title)[1]/parent::book/@id`, "b1"},
		{`//book[2]/preceding-sibling::book/@id`, "b1"},
		{`//book[2]/following-sibling::book/@id`, "b3"},
		{`//book[1]/ancestor::catalog/@count`, "3"},
		{`//m:note/ancestor-or-self::*/name()`, "catalog,book,m:note"},
		{`//book[1]/descendant::text()`, "Go,Alan"},
		{`//book[2]/self::book/@id`, "b2"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestReverseAxisPositionNumbering(t *testing.T) {
	// On a reverse axis, position 1 is the *nearest* node, not the first in
	// document order. This is the observable difference between axis order
	// and document order.
	got := evalStr(t, testDoc, `//book[3]/preceding-sibling::book[1]/@id`)
	if got != "b2" {
		t.Errorf("preceding-sibling::book[1] from b3 = %q, want b2 (nearest, not first)", got)
	}
	// The full axis result is still returned in document order.
	got = evalStr(t, testDoc, `//book[3]/preceding-sibling::book/@id`)
	if got != "b1,b2" {
		t.Errorf("preceding-sibling::book = %q, want document order b1,b2", got)
	}
}

func TestPredicatesNumericVsBoolean(t *testing.T) {
	// A numeric predicate selects by position; a boolean one filters.
	if got := evalStr(t, testDoc, `//book[2]/@id`); got != "b2" {
		t.Errorf("[2] = %q, want b2", got)
	}
	if got := evalStr(t, testDoc, `//book[true()]/@id`); got != "b1,b2,b3" {
		t.Errorf("[true()] = %q, want all", got)
	}
	// [@price > 20] filters; a node-set predicate is an existence test.
	if got := evalStr(t, testDoc, `//book[@price > 20]/@id`); got != "b1,b3" {
		t.Errorf("[@price > 20] = %q, want b1,b3", got)
	}
	if got := evalStr(t, testDoc, `//book[m:note]/@id`); got != "b3" {
		t.Errorf("[m:note] = %q, want b3", got)
	}
	// Chained predicates apply left to right, renumbering between them.
	if got := evalStr(t, testDoc, `//book[@price > 20][1]/@id`); got != "b1" {
		t.Errorf("[@price>20][1] = %q, want b1", got)
	}
}

func TestArithmeticTypes(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`1 + 2`, "3"},
		{`7 div 2`, "3.5"},  // integer div integer is decimal
		{`7 idiv 2`, "3"},   // idiv truncates
		{`-7 idiv 2`, "-3"}, // toward zero, not floor
		{`7 mod 2`, "1"},
		{`-7 mod 2`, "-1"},   // sign of the dividend
		{`0.1 + 0.2`, "0.3"}, // exact decimal, not 0.30000000000000004
		{`1.5 * 2`, "3"},
		{`1e2 + 1`, "101"},
		{`3 - 5`, "-2"},
		{`-(3)`, "-3"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestDivisionByZero(t *testing.T) {
	// Exact types error; doubles produce infinity.
	if err := evalErr(t, testDoc, `1 div 0`); !strings.Contains(err.Error(), "FOAR0001") {
		t.Errorf("1 div 0 error = %v, want FOAR0001", err)
	}
	if got := evalStr(t, testDoc, `1e0 div 0e0`); got != "INF" {
		t.Errorf("1e0 div 0e0 = %q, want INF", got)
	}
	if err := evalErr(t, testDoc, `1 idiv 0`); !strings.Contains(err.Error(), "FOAR0001") {
		t.Errorf("1 idiv 0 error = %v, want FOAR0001", err)
	}
}

func TestValueVsGeneralComparison(t *testing.T) {
	// General comparison is existential over both operands.
	if got := evalStr(t, testDoc, `//book/@price > 40`); got != "true" {
		t.Errorf("general comparison over a sequence = %q, want true", got)
	}
	// Value comparison requires singletons and must error on a 3-item operand.
	err := evalErr(t, testDoc, `//book/@price gt 40`)
	if !strings.Contains(err.Error(), "XPTY0004") {
		t.Errorf("value comparison on a sequence: error = %v, want XPTY0004", err)
	}
	// "=" and "!=" are not negations of each other over sequences: both are
	// true here, because some price equals 9.99 and some price does not.
	if got := evalStr(t, testDoc, `//book/@price = 9.99`); got != "true" {
		t.Errorf("= 9.99 -> %q, want true", got)
	}
	if got := evalStr(t, testDoc, `//book/@price != 9.99`); got != "true" {
		t.Errorf("!= 9.99 -> %q, want true (not the negation of =)", got)
	}
}

func TestUntypedAtomicComparison(t *testing.T) {
	// An attribute is untypedAtomic; compared with a number it becomes a
	// number, compared with a string it stays a string.
	if got := evalStr(t, testDoc, `//book[1]/@price > 30`); got != "true" {
		t.Errorf("numeric comparison of an attribute = %q, want true", got)
	}
	if got := evalStr(t, testDoc, `//book[1]/@id = 'b1'`); got != "true" {
		t.Errorf("string comparison of an attribute = %q, want true", got)
	}
	// Two untypedAtomic operands compare as strings, not numbers: "9.99" is
	// greater than "30.50" because '9' > '3'. This is the trap that makes
	// "@a < @b" wrong for numeric attributes unless one side is cast.
	if got := evalStr(t, testDoc, `//book[2]/@price < //book[1]/@price`); got != "false" {
		t.Errorf("untyped vs untyped must compare as strings; got %q", got)
	}
	// Casting one side forces the numeric comparison the author probably meant.
	if got := evalStr(t, testDoc, `xs:decimal(//book[2]/@price) < //book[1]/@price`); got != "true" {
		t.Errorf("numeric comparison after a cast = %q, want true", got)
	}
}

func TestNodeComparison(t *testing.T) {
	if got := evalStr(t, testDoc, `//book[1] is //book[1]`); got != "true" {
		t.Errorf("identity with itself = %q, want true", got)
	}
	if got := evalStr(t, testDoc, `//book[1] is //book[2]`); got != "false" {
		t.Errorf("identity of different nodes = %q, want false", got)
	}
	if got := evalStr(t, testDoc, `//book[1] << //book[3]`); got != "true" {
		t.Errorf("document order << = %q, want true", got)
	}
	if got := evalStr(t, testDoc, `//book[3] >> //book[1]`); got != "true" {
		t.Errorf("document order >> = %q, want true", got)
	}
}

func TestSequenceOperators(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`(1, 2, 3)`, "1,2,3"},
		{`(1 to 5)`, "1,2,3,4,5"},
		{`(5 to 1)`, ""}, // a descending range is empty, not reversed
		{`()`, ""},
		{`(//book[1] | //book[3])/@id`, "b1,b3"},
		{`(//book union //book[1])/@id`, "b1,b2,b3"}, // dedup
		{`(//book intersect //book[@price > 20])/@id`, "b1,b3"},
		{`(//book except //book[1])/@id`, "b2,b3"},
		{`reverse((1,2,3))`, "3,2,1"},
		{`subsequence((1,2,3,4,5), 2, 2)`, "2,3"},
		{`distinct-values((1, 1.0, 2, 'a', 'a'))`, "1,2,a"},
		{`insert-before((1,2,3), 2, 99)`, "1,99,2,3"},
		{`remove((1,2,3), 2)`, "1,3"},
		{`index-of((10,20,30,20), 20)`, "2,4"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestUnionRequiresNodes(t *testing.T) {
	err := evalErr(t, testDoc, `(1, 2) union (3)`)
	if !strings.Contains(err.Error(), "XPTY0004") {
		t.Errorf("union of atomics: error = %v, want XPTY0004", err)
	}
}

func TestStringFunctions(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`concat('a', 'b', 'c')`, "abc"},
		{`string-join(('a','b','c'), '-')`, "a-b-c"},
		{`string-length('hello')`, "5"},
		{`string-length('héllo')`, "5"}, // characters, not bytes
		{`substring('hello', 2)`, "ello"},
		{`substring('hello', 2, 3)`, "ell"},
		{`substring('hello', 0)`, "hello"},      // out-of-range start is clamped
		{`substring('hello', -5, 3)`, ""},       // range ends before position 1
		{`substring('hello', 1.5, 2.6)`, "ell"}, // rounded per the spec
		{`upper-case('abc')`, "ABC"},
		{`lower-case('ABC')`, "abc"},
		{`normalize-space('  a   b  ')`, "a b"},
		{`contains('hello', 'ell')`, "true"},
		{`contains('hello', '')`, "true"}, // every string contains ""
		{`starts-with('hello', 'he')`, "true"},
		{`ends-with('hello', 'lo')`, "true"},
		{`substring-before('a-b', '-')`, "a"},
		{`substring-after('a-b', '-')`, "b"},
		{`substring-before('abc', 'x')`, ""}, // no match gives ""
		{`translate('abc', 'ab', 'xy')`, "xyc"},
		{`translate('abc', 'ab', 'x')`, "xc"}, // unmatched chars are deleted
		{`string-to-codepoints('AB')`, "65,66"},
		{`codepoints-to-string((65, 66))`, "AB"},
		{`compare('a', 'b')`, "-1"},
		{`encode-for-uri('a b')`, "a%20b"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestAggregateFunctions(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`sum((1, 2, 3))`, "6"},
		{`sum(())`, "0"},    // the zero of the sum, not empty
		{`sum((), ())`, ""}, // explicit zero argument overrides
		{`sum(//book/@price)`, "85.49000000000001"},
		{`min((3, 1, 2))`, "1"},
		{`max((3, 1, 2))`, "3"},
		{`min(())`, ""}, // empty in, empty out
		{`avg((1, 2, 3))`, "2"},
		{`avg(())`, ""},
		{`count(//book)`, "3"},
		{`empty(())`, "true"},
		{`exists(//book)`, "true"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestNumericFunctions(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`abs(-5)`, "5"},
		{`abs(-5.5)`, "5.5"},
		{`ceiling(1.1)`, "2"},
		{`ceiling(-1.1)`, "-1"},
		{`floor(1.9)`, "1"},
		{`floor(-1.1)`, "-2"},
		{`round(1.5)`, "2"},
		{`round(-1.5)`, "-1"}, // ties toward positive infinity
		{`round(2.5)`, "3"},
		{`round-half-to-even(2.5)`, "2"}, // ties to even
		{`round-half-to-even(3.5)`, "4"},
		// fn:round is single-argument in XPath 2.0; the two-argument form is
		// XPath 3.0. fn:round-half-to-even does take a precision.
		{`round-half-to-even(1.2345, 2)`, "1.23"},
		{`number('42')`, "42"},
		{`number('abc')`, "NaN"}, // fn:number is lenient, unlike cast
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestBooleanAndEBV(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`true()`, "true"},
		{`not(false())`, "true"},
		{`boolean(//book)`, "true"},
		{`boolean(())`, "false"},
		{`boolean('')`, "false"},
		{`boolean('x')`, "true"},
		{`boolean(0)`, "false"},
		{`boolean(1)`, "true"},
		{`1 = 1 and 2 = 2`, "true"},
		{`1 = 2 or 2 = 2`, "true"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestEBVRejectsAtomicSequence(t *testing.T) {
	// XPath 2.0 tightened this: a multi-item atomic sequence has no EBV.
	err := evalErr(t, testDoc, `boolean((1, 2))`)
	if !strings.Contains(err.Error(), "FORG0006") {
		t.Errorf("EBV of (1,2): error = %v, want FORG0006", err)
	}
	// But a node sequence does, regardless of length.
	if got := evalStr(t, testDoc, `boolean(//book)`); got != "true" {
		t.Errorf("EBV of a node sequence = %q, want true", got)
	}
}

func TestShortCircuit(t *testing.T) {
	// The right operand must not be evaluated when the left settles it;
	// otherwise this raises a division-by-zero error.
	if got := evalStr(t, testDoc, `false() and (1 div 0) = 1`); got != "false" {
		t.Errorf("and did not short-circuit: %q", got)
	}
	if got := evalStr(t, testDoc, `true() or (1 div 0) = 1`); got != "true" {
		t.Errorf("or did not short-circuit: %q", got)
	}
}

func TestConditionalAndLoops(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`if (1 = 1) then 'y' else 'n'`, "y"},
		{`if (1 = 2) then 'y' else 'n'`, "n"},
		{`for $x in (1,2,3) return $x * 2`, "2,4,6"},
		{`for $x in (1,2), $y in (10,20) return $x * $y`, "10,20,20,40"},
		{`some $x in (1,2,3) satisfies $x > 2`, "true"},
		{`some $x in (1,2,3) satisfies $x > 5`, "false"},
		{`every $x in (1,2,3) satisfies $x > 0`, "true"},
		{`every $x in (1,2,3) satisfies $x > 2`, "false"},
		{`every $x in () satisfies false()`, "true"}, // vacuously true
		{`some $x in () satisfies true()`, "false"},
		{`for $b in //book return $b/@id`, "b1,b2,b3"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestTypeExpressions(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`1 instance of xs:integer`, "true"},
		{`1 instance of xs:decimal`, "true"}, // integer is a subtype
		{`1.5 instance of xs:integer`, "false"},
		{`'a' instance of xs:string`, "true"},
		{`() instance of empty-sequence()`, "true"},
		{`(1,2) instance of xs:integer*`, "true"},
		{`(1,2) instance of xs:integer`, "false"}, // cardinality mismatch
		{`() instance of xs:integer?`, "true"},
		{`//book[1] instance of element()`, "true"},
		{`'42' cast as xs:integer`, "42"},
		{`42 cast as xs:string`, "42"},
		{`'42' castable as xs:integer`, "true"},
		{`'abc' castable as xs:integer`, "false"},
		{`'1.5' castable as xs:integer`, "false"},
		{`xs:integer('42')`, "42"},
		{`xs:double('1.5')`, "1.5"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestCastIsStrict(t *testing.T) {
	// Unlike fn:number, a cast must fail rather than yield NaN.
	err := evalErr(t, testDoc, `'abc' cast as xs:double`)
	if !strings.Contains(err.Error(), "FORG0001") {
		t.Errorf("cast of 'abc': error = %v, want FORG0001", err)
	}
	// A prefix that parses is not enough; the whole string must be numeric.
	if err := evalErr(t, testDoc, `'12abc' cast as xs:double`); err == nil {
		t.Error("'12abc' cast to double should fail")
	}
}

func TestRegexFunctions(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`matches('abc123', '^[a-z]+[0-9]+$')`, "true"},
		{`matches('ABC', '^[a-z]+$')`, "false"},
		{`matches('ABC', '^[a-z]+$', 'i')`, "true"},
		{`replace('a1b2', '[0-9]', 'X')`, "aXbX"},
		{`replace('john smith', '(\w+) (\w+)', '$2 $1')`, "smith john"},
		{`tokenize('a,b,c', ',')`, "a,b,c"},
		{`tokenize('a1b22c', '[0-9]+')`, "a,b,c"},
		{`matches('x', '\i')`, "true"}, // XML name start character
		{`matches('1', '\i')`, "false"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestRegexRejectsEmptyMatchPattern(t *testing.T) {
	// A pattern matching "" makes replace/tokenize ill-defined.
	if err := evalErr(t, testDoc, `replace('abc', 'x*', 'Y')`); !strings.Contains(err.Error(), "FORX0003") {
		t.Errorf("error = %v, want FORX0003", err)
	}
	if err := evalErr(t, testDoc, `tokenize('abc', 'x*')`); !strings.Contains(err.Error(), "FORX0003") {
		t.Errorf("error = %v, want FORX0003", err)
	}
}

func TestNodeFunctions(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`name(//m:note)`, "m:note"},
		{`local-name(//m:note)`, "note"},
		{`namespace-uri(//m:note)`, "urn:meta"},
		{`local-name(//book[1])`, "book"},
		{`namespace-uri(//book[1])`, ""},
		{`name(/catalog/@count)`, "count"},
		{`count(//comment())`, "1"},
		{`count(//text())`, "13"},
		{`local-name(root(//m:note)/*)`, "catalog"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestDateFunctions(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`xs:date('2024-01-15')`, "2024-01-15"},
		{`year-from-date(xs:date('2024-01-15'))`, "2024"},
		{`month-from-date(xs:date('2024-01-15'))`, "1"},
		{`day-from-date(xs:date('2024-01-15'))`, "15"},
		{`xs:date('2024-01-15') < xs:date('2024-06-01')`, "true"},
		{`xs:date('2024-03-01') - xs:date('2024-02-01')`, "P29D"}, // leap year
		{`xs:date('2024-01-31') + xs:yearMonthDuration('P1M')`, "2024-02-29"},
		{`xs:dayTimeDuration('PT1H') + xs:dayTimeDuration('PT30M')`, "PT1H30M"},
		{`xs:yearMonthDuration('P1Y') * 2`, "P2Y"},
		{`hours-from-dateTime(xs:dateTime('2024-01-15T13:45:00'))`, "13"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestMonthAdditionClampsToEndOfMonth(t *testing.T) {
	// Jan 31 + 1 month is Feb 29 in a leap year and Feb 28 otherwise; it is
	// never Mar 2.
	if got := evalStr(t, testDoc, `xs:date('2023-01-31') + xs:yearMonthDuration('P1M')`); got != "2023-02-28" {
		t.Errorf("2023-01-31 + P1M = %q, want 2023-02-28", got)
	}
	if got := evalStr(t, testDoc, `xs:date('2024-01-31') + xs:yearMonthDuration('P1M')`); got != "2024-02-29" {
		t.Errorf("2024-01-31 + P1M = %q, want 2024-02-29", got)
	}
}

func TestPositionAndLast(t *testing.T) {
	if got := evalStr(t, testDoc, `//book/position()`); got != "1,2,3" {
		t.Errorf("position() over books = %q, want 1,2,3", got)
	}
	if got := evalStr(t, testDoc, `//book[position() = 2]/@id`); got != "b2" {
		t.Errorf("position()=2 = %q, want b2", got)
	}
	if got := evalStr(t, testDoc, `//book[position() != 2]/@id`); got != "b1,b3" {
		t.Errorf("position()!=2 = %q, want b1,b3", got)
	}
	if got := evalStr(t, testDoc, `//book[last()]/@id`); got != "b3" {
		t.Errorf("last() = %q, want b3", got)
	}
}

func TestDocumentOrderAndDedup(t *testing.T) {
	// A path must return document order with duplicates removed even when the
	// steps would produce neither.
	if got := evalStr(t, testDoc, `//title/../@id`); got != "b1,b2,b3" {
		t.Errorf("//title/../@id = %q, want b1,b2,b3", got)
	}
	// Every title's parent's title: three context nodes, same three results,
	// deduplicated.
	if got := evalStr(t, testDoc, `//title/parent::book/title`); got != "Go,XML,XSLT" {
		t.Errorf("= %q, want Go,XML,XSLT", got)
	}
}

func TestVariableScoping(t *testing.T) {
	// Each iteration must bind its own value; a shared mutable binding would
	// make every result the last value.
	if got := evalStr(t, testDoc, `for $x in (1,2,3) return $x`); got != "1,2,3" {
		t.Errorf("for-binding leaked between iterations: %q", got)
	}
	// An inner binding shadows an outer one.
	if got := evalStr(t, testDoc, `for $x in (1) return (for $x in (2) return $x)`); got != "2" {
		t.Errorf("inner binding did not shadow: %q", got)
	}
}

func TestUnboundVariableAndFunction(t *testing.T) {
	if err := evalErr(t, testDoc, `$nope`); !strings.Contains(err.Error(), "XPST0008") {
		t.Errorf("unbound variable: error = %v, want XPST0008", err)
	}
	if err := evalErr(t, testDoc, `no-such-function()`); !strings.Contains(err.Error(), "XPST0017") {
		t.Errorf("unknown function: error = %v, want XPST0017", err)
	}
}

func TestDefaultElementNamespace(t *testing.T) {
	// With a default element namespace, an unprefixed name test matches names
	// in that namespace...
	doc := `<a xmlns="urn:d"><b at="v"/></a>`
	root := mustParse(t, doc)
	ctx := NewContext(root, Builtins())

	seq, err := Eval(`/a/b`, ctx, testNS{defaultElem: "urn:d"})
	if err != nil {
		t.Fatal(err)
	}
	if len(seq) != 1 {
		t.Errorf("/a/b with default namespace matched %d nodes, want 1", len(seq))
	}
	// ...but never attribute names, which are in no namespace when unprefixed.
	seq, err = Eval(`/a/b/@at`, ctx, testNS{defaultElem: "urn:d"})
	if err != nil {
		t.Fatal(err)
	}
	if len(seq) != 1 {
		t.Errorf("@at with a default element namespace matched %d, want 1 "+
			"(the default namespace must not apply to attributes)", len(seq))
	}
}

func TestDocIsDisabledByDefault(t *testing.T) {
	// Failing closed matters: a stylesheet that can open arbitrary URIs is an
	// SSRF and file-disclosure vector.
	err := evalErr(t, testDoc, `doc('file:///etc/passwd')`)
	if !strings.Contains(err.Error(), "FODC0002") {
		t.Errorf("doc() error = %v, want FODC0002", err)
	}
}

func TestKindTests(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`count(//book/node())`, "8"},
		{`count(//book/text())`, "1"},
		{`count(/catalog/comment())`, "1"},
		{`count(//book/element())`, "7"},
		{`count(//book[1]/attribute())`, "2"},
		{`count(//processing-instruction())`, "0"},
		{`local-name((//element(title))[1])`, "title"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestExpressionRoundTrip(t *testing.T) {
	// String() should render something that parses back to the same thing,
	// which is what makes AST dumps useful when debugging a stylesheet.
	for _, src := range []string{
		`/catalog/book`,
		`//book[@id = 'b1']`,
		`count(//book)`,
		`for $x in (1, 2) return $x`,
		`if (1 = 1) then 'y' else 'n'`,
	} {
		e, err := Parse(src, testNS{})
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		if _, err := Parse(e.String(), testNS{}); err != nil {
			t.Errorf("re-parsing %q (from %q) failed: %v", e.String(), src, err)
		}
	}
}
