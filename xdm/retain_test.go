package xdm

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// retainedPerTree is the heap still live, per tree, with the trees of n
// calls of parse kept, after a collection.
func retainedPerTree(t *testing.T, n int, parse func() (*Tree, error)) int64 {
	t.Helper()
	trees := make([]*Tree, n)
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	for i := range trees {
		tr, err := parse()
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
	plain := retainedPerTree(t, n, func() (*Tree, error) { return ParseString(without, ParseOptions{}) })
	ns := retainedPerTree(t, n, func() (*Tree, error) { return ParseString(withNS, ParseOptions{}) })
	t.Logf("retained per tree: %d bytes without a declaration, %d with", plain, ns)
	if ns-plain > 2<<10 {
		t.Errorf("a namespace declaration costs %d bytes per kept tree, want under 2 KB", ns-plain)
	}
}

// A text value long enough for a block of its own was built in the
// tokeniser's buffer and then copied into the block: two allocations of its
// size. The buffer is handed over now, when it is not much larger than the
// value. Measured on the e-invoice corpus, whose attachments are base64 text:
// 4.10 MB allocated a pass before, 3.49 MB after.
func TestLongTextIsNotCopied(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation sizes are not stable under -race")
	}
	long := strings.Repeat("QUJDRA==", 25000) // 200 KB
	doc := `<r><b>short</b><a>` + long + `</a><c>after</c></r>`
	var tree *Tree
	per := allocated(func() {
		var err error
		if tree, err = Parse(strings.NewReader(doc), ParseOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("%d bytes allocated for a %d-byte text", per, len(long))
	// The buffer's growth to the value's size is most of the rest: 323 KB
	// measured, against 528 KB with the copy.
	if per > uint64(len(long))*2 {
		t.Errorf("parsing a %d-byte text allocated %d bytes, want under 2x: one buffer, not a buffer and a copy", len(long), per)
	}
	e := kids(kids(tree.Root)[0])
	if kids(e[0])[0].Value() != "short" || kids(e[1])[0].Value() != long || kids(e[2])[0].Value() != "after" {
		t.Errorf("values read back wrong")
	}
}

// A parsed tree's last text block was kept at the size it doubled to, on
// average a quarter empty. Measured on the DocBook corpus: 12.29 MB retained
// for 591 trees before the trim, 12.03 MB after.
func TestParsedTextBlockIsTrimmed(t *testing.T) {
	tree, err := ParseString(`<r>`+strings.Repeat(`<p>some text</p>`, 200)+`</r>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := &tree.text
	b := s.blocks[s.cur-1]
	// Rounding to the allocator's size class may leave a little.
	if slack := cap(b) - len(b); slack > len(b)/8 {
		t.Errorf("last text block holds %d bytes in %d", len(b), cap(b))
	}
}

// The name and frame indexes serve only building; a parsed tree kept them
// for its life. Measured on the DocBook corpus: 12.03 MB retained for 591
// trees with them, 11.07 MB without. A tree changed after the parse rebuilds
// what it needs.
func TestFrozenTreeDropsBuildIndexes(t *testing.T) {
	var b strings.Builder
	b.WriteString("<r>")
	for i := range 2 * smallNames {
		fmt.Fprintf(&b, `<e%d xmlns:p%d="urn:%d"/>`, i, i, i)
	}
	b.WriteString("</r>")
	tree, err := ParseString(b.String(), ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tree.nameIx != nil || tree.nameHot != nil || tree.frameOne != nil || tree.frameMany != nil || tree.open != nil {
		t.Errorf("a parsed tree kept its build indexes")
	}
	e := kids(kids(tree.Root)[0])
	e[0].SetName(QName{Local: "e5"})
	if e[0].name != e[5].name || tree.nameIx == nil {
		t.Errorf("renaming to an existing name gave index %d, want %d (index rebuilt: %v)", e[0].name, e[5].name, tree.nameIx != nil)
	}
}
