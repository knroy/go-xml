package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// Env.MaxItems sets the item bound: zero is the MaxItems default,
// negative is none, positive is the bound. The range cap and AdoptBudget
// follow it.
func TestContextMaxItems(t *testing.T) {
	charge := func(limit, n int) error {
		ctx := NewContext(nil, Builtins())
		ctx = ctx.WithEnv(func(e *Env) { e.MaxItems = limit })
		return ctx.HoldItemBudget().ChargeItems(n)
	}
	if err := charge(0, MaxItems); err != nil {
		t.Fatalf("default bound refused MaxItems items: %v", err)
	}
	err := charge(0, MaxItems+1)
	if xdm.ErrorCode(err) != "XPDY0130" || !errors.Is(err, xdm.ErrResourceLimit) ||
		!strings.Contains(err.Error(), "more than 5000000 items") {
		t.Errorf("default bound: got %v, want the XPDY0130 refusal naming 5000000", err)
	}
	if err := charge(2*MaxItems, MaxItems+1); err != nil {
		t.Errorf("a raised bound refused %d items: %v", MaxItems+1, err)
	}
	if err := charge(-1, 4*MaxItems); err != nil {
		t.Errorf("a negative bound refused %d items: %v", 4*MaxItems, err)
	}

	eval := func(limit int, expr string) error {
		ctx := NewContext(nil, Builtins())
		ctx = ctx.WithEnv(func(e *Env) { e.MaxItems = limit })
		_, err := MustCompile(expr, nil).Eval(ctx)
		return err
	}
	if err := eval(10, "count((1 to 11)[. ge 0])"); err == nil || !strings.Contains(err.Error(), "the 10 item limit") {
		t.Errorf("range over a bound of 10: got %v, want the range refusal naming 10", err)
	}
	if err := eval(10, "count(for $i in 1 to 6 return ($i, $i))"); err == nil ||
		!strings.Contains(err.Error(), "more than 10 items") {
		t.Errorf("for over a bound of 10: got %v, want the refusal naming 10", err)
	}
	if err := eval(10, "count((1 to 10)[. ge 0])"); err != nil {
		t.Errorf("10 items under a bound of 10: %v", err)
	}

	// A nested evaluation spends its caller's budget under its caller's
	// bound, whatever its own Context said.
	parent := NewContext(nil, Builtins())
	parent = parent.WithEnv(func(e *Env) { e.MaxItems = 10 })
	parent = parent.HoldItemBudget()
	child := NewContext(nil, Builtins())
	child = child.WithEnv(func(e *Env) { e.MaxItems = -1 })
	child = child.AdoptBudget(parent)
	if err := child.ChargeItems(11); err == nil || !strings.Contains(err.Error(), "more than 10 items") {
		t.Errorf("a nested context escaped its caller's bound of 10: %v", err)
	}
}
