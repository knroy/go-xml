package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// Round SM lane 05 regression tests (security audit run 5).

// TestSM05TailChargesItsCopy: fn:tail copies its argument, so the copy is
// charged to the item budget like fn:reverse's.
func TestSM05TailChargesItsCopy(t *testing.T) {
	_, err := callFn(t, nearlyExhausted(t, MaxBytes, 16), "tail", manyItems(64))
	wantRefused(t, err, wantItems)
	got, err := callFn(t, NewContext(nil, Builtins()), "tail", manyItems(3))
	if err != nil || len(got) != 2 {
		t.Fatalf("tail of 3 items = %d items, %v; want 2, nil", len(got), err)
	}
}

// TestSM05ListCastChargesItsTokens: a cast to a built-in list type reserves
// one item per token, as fn:tokenize does; castable as builds nothing.
func TestSM05ListCastChargesItsTokens(t *testing.T) {
	src := strSeq(strings.Repeat("a ", 64))
	for _, expr := range []string{"xs:NMTOKENS($s)", "$s cast as xs:NMTOKENS"} {
		c, err := Compile(expr, testNS{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Eval(sm05Ctx(t, 16).WithVar(xdm.QName{Local: "s"}, src))
		wantRefused(t, err, wantItems)
	}
	c, err := Compile("($s castable as xs:NMTOKENS, count(xs:NMTOKENS('a b')))", testNS{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Eval(sm05Ctx(t, 16).WithVar(xdm.QName{Local: "s"}, src))
	if err != nil || len(got) != 2 || got[0].(*xdm.Atomic).String() != "true" ||
		got[1].(*xdm.Atomic).String() != "2" {
		t.Fatalf("castable/control = %v, %v; want (true, 2)", got, err)
	}
}

// sm05Ctx is nearlyExhausted at the XPath 3.1 library with items items left.
func sm05Ctx(t *testing.T, items int) *Context {
	t.Helper()
	return nearlyExhausted(t, MaxBytes, items).WithVersion(XPath31).
		WithEnv(func(e *Env) { e.LibraryVersion = XPath31 })
}

// TestSM05AtomicStepAccumulationIsCharged: a path step whose per-context
// result is atomic has no dedup to bound it, so its N x N accumulation is
// charged to the item budget as it grows; a mixed one fails as soon as it is
// mixed.
func TestSM05AtomicStepAccumulationIsCharged(t *testing.T) {
	doc, err := xdm.ParseString("<r>"+strings.Repeat("<a/>", 100)+"</r>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	eval := func(src string, limit int) (xdm.Sequence, error) {
		ctx := NewContext(doc.Root, Builtins())
		if limit > 0 {
			ctx = ctx.WithEnv(func(e *Env) { e.MaxItems = limit })
		}
		return MustCompile(src, testNS{}).Eval(ctx)
	}
	_, err = eval("sum(//*/(//*!1))", 2000)
	wantRefused(t, err, wantItems)
	if got, err := eval("sum(//*/(//*!1))", 0); err != nil || got[0].(*xdm.Atomic).String() != "10201" {
		t.Fatalf("control = %v, %v; want 10201", got, err)
	}
	if _, err := eval("count(//*/(//*, 1))", 0); err == nil || !strings.Contains(err.Error(), "XPTY0018") {
		t.Fatalf("mixed step = %v; want XPTY0018", err)
	}
}

// TestSM05ImplicitCastsBoundDigits: the conversions XPath applies itself --
// general comparison, date component functions, function conversion -- are
// refused past the digit bound before the text is parsed, as a cast is.
func TestSM05ImplicitCastsBoundDigits(t *testing.T) {
	run := func(q, n string, digits int) error {
		c, err := CompileWith(q, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		ctx := NewContext(nil, Builtins()).WithEnv(func(e *Env) { e.MaxDigits = digits })
		_, err = c.Eval(ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(xdm.NewString(n))))
		return err
	}
	long := strings.Repeat("1", MaxDigits+1)
	for _, q := range []string{
		`xs:untypedAtomic('PT' || $n || 'S') = xs:dayTimeDuration('PT1S')`,
		`xs:untypedAtomic('12:00:00.' || $n) = xs:time('12:00:00')`,
		`hours-from-time(xs:untypedAtomic('12:00:00.' || $n))`,
	} {
		if err := run(q, long, 0); err == nil || !strings.Contains(err.Error(), "FOCA0003") ||
			!errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s: got %v, want FOCA0003 as a resource limit", q, err)
		}
	}
	// Function conversion honours the host's own bound.
	q := `function($d as xs:dayTimeDuration) { $d }(xs:untypedAtomic('PT' || $n || 'S'))`
	if err := run(q, strings.Repeat("1", 101), 100); err == nil || !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("function conversion: got %v, want a resource limit", err)
	}
	if err := run(q, "5", 100); err != nil {
		t.Errorf("function conversion control: %v", err)
	}
}
