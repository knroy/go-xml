package xslt

import (
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A run of n adjacent atomics used to be joined by repeated concatenation,
// which copies the growing string n times and allocates O(n^2) bytes.
func TestJoinAdjacentAtomicsIsLinear(t *testing.T) {
	const n = 20000
	var seq xdm.Sequence
	seq = append(seq, xdm.NewString("a"))
	for i := 1; i < n; i++ {
		seq = append(seq, xdm.NewInteger(int64(i)))
	}
	elem := xdm.NewNode(xdm.KindElement, xdm.QName{Local: "e"}, "")
	seq = append(seq, elem, xdm.NewString("x"), xdm.NewString("y"))

	var before, after goruntime.MemStats
	goruntime.ReadMemStats(&before)
	out := joinAdjacentAtomics(seq)
	goruntime.ReadMemStats(&after)

	if len(out) != 3 {
		t.Fatalf("got %d items, want 3", len(out))
	}
	var want strings.Builder
	want.WriteString("a")
	for i := 1; i < n; i++ {
		want.WriteString(" " + xdm.NewInteger(int64(i)).String())
	}
	if got := out[0].(*xdm.Node).Value(); got != want.String() {
		t.Fatalf("first run differs: len %d, want %d", len(got), want.Len())
	}
	if out[1] != xdm.Item(elem) {
		t.Fatalf("node item not preserved")
	}
	if got := out[2].(*xdm.Node).Value(); got != "x y" {
		t.Fatalf("second run = %q", got)
	}
	// Linear joining allocates a few hundred KB here; the quadratic form
	// allocated about 1 GB.
	if d := after.TotalAlloc - before.TotalAlloc; d > 20<<20 {
		t.Fatalf("joinAdjacentAtomics allocated %d bytes for %d atomics", d, n)
	}
}
