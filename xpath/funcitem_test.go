package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// wantErrCode asserts that expr fails with exactly the named error code.
//
// The code is the assertion, not the presence of an error. Every expression
// below is wrong in more than one way at once -- an unknown name is also a
// wrong arity, a type mismatch is also a cardinality mismatch -- so "it
// failed" is satisfied by a diagnosis other than the one being pinned, and by
// a failure from a cause the test is not about at all.
func wantErrCode(t *testing.T, ctx *Context, expr, code string) {
	t.Helper()
	_, err := Eval(expr, ctx, nil)
	if err == nil {
		t.Errorf("%s succeeded, want %s", expr, code)
		return
	}
	if got := xdm.ErrorCode(err); got != code {
		t.Errorf("%s: error code %s (%v), want %s", expr, got, err, code)
	}
}

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
	wantErrCode(t, ctx, `no-such-function#1`, "XPST0017")
	// Arity is part of a function's identity, so the wrong one does not
	// resolve: fn:substring has 2 and 3, not 9. XPST0017 in particular --
	// "unknown function" -- is what says arity is part of the name, rather
	// than the reference resolving and failing later for some other reason.
	wantErrCode(t, ctx, `substring#9`, "XPST0017")
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
	wantErrCode(t, ctx, `(function($x as xs:integer) { $x })("nope")`, "XPTY0004")
	// The declared return type is checked too. Without the check the body
	// still runs, and "nope" + nothing is a different error entirely -- which
	// is why XPTY0004 rather than "an error" is what says the declared type
	// was applied at the boundary.
	wantErrCode(t, ctx, `(function($x) as xs:integer { "nope" })(1)`, "XPTY0004")
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
		wantErrCode(t, ctx, expr, "XPTY0004")
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
	// The wrong arity is equally absent. fn:concat is variadic and registered
	// up to 100, so the arity chosen here has to be one no function has.
	const wrongArity = `function-lookup(QName("http://www.w3.org/2005/xpath-functions", "substring"), 99)`
	if got := eval30(t, wrongArity, nil); len(got) != 0 {
		t.Errorf("function-lookup at a bad arity returned %d items, want 0", len(got))
	}
}

// Atomising a function item is FOTY0013, and must not silently succeed.
//
// Two codes are in play and the loose assertion hid the difference. FOTY0013
// is atomisation refused; FOTY0014 is fn:string refused, which F&O gives its
// own code because fn:string is not atomisation -- it is defined on nodes and
// on atomic values and simply has no definition for a function item. So
// "string(concat#3)" is FOTY0014 while the arithmetic and fn:data paths, which
// do atomise, are FOTY0013. Asserting only "an error" passes with the two
// swapped, and passes if either path stops distinguishing them at all.
func TestFunctionItemDoesNotAtomize(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath30
	for _, c := range []struct{ expr, code string }{
		// fn:string has no definition for a function item; that is its own
		// code, not the atomisation one.
		{`string(concat#3)`, "FOTY0014"},
		// Arithmetic and fn:data both atomise, so both are FOTY0013.
		{`concat#3 + 1`, "FOTY0013"},
		{`data(concat#3)`, "FOTY0013"},
	} {
		wantErrCode(t, ctx, c.expr, c.code)
	}

	// The checked path names the error explicitly.
	seq := xdm.Sequence{&xdm.FunctionItem{Arity: 1}}
	if _, err := xdm.AtomizeChecked(seq); err == nil {
		t.Error("AtomizeChecked accepted a function item")
	} else if !strings.Contains(err.Error(), "FOTY0013") {
		t.Errorf("AtomizeChecked error = %v, want FOTY0013", err)
	}
}
