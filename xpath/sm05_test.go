package xpath

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
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

// TestSM05ClosureSpendsTheCallersBudget: a function item made in one
// evaluation and called in another runs under the caller's item bound and
// node budget, not the defaults of the evaluation that made it.
func TestSM05ClosureSpendsTheCallersBudget(t *testing.T) {
	compile := func(src string) *Compiled {
		c, err := CompileWith(src, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	mk := func(src string) xdm.Sequence {
		f, err := compile(src).Eval(NewContext(nil, Builtins()).
			WithEnv(func(e *Env) { e.LibraryVersion = XPath31 }))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	call := func(f xdm.Sequence, arg string, set func(*Context) *Context) error {
		ctx := set(NewContext(nil, Builtins()).WithEnv(func(e *Env) { e.LibraryVersion = XPath31 }))
		_, err := compile("$f($a)").Eval(ctx.
			WithVar(xdm.QName{Local: "f"}, f).WithVar(xdm.QName{Local: "a"}, strSeq(arg)))
		return err
	}
	tok := mk("function($s) { count(tokenize($s)) }")
	items := func(c *Context) *Context { return c.WithEnv(func(e *Env) { e.MaxItems = 10 }) }
	wantRefused(t, call(tok, strings.Repeat("a ", 64), items), wantItems)
	if err := call(tok, "a b", items); err != nil {
		t.Fatalf("items control: %v", err)
	}
	parse := mk("function($s) { count(parse-xml($s)//*) }")
	nodes := func(c *Context) *Context {
		xpathleaf.SetLimits(c, xpathleaf.Limits{Nodes: 10})
		return c
	}
	if err := call(parse, "<r>"+strings.Repeat("<a/>", 50)+"</r>", nodes); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("parse-xml through a foreign closure under MaxNodes 10: %v, want a resource limit", err)
	}
	if err := call(parse, "<r/>", nodes); err != nil {
		t.Fatalf("nodes control: %v", err)
	}
}

// TestSM05TallBodyChargesRecursionDepth: an inline function whose recursive
// call sits under a long operator chain is charged extra depth per call, so
// chain height times call depth stays bounded; a short body is not.
func TestSM05TallBodyChargesRecursionDepth(t *testing.T) {
	run := func(body string, n int) error {
		q := "let $f := function($f, $n) { if ($n le 0) then 0 else " + body +
			" } return $f($f, " + strconv.Itoa(n) + ")"
		c, err := CompileWith(q, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Eval(NewContext(nil, Builtins()))
		return err
	}
	tall := "$f($f, $n - 1)" + strings.Repeat(" + 1", 999)
	if err := run(tall, 200); err == nil || !errors.Is(err, xdm.ErrResourceLimit) ||
		!strings.Contains(err.Error(), "XPDY0001") {
		t.Fatalf("tall body at depth 200: %v, want an XPDY0001 resource limit", err)
	}
	if err := run(tall, 20); err != nil {
		t.Fatalf("tall body at depth 20: %v", err)
	}
	if err := run("$f($f, $n - 1) + 1", 400); err != nil {
		t.Fatalf("short body at depth 400: %v", err)
	}
}
