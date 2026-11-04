package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestStreamGroundIdentityMatchesTree: a loop body using is, union,
// intersect or except on its match runs on a grounded copy, which has the
// original's identity and order, byte-equal to the tree (the shapes of
// sx-union-129, sx-except-140/141, sx-intersect-140, sf-current-002).
func TestStreamGroundIdentityMatchesTree(t *testing.T) {
	bodies := []string{
		`<out><xsl:for-each select="doc/rec[1]"><xsl:value-of select="(. | descendant-or-self::*) ! local-name()"/></xsl:for-each></out>`,
		`<out><xsl:for-each select="doc/rec[1]"><xsl:value-of select="(. except descendant-or-self::*) ! local-name()"/></xsl:for-each></out>`,
		`<out><xsl:for-each select="doc/rec[1]"><xsl:value-of select="(descendant-or-self::* except .) ! local-name()"/></xsl:for-each></out>`,
		`<out><xsl:for-each select="doc/rec[1]"><xsl:value-of select="(. intersect descendant-or-self::*) ! local-name()"/></xsl:for-each></out>`,
		`<out><xsl:for-each select="head(doc/rec)"><xsl:copy-of select="*[.. is current()]"/></xsl:for-each></out>`,
	}
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, b := range bodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, b)
				streamed += coreCheck(t, coreSheetWith(d, b), dir, where, true, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}
