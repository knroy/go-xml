package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func letResult(t *testing.T, expr string) []string {
	t.Helper()
	got := eval30(t, expr, nil)
	out := make([]string, 0, len(got))
	for _, it := range got {
		out = append(out, it.(*xdm.Atomic).String())
	}
	return out
}

func TestLetExpr(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{`let $x := 1 return $x`, []string{"1"}},
		{`let $x := 1, $y := 2 return $x + $y`, []string{"3"}},
		// A later binding sees the earlier ones.
		{`let $x := 1, $y := $x + 1 return $y`, []string{"2"}},
		// No whitespace around ":=", which is how most of the suite writes it
		// and what made the lexer read "x:" as a QName prefix.
		{`let $x:=1, $y:=$x+1 return $y`, []string{"2"}},
		{`let $x:="hello", $y:=concat($x," there") return $y`, []string{"hello there"}},
		// The body may itself be a let.
		{`let $x := 1 return let $y := 2 return $x + $y`, []string{"3"}},
		// An inner binding shadows an outer one.
		{`let $x := 1 return let $x := 2 return $x`, []string{"2"}},
		{`let $x := () return count($x)`, []string{"0"}},
	}
	for _, tc := range cases {
		got := letResult(t, tc.expr)
		if !equalStrings(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

// The whole point of let, and the thing that makes it not a ForExpr with a
// different keyword: the variable is bound to the entire sequence once, and
// the body is evaluated once. The corresponding "for" iterates.
func TestLetBindsWholeSequenceOnce(t *testing.T) {
	if got, want := letResult(t, `let $x := (1, 2, 3) return count($x)`), []string{"3"}; !equalStrings(got, want) {
		t.Errorf("let count = %v, want %v — let binds the whole sequence", got, want)
	}
	// for, by contrast, binds one item at a time and concatenates.
	if got, want := letResult(t, `for $x in (1, 2, 3) return count($x)`), []string{"1", "1", "1"}; !equalStrings(got, want) {
		t.Errorf("for count = %v, want %v", got, want)
	}
	// So the body runs once, not once per item.
	if got, want := letResult(t, `let $x := (1, 2, 3) return "once"`), []string{"once"}; !equalStrings(got, want) {
		t.Errorf("let body = %v, want %v", got, want)
	}
}

// "let" is not a reserved word: XPath has none, so an element actually named
// "let" must still be reachable as a path step under both versions.
func TestLetIsNotReserved(t *testing.T) {
	tree, err := xdm.ParseString(`<root><let><x>found</x></let></root>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc := tree.Root

	for _, v := range []Version{XPath20, XPath30} {
		ctx := NewContext(doc, Builtins())
		ctx.Version = v
		got, err := Eval(`/root/let/x`, ctx, nil)
		if err != nil {
			t.Fatalf("%v: /root/let/x: %v", v, err)
		}
		if len(got) != 1 {
			t.Fatalf("%v: /root/let/x returned %d items, want 1", v, len(got))
		}
		if s := got[0].(*xdm.Node).StringValue(); s != "found" {
			t.Errorf("%v: /root/let/x = %q, want %q", v, s, "found")
		}
	}
}

// A let expression is a 3.0 construct, so a 2.0 processor must refuse it
// rather than accept it quietly.
func TestLetRejectedUnderXPath20(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	// Version left at its zero value, which is XPath20.
	if _, err := Eval(`let $x := 1 return $x`, ctx, nil); err == nil {
		t.Error("XPath20 accepted a let expression, want a static error")
	}
}

// The String() round-trip is what the inventory and optimiser tests rely on to
// identify an expression, so a new node type has to implement it faithfully.
func TestLetExprString(t *testing.T) {
	e, err := ParseVersion(`let $x := 1, $y := 2 return $x + $y`, nil, XPath30)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Binary operators are parenthesised by the existing String()
	// convention, so the round-trip is not byte-identical to the source.
	const want = "let $x := 1, $y := 2 return ($x + $y)"
	if got := e.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
