package xslt

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Concurrent loads of one path must yield the very same tree.
//
// fn:doc is defined to return the same node for the same URI within one
// execution -- "doc('x') is doc('x')" is true -- so the resolver cache is
// correctness, not just speed. loadTracked used to hold its mutex across
// os.ReadFile and the parse, which made that safe by making it serial: cold
// cache misses on one resolver loaded one at a time. Releasing the lock for
// the I/O without single-flighting the parse would let two goroutines each
// publish a tree and hand out two document nodes for one document.
func TestFileResolverConcurrentLoadIdentity(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "d"+string(rune('a'+i))+".xml")
		if err := os.WriteFile(p, []byte(`<r><a/><b/></r>`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := &FileResolver{Roots: []string{dir}}

	const readers = 16
	for i := 0; i < n; i++ {
		name := "d" + string(rune('a'+i)) + ".xml"
		var wg sync.WaitGroup
		got := make([]any, readers)
		for j := 0; j < readers; j++ {
			wg.Add(1)
			go func(j int) {
				defer wg.Done()
				tree, err := r.ResolveDocument(name, fileURIOf(dir)+"/")
				if err != nil {
					t.Errorf("resolving %s: %v", name, err)
					return
				}
				got[j] = tree
			}(j)
		}
		wg.Wait()
		for j := 1; j < readers; j++ {
			if got[j] != got[0] {
				t.Fatalf("%s: reader %d got a different tree; fn:doc identity broken",
					name, j)
			}
		}
	}
}
