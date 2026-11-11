package xpath

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// manyBindings is "$v0 <sep> 1, $v1 <sep> 1, ..." with n bindings.
func manyBindings(n int, sep string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "$v%d %s 1", i, sep)
	}
	return b.String()
}

// bindingEval evaluates expr as XPath 3.1, which let and inline functions need.
func bindingEval(expr string, root *xdm.Node) (xdm.Sequence, error) {
	c, err := CompileWith(expr, CompileOptions{Namespaces: testNS{}, Version: XPath31})
	if err != nil {
		return nil, err
	}
	return c.Eval(NewContext(root, Builtins()))
}

// The parser bounds neither the number of bindings in a for, some, every or
// let, and Descend is charged once for the whole expression. evalFor and
// evalQuantified recursed once per binding, and capturedFor once per scope of
// the chain a let builds, so a long enough binding list overflowed the
// goroutine stack: a fatal error, not a catchable one. The stack is lowered
// here so that a list of a few hundred KB reaches what at Go's default 1 GB
// stack takes about 17 MB; every walk must now iterate.
func TestManyBindingsDoNotOverflowTheStack(t *testing.T) {
	old := debug.SetMaxStack(4 << 20)
	defer debug.SetMaxStack(old)
	const n = 40000
	root := mustParse(t, "<r/>")
	for _, c := range []struct{ expr, want string }{
		{"count(for " + manyBindings(n, "in") + " return 1)", "1"},
		{"some " + manyBindings(n, "in") + " satisfies $v0 = 1", "true"},
		{"every " + manyBindings(n, "in") + " satisfies $v0 = 1", "true"},
		{"let " + manyBindings(n, ":=") + " return function() { $v0 }()", "1"},
		{"let " + manyBindings(n, ":=") + " return function() { 7 }()", "7"},
	} {
		seq, err := bindingEval(c.expr, root)
		if err != nil {
			t.Fatalf("%.40s...: %.200v", c.expr, err)
		}
		if got := renderSeq(seq); got != c.want {
			t.Fatalf("%.40s...: got %s, want %s", c.expr, got, c.want)
		}
	}
}

// The iterative loops must keep the nested-loop semantics: each binding sees
// the ones before it, the body runs once per combination, and a quantifier
// stops at the first deciding combination.
func TestMultiBindingSemanticsKept(t *testing.T) {
	for _, c := range []struct{ expr, want string }{
		{"for $a in (1, 2), $b in ($a, 10) return $a * $b", "1,10,4,20"},
		{"for $a in (), $b in (1, 2) return $b", ""},
		{"for $a in (1, 2), $b in () return $a", ""},
		{"some $a in (1, 2), $b in (3, 4) satisfies $a + $b = 6", "true"},
		{"some $a in (1, 2), $b in (3, 4) satisfies $a + $b = 7", "false"},
		{"every $a in (1, 2), $b in (3, 4) satisfies $a < $b", "true"},
		{"every $a in (1, 2), $b in ($a, 4) satisfies $a < $b", "false"},
		{"every $a in (), $b in (1) satisfies false()", "true"},
		{"some $a in (1), $b in () satisfies true()", "false"},
		{"let $a := 1, $b := $a + 1 return function() { $b }()", "2"},
	} {
		seq, err := bindingEval(c.expr, mustParse(t, "<r/>"))
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := renderSeq(seq); got != c.want {
			t.Errorf("%s: got %q, want %q", c.expr, got, c.want)
		}
	}
}

// totalAlloc reports the bytes allocated while f runs.
func totalAlloc(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// walkFreeVars copied the whole set of bound names for every binding, so k
// bindings cost O(k^2) copies, uncancellably, at compile time and on an inline
// function's first evaluation. 5000 bindings allocated about 776 MB; the walk
// is now linear in them.
func TestFreeVariablesLinearInBindings(t *testing.T) {
	const k = 5000
	c, err := CompileWith("function() { let "+manyBindings(k, ":=")+
		" return $v0 + $outer }", CompileOptions{Namespaces: testNS{}, Version: XPath31})
	if err != nil {
		t.Fatal(err)
	}
	var free []string
	n := totalAlloc(func() {
		for _, q := range c.FreeVariables() {
			free = append(free, q.Local)
		}
	})
	if strings.Join(free, ",") != "outer" {
		t.Fatalf("free variables: got %v, want [outer]", free)
	}
	if n > 16<<20 {
		t.Fatalf("FreeVariables over %d bindings allocated %d MB", k, n>>20)
	}
}

// evalStepOver concatenated every context node's selection before one final
// deduplicating sort, so "//*/following::*" over N siblings held about N^2/2
// entries for a result of N, outside the item budget: 210 MB for N = 2000.
// The accumulation is now deduplicated as it grows. "//*/(//*)" still builds
// each inner selection, which is the step's own work, but no longer holds
// them all at once.
func TestOverlappingStepDeduplicatesAsItGoes(t *testing.T) {
	const n = 2000
	root := mustParse(t, "<r>"+strings.Repeat("<a/>", n)+"</r>")
	for _, c := range []struct {
		expr, want string
		maxMB      uint64
	}{
		{"count(//*/following::*)", fmt.Sprint(n - 1), 16},
		{"count(//*/(//*))", fmt.Sprint(n + 1), 0},
		{"count(//a/following-sibling::a[1])", fmt.Sprint(n - 1), 0},
		{"(//*/(//a))[last()] is (//a)[last()]", "true", 0},
	} {
		var got string
		alloc := totalAlloc(func() {
			seq, err := Eval(c.expr, NewContext(root, Builtins()), testNS{})
			if err != nil {
				t.Fatalf("%s: %v", c.expr, err)
			}
			got = renderSeq(seq)
		})
		if got != c.want {
			t.Fatalf("%s: got %s, want %s", c.expr, got, c.want)
		}
		if c.maxMB > 0 && alloc>>20 > c.maxMB {
			t.Fatalf("%s allocated %d MB", c.expr, alloc>>20)
		}
	}
}
