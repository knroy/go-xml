package main

import (
	"runtime/debug"
	"testing"
)

// The command runs at GOGC=200 unless the environment sets GOGC, which then
// wins: the runtime has already applied it and lowerGC leaves it alone.
func TestLowerGCRespectsGOGC(t *testing.T) {
	orig := debug.SetGCPercent(100)
	defer debug.SetGCPercent(orig)

	t.Setenv("GOGC", "")
	lowerGC()
	if got := debug.SetGCPercent(100); got != cliGCPercent {
		t.Errorf("without GOGC: GC percent %d, want %d", got, cliGCPercent)
	}

	t.Setenv("GOGC", "100")
	lowerGC()
	if got := debug.SetGCPercent(100); got != 100 {
		t.Errorf("with GOGC=100: GC percent %d, want the environment's 100", got)
	}
}
