package xpath

import "testing"

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
