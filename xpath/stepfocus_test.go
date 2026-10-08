package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// evalStepOver hands a plain axis step its context node directly instead of
// copying the context once per input node to set a focus. The step's own
// predicates still see a focus of their own, numbered within what the step
// selected from that one node, so position() and last() must come out exactly
// as they did; and a step that is not a plain axis step ("a/(b)", "a/f()")
// still gets the per-node focus it reads.
func TestPathStepWithoutFocusCopy(t *testing.T) {
	tree, err := xdm.ParseString(
		`<r><a n="1"><b>1</b><b>2</b></a><a n="2"><b>3</b></a><a n="3"><b>4</b><b>5</b><b>6</b></a></r>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ expr, want string }{
		{"/r/a/b", "1 2 3 4 5 6"},
		{"/r/a/b[1]", "1 3 4"},
		{"/r/a/b[last()]", "2 3 6"},
		{"/r/a/b[position() = last() - 1]", "1 5"},
		{"/r/a/b/preceding-sibling::b[1]", "1 4 5"},
		{"/r/a[2]/b/ancestor::*/@n", "2"},
		{"/r/a/(b[1])", "1 3 4"},
		{"/r/a/position()", "1 2 3"},
		{"/r/a/last()", "3 3 3"},
		{"/r/a/b/string(.)", "1 2 3 4 5 6"},
	} {
		v, err := MustCompile(c.expr, nil).Eval(NewContext(tree.Root, Builtins()))
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		var got []string
		for _, it := range v {
			switch x := it.(type) {
			case *xdm.Node:
				got = append(got, x.StringValue())
			case *xdm.Atomic:
				got = append(got, x.String())
			}
		}
		if s := strings.Join(got, " "); s != c.want {
			t.Errorf("%s = %q, want %q", c.expr, s, c.want)
		}
	}

	// The copy cost one allocation per input node: a child step over 200
	// nodes measured 5.07 allocations per node with it and 4.07 without.
	var sb strings.Builder
	sb.WriteString("<r>")
	for i := 0; i < 200; i++ {
		sb.WriteString("<a><b/></a>")
	}
	sb.WriteString("</r>")
	big, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	as, err := MustCompile("/r/a", nil).Eval(NewContext(big.Root, nil))
	if err != nil || len(as) != 200 {
		t.Fatalf("/r/a: %d nodes, %v", len(as), err)
	}
	step := MustCompile("b", nil).expr
	ctx := NewContext(big.Root, nil)
	perNode := testing.AllocsPerRun(20, func() {
		if _, err := evalStepOver(ctx, as, step, true); err != nil {
			t.Fatal(err)
		}
	}) / 200
	t.Logf("allocations per input node: %.2f", perNode)
	if perNode > 4.5 {
		t.Errorf("a child step allocated %.2f times per input node, want at most 4.5", perNode)
	}
}
