package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A leaf builtin counts its call depth in place on the caller's context; the
// boundary and the error must be the ones Descend gives a non-leaf builtin,
// and the caller's depth must be restored on success and on failure.
func TestLeafBuiltinDepthBoundary(t *testing.T) {
	const depth = 5
	nest := strings.Repeat("for $x in 1 to 1 return ", depth)
	for _, fn := range []string{"string($x)", "reverse($x)"} { // leaf, non-leaf
		c, err := CompileWith(nest+fn, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			max  int
			want string
		}{{depth, "XPDY0001: recursion exceeded 5 levels"}, {depth + 1, ""}} {
			ctx := NewContext(nil, Builtins())
			ctx.MaxDepth = tc.max
			_, err := c.Eval(ctx)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("%s under MaxDepth %d: %v", fn, tc.max, err)
			case tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want) ||
				!errors.Is(err, xdm.ErrResourceLimit)):
				t.Errorf("%s under MaxDepth %d: got %v, want %q", fn, tc.max, err, tc.want)
			}
		}
	}

	if fn, _ := Builtins().Lookup(xdm.QName{URI: xdm.NSFN, Local: "string"}, 1); !fn.leaf {
		t.Fatal("fn:string#1 is not marked leaf; the in-place path is untested")
	}
	call := &FuncCall{Name: xdm.QName{URI: xdm.NSFN, Local: "string"}, Args: []Expr{&Literal{Val: xdm.NewInteger(1)}}}
	for _, max := range []int{3, 4} {
		ctx := NewContext(nil, Builtins())
		ctx.Depth, ctx.MaxDepth = 3, max
		_, err := call.Eval(ctx)
		if (err != nil) != (max == 3) || ctx.Depth != 3 {
			t.Errorf("MaxDepth %d: err %v, depth after %d; want depth 3 and an error only at 3", max, err, ctx.Depth)
		}
	}
}
