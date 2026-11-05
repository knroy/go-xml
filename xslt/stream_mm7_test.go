package xslt

import "testing"

// TestStreamGroupCopiesParam: a parameter copy-of(current-group()) of an
// xsl:apply-templates over a tree not of the stream, in a streamed group,
// is read at the instruction from the group's members, one group's copies
// held at a time (si-group-040); TransformReader streams it with the
// tree's result.
func TestStreamGroupCopiesParam(t *testing.T) {
	const tail = `<xsl:template match="d"><xsl:param name="c" tunnel="yes"/><c n="{count($c)}"><xsl:copy-of select="$c"/></c></xsl:template>`
	const param = `<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:param name="t"><r><d/></r></xsl:param>`
	mm5Rows(t, mm5Doc, []struct{ name, decls string }{
		{"adjacent chunks", param + `<xsl:template match="/"><o><xsl:for-each-group select="d/*" group-adjacent="(position() - 1) idiv 2">` +
			`<xsl:apply-templates select="$t/*"><xsl:with-param name="c" select="copy-of(current-group())" tunnel="yes"/></xsl:apply-templates>` +
			`</xsl:for-each-group></o></xsl:template>` + tail},
		{"the document element's children, starting-with", param + `<xsl:template match="/*"><o><xsl:for-each-group select="*" group-starting-with="rec[@k = 'a']">` +
			`<g k="{current-grouping-key()}"/><xsl:apply-templates select="$t/*"><xsl:with-param name="c" select="copy-of(current-group())" tunnel="yes"/></xsl:apply-templates>` +
			`</xsl:for-each-group></o></xsl:template>` + tail},
	})
}

// TestStreamLoopCopiesSized: an xsl:for-each over copies of the stream's
// matches whose body reads last() collects the copies before the body
// runs, as the tree evaluates the select whole (si-group-065:
// current-group()!copy-of(), a group of the document element's children
// too); P!copy-of() is copy-of(P).
func TestStreamLoopCopiesSized(t *testing.T) {
	const mode = `<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`
	const body = `<i n="{name()}" p="{position()}" l="{last()}"><xsl:copy-of select="node()"/></i>`
	mm5Rows(t, mm5Doc, []struct{ name, decls string }{
		{"group of the document element's children", mode + `<xsl:template match="/*"><o><xsl:for-each-group select="*" group-adjacent="name()">` +
			`<g><xsl:for-each select="current-group()!copy-of()">` + body + `</xsl:for-each></g></xsl:for-each-group></o></xsl:template>`},
		{"copy-of of the group", mode + `<xsl:template match="/*"><o><xsl:for-each-group select="*" group-starting-with="x">` +
			`<g><xsl:for-each select="copy-of(current-group())">` + body + `</xsl:for-each></g></xsl:for-each-group></o></xsl:template>`},
		{"a path's copies", mode + `<xsl:template match="/"><o><xsl:for-each select="d/rec!copy-of()">` + body + `</xsl:for-each></o></xsl:template>`},
		{"an error after last()", mode + `<xsl:template match="/"><o><xsl:for-each select="d/rec/copy-of()">` +
			`<i l="{last()}" v="{xs:integer(@k)}"/></xsl:for-each></o></xsl:template>`},
	})
}
