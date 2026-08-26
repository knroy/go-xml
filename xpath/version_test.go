package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The 3.0 regular expression additions must be accepted under 3.0 and refused
// under 2.0. Refusing them under 2.0 is the part worth a test: silently
// accepting a construct the version does not define is what makes an engine
// disagree with every other processor rather than merely lag behind one.
func TestRegexVersionGating(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
	}{
		{"non-capturing group", "(?:ab)+"},
		{"nested non-capturing", "(a(?:bc)d)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkRegexGrammar(tc.pattern, XPath30); err != nil {
				t.Errorf("XPath30 rejected %q: %v", tc.pattern, err)
			}
			err := checkRegexGrammar(tc.pattern, XPath20)
			if err == nil {
				t.Fatalf("XPath20 accepted %q, want FORX0002", tc.pattern)
			}
			if !strings.Contains(err.Error(), "FORX0002") {
				t.Errorf("XPath20 %q: got %v, want FORX0002", tc.pattern, err)
			}
		})
	}

	// Every other "(?" construct stays out of the language at both versions:
	// 3.0 adds the non-capturing group alone, not Perl's group syntax.
	for _, p := range []string{"(?i)a", "(?=a)b", "(?<name>a)", "(?>a)"} {
		for _, v := range []Version{XPath20, XPath30} {
			if err := checkRegexGrammar(p, v); err == nil {
				t.Errorf("%v accepted %q, want FORX0002", v, p)
			}
		}
	}
}

// The "q" flag is FORX0001 under 2.0 and a literal-string match under 3.0.
func TestQFlagVersionGating(t *testing.T) {
	const pattern = `a.c`

	if _, err := buildRegexp(pattern, "q", XPath20); err == nil {
		t.Error("XPath20 accepted the q flag, want FORX0001")
	} else if !strings.Contains(err.Error(), "FORX0001") {
		t.Errorf("XPath20 q flag: got %v, want FORX0001", err)
	}

	re, err := buildRegexp(pattern, "q", XPath30)
	if err != nil {
		t.Fatalf("XPath30 rejected the q flag: %v", err)
	}
	// Under "q" the "." is a literal dot, so it matches "a.c" and not "abc".
	if !re.MatchString("a.c") {
		t.Error(`q: "a.c" should match the literal pattern "a.c"`)
	}
	if re.MatchString("abc") {
		t.Error(`q: "abc" should not match the literal pattern "a.c"`)
	}
}

// The q flag combines with i, and with nothing else that inspects structure.
func TestQFlagWithCaseInsensitive(t *testing.T) {
	re, err := buildRegexp(`A.C`, "qi", XPath30)
	if err != nil {
		t.Fatalf("qi: %v", err)
	}
	if !re.MatchString("a.c") {
		t.Error(`qi: "a.c" should match "A.C" case-blind`)
	}
	if re.MatchString("abc") {
		t.Error(`qi: "abc" should not match the literal "A.C"`)
	}
}

// The version reaches the function library through the context, so fn:matches
// answers differently for the same expression under the two versions. This is
// the end-to-end path the QT3 run exercises.
func TestMatchesHonoursContextVersion(t *testing.T) {
	const expr = `matches("abab", "(?:ab)+")`

	ctx30 := NewContext(nil, Builtins())
	ctx30.Version = XPath30
	got, err := Eval(expr, ctx30, nil)
	if err != nil {
		t.Fatalf("XPath30 %s: %v", expr, err)
	}
	if b, _ := EffectiveBooleanValue(got); !b {
		t.Errorf("XPath30 %s = false, want true", expr)
	}

	// The zero value of Context.Version is XPath20, so a caller that never
	// heard of versions keeps the 2.0 refusal.
	ctx20 := NewContext(nil, Builtins())
	if _, err := Eval(expr, ctx20, nil); err == nil {
		t.Errorf("XPath20 %s succeeded, want FORX0002", expr)
	}
}

// Two versions of one pattern must not collide in the compiled-pattern cache:
// the same source compiles to different results, and a shared key would let
// whichever ran first answer for both.
func TestRegexCacheKeyedByVersion(t *testing.T) {
	const pattern = "(?:xy)+"

	if _, err := compileXPathRegexp(pattern, "", XPath20); err == nil {
		t.Fatal("XPath20 accepted a non-capturing group")
	}
	re, err := compileXPathRegexp(pattern, "", XPath30)
	if err != nil {
		t.Fatalf("XPath30 after XPath20 miss: %v", err)
	}
	if !re.MatchString("xyxy") {
		t.Error("cached XPath30 pattern does not match")
	}
	// And back the other way, now that the 3.0 entry is warm.
	if _, err := compileXPathRegexp(pattern, "", XPath20); err == nil {
		t.Error("XPath20 accepted a non-capturing group from the 3.0 cache entry")
	}
}

// A non-capturing group must not be counted by the operations that number
// groups, which is what F&O 5.6.1 means by "causes the left parenthesis not to
// be counted". fn:replace's $N is the observable consequence.
func TestNonCapturingGroupNotNumbered(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath30

	got, err := Eval(`replace("abcd", "(?:ab)(cd)", "[$1]")`, ctx, nil)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("replace returned %d items, want 1", len(got))
	}
	if s := got[0].(*xdm.Atomic).String(); s != "[cd]" {
		t.Errorf("replace = %q, want %q — $1 must be the capturing group", s, "[cd]")
	}
}
