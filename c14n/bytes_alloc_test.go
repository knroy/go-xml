package c14n

import (
	"runtime"
	"testing"
)

// Bytes and BytesNodeSet grew a bytes.Buffer by doubling from empty, 3.7
// times the canonical form in all, then kept the writer's chunks and joined
// them, 2.1 times, and Equal paid it for its first operand. They now size
// the result from the node set and write into it once, about 1.1 times.
func TestBytesAllocation(t *testing.T) {
	// 3 MB: doubling cost 3.1x the output here and 3.7x at 10 MB.
	root := cachedDoc(t, 3<<20).FirstChild()
	out, err := Bytes(root, Options{Algorithm: Inclusive10})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		f    func()
	}{
		{"Bytes", func() { _, _ = Bytes(root, Options{Algorithm: Inclusive10}) }},
		{"BytesNodeSet", func() { _, _ = BytesNodeSet(Subtree(root), Options{Algorithm: Inclusive10}) }},
		{"Equal", func() { _, _ = Equal(root, root, Options{Algorithm: Inclusive10}) }},
	} {
		got := heapAllocated(tc.f)
		t.Logf("%s: %d bytes allocated for %d bytes of output (%.2fx)", tc.name, got, len(out), float64(got)/float64(len(out)))
		if got > uint64(len(out))*7/5 {
			t.Errorf("%s allocated %d bytes for %d bytes of output, want under 1.4x", tc.name, got, len(out))
		}
	}
}

// heapAllocated is the heap f allocates, the least of three runs so that a
// collection landing inside one does not count against it.
func heapAllocated(f func()) uint64 {
	best := ^uint64(0)
	for range 3 {
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		f()
		runtime.ReadMemStats(&b)
		best = min(best, b.TotalAlloc-a.TotalAlloc)
	}
	return best
}
