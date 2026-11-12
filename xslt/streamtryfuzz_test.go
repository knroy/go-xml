package xslt

// The C11 grammar of TestStreamFuzz and TestStreamFuzzMain: xsl:try in a
// streamed body, the error at record k (a value that is no number, a zero
// divisor, a missing attribute, a malformed tail), the catch motionless or
// consuming (refused), matching the error or not; and calls on stylesheet
// functions of every category, inlined into the split or refused. The
// documents are the grouping grammar's (values "x", "0", "2.5" among the
// numbers). The tries are written <xsl:try rollback-output="yes">, so that
// fuzzDiff's exemption for a container under an outer <xsl:try> does not
// apply: a re-run where the tree succeeds is a difference. The modes
// grammar's rule bodies get tries too (fuzzTryRule), for the principal
// input's leg. GOXSLT_STREAM_FUZZ_TRY=0 leaves them all out.

import (
	"math/rand"
	"os"
	"strings"
)

var fuzzTryShapes = os.Getenv("GOXSLT_STREAM_FUZZ_TRY") != "0"

// fuzzIsTry decides, from a random stream of its own, whether the case of
// seed is one of fuzzMakeTry's.
func fuzzIsTry(seed int64) bool {
	return fuzzTryShapes && rand.New(rand.NewSource(seed*15485867+17)).Intn(100) < 20
}

// fuzzCallDecls are stylesheet functions the grammar calls (prefix p).
const fuzzCallDecls = `<xsl:function name="p:depth" as="xs:integer" streamability="inspection"><xsl:param name="e" as="node()"/><xsl:sequence select="count($e/ancestor-or-self::*)"/></xsl:function>
<xsl:function name="p:att" as="xs:string" streamability="inspection"><xsl:param name="e" as="node()"/><xsl:param name="n" as="xs:string"/><xsl:sequence select="string($e/@*[local-name() = $n])"/></xsl:function>
<xsl:function name="p:cd" as="xs:integer" streamability="absorbing"><xsl:param name="in" as="node()*"/><xsl:sequence select="count($in/descendant::*)"/></xsl:function>
<xsl:function name="p:txt" as="xs:string" streamability="absorbing"><xsl:param name="in" as="node()"/><xsl:sequence select="string($in)"/></xsl:function>
<xsl:function name="p:num" as="xs:integer" streamability="inspection"><xsl:param name="e" as="node()"/><xsl:sequence select="xs:integer($e/@v)"/></xsl:function>
<xsl:function name="p:kids" as="node()*" streamability="shallow-descent"><xsl:param name="in" as="node()"/><xsl:sequence select="$in/sub"/></xsl:function>
<xsl:function name="p:rec" as="xs:integer" streamability="inspection"><xsl:param name="e" as="node()"/><xsl:sequence select="if ($e/.. instance of document-node()) then 1 else p:rec($e/..) + 1"/></xsl:function>
`

// tryBody is a try body over the records at steps, from the context the
// try is in (doc: the document node).
func (g *fuzzGen) tryBody(steps string) string {
	v := g.pick(`@v`, `@v`, `@k`, `sub/@x`, `@missing`)
	return g.pick(
		`<xsl:value-of select="avg(STEPS/`+v+`)"/>`, `<xsl:value-of select="sum(STEPS/`+v+`)"/>`,
		`<xsl:value-of select="max(STEPS/@v)"/>`, `<xsl:value-of select="count(STEPS)"/>`,
		`<xsl:copy-of select="STEPS[xs:integer(@v) gt 0]"/>`, `<xsl:copy-of select="STEPS[@k = 'a']"/>`,
		`<xsl:value-of select="STEPS/xs:integer(@v)" separator=","/>`, `<xsl:value-of select="STEPS ! (1 div xs:integer(@v))"/>`,
		`<xsl:value-of select="(STEPS/@v) treat as attribute()+"/>`, `<xsl:value-of select="round(avg(STEPS[position() lt 3]/@v), 2)"/>`,
		`<xsl:for-each select="STEPS"><r><xsl:value-of select="xs:integer(@v) * 2"/></r></xsl:for-each>`,
		`<xsl:for-each select="STEPS"><r k="{@k}"><xsl:value-of select="sum(sub/@x)"/></r></xsl:for-each>`,
		`<xsl:for-each select="STEPS"><xsl:copy-of select="."/></xsl:for-each>`,
		`<xsl:value-of select="STEPS ! p:depth(.)"/>`, `<xsl:value-of select="STEPS ! p:att(., 'k')"/>`,
		`<xsl:value-of select="STEPS ! p:cd(.)"/>`, `<xsl:value-of select="p:cd(STEPS)"/>`, `<xsl:value-of select="STEPS ! p:txt(.)"/>`,
		`<xsl:value-of select="STEPS ! p:num(.)"/>`, `<xsl:value-of select="STEPS ! p:rec(.)"/>`,
		`<xsl:value-of select="STEPS ! string-length(@k)"/>`,
		// refused in a try: parts failing in two orders, apply-templates
		`<xsl:value-of select="sum(STEPS[xs:integer(@v) gt 0]/@v)"/>`, `<xsl:apply-templates select="STEPS"/>`,
	)
}

// recBody is a try body at a record's view.
func (g *fuzzGen) recBody() string {
	return g.pick(`<r><xsl:value-of select="1 div xs:integer(@v)"/></r>`, `<r v="{xs:integer(@v)}"/>`,
		`<r><xsl:copy-of select="sub[xs:integer(@x) ge 0]"/></r>`, `<r><xsl:value-of select="sum(sub/@x)"/></r>`,
		`<r><xsl:value-of select="."/></r>`, `<xsl:copy-of select="."/>`, `<r d="{p:depth(.)}"/>`,
		`<r><xsl:value-of select="sub ! p:depth(.)"/></r>`, `<r><xsl:value-of select="xs:integer(sub[1]/@x)"/></r>`,
		`<xsl:for-each select="sub"><s><xsl:value-of select="10 div xs:integer(@x)"/></s></xsl:for-each>`)
}

// catch is an xsl:catch, perhaps consuming (refused) or matching nothing.
func (g *fuzzGen) catch() string {
	errs := g.pick(``, ` errors="*"`, ` errors="*:FORG0001"`, ` errors="*:FOAR0001 *:FORG0001"`, ` errors="err:XPDY0050"`,
		` errors="*:XTDE0000"`, ` errors="*:FORG0006"`)
	return `<xsl:catch` + errs + `>` + g.pick(`<caught code="{local-name-from-QName($err:code)}"/>`,
		`<caught code="{local-name-from-QName($err:code)}"><xsl:value-of select="$err:description"/></caught>`,
		``, `c`, `<e n="{name()}"/>`, `<xsl:value-of select="count(*)"/>`, `<xsl:copy-of select="."/>`) + `</xsl:catch>`
}

// try wraps body in an xsl:try.
func (g *fuzzGen) try(body string) string {
	rb := `yes`
	if g.p(5) {
		rb = `no` // refused
	}
	if g.p(10) {
		return `<xsl:try rollback-output="` + rb + `" select="` + g.pick(`sum(STEPS/@v)`, `count(STEPS)`, `avg(STEPS/@k)`) + `">` + g.catch() + `</xsl:try>`
	}
	return `<xsl:try rollback-output="` + rb + `">` + body + g.catch() + `</xsl:try>`
}

// fuzzMakeTry is a C11 case: a container whose body holds xsl:try or calls
// stylesheet functions, over a document whose records fail here and there.
func fuzzMakeTry(seed int64) fuzzCase {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*257 + 9))}
	g.root = g.pick("doc", "root", "account")
	g.rec = g.pick("rec", "rec", "item", "p:rec")
	g.defNS = g.p(10)
	if g.p(40) {
		g.wrappers = []string{g.pick("wrap", "data")}
	}
	doc := g.groupDocument()
	steps := strings.Join(append(append([]string{g.root}, g.wrappers...), g.rec), "/")
	var content string
	switch g.n(6) {
	case 0, 1:
		content = `<o>` + g.try(g.tryBody(steps)) + `</o>`
	case 2, 3:
		content = `<o><xsl:for-each select="STEPS">` + g.try(g.recBody()) + g.pick(``, `<n k="{@k}"/>`) + `</xsl:for-each></o>`
	case 4:
		// Nested: a try per record inside a try around the loop.
		content = `<o>` + g.try(`<xsl:for-each select="STEPS">`+g.try(g.recBody())+`</xsl:for-each>`) + `</o>`
	default:
		// No try: the calls and simple maps alone.
		content = `<o>` + g.tryBody(steps) + `</o>`
	}
	content = strings.ReplaceAll(content, "STEPS", steps)
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
	s.WriteString(fuzzCallDecls)
	s.WriteString(`<xsl:template name="main">` + "\n")
	s.WriteString(`<xsl:source-document streamable="yes" href="doc.xml">` + content + `</xsl:source-document>`)
	s.WriteString("\n</xsl:template>\n</xsl:stylesheet>\n")
	c := fuzzCase{seed: seed, sheet: s.String(), doc: doc, malform: g.malform, kind: "try"}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	if g.p(3) {
		c.maxBytes = int64(50 + g.n(800))
	}
	return c
}

// fuzzTryRule is, for the modes grammar, a rule body holding a try or a
// call, from r, a stream of its own; "" when this rule keeps its body. The
// modes sheet then declares fuzzCallDecls.
func fuzzTryRule(r *rand.Rand, rec string) string {
	if !fuzzTryShapes || r.Intn(100) >= 15 {
		return ""
	}
	g := &fuzzGen{r: r, rec: rec}
	var b string
	if g.p(70) {
		b = g.try(g.recBody())
	} else {
		b = `<r>` + g.pick(`<xsl:value-of select="sub ! p:depth(.)"/>`, `<xsl:value-of select="p:cd(.)"/>`,
			`<xsl:value-of select="sub ! string-length(@x)"/>`, `<xsl:value-of select="sub ! p:att(., 'x')"/>`) + `</r>`
	}
	b = strings.ReplaceAll(b, "STEPS", "sub")
	return b + g.pick(``, `<xsl:apply-templates/>`)
}
