package xpath

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestPredicateStepAllocs pins the allocations of the XML-DSig transform shape,
// an ancestor step with a comparison predicate evaluated once per node on one
// reused context: the comparison hoist lives on the stack and the ancestor
// chain is collected in one block. Measured at 8 per evaluation here; 11 without them.
func TestPredicateStepAllocs(t *testing.T) {
	tree, err := xdm.ParseString(`<r><a><b><c Id="x"/></b></a></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var leaf *xdm.Node
	for n := tree.Root; n != nil; {
		leaf = n
		var next *xdm.Node
		for c := range n.Children() {
			next = c
		}
		n = next
	}
	c, err := Compile(`ancestor-or-self::*[@Id='x']`, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := NewContext(leaf, Builtins())
	allocs := testing.AllocsPerRun(50, func() {
		if _, err := c.Eval(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 9 {
		t.Errorf("%v allocations per evaluation, want at most 9", allocs)
	}
}
