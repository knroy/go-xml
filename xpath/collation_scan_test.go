package xpath

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Under a UCA collation the substring functions key one candidate span at a
// time. Before, they keyed every [start,end) span of the haystack, rebuilt the
// needle's key per span and never polled Env.Ctx, so contains of an absent
// needle was O(n^3) and outlasted any deadline. Each span now polls, which
// both makes the scan cancellable and counts its spans: with the needle keyed
// once and a span abandoned once its primary key outgrows the needle's, an
// absent one-letter needle costs a bounded number of spans per start.
func TestUCASubstringScanIsBoundedAndCancellable(t *testing.T) {
	const n = 400
	hay := strings.Repeat("a", n)
	coll := `'http://www.w3.org/2013/collation/UCA'`
	for _, src := range []string{
		`contains('` + hay + `', 'b', ` + coll + `)`,
		`starts-with('` + hay + `', 'b', ` + coll + `)`,
		`ends-with('` + hay + `', 'b', ` + coll + `)`,
		`substring-before('` + hay + `', 'b', ` + coll + `)`,
		`substring-after('` + hay + `', 'b', ` + coll + `)`,
	} {
		c, err := CompileWith(src, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatalf("%.40s: %v", src, err)
		}
		polls := 0
		count := ctxFunc(func() error { polls++; return nil })
		if _, err := c.Eval(NewContext(nil, Builtins()).WithEnv(func(e *Env) { e.Ctx = count })); err != nil {
			t.Fatalf("%.40s: %v", src, err)
		}
		if polls < 1 || polls > 20*n {
			t.Errorf("%.40s: %d spans polled, want 1..%d", src, polls, 20*n)
		}

		polls = 0
		cancel := ctxFunc(func() error {
			polls++
			if polls > 5 {
				return context.Canceled
			}
			return nil
		})
		if _, err := c.Eval(NewContext(nil, Builtins()).WithEnv(func(e *Env) { e.Ctx = cancel })); !errors.Is(err, context.Canceled) {
			t.Errorf("%.40s: err=%v after %d polls, want context.Canceled", src, err, polls)
		}
	}
}
