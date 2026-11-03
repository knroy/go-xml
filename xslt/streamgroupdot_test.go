package xslt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamGroupDotPattern: group-starting-with="." and group-ending-with="."
// (every item matches: a group per item) stream, and match the tree.
func TestStreamGroupDotPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["flat"]), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`<xsl:for-each-group select="doc/rec" group-ending-with="."><g n="{position()}"><xsl:copy-of select="current-group()"/></g></xsl:for-each-group>`,
		`<xsl:for-each-group select="doc/*" group-starting-with="."><g><xsl:value-of select="count(current-group())"/></g></xsl:for-each-group>`,
	} {
		want, werr, _ := coreRun(coreSheet(body), dir, false, TransformOptions{})
		got, gerr, tr := coreRun(coreSheet(body), dir, true, TransformOptions{})
		if got != want || gerr != werr {
			t.Errorf("%s:\n got %q %s\nwant %q %s", body, got, gerr, want, werr)
		}
		if tr.Runs[streamtrace.Core] == 0 || tr.Reruns != 0 || tr.Guards != 0 {
			t.Errorf("%s: not streamed: %+v", body, tr)
		}
	}
}
