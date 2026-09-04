package xquery

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
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
