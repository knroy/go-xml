package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// Round SM lane 05 regression tests (security audit run 5).

// TestSM05TailChargesItsCopy: fn:tail copies its argument, so the copy is
// charged to the item budget like fn:reverse's.
func TestSM05TailChargesItsCopy(t *testing.T) {
	_, err := callFn(t, nearlyExhausted(t, MaxBytes, 16), "tail", manyItems(64))
	wantRefused(t, err, wantItems)
	got, err := callFn(t, NewContext(nil, Builtins()), "tail", manyItems(3))
	if err != nil || len(got) != 2 {
		t.Fatalf("tail of 3 items = %d items, %v; want 2, nil", len(got), err)
	}
}

// TestSM05ListCastChargesItsTokens: a cast to a built-in list type reserves
// one item per token, as fn:tokenize does; castable as builds nothing.
func TestSM05ListCastChargesItsTokens(t *testing.T) {
	src := strSeq(strings.Repeat("a ", 64))
	for _, expr := range []string{"xs:NMTOKENS($s)", "$s cast as xs:NMTOKENS"} {
		c, err := Compile(expr, testNS{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Eval(sm05Ctx(t, 16).WithVar(xdm.QName{Local: "s"}, src))
		wantRefused(t, err, wantItems)
	}
	c, err := Compile("($s castable as xs:NMTOKENS, count(xs:NMTOKENS('a b')))", testNS{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Eval(sm05Ctx(t, 16).WithVar(xdm.QName{Local: "s"}, src))
	if err != nil || len(got) != 2 || got[0].(*xdm.Atomic).String() != "true" ||
		got[1].(*xdm.Atomic).String() != "2" {
		t.Fatalf("castable/control = %v, %v; want (true, 2)", got, err)
	}
}

// sm05Ctx is nearlyExhausted at the XPath 3.1 library with items items left.
func sm05Ctx(t *testing.T, items int) *Context {
	t.Helper()
	return nearlyExhausted(t, MaxBytes, items).WithVersion(XPath31).
		WithEnv(func(e *Env) { e.LibraryVersion = XPath31 })
}
