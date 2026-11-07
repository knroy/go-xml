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
