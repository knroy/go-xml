package xslt

import (
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamMainLiteralGlobals: a global whose content is literal text or
// literal result elements does not keep TransformReader from streaming; nor,
// since L7, does one reading the context item: the global context item is
// absent for a streamed input (streamglobal_l7.go), XPDY0002 in both legs.
func TestStreamMainLiteralGlobals(t *testing.T) {
	mode := `<xsl:mode streamable="yes"/><xsl:template match="rec"><r k="{@k}" q="{$q}"><xsl:copy-of select="$t"/></r></xsl:template>`
	cases := []struct {
		globals string
		stream  bool
	}{
		{`<xsl:variable name="q">"</xsl:variable><xsl:param name="t"><root><list a="1"/></root></xsl:param>`, true},
		{`<xsl:variable name="q"><xsl:text>x</xsl:text></xsl:variable><xsl:variable name="t"/>`, true},
		{globalItemRow + `<xsl:variable name="q"><xsl:value-of select="count(//rec)"/></xsl:variable><xsl:variable name="t"/>`, true},
		{globalItemRow + `<xsl:variable name="q">q</xsl:variable><xsl:variable name="t"><a n="{name(/*)}"/></xsl:variable>`, true},
		{globalItemRow + `<xsl:variable name="q" expand-text="yes">{count(//rec)}</xsl:variable><xsl:variable name="t"/>`, true},
		{globalItemRow + `<xsl:variable name="q" select="count(//rec)"/><xsl:variable name="t"/>`, true},
	}
	for i, c := range cases {
		sheet := mainSheet(c.globals + mode)
		so, st := mainRun(sheet, coreDocs["flat"], true, TransformOptions{})
		to, _ := mainRun(sheet, coreDocs["flat"], false, TransformOptions{})
		if so != to {
			t.Errorf("case %d:\nstream %q\ntree   %q", i, so, to)
		}
		if got := st.Runs[streamtrace.Main] > 0; got != c.stream {
			t.Errorf("case %d: streamed %v, want %v", i, got, c.stream)
		}
		if st.Guards != 0 {
			t.Errorf("case %d: %d guard hits", i, st.Guards)
		}
	}
}
