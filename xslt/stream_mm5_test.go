package xslt

import (
	"os"
	"path/filepath"
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
// keeps no node of the parse (accumulator-069s), and a lookup in such a
// value, accumulator-before('m')(k), reads nothing of the stream
// (accumulator-077s): TransformReader streams both, with the tree's result
// or error.
func TestStreamAccumMapTyped(t *testing.T) {
	const accs = `<xsl:mode streamable="yes" use-accumulators="#all"/>
	<xsl:accumulator name="n" as="xs:integer" initial-value="0" streamable="yes">
	  <xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>
	<xsl:accumulator name="m" as="map(xs:string, xs:integer*)" initial-value="map{}" streamable="yes">
	  <xsl:accumulator-rule match="rec" select="let $k := string(@k), $c := accumulator-before('n')
	    return map:put($value, $k, ($value($k), $c))"/></xsl:accumulator>`
	mm5Rows(t, mm5Doc, []struct{ name, decls string }{
		{"map size at the end", accs + `<xsl:template match="/"><o><xsl:apply-templates/><t v="{map:size(accumulator-after('m'))}"/></o></xsl:template>`},
		{"map value read at the end", accs + `<xsl:template match="/"><o><xsl:apply-templates/><t v="{accumulator-after('m')('a')}"/></o></xsl:template>`},
		{"lookup by an attribute", accs + `<xsl:template match="rec"><r v="{accumulator-before('m')(string(@k))}"/></xsl:template>`},
		{"a key missing", accs + `<xsl:template match="rec"><r v="{accumulator-before('m')(string(@v))}"/></xsl:template>`},
		{"lookup error", accs + `<xsl:template match="rec"><r v="{accumulator-before('n')(1)}"/></xsl:template>`},
	})
}

// TestStreamAccumQuantifiedPattern: an accumulator rule whose pattern's
// predicate is some/every over the attributes is decided at the start tag
// (accumulator-034s).
func TestStreamAccumQuantifiedPattern(t *testing.T) {
	const accs = `<xsl:mode streamable="yes" use-accumulators="#all"/>
	<xsl:accumulator name="q" as="xs:integer" initial-value="0" streamable="yes">
	  <xsl:accumulator-rule match="rec[every $a in data(@*) satisfies $a != '2']" select="$value + 1"/>
	  <xsl:accumulator-rule match="rec[some $a in data(@*) satisfies $a = 'c']" select="$value + 10"/></xsl:accumulator>`
	mm5Rows(t, mm5Doc, []struct{ name, decls string }{
		{"every and some", accs + `<xsl:template match="rec"><r q="{accumulator-before('q')}"/></xsl:template>`},
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

// TestStreamMergeCollectedPathKeys: a collected merge source keyed by a
// path, a streamed one by a string (merge-081). The merge streams when the
// collected keys are untyped; typed ones (an xs:integer attribute here,
// which a string comparison orders differently) send it to the tree
// before anything is read.
func TestStreamMergeCollectedPathKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.xml"), []byte(`<log><e t="1" v="a1"/><e t="10" v="a10"/><e t="9" v="a9"/></log>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "n.xml"), []byte(`<log><e t="2" v="n2"/><e t="9" v="n9"/><e t="11" v="n11"/></log>`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, vals, attr, key, doc string
		stream                     bool
	}{
		{"untyped", "'1', '10', '9'", `<xsl:attribute name="t" select="."/>`, "string(@t)", "a.xml", true},
		{"untyped, a path", "'1', '10', '9'", `<xsl:attribute name="t" select="."/>`, "@t", "a.xml", true},
		// Typed keys go to the tree: against xs:string, XTTE2230; against
		// an untyped path, the tree orders them as numbers, a merge of heads
		// taking them for strings would not (its groups differ).
		{"typed", "'1', '9', '10'", `<xsl:attribute name="t" type="xs:integer" select="."/>`, "string(@t)", "a.xml", false},
		{"typed, a path", "'1', '9', '10'", `<xsl:attribute name="t" type="xs:integer" select="."/>`, "@t", "a.xml", false},
		{"typed, a path, in order", "'1', '9', '10'", `<xsl:attribute name="t" type="xs:integer" select="."/>`, "@t", "n.xml", false},
	} {
		sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:template name="main"><xsl:variable name="v"><xsl:for-each select="(` + c.vals + `)"><xsl:element name="e" validation="preserve">` + c.attr + `</xsl:element></xsl:for-each></xsl:variable>
<out><xsl:merge>
 <xsl:merge-source name="B" select="$v/e"><xsl:merge-key select="@t"/></xsl:merge-source>
 <xsl:merge-source name="A" for-each-source="'` + c.doc + `'" select="log/e"><xsl:merge-key select="` + c.key + `"/></xsl:merge-source>
 <xsl:merge-action><g k="{current-merge-key()}" n="{count(current-merge-group())}" ty="{current-merge-group('B')/@t ! (data(.) instance of xs:integer)}"/></xsl:merge-action>
</xsl:merge></out></xsl:template></xsl:stylesheet>`
		so, se, st := coreRun(sheet, dir, true, TransformOptions{})
		to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
		if so != to || se != te {
			t.Errorf("%s:\nstream %q %q\ntree   %q %q", c.name, so, se, to, te)
		}
		if ran := st.Runs[streamtrace.Merge] > 0 && st.Reruns == 0; ran != c.stream {
			t.Errorf("%s: streamed %v, want %v", c.name, ran, c.stream)
		}
	}
}
