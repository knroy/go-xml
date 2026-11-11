package xpath

import (
	"strings"
	"testing"
)

// TestSM06BackrefNoWrongAnswer: a variable-width head before the referenced
// group, a group under an alternation, a "\N*" that is not last or runs
// unanchored to "$", and "m" with an anchor are refused by default (FORX0002,
// never false) and answered by the opt-in backtracker.
func TestSM06BackrefNoWrongAnswer(t *testing.T) {
	cases := []struct{ s, p, f string }{
		{"aa", `a*(a)\1`, ""},
		{"11", `.*(\d)\1`, ""},
		{"x11", `x+(\d)\1`, ""},
		{"ab", `(?:(a)|a)\1`, ""},
		{"aaa", `(a)(a)\1*\2`, ""},
		{"ab\naa", `^(a)\1`, "m"},
		{"aa\nb", `(a)\1$`, "m"},
		{"aaa1aa", `([a-z])\1*$`, ""},
	}
	for _, c := range cases {
		if _, err := buildBackrefRegexp(c.p, c.f, XPath31); err == nil ||
			!strings.Contains(err.Error(), "FORX0002") {
			t.Errorf("%s: err = %v, want FORX0002", c.p, err)
		}
	}
	withBacktracking(t, func() {
		for _, c := range cases {
			bt, err := compileBacktrackFallback(c.p, c.f,
				errSM06(buildBackrefRegexp(c.p, c.f, XPath31)), XPath31)
			if err != nil {
				t.Errorf("%s: backtracker refused: %v", c.p, err)
				continue
			}
			if !bt.MatchString(matchInput(c.s, c.f)) || bt.Err() != nil {
				t.Errorf("%s on %q: backtracker = false (%v), want true", c.p, c.s, bt.Err())
			}
		}
	})
	if err := evalErr(t, testDoc, `matches("aa", "a*(a)\1")`); !strings.Contains(err.Error(), "FORX0002") {
		t.Errorf("fn:matches default: %v, want FORX0002", err)
	}
}

func errSM06(_ *backrefRegexp, err error) error { return err }

// TestSM06BackrefAgreesWithBacktracker: every pattern the fixed-width path
// admits gives the backtracker's answer, for matches and replace.
func TestSM06BackrefAgreesWithBacktracker(t *testing.T) {
	pats := []string{`(a)\1`, `^(a)\1$`, `(a|b)\1`, `x(.)\1`, `(ki|ke)\1`,
		`^([a-z])\1*$`, `([a-z])\1*`, `(.)\19`, `^(a)\1`}
	ins := []string{"", "a", "aa", "aab", "baab", "xbb", "abba", "kikeke",
		"aaaa", "abc", "aa9", "abba", "xaa", "abbaabba"}
	for _, p := range pats {
		br, err := buildBackrefRegexp(p, "", XPath31)
		if err != nil {
			t.Errorf("%s: refused: %v", p, err)
			continue
		}
		bt, err := compileBacktrack(p, "", XPath31)
		if err != nil {
			t.Fatalf("%s: backtracker: %v", p, err)
		}
		for _, s := range ins {
			if g, w := br.MatchString(s), bt.MatchString(s); g != w {
				t.Errorf("matches(%q, %s) = %v, backtracker %v", s, p, g, w)
			}
			if bt.MatchString("") {
				continue
			}
			if g, w := br.ReplaceAllString(s, "<${1}>"), bt.ReplaceAllString(s, "<${1}>"); g != w {
				t.Errorf("replace(%q, %s) = %q, backtracker %q", s, p, g, w)
			}
		}
	}
}

// TestSM06BackrefLinear: the window scan does one bounded step per start, so a
// long subject with a failing tail at every start finishes promptly.
func TestSM06BackrefLinear(t *testing.T) {
	br, err := buildBackrefRegexp(`(a)b\1`, "", XPath31)
	if err != nil {
		t.Fatal(err)
	}
	if br.MatchString(strings.Repeat("abc", 1<<17)) {
		t.Error("matched without a trailing a")
	}
}
