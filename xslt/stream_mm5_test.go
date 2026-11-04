package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// mm5Rows runs each row's declarations through TransformReader and through
// the tree on doc: the same result or error, and a streamed run.
func mm5Rows(t *testing.T, doc string, rows []struct{ name, decls string }) {
	t.Helper()
	var why string
	traceMain = func(m, w string) {
		if m == "" {
			why = w
		}
	}
	defer func() { traceMain = nil }()
	for _, r := range rows {
		sheet := strings.Replace(mainSheet(r.decls), `exclude-result-prefixes="xs p"`,
			`xmlns:map="http://www.w3.org/2005/xpath-functions/map" exclude-result-prefixes="xs p map"`, 1)
		why = ""
		so, st := mainRun(sheet, doc, true, TransformOptions{})
		to, _ := mainRun(sheet, doc, false, TransformOptions{})
		if so != to {
			t.Errorf("%s:\nstreamed %s\ntree     %s", r.name, so, to)
		}
		if st.Runs[streamtrace.Main] == 0 {
			t.Errorf("%s: not streamed: %s", r.name, why)
		}
	}
}

const mm5Doc = `<d><rec k="a" v="1">t1</rec><rec k="b" v="1"/><rec k="a" v="2"><v>x</v></rec><x/><rec k="c" v="3"><v>y<s/></v></rec></d>`

// TestStreamTryCatchPrefixes: a catch with exclude-result-prefixes around
// an xsl:apply-templates that fails at a node with no rule (mode-1426).
func TestStreamTryCatchPrefixes(t *testing.T) {
	mm5Rows(t, mm5Doc, []struct{ name, decls string }{
		{"caught", `<xsl:mode streamable="yes" on-no-match="fail"/>
		  <xsl:template match="/"><xsl:apply-templates/></xsl:template>
		  <xsl:template match="*"><xsl:copy><xsl:apply-templates/></xsl:copy></xsl:template>
		  <xsl:template match="text()[not(parent::v)]"><xsl:value-of select="."/></xsl:template>
		  <xsl:template match="v"><xsl:copy><xsl:try><xsl:apply-templates/>
		    <xsl:catch errors="err:XTDE0555" xmlns:err="http://www.w3.org/2005/xqt-errors" exclude-result-prefixes="err"><c/></xsl:catch>
		  </xsl:try></xsl:copy></xsl:template>`},
	})
}
