package xdm

import (
	"math"
	"testing"
)

// AppendIntegers gives the values NewInteger gives, including across the
// shared small-integer range, negatives and the int64 edges, in far fewer
// allocations.
func TestAppendIntegers(t *testing.T) {
	for _, lo := range []int64{-300, -17, 0, 250, 1000, math.MaxInt64 - 299, math.MinInt64} {
		const n = 300
		got := AppendIntegers(nil, lo, n)
		if len(got) != n {
			t.Fatalf("lo=%d: %d items", lo, len(got))
		}
		for i, it := range got {
			want := NewInteger(lo + int64(i))
			a := it.(*Atomic)
			if a.Type != TypeInteger || a.String() != want.String() || a.Rat().Cmp(want.Rat()) != 0 || a.Int64() != lo+int64(i) {
				t.Fatalf("lo=%d item %d: got %v want %v", lo, i, a, want)
			}
		}
	}
	if got := AppendIntegers(nil, math.MaxInt64, 1); got[0].(*Atomic).Int64() != math.MaxInt64 {
		t.Error("MaxInt64")
	}
	chunked := testing.AllocsPerRun(5, func() { AppendIntegers(nil, 1000, 1280) })
	if chunked > 100 { // NewInteger per value is 3 x 1280
		t.Errorf("%v allocations for 1280 integers", chunked)
	}
}
