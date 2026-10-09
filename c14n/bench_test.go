package c14n

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// genDoc builds a document of at least size bytes out of a repeated record
// that exercises every per-element path: a prefixed element, a redeclared
// prefix, attributes in two namespaces, escaped text, a comment and a PI.
// The first record is the one ExcludeSubtree cases remove.
func genDoc(size int) string {
	var sb strings.Builder
	sb.Grow(size + 256)
	sb.WriteString(`<r xmlns="urn:r" xmlns:p="urn:p" xmlns:q="urn:q" xml:lang="en">`)
	for i := 0; sb.Len() < size; i++ {
		fmt.Fprintf(&sb, `<p:item id="%d" p:k="v&amp;w" q:z="1"><name>n &lt; %d</name><q:x xmlns:q="urn:q2"/><!--c--><?pi d?></p:item>`, i, i)
	}
	sb.WriteString(`</r>`)
	return sb.String()
}

var (
	docCacheMu sync.Mutex
	docCache   = map[int]*xdm.Node{}
)

// cachedDoc parses genDoc(size) once per process: the 10 MB parse costs more
// than the canonicalization being measured.
func cachedDoc(tb testing.TB, size int) *xdm.Node {
	tb.Helper()
	docCacheMu.Lock()
	defer docCacheMu.Unlock()
	if d, ok := docCache[size]; ok {
		return d
	}
	d := parse(tb, genDoc(size))
	docCache[size] = d
	return d
}

// TestStreamingAllocations: the design target is no allocation per element
// in steady state (the stacks are truncated, not reallocated), so the count
// for a whole canonicalization must not grow with the document. Comparing a
// 1 MB and a 10 MB document states that directly; the fixed bound catches a
// constant that has crept up. The counts were measured identical under
// -race (18 and 17), so the test does not skip there. It skips under -short
// alone: building the 10 MB document is the slow part, and whether to skip is
// decided by the flag rather than by how long the build happened to take.
func TestStreamingAllocations(t *testing.T) {
	if testing.Short() {
		t.Skip("building the 10 MB document is slow")
	}
	small, large := cachedDoc(t, 1<<20), cachedDoc(t, 10<<20)
	const bound = 64
	for _, alg := range allAlgorithms {
		opts := Options{Algorithm: alg, InclusiveNamespacePrefixes: []string{"q"}}
		allocs := func(doc *xdm.Node) float64 {
			root := doc.Children[0]
			return testing.AllocsPerRun(2, func() {
				if err := Write(io.Discard, root, opts); err != nil {
					t.Fatal(err)
				}
			})
		}
		a1, a10 := allocs(small), allocs(large)
		t.Logf("%s: 1 MB %.0f allocs, 10 MB %.0f allocs", alg, a1, a10)
		if a10 > bound || a10 > a1+4 {
			t.Errorf("%s: 1 MB %.0f allocs, 10 MB %.0f allocs; want <= %d and not growing", alg, a1, a10, bound)
		}
	}
}

func benchCanon(b *testing.B, size int, exclude bool, alg Algorithm) {
	doc := cachedDoc(b, size)
	root := doc.Children[0]
	ns := Subtree(root)
	if exclude {
		ns = ExcludeSubtree(root, root.Children[0])
	}
	opts := Options{Algorithm: alg}
	b.SetBytes(int64(len(genDoc(size))))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := WriteNodeSet(io.Discard, ns, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCanonicalize(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"1KB", 1 << 10}, {"100KB", 100 << 10}, {"10MB", 10 << 20}} {
		for _, set := range []struct {
			name    string
			exclude bool
		}{{"Subtree", false}, {"ExcludeSubtree", true}} {
			for _, alg := range []struct {
				name string
				a    Algorithm
			}{{"Inclusive10", Inclusive10}, {"Exclusive10", Exclusive10}} {
				b.Run(size.name+"/"+set.name+"/"+alg.name, func(b *testing.B) {
					benchCanon(b, size.n, set.exclude, alg.a)
				})
			}
		}
	}
}
