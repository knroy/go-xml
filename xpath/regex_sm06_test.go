package xpath

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
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

// TestSM06UCASubstringNotCubic: a needle of m characters no longer keys every
// span up to m at each start. Keyed bytes stay within a constant times n*m
// (the old scan was about n*m*m/2) and are charged to the byte budget.
func TestSM06UCASubstringNotCubic(t *testing.T) {
	c, err := parseUCACollation(UCACollation + "?lang=en")
	if err != nil {
		t.Fatal(err)
	}
	u := c.(*ucaCollation)
	const m = 200
	s := strings.Repeat("a", 2*m)
	sub := strings.Repeat("a", m) + "b"
	keyed := 0
	if i, _ := u.indexOf(s, sub, func(n int) error { keyed += n; return nil }); i != -1 {
		t.Fatalf("indexOf = %d, want -1", i)
	}
	if limit := 40 * len(s) * m; keyed > limit {
		t.Errorf("keyed %d bytes, want at most %d", keyed, limit)
	}
	if i, _ := u.indexOf(s+"b", sub, nil); i != m {
		t.Errorf("indexOf with a match = %d, want %d", i, m)
	}
	ctx := nearlyExhausted(t, 1<<16, 1<<20)
	if _, err := collationContains(ctx, c, s, sub); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("contains under a spent byte budget: err = %v, want ErrResourceLimit", err)
	}
}

// TestSM06FracPatternBounded: an [f] digit pattern wider than the picture
// width bound is refused, at every version, before any digit is rendered.
func TestSM06FracPatternBounded(t *testing.T) {
	if _, err := parseFracPattern(strings.Repeat("0", maxPictureWidth+1)); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("err = %v, want ErrResourceLimit", err)
	}
	// The left pad is one Repeat, not one prepend per missing digit.
	if got := roundHalfToEvenFrac(big.NewRat(1, 20), 5); got != ".05000" {
		t.Errorf("roundHalfToEvenFrac = %q, want .05000", got)
	}
}

// TestSM06ExponentPictureBounded: an exponent picture with more integer or
// fraction digits than the bound is refused with the picture code and the
// resource sentinel, at 3.0+ as well (the code rewrite kept the sentinel).
func TestSM06ExponentPictureBounded(t *testing.T) {
	wide := strings.Repeat("0", maxPictureWidth+1)
	for _, pic := range []string{wide + "e0", "0." + wide + "e0"} {
		_, err := FormatNumberVersion(xdm.NewInteger(1), pic, DefaultDecimalFormat(), XPath31)
		if !errors.Is(err, xdm.ErrResourceLimit) || xdm.ErrorCode(err) != "FODF1310" {
			t.Errorf("%.12s...: err = %v, want FODF1310 resource refusal", pic, err)
		}
	}
	if _, err := FormatNumberVersion(xdm.NewInteger(1), wide, DefaultDecimalFormat(), XPath31); err != nil {
		t.Errorf("wide picture without exponent: %v", err)
	}
}

// TestSM06SerializeHTMLCommentBreakout: fn:serialize refuses a comment that an
// HTML parser would close at once, as the tree serializer does.
func TestSM06SerializeHTMLCommentBreakout(t *testing.T) {
	for _, m := range []string{"html", "xhtml"} {
		for _, doc := range []string{`<r><!--><img/>--></r>`, `<r><!---><img/>--></r>`} {
			err := evalErrVersion(t, doc, `serialize(/r, map{'method':'`+m+`'})`, XPath31)
			if err == nil || !strings.Contains(err.Error(), "SERE0003") {
				t.Errorf("%s %s: err = %v, want SERE0003", m, doc, err)
			}
		}
	}
	if got := evalStrVersion(t, `<r><!--><img/>--></r>`, `serialize(/r, map{'method':'xml'})`, XPath31); !strings.Contains(got, "<!--><img/>-->") {
		t.Errorf("xml method = %q", got)
	}
}
