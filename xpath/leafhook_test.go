package xpath

import (
	"testing"
	"unsafe"
)

// The context is copied for every predicate item and focus change. The host
// language's dynamic state rides on it as one pointer, which keeps it within
// 512 bytes.
func TestContextCopyStaysSmall(t *testing.T) {
	const limit = 512
	if got := unsafe.Sizeof(Context{}); got > limit {
		t.Fatalf("Context is %d B, want at most %d", got, limit)
	}
}
