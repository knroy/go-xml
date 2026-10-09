package xslt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// XTTE2230 names the same incomparable key on every run: of nineteen
// untyped keys that cannot be compared with source 0's integer key, the one
// from the lowest source. Ranging the per-source map reported any of them.
func TestMergeKeysIncomparableReportedInSourceOrder(t *testing.T) {
	entries := []mergeEntry{{src: 0, atoms: []*xdm.Atomic{xdm.NewInteger(1)}}}
	for s := 1; s < 20; s++ {
		entries = append(entries, mergeEntry{src: s,
			atoms: []*xdm.Atomic{xdm.NewUntypedAtomic(fmt.Sprintf("u%d", s))}})
	}
	const want = `XTTE2230: the merge key "u1" of one input sequence`
	for i := 0; i < 20; i++ {
		err := checkMergeKeysComparable(entries, 20)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("run %d: got %v, want %q", i, err, want)
		}
	}
}
