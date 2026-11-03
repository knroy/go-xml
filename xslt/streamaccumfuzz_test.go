package xslt

// The accumulator grammar of TestStreamFuzz and TestStreamFuzzMain (C9):
// random xsl:accumulator declarations -- start and end rules, nested
// matches, rules on text, comments and the document node, values that grow
// and shrink, rules that fail at some record, a type that converts, a
// declaration not streamable, rules the observer must refuse -- read by
// containers and template rules in every way a streamed run answers
// (accumulator-before of a view or the document, -after once read or as
// the one reader, in loops, in rules, after an apply-templates, under an
// xsl:if), with use-accumulators lists that leave some out (XTDE3362).
// A fifth of TestStreamFuzz's seeds are accumAccumCases (a stream of their
// own), and a fifth of the modes grammar's get accumulators and a rule
// reading them (fuzzAccumize), so TestStreamFuzzMain has them too.
// GOXSLT_STREAM_FUZZ_ACCUMS=0 leaves them all out: every case is then the
// one the M3 grammar made for its seed.

import (
	"math/rand"
	"os"
	"strings"
)

// fuzzAccumShapes says the fuzzers may make accumulator cases.
var fuzzAccumShapes = os.Getenv("GOXSLT_STREAM_FUZZ_ACCUMS") != "0"

// fuzzIsAccums decides, from a random stream of its own, whether the case
// of seed is one of fuzzMakeAccums'.
func fuzzIsAccums(seed int64) bool {
	return fuzzAccumShapes && rand.New(rand.NewSource(seed*2147483647+29)).Intn(100) < 20
}

// fuzzAccumRules are accumulator rules: phase, pattern, value; REC is the
// record's name. Some must be refused (positional, a node kept, content
// read, an accumulator read).
var fuzzAccumRules = []string{
	`match="REC" select="$value + 1"`, `match="REC" select="$value + 1"`, `match="*" select="$value + 1"`,
	`match="*" phase="end" select="$value + 1"`, `match="REC" phase="end" select="$value - 1"`,
	`match="sub" select="$value + 10"`, `match="text()" select="$value + 100"`,
	`match="comment() | processing-instruction()" select="$value + 1000"`, `match="/" select="$value + 7"`,
	`match="/" phase="end" select="$value * 2"`, `match="document-node()" select="$value + 3"`,
	`match="REC[@k = 'a']" select="$value + 5"`, `match="REC[@v castable as xs:integer]" select="$value + xs:integer(@v)"`,
	`match="REC" select="$value + xs:integer(@v)"`, `match="REC" select="$value + 10 idiv (2 - number(@v))"`,
	`match="sub/text()" select="$value + string-length(.)"`, `match="REC//sub" select="$value + count(ancestor::*)"`,
	`match="@v" select="$value + 100000"`, `match="REC/@k" select="0"`, `match="x" select="0"`,
	`match="node()" select="$value + 1"`,
}

// fuzzAccumRefused are rules the observer or the analysis refuses, drawn
// one time in twenty.
var fuzzAccumRefused = []string{
	`match="REC[1]" select="$value + 1"`, `match="REC[sub]" select="$value + 1"`, `match="REC" select="$value + count(sub)"`,
	`match="REC" select="$value + string-length(string(.))"`, `match="REC" select="$value + accumulator-before('b')"`,
}

// fuzzAccumSeqRules are the rules of a sequence-valued accumulator.
var fuzzAccumSeqRules = []string{
	`match="REC" select="($value, string(@k))"`, `match="REC" phase="end" select="tail($value)"`,
	`match="*" select="(name(), $value)"`, `match="*" phase="end" select="tail($value)"`,
	`match="text()" select="($value, string(.))"`, `match="sub" select="subsequence($value, 1, 2)"`,
}

// accumDecl is a random accumulator declaration named name.
func (g *fuzzGen) accumDecl(name string) string {
	var b strings.Builder
	seq := g.p(30)
	b.WriteString(`<xsl:accumulator name="` + name + `"`)
	switch {
	case seq:
		b.WriteString(g.pick(` as="xs:string*"`, ``, ``) + ` initial-value="()"`)
	default:
		b.WriteString(g.pick(` as="xs:integer"`, ` as="xs:integer"`, ` as="xs:double"`, ``, ` as="xs:string"`) +
			g.pick(` initial-value="0"`, ` initial-value="0"`, ` initial-value="1"`, ` initial-value="xs:integer('q')"`))
	}
	if !g.p(5) {
		b.WriteString(` streamable="yes"`)
	}
	b.WriteString(`>`)
	for i, n := 0, 1+g.n(3); i < n; i++ {
		r := g.pick(fuzzAccumRules...)
		switch {
		case g.p(5):
			r = g.pick(append(fuzzAccumRefused, `match="REC" select="$value, ."`)...)
		case seq:
			r = g.pick(fuzzAccumSeqRules...)
		}
		b.WriteString(`<xsl:accumulator-rule ` + strings.ReplaceAll(r, "REC", g.rec) + `/>`)
	}
	b.WriteString(`</xsl:accumulator>`)
	return b.String()
}

// accumRead is an expression reading an accumulator of the focus.
func (g *fuzzGen) accumRead() string {
	return g.pick(`accumulator-before('a')`, `accumulator-before('a')`, `accumulator-before('b')`,
		`string-join(for $i in accumulator-before('a') return string($i), ',')`, `count(accumulator-before('b'))`,
		`accumulator-before('zz')`)
}

// accumAfter is an expression reading an after-value of the focus.
func (g *fuzzGen) accumAfter() string {
	return g.pick(`accumulator-after('a')`, `accumulator-after('b')`, `string-join(for $i in accumulator-after('a') return string($i), ',')`)
}

// accumBody is a container or rule body reading accumulators; REC is the
// record, MODE the mode the rules read them in.
func (g *fuzzGen) accumBody(steps string) string {
	r, a := g.accumRead(), g.accumAfter()
	return g.pick(
		`<xsl:for-each select="`+steps+`"><r b="{`+r+`}"/></xsl:for-each>`,
		`<xsl:for-each select="//REC"><r b="{`+r+`}" k="{@k}"/></xsl:for-each>`,
		`<xsl:for-each select="`+steps+`"><r a="{`+a+`}"/></xsl:for-each>`,
		`<xsl:for-each select="`+steps+`"><r b="{`+r+`}"><xsl:copy-of select="sub"/><xsl:value-of select="`+a+`"/></r></xsl:for-each>`,
		`<xsl:for-each select="`+steps+`"><xsl:if test="@k = 'a'"><xsl:copy-of select="sub"/></xsl:if><e a="{`+a+`}"/></xsl:for-each>`,
		`<o n="{count(//REC)}"/><xsl:value-of select="`+a+`"/>`,
		`<xsl:value-of select="`+r+`"/>`, `<xsl:value-of select="`+a+`"/>`, `<t><xsl:value-of select="`+a+`"/></t>`,
		`<xsl:apply-templates select="*" mode="MODE"/>`, `<xsl:apply-templates mode="MODE"/><e a="{`+a+`}"/>`,
		`<xsl:apply-templates select="`+steps+`" mode="MODE"/>`,
		`<xsl:iterate select="`+steps+`"><xsl:param name="s" select="0"/><xsl:on-completion><s v="{$s}"/></xsl:on-completion>`+
			`<i b="{`+r+`}"/><xsl:next-iteration><xsl:with-param name="s" select="$s + 1"/></xsl:next-iteration></xsl:iterate>`,
		// refused: the after-value before the content is read
		`<xsl:for-each select="`+steps+`"><xsl:value-of select="`+a+`"/><xsl:value-of select="count(sub)"/></xsl:for-each>`,
	)
}

// accumRules are template rules of mode MODE reading accumulators.
func (g *fuzzGen) accumRules() string {
	r, a := g.accumRead(), g.accumAfter()
	return g.pick(
		`<xsl:template match="REC" mode="MODE"><r b="{`+r+`}"><xsl:apply-templates mode="MODE"/><xsl:value-of select="`+a+`"/></r></xsl:template>`,
		`<xsl:template match="REC" mode="MODE"><r b="{`+r+`}" a="{`+a+`}"/></xsl:template>`,
		`<xsl:template match="REC" mode="MODE"><r b="{`+r+`}"/><xsl:apply-templates mode="MODE"/></xsl:template>`,
		`<xsl:template match="*" mode="MODE"><e n="{name()}" b="{`+r+`}"><xsl:apply-templates mode="MODE"/></e><f a="{`+a+`}"/></xsl:template>`,
		`<xsl:template match="sub" mode="MODE"><s b="{`+r+`}"/></xsl:template>`,
		`<xsl:template match="REC" mode="MODE"><xsl:if test="@k = 'b'"><xsl:apply-templates mode="MODE"/></xsl:if><e a="{`+a+`}"/></xsl:template>`,
	) + g.pick(``, `<xsl:template match="text()" mode="MODE"><xsl:value-of select="."/></xsl:template>`)
}

// accumUse is a use-accumulators list, or none.
func (g *fuzzGen) accumUse() string {
	return g.pick(``, ` use-accumulators="#all"`, ` use-accumulators="#all"`, ` use-accumulators="a"`,
		` use-accumulators="a b"`, ` use-accumulators=""`)
}

// fuzzMakeAccums is an accumulator case: accumulators over a record
// document, read by a streamed container and the rules it applies.
func fuzzMakeAccums(seed int64) fuzzCase {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*911 + 3))}
	g.root = g.pick("doc", "root", "account")
	g.rec = g.pick("rec", "rec", "item", "p:rec")
	for i, c := 0, g.n(3); i < c; i++ {
		if g.p(50) {
			g.wrappers = append(g.wrappers, g.pick("wrap", "data", "list"))
		}
	}
	doc := g.document()
	if g.p(30) {
		doc = g.groupDocument()
	}
	steps := strings.Join(append(append([]string{g.root}, g.wrappers...), g.rec), "/")
	var s strings.Builder
	s.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:p="urn:p" xmlns:err="http://www.w3.org/2005/xqt-errors" exclude-result-prefixes="xs p err">` + "\n")
	if g.p(25) {
		s.WriteString(`<xsl:strip-space elements="*"/>` + "\n")
	}
	s.WriteString(g.accumDecl("a") + "\n" + g.accumDecl("b") + "\n")
	s.WriteString(`<xsl:mode name="m" streamable="yes" on-no-match="` + g.pick("shallow-skip", "shallow-copy", "text-only-copy", "deep-skip") + `"/>` + "\n")
	for i, n := 0, 1+g.n(2); i < n; i++ {
		s.WriteString(strings.ReplaceAll(g.accumRules(), "MODE", "m") + "\n")
	}
	content := strings.ReplaceAll(g.accumBody(steps), "MODE", "m")
	if g.p(30) {
		content = g.coreWrap(content)
	}
	s.WriteString(`<xsl:template name="main">` + "\n")
	try := g.p(10)
	if try {
		s.WriteString("<xsl:try>")
	}
	s.WriteString(`<xsl:source-document streamable="yes" href="doc.xml"` + g.accumUse() + `>` + content + `</xsl:source-document>`)
	if try {
		s.WriteString(`<xsl:catch><caught code="{local-name-from-QName($err:code)}"/></xsl:catch></xsl:try>`)
	}
	s.WriteString("\n</xsl:template>\n</xsl:stylesheet>\n")
	sheet := strings.ReplaceAll(s.String(), "REC", g.rec)
	c := fuzzCase{seed: seed, sheet: sheet, doc: doc, malform: g.malform, kind: "accums"}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	if g.p(3) {
		c.maxBytes = int64(50 + g.n(800))
	}
	return c
}

// fuzzAccumize gives a fifth of the modes grammar's stylesheets, from a
// stream of their own, accumulators, a use-accumulators list on each mode,
// and a rule of the last mode reading them, ahead of the others.
func fuzzAccumize(seed int64, sheet, rec string) string {
	r := rand.New(rand.NewSource(seed*433494437 + 19))
	if !fuzzAccumShapes || r.Intn(100) >= 20 {
		return sheet
	}
	g := &fuzzGen{r: r, rec: rec}
	mode := ""
	if strings.Contains(sheet, `<xsl:mode name="m"`) {
		mode = "m"
	}
	rules := g.accumRules()
	rules = strings.ReplaceAll(rules, `<xsl:template match=`, `<xsl:template priority="9" match=`)
	if mode == "" {
		rules = strings.ReplaceAll(rules, ` mode="MODE"`, ``)
	}
	rules = strings.ReplaceAll(strings.ReplaceAll(rules, "MODE", mode), "REC", rec)
	decls := g.accumDecl("a") + g.accumDecl("b")
	decls = strings.ReplaceAll(decls, "REC", rec)
	use := g.accumUse()
	sheet = strings.ReplaceAll(sheet, "<xsl:mode", "<xsl:mode"+use)
	i := strings.Index(sheet, "<xsl:template")
	if i < 0 {
		return sheet
	}
	return sheet[:i] + decls + "\n" + rules + "\n" + sheet[i:]
}
