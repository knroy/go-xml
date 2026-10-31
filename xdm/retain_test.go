package xdm

import (
	"runtime"
	"strings"
	"testing"
)

// retainedPerTree is the heap still live, per tree, with n parses of doc
// kept, after a collection.
func retainedPerTree(t *testing.T, doc string, opts ParseOptions, n int) int64 {
	t.Helper()
	trees := make([]*Tree, n)
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	for i := range trees {
		tr, err := ParseString(doc, opts)
		if err != nil {
			t.Fatal(err)
		}
		trees[i] = tr
	}
	runtime.GC()
	runtime.ReadMemStats(&m1)
	runtime.KeepAlive(trees)
	return (int64(m1.HeapAlloc) - int64(m0.HeapAlloc)) / int64(n)
}

// A namespace URI was the xmlns attribute's value as the tokeniser returned
// it, a string in its arena; the tree keeps it in every name it resolves, so
// one declaration kept a whole arena block alive per document. Measured on
// the DocBook corpus: 31.6 MB retained for 591 trees before, 12.3 MB after.
// The two documents differ only in whether one attribute is a declaration.
func TestNamespaceURIDoesNotPinArena(t *testing.T) {
	if raceEnabled {
		t.Skip("heap sizes are not stable under -race")
	}
	// Attribute values fill the arena; each is copied into the tree, so
	// neither document should keep the arena itself.
	var vals string
	for i := range 9 {
		vals += ` a` + string(rune('0'+i)) + `="` + strings.Repeat("v", 900) + `"`
	}
	withNS := `<r xmlns:p="urn:example:p"` + vals + `/>`
	without := `<r p="urn:example:p"` + vals + `/>`
	const n = 200
	plain := retainedPerTree(t, without, ParseOptions{}, n)
	ns := retainedPerTree(t, withNS, ParseOptions{}, n)
	t.Logf("retained per tree: %d bytes without a declaration, %d with", plain, ns)
	if ns-plain > 2<<10 {
		t.Errorf("a namespace declaration costs %d bytes per kept tree, want under 2 KB", ns-plain)
	}
}
