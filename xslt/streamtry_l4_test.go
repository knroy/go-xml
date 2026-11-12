package xslt

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// TestStreamTryApplyMatchesTree: an xsl:try whose body applies templates
// that cannot fail but by the built-in rules (si-try-200, mode-1426)
// streams through TransformReader and catches what the tree catches; one
// reaching a rule that may fail is left to the tree.
func TestStreamTryApplyMatchesTree(t *testing.T) {
	opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}}
	cases := []struct {
		decls  string
		stream bool
	}{
		// Inside <out>: at the top of the principal result the built-in
		// shallow-copy of a leaf is refused on its own (971e7955).
		{`<xsl:mode on-no-match="shallow-copy" streamable="yes"/>
		  <xsl:template match="/"><out><xsl:try><xsl:apply-templates/><xsl:catch/></xsl:try></out></xsl:template>`, true},
		{`<xsl:mode on-no-match="fail" streamable="yes"/>
		  <xsl:template match="/"><xsl:apply-templates/></xsl:template>
		  <xsl:template match="*"><xsl:copy><xsl:apply-templates/></xsl:copy></xsl:template>
		  <xsl:template match="text()[not(parent::rec)]"><xsl:value-of select="."/></xsl:template>
		  <xsl:template match="rec"><xsl:copy><xsl:try><xsl:apply-templates/>
		    <xsl:catch errors="err:XTDE0555" xmlns:err="http://www.w3.org/2005/xqt-errors"><c>caught</c></xsl:catch>
		  </xsl:try></xsl:copy></xsl:template>`, true},
		{`<xsl:mode on-no-match="fail" streamable="yes"/>
		  <xsl:template match="/"><out><xsl:try><xsl:apply-templates/><xsl:catch><c/></xsl:catch></xsl:try></out></xsl:template>
		  <xsl:template match="rec | sub"><r><xsl:apply-templates/></r></xsl:template>`, true},
		// A rule that may fail: refused.
		{`<xsl:mode on-no-match="shallow-copy" streamable="yes"/>
		  <xsl:template match="/"><xsl:try><xsl:apply-templates/><xsl:catch><c/></xsl:catch></xsl:try></xsl:template>
		  <xsl:template match="rec"><r><xsl:value-of select="xs:integer(@v) idiv 0"/></r></xsl:template>`, false},
	}
	for name, doc := range mainDocs {
		for ci, c := range cases {
			sheet := mainSheet(c.decls)
			so, st := mainRun(sheet, doc, true, opts)
			to, _ := mainRun(sheet, doc, false, opts)
			where := fmt.Sprintf("doc %s, case %d", name, ci)
			if so != to {
				t.Errorf("%s:\nstreamed %s\ntree     %s", where, so, to)
			}
			if st.Guards != 0 {
				t.Errorf("%s: %d guard hits", where, st.Guards)
			}
			if m := st.Runs[streamtrace.Main]; c.stream != (m != 0) {
				t.Errorf("%s: %d main runs, want streamed %v", where, m, c.stream)
			}
		}
	}
}
