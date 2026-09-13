package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The resolver cache must EVICT when it is full, not empty itself.
//
// publish cleared the whole map on overflow -- "there is no useful recency
// signal here, and the same choice is made for the regex cache". That is
// arguable for a regex, which is cheap to rebuild; it is wrong for a parsed
// XML document. A stylesheet cycling over more than resolverCacheMax documents
// emptied the map on every miss, so the next request for a document it had
// just discarded missed as well and the steady state was re-parsing everything
// on every pass: the cache became an amplifier rather than a bound. Cycling
// 300 documents four times took 1.05s before and 0.31s after.

// writeDocs writes n small documents into dir and returns their file names.
func writeDocs(t *testing.T, dir string, n int) []string {
	t.Helper()
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = fmt.Sprintf("d%d.xml", i)
		body := fmt.Sprintf("<doc><n>%d</n></doc>", i)
		if err := os.WriteFile(
			filepath.Join(dir, names[i]), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return names
}

// Past the bound, most of the cache must survive. Wholesale clearing left
// exactly one entry behind every time it overflowed.
func TestResolverCacheEvictsRatherThanClears(t *testing.T) {
	dir := t.TempDir()
	names := writeDocs(t, dir, resolverCacheMax+100)
	res, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := fileURIOf(filepath.Join(dir, "x.xml"))

	for _, n := range names {
		if _, err := res.ResolveDocument(n, base); err != nil {
			t.Fatal(err)
		}
	}

	res.mu.Lock()
	got := len(res.cache)
	res.mu.Unlock()

	// The bound still holds...
	if got > resolverCacheMax {
		t.Fatalf("cache holds %d entries, above the bound of %d",
			got, resolverCacheMax)
	}
	// ...and it is actually full, rather than holding the handful of entries
	// left over from the last wholesale clear. Before the fix this was 50.
	if got != resolverCacheMax {
		t.Fatalf("after %d documents the cache holds only %d entries, want %d: "+
			"the cache is being cleared rather than evicted from",
			len(names), got, resolverCacheMax)
	}
}

// CONTROL: entries must survive past the bound, so a working set that fits is
// still served from cache. Node identity is the observable: fn:doc is defined
// to return the SAME node for one URI, so a cache hit is the same *xdm.Tree
// and a re-parse is a different one.
func TestResolverCacheKeepsWorkingSetPastTheBound(t *testing.T) {
	dir := t.TempDir()
	names := writeDocs(t, dir, resolverCacheMax+100)
	res, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := fileURIOf(filepath.Join(dir, "x.xml"))

	// Fill well past the bound.
	for _, n := range names {
		if _, err := res.ResolveDocument(n, base); err != nil {
			t.Fatal(err)
		}
	}

	// Now cycle a window smaller than the cache but resolved in a sequence
	// that keeps overflowing it, and count how many requests are served by a
	// tree the PREVIOUS round already handed out. Node identity is the
	// observable: fn:doc returns the same *xdm.Tree for one URI from cache,
	// and a different one after a re-parse.
	//
	// The window must be re-requested across rounds rather than twice in a
	// row. Asking twice in a row hits even when the map was just emptied,
	// because the first ask refills it -- which is exactly how a cache that
	// clears itself can look healthy, and why the first draft of this test
	// passed under sabotage.
	window := names[:200]
	prev := make(map[string]*xdm.Tree, len(window))
	hits, total := 0, 0
	for round := 0; round < 4; round++ {
		for _, n := range window {
			tr, err := res.ResolveDocument(n, base)
			if err != nil {
				t.Fatal(err)
			}
			if round > 0 {
				total++
				if prev[n] == tr {
					hits++
				}
			}
			prev[n] = tr
		}
		// Between rounds, touch enough fresh documents to overflow the cache.
		// A cache that clears wholesale loses the entire window here; one that
		// evicts a single entry per admission loses at most those 50.
		for _, n := range names[len(names)-100:] {
			if _, err := res.ResolveDocument(n, base); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Not all of them: the working set plus the overflow batch is larger than
	// the cache, so random eviction loses some of the window every round. The
	// threshold only has to separate a cache that retains a working set from
	// one that does not, and the measured gap is wide -- eviction scores 342
	// of 600 here and wholesale clearing scores exactly 0, because emptying
	// the map on overflow discards the whole window every round.
	if min := total / 4; hits < min {
		t.Fatalf("a %d-document working set inside a %d-entry cache survived "+
			"only %d of %d cross-round requests, want at least %d; entries "+
			"are not surviving the bound",
			len(window), resolverCacheMax, hits, total, min)
	}
}

// Re-publishing a path already in the cache must not evict anything: the map
// does not grow, so there is nothing to make room for.
func TestResolverCacheReplaceDoesNotEvict(t *testing.T) {
	dir := t.TempDir()
	names := writeDocs(t, dir, resolverCacheMax)
	res, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := fileURIOf(filepath.Join(dir, "x.xml"))
	for _, n := range names {
		if _, err := res.ResolveDocument(n, base); err != nil {
			t.Fatal(err)
		}
	}

	res.mu.Lock()
	full := len(res.cache)
	var anyPath string
	var anyTree = res.cache[""]
	for k, v := range res.cache {
		anyPath, anyTree = k, v
		break
	}
	res.publish(anyPath, anyTree)
	got := len(res.cache)
	res.mu.Unlock()

	if full != resolverCacheMax {
		t.Fatalf("cache did not fill: %d of %d", full, resolverCacheMax)
	}
	if got != full {
		t.Fatalf("replacing an existing entry changed the cache size from "+
			"%d to %d; it evicted something to make room it did not need",
			full, got)
	}
}
