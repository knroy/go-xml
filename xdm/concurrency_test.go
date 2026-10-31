package xdm

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Position resolution reads the tree's line index. A parsed tree is shared
// across concurrent transforms, so several goroutines resolve positions in it
// at the same moment.
func TestConcurrentPositionLookup(t *testing.T) {
	var b strings.Builder
	b.WriteString("<r>\n")
	for i := 0; i < 500; i++ {
		b.WriteString("  <n/>\n")
	}
	b.WriteString("</r>\n")

	tree, err := ParseString(b.String(), ParseOptions{TrackPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	elems := tree.Root.ChildElements()[0].ChildElements()

	var wg sync.WaitGroup
	bad := make(chan string, 16)
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, n := range elems {
				line, col, ok := n.Position()
				// The i-th <n/> is on line i+2, indented two spaces.
				if !ok || line != i+2 || col != 3 {
					bad <- "wrong position under concurrency"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	for m := range bad {
		t.Error(m)
		return
	}
}

// Document order is assigned once at Finalize, and comparing two nodes is a
// read of that. Sorting the same sequence concurrently must not mutate it.
func TestConcurrentDocumentOrder(t *testing.T) {
	tree, err := ParseString("<r><a/><b/><c/><d/></r>", ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kids := tree.Root.ChildElements()[0].ChildElements()

	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				// A fresh slice per iteration: sorting a shared one would be
				// a race in the test rather than in the engine.
				seq := make(Sequence, 0, len(kids))
				for j := len(kids) - 1; j >= 0; j-- {
					seq = append(seq, kids[j])
				}
				sorted := SortDocumentOrder(seq)
				if len(sorted) != len(kids) {
					t.Errorf("got %d nodes, want %d", len(sorted), len(kids))
					return
				}
			}
		}()
	}
	wg.Wait()
}

// Parses share pooled read windows (readBufs). Concurrent parses of
// different documents, through readers that hand data over a few bytes at a
// time, must each build their own tree; run under -race this also checks
// that no window is used by two parses at once.
func TestConcurrentParsesShareNoWindow(t *testing.T) {
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc := fmt.Sprintf("<r g='%d'>%s</r>", g, strings.Repeat(fmt.Sprintf("<e>%d</e>\r\n", g), 500))
			want := strings.Repeat(fmt.Sprintf("%d\n", g), 500)
			for range 20 {
				tree, err := Parse(&chunkReader{s: doc, n: 7}, ParseOptions{})
				if err != nil {
					t.Error(err)
					return
				}
				if got := tree.Root.StringValue(); got != want {
					t.Errorf("goroutine %d parsed %.40q, want %.40q", g, got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}
