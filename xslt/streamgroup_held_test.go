package xslt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamHeldGroupCap: a population grouped by key is held under the
// cap of streamhold.go: one that is the document element gives up at once
// and the tree answers (a fallback, the tree's output), never a copy of
// the whole document (streamgroup_held.go).
func TestStreamHeldGroupCap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(groupDocs["runs"]), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		sel      string
		fallback int64
	}{{"/*", 1}, {"/doc/t", 0}} {
		sheet := coreSheet(`<out><xsl:fork><xsl:for-each-group select="` + c.sel +
			`" group-by="name()"><g k="{current-grouping-key()}"><xsl:copy-of select="current-group()"/></g></xsl:for-each-group></xsl:fork></out>`)
		so, se, st := coreRun(sheet, dir, true, TransformOptions{})
		to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
		if so != to || se != te || st.Runs[streamtrace.Core] != 1 || st.Fallbacks != c.fallback || st.Reruns != 0 {
			t.Errorf("%s: core %q %q, tree %q %q, trace %+v", c.sel, so, se, to, te, st)
		}
	}
}
