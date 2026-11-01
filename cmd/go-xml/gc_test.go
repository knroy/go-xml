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

// Past 128 MB of live heap the command drops from GOGC=200 to 100; below it
// the setting is left alone.
func TestRelaxGCOnBigLiveHeap(t *testing.T) {
	orig := debug.SetGCPercent(cliGCPercent)
	defer debug.SetGCPercent(orig)

	if relaxGC(cliGCBigHeap) {
		t.Error("relaxed at exactly the threshold, want only past it")
	}
	if got := debug.SetGCPercent(cliGCPercent); got != cliGCPercent {
		t.Errorf("a small heap changed GC percent to %d", got)
	}
	if !relaxGC(cliGCBigHeap + 1) {
		t.Error("did not relax past the threshold")
	}
	if got := debug.SetGCPercent(cliGCPercent); got != 100 {
		t.Errorf("GC percent after a big heap = %d, want 100", got)
	}
}
