package xdm

import (
	"testing"
	"unsafe"
)

// TestNodeExtOutOfLine pins T9: the rarely set unexported state lives behind
// Node.ext, which keeps a node in the 288-byte size class, and a by-value copy
// ("c := *n") behaves as it did when the fields were inline -- it inherits the
// type environment, never its original's root identity, and does not see a
// type environment set on the original afterwards.
func TestNodeExtOutOfLine(t *testing.T) {
	if got := unsafe.Sizeof(Node{}); got != 280 {
		t.Errorf("unsafe.Sizeof(Node{}) = %d, want 280", got)
	}

	envA, envB := NewTypeEnvironment(), NewTypeEnvironment()
	orig := &Node{Kind: KindElement, Name: QName{Local: "r"}}
	orig.SetTypeEnv(envA)
	idOrig := detachedRootID(orig)

	cp := *orig
	if cp.TypeEnv() != envA {
		t.Errorf("copy lost the type environment")
	}
	orig.SetTypeEnv(envB)
	if cp.TypeEnv() != envA {
		t.Errorf("SetTypeEnv on the original leaked into the copy")
	}
	if orig.TypeEnv() != envB || detachedRootID(orig) != idOrig {
		t.Errorf("SetTypeEnv lost state: env ok %v, id %d want %d",
			orig.TypeEnv() == envB, detachedRootID(orig), idOrig)
	}
	if id := detachedRootID(&cp); id == idOrig {
		t.Errorf("copy took its original's detached-root id %d", id)
	}
	if detachedRootID(orig) != idOrig || cp.TypeEnv() != envA {
		t.Errorf("giving the copy its own identity disturbed either node")
	}
	orig.SetTypeEnv(nil)
	if orig.TypeEnv() != nil || TypeEnvOf(orig) != globalTypeEnv {
		t.Errorf("SetTypeEnv(nil) did not clear the environment")
	}
}
