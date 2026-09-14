package xpath

import (
	"strings"
	"testing"
	"time"
)

// quadraticSrc builds an expression whose optimiser cost was quadratic in its
// size before isClosed memoised its answer.
//
// Two properties are needed, and both are worked around rather than accidental:
//
//   - The spine must be LEFT-leaning and long, so that the left child of each
//     node is the whole chain so far and a per-node subtree scan re-reads the
//     entire prefix. "a+b+c" parses that way.
//   - The chain must stay unfoldable, or folding collapses the prefix to a
//     single literal and there is nothing left to rescan. A non-closed atom at
//     the BASE of the spine does it, because isClosed descends to the base
//     before it can answer and so never short-circuits early.
//
// Neither existing guard bounds this. maxChainLength caps one chain at 10000
// terms for stack safety, and paren nesting is capped at 1000 -- so the
// expression is built as a composition of sub-limit chains, which stays under
// both while the tree grows without bound. That is the point: the guards bound
// LENGTH, not WORK, and work is what the DoS spends.
func quadraticSrc(target int) string {
	const inner = 2000
	var term strings.Builder
	term.WriteString("(position()")
	for i := 0; i < inner; i++ {
		term.WriteString("+1")
	}
	term.WriteString(")")
	t := term.String()

	var b strings.Builder
	b.WriteString(t)
	for b.Len() < target {
		b.WriteString("+")
		b.WriteString(t)
	}
	return b.String()
}

// TestOptimizeNotQuadratic bounds the compile time of a large expression.
//
// It matters beyond compile latency because the expression need not be written
// by whoever wrote the stylesheet: xsl:evaluate compiles its target expression
// at transform time, from a string that can come out of the document being
// transformed. A quadratic compile there is a denial of service driven by
// input data.
//
// Measured on this machine: 160 kB took 736 ms before the fix and 64 ms after,
// and 640 kB took 2.79 s before and 251 ms after, with isClosed at 86% of CPU
// samples beforehand. The bound below is many times the post-fix figure so
// that a loaded machine does not fail it, and far under the pre-fix one so
// that a return to quadratic behaviour fails it decisively.
func TestOptimizeNotQuadratic(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	// The race detector's instrumentation made the same compile take 1.7 s
	// on the gate's race lane, so the budget is widened there by more than
	// the slowdown; the pre-fix figure under it would be near 20 s.
	budget := 1500 * time.Millisecond
	if raceEnabled {
		budget *= 6
	}

	src := quadraticSrc(640 * 1024)
	start := time.Now()
	_, err := Compile(src, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if elapsed > budget {
		t.Errorf("compiling %d bytes took %v, over the %v budget.\n"+
			"A whole-subtree predicate is being re-walked per node again, "+
			"which is O(n^2) in expression size and reachable from document "+
			"data through xsl:evaluate.", len(src), elapsed.Round(time.Millisecond), budget)
	}
}

// TestOptimizeFoldingUnchangedByCache is the correctness half, and it is the
// half that matters more: isClosed decides whether constant folding is legal,
// so a wrong answer there folds an expression that depends on the focus and
// produces a silently wrong result, which is far worse than a slow compile.
//
// Memoising is only sound if the predicate is a pure function of the subtree.
// These cases pin that: each pairs an expression that must fold with one that
// must not for a reason the cache could plausibly erase -- the same
// subexpression appearing in both a closed and a non-closed position, and a
// focus-dependent atom repeated where an earlier identical-looking node was
// already answered.
func TestOptimizeFoldingUnchangedByCache(t *testing.T) {
	folds := []string{
		"1 + 2",
		"(1 + 2) * (3 + 4)",
		"count((1, 2, 3))",
		"string-length('abc')",
		"abs(-5) + abs(-5)",
	}
	for _, src := range folds {
		e, err := Parse(src, nil)
		if err != nil {
			t.Fatalf("Parse(%q): %v", src, err)
		}
		if _, ok := optimize(e).(*Literal); !ok {
			t.Errorf("%q did not fold to a literal; the cache has made a "+
				"closed expression look non-closed", src)
		}
	}

	// None of these may fold: each depends on the dynamic context somewhere,
	// and the closed-looking parts around it must not carry the whole
	// expression over the line.
	refuses := []string{
		"position()",
		"1 + position()",
		"position() + 1",
		"(1 + 2) + position()",
		"abs(-5) + position()",
		"count((1, 2, position()))",
		"string-length(string(.))",
		"$v + 1",
	}
	for _, src := range refuses {
		e, err := Parse(src, nil)
		if err != nil {
			t.Fatalf("Parse(%q): %v", src, err)
		}
		if lit, ok := optimize(e).(*Literal); ok {
			t.Errorf("%q FOLDED to %v. A focus- or variable-dependent "+
				"expression was frozen at compile time; every evaluation now "+
				"sees the empty-context answer.", src, lit.Val)
		}
	}
}

// TestIsClosedCacheAgreesWithUncached checks the memo against the walk it
// replaces, over every subexpression of a mixed tree, so that agreement is
// asserted node by node rather than only at the root.
func TestIsClosedCacheAgreesWithUncached(t *testing.T) {
	srcs := []string{
		"1 + 2 + 3 + position() + 4 + 5",
		"count((1, 2, 3)) + string-length('ab') + position()",
		"(1 + 2) * (3 + position()) + abs(-1)",
		"if (position()) then 1 + 2 else 3 + 4",
		"for $x in (1, 2) return $x + 1 + 2",
	}
	for _, src := range srcs {
		e, err := Parse(src, nil)
		if err != nil {
			t.Fatalf("Parse(%q): %v", src, err)
		}
		// A shared table across the whole tree is the arrangement the
		// optimiser actually uses, so it is the one to check.
		f := newExprFacts()
		var walk func(Expr)
		walk = func(n Expr) {
			if n == nil {
				return
			}
			want := isClosedUncached(n, newExprFacts())
			if got := isClosed(n, f); got != want {
				t.Errorf("in %q: isClosed(%T) = %v, uncached = %v",
					src, n, got, want)
			}
			optimizeWith(n, func(c Expr) Expr { walk(c); return c })
		}
		walk(e)
	}
}
