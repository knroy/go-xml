package xpath

import (
	"strings"
	"testing"
)

// specEval30 evaluates expr under XPath 3.0, where the "q" flag exists.
func specEval30(t *testing.T, expr string) ([]string, error) {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath30
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seq))
	for _, it := range seq {
		out = append(out, it.(interface{ String() string }).String())
	}
	return out, nil
}

// specEval30One is specEval30 for the single-item case.
func specEval30One(t *testing.T, expr string) (string, error) {
	t.Helper()
	got, err := specEval30(t, expr)
	if err != nil {
		return "", err
	}
	if len(got) != 1 {
		return "", nil
	}
	return got[0], nil
}

// TestSpecWorkedExamplesRegex runs the worked examples given verbatim in the
// F&O 3.0 text for the regex functions and their flags.
func TestSpecWorkedExamplesRegex(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		// 5.6.1, the "x" flag (rec30 ~10705).
		{`matches("helloworld", "hello world", "x")`, "true"},
		{`matches("helloworld", "hello[ ]world", "x")`, "false"},
		{`matches("hello world", "hello\ sworld", "x")`, "true"},
		{`matches("hello world", "hello world", "x")`, "false"},
		// 5.6.1, the "q" flag (rec30 ~10711).
		{`replace("a\b\c", "\", "\\", "q")`, `a\\b\\c`},
		{`replace("a/b/c", "/", "$", "q")`, "a$b$c"},
		{`matches("abcd", ".*", "q")`, "false"},
		{`matches("Mr. B. Obama", "B. OBAMA", "iq")`, "true"},
		// 5.6.3 fn:replace (rec30 ~11019).
		{`replace("abracadabra", "bra", "*")`, "a*cada*"},
		{`replace("abcd", "(ab)|(a)", "[1=$1][2=$2]")`, "[1=ab][2=]cd"},
	}
	for _, c := range cases {
		got, err := specEval30One(t, c.expr)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestSpecWorkedExamplesTokenize runs fn:tokenize's worked examples, which are
// sequence-valued (rec30 ~11175-11214).
func TestSpecWorkedExamplesTokenize(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{`tokenize("12.3.5.6", ".", "q")`, []string{"12", "3", "5", "6"}},
		{`tokenize("abracadabra", "(ab)|(a)")`, []string{"", "r", "c", "d", "r", ""}},
		{`tokenize("The cat sat on the mat", "\s+")`, []string{"The", "cat", "sat", "on", "the", "mat"}},
		{`tokenize("1, 15, 24, 50", ",\s*")`, []string{"1", "15", "24", "50"}},
		{`tokenize("1,15,,24,50,", ",")`, []string{"1", "15", "", "24", "50", ""}},
		{`tokenize("Some unparsed <br> HTML <BR> text", "\s*<br>\s*", "i")`,
			[]string{"Some unparsed", "HTML", "text"}},
	}
	for _, c := range cases {
		got, err := specEval30(t, c.expr)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.expr, err)
			continue
		}
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestSpecRegexErrorConditions pins the error codes the spec names for the
// regex functions (rec30 error-conditions blocks, ~11035, ~11190).
func TestSpecRegexErrorConditions(t *testing.T) {
	cases := []struct {
		expr string
		code string
	}{
		{`tokenize("abba", ".?")`, "FORX0003"},
		{`replace("abba", ".?", "x")`, "FORX0003"},
		{`matches("abc", "a", "w")`, "FORX0001"},
		{`replace("abc", "a", "$x")`, "FORX0004"},
		{`replace("abc", "a", "\q")`, "FORX0004"},
		{`replace("abc", "a", "\")`, "FORX0004"},
	}
	for _, c := range cases {
		_, err := specEval30One(t, c.expr)
		if err == nil {
			t.Errorf("%s: expected %s, got no error", c.expr, c.code)
			continue
		}
		if !strings.Contains(err.Error(), c.code) {
			t.Errorf("%s: expected %s, got %v", c.expr, c.code, err)
		}
	}
}

// TestQFlagSuppressesOtherFlags checks the spec's rule that when "q" is used
// together with "m", "s" or "x", that flag has no effect (rec30 ~10711).
//
// The flag letters are read in the order written, so both orderings have to
// agree: "xq" must behave exactly as "qx" does.
func TestQFlagSuppressesOtherFlags(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		// The pattern is the two characters "a b". Under q that is a literal,
		// so the space must survive whichever order the flags are written in.
		{`matches("a b", "a b", "qx")`, "true"},
		{`matches("a b", "a b", "xq")`, "true"},
		{`matches("ab", "a b", "qx")`, "false"},
		{`matches("ab", "a b", "xq")`, "false"},
	}
	for _, c := range cases {
		got, err := specEval30One(t, c.expr)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestXFlagLeavesCharacterClassesAlone pins the spec's rule that the "x" flag
// removes whitespace "other than whitespace within a character class
// expression" (rec30 ~10700).
func TestXFlagLeavesCharacterClassesAlone(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		// A space inside [] is a member of the class and must be kept.
		{`matches("hello world", "hello[ ]world", "x")`, "true"},
		// A "]" written first in a class is a literal member in XML Schema's
		// grammar only when escaped, so use an escaped one: the class is
		// [\] ] — a right bracket or a space.
		{`matches("a b", "a[\] ]b", "x")`, "true"},
	}
	for _, c := range cases {
		got, err := specEval30One(t, c.expr)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// The spec serializes the result with a default namespace and this engine uses
// an "fn:" prefix. Both denote the same element names in the same namespace,
// so the expectations below are the spec's tree in this engine's prefix form.
//
// TestSpecAnalyzeStringExamples runs the three worked examples the spec gives
// for fn:analyze-string (rec30 ~11460-11502), plus a nested capturing group,
// which the schema in the spec allows fn:group to contain.
func TestSpecAnalyzeStringExamples(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		{`serialize(analyze-string("The cat sat on the mat.", "\w+"))`,
			`<fn:analyze-string-result xmlns:fn="http://www.w3.org/2005/xpath-functions">` +
				`<fn:match>The</fn:match><fn:non-match> </fn:non-match>` +
				`<fn:match>cat</fn:match><fn:non-match> </fn:non-match>` +
				`<fn:match>sat</fn:match><fn:non-match> </fn:non-match>` +
				`<fn:match>on</fn:match><fn:non-match> </fn:non-match>` +
				`<fn:match>the</fn:match><fn:non-match> </fn:non-match>` +
				`<fn:match>mat</fn:match><fn:non-match>.</fn:non-match>` +
				`</fn:analyze-string-result>`},
		{`serialize(analyze-string("2008-12-03", "^(\d+)\-(\d+)\-(\d+)$"))`,
			`<fn:analyze-string-result xmlns:fn="http://www.w3.org/2005/xpath-functions">` +
				`<fn:match><fn:group nr="1">2008</fn:group>-<fn:group nr="2">12</fn:group>-` +
				`<fn:group nr="3">03</fn:group></fn:match>` +
				`</fn:analyze-string-result>`},
		{`serialize(analyze-string("A1,C15,,D24, X50,", "([A-Z])([0-9]+)"))`,
			`<fn:analyze-string-result xmlns:fn="http://www.w3.org/2005/xpath-functions">` +
				`<fn:match><fn:group nr="1">A</fn:group><fn:group nr="2">1</fn:group></fn:match>` +
				`<fn:non-match>,</fn:non-match>` +
				`<fn:match><fn:group nr="1">C</fn:group><fn:group nr="2">15</fn:group></fn:match>` +
				`<fn:non-match>,,</fn:non-match>` +
				`<fn:match><fn:group nr="1">D</fn:group><fn:group nr="2">24</fn:group></fn:match>` +
				`<fn:non-match>, </fn:non-match>` +
				`<fn:match><fn:group nr="1">X</fn:group><fn:group nr="2">50</fn:group></fn:match>` +
				`<fn:non-match>,</fn:non-match>` +
				`</fn:analyze-string-result>`},
		// A group nested inside another must nest in the result tree too.
		{`serialize(analyze-string("ab", "(a(b))"))`,
			`<fn:analyze-string-result xmlns:fn="http://www.w3.org/2005/xpath-functions">` +
				`<fn:match><fn:group nr="1">a<fn:group nr="2">b</fn:group></fn:group></fn:match>` +
				`</fn:analyze-string-result>`},
	}
	for _, c := range cases {
		got, err := specEval30One(t, c.expr)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.expr, got, c.want)
		}
	}
}

// TestAnalyzeStringZeroLengthMatch pins FORX0003 for fn:analyze-string, which
// the spec requires just as it does for fn:replace and fn:tokenize.
func TestAnalyzeStringZeroLengthMatch(t *testing.T) {
	_, err := specEval30One(t, `analyze-string("abba", ".?")`)
	if err == nil || !strings.Contains(err.Error(), "FORX0003") {
		t.Errorf(`analyze-string("abba", ".?"): expected FORX0003, got %v`, err)
	}
}

// TestQFlagReachesAllFourFunctions traces the "q" flag to a use site in every
// function that accepts it, not just the ones with worked examples.
func TestQFlagReachesAllFourFunctions(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		{`matches("a.c", ".", "q")`, "true"},
		{`matches("abc", ".", "q")`, "false"},
		{`replace("a.c", ".", "-", "q")`, "a-c"},
		{`string-join(tokenize("a.b.c", ".", "q"), "|")`, "a|b|c"},
		{`serialize(analyze-string("a.c", ".", "q"))`,
			`<fn:analyze-string-result xmlns:fn="http://www.w3.org/2005/xpath-functions">` +
				`<fn:non-match>a</fn:non-match><fn:match>.</fn:match><fn:non-match>c</fn:non-match>` +
				`</fn:analyze-string-result>`},
	}
	for _, c := range cases {
		got, err := specEval30One(t, c.expr)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}
