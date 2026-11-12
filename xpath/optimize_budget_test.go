package xpath

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Constant folding evaluates closed sub-expressions at compile time, so it
// must be bounded like evaluation is -- and bounded per compilation, not per
// fold. Each fold minted its own 5M-item budget and ignored the compile
// deadline, so twenty "1 to 999999" terms in 241 bytes compiled under a 20 ms
// Context in 551 ms and allocated 2.8 GB, materialising ranges that could
// never fold to a literal, then refused at evaluation. The dead-branch form
// paid the same at compile for an expression that never evaluates.
func TestFoldingIsBoundedPerCompilation(t *testing.T) {
	terms := strings.TrimSuffix(strings.Repeat("1 to 999999,", 20), ",")
	// A foldable call over the range materialises it however the range
	// itself is treated, so this one is bounded by the shared budget alone.
	calls := strings.TrimSuffix(strings.Repeat("count(reverse(1 to 999999)),", 20), ",")
	for _, src := range []string{
		"(" + terms + ")",
		"if (false()) then (" + terms + ") else ()",
		"(" + calls + ")",
	} {
		// The deadline only has to outlast a bounded compile on a loaded CI
		// runner; the allocation check below is what tells bounded from
		// materialised (the unbounded fold allocated 2.8 GB).
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, err := CompileWith(src, CompileOptions{Context: ctx, MaxBytes: 4096})
		runtime.ReadMemStats(&after)
		cancel()
		if err != nil {
			t.Errorf("%.40s...: compile failed: %v", src, err)
		}
		if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb >= 50 {
			t.Errorf("%.40s...: compiling allocated %d MB; folding is "+
				"materialising ranges that cannot fold", src, mb)
		}
	}

	// A range between two different literal bounds is never a singleton,
	// so it is not evaluated at all: no fold context is ever built for it.
	e, err := Parse("1 to 4000", nil)
	if err != nil {
		t.Fatal(err)
	}
	f := newExprFacts()
	optimizeDepthFacts(e, 0, f)
	if f.fold != nil {
		t.Error("1 to 4000 was evaluated for folding; a range of two " +
			"different literal bounds cannot fold to a literal")
	}

	// What folds today still folds: a singleton range, and an aggregate over
	// a range, which is answered arithmetically without materialising it.
	for _, src := range []string{"5 to 5", "count(1 to 999999)", "sum(1 to 100)"} {
		e, err := Parse(src, nil)
		if err != nil {
			t.Fatalf("Parse(%q): %v", src, err)
		}
		if _, ok := optimize(e).(*Literal); !ok {
			t.Errorf("%q no longer folds to a literal", src)
		}
	}
}
