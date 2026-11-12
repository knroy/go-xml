package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamErrorPathMemory: a container that fails at the end of a large
// document (big transactions, the last value not a decimal) holds next to
// nothing while streaming up to the error. FORG0001 is a final family
// (finalCodes): the streamed error is the result, the tree's code, with no
// re-run, and the peak live heap stays a fraction of the tree's.
func TestStreamErrorPathMemory(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("measures the live heap")
	}
	var b strings.Builder
	b.WriteString("<transactions>")
	for i := range 80000 {
		fmt.Fprintf(&b, `<transaction value="%d.%02d" date="2006-02-%02d"><memo>m%d</memo></transaction>`, i%500, i%100, 1+i%28, i%10)
	}
	b.WriteString(`<transaction value="n/a" date="2006-03-01"/></transactions>`)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	sheet := coreSheet(`<out><xsl:value-of select="sum(//transaction/xs:decimal(@value))"/></out>`)
	var se string
	var st streamtrace.Counts
	core := splitPeak(func() { _, se, st = coreRun(sheet, dir, true, TransformOptions{}) })
	var te string
	tree := splitPeak(func() { _, te, _ = coreRun(sheet, dir, false, TransformOptions{}) })
	t.Logf("streamed %.1f MB, tree %.1f MB", float64(core)/1e6, float64(tree)/1e6)
	if te == "" || se != te {
		t.Fatalf("errors: streamed %q, tree %q; want the tree's", se, te)
	}
	if st.Runs[streamtrace.Core] != 1 || st.Reruns != 0 || st.Fallbacks != 0 {
		t.Errorf("trace %+v: want one core run that failed, with no re-run", st)
	}
	if float64(core) > 0.2*float64(tree) {
		t.Errorf("streamed error path: peak live heap %.1f MB, tree %.1f MB (bound 20%%)", float64(core)/1e6, float64(tree)/1e6)
	}
}
