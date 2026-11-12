package xslt

import (
	"strings"
	"testing"
)

// TestNamespaceAfterChildTree: XSLT 3.0 §5.7.1 makes a namespace node that
// follows content in an element's result sequence XTDE0410, in a tree result
// as much as in a streamed one. Saxon-HE 12.7 raises it for xsl:namespace
// after a child, and inside a variable too.
func TestNamespaceAfterChildTree(t *testing.T) {
	for name, body := range map[string]string{
		"xsl-namespace": `<out><a/><xsl:namespace name="q" select="'urn:q'"/></out>`,
		"copy-of":       `<out><a/><xsl:copy-of select="//*:name[1]/namespace::p"/></out>`,
		"sequence":      `<out><a/><xsl:sequence select="//*:name[1]/namespace::*[name() = 'p']"/></out>`,
		"attr-sequence": `<out><a/><xsl:sequence select="//rec[1]/@k"/></out>`,
		"in-variable":   `<xsl:variable name="v"><out><a/><xsl:namespace name="q" select="'urn:q'"/></out></xsl:variable><xsl:copy-of select="$v"/>`,
		// Paths other than xsl:sequence that put a node back on the builder
		// (xdmbuild.Builder.AppendNode) dropped the error.
		"where-populated":  `<out><a/><xsl:where-populated><xsl:attribute name="x">1</xsl:attribute></xsl:where-populated></out>`,
		"on-non-empty-sel": `<out><a/><xsl:on-non-empty select="//rec[1]/@k"/></out>`,
		"on-empty-ns":      `<out><a/><xsl:sequence select="//*:name[1]/namespace::*[name() = 'p']"/><xsl:on-empty><e/></xsl:on-empty></out>`,
	} {
		sheet := outSheet(t, ``, `<xsl:template match="/">`+body+`</xsl:template>`)
		for _, main := range []bool{false, true} {
			_, err := outRun(sheet, outDoc, main, false, TransformOptions{})
			if err == nil || !strings.Contains(err.Error(), "XTDE0410") {
				t.Errorf("%s (main %v): err %v, want XTDE0410", name, main, err)
			}
		}
	}
	// Before any child it is the ordinary way to declare one.
	sheet := outSheet(t, ``, `<xsl:template match="/"><out><xsl:namespace name="q" select="'urn:q'"/><a/></out></xsl:template>`)
	if got, err := outRun(sheet, outDoc, false, false, TransformOptions{}); err != nil || !strings.Contains(got, `xmlns:q="urn:q"`) {
		t.Errorf("namespace before child: %q, %v", got, err)
	}
}

// TestTryCatchesAttrAfterContent: an attribute an xsl:try returns into an
// element that already has content is XTDE0410 (§5.7.1) caught by that try,
// whose output 8.3.1 has "written to the result tree during the execution of
// xsl:try"; the rollback discards the body's output. Saxon-HE 12.7 catches it
// there too. The shape is DocBook xslTNG's titles.xsl, which adds a title's
// attributes inside a try after a label (fit.001.xml).
func TestTryCatchesAttrAfterContent(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"label-then-attr", `<h2>Chapter <xsl:try><xsl:sequence select="//rec[1]/@k"/><xsl:catch errors="err:XTDE0420"/><xsl:catch><c code="{$err:code}"/></xsl:catch></xsl:try></h2>`,
			`<h2>Chapter <c code="err:XTDE0410"/></h2>`},
		{"body-content-then-attr", `<out><xsl:try><x/><xsl:attribute name="a">1</xsl:attribute><xsl:catch><c/></xsl:catch></xsl:try></out>`,
			`<out><c/></out>`},
		{"ns-after-content", `<out><a/><xsl:try><xsl:namespace name="q" select="'urn:q'"/><xsl:catch><c/></xsl:catch></xsl:try></out>`,
			`<out><a/><c/></out>`},
		{"empty-text-is-no-content", `<out><xsl:value-of select="''"/><xsl:try><xsl:sequence select="//rec[1]/@k"/><xsl:catch><c/></xsl:catch></xsl:try></out>`,
			`<out k="1"/>`},
	} {
		sheet := compileString(t, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" `+
			`xmlns:err="http://www.w3.org/2005/xqt-errors" exclude-result-prefixes="err">`+
			`<xsl:template match="/">`+c.body+`</xsl:template></xsl:stylesheet>`)
		for _, main := range []bool{false, true} {
			got, err := outRun(sheet, `<doc><rec k="1"/></doc>`, main, false, TransformOptions{})
			if err != nil {
				t.Errorf("%s (main %v): %v", c.name, main, err)
			} else if !strings.Contains(got, c.want) {
				t.Errorf("%s (main %v): got %q, want %q", c.name, main, got, c.want)
			}
		}
	}
}
