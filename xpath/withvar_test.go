package xpath

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// WithVar holds its binding inline: one allocation (the context), no map,
// and lookups still honour shadowing, namespaces and focus copies.
func TestWithVarInlineBinding(t *testing.T) {
	x, nsX, y := xdm.QName{Local: "x"}, xdm.QName{URI: "urn:a", Local: "x"}, xdm.QName{Local: "y"}
	root := NewContext(nil, Builtins())
	root.Vars["y"] = strSeq("root-y")
	c := root.WithVar(x, strSeq("1")).WithVar(nsX, strSeq("ns")).WithVar(x, strSeq("3"))
	c = c.WithFocus(xdm.NewString("item"), 1, 1)
	for _, tc := range []struct {
		name xdm.QName
		want string
	}{{x, "3"}, {nsX, "ns"}, {y, "root-y"}} {
		v, ok := c.LookupVar(tc.name)
		if !ok || len(v) != 1 || v[0].(*xdm.Atomic).String() != tc.want {
			t.Errorf("LookupVar(%s) = %v, %v; want %q", tc.name.Clark(), v, ok, tc.want)
		}
	}
	if _, ok := c.LookupVar(xdm.QName{URI: "urn:b", Local: "x"}); ok {
		t.Error("{urn:b}x resolved to a binding of another namespace")
	}
	if n := testing.AllocsPerRun(100, func() { withVarSink = root.WithVar(x, nil) }); n != 1 {
		t.Errorf("WithVar allocates %v objects, want 1", n)
	}
}

var withVarSink *Context

// A retained-focus closure takes its bindings from the call, so the inline
// pair at the reference point must not shadow the caller's.
func TestRetainedFocusTakesCallBinding(t *testing.T) {
	x := xdm.QName{Local: "x"}
	root := NewContext(nil, Builtins())
	ref := root.WithVar(x, strSeq("reference"))
	call := root.WithVar(x, strSeq("call"))
	f := withRetainedFocus(ref, func(ctx any, _ []xdm.Sequence) (xdm.Sequence, error) {
		v, _ := ctx.(*Context).LookupVar(x)
		return v, nil
	})
	v, _ := f(call, nil)
	if len(v) != 1 || v[0].(*xdm.Atomic).String() != "call" {
		t.Fatalf("$x = %v, want the call's binding", v)
	}
}
