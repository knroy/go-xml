package xpath

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The four positional spellings must keep answering exactly as they did, now
// that each delegates to CompileWith. This is the compatibility half of the
// change: the module is v1, whose contract is that the exported API is stable,
// so CompileWith is additive and these four are unchanged.
func TestCompileWrappersMatchCompileWith(t *testing.T) {
	ns := testResolver{"p": "urn:p"}
	for _, tc := range []struct {
		name string
		old  func() (*Compiled, error)
		with CompileOptions
		src  string
	}{
		{"Compile", func() (*Compiled, error) { return Compile("1 + 1", ns) },
			CompileOptions{Namespaces: ns}, "1 + 1"},
		{"CompileVersion", func() (*Compiled, error) {
			return CompileVersion(`map{'a':1}`, ns, XPath31)
		}, CompileOptions{Namespaces: ns, Version: XPath31}, `map{'a':1}`},
		{"CompileXQuery", func() (*Compiled, error) {
			return CompileXQuery("1 + 1", ns, XPath31)
		}, CompileOptions{Namespaces: ns, Version: XPath31, XQuery: true}, "1 + 1"},
		{"CompileVersionRefFloor", func() (*Compiled, error) {
			return CompileVersionRefFloor("fn:abs#1", ns, XPath20, XPath30)
		}, CompileOptions{Namespaces: ns, Version: XPath20, RefFloor: XPath30},
			"fn:abs#1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, aerr := tc.old()
			b, berr := CompileWith(tc.src, tc.with)
			if (aerr == nil) != (berr == nil) {
				t.Fatalf("%s err=%v, CompileWith err=%v", tc.name, aerr, berr)
			}
			if aerr != nil {
				return
			}
			if a.Source() != b.Source() || a.version != b.version {
				t.Errorf("%s gave src=%q v=%v, CompileWith gave src=%q v=%v",
					tc.name, a.Source(), a.version, b.Source(), b.version)
			}
		})
	}
}

// The zero value compiles XPath 2.0, matching Compile(src, nil). The pairing
// is the point: 3.1 syntax must be REFUSED under the zero value and accepted
// when Version is set, so forgetting the field is a parse error at the call
// site rather than a wrong answer later.
func TestCompileWithZeroOptionsIsXPath20(t *testing.T) {
	if _, err := CompileWith("1 + 1", CompileOptions{}); err != nil {
		t.Fatalf("a 2.0 expression under the zero value: %v", err)
	}
	if _, err := CompileWith(`map{'a':1}`, CompileOptions{}); err == nil {
		t.Error("map{} compiled under the zero value; the zero Version is " +
			"XPath20, whose grammar has no map constructor")
	}
	if _, err := CompileWith(`map{'a':1}`, CompileOptions{Version: XPath31}); err != nil {
		t.Errorf("map{} with Version XPath31: %v", err)
	}
}

// MaxBytes is checked before parsing. The control is exactly at the limit.
func TestCompileWithMaxBytes(t *testing.T) {
	src := "1" + strings.Repeat("+1", 500)
	if _, err := CompileWith(src, CompileOptions{MaxBytes: len(src) - 1}); err == nil {
		t.Error("an expression over MaxBytes was compiled")
	} else if !strings.Contains(err.Error(), "XPST0003") {
		t.Errorf("got %v, want XPST0003", err)
	}
	if _, err := CompileWith(src, CompileOptions{MaxBytes: len(src)}); err != nil {
		t.Errorf("an expression exactly at MaxBytes was refused: %v", err)
	}
	if _, err := CompileWith(src, CompileOptions{}); err != nil {
		t.Errorf("MaxBytes zero must be unbounded: %v", err)
	}
}

// An already-cancelled context is refused before any work, and errors.Is must
// still find the cause so a caller can tell a deadline from a parse error.
func TestCompileWithHonoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CompileWith("1 + 1", CompileOptions{Context: ctx})
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

// A deadline already past is observed between parsing and optimisation.
//
// This does NOT reach the check inside the optimiser: parsing is roughly two
// thirds of the cost on an expression this size and has no cancellation of
// its own, so an expired deadline is always caught when parsing finishes. An
// earlier version of this test was the only cover for the in-optimiser check
// and passed with that check deleted; the test below is the one that reaches it.
func TestCompileWithHonoursADeadlineBetweenPhases(t *testing.T) {
	src := "position()" + strings.Repeat(" + 1", 9000)
	big := strings.Repeat("("+src+") + ", 200) + "1"

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)
	if _, err := CompileWith(big, CompileOptions{Context: ctx}); err == nil {
		t.Error("an expired deadline did not stop the compile")
	}
	// The control: the same expression with no deadline must still compile,
	// so the test cannot pass by the expression being invalid.
	if _, err := CompileWith(big, CompileOptions{}); err != nil {
		t.Errorf("the same expression without a deadline: %v", err)
	}
}

// The cancellation check INSIDE the optimiser.
//
// A context cancelled by the optimiser's own progress is the only way to
// reach it deterministically, for the reason given above.
func TestCompileWithHonoursCancellationInsideTheOptimiser(t *testing.T) {
	src := "position()" + strings.Repeat(" + 1", 9000)
	big := strings.Repeat("("+src+") + ", 40) + "1"

	e, err := ParseVersion(big, nil, XPath20)
	if err != nil {
		t.Fatalf("the fixture must parse: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ticks int
	probe := ctxFunc(func() error {
		ticks++
		if ticks > 1 {
			cancel()
		}
		return ctx.Err()
	})
	if _, err := optimizeContext(probe, e); err == nil {
		t.Error("the optimiser ran to completion after its context was " +
			"cancelled; the stride check is not observed")
	}
	if ticks < 2 {
		t.Errorf("the optimiser checked cancellation %d times; the walk must "+
			"cross optimizeCancelStride at least twice for this to be a test",
			ticks)
	}
}

// Nil Context means no deadline, not an immediately-cancelled one, and costs
// no allocation.
func TestCompileWithNilContextIsUnbounded(t *testing.T) {
	if _, err := CompileWith("1 + 1", CompileOptions{Context: nil}); err != nil {
		t.Errorf("a nil Context must mean no deadline: %v", err)
	}
}

// ctxFunc adapts a func to context.Context, so a test can observe exactly
// when the optimiser asks and answer on its own schedule.
type ctxFunc func() error

func (f ctxFunc) Deadline() (time.Time, bool) { return time.Time{}, false }
func (f ctxFunc) Done() <-chan struct{}       { return nil }
func (f ctxFunc) Err() error                  { return f() }
func (f ctxFunc) Value(any) any               { return nil }
