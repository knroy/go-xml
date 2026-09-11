package xquery

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// maxNestDepth is a depth cap, and it reported XPST0003 -- "the expression is
// syntactically invalid" -- about a query that is nothing of the sort. The
// code stays, because callers and the conformance suites read it, and
// xdm.ErrResourceLimit is added alongside so a caller can tell "your query is
// malformed" from "this parser declined to read one this deep".
func TestNestingRefusalCarriesSentinelAndKeepsItsCode(t *testing.T) {
	q := strings.Repeat("(", maxNestDepth+100) + "<a/>" +
		strings.Repeat(")", maxNestDepth+100)
	_, err := Compile(q, Options{})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell this refusal from a malformed query", err)
	}
	if code := xdm.ErrorCode(err); code != "XPST0003" {
		t.Errorf("code = %q, want XPST0003; the wrap must ADD the sentinel, "+
			"never replace the code", code)
	}
	if !strings.Contains(err.Error(), "expressions nested more than") {
		t.Errorf("message %q lost its leading text", err)
	}
}

// A genuinely malformed query must NOT report as a resource limit, or the
// distinction is worthless.
func TestMalformedQueryIsNotAResourceLimit(t *testing.T) {
	_, err := Compile(`<a/> +`, Options{})
	if err == nil {
		t.Fatal("expected a syntax error")
	}
	if errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("a syntax error %v reports as a resource limit", err)
	}
}

// XQuery reaches the regex functions through xpath.Builtins(), so the four
// wrappers are literally the same code the XPath tests cover. What is not
// shared is the path back out: a function error crosses the XQuery evaluator
// and Eval's own error handling before a caller sees it, and a layer that
// rebuilt the error from its text rather than wrapping it would drop the
// sentinel without changing a single message.
//
// So this asserts the classification survives the host, not that the regex
// layer has it -- xpath/regex_wrapper_limit_test.go is what asserts that.
func TestRegexBudgetCarriesTheSentinelThroughXQuery(t *testing.T) {
	old := xpath.BacktrackingRegexEnabled()
	xpath.SetBacktrackingRegex(true)
	defer xpath.SetBacktrackingRegex(old)

	input := strings.Repeat("a", 60)
	for _, q := range []string{
		`matches("` + input + `", "(a*)*\1b")`,
		`replace("` + input + `", "(a*)*\1b", "x")`,
		`tokenize("` + input + `", "(a*)*\1b")`,
		`analyze-string("` + input + `", "(a*)*\1b")`,
	} {
		ctx := xpath.NewContext(nil, xpath.Builtins())
		ctx.Version = xpath.XPath31
		_, err := Eval(q, ctx, Options{})
		if err == nil {
			t.Errorf("%s returned a result; an exhausted budget is not an "+
				"answer this engine computed", q)
			continue
		}
		if !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s: errors.Is(%v, ErrResourceLimit) = false; the "+
				"classification was lost between the function and Eval", q, err)
		}
	}
}
