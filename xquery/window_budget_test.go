package xquery

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/knroy/go-xml/v2/xpath"
)

// limitedQuery runs src with a MaxItems of limit.
func limitedQuery(src string, limit int) error {
	ctx := xpath.NewContext(nil, xpath.Builtins()).
		WithEnv(func(e *xpath.Env) { e.MaxItems = limit })
	_, err := Eval(src, ctx, Options{})
	return err
}

// A sliding window copies its items once per start, so a window that never
// closes holds n(n+1)/2 items while only the binding sequence and the tuple
// count were charged: 300 items made 45,150 under a budget of 10,000.
func TestSlidingWindowCopiesReachTheItemBudget(t *testing.T) {
	wantRefused(t, limitedQuery(`sum(for sliding window $w in 1 to 300 `+
		`start when true() end when false() return count($w))`, 10000))
	if err := limitedQuery(`sum(for sliding window $w in 1 to 300 `+
		`start when true() end when false() return count($w))`, 50000); err != nil {
		t.Fatalf("a window query inside the budget was refused: %v", err)
	}
}

// A "let" before a "for" binds one slice into every tuple, and group by
// concatenated that value across the members uncharged: 300 x 300 = 90,000
// items under a budget of 10,000.
func TestGroupByMergedBindingsReachTheItemBudget(t *testing.T) {
	const q = `let $big := 1 to 300 for $i in 1 to 300 ` +
		`group by $k := 0 return count($big)`
	wantRefused(t, limitedQuery(q, 10000))
	if err := limitedQuery(q, 100000); err != nil {
		t.Fatalf("a group by inside the budget was refused: %v", err)
	}
}

// countdownCtx reports cancellation once Err has been asked n times, so a
// test can cancel mid-evaluation without depending on a clock.
type countdownCtx struct {
	context.Context
	n atomic.Int64
}

func (c *countdownCtx) Err() error {
	if c.n.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// "only end" with an end that never fires scans to the end of the sequence
// from every start and emits nothing, which is quadratic work no item charge
// sees. The scan must observe cancellation.
func TestWindowBoundaryScanObservesCancellation(t *testing.T) {
	for _, q := range []string{
		`for sliding window $w in 1 to 2000 start when true() ` +
			`only end when false() return 1`,
		`for tumbling window $w in 1 to 2000 start when true() ` +
			`only end when false() return 1`,
	} {
		cc := &countdownCtx{Context: context.Background()}
		cc.n.Store(1000)
		ctx := xpath.NewContext(nil, xpath.Builtins()).
			WithEnv(func(e *xpath.Env) { e.Ctx = cc })
		_, err := Eval(q, ctx, Options{})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", q, err)
		}
	}
}
