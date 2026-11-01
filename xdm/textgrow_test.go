package xdm

import (
	"bytes"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// A document that is one long text run is parsed in about its own size of
// allocation: the decoder sizes the run's buffer from the input left rather
// than growing it through several steps of its own, and the tree keeps that
// buffer as the value's block. The bound is 1.25x; the growth steps alone
// were 1.7x at 10 MiB.
func TestParseLongTextAllocatesOnce(t *testing.T) {
	const n = 10 << 20
	doc := []byte("<r>" + strings.Repeat("QUJD", n/4) + "</r>")
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	tr, err := Parse(bytes.NewReader(doc), ParseOptions{})
	runtime.ReadMemStats(&b)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.TotalAlloc - a.TotalAlloc; got > n+n/4 {
		t.Errorf("parse allocated %d bytes for %d of text (%.2fx), want at most 1.25x", got, n, float64(got)/n)
	}
	if v := tr.Root.StringValue(); len(v) != n {
		t.Errorf("text is %d bytes, want %d", len(v), n)
	}
}

// Concurrent parses of long text share nothing: each decoder's buffer is its
// own, and a value handed to a tree is not written again.
func TestParseLongTextConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			text := strings.Repeat("QUJD", (1<<20+i*997)/4)
			tr, err := Parse(strings.NewReader("<r>"+text+"</r>"), ParseOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			if tr.Root.StringValue() != text {
				t.Errorf("parse %d: text differs", i)
			}
		}()
	}
	wg.Wait()
}
