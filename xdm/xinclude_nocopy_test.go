package xdm

import (
	"runtime"
	"strings"
	"testing"
)

// A tree with no XInclude element has nothing to process, so neither the
// including document nor an included one is copied (-xinclude
// on a 100 MB document peaked 500 MB higher for no inclusion at all).
func TestXIncludeSkipsCopyWithoutXIElements(t *testing.T) {
	tree := parseWithBase(t, "mem:/a.xml", "<a><b/>text<c/></a>")
	out, err := ProcessXInclude(tree, XIncludeOptions{})
	if err != nil || out != tree {
		t.Fatalf("plain tree: got %p (tree %p), err %v; want the tree itself", out, tree, err)
	}
	// A tree that is not a frozen parse can still be changed, so it gets its
	// own copy even with nothing to include.
	built := &Tree{Root: Copy(tree.Root)}
	if out, err := ProcessXInclude(built, XIncludeOptions{}); err != nil || out == built {
		t.Fatalf("built tree: got %p (tree %p), err %v; want a new tree", out, built, err)
	}

	big := "<r>" + strings.Repeat("<e a='1'>text</e>", 20000) + "</r>"
	parseBytes := allocBytes(func() { parseWithBase(t, "mem:/big.xml", big) })
	doc := parseWithBase(t, "mem:/a.xml", `<a xmlns:xi="http://www.w3.org/2001/XInclude"><xi:include href="big.xml"/></a>`)
	res := &mapResolver{files: map[string]string{"mem:/big.xml": big}}
	incBytes := allocBytes(func() {
		if _, err := ProcessXInclude(doc, XIncludeOptions{Resolver: res}); err != nil {
			t.Fatal(err)
		}
	})
	// Parse plus the one copy into the result is about 2.2 parses; a copy of
	// the included document before it, or through a holder, adds 1 each.
	if incBytes > parseBytes*13/5 {
		t.Errorf("including a plain document allocated %d bytes; parsing it alone is %d", incBytes, parseBytes)
	}
	t.Logf("parse %d include %d", parseBytes, incBytes)
}

func allocBytes(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}
