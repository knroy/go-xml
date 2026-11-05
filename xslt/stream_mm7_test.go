package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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

// TestStreamSnapshotClimb: a path from snapshots of the stream's matches
// climbs into the snapshots' own ancestors, copied with their attributes
// and namespaces (sf-snapshot-0101c): the split grounds the matches, the
// outer makes the snapshots and runs the steps on the tree's code.
func TestStreamSnapshotClimb(t *testing.T) {
	dir := t.TempDir()
	doc := `<b:r xmlns:b="urn:b" xmlns:g="urn:g" g:id="R"><x a="1"><g:id c="s">one</g:id><y/></x><g:id c="t">two<z/></g:id><x><x><g:id/></x></x></b:r>`
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{
		`<xsl:sequence select="snapshot(//*:id)/self::*:id/ancestor::node()[last()]"/>`,
		`<xsl:sequence select="snapshot(//*:id)/ancestor::*[1]"/>`,
		`<n><xsl:value-of select="count(snapshot(//*:id)/ancestor-or-self::node())"/></n>`,
		`<xsl:copy-of select="snapshot(/*/*)/../@*"/>`,
		`<xsl:sequence select="snapshot(//x)/parent::*[@a = 1]"/>`,
		`<n><xsl:value-of select="snapshot(//*:id)/ancestor::node()[last()] ! (document-uri(.), base-uri(.))"/></n>`,
		`<n><xsl:value-of select="count(snapshot(//*:id)/ancestor::*/namespace::*)"/></n>`,
	} {
		coreCheck(t, coreSheet(body), dir, fmt.Sprint("row ", i), true, TransformOptions{})
	}
}
