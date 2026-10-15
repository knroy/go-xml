package xdm

import (
	"testing"
	"unsafe"
)

// Every string, number and boolean an evaluation produces is boxed in an
// Atomic, so its size is paid per value: the rare fields live behind ext.
func TestAtomicStaysSmall(t *testing.T) {
	if got := unsafe.Sizeof(Atomic{}); got > 48 {
		t.Fatalf("Atomic is %d B, want at most 48: keep rare fields in atomicExt", got)
	}
	d := NewString("x").WithDerived("token")
	if d.Derived() != "token" || NewString("x").Derived() != "" {
		t.Fatalf("WithDerived leaked into or lost from a copy")
	}
	if !NewBoolean(true).Bool() || NewBoolean(false).Bool() || NewDouble(1).Bool() {
		t.Fatalf("Bool wrong")
	}
}
