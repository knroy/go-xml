package xpath

import (
	"context"
	"errors"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The higher-order functions loop over their input calling a function item,
// and that loop must observe Env.Ctx per call as path steps and quantified
// expressions do per item. Before, a 4M-item fold-left polled once, so a
// cancelled evaluation ran to completion. The context here answers nil for
// the first few polls -- enough to get past compile and the outer
// expression -- then reports cancellation, so an expression that polls only
// on entry finishes and one that polls per call stops. No clock is involved.
func TestHigherOrderFunctionsObserveCancellation(t *testing.T) {
	for _, src := range []string{
		`fold-left(1 to 1000, 0, function($a, $b) { $a + $b })`,
		`fold-right(1 to 1000, 0, function($a, $b) { $a + $b })`,
		`for-each(1 to 1000, function($a) { $a + 1 })`,
		`filter(1 to 1000, function($a) { $a mod 2 = 0 })`,
		`for-each-pair(1 to 1000, 1 to 1000, function($a, $b) { $a + $b })`,
		`sort(1 to 1000, (), function($a) { -$a })`,
		`array:fold-left(array { 1 to 1000 }, 0, function($a, $b) { $a + $b })`,
		`array:fold-right(array { 1 to 1000 }, 0, function($a, $b) { $a + $b })`,
		`array:for-each(array { 1 to 1000 }, function($a) { $a + 1 })`,
		`array:filter(array { 1 to 1000 }, function($a) { $a mod 2 = 0 })`,
		`array:for-each-pair(array { 1 to 1000 }, array { 1 to 1000 }, function($a, $b) { $a + $b })`,
		`array:sort(array { 1 to 1000 }, (), function($a) { -$a })`,
		`map:for-each(map:merge(for $i in 1 to 1000 return map { $i: $i }), function($k, $v) { $v })`,
	} {
		c, err := CompileWith(src, CompileOptions{Version: XPath31,
			Namespaces: testResolver{"array": xdm.NSArray, "map": xdm.NSMap}})
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		polls := 0
		gctx := ctxFunc(func() error {
			polls++
			if polls > 50 {
				return context.Canceled
			}
			return nil
		})
		ctx := NewContext(nil, Builtins()).WithEnv(func(e *Env) { e.Ctx = gctx })
		if _, err := c.Eval(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err=%v after %d polls, want context.Canceled", src, err, polls)
		}
	}
}
