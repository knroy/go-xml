package xslt

// The G3 grammar of TestStreamFuzz (streamshapes.go): P ! R making nodes or
// values, tail(P), a motionless choice inside a call, consuming xsl:if and
// xsl:when tests, arrays, maps and lookups over P, and text value templates
// with motionless or consuming parts, at the document node, in loop bodies,
// under wrappers. GOXSLT_STREAM_FUZZ_SHAPES=0 leaves them out.

import (
	"math/rand"
	"os"
	"strings"
)

var fuzzShapeShapes = os.Getenv("GOXSLT_STREAM_FUZZ_SHAPES") != "0"

// fuzzIsShapes decides, from a random stream of its own, whether the case
// of seed is one of fuzzMakeShapes's.
func fuzzIsShapes(seed int64) bool {
	return fuzzShapeShapes && rand.New(rand.NewSource(seed*7919+23)).Intn(100) < 12
}

const fuzzShapeDecls = `<xsl:param name="c" select="false()"/>
<xsl:function name="p:mk"><xsl:param name="s" as="xs:string"/><e><xsl:value-of select="$s"/></e><xsl:if test="$s = 'never'"><xsl:sequence select="1"/></xsl:if></xsl:function>
`

// shapeSelect is a consuming select over the records at STEPS (P), from the
// document node.
func (g *fuzzGen) shapeSelect() string {
	p := g.pick(`STEPS`, `//REC`, `outermost(//REC)`, `STEPS[@k = 'a']`, `STEPS[2]`)
	v := g.pick(`@v`, `@k`, `sub/@x`, `.`, `@missing`)
	return strings.ReplaceAll(g.pick(
		`count(P ! parse-xml('&lt;q>' || `+v+` || '&lt;/q>')/q)`,
		`string-join(P ! p:mk(string(`+v+`)), ',')`,
		`boolean(P ! ('', parse-xml('&lt;q/>')))`,
		`count(P ! (., .))`,
		`string-join(P/@k ! parse-xml('&lt;q>' || . || '&lt;/q>')/q, ',')`,
		`(P ! p:mk(string(@k))) instance of element(e)+`,
		`sum(tail(P)/`+v+`)`, `count(tail(P))`, `string-join(tail(tail(P))/@k, ',')`,
		`count(if ($c) then () else P)`, `sum(if (not($c)) then P/@v else ())`,
		`string(count(if ($c) then //sub else P))`,
		`[P/`+v+`/data(), 'x']?*`, `Q{http://www.w3.org/2005/xpath-functions/array}size([P/copy-of(), 1])`,
		`map{'n': count(P), 'k': 1}?n`, `array{P/@k/string()}?2`, `[P]?*/@k`,
	), "P", p)
}

// shapeBody is a container body of the grammar.
func (g *fuzzGen) shapeBody() string {
	sel := g.shapeSelect()
	switch g.n(9) {
	case 0, 1:
		return `<o><xsl:value-of select="` + sel + `"/></o>`
	case 2:
		return `<o xsl:expand-text="yes">n={` + sel + `} k={1 + 1}</o>`
	case 3:
		return `<o><xsl:if test="` + g.pick(`STEPS`, `//sub[@x = '3']`, `data(STEPS/@v)`, `not(//REC[@k = 'zz'])`, `exists(tail(STEPS))`) + `">` +
			g.pick(`yes`, `<y n="{1}"/>`, `<xsl:value-of select="name()"/>`, `<xsl:value-of select="count(//sub)"/>`) + `</xsl:if></o>`
	case 4:
		return `<xsl:choose><xsl:when test="$c">a</xsl:when><xsl:when test="count(//REC) gt ` + g.pick("0", "1", "3") +
			`"><many/></xsl:when><xsl:otherwise>` + g.pick(`<few/>`, `<xsl:value-of select="count(//sub)"/>`) + `</xsl:otherwise></xsl:choose>`
	case 5:
		return `<xsl:for-each select="STEPS" expand-text="yes"><r>{@k}:{position()}` + g.pick(``, `{count(sub)}`, `{.}`, `{sub}{.}`) + `</r></xsl:for-each>`
	case 6:
		return `<xsl:for-each select="STEPS"><xsl:if test="` + g.pick(`sub`, `sub[@x = '1']`, `@k = 'a'`) + `"><r k="{@k}"/></xsl:if></xsl:for-each>`
	case 7:
		return `<o>{literal}<xsl:value-of select="` + sel + `"/></o>`
	}
	return `<o><xsl:copy-of select="` + g.pick(`STEPS ! p:mk(string(.))`, `[STEPS/@v/data()]?*`, `tail(STEPS)/@k/string()`) + `"/></o>`
}

func fuzzMakeShapes(seed int64) fuzzCase {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*389 + 5))}
	g.root = g.pick("doc", "root", "account")
	g.rec = g.pick("rec", "rec", "item", "p:rec")
	g.defNS = g.p(10)
	if g.p(40) {
		g.wrappers = []string{g.pick("wrap", "data")}
	}
	doc := g.groupDocument()
	steps := strings.Join(append(append([]string{g.root}, g.wrappers...), g.rec), "/")
	content := strings.NewReplacer("STEPS", steps, "REC", g.rec).Replace(g.shapeBody())
	if g.p(30) {
		content = g.coreWrap(content)
	}
	var s strings.Builder
	s.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:p="urn:p" xmlns:err="http://www.w3.org/2005/xqt-errors" exclude-result-prefixes="xs p err"`)
	if g.defNS && g.p(85) {
		s.WriteString(` xpath-default-namespace="urn:d"`)
	}
	s.WriteString(">\n")
	if g.p(25) {
		s.WriteString(`<xsl:strip-space elements="*"/>` + "\n")
	}
	s.WriteString(fuzzShapeDecls)
	s.WriteString(`<xsl:template name="main">` + "\n")
	s.WriteString(`<xsl:source-document streamable="yes" href="doc.xml">` + content + `</xsl:source-document>`)
	s.WriteString("\n</xsl:template>\n</xsl:stylesheet>\n")
	c := fuzzCase{seed: seed, sheet: s.String(), doc: doc, malform: g.malform, kind: "shapes"}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	if g.p(3) {
		c.maxBytes = int64(50 + g.n(800))
	}
	return c
}
