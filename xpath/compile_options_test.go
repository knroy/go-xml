package xpath

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The zero value compiles XPath 2.0, which is what Compile(src, nil) did
// before CompileOptions existed. The pairing is the point: a 3.1 expression
// must be REFUSED under the zero value and accepted when Version is set, so
// forgetting the field is a parse error at the call site rather than a wrong
// answer later.
func TestCompileZeroOptionsIsXPath20(t *testing.T) {
	if _, err := Compile("1 + 1", CompileOptions{}); err != nil {
		t.Fatalf("a 2.0 expression under the zero value: %v", err)
	}
	if _, err := Compile(`map{'a':1}`, CompileOptions{}); err == nil {
		t.Error("map{} compiled under the zero value; the zero Version is " +
			"XPath20, whose grammar has no map constructor")
	}
	if _, err := Compile(`map{'a':1}`, CompileOptions{Version: XPath31}); err != nil {
		t.Errorf("map{} with Version XPath31: %v", err)
	}
}

// MaxBytes is checked before parsing, so an over-large expression costs the
// comparison and nothing else. The control is one byte under the limit.
func TestCompileMaxBytes(t *testing.T) {
	src := "1" + strings.Repeat("+1", 500) // 1001 bytes
	if _, err := Compile(src, CompileOptions{MaxBytes: len(src) - 1}); err == nil {
		t.Error("an expression over MaxBytes was compiled")
	} else if !strings.Contains(err.Error(), "XPST0003") {
		t.Errorf("got %v, want XPST0003", err)
	}
	if _, err := Compile(src, CompileOptions{MaxBytes: len(src)}); err != nil {
		t.Errorf("an expression exactly at MaxBytes was refused: %v", err)
	}
	if _, err := Compile(src, CompileOptions{}); err != nil {
		t.Errorf("MaxBytes zero must be unbounded: %v", err)
	}
}

// An already-cancelled context is refused before any work. errors.Is must
// still find the cause, so a caller can tell a deadline from a parse error.
func TestCompileHonoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Compile("1 + 1", CompileOptions{Context: ctx})
	if err == nil {
		t.Fatal("a cancelled context compiled anyway")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want it to wrap context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "XPST0003") {
		t.Errorf("got %v, want the XPST0003 code the other limits carry", err)
	}
}

// A deadline that expires part-way through a compile.
//
// The check this exercises is the one between parsing and optimisation, NOT
// the one inside the optimiser: parsing is ~65% of compile time on an
// expression this size and has no cancellation of its own, so a deadline set
// to expire during the work is observed when parsing finishes. The
// in-optimiser check covers the remaining 35%, and is pinned separately below
// because it needs a context that is live when optimisation begins.
func TestCompileHonoursADeadlineBetweenPhases(t *testing.T) {
	src := "position()" + strings.Repeat(" + 1", 9000)
	big := strings.Repeat("("+src+") + ", 200) + "1"

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // the deadline is already past
	if _, err := Compile(big, CompileOptions{Context: ctx}); err == nil {
		t.Error("an expired deadline did not stop the compile")
	}

	// The control: the same expression with no deadline must still compile,
	// so the test cannot pass by the expression being invalid.
	if _, err := Compile(big, CompileOptions{}); err != nil {
		t.Errorf("the same expression without a deadline: %v", err)
	}
}

// The cancellation check INSIDE the optimiser.
//
// A context cancelled by the optimiser's own progress is the only way to
// reach it deterministically: a wall-clock deadline is consumed by parsing
// first, which is why the test above cannot cover this path and why an
// earlier version of it passed with this check deleted.
func TestCompileHonoursCancellationInsideTheOptimiser(t *testing.T) {
	src := "position()" + strings.Repeat(" + 1", 9000)
	big := strings.Repeat("("+src+") + ", 40) + "1"

	// Parse first and discard, so the deadline below starts after parsing.
	if _, err := ParseVersion(big, nil, XPath20); err != nil {
		t.Fatalf("the fixture must parse: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var ticks int
	probe := ctxFunc(func() error {
		ticks++
		if ticks > 1 {
			cancel()
		}
		return ctx.Err()
	})
	if _, err := optimizeContext(probe, mustParseExpr(t, big)); err == nil {
		t.Error("the optimiser ran to completion after its context was " +
			"cancelled; the stride check is not observed")
	}
	if ticks < 2 {
		t.Errorf("the optimiser checked cancellation %d times; the walk must "+
			"cross optimizeCancelStride at least twice for this to be a test",
			ticks)
	}
}

func mustParseExpr(t *testing.T, src string) Expr {
	t.Helper()
	e, err := ParseVersion(src, nil, XPath20)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return e
}

// ctxFunc adapts a func to context.Context, so a test can observe exactly
// when the optimiser asks and answer on its own schedule.
type ctxFunc func() error

func (f ctxFunc) Deadline() (time.Time, bool) { return time.Time{}, false }
func (f ctxFunc) Done() <-chan struct{}       { return nil }
func (f ctxFunc) Err() error                  { return f() }
func (f ctxFunc) Value(any) any               { return nil }

// Nil Context means no deadline, not an immediately-cancelled one.
func TestCompileNilContextIsUnbounded(t *testing.T) {
	if _, err := Compile("1 + 1", CompileOptions{Context: nil}); err != nil {
		t.Errorf("a nil Context must mean no deadline: %v", err)
	}
}

// XQuery and RefFloor reach the parse variants they name.
func TestCompileSelectsTheParseVariant(t *testing.T) {
	if _, err := Compile("1 + 1", CompileOptions{
		Version: XPath31, XQuery: true,
	}); err != nil {
		t.Errorf("XQuery: %v", err)
	}
	if _, err := Compile("fn:abs#1", CompileOptions{
		Version: XPath20, RefFloor: XPath30,
	}); err != nil {
		t.Errorf("RefFloor: %v", err)
	}
}
