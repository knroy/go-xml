package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// evalDepth runs an expression against a default context, as an untrusted
// caller would: no raised MaxDepth, so the package bound is the one in force.
func evalDepth(t *testing.T, expr string) (xdm.Sequence, error) {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	return Eval(expr, ctx, nil)
}

// atomText renders a single-atomic result, which is all these tests produce.
func atomText(t *testing.T, seq xdm.Sequence) string {
	t.Helper()
	if len(seq) != 1 {
		t.Fatalf("want one item, got %d", len(seq))
	}
	a, ok := seq[0].(*xdm.Atomic)
	if !ok {
		t.Fatalf("want an atomic result, got %s", seq[0].TypeName())
	}
	return a.String()
}

// A function item that applies itself recurses without ever passing through
// a named function call, which is the only place the depth bound used to be
// charged. Unbounded, this is not an error but a fatal stack overflow: Go
// cannot recover from one, so the whole host process dies. The bound must
// refuse it the same way it refuses named recursion.
func TestSelfApplyingInlineFunctionIsRefused(t *testing.T) {
	// The 62-byte expression from the report, in XPath and in the two
	// shapes a caller is most likely to meet it.
	for _, expr := range []string{
		`let $f := function($g, $n) { $g($g, $n + 1) } return $f($f, 1)`,
		// Self-application through a variable the body reads from its own
		// captured scope rather than from an argument.
		`let $f := function($n) { $n } return
		 let $g := function($h, $n) { $h($h, $f($n) + 1) } return $g($g, 1)`,
		// Mutual recursion between two function items.
		`let $a := function($f, $g, $n) { $g($f, $g, $n + 1) } return
		 let $b := function($f, $g, $n) { $f($f, $g, $n + 1) } return $a($a, $b, 1)`,
	} {
		_, err := evalDepth(t, expr)
		if err == nil {
			t.Fatalf("unbounded self-application was accepted: %s", expr)
		}
		if !strings.Contains(err.Error(), "XPDY0001") ||
			!strings.Contains(err.Error(), "recursion exceeded") {
			t.Fatalf("want XPDY0001 recursion refusal, got %v (for %s)", err, expr)
		}
		// The sentinel is what lets a caller tell a refusal from a fault,
		// and every other depth refusal carries it.
		if !errors.Is(err, xdm.ErrResourceLimit) {
			t.Fatalf("depth refusal lacks xdm.ErrResourceLimit: %v", err)
		}
	}
}

// The bound charges NESTING, not calls. Higher-order functions are pervasive
// and legitimate: fn:for-each over a thousand items is a thousand sequential
// calls at depth one, and a depth charge that accumulated across iterations
// rather than nesting would refuse it. That is the failure mode this guards.
func TestHigherOrderFunctionsAreNotChargedPerIteration(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		// A thousand sequential calls of the same function item.
		{"for-each 1000", `sum(fn:for-each(1 to 1000, function($x) { $x }))`, "500500"},
		// fn:filter, which invokes the predicate once per item.
		{"filter 1000", `count(fn:filter(1 to 1000, function($x) { $x mod 2 = 0 }))`, "500"},
		// fn:fold-left threads an accumulator through a thousand calls. The
		// calls are sequential; only the accumulator carries between them.
		{"fold-left 1000", `fn:fold-left(1 to 1000, 0, function($a, $b) { $a + $b })`, "500500"},
		{"fold-right 1000", `fn:fold-right(1 to 1000, 0, function($a, $b) { $a + $b })`, "500500"},
		// A partial application invoked many times.
		{"partial application 1000",
			`let $add := function($a, $b) { $a + $b } return
			 let $inc := $add(1, ?) return sum(fn:for-each(1 to 1000, $inc))`, "501500"},
		// fn:apply, and a function reached through fn:function-lookup.
		{"apply", `fn:apply(function($a, $b) { $a + $b }, [40, 2])`, "42"},
		// Maps and arrays are function items too, so every map:for-each and
		// array:for-each runs through the same path.
		// Written with EQNames so the test needs no prefix bindings; these
		// are map:for-each and array:for-each.
		{"map:for-each",
			`sum(Q{http://www.w3.org/2005/xpath-functions/map}for-each(` +
				`map{1:1, 2:2, 3:3}, function($k, $v) { $v }))`, "6"},
		{"array:for-each",
			`sum(Q{http://www.w3.org/2005/xpath-functions/array}for-each(` +
				`[1, 2, 3], function($x) { $x })?*)`, "6"},
		// fn:sort's key function is invoked once per item.
		{"sort 1000", `count(fn:sort(1 to 1000, (), function($x) { -$x }))`, "1000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evalDepth(t, tt.expr)
			if err != nil {
				t.Fatalf("legitimate higher-order use was refused: %v", err)
			}
			if s := atomText(t, got); s != tt.want {
				t.Fatalf("got %q, want %q", s, tt.want)
			}
		})
	}
}

// Genuine nesting below the bound must still work: the charge has to be
// released as each call returns, or a program that nests 400 deep once would
// poison every later call.
func TestLegitimateDeepDynamicNestingIsAccepted(t *testing.T) {
	// 400 nested self-applications, under the 500 default.
	const expr = `let $f := function($g, $n) {
		if ($n >= 400) then $n else $g($g, $n + 1)
	} return $f($f, 1)`
	got, err := evalDepth(t, expr)
	if err != nil {
		t.Fatalf("400-deep dynamic recursion was refused: %v", err)
	}
	if s := atomText(t, got); s != "400" {
		t.Fatalf("got %q, want 400", s)
	}
	// Running it twice in one evaluation proves the charge is released
	// rather than accumulated: two 400-deep nests in sequence are still
	// only 400 deep.
	got, err = evalDepth(t, `let $f := function($g, $n) {
		if ($n >= 400) then $n else $g($g, $n + 1)
	} return $f($f, 1) + $f($f, 1)`)
	if err != nil {
		t.Fatalf("two sequential 400-deep nests were refused: %v", err)
	}
	if s := atomText(t, got); s != "800" {
		t.Fatalf("got %q, want 800", s)
	}
}

// A caller that trusts its input can still raise the bound, exactly as it can
// for named recursion. The guard is a default for untrusted input, not a
// conformance limit.
func TestDynamicCallDepthHonoursCallerMaxDepth(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	ctx.MaxDepth = 30
	_, err := Eval(`let $f := function($g, $n) {
		if ($n >= 100) then $n else $g($g, $n + 1)
	} return $f($f, 1)`, ctx, nil)
	if err == nil {
		t.Fatal("a 100-deep nest was accepted under MaxDepth 30")
	}
	if !strings.Contains(err.Error(), "recursion exceeded 30 levels") {
		t.Fatalf("want the caller's own bound in the message, got %v", err)
	}
}
