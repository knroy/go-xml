package xslt

// The grouping grammar of TestStreamFuzz and TestStreamFuzzMain (C8): a
// record document whose keys run (equal neighbours, a key alone first or
// last, a record without its key, other elements between), and a container
// grouping its records -- adjacent, starting-with, ending-with, by a
// composite key or a collation, nested, in a loop, the body reading the
// group in every way the core takes and some it must refuse (group-by, a
// sort, two consumers, a crawling population). The rule bodies of the
// modes grammar group too (fuzzGroupRule), apply-templates over
// current-group() among them, so that the principal input's leg has them.
// GOXSLT_STREAM_FUZZ_GROUPS=0 leaves them all out: every case is then the
// one the M2 grammar made for its seed.

import (
	"math/rand"
	"os"
	"strings"
)

// fuzzGroupShapes says the fuzzers may make grouping cases.
var fuzzGroupShapes = os.Getenv("GOXSLT_STREAM_FUZZ_GROUPS") != "0"

// fuzzIsGroups decides, from a random stream of its own, whether the case of
// seed is one of fuzzMakeGroups'.
func fuzzIsGroups(seed int64) bool {
	return fuzzGroupShapes && rand.New(rand.NewSource(seed*6700417+11)).Intn(100) < 25
}

// groupRecord is a record whose key is k (none when empty).
func (g *fuzzGen) groupRecord(k string) string {
	var b strings.Builder
	b.WriteString("<" + g.rec)
	if k != "" {
		b.WriteString(` k="` + k + `"`)
	}
	if g.p(90) {
		b.WriteString(` v="` + g.pick("1", "2", "-1", "3", "-5", "0", "x", "10", "2.5") + `"`)
	}
	if g.p(20) {
		b.WriteString("/>")
		return b.String()
	}
	b.WriteString(">")
	for i, c := 0, g.n(3); i < c; i++ {
		switch g.n(5) {
		case 0:
			b.WriteString(`<sub x="` + g.pick(fuzzNums...) + `">` + g.pick(fuzzKeys...) + "</sub>")
		case 1:
			b.WriteString(g.pick("text", " ", "5"))
		case 2:
			b.WriteString("<!-- c -->")
		case 3:
			if g.depth < 2 {
				g.depth++
				b.WriteString(g.groupRecord(g.pick(fuzzKeys...)))
				g.depth--
			}
		default:
			b.WriteString("<x>" + g.pick(fuzzNums...) + "</x>")
		}
	}
	b.WriteString("</" + g.rec + ">")
	return b.String()
}

// groupDocument is a record document whose keys run.
func (g *fuzzGen) groupDocument() string {
	var b strings.Builder
	b.WriteString("<" + g.root + ` xmlns:p="urn:p"`)
	if g.defNS {
		b.WriteString(` xmlns="urn:d"`)
	}
	b.WriteString(">")
	for _, w := range g.wrappers {
		b.WriteString("<" + w + ">")
	}
	n := g.n(9)
	if g.p(5) {
		n = 200 + g.n(800)
	}
	k := g.pick(fuzzKeys...)
	for i := 0; i < n; i++ {
		if g.p(40) {
			k = g.pick("a", "b", "B", "A", "c", "", "é", "E")
		}
		if g.p(12) {
			b.WriteString(g.pick("<other v=\"-1\"/>", "stray", "<!-- between -->", "<sub x=\"1\"/>"))
		}
		rk := k
		if g.p(4) {
			rk = "" // no key: XTTE1100 for group-adjacent="@k"
		}
		b.WriteString(g.groupRecord(rk))
	}
	for i := len(g.wrappers) - 1; i >= 0; i-- {
		b.WriteString("</" + g.wrappers[i] + ">")
	}
	b.WriteString("</" + g.root + ">")
	doc := b.String()
	if g.p(6) {
		g.malform = true
		doc = doc[:g.n(len(doc)+1)]
	}
	return doc
}

// groupBodies are bodies of a grouping: reading the group, its first item,
// or nothing of it; and some the core must refuse. CORE stands for a body
// a loop over the group's members has.
var fuzzGroupBodies = []string{
	`<g k="{current-grouping-key()}" p="{position()}" f="{@v}"/>`, `<g/>`,
	`<g><xsl:copy-of select="current-group()"/></g>`, `<xsl:copy-of select="current-group()"/>`,
	`<g n="{count(current-group())}"/>`, `<g><xsl:value-of select="sum(current-group()/@v)"/></g>`,
	`<g><xsl:value-of select="current-group()/@k" separator="-"/></g>`, `<g><xsl:value-of select="current-group()"/></g>`,
	`<g><xsl:copy-of select="current-group()[1]"/></g>`, `<g><xsl:copy-of select="current-group()[2]"/></g>`,
	`<g><xsl:copy-of select="current-group()[@v &gt; 0]"/></g>`, `<g><xsl:copy-of select="current-group()/sub"/></g>`,
	`<g><xsl:sequence select="current-group()"/></g>`, `<g><xsl:value-of select="string-join(current-group()/x, '+')"/></g>`,
	`<g><xsl:value-of select="exists(current-group()[@v = '3'])"/></g>`, `<g><xsl:value-of select="avg(current-group()/@v)"/></g>`,
	`<g><xsl:value-of select="count(current-group()//x)"/></g>`, `<g s="{string-join(current-group()/@k, '')}"/>`,
	`<f><xsl:copy-of select="."/></f>`, `<f><xsl:value-of select="."/></f>`, `<f n="{count(sub)}"/>`,
	`<g><xsl:for-each select="current-group()">CORE</xsl:for-each></g>`,
	`<g><xsl:iterate select="current-group()"><xsl:param name="s" select="0"/><xsl:on-completion><s v="{$s}"/></xsl:on-completion>` +
		`<xsl:next-iteration><xsl:with-param name="s" select="$s + string-length(@k)"/></xsl:next-iteration></xsl:iterate></g>`,
	`<w k="{current-grouping-key()}"><xsl:for-each-group select="current-group()" group-adjacent="string(@v)">` +
		`<i v="{current-grouping-key()}"><xsl:copy-of select="current-group()"/></i></xsl:for-each-group></w>`,
	`<w><xsl:for-each-group select="current-group()" group-starting-with="*[@v = '1']"><i n="{count(current-group())}"/></xsl:for-each-group></w>`,
	`<xsl:if test="@k = 'a'"><a><xsl:copy-of select="current-group()"/></a></xsl:if>`,
	`<xsl:choose><xsl:when test="position() = 1"><one><xsl:value-of select="count(current-group())"/></one></xsl:when>` +
		`<xsl:otherwise><o><xsl:copy-of select="current-group()/@v"/></o></xsl:otherwise></xsl:choose>`,
	// refused: two consumers, current-group() where a view cannot have it
	`<g n="{count(current-group())}"><xsl:copy-of select="current-group()"/></g>`,
	`<g><xsl:value-of select="count(current-group()[last()])"/></g>`, `<xsl:sequence select="current-group()"/>`,
	`<g><xsl:copy-of select="."/><xsl:copy-of select="current-group()"/></g>`,
}

// fuzzGroupCores are bodies of a loop over the group's members.
var fuzzGroupCores = []string{`<m p="{position()}" v="{@v}"><xsl:value-of select="."/></m>`, `<xsl:copy-of select="."/>`,
	`<m k="{@k}"/>`, `<m><xsl:value-of select="count(sub)"/></m>`, `<m><xsl:copy-of select="sub"/></m>`}

// group is a for-each-group over pop with a body; esc escapes for an
// attribute.
func (g *fuzzGen) group(pop string) string {
	esc := func(x string) string { return strings.ReplaceAll(strings.ReplaceAll(x, "<", "&lt;"), `"`, "&quot;") }
	var by string
	switch g.n(12) {
	case 0, 1, 2:
		by = `group-adjacent="` + esc(g.pick("@k", "@k", "@v", "string(@k)", "name()", "position() idiv 2",
			"@k = 'a'", "number(@v) ge 0", "@missing", "string(.)", "@k, @v", "upper-case(@k)")) + `"`
		if g.p(25) {
			by += ` composite="yes"`
		}
		if g.p(20) {
			by += ` collation=` + strings.ReplaceAll(fuzzColl, "'", `"`)
		}
	case 3, 4, 5:
		by = `group-starting-with="` + esc(g.pick("REC[@v < 0]", "*[@k = 'a']", "REC[not(@k)]", "*", "other",
			"REC[1]", "REC[sub]", "REC[@v = '1']")) + `"`
	case 6, 7, 8:
		by = `group-ending-with="` + esc(g.pick("REC[@v < 0]", "*[@k = 'b']", "REC[@k = '']", "*", "x",
			"REC[last()]", "REC[@v > 2]")) + `"`
	case 9:
		by = `group-by="@k"` // refused
	default:
		by = `group-adjacent="@k"`
	}
	body := g.pick(fuzzGroupBodies...)
	body = strings.ReplaceAll(body, "CORE", g.pick(fuzzGroupCores...))
	sort := ""
	if g.p(5) {
		sort = `<xsl:sort select="@k"/>` // refused
	}
	return `<xsl:for-each-group select="` + esc(pop) + `" ` + by + `>` + sort + body + `</xsl:for-each-group>`
}

// fuzzMakeGroups is a grouping case: a container grouping the records of a
// key-run document.
func fuzzMakeGroups(seed int64) fuzzCase {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*131 + 5))}
	g.root = g.pick("doc", "root", "account")
	g.rec = g.pick("rec", "rec", "item", "p:rec")
	g.defNS = g.p(10)
	if g.p(40) {
		g.wrappers = []string{g.pick("wrap", "data")}
	}
	doc := g.groupDocument()
	steps := strings.Join(append(append([]string{g.root}, g.wrappers...), g.rec), "/")
	var content string
	if len(g.wrappers) > 0 && g.p(40) {
		// In a loop over the wrappers: the population from each.
		w := strings.Join(append([]string{g.root}, g.wrappers...), "/")
		content = `<xsl:for-each select="` + w + `"><l>` + g.group(g.pick(g.rec, "*", g.rec+"[@v > 0]")) + `</l></xsl:for-each>`
	} else {
		content = g.group(g.pick(steps, steps, "/"+steps, steps+"[@v != '0']", strings.Replace(steps, g.rec, "*", 1),
			"//"+g.rec, "outermost(//"+g.rec+")", steps+"[2]", steps+"/sub"))
	}
	content = strings.ReplaceAll(content, "REC", g.rec)
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
	c := fuzzCase{seed: seed, sheet: s.String(), doc: doc, malform: g.malform, kind: "groups"}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	if g.p(3) {
		c.maxBytes = int64(50 + g.n(800))
	}
	return c
}

// fuzzGroupRule is, for the modes grammar, a rule body that groups the
// children of the rule's element, from r, a stream of its own (so that the
// grammar's own draws are unchanged); "" when this rule keeps its body.
func fuzzGroupRule(r *rand.Rand, rec, mode string) string {
	if !fuzzGroupShapes || r.Intn(100) >= 20 {
		return ""
	}
	g := &fuzzGen{r: r, rec: rec}
	pop := g.pick(rec, "*", rec+"[@k]", "sub", rec+"/sub")
	if g.p(40) {
		by := g.pick(`group-adjacent="@k"`, `group-adjacent="string(@v)"`, `group-starting-with="*[@v = '1']"`,
			`group-ending-with="`+rec+`[@k = 'b']"`)
		inner := g.pick(`<xsl:apply-templates select="current-group()" mode="MODE"/>`,
			`<xsl:apply-templates select="current-group()"/>`,
			`<xsl:apply-templates select="current-group()[1]" mode="#current"/>`,
			`<xsl:apply-templates select="current-group()/sub"/>`)
		return `<b n="{name()}"><xsl:for-each-group select="` + pop + `" ` + by + `><g p="{position()}">` +
			strings.ReplaceAll(inner, "MODE", mode) + `</g></xsl:for-each-group></b>`
	}
	return strings.ReplaceAll(g.group(pop), "REC", rec)
}
