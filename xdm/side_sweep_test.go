package xdm

import (
	"runtime"
	"testing"
	"time"
)

// nsAxis makes the namespace nodes of a fresh child of r and takes it back.
func nsAxis(r *Node) {
	e := r.AppendElement(QName{Local: "e"})
	for range e.NamespaceNodes() {
	}
	r.RemoveLastChild()
}

// TestSideTruncateRacesSweep: RemoveLastChild reads the namespace table while
// the sweep cleanup goroutine clears it; under -race an unlocked read fails.
func TestSideTruncateRacesSweep(t *testing.T) {
	tree := NewTree()
	r := tree.Root.AppendElement(QName{Local: "r"})
	nsAxis(r)
	runtime.GC()
	time.Sleep(50 * time.Millisecond)  // no happens-before edge for the detector
	r.AppendElement(QName{Local: "e"}) // no namespace query: nothing locks sideMu first
	r.RemoveLastChild()
}

// TestSideTruncateKeepsOneSweepChain: emptying the table while a sweep is
// pending must not let the next add arm a second chain.
func TestSideTruncateKeepsOneSweepChain(t *testing.T) {
	tree := NewTree()
	r := tree.Root.AppendElement(QName{Local: "r"})
	for i := 0; i < 10; i++ {
		nsAxis(r)
	}
	e := r.AppendElement(QName{Local: "e"})
	var keep []*Node
	for ns := range e.NamespaceNodes() {
		keep = append(keep, ns) // the table stays non-empty, so chains re-arm
	}
	before := sideSweeps.Load()
	for i := 0; i < 5; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	if n := sideSweeps.Load() - before; n > 7 {
		t.Errorf("%d sweeps in 5 collections (want about 5): more than one chain", n)
	}
	runtime.KeepAlive(keep)
}
