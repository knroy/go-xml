package xquery

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xpath"
)

// The variables reachable through each global's function calls are closed
// once, at compile time: every Eval walked the call graph again per
// variable, scanning every function per call name and lexing each body it
// matched, V x F^2 per evaluation.
func TestSM07ReachableVarsAreClosedOnce(t *testing.T) {
	const v, f = 60, 60
	var b strings.Builder
	b.WriteString("declare variable $g := 1;\n")
	for i := 0; i < f; i++ {
		next := "$g"
		if i+1 < f {
			next = fmt.Sprintf("local:f%d()", i+1)
		}
		fmt.Fprintf(&b, "declare function local:f%d() { %s };\n", i, next)
	}
	for i := 0; i < v; i++ {
		fmt.Fprintf(&b, "declare variable $v%d := local:f0();\n", i)
	}
	b.WriteString("$v0")
	q, err := Compile(b.String(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := q.reachableVars(q.vars[1]); len(got) != 1 || got[0] != "g" {
		t.Fatalf("reachable from $v0 = %v, want [g]", got)
	}
	ctx := xpath.NewContext(nil, xpath.Builtins())
	if _, err := q.Eval(ctx); err != nil {
		t.Fatal(err)
	}
	if a := allocated(func() { _, _ = q.Eval(ctx) }); a > 1<<20 {
		t.Fatalf("one Eval of %d globals over a %d-function chain allocated %d bytes; "+
			"the call graph is walked per evaluation again", v, f, a)
	}
}
