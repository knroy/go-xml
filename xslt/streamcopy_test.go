package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamCopySelect: xsl:copy whose select consumes runs on the core,
// its body reading the copied node's content off the cursor; a select of
// several items is the tree's XTTE3180 (streamcopy.go).
func TestStreamCopySelect(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{
			`<o><xsl:copy select="doc"><xsl:copy-of select="rec"/></xsl:copy></o>`,
			`<o><xsl:copy select="doc/rec[1]"><n><xsl:value-of select="count(*)"/></n></xsl:copy></o>`,
			`<o><xsl:copy select="doc/rec[@k = 'b']"><xsl:attribute name="v" select="@v"/><xsl:value-of select="."/></xsl:copy></o>`,
			`<o><xsl:copy select="doc/other"><e/></xsl:copy></o>`,
			`<o><xsl:copy select="doc/rec"><e/></xsl:copy></o>`, // XTTE3180 where there are two
			`<o><xsl:copy select="*"><xsl:copy select="rec[2]"><xsl:copy-of select="sub"/></xsl:copy></xsl:copy></o>`,
		} {
			coreCheck(t, coreSheet(body), dir, name+": "+body, true, TransformOptions{})
		}
	}
}
