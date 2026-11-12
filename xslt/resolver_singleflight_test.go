package xslt

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
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

// A waiter does not inherit an error the loader's own entity budget caused.
// The loader parses under the budget of its evaluation, so a load that one
// transform's drained allowance refused says nothing about another's: the
// waiter, handed that failure, loads the path again under its own budget.
func TestFileResolverWaiterRetriesOtherBudget(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "d.xml")
	if err := os.WriteFile(p, []byte(`<r/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &FileResolver{Roots: []string{dir}}
	call := &loadCall{done: make(chan struct{}), budget: xdm.NewEntityBudget()}
	r.inflight = map[string]*loadCall{p: call}
	got := make(chan error, 1)
	go func() {
		_, err := r.loadTracked(p, "d.xml", false, nil)
		got <- err
	}()
	// Time for the waiter to block on call.done. One that arrives after the
	// release finds the key gone and loads on its own, which passes too.
	time.Sleep(20 * time.Millisecond)
	r.mu.Lock()
	delete(r.inflight, p)
	call.err = errors.New("the loader's entity budget is spent")
	r.mu.Unlock()
	close(call.done)
	if err := <-got; err != nil {
		t.Errorf("waiter with its own budget: err = %v, want the document", err)
	}
}
