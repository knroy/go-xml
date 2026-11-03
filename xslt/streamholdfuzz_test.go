package xslt

// Lane g1's grammar of TestStreamFuzz: a select at the document node that
// holds its matches (streamhold.go) or counts them (streamfold.go),
// xsl:copy with a consuming select (streamcopy.go), xsl:on-empty and
// xsl:on-non-empty beside or as the consumer (streamonempty.go), and
// validation="strip"/"preserve" on constructors (streamvalid.go), over the
// grouping grammar's documents (sometimes hundreds of records, so the hold's
// cap is reached). GOXSLT_STREAM_FUZZ_HOLD=0 leaves them out.

import (
	"math/rand"
	"os"
	"strings"
)

var fuzzHoldShapes = os.Getenv("GOXSLT_STREAM_FUZZ_HOLD") != "0"

// fuzzIsHold decides, from a random stream of its own, whether the case of
// seed is one of fuzzMakeHold's.
func fuzzIsHold(seed int64) bool {
	return fuzzHoldShapes && rand.New(rand.NewSource(seed*32452843+29)).Intn(100) < 15
}

// holdSeq is a sequence made of the matches of P, by position only.
func (g *fuzzGen) holdSeq() string {
	return g.pick(`P`, `remove(P, 2)`, `insert-before(P, 2, 7)`, `insert-before(7, 1, P)`, `subsequence(P, 2)`,
		`subsequence(P, 1, 3)`, `tail(P)`, `head(P)`, `unordered(P)`, `(P, 1)`, `(1, P)`, `one-or-more(P)`,
		`zero-or-one(P)`, `exactly-one(P)`, `remove(tail(P), 1)`)
}

// holdSelect is a select at the document node over P.
func (g *fuzzGen) holdSelect() string {
	q := g.holdSeq()
	if g.p(50) {
		return g.pick(`count`, `exists`, `empty`, `boolean`, `not`) + `(` + q + `)` // counted
	}
	return g.pick(q, q+` ! name()`, `(`+q+`) treat as element()+`, `(`+q+`) instance of element()*`,
		`deep-equal(`+q+`, ())`, `sum(`+q+`/@v)`, `string-join(`+q+` ! string(@k), ',')`,
		`copy-of(`+q+`)[@k = 'a']`, `(`+q+`) ! copy-of(.)`)
}

// fuzzMakeHold is a g1 case.
func fuzzMakeHold(seed int64) fuzzCase {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*613 + 5))}
	g.root = g.pick("doc", "root", "account")
	g.rec = g.pick("rec", "rec", "item")
	if g.p(40) {
		g.wrappers = []string{g.pick("wrap", "data")}
	}
	doc := g.groupDocument()
	steps := strings.Join(append(append([]string{g.root}, g.wrappers...), g.rec), "/")
	path := g.pick(steps, steps, "/"+steps, "//"+g.rec, steps+"[@k = 'a']", steps+"/sub", steps+"[2]", "/*", "*")
	var content string
	switch g.n(7) {
	case 0, 1, 2:
		sel := g.holdSelect()
		content = `<o>` + g.pick(`<xsl:value-of select="S"/>`, `<xsl:sequence select="S"/>`, `<xsl:copy-of select="S"/>`,
			`<e n="{S}"/>`) + `</o>`
		content = strings.ReplaceAll(content, "S", strings.ReplaceAll(sel, "P", path))
	case 3:
		content = `<o><xsl:copy select="` + g.pick(path, g.root, steps+"[1]", steps+"[@k = 'b']") + `">` +
			g.pick(`<xsl:copy-of select="sub"/>`, `<n><xsl:value-of select="count(*)"/></n>`, `<xsl:attribute name="k" select="@k"/>`,
				`<xsl:value-of select="."/>`, `<e/>`, `<xsl:copy select="*[1]"><xsl:copy-of select="@*"/></xsl:copy>`) + `</xsl:copy></o>`
	case 4, 5:
		cons := g.pick(`<xsl:copy-of select="P"/>`, `<xsl:value-of select="count(P)"/>`, `<xsl:sequence select="P/@k/string()"/>`)
		mot := g.pick(`<h/>`, `<xsl:sequence select="()"/>`, `<xsl:sequence select="''"/>`, `x`)
		content = `<o>` + g.pick(
			cons+`<xsl:on-empty>`+mot+`</xsl:on-empty>`,
			`<xsl:on-non-empty>`+mot+`</xsl:on-non-empty>`+cons,
			`<xsl:on-non-empty>`+cons+`</xsl:on-non-empty>`+mot,
			mot+`<xsl:on-empty>`+cons+`</xsl:on-empty>`,
			`<xsl:sequence select="()"/><xsl:on-empty select="count(P)"/>`,
			`<xsl:on-non-empty select="sum(P/@v)"/>`+mot) + `</o>`
		content = strings.ReplaceAll(content, "P", path)
	default:
		v := g.pick(`strip`, `preserve`)
		content = g.pick(`<o><xsl:copy-of select="P" validation="V"/></o>`,
			`<xsl:element name="e" validation="V"><xsl:copy-of select="P"/></xsl:element>`,
			`<e xsl:validation="V"><xsl:value-of select="count(P)"/></e>`,
			`<o><xsl:copy select="`+steps+`[1]" validation="V"><xsl:copy-of select="sub"/></xsl:copy></o>`)
		content = strings.ReplaceAll(strings.ReplaceAll(content, "P", path), "V", v)
	}
	if g.p(30) {
		content = g.coreWrap(content)
	}
	var s strings.Builder
	s.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:p="urn:p" exclude-result-prefixes="xs p">` + "\n")
	if g.p(30) {
		s.WriteString(`<xsl:strip-space elements="*"/>` + "\n")
	}
	s.WriteString(`<xsl:template name="main">` + "\n")
	s.WriteString(`<xsl:source-document streamable="yes" href="doc.xml">` + content + `</xsl:source-document>`)
	s.WriteString("\n</xsl:template>\n</xsl:stylesheet>\n")
	c := fuzzCase{seed: seed, sheet: s.String(), doc: doc, malform: g.malform, kind: "hold"}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	if g.p(3) {
		c.maxBytes = int64(50 + g.n(800))
	}
	return c
}
