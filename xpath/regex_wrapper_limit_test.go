package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Every regex wrapper must classify an exhausted backtracking budget the same
// way.
//
// regex_budget_soundness_test.go already pins that the budget is an error
// rather than a silent false, and resourcelimit_test.go pins that fn:matches
// carries xdm.ErrResourceLimit. Neither covers the siblings, and the sentinel
// is exactly the kind of contract that holds at one call site and not at the
// next: each of the four functions has its own path out of the engine --
// fn:matches reads bt.Err() after MatchString, fn:replace reads it three
// times, fn:tokenize reads it through tokenizeBacktrack, and
// fn:analyze-string builds a node tree from the match list -- so "the regex
// layer wraps the sentinel" is a claim about four independent pieces of code.
//
// A caller that cannot tell "your pattern is wrong" from "this input was too
// expensive" retries the first forever and gives up on the second, and which
// mistake it makes would depend on which function it happened to call.
//
// fn:analyze-string is the reason this test exists: it compiled through
// compileXPathRegexp alone, so a backreference pattern never reached the
// backtracking engine at all and it answered FORX0002 "backreference \1 is
// not supported" where its three siblings answered the budget error.

// budgetPattern is the shape the backtracking budget exists for: nested
// unbounded quantification closed by a backreference, which explores
// exponentially many splits of the input. Sixty "a"s exhaust the budget.
const budgetPattern = `(a*)*\1b`

// regexLimitEval evaluates expr with the backtracking engine on, which is
// where the budget lives; with it off, these patterns are refused at compile
// time and no budget is ever charged.
func regexLimitEval(t *testing.T, expr string) error {
	t.Helper()
	old := BacktrackingRegexEnabled()
	SetBacktrackingRegex(true)
	defer SetBacktrackingRegex(old)
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	_, err := Eval(expr, ctx, nil)
	return err
}

func TestEveryRegexWrapperClassifiesTheBudgetAsAResourceLimit(t *testing.T) {
	input := strings.Repeat("a", 60)
	tests := []struct {
		name string
		expr string
	}{
		// The budget is charged on the subject string.
		{"matches", `matches("` + input + `", "` + budgetPattern + `")`},
		// fn:replace charges it on the MatchString("") empty-match probe
		// before it ever looks at the subject, so the sentinel has to survive
		// a different one of its three Err() reads than fn:matches uses.
		{"replace", `replace("` + input + `", "` + budgetPattern + `", "x")`},
		{"tokenize", `tokenize("` + input + `", "` + budgetPattern + `")`},
		{"analyze-string", `analyze-string("` + input + `", "` + budgetPattern + `")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := regexLimitEval(t, tt.expr)
			if err == nil {
				t.Fatalf("%s returned a result; an exhausted budget must be "+
					"an error, since any answer here was never computed", tt.expr)
			}
			if !errors.Is(err, xdm.ErrResourceLimit) {
				t.Errorf("errors.Is(%v, ErrResourceLimit) = false; this "+
					"wrapper's caller cannot tell an expensive input from an "+
					"invalid pattern, though its siblings' callers can", err)
			}
			// The sentinel is ADDED, never substituted: FORX0002 is what the
			// conformance suites read and roughly thirty sites in this repo
			// match on the message text.
			if code := xdm.ErrorCode(err); code != "FORX0002" {
				t.Errorf("code = %q, want FORX0002", code)
			}
			if !strings.Contains(err.Error(), "backtracking step budget") {
				t.Errorf("message %q no longer names the budget", err)
			}
		})
	}
}

// The other half, and the reason the test above is not vacuous: the same four
// wrappers must NOT report the sentinel for a pattern that is simply wrong,
// and must still answer normally for a backreference pattern the engine can
// afford. Without this, "make every regex error a resource limit" would pass.
func TestRegexWrappersSeparateCheapAnswersAndRealFaults(t *testing.T) {
	t.Run("a cheap backreference still answers", func(t *testing.T) {
		// Each wrapper is given a backreference pattern, which is what routes
		// it to the backtracking engine, on an input small enough to decide.
		// A limit that swallowed these would be an accidental low ceiling.
		for _, expr := range []string{
			`matches("abcabc", "^(abc)\1$")`,
			`replace("abcabc", "(abc)\1", "x")`,
			`tokenize("a,bb,a", "(,)\1?")`,
			`analyze-string("abcabc", "(abc)\1")`,
		} {
			if err := regexLimitEval(t, expr); err != nil {
				t.Errorf("%s = %v; this pattern is decided in a handful of "+
					"steps and must not be refused", expr, err)
			}
		}
	})

	t.Run("a malformed pattern is not a resource limit", func(t *testing.T) {
		for _, expr := range []string{
			`matches("a", "(")`,
			`replace("a", "(", "x")`,
			`tokenize("a", "(")`,
			`analyze-string("a", "(")`,
		} {
			err := regexLimitEval(t, expr)
			if err == nil {
				t.Errorf("%s: expected FORX0002 for an unclosed group", expr)
				continue
			}
			if errors.Is(err, xdm.ErrResourceLimit) {
				t.Errorf("%s: %v reports as a resource limit, but the pattern "+
					"is genuinely invalid; retrying it can never help", expr, err)
			}
		}
	})
}

// fn:analyze-string must reach the backtracking engine at all.
//
// This is separate from the sentinel: before the fix it refused "(abc)\1"
// outright, so which patterns the function library accepts depended on which
// function was asked. The sentinel test above cannot see that regression on
// its own -- a refusal is still an error -- so the acceptance is pinned here.
func TestAnalyzeStringAcceptsBackreferencePatterns(t *testing.T) {
	old := BacktrackingRegexEnabled()
	SetBacktrackingRegex(true)
	defer SetBacktrackingRegex(old)
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	seq, err := Eval(`analyze-string("xabcabcy", "(abc)\1")`, ctx, nil)
	if err != nil {
		t.Fatalf("analyze-string with a backreference = %v; fn:matches, "+
			"fn:replace, fn:tokenize and xsl:analyze-string all accept it", err)
	}
	if len(seq) != 1 {
		t.Fatalf("got %d items, want the single result element", len(seq))
	}
	n, ok := seq[0].(*xdm.Node)
	if !ok {
		t.Fatalf("got %T, want a node", seq[0])
	}
	// "abcabc" is the match; "x" and "y" are the non-matching substrings
	// either side of it.
	if got := n.StringValue(); got != "xabcabcy" {
		t.Errorf("string value = %q, want the whole input back", got)
	}
	var kinds []string
	for _, c := range n.Children {
		kinds = append(kinds, c.Name.Local)
	}
	want := "non-match match non-match"
	if got := strings.Join(kinds, " "); got != want {
		t.Errorf("children = %q, want %q", got, want)
	}
}
