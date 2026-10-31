package relaxng

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// store used to copy the whole map every time: 1000 stores allocated tens of
// megabytes. Now the copies sum to O(n). Concurrent readers must stay safe
// (run under -race) and the cap must hold.
func TestNameMemoStoreAmortised(t *testing.T) {
	var c nameMemo
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	for i := 0; i < maxOpenMemo; i++ {
		c.store(xdm.QName{Local: fmt.Sprintf("n%d", i)}, emptyPat{})
	}
	runtime.ReadMemStats(&m1)
	if got := m1.TotalAlloc - m0.TotalAlloc; got > 2<<20 {
		t.Errorf("%d stores allocated %d KB, want under 2048 KB", maxOpenMemo, got>>10)
	}
	if _, ok := c.load(xdm.QName{Local: "n0"}); !ok {
		t.Error("first entry not published")
	}
	c.store(xdm.QName{Local: "over"}, emptyPat{})
	c.store(xdm.QName{Local: "over2"}, emptyPat{})
	n := len(*c.m.Load()) + len(c.pending)
	if n > maxOpenMemo {
		t.Errorf("%d entries, cap is %d", n, maxOpenMemo)
	}
}
