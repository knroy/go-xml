package xquery

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// evalQ runs src on a fresh context and returns the sequence and the error.
func evalQ(t *testing.T, src string) (xdm.Sequence, error) {
	t.Helper()
	return Eval(src, xpath.NewContext(nil, xpath.Builtins()), Options{})
}

// wantRefused asserts that err is the item-budget refusal, with the code and
// the sentinel a caller distinguishes it by.
func wantRefused(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected XPDY0130; the documented MaxItems budget did not " +
			"bind on this path")
	}
	if code := xdm.ErrorCode(err); code != "XPDY0130" {
		t.Errorf("code = %q, want XPDY0130 (error: %v)", code, err)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell a refusal to allocate from a bad query", err)
	}
	if !strings.Contains(err.Error(), "materialised more than") {
		t.Errorf("message %q is not the item-budget refusal", err)
	}
}

// A FLWOR is parsed and evaluated by this package rather than by xpath, so
// its tuple stream never reached xpath's accumulating constructs and the
// documented MaxItems budget was not applied to it: the query below built
// 2,250,000 tuples and returned them without complaint, while the very same
// expression wrapped in count() -- which routes the whole thing through
// xpath instead -- was refused. The budget must bind on both.
func TestBareFLWORReachesTheItemBudget(t *testing.T) {
	_, err := evalQ(t, "for $a in 1 to 1500, $b in 1 to 1500 return $a")
	wantRefused(t, err)
}

// The wrapped form was already refused, and must stay refused: the fix moves
// the boundary the budget is measured over, and moving it must not lose the
// case that worked.
func TestWrappedFLWORStillReachesTheItemBudget(t *testing.T) {
	_, err := evalQ(t, "count(for $a in 1 to 1500, $b in 1 to 1500 return $a)")
	wantRefused(t, err)
}

// The two forms are the same expression, so they must agree: whatever the
// budget decides, it decides for both. A limit that fires on one wrapping and
// not the other is the defect restated.
func TestBudgetAgreesBetweenTheBareAndWrappedForms(t *testing.T) {
	for _, q := range []string{
		"for $a in 1 to 1500, $b in 1 to 1500 return $a",
		"for $a in 1 to 900, $b in 1 to 900 return $a",
		"for $x in 1 to 500 return $x",
	} {
		_, bare := evalQ(t, q)
		_, wrapped := evalQ(t, "count("+q+")")
		if (bare != nil) != (wrapped != nil) {
			t.Errorf("%q: bare err = %v but count(...) err = %v; the same "+
				"expression must reach the same verdict either way",
				q, bare, wrapped)
		}
	}
}

// A query comfortably under the budget must still run. The hazard on this
// side of the fix is a boundary drawn so tight that legitimate work is
// refused -- 810,000 items is well inside a five-million allowance.
func TestQueryUnderTheBudgetStillSucceeds(t *testing.T) {
	seq, err := evalQ(t, "for $a in 1 to 900, $b in 1 to 900 return $a")
	if err != nil {
		t.Fatalf("a query well inside the budget was refused: %v", err)
	}
	if len(seq) != 810000 {
		t.Errorf("len = %d, want 810000", len(seq))
	}
}

// The budget bounds ONE evaluation, not the lifetime of a context. A counter
// armed but never reset would let a long-lived context accumulate across
// independent queries and start refusing valid ones after enough use, which
// is a worse bug than the one being fixed: it is a denial of service that
// arrives on legitimate traffic.
//
// Each iteration here materialises about 250,000 items; twenty of them is
// five million, so a context that leaked would fail well before the end.
func TestSequentialQueriesOnOneContextDoNotAccumulate(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	const q = "for $a in 1 to 500, $b in 1 to 500 return $a"
	for i := 0; i < 40; i++ {
		seq, err := Eval(q, ctx, Options{})
		if err != nil {
			t.Fatalf("query %d of 40 on a reused context was refused: %v; "+
				"the budget is leaking across evaluations", i+1, err)
		}
		if len(seq) != 250000 {
			t.Fatalf("query %d: len = %d, want 250000", i+1, len(seq))
		}
	}
}

// The reverse hazard: a budget reset so often that it never binds. One query
// evaluated many times must still be refused every time -- if the refusal
// came only from residue left by an earlier run, the first would pass.
func TestRefusalIsReproducibleOnAReusedContext(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	const q = "for $a in 1 to 1500, $b in 1 to 1500 return $a"
	for i := 0; i < 3; i++ {
		_, err := Eval(q, ctx, Options{})
		wantRefused(t, err)
	}
}

// A refused query must not poison the context it ran on: the budget is reset
// when the next evaluation arms it, so a small query after a refused one
// succeeds.
func TestASmallQueryAfterARefusedOneSucceeds(t *testing.T) {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	if _, err := Eval("for $a in 1 to 1500, $b in 1 to 1500 return $a",
		ctx, Options{}); err == nil {
		t.Fatal("expected the large query to be refused")
	}
	seq, err := Eval("for $x in 1 to 10 return $x", ctx, Options{})
	if err != nil {
		t.Fatalf("a ten-item query after a refused one was refused too: %v; "+
			"the failed evaluation left its charges on the context", err)
	}
	if len(seq) != 10 {
		t.Errorf("len = %d, want 10", len(seq))
	}
}

// A "let" that names a large sequence repeatedly is the other shape of the
// same runaway: the tuple stream stays short, but the return expression
// concatenates a big sequence per tuple. Charging only the stream would miss
// it.
func TestReturnConcatenationIsCharged(t *testing.T) {
	_, err := evalQ(t,
		"for $a in 1 to 3000 let $s := 1 to 3000 return $s")
	wantRefused(t, err)
}
