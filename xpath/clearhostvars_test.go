package xpath

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestClearHostVarsOneScope: the host's cleared and marked names are bound in
// one scope each, not one scope per name, and still resolve as before.
func TestClearHostVarsOneScope(t *testing.T) {
	oc, om := ClearedOnDynamicCall, MarkedOnDynamicCall
	defer func() { ClearedOnDynamicCall, MarkedOnDynamicCall = oc, om }()
	var cleared []xdm.QName
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		cleared = append(cleared, xdm.QName{URI: "urn:h", Local: n})
	}
	ClearedOnDynamicCall = cleared
	MarkedOnDynamicCall = []xdm.QName{{URI: "urn:h", Local: "m"}}

	base := NewContext(nil, Builtins()).WithVar(xdm.QName{URI: "urn:h", Local: "c"}, xdm.One(xdm.NewInteger(7)))
	got := clearHostVars(base)
	if v, ok := got.LookupVar(cleared[2]); !ok || len(v) != 0 {
		t.Fatalf("cleared name resolves to %v, %v; want bound and empty", v, ok)
	}
	if v, ok := got.LookupVar(MarkedOnDynamicCall[0]); !ok || len(v) != 1 {
		t.Fatalf("marked name resolves to %v, %v; want one true", v, ok)
	}
	depth := 0
	for s := got; s.bind != base.bind; s = s.Parent {
		depth++
	}
	if depth != 2 {
		t.Fatalf("clearHostVars made %d scopes, want 2", depth)
	}
}
