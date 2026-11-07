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
// nothing while streaming up to the error. With re-runs off
// (streamtrace.SetNoRerun, tests/streamdiff's norerun leg) the streamed
// error is the result, the tree's code, and the peak live heap stays a
// fraction of the tree's; with them on, the re-run reads the tree (logged).
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
	var re string
	rerun := splitPeak(func() { _, re, _ = coreRun(sheet, dir, true, TransformOptions{}) })
	var se string
	var st streamtrace.Counts
	streamtrace.SetNoRerun(true)
	core := splitPeak(func() { _, se, st = coreRun(sheet, dir, true, TransformOptions{}) })
	streamtrace.SetNoRerun(false)
	var te string
	tree := splitPeak(func() { _, te, _ = coreRun(sheet, dir, false, TransformOptions{}) })
	t.Logf("streamed %.1f MB, with the re-run %.1f MB, tree %.1f MB", float64(core)/1e6, float64(rerun)/1e6, float64(tree)/1e6)
	code := func(e string) string { c, _, _ := strings.Cut(e, ":"); return c }
	if te == "" || code(se) != code(te) || code(re) != code(te) {
		t.Fatalf("errors: streamed %q, re-run %q, tree %q; want the tree's code", se, re, te)
	}
	if st.Runs[streamtrace.Core] != 1 || st.Reruns != 1 || st.Fallbacks != 0 {
		t.Errorf("trace %+v: want one core run that failed (one re-run counted, not run)", st)
	}
	if float64(core) > 0.2*float64(tree) {
		t.Errorf("streamed error path: peak live heap %.1f MB, tree %.1f MB (bound 20%%)", float64(core)/1e6, float64(tree)/1e6)
	}
}
