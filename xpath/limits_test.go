package xpath

import (
	"strings"
	"testing"
)

// Depth bounds the stack and the context deadline bounds the clock, but
// neither bounds memory. "count(1 to 9999999)" is one shallow, fast expression
// that materialised nine million heap-allocated values and peaked at 1.8 GB of
// resident memory — a denial of service in a single line of stylesheet.
//
// The item budget bounds it. These tests pin both halves: that the runaway is
// refused, and that ordinary work is not.
func TestItemBudgetStopsRunaways(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	refused := []struct{ why, expr string }{
		// A range only has to be built when something consumes the items.
		// The aggregates are answered from the bounds instead, so the cases
		// here are the ones that genuinely materialise.
		{"a range bound to a variable must be built",
			`count(for $x in 1 to 9999999 return $x)`},
		{"a nested for multiplies past the budget",
			`count(for $a in 1 to 3000, $b in 1 to 3000 return 1)`},
		{"a filtered range must be built to filter it",
			`count((1 to 100000000)[. mod 2 = 0])`},
		{"reversing a range needs every item",
			`count(reverse(1 to 9999999))`},
	}
	for _, c := range refused {
		if _, err := Eval(c.expr, ctx, nil); err == nil {
			t.Errorf("%s: %s was accepted", c.why, c.expr)
		}
	}
}

// The budget is per expression evaluation, not per transform. A stylesheet
// that evaluates a legitimate range once per node of a large document is doing
// nothing wrong, and a budget carried across all of them would refuse it part
// way through.
func TestItemBudgetResetsPerEvaluation(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	compiled, err := Compile(`count(1 to 500)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Far more items in total than the budget, but never in one evaluation.
	for i := 0; i < 20000; i++ {
		seq, err := compiled.Eval(ctx)
		if err != nil {
			t.Fatalf("evaluation %d failed: %v — the budget is accumulating "+
				"across evaluations instead of resetting", i, err)
		}
		if len(seq) != 1 {
			t.Fatalf("evaluation %d returned %d items", i, len(seq))
		}
	}
}

// fn:count over a bare range is answered from the bounds, because the
// cardinality of "lo to hi" is hi - lo + 1 and the items are never needed.
// Without this, count(1 to 10000000) — which the W3C suite tests and Saxon
// answers instantly — would have to allocate ten million values to discard
// them. Every expectation here matches Saxon-HE 12.4.
func TestCountOfRangeIsComputed(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	cases := []struct{ expr, want string }{
		{`count(1 to 10000000)`, "10000000"},
		{`count(1 to 10)`, "10"},
		{`count(5 to 5)`, "1"},
		// A descending range is empty, not reversed.
		{`count(10 to 1)`, "0"},
		{`count(() to 5)`, "0"},
		{`count(5 to ())`, "0"},
		{`count(-3 to 3)`, "7"},
	}
	for _, c := range cases {
		seq, err := Eval(c.expr, ctx, nil)
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

// The shortcut must apply only where the cardinality really is the range
// length. A predicate, a for, or any other wrapper changes how many items
// survive, so those take the ordinary path and meet the budget.
func TestCountShortcutIsNarrow(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	// A predicate halves the range: the answer proves the items were built.
	seq, err := Eval(`count((1 to 10)[. mod 2 = 0])`, ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := seq[0].(interface{ String() string }).String(); got != "5" {
		t.Errorf("count over a filtered range = %q, want 5", got)
	}
	// And the same shape past the budget must be refused rather than
	// shortcut to a wrong answer.
	if _, err := Eval(`count((1 to 100000000)[. mod 2 = 0])`, ctx, nil); err == nil {
		t.Error("a filtered huge range was accepted; the shortcut is too broad")
	}
}

// The error must say what happened, since a user meeting it needs to know the
// expression is too large rather than wrong.
func TestItemBudgetErrorIsClear(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	_, err := Eval(`count(for $a in 1 to 3000, $b in 1 to 3000 return 1)`, ctx, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"XPDY0130", "items"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// An integer range is fully determined by its bounds, so every aggregate over
// one is arithmetic rather than iteration: count is hi-lo+1, sum is the
// arithmetic series n(first+last)/2, min and max are the bounds themselves.
// All expectations match Saxon-HE 12.4 where Saxon can evaluate them.
func TestRangeAggregatesAreComputed(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	cases := []struct{ expr, want string }{
		{`count(1 to 10000000)`, "10000000"},
		{`sum(1 to 10000000)`, "50000005000000"},
		{`min(1 to 10000000)`, "1"},
		{`max(1 to 10000000)`, "10000000"},
		{`avg(1 to 10000000)`, "5000000.5"},

		// Small ranges must agree with what iteration would produce.
		{`sum(1 to 10)`, "55"},
		{`sum(-5 to 5)`, "0"},
		{`sum(2 to 2)`, "2"},
		{`avg(1 to 2)`, "1.5"},
		{`min(-3 to 3)`, "-3"},

		// An empty range: fn:sum returns 0, the others the empty sequence.
		{`sum(10 to 1)`, "0"},
		{`count(min(10 to 1))`, "0"},
		{`count(max(10 to 1))`, "0"},
		{`count(avg(10 to 1))`, "0"},
		{`sum(() to 5)`, "0"},
	}
	for _, c := range cases {
		got := evalOne(t, ctx, c.expr)
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// n(first+last) overflows int64 well before the bounds do, so the series is
// summed in big.Int. Truncating would be silently wrong rather than an error,
// since xs:integer is arbitrary-precision here.
func TestRangeSumExceedsInt64(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	// 5e12 * (5e12+1) / 2 is about 1.25e25, far past int64's 9.2e18.
	const want = "12500000000002500000000000"
	if got := evalOne(t, ctx, `sum(1 to 5000000000000)`); got != want {
		t.Errorf("sum(1 to 5000000000000) = %q, want %q", got, want)
	}
	// A range whose *bounds* fit int64 but whose sum does not.
	if got := evalOne(t, ctx, `sum(1 to 4000000000)`); got != "8000000002000000000" {
		t.Errorf("sum(1 to 4000000000) = %q, want 8000000002000000000", got)
	}
}

// The shortcut applies only where the cardinality really is the range length.
// Anything that changes which items survive must take the ordinary path, or
// the answer would be confidently wrong rather than merely slow.
func TestRangeAggregateShortcutIsNarrow(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	cases := []struct{ why, expr, want string }{
		{"a predicate halves the range", `sum((1 to 10)[. mod 2 = 0])`, "30"},
		{"a for-expression maps the values", `sum(for $x in 1 to 10 return $x * 2)`, "110"},
		// A comma sequence of two ranges is not itself a range. (A union
		// would not even type-check: "|" requires nodes, as Saxon agrees.)
		{"two ranges joined are not one range", `count((1 to 5, 4 to 8))`, "10"},
		{"an aggregate of an aggregate", `sum((count(1 to 10), count(1 to 5)))`, "15"},
	}
	for _, c := range cases {
		if got := evalOne(t, ctx, c.expr); got != c.want {
			t.Errorf("%s: %s = %q, want %q", c.why, c.expr, got, c.want)
		}
	}
}

func evalOne(t *testing.T, ctx *Context, expr string) string {
	t.Helper()
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		t.Errorf("%s: %v", expr, err)
		return ""
	}
	if len(seq) != 1 {
		return ""
	}
	return seq[0].(interface{ String() string }).String()
}
