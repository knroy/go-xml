package xquery

import (
	"runtime/debug"
	"strings"
	"testing"
)

// A chain of function tests in a binding's type is scanned in a loop: a
// million links used a Go frame each and overflowed the stack, fatally.
// Here the chain is long enough to be refused by xpath's depth bound, which
// must be what answers, and a short chain still compiles.
func TestSM07FunctionTestChainIsScannedIteratively(t *testing.T) {
	q := func(n int) string {
		return "let $f as " + strings.Repeat("function() as ", n) +
			"item()* := 1 return 2"
	}
	if _, err := Compile(q(3), Options{}); err != nil {
		t.Fatalf("3 links: %v", err)
	}
	src := q(100000)
	p := &parser{src: src[len("let $f "):]}
	p.consumeWord("as")
	// A 4 MiB stack holds the loop easily and not one frame per link, so a
	// recursive scan dies here rather than passing.
	old := debug.SetMaxStack(4 << 20)
	got, err := p.scanSequenceType()
	debug.SetMaxStack(old)
	if err != nil {
		t.Fatalf("scan of 100000 links: %v", err)
	}
	if !strings.HasSuffix(got, "item()*") {
		t.Fatalf("scan stopped early: ...%q", got[max(0, len(got)-20):])
	}
	if _, err := Compile(src, Options{}); err == nil {
		t.Fatal("100000 nested function tests compiled; xpath's depth bound did not apply")
	}
}
