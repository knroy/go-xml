package xpath

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
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
		n := charAtomCache.Len()
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

// ---------------------------------------------------------------------------
// Concurrent bound tests
// ---------------------------------------------------------------------------

// The three sequential bound tests above — TestRegexCacheIsBounded,
// TestCharAtomCacheIsBounded and their collation equivalent — drive each cache
// from a single goroutine, and they passed against code whose bound did not
// hold at all under concurrency. That is the reason both kinds of test exist,
// and the reason these were added.
//
// The original idiom checked an atomic size counter, cleared the map, and
// inserted, as three separate steps against a sync.Map. Concurrent callers
// interleaved between the check and the insert, so entries accumulated past the
// bound: measured at a peak of 1655 live entries against charAtomCacheMax=1024
// with 200 concurrent callers, and 3454 with 800. The overshoot scaled with the
// number of goroutines in flight rather than with the volume of input, so it was
// a violated bound and not unbounded growth — an attacker could not enlarge it
// by sending more data. A bound that holds only on one goroutine is still not a
// bound, which is what these tests pin.
//
// Each test samples the cache size *while* the writers are running, not only
// after they finish. The overshoot is a transient peak: a post-hoc count lands
// wherever the last clear left the map and routinely reads below the bound even
// when the bound was breached moments earlier. A test that only counted at the
// end could not observe the defect it exists to catch.

// concurrentCachePeak runs write concurrently across many goroutines while
// sampling the cache size, and returns the largest size observed at any
// instant.
//
// The writers all wait on one channel so they are released together, which is
// what produces genuine contention rather than a staggered sequence. Several
// sampler goroutines run in parallel: a single sampler observed the size only
// about half as often, and the peak being caught here is transient.
func concurrentCachePeak(goroutines, each int, size func() int, write func(g, i int)) int {
	var stop atomic.Bool
	var peak atomic.Int64
	var samplers sync.WaitGroup
	for s := 0; s < 4; s++ {
		samplers.Add(1)
		go func() {
			defer samplers.Done()
			for !stop.Load() {
				n := int64(size())
				for {
					old := peak.Load()
					if n <= old || peak.CompareAndSwap(old, n) {
						break
					}
				}
			}
		}()
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < each; i++ {
				write(g, i)
			}
		}(g)
	}
	close(start)
	wg.Wait()
	stop.Store(true)
	samplers.Wait()
	return int(peak.Load())
}

// The regex cache must hold its bound when many goroutines compile distinct
// patterns at once. Each goroutine uses a disjoint key range, so the writers
// contend on the cache without deduplicating each other's work.
func TestRegexCacheIsBoundedConcurrently(t *testing.T) {
	const goroutines = 200
	const each = 60
	peak := concurrentCachePeak(goroutines, each, regexCache.Len, func(g, i int) {
		// Compiling through the public entry point exercises the same store
		// path the evaluator uses.
		CompileRegexp(fmt.Sprintf("conc%d_%dz", g, i), "")
	})
	if peak > regexCacheMax {
		t.Errorf("regex cache peaked at %d entries under %d concurrent writers, "+
			"bound is %d — the bound does not hold under concurrency",
			peak, goroutines, regexCacheMax)
	}
}

// The char atom cache has the same shape and had the same defect. It is only
// reachable with the backtracking engine on, which is what withBacktracking
// arranges.
func TestCharAtomCacheIsBoundedConcurrently(t *testing.T) {
	withBacktracking(t, func() {
		const goroutines = 200
		const each = 60
		peak := concurrentCachePeak(goroutines, each, charAtomCache.Len, func(g, i int) {
			// One distinct single-character class per iteration; the
			// backreference is what routes the pattern to the backtracking
			// engine, and so to the char atom cache, at all.
			CompileRegexp(fmt.Sprintf("([a-%c])\\1", rune(0x3000+g*each+i)), "")
		})
		if peak > charAtomCacheMax {
			t.Errorf("char atom cache peaked at %d entries under %d concurrent writers, "+
				"bound is %d — the bound does not hold under concurrency",
				peak, goroutines, charAtomCacheMax)
		}
	})
}

// The UCA collation cache is smaller (256) and so crosses its bound sooner.
// Every URI here is unsupported and therefore fails to parse; that is
// deliberate, because failures are memoised too and the failure path is the one
// a stylesheet naming a bad collation from document data would drive.
func TestUCACacheIsBoundedConcurrently(t *testing.T) {
	const goroutines = 200
	const each = 30
	peak := concurrentCachePeak(goroutines, each, ucaCache.Len, func(g, i int) {
		ResolveCollation(fmt.Sprintf("%s?lang=zz-conc-%d-%d", UCACollation, g, i))
	})
	if peak > ucaCacheMax {
		t.Errorf("UCA collation cache peaked at %d entries under %d concurrent writers, "+
			"bound is %d — the bound does not hold under concurrency",
			peak, goroutines, ucaCacheMax)
	}
}
