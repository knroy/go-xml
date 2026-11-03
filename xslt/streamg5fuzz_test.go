package xslt

// Lane g5's grammar of TestStreamFuzz: calls that take the consuming path
// whole (fold-left, fold-right, filter, for-each, distinct-values,
// codepoints-to-string, trace) with function items; xsl:message in streamed
// bodies (motionless, consuming, failing content, terminate, in loops and in
// fork prongs) -- fuzzRun's results carry the messages, which fuzzDiff
// compares; xsl:try over folds that cannot fail and over ones that can
// (refused); snapshot(P) where it is copied; a filter of a constant beside a
// consumer; xsl:fallback; iterate defaults that are map constructors. The
// documents are the grouping grammar's (values "x", "0", "2.5" among the
// numbers, missing keys). GOXSLT_STREAM_FUZZ_G5=0 leaves them out.

import (
	"math/rand"
	"os"
	"strings"
)

var fuzzG5Shapes = os.Getenv("GOXSLT_STREAM_FUZZ_G5") != "0"

// fuzzIsG5 decides, from a random stream of its own, whether the case of
// seed is one of fuzzMakeG5's.
func fuzzIsG5(seed int64) bool {
	return fuzzG5Shapes && rand.New(rand.NewSource(seed*32452843+29)).Intn(100) < 15
}

// g5Body is a body over the records at STEPS.
func (g *fuzzGen) g5Body() string {
	v := g.pick(`@v`, `@v`, `@k`, `sub/@x`, `@missing`)
	return g.pick(
		`<v><xsl:value-of select="fold-left(STEPS/`+v+`/data(), 0, function($a, $b) { $a + number($b) })"/></v>`,
		`<v><xsl:value-of select="fold-left(STEPS/number(@v), 1, function($a, $b) { $a * $b })"/></v>`,
		`<v><xsl:value-of select="fold-left(STEPS/xs:integer(@v), 0, function($a, $b) { $a + $b })"/></v>`,
		`<v><xsl:value-of select="filter(STEPS/@k/data(), function($x) { $x != 'a' })"/></v>`,
		`<v><xsl:value-of select="for-each(STEPS/@k/data(), upper-case#1)"/></v>`,
		`<v><xsl:value-of select="for-each(STEPS/@k/data(), name#0)"/></v>`,
		`<v><xsl:value-of select="count(distinct-values(STEPS/`+v+`))"/></v>`,
		`<v><xsl:value-of select="distinct-values(STEPS/@k)"/></v>`,
		`<v><xsl:value-of select="codepoints-to-string(STEPS/(64 + string-length(@k)))"/></v>`,
		`<v><xsl:value-of select="codepoints-to-string(STEPS/xs:integer(@v))"/></v>`,
		`<xsl:for-each select="trace(STEPS, 'l')"><r k="{@k}"/></xsl:for-each>`,
		`<v><xsl:value-of select="count(trace(STEPS, 'c'))"/></v>`,
		`<v><xsl:value-of select="sum(trace(STEPS/number(@v), 's'))"/></v>`,
		`<xsl:message select="count(STEPS)"/><n/>`,
		`<xsl:message select="'a'"/><v><xsl:value-of select="sum(STEPS/`+v+`)"/></v><xsl:message>b</xsl:message>`,
		`<xsl:for-each select="STEPS"><xsl:message select="string(@k)"/><r v="{@v}"/></xsl:for-each>`,
		`<xsl:for-each select="STEPS"><xsl:message><xsl:copy-of select="."/></xsl:message></xsl:for-each>`,
		`<xsl:message select="sum(STEPS/xs:integer(@v))"/><after/>`,
		`<xsl:message><xsl:value-of select="STEPS/xs:integer(@v)"/></xsl:message><after/>`,
		`<xsl:message select="'stop'" terminate="{if (true()) then 'yes' else 'no'}"/>`,
		`<xsl:message select="'m'"/><v><xsl:value-of select="sum(STEPS/xs:integer(@v))"/></v>`,
		`<o><xsl:sequence select="snapshot(STEPS/sub)"/></o>`,
		`<o><xsl:copy-of select="snapshot(STEPS)"/></o>`,
		`<o><xsl:sequence select="snapshot(STEPS/@k)"/></o>`,
		`<o><xsl:sequence select="(1 to 5)[. gt 3]"/><xsl:sequence select="STEPS/@v/data()"/><xsl:on-empty>none</xsl:on-empty></o>`,
		`<v><xsl:sequence select="count(STEPS)"><xsl:fallback>f</xsl:fallback></xsl:sequence></v>`,
		`<xsl:iterate select="STEPS"><xsl:param name="m" as="map(*)" select="map{}"/>`+
			`<xsl:on-completion><n><xsl:value-of select="map:size($m)"/></n></xsl:on-completion>`+
			`<xsl:next-iteration><xsl:with-param name="m" select="map:put($m, string(@k), 1)"/></xsl:next-iteration></xsl:iterate>`,
	)
}

// g5Try is a try over a fold (most of which cannot fail once their items
// have passed), its catch printing the error.
func (g *fuzzGen) g5Try() string {
	sel := g.pick(`avg(STEPS/@v[. &gt; 0])`, `avg(STEPS/(@v * 2))`, `sum(STEPS/@v/data()[. &lt; 1000][. &gt; 0])`,
		`avg(STEPS/sub/text()[. &lt; 1000][. &gt; 0])`, `avg(STEPS/@v ! xs:NMTOKENS(.) ! xs:decimal(.))`,
		`min(STEPS/abs(@v))`, `max(STEPS/number(@v)[. &gt; 0])`, `sum(STEPS/@v[xs:decimal(.) gt 0])`,
		`round(avg(STEPS/(if (@k = 'a') then +@v else (@v + 1))))`,
		// refused: the fold can fail on an item that passed
		`sum(STEPS/@v[xs:decimal(.) ne 0]/string(.))`, `avg(STEPS/xs:date(@v))`)
	return `<xsl:try rollback-output="yes"><v><xsl:value-of select="` + sel + `"/></v>` +
		`<xsl:catch><e c="{$err:code}" d="{$err:description}"/></xsl:catch></xsl:try>`
}

// fuzzMakeG5 is a lane-g5 case over a record document.
func fuzzMakeG5(seed int64) fuzzCase {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*331 + 7))}
	g.root = g.pick("doc", "root", "account")
	g.rec = g.pick("rec", "rec", "item", "p:rec")
	g.defNS = g.p(10)
	if g.p(40) {
		g.wrappers = []string{g.pick("wrap", "data")}
	}
	doc := g.groupDocument()
	steps := strings.Join(append(append([]string{g.root}, g.wrappers...), g.rec), "/")
	var content string
	switch g.n(8) {
	case 0, 1, 2, 3:
		content = g.g5Body()
	case 4, 5:
		content = g.g5Try()
	case 6:
		// Messages in fork prongs: told in prong order.
		content = `<xsl:fork><xsl:sequence><xsl:message select="'p1'"/><c><xsl:value-of select="count(STEPS)"/></c></xsl:sequence>` +
			`<xsl:sequence><xsl:message select="'p2'"/><s><xsl:value-of select="sum(STEPS/` + g.pick("@v", "xs:integer(@v)") + `)"/></s></xsl:sequence>` +
			`<xsl:sequence><xsl:message select="'p3'"/></xsl:sequence></xsl:fork>`
	default:
		content = `<o>` + g.g5Body() + `</o>`
	}
	content = strings.ReplaceAll(content, "STEPS", steps)
	if g.p(30) {
		content = g.coreWrap(content)
	}
	var s strings.Builder
	s.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema"` +
		` xmlns:p="urn:p" xmlns:err="http://www.w3.org/2005/xqt-errors" xmlns:map="http://www.w3.org/2005/xpath-functions/map"` +
		` exclude-result-prefixes="xs p err map"`)
	if g.defNS && g.p(85) {
		s.WriteString(` xpath-default-namespace="urn:d"`)
	}
	s.WriteString(">\n")
	if g.p(25) {
		s.WriteString(`<xsl:strip-space elements="*"/>` + "\n")
	}
	s.WriteString(`<xsl:template name="main">` + "\n")
	s.WriteString(`<xsl:source-document streamable="yes" href="doc.xml">` + content + `</xsl:source-document>`)
	s.WriteString("\n</xsl:template>\n</xsl:stylesheet>\n")
	c := fuzzCase{seed: seed, sheet: s.String(), doc: doc, malform: g.malform, kind: "g5"}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	if g.p(3) {
		c.maxBytes = int64(50 + g.n(800))
	}
	return c
}
