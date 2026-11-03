package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// shapeDecls are the declarations the shape bodies use.
const shapeDecls = `<xsl:param name="c" select="false()"/>
<xsl:mode name="m" streamable="yes" on-no-match="deep-skip"/><xsl:template match="rec" mode="m"><xsl:value-of select="@k"/></xsl:template>
<xsl:function name="p:mk"><xsl:param name="s" as="xs:string"/><e><xsl:value-of select="$s"/></e><xsl:if test="$s = 'never'"><xsl:sequence select="1"/></xsl:if></xsl:function>`

// shapeBodies are container bodies of the expression shapes of
// streamshapes.go; stream says the core must take the container.
var shapeBodies = []struct {
	body   string
	stream bool
}{
	// P ! R: R per match, on the view or a grounded copy.
	{`<o><xsl:value-of select="count(//rec ! parse-xml('&lt;p a=''1''>' || @k || '&lt;/p>')/*)"/></o>`, true},
	{`<o><xsl:value-of select="boolean(outermost(//rec) ! p:mk(string(.)))"/></o>`, true},
	{`<o><xsl:value-of select="exists(outermost(//rec) ! parse-xml('&lt;p>' || . || '&lt;/p>')//text())"/></o>`, true},
	{`<o><xsl:value-of select="(/doc/rec ! p:mk(string(@k))) instance of element(e)+"/></o>`, true},
	{`<o><xsl:copy-of select="/doc/rec ! p:mk(string(.))"/></o>`, true},
	{`<o><xsl:value-of select="count(//sub ! (., .))"/></o>`, true},
	{`<o><xsl:value-of select="boolean(/doc/rec ! ('', parse-xml('&lt;p/>')))"/></o>`, true}, // EBV, not existence: FORG0006
	{`<o><xsl:value-of select="count(/doc/rec ! sub)"/></o>`, true},
	{`<o><xsl:value-of select="string-join(/doc/rec ! (sub, 'x') ! string(), ',')"/></o>`, true},
	{`<o><xsl:value-of select="string-join(/doc/rec/@k ! parse-xml('&lt;q>' || . || '&lt;/q>')/q, ',')"/></o>`, true},
	{`<o><xsl:value-of select="count(//rec ! sub)"/></o>`, false},                             // XTSE3430 (crawling, then consuming)
	{`<o><xsl:value-of select="string-join(/doc/rec ! (position(), sub), ',')"/></o>`, false}, // position() in R
	{`<o><xsl:value-of select="count(/doc/rec/sub ! (.., .))"/></o>`, false},                  // R climbs
	// tail(P): P's matches but the first.
	{`<o><xsl:value-of select="sum(tail(/doc/rec)/@v)"/></o>`, true},
	{`<o><xsl:value-of select="count(tail(//rec))"/></o>`, true},
	{`<o><xsl:value-of select="string-join(tail(tail(/doc/rec))/@k, ',')"/></o>`, true},
	{`<o><xsl:value-of select="sum((tail(/doc/rec/sub)/number(@x), 31))"/></o>`, true},
	{`<o><xsl:value-of select="count(tail(/doc/rec/@k))"/></o>`, false},       // P has a tail of its own
	{`<o><xsl:apply-templates select="tail(/doc/rec)" mode="m"/></o>`, false}, // a loop: no skip
	{`<o><xsl:for-each select="tail(//rec)"><xsl:value-of select="@k"/></xsl:for-each></o>`, false},
	// F(if (C) then A else B), C motionless: the choice hoisted.
	{`<o><xsl:value-of select="count(if ($c) then () else //rec)"/></o>`, true},
	{`<o><xsl:value-of select="sum(if (not($c)) then //rec/@v else //sub/@x)"/></o>`, true},
	{`<o><xsl:value-of select="string(count(if ($c) then //rec else /doc/rec/sub))"/></o>`, true},
}

// TestStreamShapesMatchTree: the shapes streamshapes.go adds to the split
// give the tree's output or error, and stream where they should.
func TestStreamShapesMatchTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range shapeBodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d+shapeDecls, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}
