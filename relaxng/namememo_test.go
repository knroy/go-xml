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

// Pending entries are published after a few locked hits, so a steady
// lookup stream becomes lock-free instead of staying on the slow path.
func TestNameMemoPendingPublishedOnHits(t *testing.T) {
	var c nameMemo
	for _, n := range []string{"a", "b"} { // 2nd store leaves one pending
		c.store(xdm.QName{Local: n}, emptyPat{})
	}
	if c.np.Load() == 0 {
		t.Fatal("setup: expected a pending entry")
	}
	for i := 0; i < pendingHitsPublish; i++ {
		if _, ok := c.load(xdm.QName{Local: "b"}); !ok {
			t.Fatal("pending entry lost")
		}
	}
	if c.np.Load() != 0 || len(*c.m.Load()) != 2 {
		t.Errorf("pending not published: np=%d snapshot=%d", c.np.Load(), len(*c.m.Load()))
	}
}
