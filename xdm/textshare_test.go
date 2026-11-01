package xdm

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"
)

// sharesBlock reports whether dst's store holds a block that is src's.
func sharesBlock(dst, src *Tree) int {
	n := 0
	for _, d := range dst.text.blocks {
		for _, s := range src.text.blocks {
			if len(d) > 0 && len(s) > 0 && unsafe.SliceData(d) == unsafe.SliceData(s) {
				n++
			}
		}
	}
	return n
}

// textShareSource builds a tree of n children, each holding a distinct text
// of about 40 bytes, in a store that is still being appended to.
func textShareSource(n int) (*Tree, *Node) {
	src := NewTree()
	root := src.Root.AppendElement(QName{Local: "r"})
	for i := range n {
		c := root.AppendElement(QName{Local: "e"})
		c.AppendAttr(QName{Local: "a"}, fmt.Sprintf("attribute value number %08d padded", i))
		c.AppendText(fmt.Sprintf("element text number %08d, a bit longer", i))
	}
	return src, root
}

// A copy of a large part of a tree borrows its text blocks; a copy of a
// sliver of it does not pin them. Whatever it borrows, the copy's values stay
// what they were when the source or the copy is added to afterwards.
func TestCopyTextSharesBlocksOnlyWhenItCopiesMost(t *testing.T) {
	src, root := textShareSource(6000)

	dst := NewTree()
	cp := dst.Root.AppendCopy(root)
	if got := sharesBlock(dst, src); got == 0 {
		t.Fatal("a copy of the whole tree borrowed none of its text blocks")
	}

	small := NewTree()
	i := 0
	for c := range root.Children() {
		if i++; i == 4000 {
			small.Root.AppendCopy(c)
			break
		}
	}
	if got := sharesBlock(small, src); got != 0 {
		t.Fatalf("a copy of one child pinned %d blocks of the source", got)
	}

	// The last text of the copy is the last value of the source's open block.
	// Appending to it must land in storage of the copy's own, and the source
	// appending after the borrowed length must not reach the copy.
	last := cp.LastChild().LastChild()
	srcLast := root.LastChild().LastChild()
	want := last.Value()
	last.AppendValue("XYZ")
	root.AppendElement(QName{Local: "x"}).AppendText("QQQQ")
	if got := last.Value(); got != want+"XYZ" {
		t.Fatalf("copied text changed under the copy: %q, want %q", got, want+"XYZ")
	}
	if got := srcLast.Value(); !strings.HasPrefix(got, "element text number 00005999") || got != want {
		t.Fatalf("source text changed: %q, want %q", got, want)
	}
	for i, c := 0, cp.FirstChild(); c != nil && i < 6000; i, c = i+1, c.NextSibling() {
		if v := c.Attr("", "a").Value(); v != fmt.Sprintf("attribute value number %08d padded", i) {
			t.Fatalf("attribute %d: %q", i, v)
		}
	}
}
