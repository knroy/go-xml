package xslt

import (
	"testing"
	"unsafe"
)

// The runtime is copied on every focus, variable and mode change (withFocus,
// withVar, withCurrent, ...), so its size is paid per instruction. It holds
// the caller's TransformOptions by pointer: by value they were more than half
// of it (672 B against 328 B), and they are never written after newRuntime.
func TestRuntimeCopyStaysSmall(t *testing.T) {
	const limit = 400
	if got := unsafe.Sizeof(runtime{}); got > limit {
		t.Fatalf("runtime is %d B, want at most %d: hold large state by pointer", got, limit)
	}
}
