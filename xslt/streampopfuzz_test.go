package xslt

// The path-algebra grammar of TestStreamFuzz (lane G2): set operators over
// the record paths (with each other and with a variable's nodes), simple
// maps, filters by position, positional steps, and the populations of
// for-each, iterate and apply-templates -- windows, motionless parts,
// attributes, copies, values, choices -- some of which the core must refuse.
// GOXSLT_STREAM_FUZZ_PATHS=0 leaves them out: every case is then the one the
// other grammars made for its seed.

import (
	"math/rand"
	"os"
	"strings"
)

// fuzzPathShapes says the fuzzers may make path-algebra cases.
var fuzzPathShapes = os.Getenv("GOXSLT_STREAM_FUZZ_PATHS") != "0"

// fuzzIsPaths decides, from a random stream of its own, whether the case of
// seed is one of fuzzMakePaths'.
func fuzzIsPaths(seed int64) bool {
	return fuzzPathShapes && rand.New(rand.NewSource(seed*15485863+29)).Intn(100) < 20
}

// setExpr is a set operator over record paths, perhaps with $v.
func (g *fuzzGen) setExpr() string {
	op := g.pick("|", "union", "except", "intersect")
	rel := func() string {
		return g.pick("sub", "x", "*", "sub[1]", "sub[position() lt 2]", "sub[@x]", "name", "node()")
	}
	switch g.n(9) {
	case 0, 1:
		return g.path() + " " + op + " " + g.path()
	case 2:
		return g.path() + " " + op + " $v"
	case 3:
		return "$v " + op + " " + g.path()
	case 4:
		return "(" + g.path() + " | " + g.path() + ")/" + rel()
	case 5:
		return "(" + g.path() + " " + op + " " + g.path() + ") " + g.pick("union", "except", "intersect") + " " + g.path()
	case 6:
		return g.path() + " ! (" + rel() + " " + op + " " + rel() + ")"
	case 7:
		return "//" + g.rec + "[position() " + g.pick("lt 3", "mod 2 = 1", "= 2", "gt 1", "idiv 2 = 1") + "]"
	}
	return "(. " + op + " " + rel() + ")"
}

// population is a loop's select of the grammar.
func (g *fuzzGen) population() string {
	p := g.path1()
	switch g.n(16) {
	case 0:
		return "head(" + p + ")"
	case 1:
		return "tail(" + p + ")"
	case 2:
		return "subsequence(" + p + ", " + g.pick("2", "0.5", "1.5", "$n", "-1") + g.pick("", ", 2", ", 1.6", ", $n", ", 0") + ")"
	case 3:
		return "remove(" + p + ", " + g.pick("1", "2", "0", "$n") + ")"
	case 4:
		return "insert-before(" + p + ", " + g.pick("0", "2", "9", "$n") + ", $v)"
	case 5:
		return "insert-before($v, " + g.pick("0", "2", "9") + ", " + p + ")"
	case 6:
		return g.pick("($v, ", "(1, 'a', ", "($v/@k, ") + p + ")"
	case 7:
		return "(" + p + ", " + g.pick("$v", "$v/@x", "7") + ")"
	case 8:
		return p + g.pick("/@k", "/@v", "/@*")
	case 9:
		return "data(" + p + "/@v), 101"
	case 10:
		return "copy-of(" + p + ")"
	case 11:
		return "if ($c) then () else " + p
	case 12:
		return "if (" + p + ") then 0 else 1"
	case 13:
		return g.pick("one-or-more(", "unordered(", "head(tail(") + p + g.pick(")", ")", "))")
	case 14:
		return p + " except $v"
	}
	return g.setExpr()
}

// fuzzMakePaths is a path-algebra case over a record document.
func fuzzMakePaths(seed int64) fuzzCase {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*977 + 3))}
	g.root = g.pick("doc", "root", "account")
	g.rec = g.pick("rec", "rec", "item", "p:rec")
	g.defNS = g.p(10)
	for i, c := 0, g.n(3); i < c; i++ {
		if g.p(50) {
			g.wrappers = append(g.wrappers, g.pick("wrap", "data", "list"))
		}
	}
	doc := g.document()
	body := `<e p="{position()}"><xsl:value-of select="` + g.pick(".", "name(.)", "@k", "string(.)", ". || '|'") + `"/></e>`
	var content string
	switch g.n(8) {
	case 0, 1:
		content = `<xsl:value-of select="` + g.pick("count", "exists", "empty", "boolean", "not") + "(" + g.setExpr() + `)"/>`
	case 2:
		content = `<xsl:copy-of select="` + g.setExpr() + `"/>`
	case 3:
		content = `<xsl:value-of select="` + g.pick("(S)[position() mod 2 = 0]", "(S) ! name()", "(S)/@k", "sum((S)/@v)", "data(S)") +
			`" separator=","/>`
		content = strings.Replace(content, "S", g.setExpr(), 1)
	case 4, 5:
		content = `<xsl:for-each select="` + g.population() + `">` + body + `</xsl:for-each>`
	case 6:
		content = `<xsl:iterate select="` + g.population() + `"><xsl:param name="k" select="0"/>` +
			`<xsl:on-completion><n><xsl:value-of select="$k"/></n></xsl:on-completion>` + body +
			`<xsl:next-iteration><xsl:with-param name="k" select="$k + 1"/></xsl:next-iteration></xsl:iterate>`
	default:
		content = `<xsl:apply-templates mode="m" select="` + g.pick("head(P)", "tail(P)", "subsequence(P, 2)", "remove(P, 1)",
			"one-or-more(P)", "unordered(P)", "P | P", "P except $v", "insert-before(P, 1, $v)") + `"/>`
		content = strings.ReplaceAll(content, "P", g.path1())
	}
	if g.p(40) {
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
	s.WriteString(`<xsl:variable name="v" as="element()*"><` + g.rec + ` k="a" v="1" xmlns:p="urn:p"/><sub x="2"/></xsl:variable>` +
		`<xsl:variable name="n" select="` + g.pick("2", "1.5", "'x'", "()") + `"/><xsl:variable name="c" select="` + g.pick("true()", "false()") + `"/>` + "\n")
	s.WriteString(`<xsl:mode name="m" streamable="yes"/><xsl:template match="` + g.rec + `" mode="m"><r p="{position()}" k="{@k}"/></xsl:template>` + "\n")
	s.WriteString(`<xsl:template name="main">` + "\n")
	try := g.p(8)
	if try {
		s.WriteString("<xsl:try>")
	}
	s.WriteString(`<xsl:source-document streamable="yes" href="doc.xml">` + content + `</xsl:source-document>`)
	if try {
		s.WriteString(`<xsl:catch><caught code="{local-name-from-QName($err:code)}"/></xsl:catch></xsl:try>`)
	}
	s.WriteString("\n</xsl:template>\n</xsl:stylesheet>\n")
	c := fuzzCase{seed: seed, sheet: s.String(), doc: doc, malform: g.malform, kind: "paths"}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	if g.p(3) {
		c.maxBytes = int64(50 + g.n(800))
	}
	return c
}
