package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
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

// evalStepOver keeps the first axis step's result as its accumulator instead
// of copying it, since evalFrom's result is a fresh slice. A step of another
// kind can return a sequence a variable holds, so that one is still copied:
// "/r/a/$v" must not write into the spare capacity of $v's slice.
func TestPathStepResultNotCopied(t *testing.T) {
	tree, err := xdm.ParseString(`<r><a><b/><b/><b/></a><a><b/></a></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := NewContext(tree.Root, Builtins())
	bs, err := MustCompile("/r/a[1]/b", nil).Eval(ctx)
	if err != nil || len(bs) != 3 {
		t.Fatalf("/r/a[1]/b: %v %v", bs, err)
	}
	v := make(xdm.Sequence, 3, 10)
	copy(v, bs)
	ctx.Vars["v"] = v
	got, err := MustCompile("/r/a/$v", nil).Eval(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("/r/a/$v: %d items, %v", len(got), err)
	}
	if spare := v[:10]; spare[3] != nil {
		t.Errorf("/r/a/$v wrote into the spare capacity of $v")
	}
	if n, err := MustCompile("count(/r/a/b)", nil).Eval(ctx); err != nil || n[0].(*xdm.Atomic).String() != "4" {
		t.Errorf("count(/r/a/b) = %v, %v; want 4", n, err)
	}

	// One input node: the slice evalFrom built is the result itself.
	as := bs[:1:1]
	as[0] = bs[0].(*xdm.Node).Parent
	step := MustCompile("b", nil).expr
	allocs := testing.AllocsPerRun(50, func() {
		if _, err := evalStepOver(ctx, as, step, true); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("allocations: %.0f", allocs)
	if allocs > 6 {
		t.Errorf("a child step over one node allocated %.0f times, want at most 6", allocs)
	}
}

// A relative path starts from the context item without boxing it into a
// one-item sequence: "b" from one a allocates only what its step builds.
func TestRelativePathStartsWithoutBoxing(t *testing.T) {
	tree, err := xdm.ParseString(`<r><a><b/><b/><b/></a></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Root.FirstChild().FirstChild()
	ctx := NewContext(a, Builtins())
	e := MustCompile("b", nil).expr.(*PathExpr)
	if got, err := e.Eval(ctx); err != nil || len(got) != 3 {
		t.Fatalf("b: %d items, %v", len(got), err)
	}
	allocs := testing.AllocsPerRun(50, func() {
		if _, err := e.Eval(ctx); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("allocations: %.0f", allocs)
	if allocs > 3 {
		t.Errorf("the path \"b\" allocated %.0f times, want at most 3", allocs)
	}
}
