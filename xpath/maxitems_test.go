package xpath

import (
	"context"
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

// An array holds its members by reference, so a few charged constructors
// describe many leaves. array:flatten and fn:data materialise them, and the
// host's MaxItems must bound that, not only xdm.Flatten's fixed ceiling.
func TestFlattenAndDataChargeTheItemBudget(t *testing.T) {
	const nest = `let $a := [1, 2, 3, 4], $b := [$a, $a, $a, $a], $c := [$b, $b, $b, $b] return `
	eval := func(limit int, expr string) error {
		ctx := NewContext(nil, Builtins())
		ctx = ctx.WithEnv(func(e *Env) { e.MaxItems = limit })
		c, err := CompileWith(nest+expr, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Eval(ctx)
		return err
	}
	for _, expr := range []string{`count(Q{http://www.w3.org/2005/xpath-functions/array}flatten($c))`, `count(data($c))`} {
		if err := eval(40, expr); xdm.ErrorCode(err) != "XPDY0130" || !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s under a bound of 40: got %v, want XPDY0130", expr, err)
		}
		if err := eval(200, expr); err != nil {
			t.Errorf("%s under a bound of 200: %v", expr, err)
		}
	}
}

// array:flatten and fn:data flatten under the evaluation's Env.Ctx, so a
// cancelled host context stops a shared-member expansion with its error
// rather than with xdm.Flatten's step-ceiling panic.
func TestFlattenAndDataObserveEnvCtx(t *testing.T) {
	a := xdm.NewArray(xdm.Sequence{xdm.NewArray()})
	for i := 0; i < 30; i++ {
		a = xdm.NewArray(xdm.Sequence{a}, xdm.Sequence{a})
	}
	gctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := NewContext(nil, Builtins()).WithEnv(func(e *Env) { e.Ctx = gctx })
	for _, name := range []xdm.QName{{URI: xdm.NSArray, Local: "flatten"}, {URI: xdm.NSFN, Local: "data"}} {
		f, ok := Builtins().Lookup(name, 1)
		if !ok {
			t.Fatalf("%s#1 is not registered", name.Local)
		}
		if _, err := f.Call(ctx, []xdm.Sequence{{a}}); !errors.Is(err, context.Canceled) {
			t.Errorf("%s under a cancelled Env.Ctx: err=%v, want context.Canceled", name.Local, err)
		}
	}
}

// Implicit atomization -- a comparison, an argument conversion, sum -- flattens
// arrays as fn:data does, so the host's MaxItems bounds a shared-member
// expansion there too. Each atomization used to flatten one array at a time
// under a fresh bound, uncharged, so a hundred references to one array were a
// hundred free walks.
func TestImplicitAtomizationChargesTheItemBudget(t *testing.T) {
	const nest = `let $a := [1, 2, 3, 4], $b := [$a, $a, $a, $a], $c := [$b, $b, $b, $b] return `
	eval := func(limit int, expr string) error {
		ctx := NewContext(nil, Builtins())
		ctx = ctx.WithEnv(func(e *Env) { e.MaxItems = limit })
		c, err := CompileWith(nest+expr, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Eval(ctx)
		return err
	}
	for _, expr := range []string{
		`((1 to 4) ! $c) = -1`,
		`sum((1 to 4) ! $c)`,
		`string-join((1 to 4) ! $c, ',')`,
		`function($x as xs:integer*) { count($x) }((1 to 4) ! $c)`,
	} {
		if err := eval(100, expr); xdm.ErrorCode(err) != "XPDY0130" || !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s under a bound of 100: got %v, want XPDY0130", expr, err)
		}
	}
	if err := eval(1000, `((1 to 4) ! $c) = -1`); err != nil {
		t.Errorf("256 atoms under a bound of 1000: %v", err)
	}
}

// array:join copies every member of every array it is given, and an input
// can name one array many times: the copies are charged as they are made.
func TestArrayJoinChargesTheItemBudget(t *testing.T) {
	const src = `Q{http://www.w3.org/2005/xpath-functions/array}size(Q{http://www.w3.org/2005/xpath-functions/array}join(` +
		`let $a := array { 1 to 100 } return (1 to 100) ! $a))`
	eval := func(limit int) (xdm.Sequence, error) {
		ctx := NewContext(nil, Builtins()).WithEnv(func(e *Env) { e.MaxItems = limit })
		c, err := CompileWith(src, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		return c.Eval(ctx)
	}
	if _, err := eval(1000); xdm.ErrorCode(err) != "XPDY0130" || !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("10,000 joined members under a bound of 1000: got %v, want XPDY0130", err)
	}
	if got, err := eval(20000); err != nil || len(got) != 1 || got[0].(*xdm.Atomic).String() != "10000" {
		t.Errorf("under a bound of 20000: got %v, %v, want 10000", got, err)
	}
}

// A host may evaluate the Expr a Parse function returns directly, without
// Compiled.Eval's recovery. A shared-member array past xdm.Flatten's step
// bound is then refused with an error, not with a panic out of Eval.
func TestExprEvalReturnsTheFlattenRefusal(t *testing.T) {
	e, err := ParseVersion(`-fold-left(1 to 25, [], function($a, $i) { [$a, $a] })`, nil, XPath31)
	if err != nil {
		t.Fatal(err)
	}
	err = func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("Expr.Eval panicked: %v", p)
			}
		}()
		_, err = e.Eval(NewContext(nil, Builtins()).WithVersion(XPath31))
		return err
	}()
	if xdm.ErrorCode(err) != "XPDY0130" || !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("Expr.Eval: got %v, want XPDY0130", err)
	}
}
