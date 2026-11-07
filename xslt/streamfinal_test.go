package xslt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// TestStreamFinalErrors: a container whose streamed body fails with a
// family of finalCodes (or mergeFinalCodes, for xsl:merge) returns the
// stream's error with no re-run on the tree, and it is the tree's error.
// Each family is then dropped from its list (the sabotage): the container
// re-runs, which the case must see, so every entry is shown to be live.
func TestStreamFinalErrors(t *testing.T) {
	dir := t.TempDir()
	doc := `<doc><rec n="3" v="x" bad="1" p="zz:e" w="0" empty="" ns="http://www.w3.org/2000/xmlns/"/><rec n="1" v="y"/></doc>`
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	acc := `<xsl:accumulator name="a" initial-value="0" streamable="yes"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>`
	merge := func(key, action string) string {
		return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:template name="main"><top><xsl:merge>
<xsl:merge-source for-each-source="'doc.xml'" streamable="yes" select="doc/rec"><xsl:merge-key select="` + key + `"/></xsl:merge-source>
<xsl:merge-action>` + action + `</xsl:merge-action></xsl:merge></top></xsl:template>
<xsl:template name="n"><xsl:value-of select="current-merge-group()"/></xsl:template>
<xsl:function name="Q{urn:f}k"><xsl:sequence select="current-merge-key()"/></xsl:function>
</xsl:stylesheet>`
	}
	for _, c := range []struct {
		code, sheet string
		merge       bool
	}{
		{"XPDY0050", coreSheet(`<xsl:value-of select="data(//rec/@v) treat as xs:integer*"/>`), false},
		{"FOTY0013", coreSheet(`<xsl:value-of select="data((//rec/@v, true#0))"/>`), false},
		{"FORG0001", coreSheet(`<xsl:value-of select="sum(//rec/xs:decimal(@v))"/>`), false},
		{"FORG0003", coreSheet(`<xsl:value-of select="string(zero-or-one(//rec/@n))"/>`), false},
		{"FORG0004", coreSheet(`<xsl:value-of select="count(one-or-more(//rec/@none))"/>`), false},
		{"FORG0005", coreSheet(`<xsl:value-of select="string(exactly-one(//rec/@n))"/>`), false},
		{"FORG0006", coreSheet(`<xsl:value-of select="sum(//rec/@v ! string(.))"/>`), false},
		{"FOCH0001", coreSheet(`<xsl:value-of select="codepoints-to-string(//rec/@w ! xs:integer(.))"/>`), false},
		{"FOCH0002", coreSheet(`<xsl:value-of select="distinct-values(//rec/@v, 'urn:no-such-collation')"/>`), false},
		{"FOAR0001", coreSheet(`<xsl:value-of select="sum(//rec/@n ! (xs:integer(.) idiv 0))"/>`), false},
		{"XTDE0820", coreSheet(`<xsl:for-each select="//rec"><xsl:element name="{@bad}"/></xsl:for-each>`), false},
		{"XTDE0830", coreSheet(`<xsl:for-each select="//rec"><xsl:element name="{@p}"/></xsl:for-each>`), false},
		{"XTDE0835", coreSheet(`<xsl:for-each select="//rec"><xsl:element name="e" namespace="{@ns}"/></xsl:for-each>`), false},
		{"XTDE0850", coreSheet(`<xsl:for-each select="//rec"><e><xsl:attribute name="{@bad}"/></e></xsl:for-each>`), false},
		{"XTDE0855", coreSheet(`<xsl:for-each select="//rec"><e><xsl:attribute name="xmlns{@empty}"/></e></xsl:for-each>`), false},
		{"XTDE0860", coreSheet(`<xsl:for-each select="//rec"><e><xsl:attribute name="{@p}"/></e></xsl:for-each>`), false},
		{"XTDE0865", coreSheet(`<xsl:for-each select="//rec"><e><xsl:attribute name="a" namespace="{@ns}"/></e></xsl:for-each>`), false},
		{"XTDE0930", coreSheet(`<xsl:for-each select="//rec"><e><xsl:namespace name="p" select="string(@empty)"/></e></xsl:for-each>`), false},
		{"XTDE0450", coreSheet(`<xsl:for-each select="//rec"><e><xsl:sequence select="true#0"/></e></xsl:for-each>`), false},
		{"XTTE3180", coreSheet(`<xsl:copy select="//rec/@n"/>`), false},
		{"XTTE0570", coreSheet(`<xsl:for-each select="//rec"><xsl:variable name="x" as="xs:integer" select="string(@v)"/><xsl:value-of select="$x"/></xsl:for-each>`), false},
		{"XTTE3375", coreSheet(`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:sequence select="//rec/@v/string()"/></xsl:map></xsl:variable><xsl:value-of select="map:size($m)" xmlns:map="http://www.w3.org/2005/xpath-functions/map"/>`), false},
		{"XTDE3365", coreSheet(`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:for-each select="//rec"><xsl:map-entry key="'k'" select="1"/></xsl:for-each></xsl:map></xsl:variable><xsl:value-of select="map:size($m)" xmlns:map="http://www.w3.org/2005/xpath-functions/map"/>`), false},
		{"XTDE3362", strings.Replace(coreSheetWith(acc, `<xsl:for-each select="//rec"><xsl:value-of select="accumulator-before('a')"/></xsl:for-each>`),
			`href="doc.xml"`, `href="doc.xml" use-accumulators=""`, 1), false},
		{"XTDE2220", merge("xs:integer(@n)", `<g/>`), true},
		{"XTDE3480", merge(`xs:integer(@n)" order="descending`, `<g><xsl:call-template name="n"/></g>`), true},
		{"XTDE3510", merge(`xs:integer(@n)" order="descending`, `<g xmlns:f="urn:f"><xsl:value-of select="f:k()"/></g>`), true},
	} {
		t.Run(c.code, func(t *testing.T) {
			_, te, _ := coreRun(c.sheet, dir, false, TransformOptions{})
			_, se, st := coreRun(c.sheet, dir, true, TransformOptions{})
			if xdm.ErrorCode(errors.New(te)) != c.code {
				t.Fatalf("tree: %q, want %s", te, c.code)
			}
			if se != te || st.Streamed() == 0 || st.Reruns != 0 {
				t.Fatalf("streamed %q (trace %+v); want the tree's %q, streamed, no re-run", se, st, te)
			}
			list := finalCodes
			if c.merge {
				list = mergeFinalCodes
			}
			delete(list, c.code)
			defer func() { list[c.code] = true }()
			if _, se, st = coreRun(c.sheet, dir, true, TransformOptions{}); se != te || st.Reruns != 1 {
				t.Errorf("with %s dropped: %q, %d re-runs; want the re-run's %q", c.code, se, st.Reruns, te)
			}
		})
	}
}

// TestStreamOpenAndTypeCodes: the streamed container's own errors carry the
// tree's codes. A document that cannot be opened is FODC0002, final (the
// tree's load opens the same file); a document invalid against type= is
// XTTE1540, as the tree's assess raises it (seen with re-runs off: it still
// re-runs, the tree's text listing every failure).
func TestStreamOpenAndTypeCodes(t *testing.T) {
	dir := t.TempDir()
	sheet := coreSheet(`<xsl:value-of select="count(//rec)"/>`)
	_, te, _ := coreRun(sheet, dir, false, TransformOptions{})
	_, se, st := coreRun(sheet, dir, true, TransformOptions{})
	if xdm.ErrorCode(errors.New(te)) != "FODC0002" || xdm.ErrorCode(errors.New(se)) != "FODC0002" || st.Reruns != 0 {
		t.Errorf("missing document: streamed %q (%d re-runs), tree %q; want FODC0002 with no re-run", se, st.Reruns, te)
	}
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(inDocs["bad-value"]), 0o600); err != nil {
		t.Fatal(err)
	}
	sheet = inSheet(`type="listT"`, `<v><xsl:value-of select="count(list/item)"/></v>`)
	_, te, _ = coreRun(sheet, dir, false, TransformOptions{})
	streamtrace.SetNoRerun(true)
	_, se, st = coreRun(sheet, dir, true, TransformOptions{})
	streamtrace.SetNoRerun(false)
	if xdm.ErrorCode(errors.New(te)) != "XTTE1540" || xdm.ErrorCode(errors.New(se)) != "XTTE1540" || st.Reruns != 1 {
		t.Errorf("type=: streamed %q (%d re-runs), tree %q; want XTTE1540", se, st.Reruns, te)
	}
}
