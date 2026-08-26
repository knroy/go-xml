package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func str30(t *testing.T, expr string) []string {
	t.Helper()
	got := eval30(t, expr, nil)
	out := make([]string, 0, len(got))
	for _, it := range got {
		out = append(out, it.(*xdm.Atomic).String())
	}
	return out
}

// Function items are a 3.0 addition, so none of the syntax may parse as 2.0.
func TestFunctionItemsRejectedUnderXPath20(t *testing.T) {
	for _, expr := range []string{
		`concat#3`,
		`function($x) { $x }`,
		`let $f := concat#3 return $f("a", "b", "c")`,
	} {
		ctx := NewContext(nil, Builtins())
		if _, err := Eval(expr, ctx, nil); err == nil {
			t.Errorf("XPath20 accepted %s, want a static error", expr)
		}
	}
}

func TestNamedFunctionRef(t *testing.T) {
	// A reference is a value: it exists, has a name and an arity.
	if got, want := str30(t, `function-arity(concat#3)`), []string{"3"}; !equalStrings(got, want) {
		t.Errorf("function-arity(concat#3) = %v, want %v", got, want)
	}
	if got := str30(t, `local-name-from-QName(function-name(substring#2))`); !equalStrings(got, []string{"substring"}) {
		t.Errorf("function-name(substring#2) local part = %v, want [substring]", got)
	}
	// An anonymous function has no name.
	if got := eval30(t, `function-name(function($x) { $x })`, nil); len(got) != 0 {
		t.Errorf("function-name of an inline function returned %d items, want 0", len(got))
	}
	// A reference to a function that does not exist is a static error.
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath30
	if _, err := Eval(`no-such-function#1`, ctx, nil); err == nil {
		t.Error("a reference to an unknown function succeeded, want XPST0017")
	}
	// Arity is part of a function's identity, so the wrong one does not
	// resolve: fn:substring has 2 and 3, not 9.
	if _, err := Eval(`substring#9`, ctx, nil); err == nil {
		t.Error("substring#9 resolved, want XPST0017")
	}
}

func TestDynamicCall(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{`let $f := concat#3 return $f("a", "b", "c")`, []string{"abc"}},
		{`let $f := function($x) { $x + 1 } return $f(41)`, []string{"42"}},
		{`(function($x) { $x * 2 })(21)`, []string{"42"}},
		// Zero arity.
		{`let $f := function() { 7 } return $f()`, []string{"7"}},
		// A function returned from a function.
		{`let $mk := function($n) { function($x) { $x + $n } } return $mk(10)(32)`, []string{"42"}},
	}
	for _, tc := range cases {
		if got := str30(t, tc.expr); !equalStrings(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

// An inline function closes over the scope it was written in, not the scope it
// is called from. This is what makes it a closure rather than a nameless
// function definition.
func TestInlineFunctionIsAClosure(t *testing.T) {
	// $n is captured at the point the function is written. The outer $n is
	// shadowed by an inner binding at the call site, which must not be seen.
	const expr = `let $n := 2
	              return let $f := function($x) { $x * $n }
	                     return let $n := 1000 return $f(21)`
	if got, want := str30(t, expr), []string{"42"}; !equalStrings(got, want) {
		t.Errorf("closure = %v, want %v — the captured $n must win", got, want)
	}
}

// A declared parameter type is applied on the way in, so a mismatch is
// XPTY0004 at the call rather than a stranger error from inside the body.
func TestInlineFunctionParamTypes(t *testing.T) {
	if got, want := str30(t, `(function($x as xs:integer) { $x + 1 })(41)`), []string{"42"}; !equalStrings(got, want) {
		t.Errorf("typed param = %v, want %v", got, want)
	}
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath30
	if _, err := Eval(`(function($x as xs:integer) { $x })("nope")`, ctx, nil); err == nil {
		t.Error("a string passed to an xs:integer parameter succeeded, want XPTY0004")
	}
	// The declared return type is checked too.
	if _, err := Eval(`(function($x) as xs:integer { "nope" })(1)`, ctx, nil); err == nil {
		t.Error("a string returned from an xs:integer function succeeded, want XPTY0004")
	}
}

// Calling a non-function, or calling with the wrong number of arguments, is
// XPTY0004 rather than something that silently does nothing.
func TestDynamicCallErrors(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath30
	for _, expr := range []string{
		`let $f := 42 return $f(1)`,
		`let $f := function($x) { $x } return $f(1, 2)`,
		`let $f := function($x) { $x } return $f()`,
	} {
		if _, err := Eval(expr, ctx, nil); err == nil {
			t.Errorf("%s succeeded, want XPTY0004", expr)
		}
	}
}

func TestHigherOrderFunctions(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{`for-each((1, 2, 3), function($x) { $x * 2 })`, []string{"2", "4", "6"}},
		{`filter((1, 2, 3, 4), function($x) { $x mod 2 = 0 })`, []string{"2", "4"}},
		{`fold-left((1, 2, 3), 0, function($a, $b) { $a + $b })`, []string{"6"}},
		{`fold-right((1, 2, 3), 0, function($a, $b) { $a + $b })`, []string{"6"}},
		{`for-each-pair((1, 2, 3), (10, 20, 30), function($a, $b) { $a * $b })`, []string{"10", "40", "90"}},
		// for-each-pair stops at the shorter sequence.
		{`for-each-pair((1, 2, 3), (10, 20), function($a, $b) { $a + $b })`, []string{"11", "22"}},
		// An empty input sequence gives the zero for a fold and nothing else.
		{`fold-left((), 99, function($a, $b) { $a + $b })`, []string{"99"}},
		{`for-each((), function($x) { $x })`, nil},
		{`filter((), function($x) { true() })`, nil},
	}
	for _, tc := range cases {
		if got := str30(t, tc.expr); !equalStrings(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

// fold-left passes the accumulator first and fold-right passes it second.
// The difference is invisible for a commutative operation, so it is pinned
// with a non-commutative one.
func TestFoldArgumentOrderAndDirection(t *testing.T) {
	// Left fold, string concatenation: (((""+a)+b)+c) = "abc".
	if got, want := str30(t,
		`fold-left(("a","b","c"), "", function($acc, $x) { concat($acc, $x) })`,
		), []string{"abc"}; !equalStrings(got, want) {
		t.Errorf("fold-left concat = %v, want %v", got, want)
	}
	// Right fold, same operation: (a+(b+(c+""))) = "abc", but the item is the
	// first parameter and the accumulator the second.
	if got, want := str30(t,
		`fold-right(("a","b","c"), "", function($x, $acc) { concat($x, $acc) })`,
		), []string{"abc"}; !equalStrings(got, want) {
		t.Errorf("fold-right concat = %v, want %v", got, want)
	}
	// Subtraction shows the direction: left is ((10-1)-2)-3 = 4.
	if got, want := str30(t,
		`fold-left((1,2,3), 10, function($acc, $x) { $acc - $x })`,
		), []string{"4"}; !equalStrings(got, want) {
		t.Errorf("fold-left subtract = %v, want %v", got, want)
	}
	// Right is 1-(2-(3-10)) = 1-(2-(-7)) = 1-9 = -8.
	if got, want := str30(t,
		`fold-right((1,2,3), 10, function($x, $acc) { $x - $acc })`,
		), []string{"-8"}; !equalStrings(got, want) {
		t.Errorf("fold-right subtract = %v, want %v", got, want)
	}
}

func TestFunctionLookup(t *testing.T) {
	// A name that exists resolves to a callable function item.
	const expr = `let $f := function-lookup(QName("http://www.w3.org/2005/xpath-functions", "concat"), 3)
	              return $f("a", "b", "c")`
	if got, want := str30(t, expr), []string{"abc"}; !equalStrings(got, want) {
		t.Errorf("function-lookup = %v, want %v", got, want)
	}
	// A name that does not gives the empty sequence rather than an error,
	// which is what makes it usable as an availability test.
	const missing = `function-lookup(QName("http://www.w3.org/2005/xpath-functions", "no-such-fn"), 1)`
	if got := eval30(t, missing, nil); len(got) != 0 {
		t.Errorf("function-lookup of an unknown name returned %d items, want 0", len(got))
	}
	// The wrong arity is equally absent.
	const wrongArity = `function-lookup(QName("http://www.w3.org/2005/xpath-functions", "concat"), 99)`
	if got := eval30(t, wrongArity, nil); len(got) != 0 {
		t.Errorf("function-lookup at a bad arity returned %d items, want 0", len(got))
	}
}

// Atomising a function item is FOTY0013, and must not silently succeed.
func TestFunctionItemDoesNotAtomize(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath30
	for _, expr := range []string{
		`string(concat#3)`,
		`concat#3 + 1`,
		`data(concat#3)`,
	} {
		if _, err := Eval(expr, ctx, nil); err == nil {
			t.Errorf("%s succeeded, want an error", expr)
		}
	}

	// The checked path names the error explicitly.
	seq := xdm.Sequence{&xdm.FunctionItem{Arity: 1}}
	if _, err := xdm.AtomizeChecked(seq); err == nil {
		t.Error("AtomizeChecked accepted a function item")
	} else if !strings.Contains(err.Error(), "FOTY0013") {
		t.Errorf("AtomizeChecked error = %v, want FOTY0013", err)
	}
}
