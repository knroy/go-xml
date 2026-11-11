package xdm

import (
	"fmt"
	"strings"
	"testing"
)

// TestSM11ViewBatchCountsFrames: an ancestor's namespace declarations count
// toward the view batch's path size, so the copies made into new batches stay
// linear in the input rather than growing as declarations x children/4096.
func TestSM11ViewBatchCountsFrames(t *testing.T) {
	const k, n = 8192, 40000
	var b strings.Builder
	b.WriteString("<r")
	for i := range k {
		fmt.Fprintf(&b, " xmlns:p%d='u'", i)
	}
	b.WriteString(">")
	b.WriteString(strings.Repeat("<c/>", n))
	b.WriteString("</r>")
	c := &viewAll{fn: func(*Node, int) {}}
	bs := driver(c)
	if _, err := parse(strings.NewReader(b.String()), "", ParseOptions{bursts: bs}); err != nil {
		t.Fatal(err)
	}
	// The root once, then again per batch; a batch now holds four times the
	// root's copied size, so the re-copies stay a fraction of the children.
	// Uncounted, every 4096 children re-copied all k bindings (about 10k).
	if limit := k + n/2; bs.viewCopies > limit {
		t.Fatalf("%d ancestor bindings and records copied for %d children (limit %d)",
			bs.viewCopies, n, limit)
	}
}
