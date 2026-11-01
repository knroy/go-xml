package xpath

import (
	"errors"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// deep-equal and map:find bound value nesting at maxJSONDepth, as parse-json
// does, and count it apart from function recursion.
func TestValueNestingMatchesJSONBound(t *testing.T) {
	nest := func(n int) xdm.Sequence {
		v := xdm.One(xdm.NewInteger(1))
		for i := 0; i < n; i++ {
			v = xdm.One(xdm.NewArray(v))
		}
		return v
	}
	eval := func(query string, v xdm.Sequence, depth int32) error {
		c, err := CompileWith(query, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		ctx := NewContext(nil, Builtins()).WithVar(xdm.QName{Local: "v"}, v)
		ctx.Depth = depth
		_, err = c.Eval(ctx)
		return err
	}
	if err := eval(`deep-equal($v, $v)`, nest(maxJSONDepth), 0); err != nil {
		t.Errorf("deep-equal at %d: %v", maxJSONDepth, err)
	}
	if err := eval(`deep-equal($v, $v)`, nest(maxJSONDepth), 400); err != nil {
		t.Errorf("deep-equal at %d after 400 levels of recursion: %v", maxJSONDepth, err)
	}
	if err := eval(`deep-equal($v, $v)`, nest(maxJSONDepth+1), 0); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("deep-equal at %d: got %v, want a refusal", maxJSONDepth+1, err)
	}
	find := `Q{http://www.w3.org/2005/xpath-functions/map}find($v, 1)`
	if err := eval(find, nest(maxJSONDepth-1), 400); err != nil {
		t.Errorf("map:find at %d: %v", maxJSONDepth-1, err)
	}
	if err := eval(find, nest(maxJSONDepth+1), 0); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("map:find at %d: got %v, want a refusal", maxJSONDepth+1, err)
	}
}
