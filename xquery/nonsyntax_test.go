package xquery

import "testing"

// skipNonSyntax is the single answer to "what here is not syntax", and every
// scanner in the package routes through it. The regions it has to know about
// are the four whose bytes look like grammar and are not: a string literal, a
// comment (which nests), a pragma (whose contents are unparsed text) and a
// string constructor.
func TestSkipNonSyntax(t *testing.T) {
	// want is the index of the last byte of the region, which is what the
	// helper returns; callers resume at want+1.
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"single-quoted literal", "'abc'", 4},
		{"double-quoted literal", `"abc"`, 4},
		{"doubled quote escapes", "'it''s'", 6},
		{"brace in literal", "'a}b'", 4},

		{"comment", "(: c :)", 6},
		{"nested comment", "(: a (: b :) c :)", 16},
		{"brace in comment", "(: } :)", 6},
		{"quote in comment", "(: it's :)", 9},
		{"pragma opener in comment", "(: (#p:x :)", 10},

		// [105] Pragma ::= "(#" S? EQName (S PragmaContents)? "#)".
		// PragmaContents is (Char* - (Char* '#)' Char*)): unparsed text, so
		// a quote or a brace inside is an ordinary character.
		{"pragma", "(#p:x y#)", 8},
		{"quote in pragma", `(#p:x " #)`, 9},
		{"brace in pragma", "(#p:x } #)", 9},
		{"comment opener in pragma", "(#p:x (: #)", 10},
		// A pragma does not nest: it ends at the first "#)".
		{"pragma does not nest", "(#a (#b #)", 9},
	} {
		got, ok, err := skipNonSyntax(c.src, 0)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !ok {
			t.Errorf("%s: not recognised as non-syntax: %q", c.name, c.src)
			continue
		}
		if got != c.want {
			t.Errorf("%s: end %d, want %d, in %q",
				c.name, got, c.want, c.src)
		}
	}
}

// A scan that cannot tell where a non-syntax region ends does not know where
// syntax resumes, so the helper reports the fault rather than pretending the
// contents are grammar. Every caller then decides in its own terms; none may
// carry on scanning inside the region.
func TestSkipNonSyntaxRejectsUnterminated(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"unterminated literal", "'abc"},
		{"unterminated comment", "(: abc"},
		{"unterminated nested comment", "(: a (: b :)"},
		{"unterminated pragma", "(#p:x abc"},
		{"pragma with only a hash", "(#p:x #"},
	} {
		if _, ok, err := skipNonSyntax(c.src, 0); err == nil {
			t.Errorf("%s: want an error for %q (ok=%v)", c.name, c.src, ok)
		}
	}
}

// A "(" that opens neither a comment nor a pragma is an ordinary parenthesis,
// and a backtick that opens no string constructor is an ordinary byte. Both
// must be reported as "not a non-syntax token" so that the caller's own case
// for them still runs -- a parenthesis it must count, an operator it must
// read.
func TestSkipNonSyntaxLeavesOrdinaryBytes(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"bare parenthesis", "(1, 2)"},
		{"lone backtick", "`x"},
		{"name", "abc"},
		{"brace", "{1}"},
		{"empty", ""},
	} {
		if _, ok, err := skipNonSyntax(c.src, 0); ok || err != nil {
			t.Errorf("%s: %q reported as non-syntax (ok=%v, err=%v)",
				c.name, c.src, ok, err)
		}
	}
}

// findEnclosed and findParen both walk raw source, and both used to be blind
// to the pragma: the quote in "(#p:x \" #)" opened a literal that ran past the
// closing delimiter they were looking for.
func TestBracketScansStepOverPragmas(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"quote in pragma", `{ (#p:x " #) {1} }`, 17},
		{"brace in pragma", `{ (#p:x } #) 1 }`, 15},
		{"comment opener in pragma", `{ (#p:x (: #) 1 }`, 16},
	} {
		got, err := findEnclosed(c.src, 0)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: close at %d, want %d, in %q",
				c.name, got, c.want, c.src)
		}
	}

	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"paren in pragma", `( (#p:x ) #) )`, 13},
		{"quote in pragma", `( (#p:x " #) )`, 13},
	} {
		got, err := findParen(c.src, 0)
		if err != nil {
			t.Errorf("findParen %s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("findParen %s: close at %d, want %d, in %q",
				c.name, got, c.want, c.src)
		}
	}
}

// The bug this refactor removes, end to end: an ExtensionExpr whose pragma
// carries a quote. needsXQueryParser must still route it to this package's
// parser, because XPath's grammar has no pragma at all and would refuse it as
// an unterminated string literal.
func TestPragmaRoutesToTheXQueryParser(t *testing.T) {
	for _, src := range []string{
		`1 eq (#p:x " #) {1}`,
		`1 eq (#p:x } #) {1}`,
		`(#p:x (: #) {1}`,
	} {
		if !needsXQueryParser(src) {
			t.Errorf("%q: routed to xpath, which has no pragma", src)
		}
	}
}

// A pragma written inside a FLWOR clause: its contents must not end the
// clause expression, however the delimiters inside it look. scanExprSingle's
// scan is the one that decides where the binding expression stops.
func TestPragmaInsideFLWORClause(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"quote in pragma", `for $x in (#p:x " #) {1} return $x`},
		{"return in pragma", `for $x in (#p:x return #) {1} return $x`},
		{"comma in pragma", `for $x in (#p:x , #) {1} return $x`},
	} {
		p := &parser{src: c.src}
		got, err := p.scanExprSingleSource()
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		// The scan must reach the trailing "return", not one written inside
		// the pragma, so the whole extension expression is the binding.
		if got == "" || got == c.src {
			t.Errorf("%s: scanned %q from %q", c.name, got, c.src)
		}
	}
}

// A comment is the one non-syntax region that is also whitespace
// (A.2.4.1 Whitespace ::= S | Comment), so the cases that motivated the
// nesting depth count are kept here alongside the pragma ones.
func TestCommentsInScans(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"nested comment", "{ (: a (: } :) b :) 1 }", 22},
		{"brace in comment", "{ (: } :) 1 }", 12},
		{"quote in comment", "{ (: it's :) 1 }", 15},
	} {
		got, err := findEnclosed(c.src, 0)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: close at %d, want %d, in %q",
				c.name, got, c.want, c.src)
		}
	}

	// An unterminated comment or pragma must not be scanned through as
	// though its contents were grammar.
	for _, c := range []struct{ name, src string }{
		{"unterminated comment", "{ (: abc"},
		{"unterminated pragma", "{ (#p:x abc"},
	} {
		if _, err := findEnclosed(c.src, 0); err == nil {
			t.Errorf("%s: want an error for %q", c.name, c.src)
		}
	}
}
