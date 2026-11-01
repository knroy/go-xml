package xdm

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

// TestParseLongTextAllocation bounds what parsing long text values allocates
// against the document's size: the decoder's buffer is grown to fit, in exact
// steps, and handed to the tree only when no later value will want it.
func TestParseLongTextAllocation(t *testing.T) {
	for _, c := range []struct {
		name     string
		n, size  int
		maxRatio float64
	}{
		{"one 1 MiB value", 1, 1 << 20, 1.3},
		{"35 values of 700 kB", 35, 700000, 1.1},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := strings.Repeat("QUJD", c.size/4)
			var b bytes.Buffer
			b.WriteString("<r>")
			for i := 0; i < c.n; i++ {
				b.WriteString("<v>" + v + "</v>")
			}
			b.WriteString("</r>")
			doc := b.Bytes()
			var m0, m1 runtime.MemStats
			runtime.ReadMemStats(&m0)
			tr, err := Parse(bytes.NewReader(doc), ParseOptions{})
			runtime.ReadMemStats(&m1)
			if err != nil {
				t.Fatal(err)
			}
			runtime.KeepAlive(tr)
			if r := float64(m1.TotalAlloc-m0.TotalAlloc) / float64(len(doc)); r > c.maxRatio {
				t.Errorf("parsing allocated %.2fx the document, want at most %.2fx", r, c.maxRatio)
			}
		})
	}
}
