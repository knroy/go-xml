package xpath

import (
	"strings"
	"testing"
)

func lex(t *testing.T, src string) []Token {
	t.Helper()
	toks, err := NewLexer(src).Tokens()
	if err != nil {
		t.Fatalf("lex(%q): %v", src, err)
	}
	return toks[:len(toks)-1] // drop EOF
}

func TestLexStarIsContextDependent(t *testing.T) {
	// This is the central lexical ambiguity of XPath: after an operand, '*'
	// multiplies; otherwise it is a wildcard name test.
	toks := lex(t, `2 * 3`)
	if toks[1].Kind != TokOp || toks[1].Val != "*" {
		t.Errorf("`2 * 3`: middle token = %v %q, want operator *", toks[1].Kind, toks[1].Val)
	}

	toks = lex(t, `a/*`)
	if toks[2].Kind != TokWildcard {
		t.Errorf("`a/*`: last token = %v %q, want wildcard", toks[2].Kind, toks[2].Val)
	}

	toks = lex(t, `*`)
	if toks[0].Kind != TokWildcard {
		t.Errorf("`*` alone should be a wildcard, got %v", toks[0].Kind)
	}
}

func TestLexKeywordsAreContextDependent(t *testing.T) {
	// `div` is an operator after an operand and an element name otherwise.
	toks := lex(t, `6 div 2`)
	if toks[1].Kind != TokOp || toks[1].Val != "div" {
		t.Errorf("`6 div 2`: div should lex as an operator, got %v", toks[1].Kind)
	}

	toks = lex(t, `/div`)
	if toks[1].Kind != TokName || toks[1].Val != "div" {
		t.Errorf("`/div`: div should lex as a name, got %v %q", toks[1].Kind, toks[1].Val)
	}

	// `and` as an element name in a path.
	toks = lex(t, `a/and`)
	if toks[2].Kind != TokName {
		t.Errorf("`a/and`: trailing and should be a name, got %v", toks[2].Kind)
	}
}

func TestLexWildcardForms(t *testing.T) {
	cases := []struct{ src, want string }{
		{`*`, "*"},
		{`p:*`, "p:*"},
		{`*:local`, "*:local"},
	}
	for _, c := range cases {
		toks := lex(t, c.src)
		if len(toks) != 1 || toks[0].Kind != TokWildcard || toks[0].Val != c.want {
			t.Errorf("lex(%q) = %+v, want single wildcard %q", c.src, toks, c.want)
		}
	}
}

func TestLexNumericLiteralTypes(t *testing.T) {
	// The lexical form fixes the type: no dot/E is integer, dot is decimal,
	// E is double. Getting this wrong makes `1 div 3` return the wrong type.
	cases := []struct {
		src  string
		want numLiteralKind
	}{
		{"1", numInteger},
		{"42", numInteger},
		{"1.5", numDecimal},
		{".5", numDecimal},
		{"1e3", numDouble},
		{"1.5E-2", numDouble},
	}
	for _, c := range cases {
		toks := lex(t, c.src)
		if len(toks) != 1 || toks[0].Kind != TokNumber {
			t.Errorf("lex(%q): want one number token, got %+v", c.src, toks)
			continue
		}
		if toks[0].NumType != c.want {
			t.Errorf("lex(%q).NumType = %v, want %v", c.src, toks[0].NumType, c.want)
		}
	}
}

func TestLexTrailingEIsNotAnExponent(t *testing.T) {
	// "1e" with no exponent digits must not swallow the 'e': it belongs to
	// whatever follows. The literal still has to be separated from that —
	// "1eq 2" is XPST0003, not "1 eq 2" — so what this checks is that the
	// number ends at the right place and the error names the literal rather
	// than reporting a malformed exponent.
	for _, src := range []string{`1eq 2`, `10div 3`, `10idiv 3`} {
		l := NewLexer(src)
		_, err := l.Tokens()
		if err == nil {
			t.Errorf("lex(%q) succeeded; a literal must be separated from a name", src)
			continue
		}
		if !strings.Contains(err.Error(), "must be separated") {
			t.Errorf("lex(%q): %v, want a separation error", src, err)
		}
	}
	// A literal followed by something that is not a name character is fine,
	// and "1e5" is still one double.
	toks := lex(t, `1e5`)
	if len(toks) != 1 || toks[0].NumType != numDouble {
		t.Errorf("lex(\"1e5\") = %+v, want one double", toks)
	}
	if toks := lex(t, `1+2`); len(toks) != 3 {
		t.Errorf("lex(\"1+2\") = %+v, want three tokens", toks)
	}
}

func TestLexStringEscaping(t *testing.T) {
	cases := []struct{ src, want string }{
		{`"abc"`, "abc"},
		{`'abc'`, "abc"},
		{`"a""b"`, `a"b`}, // doubled quote is an escape
		{`'a''b'`, `a'b`},
		{`"it's"`, "it's"},
		{`""`, ""},
	}
	for _, c := range cases {
		toks := lex(t, c.src)
		if len(toks) != 1 || toks[0].Kind != TokString || toks[0].Val != c.want {
			t.Errorf("lex(%q) = %+v, want string %q", c.src, toks, c.want)
		}
	}
}

func TestLexComments(t *testing.T) {
	toks := lex(t, `1 (: this is a comment :) + 2`)
	if len(toks) != 3 {
		t.Fatalf("got %d tokens, want 3 (comment must be skipped): %+v", len(toks), toks)
	}
	// Nested comments.
	toks = lex(t, `1 (: outer (: inner :) still outer :) + 2`)
	if len(toks) != 3 {
		t.Errorf("nested comment not handled: got %d tokens %+v", len(toks), toks)
	}
}

func TestLexAxisSeparatorNotQName(t *testing.T) {
	// `child::a` must not lex "child:" as a QName prefix.
	toks := lex(t, `child::a`)
	if len(toks) != 3 {
		t.Fatalf("got %d tokens, want 3: %+v", len(toks), toks)
	}
	if toks[0].Val != "child" || toks[1].Val != "::" || toks[2].Val != "a" {
		t.Errorf("got %q %q %q, want child :: a", toks[0].Val, toks[1].Val, toks[2].Val)
	}
}

func TestLexMultiCharOperators(t *testing.T) {
	for _, op := range []string{"!=", "<=", ">=", "<<", ">>", "//"} {
		toks := lex(t, "a "+op+" b")
		if toks[1].Val != op {
			t.Errorf("operator %q lexed as %q", op, toks[1].Val)
		}
	}
}

func TestLexVariables(t *testing.T) {
	toks := lex(t, `$foo + $ns:bar`)
	if toks[0].Kind != TokVar || toks[0].Val != "foo" {
		t.Errorf("got %v %q, want var foo", toks[0].Kind, toks[0].Val)
	}
	if toks[2].Kind != TokVar || toks[2].Val != "ns:bar" {
		t.Errorf("got %v %q, want var ns:bar", toks[2].Kind, toks[2].Val)
	}
}

func TestLexRejectsUnterminatedString(t *testing.T) {
	if _, err := NewLexer(`"abc`).Tokens(); err == nil {
		t.Error("unterminated string accepted")
	}
}
