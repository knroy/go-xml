package xslt

import (
	"testing"
	"unsafe"
)

// The runtime is copied on every focus, variable and selection change, so
// what is set once per transform lives behind the transformState pointer
// and the template selection behind its own. 64 bytes is the size class the
// copy is held to; a field added to the runtime rather than to
// transformState moves it to the next one.
func TestRuntimeCopyIsSmall(t *testing.T) {
	if got := unsafe.Sizeof(runtime{}); got > 64 {
		t.Errorf("runtime is %d bytes, want at most 64: per-transform "+
			"fields belong in transformState", got)
	}
}
