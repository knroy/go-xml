package xpath

import (
	"errors"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A value nested far past any real document must be refused with the
// resource-limit error, not recurse until the Go stack overflows (fatal, and
// not recoverable). The depth is just past the smallest bound, so a reverted
// fix fails on the assertion rather than crashing the test binary.
func TestDeepNestedValuesAreRefused(t *testing.T) {
	const depth = 20000
	nest := func(wrap func(xdm.Sequence) xdm.Item) xdm.Sequence {
		v := xdm.One(xdm.NewInteger(1))
		for i := 0; i < depth; i++ {
			v = xdm.One(wrap(v))
		}
		return v
	}
	arrays := nest(func(s xdm.Sequence) xdm.Item { return xdm.NewArray(s) })
	maps := nest(func(s xdm.Sequence) xdm.Item {
		m := xdm.NewMap()
		m, _ = m.Put(xdm.NewString("k"), s)
		return m
	})
	for _, tc := range []struct{ name, query string }{
		{"json array", `serialize($a, map{"method":"json"})`},
		{"json map", `serialize($m, map{"method":"json"})`},
		{"adaptive array", `serialize($a, map{"method":"adaptive"})`},
		{"adaptive map", `serialize($m, map{"method":"adaptive"})`},
		{"deep-equal array", `deep-equal($a, $a)`},
		{"deep-equal map", `deep-equal($m, $m)`},
		{"map:find", `Q{http://www.w3.org/2005/xpath-functions/map}find($m, "nope")`},
	} {
		c, err := CompileWith(tc.query, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		ctx := NewContext(nil, Builtins())
		ctx = ctx.WithVar(xdm.QName{Local: "a"}, arrays).WithVar(xdm.QName{Local: "m"}, maps)
		if _, err := c.Eval(ctx); err == nil || !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s: got %v, want an ErrResourceLimit refusal", tc.name, err)
		}
	}

	// Flatten is iterative: the same value flattens to its one leaf.
	if got := xdm.Flatten(arrays); len(got) != 1 {
		t.Errorf("Flatten of %d nested arrays: %d items, want 1", depth, len(got))
	}
}
