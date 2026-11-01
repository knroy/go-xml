package xpath

import (
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// Once the sink has refused a write the rest of the sequence is wasted work,
// so fn:serialize must stop at the first refusal instead of walking every item.
func TestSerializeStopsAtTheFirstRefusal(t *testing.T) {
	const items = 100000
	seq := make(xdm.Sequence, items)
	for i := range seq {
		seq[i] = xdm.NewInteger(int64(i))
	}
	opts := xdm.NewMapBuilder()
	if err := opts.Set(xdm.NewString("item-separator"), xdm.One(xdm.NewString(strings.Repeat("s", 1<<10)))); err != nil {
		t.Fatal(err)
	}
	ctx := nearlyExhausted(t, 8<<10, 16)
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	_, err := callFn(t, ctx, "serialize", seq, xdm.One(opts.Build()))
	runtime.ReadMemStats(&b)
	wantRefused(t, err, "")
	if n := b.Mallocs - a.Mallocs; n > items/10 {
		t.Errorf("%d allocations after the refusal; the item loop kept going", n)
	}
}
