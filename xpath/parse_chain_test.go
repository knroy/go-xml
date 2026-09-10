package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// maxParseDepth counts how deeply an expression NESTS. A flat infix chain is
// parsed by a left-associative loop that never re-enters parseExprSingle, so
// the depth stayed at 1 while the AST's left spine grew one node per term —
// and three walkers descend that spine by recursion: optimize, BinaryOp.Eval
// and String(). "1" + strings.Repeat("+1", 3000000) therefore ended the
// process inside Compile, before any document was seen. Go reports a stack
// overflow as a fatal error, so recover() does not catch it and the whole
// process dies rather than the request failing.
//
// Measured before maxChainLength existed: an "or" chain crashed at 60,000
// terms under an 8 MB goroutine stack and at 200,000 under a 32 MB one.
//
// Reachable through a stylesheet's @select and through any caller that
// compiles an attacker-supplied expression: the chain lives inside a single
// attribute value, so the XML parser's own nesting limit, which counts
// ELEMENTS, never sees it.
func TestLongOperatorChainIsRefusedNotFatal(t *testing.T) {
	n := maxChainLength + 1
	for _, c := range []struct{ name, src string }{
		{"additive", "1" + strings.Repeat("+1", n)},
		{"or", "true()" + strings.Repeat(" or true()", n)},
		{"and", "true()" + strings.Repeat(" and true()", n)},
		{"union", "a" + strings.Repeat("|a", n)},
		{"intersect", "a" + strings.Repeat(" intersect a", n)},
		{"multiplicative", "1" + strings.Repeat("*1", n)},
		{"stringconcat", `"a"` + strings.Repeat(`||"a"`, n)},
		{"arrow", "'a'" + strings.Repeat(" => string()", n)},
		{"simplemap", "a" + strings.Repeat("!a", n)},
		{"unary", strings.Repeat("-", n) + "1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Compile(c.src, nil)
			if err == nil {
				t.Fatalf("a chain of %d terms was accepted; the bound did not apply", n)
			}
			if !strings.Contains(err.Error(), "operator chain exceeds") {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
			// The code is borrowed and the sentinel is what tells a caller
			// this was a refusal rather than a syntax fault. Both must hold:
			// the suites read the code out of the message, and an embedding
			// caller reads the sentinel.
			if !strings.HasPrefix(err.Error(), "XPST0003:") {
				t.Errorf("error does not begin with the borrowed code: %v", err)
			}
			if !errors.Is(err, xdm.ErrResourceLimit) {
				t.Errorf("error does not wrap xdm.ErrResourceLimit: %v", err)
			}
		})
	}
}

// The acceptance direction, which is the half that matters more: a bound set
// too low refuses valid stylesheets, and a false rejection is a conformance
// bug. The longest flat chain in any suite or corpus this library is tested
// against is 190 — a union of 190 self:: steps in the DocBook round-trip
// stylesheet under tests/misc/docbook in the XSLT 3.0 suite — with 56 in
// DocBook xslTNG, 28 in XSpec and 17 in the QT3 tools. Everything here is far
// above all of those and must still compile.
func TestLongButLegalChainsStillCompile(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		// A generated predicate of the shape real stylesheets hold.
		{"generated predicate", func() string {
			var b strings.Builder
			for i := 0; i < 5000; i++ {
				if i > 0 {
					b.WriteString(" or ")
				}
				b.WriteString("@a='v")
				b.WriteString(strings.Repeat("x", i%7))
				b.WriteString("'")
			}
			return b.String()
		}()},
		// A union of steps, which is how a DocBook-style match pattern is
		// written, an order of magnitude longer than the real one.
		{"union of steps", "self::a" + strings.Repeat("|self::a", 4999)},
		{"additive", "1" + strings.Repeat("+1", 5000)},
		{"and", "true()" + strings.Repeat(" and true()", 5000)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Compile(c.src, nil); err != nil {
				t.Fatalf("a legal %d-byte chain was refused: %v", len(c.src), err)
			}
		})
	}
}

// The bound is charged per chain, not cumulatively over the parse. A
// stylesheet holding many separate short chains has many short spines and no
// deep recursion; refusing it would be exactly the false rejection this bound
// exists to avoid. Without a per-loop counter the expression below — 200,000
// operators in total, none of them in a chain longer than two — would be
// refused.
func TestManyShortChainsAreNotRefused(t *testing.T) {
	parts := make([]string, 0, 100000)
	for i := 0; i < 100000; i++ {
		parts = append(parts, "(1+1)")
	}
	src := "count((" + strings.Join(parts, ",") + "))"
	if _, err := Compile(src, nil); err != nil {
		t.Fatalf("%d separate two-term chains were refused: %v", len(parts), err)
	}
}
