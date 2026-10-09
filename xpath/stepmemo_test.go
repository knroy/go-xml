package xpath

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
)

type memoRuntime struct{ m any }

func (r memoRuntime) StepMemo() any { return r.m }

func memoContext(item xdm.Item) (*Context, *stepMemo) {
	m := xpathleaf.NewStepMemo().(*stepMemo)
	ctx := NewContext(item, Builtins())
	ctx.host = &xpathleaf.Host{Runtime: memoRuntime{m}}
	return ctx, m
}

// "//x[p]" over a parsed document, answered from the memo of which nodes have
// an x child, selects what the walk selects, in the same order and with the
// same errors, on the first evaluation and on a repeat.
func TestStepMemoAnswersAsTheWalk(t *testing.T) {
	tree, err := xdm.ParseString(
		`<a id="0"><b id="1"><c id="2"/><b id="3"><c id="4">t</c></b><b id="5"/></b><c id="6"/><b id="7">x</b></a>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	show := func(ctx *Context, e string) string {
		v, err := MustCompile(e, nil).Eval(ctx)
		if err != nil {
			return "ERROR " + xdm.ErrorCode(err)
		}
		var got []string
		for _, it := range v {
			if n, ok := it.(*xdm.Node); ok {
				got = append(got, fmt.Sprintf("%p", n))
			} else {
				got = append(got, it.(*xdm.Atomic).String())
			}
		}
		return strings.Join(got, " ")
	}
	ctx, m := memoContext(tree.Root)
	for _, e := range []string{
		"//b", "//b[1]", "//b[last()]", "//b[2]", "//b[@id > 2]", "//b[c][1]",
		"//*[1]", "//*:c[2]", "//c[. = 't']", "//b/c", "count(//c)", "//b[xs:integer('x')]",
		"//nothing[1]", "/a//b[1]",
	} {
		want := show(NewContext(tree.Root, Builtins()), e)
		for i := 0; i < 2; i++ {
			if got := show(ctx, e); got != want {
				t.Errorf("%s, evaluation %d: got %q, want %q", e, i+1, got, want)
			}
		}
	}
	if len(m.parents) == 0 {
		t.Error("the memo was never used")
	}
}

// A tree that was not parsed may be changed between evaluations, so a walk
// over it is never remembered.
func TestStepMemoSkipsConstructedTrees(t *testing.T) {
	tree := xdm.NewTree()
	a := tree.Root.AppendElement(xdm.QName{Local: "a"})
	a.AppendElement(xdm.QName{Local: "b"})
	tree.Finalize()
	ctx, m := memoContext(tree.Root)
	count := func() string {
		v, err := MustCompile("count(//b[true()])", nil).Eval(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v[0].(*xdm.Atomic).String()
	}
	if got := count(); got != "1" {
		t.Fatalf("got %s b elements, want 1", got)
	}
	a.AppendElement(xdm.QName{Local: "c"}).AppendElement(xdm.QName{Local: "b"})
	tree.Finalize()
	if got := count(); got != "2" {
		t.Errorf("after adding a b under a new parent: got %s, want 2", got)
	}
	if len(m.parents) != 0 {
		t.Error("a constructed tree was memoised")
	}
}
