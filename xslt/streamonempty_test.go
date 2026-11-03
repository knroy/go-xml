package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamOnEmpty: a sequence constructor with xsl:on-empty or
// xsl:on-non-empty runs on the core when one of them, or a sibling, is its
// consumer: the tree's output and error, the 8.4.4 order kept
// (streamonempty.go).
func TestStreamOnEmpty(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{
			`<o><xsl:copy-of select="doc/rec"/><xsl:on-empty><none/></xsl:on-empty></o>`,
			`<o><xsl:on-non-empty><h/></xsl:on-non-empty><xsl:copy-of select="doc/rec[@k='a']"/></o>`,
			`<o><xsl:on-non-empty select="count(doc/rec)"/><xsl:sequence select="'x'"/></o>`,
			`<o><xsl:on-non-empty select="count(doc/rec)"/><xsl:sequence select="()"/></o>`,
			`<o><xsl:sequence select="()"/><xsl:on-empty select="sum(doc/rec/@v)"/></o>`,
			`<o><e/><xsl:on-empty><xsl:copy-of select="doc/rec"/></xsl:on-empty></o>`,
			`<o><xsl:sequence select="''"/><xsl:on-empty><xsl:value-of select="count(//rec)"/></xsl:on-empty></o>`,
			`<o><xsl:on-non-empty><xsl:value-of select="doc/rec/@k"/></xsl:on-non-empty><xsl:if test="false()"><x/></xsl:if></o>`,
		} {
			coreCheck(t, coreSheet(body), dir, name+": "+body, true, TransformOptions{})
		}
	}
}
