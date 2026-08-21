package xpath

import (
	"strings"
	"testing"
)

// Constant folding must preserve values exactly, including type: xs:double(1)
// and the integer 1 are different values, and a folded literal carries its
// type with it.
func TestConstantFoldingPreservesValues(t *testing.T) {
	ns := testResolver{"xs": "http://www.w3.org/2001/XMLSchema"}
	ctx := NewContext(nil, Builtins())

	cases := []struct{ expr, want string }{
		{`1 + 2`, "3"},
		{`(1 + 2) * 3`, "9"},
		{`7 idiv 2`, "3"},
		{`1.5 + 1.5`, "3"},
		// Exact decimal arithmetic must survive folding.
		{`0.1 + 0.2`, "0.3"},
		{`xs:double(1) + 1`, "2"},
		{`concat('a', 'b', 'c')`, "abc"},
		{`upper-case('abc')`, "ABC"},
		{`string-length('hello')`, "5"},
		{`count(1 to 100)`, "100"},
		{`sum(1 to 100)`, "5050"},
		{`not(true())`, "false"},
		{`abs(-5)`, "5"},
	}
	for _, c := range cases {
		compiled, err := Compile(c.expr, ns)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		seq, err := compiled.Eval(ctx)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got := ""
		if len(seq) == 1 {
			got = seq[0].(interface{ String() string }).String()
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// An expression that raises must still raise at evaluation, not at
// compilation. Folding "1 div 0" into a compile-time error would refuse a
// stylesheet whose branch is never taken.
func TestFoldingDefersErrors(t *testing.T) {
	ns := testResolver{"xs": "http://www.w3.org/2001/XMLSchema"}
	for _, expr := range []string{
		`1 idiv 0`,
		`xs:integer('abc')`,
		`xs:date('not-a-date')`,
	} {
		// Compilation must succeed...
		compiled, err := Compile(expr, ns)
		if err != nil {
			t.Errorf("%s failed to compile: %v — the error should be deferred "+
				"to evaluation", expr, err)
			continue
		}
		// ...and the error must appear when it is evaluated.
		if _, err := compiled.Eval(NewContext(nil, Builtins())); err == nil {
			t.Errorf("%s evaluated without the error it should raise", expr)
		}
	}

	// The branch that is never taken must not raise at all.
	compiled, err := Compile(`if (true()) then 1 else (1 idiv 0)`, ns)
	if err != nil {
		t.Fatalf("compiling a guarded division failed: %v", err)
	}
	if _, err := compiled.Eval(NewContext(nil, Builtins())); err != nil {
		t.Errorf("an unreached branch raised: %v", err)
	}
}

// Nothing that depends on the dynamic context may be folded: its value is not
// knowable at compile time, and folding it would freeze one node's answer into
// the stylesheet.
func TestFoldingSkipsContextDependentExpressions(t *testing.T) {
	ns := testResolver{"xs": "http://www.w3.org/2001/XMLSchema"}
	for _, expr := range []string{
		`position() + 1`,
		`last()`,
		`. + 1`,
		`$x + 1`,
		`current-dateTime()`,
		`string(.)`,
		`count(//a)`,
	} {
		compiled, err := Compile(expr, ns)
		if err != nil {
			continue // a parse error is not what this test is about
		}
		if _, ok := compiled.expr.(*Literal); ok {
			t.Errorf("%s was folded to a literal; its value depends on the "+
				"dynamic context", expr)
		}
	}
}

// The folded tree must actually be a literal, or the pass is doing nothing.
func TestFoldingActuallyReplacesTheTree(t *testing.T) {
	ns := testResolver{"xs": "http://www.w3.org/2001/XMLSchema"}
	for _, expr := range []string{`1 + 2`, `count(1 to 10)`, `concat('a','b')`} {
		compiled, err := Compile(expr, ns)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if _, ok := compiled.expr.(*Literal); !ok {
			t.Errorf("%s compiled to %T, want a folded *Literal", expr, compiled.expr)
		}
	}
}

// A user-defined function is never folded, even if it happens to be pure: the
// library it resolves through is supplied by the caller and can change.
func TestFoldingSkipsUnknownFunctions(t *testing.T) {
	ns := testResolver{"my": "urn:example", "xs": "http://www.w3.org/2001/XMLSchema"}
	compiled, err := Compile(`my:f(1)`, ns)
	if err != nil {
		t.Skipf("parse: %v", err)
	}
	if _, ok := compiled.expr.(*Literal); ok {
		t.Error("a user-defined function call was folded")
	}
	// It must still fail at evaluation, since no such function is registered.
	if _, err := compiled.Eval(NewContext(nil, Builtins())); err == nil {
		t.Error("an unregistered function evaluated without error")
	} else if !strings.Contains(err.Error(), "XPST0017") {
		t.Errorf("error = %v, want XPST0017", err)
	}
}
