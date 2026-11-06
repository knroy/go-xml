package xdm

import (
	"strings"
	"testing"
)

// A tree's text store addresses 1<<17 blocks and panics past them. Adding
// one long string again, as xsl:copy-of of one 8 KB text node into 131,073
// elements does, used to take a block per copy and reached the panic with
// 45 MB of heap; it now takes one block.
func TestTextStoreSameLongStringTakesOneBlock(t *testing.T) {
	var s textStore
	v := strings.Repeat("x", textOwnFrom)
	for range 1<<(32-textShift) + 1 {
		if off, n := s.add(v); s.str(off, n) != v {
			t.Fatal("a kept value reads back wrong")
		}
	}
	if len(s.blocks) != 1 {
		t.Errorf("%d copies of one string took %d blocks, want 1", 1<<(32-textShift)+1, len(s.blocks))
	}
}

// Past half the addressable blocks, distinct long values (substrings of one
// string cost no bytes each) are copied and packed, so the rest of the block
// space takes real bytes rather than none.
func TestTextStorePacksPastHalfTheBlocks(t *testing.T) {
	var s textStore
	s.blocks = make([][]byte, textCopyFrom)
	big := strings.Repeat("abcdefgh", 1024) + strings.Repeat("y", 1000)
	const k = 1000
	for i := range k {
		v := big[i : i+textOwnFrom]
		if off, n := s.add(v); s.str(off, n) != v {
			t.Fatalf("value %d reads back wrong", i)
		}
	}
	if got := len(s.blocks) - textCopyFrom; got > k/3 {
		t.Errorf("%d distinct 8 KB values took %d blocks, want them packed (at most %d)", k, got, k/3)
	}
}

// SetTextBlocks sets the number of blocks a text store addresses, for
// textceiling_test.go (package xdm_test, which imports xslt and xquery), and
// returns the function that restores it.
func SetTextBlocks(n int) func() {
	old := textBlocks
	textBlocks = n
	return func() { textBlocks = old }
}
