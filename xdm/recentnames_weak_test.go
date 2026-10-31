package xdm

import (
	"runtime"
	"testing"
	"weak"
)

// The cache of shared typing names must not keep a TypeEnvironment, and so a
// dropped schema, alive: a process that reloads schemas and validates typed
// copies grew by about 23 MB before the slots were weak.
func TestRecentNamesDoNotPinTypeEnvironment(t *testing.T) {
	var nt nodeTyping
	env := &TypeEnvironment{}
	w := weak.Make(env)
	setNames(&nt, typingNames{annotation: "t:one", env: env})
	if nt.get().env != env {
		t.Fatal("names lost their environment")
	}
	// A second set made while the first is in use must still share it.
	var other nodeTyping
	setNames(&other, typingNames{annotation: "t:one", env: env})
	if other.names != nt.names {
		t.Error("equal names in use were not shared")
	}
	nt, other, env = nodeTyping{}, nodeTyping{}, nil
	for i := 0; i < 3 && w.Value() != nil; i++ {
		runtime.GC()
	}
	if w.Value() != nil {
		t.Error("TypeEnvironment still reachable after every node dropped its names")
	}
}
