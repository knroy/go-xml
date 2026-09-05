package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// eval31 evaluates expr under XPath 3.1, which is what the JSON functions
// need, and returns the single result item's string value.
func eval31(t *testing.T, expr string, root *xdm.Node) (string, error) {
	t.Helper()
	ctx := NewContext(root, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		return "", err
	}
	if len(seq) != 1 {
		return "", nil
	}
	return seq[0].(interface{ String() string }).String(), nil
}

// TestNumericCastOverflowIsInfinityNotALexicalError is the load-bearing test
// in this file.
//
// "1e400" is a well-formed xs:double lexical form -- isSchemaFloatLexical
// accepts it, and it must, because the grammar has no upper bound on the
// exponent. What it names is a magnitude no double can hold. F&O 3.0 §18.3:
// "If the value is too large or too small to be accurately represented by the
// implementation, it is handled as an overflow or underflow as defined in
// §4.2", and §4.2 permits exactly three behaviours for an xs:double or
// xs:float overflow -- raise FOAR0002, return INF or -INF, or return the
// largest finite value. FORG0001 "invalid lexical value" is none of the
// three, and says something factually untrue about the text.
//
// This processor returns ±INF, and had already made that choice twice
// elsewhere: the lexer returns INF for the literal 1e400 (with the reasoning
// written out at lexer.go), and 1e308 * 10 returns INF rather than raising
// FOAR0002. Casting the identical text raised FORG0001, so the engine
// disagreed with itself about what "1e400" denotes depending only on whether
// it arrived as a literal or as a string.
//
// The assertions are on the VALUE, not on the absence of an error. The
// failure mode this replaces was not only the error: fn:number, which turns a
// failed cast into NaN by design, answered NaN for a value it could perfectly
// well convert -- a wrong number with nothing to signal it.
func TestNumericCastOverflowIsInfinityNotALexicalError(t *testing.T) {
	for _, c := range []struct{ expr, want string }{
		// The literal path, which was already right, pinned so the two
		// cannot drift apart again.
		{`string(1e400)`, "INF"},
		{`string(-1e400)`, "-INF"},

		// The cast path, which was raising FORG0001.
		{`string(xs:double("1e400"))`, "INF"},
		{`string(xs:double("-1e400"))`, "-INF"},
		{`string(xs:double("+1e400"))`, "INF"},
		{`string(xs:double("1e999999999"))`, "INF"},

		// xs:float overflows two ways. "1e39" is representable as a double
		// and overflows only on the narrowing to float, which already
		// worked; "1e400" overflows at the double stage, which did not. Both
		// must give INF, or the type would answer differently for two values
		// that are equally out of its range.
		{`string(xs:float("1e39"))`, "INF"},
		{`string(xs:float("1e400"))`, "INF"},
		{`string(xs:float("-1e400"))`, "-INF"},

		// fn:number converts rather than erroring, so a cast that wrongly
		// failed came back as NaN with no diagnostic at all.
		{`string(number("1e400"))`, "INF"},
		{`string(number("-1e400"))`, "-INF"},

		// "castable as" asks whether the cast would succeed, and the answer
		// was false for text the grammar admits.
		{`string("1e400" castable as xs:double)`, "true"},
		{`string("1e400" castable as xs:float)`, "true"},

		// Underflow was never broken. It is asserted here because the fix
		// discards strconv.ErrRange in both directions, and ErrRange is what
		// an underflow reports too: ParseFloat returns ±0 with it, which is
		// the "0.0E0" §4.2 permits.
		{`string(xs:double("1e-400"))`, "0"},
		{`string(xs:double("-1e-400"))`, "-0"},
		{`string(xs:float("1e-400"))`, "0"},

		// A malformed lexical form is still FORG0001, which is the whole
		// point of telling the two apart. These must not be swept up by the
		// range branch.
		{`string("abc" castable as xs:double)`, "false"},
		{`string("1e400x" castable as xs:double)`, "false"},
		{`string("" castable as xs:double)`, "false"},
		{`string("1e" castable as xs:double)`, "false"},
		// Spellings Go's parser accepts and XML Schema does not. These are
		// rejected by isSchemaFloatLexical before ParseFloat is reached, and
		// swapping Sscanf for ParseFloat must not have opened them up:
		// ParseFloat accepts every one of them.
		{`string("Inf" castable as xs:double)`, "false"},
		{`string("Infinity" castable as xs:double)`, "false"},
		{`string("+Inf" castable as xs:double)`, "false"},
		{`string("nan" castable as xs:double)`, "false"},
		{`string("0x10" castable as xs:double)`, "false"},
		{`string("1_0" castable as xs:double)`, "false"},

		// The three XML Schema special values still parse, and only in these
		// exact spellings.
		{`string(xs:double("INF"))`, "INF"},
		{`string(xs:double("-INF"))`, "-INF"},
		{`string(xs:double("NaN"))`, "NaN"},

		// Ordinary values, so the fix cannot be a blanket "never fail".
		{`string(xs:double("1.5e3"))`, "1500"},
		{`string(xs:float("2.5"))`, "2.5"},
	} {
		got, err := eval31(t, c.expr, nil)
		if err != nil {
			t.Errorf("%s: unexpected error %v (want %q)", c.expr, err, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestNumericCastOverflowFromDocumentData reaches the same defect the way a
// real document does, which is the reason it matters.
//
// A cast written by hand is a stylesheet author's own business; an element
// holding "1e400" is data arriving from outside, and every numeric operation
// over it went through the same broken path. fn:number answered NaN, so a
// comparison quietly became false, and fn:sum, fn:avg and fn:max -- which
// cast rather than convert -- raised FORG0001 on a document that is perfectly
// well-formed.
func TestNumericCastOverflowFromDocumentData(t *testing.T) {
	d, err := xdm.ParseString(`<r><v>1e400</v><v>2</v></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ expr, want string }{
		{`string(number(/r/v[1]))`, "INF"},
		{`string(/r/v[1] cast as xs:double)`, "INF"},
		// The silent one: NaN > 0 is false, so a document holding an
		// enormous number compared as "not greater than zero".
		{`string(/r/v[1] > 0)`, "true"},
		{`string(sum(/r/v))`, "INF"},
		{`string(avg(/r/v))`, "INF"},
		{`string(max(/r/v))`, "INF"},
		{`string(min(/r/v))`, "2"},
	} {
		got, err := eval31(t, c.expr, d.Root)
		if err != nil {
			t.Errorf("%s: unexpected error %v (want %q)", c.expr, err, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestParseJSONNumberOverflowIsInfinity covers the second, independent copy of
// the same mistake, in the JSON scanner's number handler.
//
// The scanner has already checked the lexeme against JSON's number production
// by the time it is parsed, so the only failure strconv.ParseFloat has left to
// report is a range error -- and on that it still hands back ±Inf or ±0.
// Treating it as "not a valid JSON number" rejected input JSON permits, and
// put fn:parse-json at odds with fn:json-to-xml, which carries the same
// lexeme through untouched.
func TestParseJSONNumberOverflowIsInfinity(t *testing.T) {
	for _, c := range []struct{ expr, want string }{
		{`string(parse-json('1e400'))`, "INF"},
		{`string(parse-json('-1e400'))`, "-INF"},
		{`string(parse-json('1e-400'))`, "0"},
		{`string(parse-json('[1e400]')(1))`, "INF"},
		{`string(parse-json('{"a":1e400}')?a)`, "INF"},
	} {
		got, err := eval31(t, c.expr, nil)
		if err != nil {
			t.Errorf("%s: unexpected error %v (want %q)", c.expr, err, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}

	// The grammar is still enforced. A range error is the only thing the new
	// branch forgives; a malformed number is FOJS0001 as before.
	for _, expr := range []string{
		`parse-json('1e')`,
		`parse-json('01')`,
		`parse-json('1.')`,
		`parse-json('+1')`,
	} {
		if _, err := eval31(t, expr, nil); err == nil {
			t.Errorf("%s: want a syntax error, got none", expr)
		} else if !strings.Contains(err.Error(), "FOJS0001") {
			t.Errorf("%s: want FOJS0001, got %v", expr, err)
		}
	}

	// fn:json-to-xml keeps the lexeme verbatim, which is the behaviour
	// fn:parse-json now agrees with rather than contradicts.
	got, err := eval31(t, `string(json-to-xml('1e400'))`, nil)
	if err != nil {
		t.Fatalf("json-to-xml('1e400'): %v", err)
	}
	if got != "1e400" {
		t.Errorf("json-to-xml('1e400') string value = %q, want %q", got, "1e400")
	}
}
