package xpath

import (
	"testing"
	"unsafe"

	"github.com/knroy/go-xml/v2/xdm"
)

// The per-scope part is what every step, predicate and binding copies, so its
// size is the cost of a scope change. It was 512 bytes when the environment and
// the static properties lived in it too.
func TestContextPerScopePartStaysSmall(t *testing.T) {
	if n := unsafe.Sizeof(Context{}); n > 160 {
		t.Errorf("Context is %d bytes, want at most 160: a field added here is "+
			"copied on every scope change; per-evaluation state belongs in Env", n)
	}
}

// Compiled.Eval installs the expression's static properties by pointer: an
// expression evaluated from a plain caller reuses one static part built at
// compile time rather than building one per evaluation.
func TestScopeReusesTheCompiledStaticPart(t *testing.T) {
	c := MustCompile(`static-base-uri()`, nil).WithStaticBaseURI("http://a/")
	ctx := NewContext(nil, Builtins())
	s1, s2 := c.scope(ctx).static, c.scope(ctx).static
	if s1 != &c.own || s2 != &c.own {
		t.Fatalf("scope built a static part per evaluation (%p, %p, own %p)", s1, s2, &c.own)
	}
	got, err := c.EvalString(ctx)
	if err != nil || got != "http://a/" {
		t.Fatalf("static-base-uri() = %q, %v; want http://a/", got, err)
	}
	// A caller whose static part has something c lacks still lends it: c was
	// compiled without a collation, so the caller's default applies.
	coll, err := ResolveCollation(HTMLASCIICaseInsensitive)
	if err != nil {
		t.Fatal(err)
	}
	withColl := withCollation(ctx, coll)
	if s := c.scope(withColl).st(); s == &c.own || s.collation != coll ||
		s.baseURI != "http://a/" {
		t.Errorf("merged static part = %+v; want the caller's collation under c's base URI", *s)
	}
}

// WithEnv cannot reset or drop a budget: the counters stay the Context's own
// whatever the configure function does to its copy.
func TestWithEnvKeepsTheBudgetCounters(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	before := ctx.ev().items
	got := ctx.WithEnv(func(e *Env) {
		*e = Env{MaxItems: 1}
	})
	if got.ev().items != before || got.ev().bytes != ctx.ev().bytes ||
		got.ev().nodes != ctx.ev().nodes || got.ev().entities != ctx.ev().entities {
		t.Fatal("WithEnv replaced the budget counters")
	}
	if err := got.ChargeItems(2); err == nil {
		t.Error("the bound set through WithEnv was not applied to the kept counter")
	}
	// The original context's environment is untouched.
	if ctx.Env().MaxItems != 0 {
		t.Errorf("WithEnv wrote through to the shared environment: MaxItems %d", ctx.Env().MaxItems)
	}
}

// A NewContext configure function runs before the budgets are installed, so it
// cannot remove them either.
func TestNewContextConfigureCannotRemoveBudgets(t *testing.T) {
	ctx := NewContext(xdm.NewString("x"), Builtins(), func(e *Env) { *e = Env{} })
	if e := ctx.ev(); e.items == nil || e.bytes == nil || e.nodes == nil || e.entities == nil {
		t.Fatal("a configure function removed a budget")
	}
	if ctx.Env().Ctx != nil {
		t.Error("the default Ctx overrode what the configure function set")
	}
}
