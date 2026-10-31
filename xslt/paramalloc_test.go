package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// paramCallAllocs is the allocations per xsl:call-template of a stylesheet
// that makes 200 calls passing WITH, less those of one making the calls with
// no parameters at all.
func paramCallAllocs(t *testing.T, with string) float64 {
	t.Helper()
	run := func(with string) float64 {
		src := strings.ReplaceAll(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:template name="main"><xsl:for-each select="1 to 200"><xsl:call-template name="t">WITH</xsl:call-template></xsl:for-each></xsl:template>
<xsl:template name="t"><xsl:param name="a" select="0"/><xsl:param name="b" select="0"/><xsl:param name="c" tunnel="yes" select="0"/></xsl:template>
</xsl:stylesheet>`, "WITH", with)
		d, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		st, err := Compile(d.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return testing.AllocsPerRun(5, func() {
			if _, err := st.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main"}); err != nil {
				t.Fatal(err)
			}
		})
	}
	return (run(with) - run("")) / 200
}

// Parameters an invocation supplies are a small slice, not two maps: the two
// cost a header and a bucket group each per call, the tunnel one even when no
// tunnel parameter was passed.
func TestWithParamAllocs(t *testing.T) {
	two := `<xsl:with-param name="a" select="1"/><xsl:with-param name="b" select="2"/>`
	// One for the slice; the two maps cost three.
	if got := paramCallAllocs(t, two); got > 2 {
		t.Errorf("two xsl:with-param cost %.1f allocations per call, want at most 2", got)
	}
}
