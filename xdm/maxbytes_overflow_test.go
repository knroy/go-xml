package xdm

import (
	"math"
	"testing"
)

// The largest byte limit a caller can name must not refuse every document.
//
// The reader is wrapped in io.LimitReader(r, maxBytes+1) so that hitting the
// limit is distinguishable from a document exactly at it. At math.MaxInt64
// that addition overflows to math.MinInt64, io.LimitReader reads a negative
// limit as "nothing left", and every document failed with "no root element" --
// the one setting a caller would choose to mean "do not limit me" was the one
// setting that rejected valid input.
func TestMaxBytesAtMaxInt64(t *testing.T) {
	for _, mb := range []int64{math.MaxInt64, math.MaxInt64 - 1, 1 << 40} {
		if _, err := ParseString(`<r/>`, ParseOptions{MaxBytes: mb}); err != nil {
			t.Errorf("MaxBytes=%d: %v", mb, err)
		}
	}
	// The limit must still be enforced where a document really does exceed it.
	if _, err := ParseString(`<r/>`, ParseOptions{MaxBytes: 3}); err == nil {
		t.Error("MaxBytes=3 should refuse a 4-byte document")
	}
}
