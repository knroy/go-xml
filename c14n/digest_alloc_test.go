package c14n

import (
	"crypto/sha256"
	"testing"
)

// Every call allocated an 80 KiB output buffer, 81 KB of garbage for each
// Digest of a tiny document. The buffers now come from a pool. The bound is half the unpooled
// figure because the race detector makes a pool drop a quarter of its puts.
func TestDigestSmallDocumentAllocation(t *testing.T) {
	root := parse(t, `<a><b>x</b></a>`).FirstChild()
	h := sha256.New()
	const calls = 101
	got := heapAllocated(func() {
		for range calls {
			h.Reset()
			if _, err := Digest(h, root, Options{Algorithm: Inclusive10}); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Logf("%d Digest calls allocated %d bytes", calls, got)
	if got > 4<<20 {
		t.Errorf("%d Digest calls allocated %d bytes, want under 4 MiB (8.3 MB with a fresh buffer each)", calls, got)
	}
}
