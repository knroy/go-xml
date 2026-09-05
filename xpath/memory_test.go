package xpath

import (
	"fmt"
	"runtime"
	"testing"
)

// The regex cache is process-wide and was originally unbounded, on the
// reasoning that patterns come from stylesheets and so form a fixed set. That
// is wrong: "matches($s, $node/@pattern)" compiles a pattern taken from
// document data, so a long-running process would retain one compiled regexp
// per distinct pattern it had ever seen — measured at 17.6 MB after 20,000
// patterns and still climbing.
//
// This pins the bound. It measures the heap, so it is inherently a little
// noisy; the threshold is set far above the expected value (a few hundred KB)
// and far below the unbounded behaviour, so it fails on a real regression
// rather than on GC timing.
func TestRegexCacheIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates; skipped under -short")
	}
	ctx := NewContext(nil, Builtins())

	// Warm up, so first-use allocations are not counted as growth.
	for i := 0; i < 100; i++ {
		mustEval(t, ctx, fmt.Sprintf(`matches('abc', 'warm%db')`, i))
	}
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)

	const patterns = 20000
	for i := 0; i < patterns; i++ {
		mustEval(t, ctx, fmt.Sprintf(`matches('abc', 'a%db')`, i))
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m1)

	growthMB := (float64(m1.HeapAlloc) - float64(m0.HeapAlloc)) / (1 << 20)
	const limitMB = 6.0
	if growthMB > limitMB {
		t.Errorf("heap grew %.1f MB over %d distinct patterns, limit %.1f MB — "+
			"the regex cache is not being bounded", growthMB, patterns, limitMB)
	}
}

// A cleared cache must still return correct results: every entry is
// reproducible from its key, so correctness cannot depend on a hit.
func TestRegexCacheStaysCorrectWhenCleared(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	// Push well past the bound so at least one clear happens, checking a
	// stable pattern's answer throughout.
	for i := 0; i < regexCacheMax*3; i++ {
		mustEval(t, ctx, fmt.Sprintf(`matches('abc', 'x%dy')`, i))
		if i%500 == 0 {
			if got := mustEval(t, ctx, `matches('abc', '^a.c$')`); got != "true" {
				t.Fatalf("stable pattern gave %q after %d insertions", got, i)
			}
		}
	}
	if got := mustEval(t, ctx, `matches('abc', '^a.c$')`); got != "true" {
		t.Errorf("stable pattern gave %q after the cache filled", got)
	}
}

func mustEval(t *testing.T, ctx *Context, expr string) string {
	t.Helper()
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	if len(seq) == 0 {
		return ""
	}
	return seq[0].(interface{ String() string }).String()
}

// charAtomCache has the same shape and the same failure mode as the regex
// cache: it is process-wide, and its keys are not a fixed set drawn from the
// stylesheet, because a pattern taken from document data mints an atom per
// distinct single-character construct it contains.
//
// This counts entries rather than measuring the heap. A char atom is small,
// so heap growth over a few thousand of them is within GC noise; the map size
// is the thing the bound actually controls, and it is exact.
func TestCharAtomCacheIsBounded(t *testing.T) {
	withBacktracking(t, func() {
		// Each atom is a distinct one-character class, so the pattern below
		// contributes one new cache key per iteration. The backreference is
		// what routes the pattern to the backtracking engine at all.
		const atoms = charAtomCacheMax * 3
		for i := 0; i < atoms; i++ {
			pat := fmt.Sprintf("([a-%c])\\1", rune(0x0100+i))
			if _, err := CompileRegexp(pat, ""); err != nil {
				t.Fatalf("%s: %v", pat, err)
			}
		}
		n := 0
		charAtomCache.Range(func(_, _ any) bool { n++; return true })
		if n > charAtomCacheMax {
			t.Errorf("char atom cache holds %d entries after %d distinct atoms, "+
				"bound is %d — the cache is not being bounded",
				n, atoms, charAtomCacheMax)
		}
	})
}

// A cleared char atom cache must still compile and match correctly: every
// entry is reproducible from its key, so no answer may depend on a hit.
func TestCharAtomCacheStaysCorrectWhenCleared(t *testing.T) {
	withBacktracking(t, func() {
		check := func(after int) {
			t.Helper()
			re, err := CompileRegexp(`([a-c])x\1`, "")
			if err != nil {
				t.Fatalf("stable pattern failed to compile after %d insertions: %v", after, err)
			}
			if !re.MatchString("bxb") || re.MatchString("bxc") {
				t.Fatalf("stable pattern gave the wrong answer after %d insertions", after)
			}
		}
		for i := 0; i < charAtomCacheMax*3; i++ {
			pat := fmt.Sprintf("([a-%c])\\1", rune(0x2000+i))
			if _, err := CompileRegexp(pat, ""); err != nil {
				t.Fatalf("%s: %v", pat, err)
			}
			if i%500 == 0 {
				check(i)
			}
		}
		check(charAtomCacheMax * 3)
	})
}
