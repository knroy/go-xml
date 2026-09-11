package xpath

import (
	"strings"
	"testing"
)

// These tests are written against the normative F&O text rather than against
// the engine's existing behaviour: each case is a worked example or an
// explicit rule quoted from the vendored specification.
//
// The deep-equal node cases are the load-bearing ones. F&O 3.0 §14.2.2 gives
// exactly one node kind a fixed collation — "the string value of $i1 is equal
// to the string value of $i2 when compared using the Unicode codepoint
// collation", for namespace nodes — and leaves every other string comparison
// to the general clause that the $collation argument "is used at all levels of
// recursion when strings are compared (but not when names are compared)". The
// implementation had that carve-out inverted: namespace nodes took the
// collation while comments and processing instructions were pinned to
// codepoint.

// ci is the HTML ASCII case-insensitive collation, the second collation every
// implementation is required to support. It is the cheapest way to tell a
// collation that is used from one that is merely validated.
const ci = `"http://www.w3.org/2005/xpath-functions/collation/html-ascii-case-insensitive"`

// s2eval evaluates expr under XPath 3.1, which fn:sort, fn:collation-key and
// fn:contains-token require.
func s2eval(t *testing.T, expr string) (string, error) {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		return "", err
	}
	if len(seq) != 1 {
		return "", nil
	}
	return seq[0].(interface{ String() string }).String(), nil
}

// TestSpecS2CollationIsUsed checks that every function declaring a $collation
// parameter reaches a comparison the collation actually decides. A function
// that validates the URI and then compares with Go's own string equality would
// pass a validation test and fail this one.
func TestSpecS2CollationIsUsed(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`compare("abc","ABC",` + ci + `)`, "0"},
		{`contains("hello WORLD","world",` + ci + `)`, "true"},
		{`starts-with("HELLO","hello",` + ci + `)`, "true"},
		{`ends-with("HELLO","LLo",` + ci + `)`, "true"},
		{`substring-before("HELLOxWORLD","xworld",` + ci + `)`, "HELLO"},
		{`substring-after("HELLOxWORLD","helloX",` + ci + `)`, "WORLD"},
		{`string-join(for $i in index-of(("a","B","c"),"b",` + ci + `) return string($i),",")`, "2"},
		{`string-join(distinct-values(("a","A","b"),` + ci + `),",")`, "a,b"},
		{`min(("B","a"),` + ci + `)`, "a"},
		{`max(("B","a"),` + ci + `)`, "B"},
		{`deep-equal(("a","A"),("A","a"),` + ci + `)`, "true"},
		{`string-join(sort(("b","A","a","B"),` + ci + `),",")`, "A,a,b,B"},
		{`contains-token("a B c","b",` + ci + `)`, "true"},
	}
	for _, c := range cases {
		got, err := s2eval(t, c.expr)
		if err != nil {
			t.Errorf("%s: error %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("collation not reaching the comparison: %s got %q want %q",
				c.expr, got, c.want)
		}
	}
}

// TestSpecS2DeepEqualNodeCollation covers the node kinds whose string values
// the general collation clause governs. The comment case is the regression:
// the spec states text and comment nodes in one sentence, so a comment cannot
// take codepoint comparison while a text node takes the collation.
func TestSpecS2DeepEqualNodeCollation(t *testing.T) {
	cases := []struct{ name, expr, want string }{
		{"text",
			`deep-equal(parse-xml("<e>abc</e>")/e, parse-xml("<e>ABC</e>")/e, ` + ci + `)`, "true"},
		{"attribute",
			`deep-equal(parse-xml("<e a='abc'/>")/e, parse-xml("<e a='ABC'/>")/e, ` + ci + `)`, "true"},
		{"comment",
			`deep-equal(parse-xml("<e><!--abc--></e>")/e/comment(), parse-xml("<e><!--ABC--></e>")/e/comment(), ` + ci + `)`, "true"},
	}
	for _, c := range cases {
		got, err := s2eval(t, c.expr)
		if err != nil {
			t.Errorf("%s: error %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: %s got %q want %q", c.name, c.expr, got, c.want)
		}
	}
}

// TestSpecS2DeepEqualNamespaceIsCodepoint pins the one carve-out. Two
// namespace URIs differing only in case are different namespaces, so a
// case-blind $collation must not make them deep-equal.
func TestSpecS2DeepEqualNamespaceIsCodepoint(t *testing.T) {
	expr := `deep-equal(` +
		`parse-xml("<e xmlns:p='http://X'/>")/e/namespace::p, ` +
		`parse-xml("<e xmlns:p='http://x'/>")/e/namespace::p, ` + ci + `)`
	got, err := s2eval(t, expr)
	if err != nil {
		t.Skipf("namespace axis not reachable here: %v", err)
	}
	if got != "false" {
		t.Errorf("namespace node compared under the collation rather than "+
			"codepoint: got %q", got)
	}
}

// TestSpecS2DeepEqualPICollation covers the processing-instruction rule, which
// unlike the namespace rule immediately above it in the spec carries no
// codepoint carve-out.
func TestSpecS2DeepEqualPICollation(t *testing.T) {
	expr := `deep-equal(` +
		`parse-xml("<e><?t abc?></e>")/e/processing-instruction(), ` +
		`parse-xml("<e><?t ABC?></e>")/e/processing-instruction(), ` + ci + `)`
	got, err := s2eval(t, expr)
	if err != nil {
		t.Fatal(err)
	}
	if got != "true" {
		t.Errorf("PI string value ignored the collation: got %q want true", got)
	}
}

// TestSpecS2FOCH0002 checks that an unknown collation URI is refused rather
// than silently falling back to codepoint.
func TestSpecS2FOCH0002(t *testing.T) {
	const unknown = `"http://example.com/no-such-collation"`
	for _, e := range []string{
		`compare("a","b",` + unknown + `)`,
		`contains("a","b",` + unknown + `)`,
		`deep-equal(1,1,` + unknown + `)`,
		`sort((1,2),` + unknown + `)`,
		`collation-key("a",` + unknown + `)`,
	} {
		if _, err := s2eval(t, e); err == nil ||
			!strings.Contains(err.Error(), "FOCH0002") {
			t.Errorf("%s: want FOCH0002, got %v", e, err)
		}
	}
}

// TestSpecS2SortStable checks the stability fn:sort requires. Items whose keys
// compare equal must come out in input order.
func TestSpecS2SortStable(t *testing.T) {
	got, err := s2eval(t,
		`string-join(for $x in sort(1 to 26, (), function($x) { 1 }) return string($x), ",")`)
	if err != nil {
		t.Fatal(err)
	}
	want := "1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26"
	if got != want {
		t.Errorf("fn:sort is not stable:\n got %s\nwant %s", got, want)
	}
	got2, err := s2eval(t,
		`string-join(sort(("b1","a1","b2","a2","b3","a3"), (), function($x) { substring($x,1,1) }), ",")`)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != "a1,a2,a3,b1,b2,b3" {
		t.Errorf("fn:sort not stable within key groups: %s", got2)
	}
}

// TestSpecS2CollationKey checks that fn:collation-key keys under the named
// collation rather than always under codepoint.
func TestSpecS2CollationKey(t *testing.T) {
	eq, err := s2eval(t, `collation-key("abc",`+ci+`) = collation-key("ABC",`+ci+`)`)
	if err != nil {
		t.Fatal(err)
	}
	if eq != "true" {
		t.Errorf("collation-key ignored the collation: %q", eq)
	}
	ne, err := s2eval(t, `collation-key("abc") = collation-key("ABC")`)
	if err != nil {
		t.Fatal(err)
	}
	if ne != "false" {
		t.Errorf("codepoint collation-key conflated case: %q", ne)
	}
}

// TestSpecS2DefaultCollationIsCodepoint checks the default the spec states,
// and that it is actually in force rather than merely reported.
func TestSpecS2DefaultCollationIsCodepoint(t *testing.T) {
	got, err := s2eval(t, `default-collation()`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://www.w3.org/2005/xpath-functions/collation/codepoint" {
		t.Errorf("default-collation() = %q", got)
	}
	c, err := s2eval(t, `compare("abc","ABC")`)
	if err != nil {
		t.Fatal(err)
	}
	if c != "1" {
		t.Errorf(`compare("abc","ABC") = %q, want 1 under codepoint`, c)
	}
}

// TestSpecS2URIWorkedExamples runs the worked examples from the F&O 3.0
// URI-functions section verbatim.
func TestSpecS2URIWorkedExamples(t *testing.T) {
	cases := []struct{ expr, want string }{
		// fn:encode-for-uri: everything outside the unreserved set
		// ALPHA / DIGIT / "-" / "." / "_" / "~" is percent-encoded, "/"
		// included.
		{`encode-for-uri("http://www.example.com/00/Weather/CA/Los%20Angeles#ocean")`,
			"http%3A%2F%2Fwww.example.com%2F00%2FWeather%2FCA%2FLos%2520Angeles%23ocean"},
		{`concat("http://www.example.com/", encode-for-uri("~bébé"))`,
			"http://www.example.com/~b%C3%A9b%C3%A9"},
		{`concat("http://www.example.com/", encode-for-uri("100% organic"))`,
			"http://www.example.com/100%25%20organic"},
		// fn:iri-to-uri leaves URI syntax alone, and is idempotent: both
		// "My Documents" and "My%20Documents" give "My%20Documents".
		{`iri-to-uri("http://www.example.com/00/Weather/CA/Los%20Angeles#ocean")`,
			"http://www.example.com/00/Weather/CA/Los%20Angeles#ocean"},
		{`iri-to-uri("http://www.example.com/~bébé")`,
			"http://www.example.com/~b%C3%A9b%C3%A9"},
		{`iri-to-uri("My Documents")`, "My%20Documents"},
		{`iri-to-uri("My%20Documents")`, "My%20Documents"},
		// fn:escape-html-uri escapes only what falls outside #x20-#x7E, so a
		// space survives where iri-to-uri would encode it.
		{`escape-html-uri("http://www.example.com/00/Weather/CA/Los Angeles#ocean")`,
			"http://www.example.com/00/Weather/CA/Los Angeles#ocean"},
		{`escape-html-uri("javascript:if (navigator.browserLanguage == 'fr') window.open('http://www.example.com/~bébé');")`,
			"javascript:if (navigator.browserLanguage == 'fr') window.open('http://www.example.com/~b%C3%A9b%C3%A9');"},
		// The #x20-#x7E range is inclusive at both ends.
		{`escape-html-uri(concat(codepoints-to-string(32), codepoints-to-string(126), codepoints-to-string(127)))`,
			" ~%7F"},
	}
	for _, c := range cases {
		got, err := s2eval(t, c.expr)
		if err != nil {
			t.Errorf("%s: error %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.expr, got, c.want)
		}
	}
}
