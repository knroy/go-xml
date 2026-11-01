package xslt

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// vacuous and deemedEmpty used to flatten the whole array before testing it,
// 16 B a leaf, so a 100,000-member array whose first member was content still
// copied all of them. They now stop at the first leaf that decides.
func TestEmptinessOfArrayStopsAtFirstDecidingLeaf(t *testing.T) {
	const n = 100000
	members := make([]xdm.Sequence, n)
	members[0] = xdm.Sequence{xdm.NewString("x")}
	for i := 1; i < n; i++ {
		members[i] = xdm.Sequence{xdm.NewString("")}
	}
	arr := xdm.NewArray(members...)
	if vacuous(arr) || deemedEmpty(arr) {
		t.Fatal("an array holding \"x\" is content")
	}
	for name, f := range map[string]func(xdm.Item) bool{"vacuous": vacuous, "deemedEmpty": deemedEmpty} {
		if a := testing.AllocsPerRun(5, func() { f(arr) }); a > 4 {
			t.Errorf("%s made %.0f allocations on an array decided by its first leaf", name, a)
		}
	}
	// Nested, all empty: both are true, and the answer survives nesting.
	inner := xdm.NewArray(xdm.Sequence{xdm.NewString("")}, xdm.Sequence{})
	outer := xdm.NewArray(xdm.Sequence{inner}, xdm.Sequence{xdm.NewArray()})
	if !vacuous(outer) || !deemedEmpty(outer) {
		t.Error("nested all-empty array should be vacuous and deemed-empty")
	}
}
