package xslt

import (
	"testing"
	"unsafe"
)

// applySorts holds one sortValue per item per key. It was 152 bytes, with the
// collation and case-fold material inline; that material now hangs off a
// pointer, which only the keys needing it pay for.
func TestSortValueStaysSmall(t *testing.T) {
	if sz := unsafe.Sizeof(sortValue{}); sz > 96 {
		t.Fatalf("sortValue is %d bytes, want at most 96", sz)
	}
}
