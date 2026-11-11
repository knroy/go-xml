package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestSM03CopyEventsBindings: a streamed copy of a document whose root
// declares K namespaces gives the tree's output, and an element below the
// copy's top costs what it declares itself, not the K bindings it inherits
// (each of which was noted on it, at a scan of its parent's): the bytes a
// copy allocates per element do not grow with K.
func TestSM03CopyEventsBindings(t *testing.T) {
	dir := t.TempDir()
	body := `<out><xsl:copy-of select="."/></out>`
	perElem := func(k int) float64 {
		var ns strings.Builder
		for i := range k {
			fmt.Fprintf(&ns, ` xmlns:p%d="urn:%d"`, i, i)
		}
		alloc := func(m int) uint64 {
			src := `<doc xmlns="urn:d"` + ns.String() + `>` + strings.Repeat(`<e a="1"/>`, m) +
				`<e xmlns="urn:o" xmlns:q="urn:q"><q:f xmlns:p0="urn:0"/></e></doc>`
			if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
			var a, b goruntime.MemStats
			goruntime.ReadMemStats(&a)
			so, se, st := coreRun(coreSheet(body), dir, true, TransformOptions{})
			goruntime.ReadMemStats(&b)
			to, te, _ := coreRun(coreSheet(body), dir, false, TransformOptions{})
			if so != to || se != "" || te != "" || st.Reruns != 0 || st.Runs == [3]int64{} {
				t.Fatalf("k %d m %d: stream %q %s\ntree %q %s (trace %+v)", k, m, so[max(0, len(so)-300):], se, to[max(0, len(to)-300):], te, st)
			}
			return b.TotalAlloc - a.TotalAlloc
		}
		return float64(alloc(800)-alloc(200)) / 600
	}
	small, large := perElem(4), perElem(256)
	if large > 2*small+512 {
		t.Errorf("bytes per copied element: %.0f under 256 bindings, %.0f under 4", large, small)
	}
}
