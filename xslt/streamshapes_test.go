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
	// xsl:if / xsl:when whose test reads the context; the bodies motionless.
	{`<o><xsl:if test="doc/rec"><y n="{1}"/></xsl:if></o>`, true},
	{`<o><xsl:if test="rec">no</xsl:if></o>`, true},
	{`<o><xsl:if test="doc/rec"><y/></xsl:if><xsl:if test="rec">no</xsl:if></o>`, false}, // two consumers
	{`<o><xsl:if test="//sub[@x = '3']">yes</xsl:if></o>`, true},
	{`<xsl:for-each select="/doc/rec"><xsl:if test="sub"><r k="{@k}"/></xsl:if></xsl:for-each>`, true},
	{`<xsl:choose><xsl:when test="$c">a</xsl:when><xsl:when test="count(//rec) gt 1"><many/></xsl:when><xsl:otherwise><few/></xsl:otherwise></xsl:choose>`, true},
	{`<o><xsl:if test="data(//rec/@v)">x</xsl:if></o>`, true}, // EBV of several values: FORG0006
	{`<o><xsl:if test="not(//rec[@k = 'zz'])"><xsl:value-of select="name()"/></xsl:if></o>`, true},
	{`<o><xsl:if test="doc/rec"><xsl:value-of select="count(//sub)"/></xsl:if></o>`, false},
	{`<xsl:choose><xsl:when test="//rec">a</xsl:when><xsl:when test="//sub">b</xsl:when></xsl:choose>`, false},
	{`<xsl:choose><xsl:when test="//rec">a</xsl:when><xsl:otherwise><xsl:value-of select="count(//sub)"/></xsl:otherwise></xsl:choose>`, false},
	// Text value templates: motionless parts, or one consuming part.
	{`<o xsl:expand-text="yes">{count(//rec)}</o>`, true},
	{`<o xsl:expand-text="yes">n={count(//rec)} k={1 + 1}, {name()}</o>`, true},
	{`<xsl:for-each select="//rec" expand-text="yes"><r>{@k}:{position()}</r></xsl:for-each>`, true},
	{`<xsl:for-each select="/doc/rec" expand-text="yes"><xsl:text>[{@k}]</xsl:text><xsl:value-of select="."/></xsl:for-each>`, true},
	{`<o>{literal}<xsl:value-of select="count(//rec)"/></o>`, true},
	{`<o xsl:expand-text="yes">{{x}}<xsl:value-of select="count(//rec)"/></o>`, true},
	{`<o xsl:expand-text="yes">{count(//rec)}{sum(//rec/@v)}</o>`, false},
	{`<o xsl:expand-text="yes">{count(//rec)}<xsl:value-of select="count(//sub)"/></o>`, false},
	{`<xsl:for-each select="/doc/rec" expand-text="yes"><xsl:text>[{sub}]</xsl:text></xsl:for-each>`, false},
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
