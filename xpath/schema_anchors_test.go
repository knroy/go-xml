package xpath

import (
	"regexp"
	"testing"
)

// Appendix F's grammar has no anchors: a pattern facet is anchored as a whole,
// so "^" and "$" are ordinary characters a value has to carry. XPath's
// fn:matches does have them, and both flavours share one translator, so the
// schema side escapes them on the way in.
func TestSchemaPatternAnchorsAreLiteral(t *testing.T) {
	for _, c := range []struct {
		pattern string
		value   string
		match   bool
	}{
		// The value must carry the characters, not be delimited by them.
		{`^abc$`, `^abc$`, true},
		{`^abc$`, `abc`, false},
		{`a$`, `a$`, true},
		{`a$`, `a`, false},

		// Negation is still negation, at the head of a class.
		{`[^a]`, `b`, true},
		{`[^a]`, `a`, false},

		// Elsewhere in a class "^" is a literal member.
		{`[a^]`, `^`, true},
		{`[a^]`, `a`, true},
		{`[a^]`, `b`, false},

		// Classes nest: subtraction writes an inner class whose "^"
		// negates just as an outer one does. Escaping it turned this
		// into a subtraction of the literal "^" and "a", so "b" — which
		// the subtraction is meant to leave in — was refused.
		{`[a-z-[^a]]`, `a`, true},
		{`[a-z-[^a]]`, `b`, false},

		// An escaped anchor was already literal and must not double up.
		{`\^`, `^`, true},
	} {
		tr, err := TranslateSchemaRegexp(c.pattern)
		if err != nil {
			t.Errorf("%q: translate: %v", c.pattern, err)
			continue
		}
		re, err := regexp.Compile(`\A(?:` + tr + `)\z`)
		if err != nil {
			t.Errorf("%q -> %q: compile: %v", c.pattern, tr, err)
			continue
		}
		if got := re.MatchString(c.value); got != c.match {
			t.Errorf("pattern %q on %q = %v, want %v (translated %q)",
				c.pattern, c.value, got, c.match, tr)
		}
	}
}

// fn:matches keeps its anchors: the two flavours differ exactly here, and the
// escaping must not reach the XPath side.
func TestMatchesKeepsItsAnchors(t *testing.T) {
	tr, err := translatePattern(`^abc$`, false, XPath20)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	re, err := regexp.Compile(tr)
	if err != nil {
		t.Fatalf("compile %q: %v", tr, err)
	}
	if !re.MatchString("abc") {
		t.Error("fn:matches lost its anchors; ^abc$ should match abc")
	}
}
