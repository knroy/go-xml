package xdm

import (
	"strings"
	"testing"
)

// TestPlanFromCountsCapacity: a copy borrows a source block only when the text
// it takes is at least half the array it would pin, and an untrimmed built
// block is 32 KB however little it holds.
func TestPlanFromCountsCapacity(t *testing.T) {
	bt := NewTree()
	br := bt.Root.AppendElement(QName{Local: "r"})
	for i := 0; i < 70; i++ {
		br.AppendElement(QName{Local: "p"}).AppendText(strings.Repeat("w", 1000))
	}
	for i := 0; i < 8; i++ {
		br.AppendElement(QName{Local: "p"}).AppendText(strings.Repeat("y", 8000))
	}
	sub := br.AppendElement(QName{Local: "s"})
	sub.AppendText(strings.Repeat("z", 8000))
	for i := 0; i < planMin; i++ {
		sub.AppendElement(QName{Local: "e"})
	}
	last := bt.text.blocks[len(bt.text.blocks)-1]
	if len(last) >= cap(last)/2 || len(last) < 2*8000 {
		t.Fatalf("setup: last block len %d cap %d", len(last), cap(last))
	}
	dst := NewTree()
	dst.Root.AppendCopy(sub)
	for _, b := range dst.text.blocks {
		if len(b) > 0 && &b[0] == &last[0] {
			t.Errorf("copy borrowed the source block: pins %d B for 8000 B copied", cap(last))
		}
	}
}
