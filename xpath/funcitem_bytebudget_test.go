package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// wantByteRefusal asserts the byte-budget refusal, with the code and the
// sentinel a caller distinguishes it by. The same three assertions the host
// packages make -- see xquery/bytebudget_test.go.
func wantByteRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected XPDY0130; the caller's MaxBytes budget did not " +
			"bind inside the function item")
	}
	if code := xdm.ErrorCode(err); code != "XPDY0130" {
		t.Errorf("code = %q, want XPDY0130 (error: %v)", code, err)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell a refusal to allocate from a bad expression", err)
	}
	if !strings.Contains(err.Error(), "bytes of string content") {
		t.Errorf("message %q is not the byte-budget refusal", err)
	}
}

// A function item is a value, so the public API lets one be produced under one
// context and invoked under another: xdm.FunctionItem.Invoke is an exported
// field, and the item itself comes back out of Compiled.Eval. The body must be
// charged against the budget of the context it is CALLED in, not the one it
// was written in -- otherwise a caller that has nearly spent its allowance
// hands the function item a fresh unbounded one, and the budget is escaped by
// wrapping the work in a closure.
func TestFunctionItemChargesTheCallersByteBudget(t *testing.T) {
	for _, tc := range []struct{ name, expr string }{
		// The inline function expression: captured is the closure's context.
		{"inline", "function($x) { concat($x, $x) }"},
		// A named function reference, which goes through withRetainedFocus.
		{"namedref", "concat#2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The item is produced under one context...
			made := NewContext(nil, Builtins())
			made.Version = XPath30
			seq, err := Eval(tc.expr, made, nil)
			if err != nil {
				t.Fatalf("evaluating %q: %v", tc.expr, err)
			}
			fn, ok := seq[0].(*xdm.FunctionItem)
			if !ok {
				t.Fatalf("%q produced %T, not a function item", tc.expr, seq[0])
			}

			// ...and invoked under another that has all but spent its bytes.
			// The hold is what a host language sets, and is also what makes
			// the charge survive to the call: without it the reset in
			// Compiled.Eval would clear it again.
			caller := NewContext(nil, Builtins())
			caller.Version = XPath30
			caller = caller.HoldByteBudget()
			if err := caller.ChargeBytes(MaxBytes - 8); err != nil {
				t.Fatalf("charging the caller's budget: %v", err)
			}

			arg := xdm.One(xdm.NewString(strings.Repeat("A", 64)))
			args := []xdm.Sequence{arg}
			if fn.Arity == 2 {
				args = append(args, arg)
			}
			_, err = fn.Invoke(caller, args)
			wantByteRefusal(t, err)
		})
	}
}

// The counter itself must be the caller's, not a copy of it: the charge the
// body makes has to be visible to the caller afterwards, or a sequence of
// calls each stays inside the bound while the total runs away.
func TestFunctionItemSharesTheCallersByteCounter(t *testing.T) {
	made := NewContext(nil, Builtins())
	made.Version = XPath30
	seq, err := Eval("function($x) { concat($x, $x) }", made, nil)
	if err != nil {
		t.Fatal(err)
	}
	fn := seq[0].(*xdm.FunctionItem)

	caller := NewContext(nil, Builtins())
	caller.Version = XPath30
	caller = caller.HoldByteBudget()
	arg := xdm.One(xdm.NewString(strings.Repeat("A", 1024)))
	if _, err := fn.Invoke(caller, []xdm.Sequence{arg}); err != nil {
		t.Fatalf("the call itself failed: %v", err)
	}
	if got := *caller.bytes; got == 0 {
		t.Fatal("the function item's body charged nothing to the caller's " +
			"counter; it ran against a budget of its own, so a caller can " +
			"escape MaxBytes by wrapping the work in a closure")
	}
	// The context the item was made under must be untouched -- the budget
	// follows the call, and nothing about the closure's own evaluation.
	if got := *made.bytes; got != 0 {
		t.Errorf("the closure's own context was charged %d bytes; the "+
			"budget did not follow the call", got)
	}
}
