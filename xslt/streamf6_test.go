package xslt

import (
	"testing"
)

// groupF6Doc is the document of the group rows: four groups by h.
const groupF6Doc = `<doc><s><h>1</h><p>a</p><p>b</p><h>2</h><p>c</p><h>3</h><h>4</h><p>d</p></s></doc>`

// groupF6Check runs each stylesheet of declarations over groupF6Doc,
// streamed and on the tree: the same output, no posture guard hit.
func groupF6Check(t *testing.T, sheets []string) {
	t.Helper()
	for _, d := range sheets {
		sheet := mainSheet(d)
		so, st := mainRun(sheet, groupF6Doc, true, TransformOptions{})
		to, _ := mainRun(sheet, groupF6Doc, false, TransformOptions{})
		if so != to || st.Guards != 0 {
			t.Errorf("%s:\nstream %s (guards %d)\ntree   %s", d, so, st.Guards, to)
		}
	}
}

// TestStreamGroupOuterReadsNoGroup: a split in a streamed group whose outer
// still calls current-group() -- beside copy-of(.) in a map, or of the whole
// group in a variable -- is refused: the outer runs on the tree's code,
// where the group is the posture guard's mark (sx-MapExpr-101..104).
func TestStreamGroupOuterReadsNoGroup(t *testing.T) {
	head := `<xsl:mode streamable="yes" on-no-match="shallow-copy"/>` +
		`<xsl:template match="s"><xsl:copy><xsl:for-each-group select="*" group-starting-with="h">`
	tail := `</xsl:for-each-group></xsl:copy></xsl:template>`
	groupF6Check(t, []string{
		head + `<xsl:variable name="m" as="item()" select="map { 1 : copy-of(.), 2 : copy-of(current-group()[position() gt 1]) }"/>` +
			`<xsl:choose><xsl:when test="$m?2"><div><xsl:apply-templates select="$m?*"/></div></xsl:when>` +
			`<xsl:otherwise><xsl:apply-templates select="$m?*"/></xsl:otherwise></xsl:choose>` + tail,
		head + `<xsl:variable name="m" as="item()" select="map { 1 : copy-of(.), 2 : copy-of(current-group()[position() gt 1]) }"/>` +
			`<g><xsl:copy-of select="$m?*"/></g>` + tail,
		head + `<xsl:variable name="m" select="copy-of(current-group())"/><g><xsl:copy-of select="$m"/></g>` + tail,
	})
}
