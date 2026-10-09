package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// "name(A) = name(B)" and "name(A) = 'lit'" are answered without building
// the names (nameComparison). Each must agree with the comparison evaluated
// as written, which string(...) around a call forces, errors included.
func TestNameComparison(t *testing.T) {
	tree, err := xdm.ParseString(
		`<m:a xmlns:m="urn:meta" xmlns:n="urn:meta" m:id="1" id="2"><m:b/><n:b/><b/>t<!--c--><?pi x?>`+
			`<m:b><m:c/></m:b><b/></m:a>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ns := testNS{}
	for _, src := range []string{
		"count(//*[name(.) = name(..)])", "count(//*[name() = 'm:b'])", "count(//*[name() != 'm:b'])",
		"count(//*['b' = name()])", "count(//*[name(.) = 'b'])", "count(//node()[name() = ''])",
		"count(//@*[name() = 'm:id'])", "count(//@*[name() = 'id'])", "count(//processing-instruction()[name() = 'pi'])",
		"for $e in //* return count($e/../*[name() = name($e)])",
		"for $e in //* return count($e/preceding-sibling::*[name(.) = name($e)])",
		"count(//*[name() = 'm:'])", "count(//*[name() = ':b'])", "count(//*[name() = 'mb'])",
		"name($none) = ''", "name($bs) = 'b'", "name($one) = 'b'", "name(1) = 'b'", "count(//*[name() = 1])",
		"/m:a/name() = 'm:a'", "name() = ''",
	} {
		ref := src
		for _, r := range [][2]string{{"name(.)", "string(name(.))"}, {"name()", "string(name())"},
			{"name(..)", "string(name(..))"}, {"name($e)", "string(name($e))"},
			{"name($none)", "string(name($none))"}, {"name($bs)", "string(name($bs))"}, {"name($one)", "string(name($one))"}, {"name(1)", "string(name(1))"}} {
			ref = strings.ReplaceAll(ref, r[0], r[1])
		}
		if ref == src {
			t.Fatalf("%s: no reference spelling", src)
		}
		for _, ctxNode := range []*xdm.Node{tree.Root, tree.Root.Children[0]} {
			ctx := func() *Context {
				c := NewContext(ctxNode, Builtins())
				c.Vars["none"] = xdm.Empty()
				c.Vars["bs"] = xdm.Sequence{tree.Root.Children[0].Children[2], tree.Root.Children[0].Children[6]}
				c.Vars["one"] = xdm.One(xdm.NewInteger(1))
				return c
			}
			got, gerr := MustCompile(src, ns).Eval(ctx())
			want, werr := MustCompile(ref, ns).Eval(ctx())
			if (gerr == nil) != (werr == nil) || gerr != nil && gerr.Error() != werr.Error() {
				t.Fatalf("%s: error %v, want %v", src, gerr, werr)
			}
			if gerr == nil && atomsText(got) != atomsText(want) {
				t.Fatalf("%s from %s: %s, want %s", src, ctxNode.Kind, atomsText(got), atomsText(want))
			}
		}
	}

	// The fast path is taken: a comparison of two names over one node each.
	e := MustCompile("name(.) = 'm:a'", ns).Expr().(*BinaryOp)
	if got, ok := nameComparison(NewContext(tree.Root.Children[0], Builtins()), e); !ok || !got {
		t.Errorf("name(.) = 'm:a': %v, %v; want true, true", got, ok)
	}
	if _, ok := nameComparison(NewContext(xdm.NewString("x"), Builtins()), e); ok {
		t.Error("name(.) over a string is answered without the call")
	}
}

func atomsText(s xdm.Sequence) string {
	var b strings.Builder
	for _, it := range s {
		b.WriteString(it.(*xdm.Atomic).String() + " ")
	}
	return b.String()
}
