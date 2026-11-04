package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamForeignUnionOrder: a union of a streamed path with a variable's
// nodes orders them as the tree does whether the variable's tree was made
// before the document was opened or after (streamsplit_foreign.go).
func TestStreamForeignUnionOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["flat"]), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, where := range []string{"before", "after"} {
		before, after := `<xsl:variable name="ins" as="element()*"><a>A</a><b>B</b></xsl:variable><xsl:sequence select="count($ins)"/>`, ""
		if where == "after" {
			before, after = after, before
		}
		sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:template name="main">` + before + `<top><xsl:source-document streamable="yes" href="doc.xml">` + after +
			`<out><xsl:copy-of select="//sub union $ins"/></out></xsl:source-document></top></xsl:template></xsl:stylesheet>`
		if coreCheck(t, sheet, dir, "variable "+where, true, TransformOptions{}) == 0 {
			t.Errorf("variable %s: not streamed", where)
		}
	}
}
