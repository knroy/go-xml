package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func eval30(t *testing.T, expr string, item xdm.Item) xdm.Sequence {
	t.Helper()
	ctx := NewContext(item, Builtins())
	ctx.Version = XPath30
	got, err := Eval(expr, ctx, nil)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return got
}

// A 3.0 function must be invisible to a 2.0 expression, not merely inert: the
// error is XPST0017, the same one every other processor raises for a name it
// does not have.
func TestSeq30FunctionsHiddenFromXPath20(t *testing.T) {
	for _, expr := range []string{
		`head((1, 2))`, `tail((1, 2))`, `innermost(())`, `outermost(())`,
		`round(1, 2)`,
	} {
		ctx := NewContext(nil, Builtins())
		// Version left at its zero value, which is XPath20.
		if _, err := Eval(expr, ctx, nil); err == nil {
			t.Errorf("XPath20 evaluated %s, want XPST0017", expr)
		}
	}
}

func TestHeadAndTail(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{`head((1, 2, 3))`, []string{"1"}},
		{`head(("a", "b", "c"))`, []string{"a"}},
		{`head(())`, nil},
		{`head("a")`, []string{"a"}},
		{`tail((1, 2, 3))`, []string{"2", "3"}},
		{`tail(("a", "b", "c"))`, []string{"b", "c"}},
		{`tail("a")`, nil},
		{`tail(())`, nil},
	}
	for _, tc := range cases {
		got := eval30(t, tc.expr, nil)
		if len(got) != len(tc.want) {
			t.Errorf("%s returned %d items, want %d", tc.expr, len(got), len(tc.want))
			continue
		}
		for i, w := range tc.want {
			if s := got[i].(*xdm.Atomic).String(); s != w {
				t.Errorf("%s item %d = %q, want %q", tc.expr, i, s, w)
			}
		}
	}
}

// fn:tail must not hand back a slice that aliases its argument's backing
// array, or a later append through the returned sequence would write into the
// caller's.
func TestTailDoesNotAliasArgument(t *testing.T) {
	arg := xdm.Sequence{xdm.NewString("a"), xdm.NewString("b"), xdm.NewString("c")}
	fn, ok := Builtins().Lookup(xdm.QName{URI: xdm.NSFN, Local: "tail"}, 1)
	if !ok {
		t.Fatal("fn:tail is not registered")
	}
	got, err := fn.Call(NewContext(nil, Builtins()), []xdm.Sequence{arg})
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	got = append(got, xdm.NewString("z"))
	if s := arg[2].(*xdm.Atomic).String(); s != "c" {
		t.Errorf("appending to the result overwrote the argument: arg[2] = %q", s)
	}
}

// innermost and outermost are defined over a document, so they need one.
const nestedDoc = `<root>
  <a id="1"><b id="2"><c id="3"/></b></a>
  <d id="4"/>
</root>`

func idsOf(t *testing.T, seq xdm.Sequence) []string {
	t.Helper()
	var out []string
	for _, it := range seq {
		n, ok := it.(*xdm.Node)
		if !ok {
			t.Fatalf("result item is %T, want a node", it)
		}
		var id string
		for _, a := range n.Attrs {
			if a.Name.Local == "id" {
				id = a.Value
			}
		}
		out = append(out, id)
	}
	return out
}

func TestInnermostOutermost(t *testing.T) {
	tree, err := xdm.ParseString(nestedDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc := tree.Root

	// //* is root, a, b, c, d. The innermost of those are the ones that are
	// not an ancestor of another member: c and d. The outermost are the ones
	// with no member as an ancestor: root alone.
	if got, want := idsOf(t, eval30(t, `innermost(//*)`, doc)), []string{"3", "4"}; !equalStrings(got, want) {
		t.Errorf("innermost(//*) = %v, want %v", got, want)
	}
	if got := eval30(t, `outermost(//*)`, doc); len(got) != 1 {
		t.Errorf("outermost(//*) returned %d items, want 1 (the root)", len(got))
	}

	// Given only the leaves, each is both innermost and outermost.
	if got, want := idsOf(t, eval30(t, `innermost(//c | //d)`, doc)), []string{"3", "4"}; !equalStrings(got, want) {
		t.Errorf("innermost(//c | //d) = %v, want %v", got, want)
	}
	if got, want := idsOf(t, eval30(t, `outermost(//c | //d)`, doc)), []string{"3", "4"}; !equalStrings(got, want) {
		t.Errorf("outermost(//c | //d) = %v, want %v", got, want)
	}

	// The result is in document order with duplicates removed, whatever
	// order and multiplicity the input had.
	if got, want := idsOf(t, eval30(t, `innermost((//d, //c, //d))`, doc)), []string{"3", "4"}; !equalStrings(got, want) {
		t.Errorf("innermost with duplicates = %v, want %v", got, want)
	}

	for _, expr := range []string{`innermost(())`, `outermost(())`} {
		if got := eval30(t, expr, doc); len(got) != 0 {
			t.Errorf("%s returned %d items, want the empty sequence", expr, len(got))
		}
	}
}

// The spec singles this out: "$nodes except $nodes/descendant::node()" would
// be wrong for outermost because an attribute is not a descendant of its
// element. An attribute whose element is also in the input must be dropped.
func TestOutermostHandlesAttributes(t *testing.T) {
	tree, err := xdm.ParseString(nestedDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc := tree.Root
	// The element d and its own id attribute. d is an ancestor of the
	// attribute in the XDM sense, so only d is outermost.
	got := eval30(t, `outermost((//d, //d/@id))`, doc)
	if len(got) != 1 {
		t.Fatalf("outermost((//d, //d/@id)) returned %d items, want 1", len(got))
	}
	if n := got[0].(*xdm.Node); n.Kind != xdm.KindElement {
		t.Errorf("outermost kept the attribute, want the element")
	}
}

func TestHasChildren(t *testing.T) {
	tree, err := xdm.ParseString(nestedDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc := tree.Root
	cases := []struct {
		expr string
		want bool
	}{
		{`has-children(/root)`, true},
		{`has-children(//c)`, false},
		{`has-children(//d)`, false},
		// An empty sequence is false, not an error.
		{`has-children(())`, false},
	}
	for _, tc := range cases {
		got := eval30(t, tc.expr, doc)
		b, err := EffectiveBooleanValue(got)
		if err != nil {
			t.Fatalf("%s: %v", tc.expr, err)
		}
		if b != tc.want {
			t.Errorf("%s = %v, want %v", tc.expr, b, tc.want)
		}
	}

	// The zero-argument form reads the context item.
	ctx := NewContext(doc, Builtins())
	ctx.Version = XPath30
	got, err := Eval(`//c/has-children()`, ctx, nil)
	if err != nil {
		t.Fatalf("has-children(): %v", err)
	}
	if b, _ := EffectiveBooleanValue(got); b {
		t.Error("//c/has-children() = true, want false")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
