package xdm

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

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

// A start tag of many distinct attributes sizes the structures that grow with
// them -- the tokenizer's attribute slice, the name table and its index, the
// duplicate check -- from the tag, not through every step of their growth.
// 100,000 attributes allocated about 100 bytes of each byte of the tag with
// the steps; the structures' own floor is about 40.
func TestParseManyAttributesAllocation(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("<r")
	for i := 0; i < 100000; i++ {
		sb.WriteString(" a")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(`=""`)
	}
	sb.WriteString("/>")
	doc := sb.String()
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	tr, err := Parse(strings.NewReader(doc), ParseOptions{})
	runtime.ReadMemStats(&b)
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.Root.ChildElements()[0].NumAttrs(); got != 100000 {
		t.Fatalf("%d attributes, want 100000", got)
	}
	if got := (b.TotalAlloc - a.TotalAlloc) / uint64(len(doc)); got > 60 {
		t.Errorf("parse allocated %dx the tag's size, want at most 60x", got)
	}
}
