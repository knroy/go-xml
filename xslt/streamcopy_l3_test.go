package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamDocCopyEvents: a copy of the document, or of the document
// element, at the document node runs on the core, read off the cursor as
// events (into an open element) or built once (anywhere else), and its
// string value is read off the text events: the tree's output and error
// (streamcopy_l3.go).
func TestStreamDocCopyEvents(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, decls := range coreDecls {
			for _, body := range []string{
				`<o><h/><xsl:copy-of select="."/><t/></o>`,
				`<o><xsl:sequence select="copy-of(.)"/></o>`,
				`<o><xsl:sequence select="snapshot(.)"/></o>`,
				`<o><xsl:sequence select="."/></o>`,
				`<o><xsl:value-of select="."/></o>`,
				`<o><xsl:value-of select="string(.)"/></o>`,
				`<o><xsl:copy-of select="*"/></o>`,
				`<o><xsl:copy-of select="node()"/></o>`,
				`<xsl:variable name="d" select="copy-of(.)"/><o><xsl:value-of select="count($d//rec), name($d/*)"/></o>`,
				`<xsl:variable name="d" as="node()"><xsl:copy-of select="."/></xsl:variable><o><xsl:copy-of select="$d/*"/></o>`,
			} {
				coreCheck(t, coreSheetWith(decls, body), dir, name+": "+decls+body, true, TransformOptions{})
			}
		}
	}
}
