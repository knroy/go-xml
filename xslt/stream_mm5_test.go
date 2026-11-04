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

// TestStreamAccumMapTyped: an accumulator whose @as is a map of atomics
// keeps no node of the parse (accumulator-069s): TransformReader streams
// it, with the tree's result.
func TestStreamAccumMapTyped(t *testing.T) {
	const accs = `<xsl:mode streamable="yes" use-accumulators="#all"/>
	<xsl:accumulator name="n" as="xs:integer" initial-value="0" streamable="yes">
	  <xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>
	<xsl:accumulator name="m" as="map(xs:string, xs:integer*)" initial-value="map{}" streamable="yes">
	  <xsl:accumulator-rule match="rec" select="let $k := string(@k), $c := accumulator-before('n')
	    return map:put($value, $k, ($value($k), $c))"/></xsl:accumulator>`
	mm5Rows(t, mm5Doc, []struct{ name, decls string }{
		{"map size at the end", accs + `<xsl:template match="/"><o><xsl:apply-templates/><t v="{map:size(accumulator-after('m'))}"/></o></xsl:template>`},
	})
}

// TestStreamApplyInMapTypedVariable: xsl:apply-templates in a variable
// typed a sequence of maps (si-fork-812): what rules make is kept, and a
// built-in rule's node is the type error in both modes.
func TestStreamApplyInMapTypedVariable(t *testing.T) {
	const body = `<xsl:template match="/"><xsl:variable name="v" as="map(xs:string, item()*)*"><xsl:apply-templates/></xsl:variable>
	  <o><xsl:for-each select="$v"><m k="{?k}" n="{?n}"/></xsl:for-each></o></xsl:template>`
	mm5Rows(t, mm5Doc, []struct{ name, decls string }{
		{"maps", `<xsl:mode streamable="yes"/>` + body + `<xsl:template match="d"><xsl:apply-templates select="rec"/></xsl:template>
		  <xsl:template match="rec"><xsl:map><xsl:map-entry key="'k'" select="string(@k)"/><xsl:map-entry key="'n'" select="xs:integer(@v)"/></xsl:map></xsl:template>`},
		{"a built-in rule's text node", `<xsl:mode streamable="yes"/>` + body + `<xsl:template match="rec[@k = 'b']"/>
		  <xsl:template match="rec"><xsl:map><xsl:map-entry key="'k'" select="string(@k)"/></xsl:map><xsl:apply-templates/></xsl:template>`},
	})
}

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
