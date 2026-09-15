package xquery_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xquery"
)

func evalDepth(t *testing.T, q string, max int) error {
	t.Helper()
	c, err := xquery.Compile(q, xquery.Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx := xpath.NewContext(nil, nil)
	ctx.MaxDepth = max
	_, ee := c.Eval(ctx)
	return ee
}

func wantDepthRefusal(t *testing.T, err error, bound string) {
	t.Helper()
	if err == nil {
		t.Fatal("unbounded self-application returned no error; the recursion " +
			"depth is not charged, and at scale this shape reaches a Go stack " +
			"overflow, which is fatal and cannot be recovered")
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell this refusal from a fault in its input", err)
	}
	if code := xdm.ErrorCode(err); code != "XPDY0001" {
		t.Errorf("code = %q, want XPDY0001", code)
	}
	if !strings.Contains(err.Error(), bound) {
		t.Errorf("message %q does not name the caller's bound of %s, so the "+
			"refusal may not be the one the caller asked for", err, bound)
	}
}

// A function applying itself through its own NAME reaches the body through
// withRetainedFocus, not through InlineFunctionExpr.Eval, and only the second
// of those took the depth from the call. The captured context carries the
// depth the reference was written at -- the same shallow value every time
// round -- so the charge never accumulated.
func TestNamedFunctionReferenceChargesDepth(t *testing.T) {
	err := evalDepth(t, `declare function local:f($g, $n as xs:integer)
	    as xs:integer { $g($g, $n + 1) }; local:f(local:f#2, 1)`, 25)
	wantDepthRefusal(t, err, "25")
}

// An inline function whose body holds XQuery-only syntax -- here a direct
// constructor -- is built by this package rather than by xpath, and that
// path discarded the call context entirely. One character, the <a> wrapper,
// decided whether the recursion was bounded.
func TestXQueryInlineFunctionChargesDepth(t *testing.T) {
	err := evalDepth(t, `let $f := function($g, $n) { <a>{ $g($g, $n + 1) }</a> }
	    return $f($f, 1)`, 25)
	wantDepthRefusal(t, err, "25")
}

// The other direction, for both shapes: a bound must not refuse a recursion
// that stays inside it, or charging twice per level would pass the tests
// above and still be wrong.
func TestRecursionInsideTheBoundStillRuns(t *testing.T) {
	for _, c := range []struct{ name, q string }{
		{"named reference", `declare function local:f($g, $n as xs:integer)
		    as xs:integer { if ($n >= 20) then $n else $g($g, $n + 1) };
		  local:f(local:f#2, 1)`},
		{"xquery inline", `let $f := function($g, $n) {
		    if ($n >= 20) then $n else $g($g, $n + 1) } return $f($f, 1)`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := evalDepth(t, c.q, 200); err != nil {
				t.Errorf("twenty levels under a bound of 200 must run: %v", err)
			}
		})
	}
}
