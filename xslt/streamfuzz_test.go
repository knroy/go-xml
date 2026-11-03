package xslt

// TestStreamFuzz is the differential fuzzer for streamed execution: each
// generated case -- a random record document, possibly malformed or over a
// node or byte limit, and a streamable xsl:source-document whose body is one
// value-of/copy-of/sequence over a record path or a for-each/iterate over
// one, many of them shapes the gate must refuse, or (fuzzCoreShapes) the
// streamed core's shapes: wrappers, loops whose bodies read their matches,
// selects inside -- runs streamed and on the tree, which must agree exactly,
// error text and all: the core re-runs on any failure, so its errors are the
// tree's; a re-run where the tree succeeds is a difference too.
//
//	go test ./xslt -run 'TestStreamFuzz$' -v                      # default 1000 cases from seed 1
//	GOXSLT_STREAM_FUZZ=100000 GOXSLT_STREAM_FUZZ_SEED=1 go test ./xslt -run 'TestStreamFuzz$' -v -timeout 0
//
// GOXSLT_STREAM_FUZZ_CORE=0 makes only the stage-1b cases, each exactly as
// that grammar made it for its seed, for comparing a run with an older one.
// GOXSLT_STREAM_FUZZ_MODES=0 leaves out the M2 cases (fuzzMakeModes: modes,
// template rules and the built-in rules), so that every other case is the one
// the M1 grammar made for its seed.
//
// A failing case prints its seed and the command that replays it alone.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

type fuzzGen struct {
	r        *rand.Rand
	root     string
	wrappers []string
	rec      string
	malform  bool
	defNS    bool // the document's default namespace is urn:d
	kind     string
	depth    int
	// maxDepth bounds how deep records nest; 0 is 4.
	maxDepth int
}

func (g *fuzzGen) n(k int) int              { return g.r.Intn(k) }
func (g *fuzzGen) p(pc int) bool            { return g.r.Intn(100) < pc }
func (g *fuzzGen) pick(xs ...string) string { return xs[g.r.Intn(len(xs))] }

var fuzzNums = []string{"1", "2", "3", "-1", "0", "-0", "2.5", "10", "1e3", "1.0E-2", "NaN", "INF", "-INF",
	"007", " 4 ", "x", "", "12345678901234567890", "0.1", "0.2", "0.30000000000000004", "3.0"}
var fuzzKeys = []string{"a", "A", "b", "B", "ab", "", " a", "é", "E", "z", "1", "10", "2"}

func (g *fuzzGen) record() string {
	var b strings.Builder
	b.WriteString("<" + g.rec)
	if g.p(85) {
		b.WriteString(` k="` + g.pick(fuzzKeys...) + `"`)
	}
	if g.p(85) {
		b.WriteString(` v="` + g.pick(fuzzNums...) + `"`)
	}
	if g.p(10) {
		b.WriteString(` xml:lang="` + g.pick("en", "de") + `"`)
	}
	if g.p(15) {
		b.WriteString("/>")
		return b.String()
	}
	b.WriteString(">")
	for i, c := 0, g.n(4); i < c; i++ {
		if g.depth < max(g.maxDepth, 4) && g.p(20) {
			g.depth++
			if g.p(50) {
				b.WriteString(g.record())
			} else {
				w := g.pick("sub", "other", "wrap", "p:w")
				b.WriteString("<" + w + g.pick("", ` id="n"`) + ">" + g.record() + "</" + w + ">")
			}
			g.depth--
			continue
		}
		switch g.n(6) {
		case 0:
			b.WriteString(`<sub x="` + g.pick(fuzzNums...) + `">` + g.pick(fuzzKeys...) + "</sub>")
		case 1:
			b.WriteString("<name>" + g.pick(fuzzKeys...) + "</name>")
		case 2:
			b.WriteString(g.pick("text", " ", "\n  ", "5"))
		case 3:
			b.WriteString("<x>" + g.pick(fuzzNums...) + "</x>")
		case 4:
			b.WriteString("<!-- c -->")
		default:
			b.WriteString(`<sub><x>` + g.pick(fuzzNums...) + `</x></sub>`)
		}
	}
	b.WriteString("</" + g.rec + ">")
	return b.String()
}

func (g *fuzzGen) document() string {
	var b strings.Builder
	if g.p(30) {
		b.WriteString(`<?xml version="1.0"?>` + "\n")
	}
	b.WriteString("<" + g.root + ` xmlns:p="urn:p"`)
	if g.defNS {
		b.WriteString(` xmlns="urn:d"`)
	}
	if g.p(20) {
		b.WriteString(` xml:base="http://ex/a/"`)
	}
	b.WriteString(">\n")
	for _, w := range g.wrappers {
		b.WriteString("<" + w + g.pick("", ` id="w"`) + ">\n")
	}
	nrec := g.n(6)
	if g.p(5) {
		nrec = 50 + g.n(400)
	}
	if g.p(2) {
		nrec = 1500 + g.n(1000) // several burst batches
	}
	for i := 0; i < nrec; i++ {
		if g.p(25) {
			b.WriteString(g.pick("<!-- between -->", "<other v=\"9\"><rec v=\"inner\"/></other>", "stray", "<rec2 v=\"5\"/>", "  ") + "\n")
		}
		if g.p(1) {
			// a record of more than one batch's small-burst limit
			b.WriteString("<" + g.rec + ` k="big" v="1">` + strings.Repeat("<x>1</x>", 300+g.n(300)) + "</" + g.rec + ">\n")
		}
		b.WriteString(g.record() + "\n")
	}
	for i := len(g.wrappers) - 1; i >= 0; i-- {
		b.WriteString("</" + g.wrappers[i] + ">\n")
	}
	b.WriteString("</" + g.root + ">")
	doc := b.String()
	if g.p(8) {
		g.malform = true
		switch g.n(4) {
		case 0:
			doc = doc[:g.n(len(doc)+1)]
		case 1:
			doc = strings.Replace(doc, "</"+g.root+">", "</wrong>", 1)
		case 2:
			doc += "<second/>"
		default:
			doc = strings.Replace(doc, "</"+g.root+">", `<`+g.rec+` v="1" v="2"/></`+g.root+">", 1)
		}
	}
	return doc
}

// path is a record path, sometimes mutated into one the gate must refuse.
func (g *fuzzGen) path() string {
	if g.p(60) {
		g.kind = "p2"
		return g.path2()
	}
	g.kind = "p1"
	return g.path1()
}

// wild replaces a name step by a wildcard that still matches it, or not.
func (g *fuzzGen) wild(step string) string {
	local, prefixed := step, false
	if i := strings.IndexByte(step, ':'); i >= 0 {
		local, prefixed = step[i+1:], true
	}
	switch g.n(10) {
	case 0, 1, 2:
		return "*"
	case 3, 4:
		return "*:" + local
	case 5:
		if prefixed {
			return "p:*"
		}
		return "q:*"
	case 6:
		return "*:nomatch"
	}
	return step
}

// path2 is a record path with descendant steps, wildcards or outermost().
func (g *fuzzGen) path2() string {
	preds := func() string {
		s := ""
		for i, c := 0, g.n(3); i < c; i++ {
			if g.p(50) {
				s += "[" + g.pred() + "]"
			}
		}
		return s
	}
	tail := func() string {
		if g.p(40) {
			return ""
		}
		return g.tail()
	}
	steps := append([]string{g.root}, g.wrappers...)
	steps = append(steps, g.rec)
	switch g.n(16) {
	case 0, 1, 2:
		return "//" + g.rec + preds() + tail()
	case 3:
		return g.pick("", "/") + g.root + "//" + g.rec + preds() + tail()
	case 4, 5:
		for i := range steps {
			if g.p(50) {
				steps[i] = g.wild(steps[i])
			}
		}
		return g.pick("", "/") + strings.Join(steps, "/") + preds() + tail()
	case 6:
		return "//" + g.wild(g.rec) + preds() + tail()
	case 7, 8:
		in := g.pick("//"+g.rec, g.root+"//"+g.rec, "//"+g.wild(g.rec), "descendant::"+g.rec, "//"+g.rec+"/sub")
		p := g.pick("outermost", "outermost", "innermost") + "(" + in + preds() + ")"
		if t := tail(); t != "" {
			p += t
		}
		return p
	case 9:
		return "descendant::" + g.rec + preds() + tail()
	case 10:
		return g.pick("//text()", "//@v", "//*", "//node()", "//@*", "//sub", "//x", "//comment()", "//*/@k", "/descendant::*", "//*[@v]")
	case 11:
		return "//" + g.rec + g.pick("//text()", "//sub", "//@x", "//"+g.rec, "/descendant::x", "//*")
	case 12:
		return ".//" + g.rec + preds() + tail()
	case 13:
		w := g.pick("sub", "other", "wrap", "p:w")
		return "//" + w + "/" + g.rec + preds() + tail()
	case 14:
		return "//" + g.rec + "//" + g.rec + preds() + tail()
	}
	return "/" + g.root + "/descendant::" + g.rec + preds() + tail()
}

func (g *fuzzGen) path1() string {
	steps := append([]string{g.root}, g.wrappers...)
	steps = append(steps, g.rec)
	last := len(steps) - 1
	switch g.n(25) {
	case 0:
		steps[last] = "*"
	case 1:
		return "//" + g.rec + g.tail()
	case 2:
		steps = append([]string{"."}, steps...)
		last++
	case 3:
		if len(steps) > 1 {
			steps[0] += "[" + g.pred() + "]"
		}
	}
	for i, c := 0, g.n(3); i < c; i++ {
		if g.p(60) {
			steps[last] += "[" + g.pred() + "]"
		}
	}
	p := strings.Join(steps, "/") + g.tail()
	if g.p(30) {
		p = "/" + p
	}
	return p
}

func (g *fuzzGen) pred() string {
	return g.pick("@k = 'a'", "@k", "@v > 2", "@v = 1", "sub", "not(@v)", "1", "2", "last()", "position() = 1",
		"@k = ('a', 'B')", "string-length(@k) > 0", "exists(sub/@x)", "../@id", "@v castable as xs:double",
		"xs:integer(@v) = 1", "number(@v) = number(@v)", "@xml:lang = 'en'", "name = 'a'", "x > 1", "*[1]",
		"@k eq 'a'", "@v gt 1", "ancestor::*/@id", "count(sub) = 1")
}

func (g *fuzzGen) tail() string {
	if g.p(25) {
		return ""
	}
	if len(fuzzTailsExtra) > 0 && g.p(12) {
		return "/" + g.pick(fuzzTailsExtra...) // streamleaffuzz_test.go
	}
	return "/" + g.pick("@v", "@v", "@k", "@k", "@*", "sub", "sub/@x", "name", "text()", "x", "@v/data()",
		"@v/xs:decimal(.)", "@v/xs:double(.)", "@v/number()", "@v/xs:integer(.)", "@v/xs:float(.)",
		"(if (@k) then xs:decimal(@v) else xs:double(@v))", "(if (@k = 'a') then xs:float(@v) else xs:integer(@v))",
		"@k/string()", "copy-of()", "descendant::*", "*[1]", "sub[1]/@x", "string()", "string-length(.)",
		"(@v castable as xs:double)", "x/data()[. > 1]", "@v/xs:untypedAtomic(.)", "@k/xs:anyURI(.)",
		"(if (@v castable as xs:integer) then xs:integer(@v) else xs:string(@v))", "..", "../@id",
		"(@k, @v)", "node()", "descendant-or-self::x", "@v/(. + 1)", "sub/x/number()", "position()")
}

const fuzzColl = `'http://www.w3.org/2005/xpath-functions/collation/html-ascii-case-insensitive'`

func (g *fuzzGen) selectExpr() string {
	p := g.path()
	t := g.pick(
		"count(P)", "count(P)", "sum(P)", "sum(P)", "sum(P, 'zero')", "sum(P, ())", "avg(P)", "min(P)", "max(P)",
		"min(P)", "max(P)", "min(P, "+fuzzColl+")", "max(P, "+fuzzColl+")", "max(P ! string())", "min(P ! number())",
		"sum(P ! xs:decimal(.))", "exists(P)", "empty(P)", "boolean(P)", "not(P)", "head(P)", "exactly-one(P)",
		"zero-or-one(P)", "one-or-more(P)", "deep-equal(P ! string(), ('a', 'B'))", "P = 'a'", "P != 1", "P > 2",
		"P eq 1", "(P)[1]", "(P)[2]", "(P)[1] + 1", "string(head(P))", "number(head(P))", "xs:string(P)",
		"string(P)", "some $x in P satisfies $x = 'a'", "every $x in P satisfies $x > 0",
		"if (P) then 1 else 0", "P instance of xs:string*", "count(P) + 1", "round(sum(P))", "string-join(P, ',')",
		"P ! string()", "P ! position()", "for $x in P return string($x)", "copy-of(P)", "copy-of(P)[1]",
		"tail(P)/@k", "copy-of(P)/sub", "data(P)", "insert-before(P, 1, 'z')", "remove(P, 1)", "subsequence(P, 2, 2)",
		"index-of(P ! string(), 'a')", "count(P[. = 'a'])", "P[position() > 1]", "sum(unordered(P))",
		"max(P) - min(P)", "-sum(P)", "avg(P) * 2", "count(P) = 0", "(sum(P), 1)", "(1, count(P))",
		"concat('n=', count(P))", "string-length(string-join(P ! string(), ''))", "upper-case(string(head(P)))",
		// refused
		"distinct-values(P)", "reverse(P)", "P[last()]", "count(P union ())", "let $x := P return count($x)",
		"count(P) + count(P)", "sort(P)", "(P) ! name(..)", "P is P",
	)
	if g.p(15) {
		return p // bare: the whole select is the path
	}
	if len(fuzzSelectsExtra) > 0 && g.p(10) {
		t = g.pick(fuzzSelectsExtra...) // streamleaffuzz_test.go
	}
	return strings.ReplaceAll(t, "P", "("+p+")")
}

type fuzzCase struct {
	seed       int64
	sheet, doc string
	malform    bool
	maxNodes   int
	maxBytes   int64
	kind       string
}

// fuzzCoreShapes says fuzzMake may make a case of the core's shapes (C5): off
// with GOXSLT_STREAM_FUZZ_CORE=0, when every case is the one the stage-1b
// grammar made for its seed, so that a run compares with the base's.
var fuzzCoreShapes = os.Getenv("GOXSLT_STREAM_FUZZ_CORE") != "0"

// fuzzIsCore decides, from a random stream of its own, whether the case of
// seed is a core shape: the stage-1b cases keep their draws.
func fuzzIsCore(seed int64) bool {
	return fuzzCoreShapes && rand.New(rand.NewSource(seed*7919+17)).Intn(100) < 40
}

// fuzzModeShapes says fuzzMake may make a case of the M2 grammar
// (GOXSLT_STREAM_FUZZ_MODES=0 turns it off).
var fuzzModeShapes = os.Getenv("GOXSLT_STREAM_FUZZ_MODES") != "0"

// fuzzIsModes decides, from a random stream of its own, whether the case of
// seed is one of fuzzMakeModes'.
func fuzzIsModes(seed int64) bool {
	return fuzzModeShapes && rand.New(rand.NewSource(seed*104729+31)).Intn(100) < 35
}

// fuzzPatterns are match patterns: of elements, attributes, text, the
// document node; a few a view cannot decide (positional, reading content).
var fuzzPatterns = []string{"REC", "REC", "*", "*", "node()", "text()", "@*", "@k", "@v", "sub", "x", "name",
	"REC[@k = 'a']", "REC[@v > 2]", "REC[@k][@v]", "ROOT/REC", "/ROOT", "/", "document-node()", "*:REC", "p:*",
	"sub[@x]", "wrap//REC", "REC/sub", "comment()", "node()[not(self::x)]", "text()[. = 'text']",
	"sub|x", "REC|@v", "other", "REC[1]", "REC[sub]", "REC[. = 'a']", "*[last()]", "sub/text()", "@*[. = '1']"}

// fuzzRuleBodies are template rule bodies; MODE is replaced by a mode name.
var fuzzRuleBodies = []string{
	``, `<xsl:copy><xsl:apply-templates select="@*"/><xsl:apply-templates/></xsl:copy>`,
	`<r k="{@k}"><xsl:apply-templates/></r>`, `<xsl:apply-templates/>`, `<xsl:apply-templates mode="MODE"/>`,
	`<xsl:apply-templates mode="#current"/>`, `<w><xsl:next-match/></w>`, `<xsl:copy-of select="."/>`,
	`<v><xsl:value-of select="."/></v>`, `<xsl:apply-templates select="sub"/>`, `<xsl:apply-templates select="*"/>`,
	`<n p="{position()}" l="{local-name()}"/>`, `<xsl:value-of select="upper-case(.)"/>`, `<xsl:copy/>`,
	`<w><xsl:next-match><xsl:with-param name="q" select="'Q'"/></xsl:next-match></w>`, `<xsl:apply-imports/>`,
	`<xsl:param name="q" select="'-'"/><q v="{$q}"><xsl:apply-templates/></q>`,
	`<xsl:param name="t" tunnel="yes" select="'d'"/><t t="{$t}"><xsl:apply-templates>` +
		`<xsl:with-param name="t" tunnel="yes" select="concat($t, '+')"/></xsl:apply-templates></t>`,
	`<xsl:param name="t" tunnel="yes" select="'d'"/><t t="{$t}"><xsl:next-match/></t>`,
	`<xsl:attribute name="a-{local-name()}" select="."/>`, `<xsl:if test="@k = 'a'"><a><xsl:apply-templates/></a></xsl:if>`,
	`<xsl:choose><xsl:when test="@v = '1'"><one><xsl:copy-of select="."/></one></xsl:when>` +
		`<xsl:otherwise><xsl:apply-templates/></xsl:otherwise></xsl:choose>`,
	`<d n="{count(ancestor::*)}"><xsl:apply-templates select="outermost(.//REC)"/></d>`,
	`<xsl:apply-templates select="REC[@k = 'a']"/>`, `<s><xsl:value-of select="sum(sub/@x)"/></s>`,
	`<xsl:for-each select="sub"><f x="{@x}"><xsl:apply-templates/></f></xsl:for-each>`,
	`<xsl:apply-templates select="."  mode="MODE"/>`, `<c><xsl:value-of select="count(*)"/></c>`,
	// refused
	`<xsl:apply-templates/><xsl:apply-templates/>`, `<xsl:value-of select="following-sibling::*[1]"/>`,
	`<xsl:apply-templates select="sub"><xsl:sort select="@x"/></xsl:apply-templates>`,
}

// fuzzMakeModes is an M2 case (C6): one or two modes, streamable or not,
// with on-no-match, template rules of random patterns, priorities and
// bodies (apply-templates, next-match, tunnels, mode switches), and a
// container that applies them, over a record document perhaps nested deep.
func fuzzMakeModes(seed int64) fuzzCase {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*31 + 7))}
	g.root = g.pick("doc", "root", "account")
	g.rec = g.pick("rec", "rec", "item", "p:rec")
	g.defNS = g.p(10)
	if g.p(30) {
		g.maxDepth = 4 + g.n(12)
	}
	for i, c := 0, g.n(3); i < c; i++ {
		if g.p(50) {
			g.wrappers = append(g.wrappers, g.pick("wrap", "data", "list"))
		}
	}
	doc := g.document()
	sub := func(x string) string {
		x = strings.ReplaceAll(x, "ROOT", g.root)
		return strings.ReplaceAll(x, "REC", g.rec)
	}
	modes := []string{""}
	if g.p(50) {
		modes = append(modes, "m")
	}
	var s strings.Builder
	s.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:p="urn:p" xmlns:err="http://www.w3.org/2005/xqt-errors" exclude-result-prefixes="xs p err"`)
	if g.defNS && g.p(85) {
		s.WriteString(` xpath-default-namespace="urn:d"`)
	}
	s.WriteString(">\n")
	if g.p(30) {
		s.WriteString(g.pick(`<xsl:strip-space elements="*"/>`, `<xsl:strip-space elements="sub"/>`) + "\n")
	}
	for _, m := range modes {
		s.WriteString(`<xsl:mode`)
		if m != "" {
			s.WriteString(` name="` + m + `"`)
		}
		if g.p(92) {
			s.WriteString(` streamable="yes"`)
		}
		if g.p(75) {
			s.WriteString(` on-no-match="` + g.pick("shallow-copy", "shallow-copy", "deep-copy", "shallow-skip",
				"deep-skip", "text-only-copy", "fail") + `"`)
		}
		if g.p(8) {
			s.WriteString(` on-multiple-match="fail"`)
		}
		s.WriteString("/>\n")
	}
	gr := rand.New(rand.NewSource(seed*39916801 + 13)) // the grouping rule bodies' draws
	tr := rand.New(rand.NewSource(seed*1000003 + 19))  // the try rule bodies' draws
	calls := false
	fr := rand.New(rand.NewSource(seed*524287 + 19)) // the fork rule bodies' (streamforkfuzz_test.go)
	for i, n := 0, g.n(6); i < n; i++ {
		s.WriteString(`<xsl:template match="` + strings.ReplaceAll(sub(g.pick(fuzzPatterns...)), "<", "&lt;") + `"`)
		if g.p(40) || len(modes) > 1 && g.p(50) {
			m := g.pick(modes...)
			if m == "" {
				m = g.pick("#unnamed", "#default")
			}
			s.WriteString(` mode="` + m + `"`)
		}
		if g.p(30) {
			s.WriteString(` priority="` + g.pick("-1", "0", "1", "2", "0.5") + `"`)
		}
		body := sub(g.pick(fuzzRuleBodies...))
		body = strings.ReplaceAll(body, "MODE", g.pick(modes[len(modes)-1], "#default", "#current"))
		if gb := fuzzGroupRule(gr, g.rec, modes[len(modes)-1]); gb != "" {
			body = gb
		}
		// Both streams draw for every rule, so each lane's seeds keep their
		// draws; a try body wins over a fork body.
		tb, fb := fuzzTryRule(tr, g.rec), fuzzForkRule(fr, g.rec)
		if tb != "" {
			body, calls = tb, true
		} else if fb != "" {
			body = fb
		}
		s.WriteString(">" + body + "</xsl:template>\n")
	}
	s.WriteString(`<xsl:template name="main">` + "\n")
	try := g.p(8)
	if try {
		s.WriteString("<xsl:try>")
	}
	s.WriteString(`<xsl:source-document streamable="yes" href="doc.xml">`)
	body := g.pick(`<xsl:apply-templates select="."/>`, `<xsl:apply-templates select="."/>`, `<xsl:apply-templates/>`,
		`<xsl:apply-templates select="ROOT/REC"/>`, `<xsl:apply-templates select="*/*"/>`,
		`<xsl:apply-templates select="." mode="m"/>`, `<xsl:apply-templates mode="m"/>`,
		`<xsl:apply-templates select="."><xsl:with-param name="t" tunnel="yes" select="'T'"/></xsl:apply-templates>`,
		`<xsl:apply-templates><xsl:with-param name="q" select="'P'"/></xsl:apply-templates>`,
		`<xsl:for-each select="ROOT/REC"><xsl:apply-templates select="."/></xsl:for-each>`)
	body = sub(body)
	if g.p(30) {
		body = g.coreWrap(body)
	}
	s.WriteString(body)
	s.WriteString("</xsl:source-document>")
	if try {
		s.WriteString(`<xsl:catch><caught code="{local-name-from-QName($err:code)}"/></xsl:catch></xsl:try>`)
	}
	s.WriteString("\n</xsl:template>\n")
	if calls {
		s.WriteString(fuzzCallDecls)
	}
	s.WriteString("</xsl:stylesheet>\n")
	c := fuzzCase{seed: seed, sheet: fuzzAccumize(seed, s.String(), g.rec), doc: doc, malform: g.malform, kind: "modes"}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	if g.p(3) {
		c.maxBytes = int64(50 + g.n(800))
	}
	return c
}

// coreLeaf is an element-context consumer for a loop body (C5): the match's
// own content read at its start tag, a reducer of its children, a nested
// loop, or none (a motionless body).
func (g *fuzzGen) coreBody() string {
	return g.pick(
		`<r><xsl:value-of select="."/></r>`, `<xsl:copy-of select="."/>`,
		`<r n="{name()}"><xsl:value-of select="count(sub)"/></r>`,
		`<r><xsl:value-of select="sum(sub/@x)"/></r>`, `<r><xsl:sequence select="exists(sub)"/></r>`,
		`<r k="{@k}"><xsl:for-each select="sub"><s x="{@x}" p="{position()}"/></xsl:for-each></r>`,
		`<xsl:copy><xsl:copy-of select="@*"/><xsl:value-of select="string(.)"/></xsl:copy>`,
		`<xsl:if test="@k = 'a'"><a><xsl:value-of select="."/></a></xsl:if>`,
		`<xsl:choose><xsl:when test="@v = '1'"><one><xsl:copy-of select="sub"/></one></xsl:when>`+
			`<xsl:otherwise><r p="{position()}"/></xsl:otherwise></xsl:choose>`,
		`<r><xsl:attribute name="n" select="count(.//node())"/></r>`,
		`<xsl:element name="e{position()}"><xsl:value-of select="sub[1]/@x"/></xsl:element>`,
		`<r><xsl:value-of select="max(sub/@x)"/></r>`, // refused: no fallback below the document
		`<r k="{@k}" p="{position()}"/>`, `<xsl:copy/>`, `<r a="{ancestor::*[1]/@id}" d="{count(ancestor::*)}"/>`,
		`<r><xsl:copy-of select="sub"/></r>`, `<r><xsl:value-of select="string-join(sub/@x, '-')"/></r>`,
	)
}

// coreWrap wraps s, the content of a container, in a random wrapper.
func (g *fuzzGen) coreWrap(s string) string {
	switch g.n(8) {
	case 0:
		return `<out a="{1 + 1}">` + s + `</out>`
	case 1:
		return `<xsl:element name="w{2}">` + s + `</xsl:element>`
	case 2:
		return `<xsl:if test="true()">` + s + `</xsl:if>`
	case 3:
		return `<xsl:choose><xsl:when test="false()"><no/></xsl:when><xsl:otherwise>` + s + `</xsl:otherwise></xsl:choose>`
	case 4:
		return `<xsl:variable name="m" select="'v'"/><o m="{$m}">` + s + `</o>`
	case 5:
		return `<pre/>` + s + `<post n="{1}"/>`
	case 6:
		return `<x><xsl:source-document streamable="yes" href="doc.xml"><n><xsl:value-of select="count(//sub)"/></n>` +
			`</xsl:source-document>` + s + `</x>`
	}
	return `<w>` + s + `</w>`
}

// coreContent is a container body of the core's shapes, and whether it is
// a loop.
func (g *fuzzGen) coreContent(esc func(string) string) (string, bool) {
	var s string
	loop := true
	switch g.n(10) {
	case 0, 1, 2:
		loop = false
		sel := esc(g.selectExpr())
		switch g.n(4) {
		case 0:
			s = `<xsl:value-of select="` + sel + `"/>`
		case 1:
			s = `<xsl:copy-of select="` + sel + `"/>`
		case 2:
			s = `<xsl:sequence select="` + sel + `"/>`
		default:
			s = `<e><xsl:attribute name="n" select="` + sel + `"/></e>`
		}
	case 3:
		loop = false
		s = `<static>text</static>`
	case 4:
		p := esc(g.path())
		s = `<xsl:iterate select="` + p + `"><xsl:param name="t" select="0"/><xsl:on-completion><t v="{$t}"/></xsl:on-completion>` +
			`<xsl:next-iteration><xsl:with-param name="t" select="$t + ` + g.pick("1", "count(.//node())", "string-length(@k)") +
			`"/></xsl:next-iteration></xsl:iterate>`
	default:
		p := esc(g.path())
		s = `<xsl:for-each select="` + p + `">` + g.coreBody() + `</xsl:for-each>`
	}
	for i, n := 0, g.n(3); i < n; i++ {
		s = g.coreWrap(s)
	}
	return s, loop
}

func fuzzMake(seed int64) fuzzCase {
	if fuzzIsHold(seed) {
		return fuzzMakeHold(seed) // streamholdfuzz_test.go
	}
	if fuzzIsG5(seed) {
		return fuzzMakeG5(seed) // streamg5fuzz_test.go
	}
	if fuzzIsForks(seed) {
		return fuzzMakeForks(seed) // streamforkfuzz_test.go
	}
	if fuzzIsGroups(seed) {
		return fuzzMakeGroups(seed)
	}
	if fuzzIsPaths(seed) {
		return fuzzMakePaths(seed) // streampopfuzz_test.go
	}
	if fuzzIsTry(seed) {
		return fuzzMakeTry(seed)
	}
	if fuzzIsShapes(seed) {
		return fuzzMakeShapes(seed) // streamshapesfuzz_test.go
	}
	if fuzzIsAccums(seed) {
		return fuzzMakeAccums(seed)
	}
	if fuzzIsModes(seed) {
		return fuzzMakeModes(seed)
	}
	g := &fuzzGen{r: rand.New(rand.NewSource(seed))}
	g.root = g.pick("doc", "root", "account")
	g.rec = g.pick("rec", "rec", "item", "p:rec")
	g.defNS = g.p(15)
	for i, c := 0, g.n(3); i < c; i++ {
		if g.p(50) {
			g.wrappers = append(g.wrappers, g.pick("wrap", "data", "list"))
		}
	}
	doc := g.document()
	var s strings.Builder
	s.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:p="urn:p" xmlns:err="http://www.w3.org/2005/xqt-errors" exclude-result-prefixes="xs p err"`)
	if g.p(10) {
		s.WriteString(` default-collation=` + strings.ReplaceAll(fuzzColl, "'", `"`))
	}
	if g.defNS && g.p(85) || g.p(3) {
		s.WriteString(` xpath-default-namespace="urn:d"`)
	}
	s.WriteString(">\n")
	if g.p(20) {
		s.WriteString(g.pick(`<xsl:strip-space elements="*"/>`, `<xsl:strip-space elements="sub"/>`) + "\n")
	}
	if g.p(15) {
		s.WriteString(`<xsl:output method="` + g.pick("xml", "text", "adaptive") + `"/>` + "\n")
	}
	s.WriteString(`<xsl:template name="main">` + "\n")
	try := g.p(10)
	if try {
		s.WriteString("<xsl:try>")
	}
	s.WriteString(`<xsl:source-document streamable="yes" href="doc.xml">`)
	if fuzzIsCore(seed) {
		content, loop := g.coreContent(func(x string) string {
			return strings.ReplaceAll(strings.ReplaceAll(x, "<", "&lt;"), `"`, "&quot;")
		})
		// A loop the burst path takes has its error order (fuzzKnown).
		g.kind = "core"
		if loop {
			g.kind = "core-loop"
		}
		s.WriteString(content)
		s.WriteString("</xsl:source-document>")
		if try {
			s.WriteString(`<xsl:catch><caught code="{local-name-from-QName($err:code)}"/></xsl:catch></xsl:try>`)
		}
		s.WriteString("\n</xsl:template>\n</xsl:stylesheet>\n")
		c := fuzzCase{seed: seed, sheet: s.String(), doc: doc, malform: g.malform, kind: g.kind}
		if g.p(3) {
			c.maxNodes = 5 + g.n(60)
		}
		if g.p(3) {
			c.maxBytes = int64(50 + g.n(800))
		}
		return c
	}
	wrap := g.p(30)
	if wrap {
		s.WriteString(`<out a="x">`)
	}
	esc := func(x string) string { return strings.ReplaceAll(strings.ReplaceAll(x, "<", "&lt;"), `"`, "&quot;") }
	if g.p(40) {
		p := esc(g.path())
		g.kind += "-loop"
		switch g.n(10) {
		case 0:
			s.WriteString(`<xsl:iterate select="` + p + `"><xsl:param name="t" select="0"/><xsl:on-completion><t v="{$t}"/></xsl:on-completion><xsl:next-iteration><xsl:with-param name="t" select="$t + count(.//node())"/></xsl:next-iteration></xsl:iterate>`)
		case 1:
			s.WriteString(`<xsl:iterate select="` + p + `"><xsl:if test="@k = 'b'"><xsl:break><b/></xsl:break></xsl:if><i><xsl:value-of select="@k"/></i></xsl:iterate>`)
		default:
			body := g.pick(`<r k="{@k}"/>`, `<xsl:copy-of select="."/>`, `<xsl:value-of select="@v"/>`,
				`<r n="{name()}" d="{count(ancestor::*)}"/>`, `<r p="{position()}"><xsl:copy-of select="@*"/></r>`,
				`<xsl:value-of select="count(.//node())"/>`, `<r a="{ancestor::*[1]/@id}" l="{local-name(..)}"/>`,
				`<xsl:sequence select="string(.)"/>`, `<xsl:copy-of select="sub"/>`, `<r b="{base-uri(.)}"/>`,
				`<xsl:for-each select="sub"><s x="{@x}"/></xsl:for-each>`, `<r i="{count(ancestor::`+g.rec+`)}"/>`)
			s.WriteString(`<xsl:for-each select="` + p + `">` + body + `</xsl:for-each>`)
		}
		goto done
	}
	{
		sel := esc(g.selectExpr())
		switch g.n(3) {
		case 0:
			sep := ""
			if g.p(30) {
				sep = ` separator="` + g.pick("", "|", ", ") + `"`
			}
			s.WriteString(`<xsl:value-of select="` + sel + `"` + sep + "/>")
		case 1:
			s.WriteString(`<xsl:copy-of select="` + sel + `"/>`)
		default:
			s.WriteString(`<xsl:sequence select="` + sel + `"/>`)
		}
	}
done:
	if wrap {
		s.WriteString(`</out>`)
	}
	s.WriteString("</xsl:source-document>")
	if try {
		s.WriteString(`<xsl:catch><caught code="{local-name-from-QName($err:code)}"/></xsl:catch></xsl:try>`)
	}
	s.WriteString("\n</xsl:template>\n</xsl:stylesheet>\n")
	c := fuzzCase{seed: seed, sheet: s.String(), doc: doc, malform: g.malform, kind: g.kind}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	if g.p(3) {
		c.maxBytes = int64(50 + g.n(800))
	}
	return c
}

type fuzzRes struct {
	out, err, panic string
	msgs            string // Result.Messages (streamg5fuzz_test.go)
	trace           streamtrace.Counts
}

// fuzzRun runs c from dir, with streaming on or off (streamtrace's switch,
// as GOXSLT_STREAM_OFF sets it).
func fuzzRun(c fuzzCase, dir string, streamed bool) (r fuzzRes) {
	before := streamtrace.Snapshot()
	defer func() {
		if p := recover(); p != nil {
			r.panic = fmt.Sprint(p)
		}
		streamtrace.SetOff(false)
		r.trace = streamtrace.Snapshot().Sub(before)
	}()
	tree, err := xdm.ParseString(c.sheet, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		r.err = "SHEET: " + err.Error()
		return
	}
	sheet, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		r.err = "COMPILE: " + err.Error()
		return
	}
	docs, _ := NewFileResolver(dir)
	docs.MaxNodes, docs.MaxBytes = c.maxNodes, c.maxBytes
	streamtrace.SetOff(!streamed)
	res, err := sheet.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
	if err != nil {
		r.err = err.Error()
		return
	}
	r.out = res.String()
	r.msgs = strings.Join(res.Messages, "\x00")
	return
}

var (
	fuzzCodeRe   = regexp.MustCompile(`\b[A-Z]{4}[0-9]{4}\b`)
	fuzzCaughtRe = regexp.MustCompile(`<caught code="([A-Za-z0-9]*)"`)
)

// fuzzCode is the outcome of r: ok, compile, an error code, or the code an
// xsl:catch caught.
func fuzzCode(r fuzzRes) string {
	if r.err == "" {
		if m := fuzzCaughtRe.FindStringSubmatch(r.out); m != nil {
			return "caught:" + m[1]
		}
		return "ok"
	}
	if strings.HasPrefix(r.err, "COMPILE") {
		return "compile"
	}
	if m := fuzzCodeRe.FindString(r.err); m != "" {
		return m
	}
	return "nocode"
}

// fuzzDiff says how co, the streamed run, differs from tr, the tree's, or
// "": exactly, error text and all.
func fuzzDiff(c fuzzCase, co, tr fuzzRes) string {
	switch {
	case co.panic != "" || tr.panic != "":
		return "PANIC"
	case co.trace.Guards+tr.trace.Guards != 0:
		return "GUARD"
	case tr.trace.Streamed() != 0:
		return "STREAMED WHILE OFF"
	case co.out != tr.out:
		return "OUTPUT"
	case co.err != tr.err:
		return "ERROR " + fuzzCode(co) + " vs " + fuzzCode(tr)
	case co.msgs != tr.msgs:
		return "MESSAGES"
	case co.trace.Reruns > 0 && tr.err == "" && !strings.Contains(c.sheet, "<xsl:try>"):
		// The tree's answer hides what the stream did wrong. (Under an
		// xsl:try the container may have failed on the tree too, the
		// catch, whose output method may not show it, taking over.)
		return "RE-RUN where the tree succeeds"
	}
	return ""
}

func TestStreamFuzz(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	n, seed0 := 1000, int64(1)
	if v := os.Getenv("GOXSLT_STREAM_FUZZ"); v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil {
			t.Fatalf("GOXSLT_STREAM_FUZZ: %v", err)
		}
	}
	if os.Getenv("GOXSLT_STREAM_FUZZ_RERUN") != "" {
		traceRerun = func(err error) { t.Logf("re-run after: %v", err) }
		defer func() { traceRerun = nil }()
	}
	if v := os.Getenv("GOXSLT_STREAM_FUZZ_SEED"); v != "" {
		var err error
		if seed0, err = strconv.ParseInt(v, 10, 64); err != nil {
			t.Fatalf("GOXSLT_STREAM_FUZZ_SEED: %v", err)
		}
	}
	dir := t.TempDir()
	tally := map[string]int{}
	var trace streamtrace.Counts
	var coreKinds [streamtrace.NumKinds]int64
	for i := 0; i < n; i++ {
		c := fuzzMake(seed0 + int64(i))
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(c.doc), 0o600); err != nil {
			t.Fatal(err)
		}
		groups, tries := 0, 0
		traceGroup = func() { groups++ }
		traceTry = func() { tries++ }
		co := fuzzRun(c, dir, true)
		traceGroup, traceTry = nil, nil
		tr := fuzzRun(c, dir, false)
		tally["cases"]++
		if co.trace.Streamed() > 0 {
			tally["streamed"]++
			if co.trace.Reruns > 0 {
				tally["streamed: re-run on the tree"]++
			}
			if co.trace.Fallbacks > 0 {
				tally["streamed: a select fell back to the tree"]++
			}
			if strings.HasSuffix(c.kind, "-loop") {
				tally["streamed: loop shapes"]++
			}
		}
		if strings.HasPrefix(c.kind, "core") {
			tally["core shapes"]++
		}
		if c.kind == "groups" {
			tally["group shapes"]++
			if co.trace.Streamed() > 0 {
				tally["streamed: group shapes"]++
			}
		}
		if groups > 0 {
			tally["streamed: a grouping ran"]++
		}
		if c.kind == "hold" {
			tally["hold shapes"]++ // streamholdfuzz_test.go
			if co.trace.Streamed() > 0 {
				tally["streamed: hold shapes"]++
			}
		}
		if c.kind == "shapes" { // streamshapesfuzz_test.go
			tally["G3 shapes"]++
			if co.trace.Streamed() > 0 {
				tally["streamed: G3 shapes"]++
			}
		}
		if c.kind == "g5" {
			tally["g5 shapes"]++
			if co.trace.Streamed() > 0 {
				tally["streamed: g5 shapes"]++
			}
		}
		if c.kind == "try" {
			tally["try shapes"]++
			if co.trace.Streamed() > 0 {
				tally["streamed: try shapes"]++
			}
		}
		if tries > 0 {
			tally["streamed: a try caught on the cursor"]++
		}
		if c.kind == "forks" {
			tally["fork shapes"]++
			if co.trace.Streamed() > 0 {
				tally["streamed: fork shapes"]++
			}
			if co.trace.Kinds[streamtrace.KindFork] > 0 {
				tally["streamed: a tee"]++
			}
		}
		if strings.Contains(c.sheet, "<xsl:accumulator ") {
			tally["accumulator shapes"]++
			if co.trace.Streamed() > 0 {
				tally["streamed: accumulator shapes"]++
			}
		}
		if c.kind == "modes" {
			tally["mode shapes"]++
			if co.trace.Streamed() > 0 {
				tally["streamed: mode shapes"]++
			}
		}
		for j, r := range co.trace.Runs {
			trace.Runs[j] += r
		}
		for j, n := range co.trace.Kinds {
			coreKinds[j] += n
		}
		if d := fuzzDiff(c, co, tr); d != "" {
			tally["DIFFERENCES"]++
			if tally["DIFFERENCES"] <= 10 {
				t.Errorf("seed %d: %s\nreplay: GOXSLT_STREAM_FUZZ=1 GOXSLT_STREAM_FUZZ_SEED=%d go test ./xslt -run 'TestStreamFuzz$' -v\n"+
					"stylesheet:\n%s\ndocument:\n%s\nstreamed %+v\ntree     %+v", c.seed, d, c.seed, c.sheet, c.doc, co, tr)
			}
		}
	}
	keys := make([]string, 0, len(tally))
	for k := range tally {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("STREAMFUZZ %-50s %d", k, tally[k])
	}
	for j, r := range trace.Runs {
		t.Logf("STREAMFUZZ runs %-45s %d", streamtrace.Strategy(j), r)
	}
	for j, r := range coreKinds {
		t.Logf("STREAMFUZZ sNodes %-44s %d", streamtrace.Kind(j), r)
	}
	t.Logf("STREAMFUZZ seeds %d..%d", seed0, seed0+int64(n)-1)
}

// TestStreamFuzzMain is the fuzzer's leg for a streamed principal input
// (C7): each case of the M2 grammar (fuzzMakeModes: modes, rules, built-in
// rules over a random record document, perhaps malformed or over a parse
// limit) is applied to its document in an initial mode, with or without
// parameters and a result budget, through TransformReader and through
// xdm.Parse + Transform, which must agree exactly: output, Result.Nodes,
// error text.
//
//	GOXSLT_STREAM_FUZZ=100000 GOXSLT_STREAM_FUZZ_SEED=1 go test ./xslt -run 'TestStreamFuzzMain$' -v -timeout 0
func TestStreamFuzzMain(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	n, seed0 := 1000, int64(1)
	if v := os.Getenv("GOXSLT_STREAM_FUZZ"); v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil {
			t.Fatalf("GOXSLT_STREAM_FUZZ: %v", err)
		}
	}
	if v := os.Getenv("GOXSLT_STREAM_FUZZ_SEED"); v != "" {
		var err error
		if seed0, err = strconv.ParseInt(v, 10, 64); err != nil {
			t.Fatalf("GOXSLT_STREAM_FUZZ_SEED: %v", err)
		}
	}
	tally := map[string]int{}
	for i := 0; i < n; i++ {
		seed := seed0 + int64(i)
		c := fuzzMakeModes(seed)
		r := rand.New(rand.NewSource(seed*15485863 + 3))
		opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{MaxNodes: c.maxNodes, MaxBytes: c.maxBytes,
			BaseURI: "file:///f/doc.xml", DocumentURI: "file:///f/doc.xml"}}
		if strings.Contains(c.sheet, `name="m"`) && r.Intn(2) == 0 {
			opts.InitialMode = "m"
		} else if r.Intn(4) == 0 {
			opts.InitialMode = "#default"
		}
		if r.Intn(4) == 0 {
			opts.InitialModeParams = map[string]xdm.Sequence{"q": {xdm.NewString("P")}}
		}
		if r.Intn(4) == 0 {
			opts.InitialModeTunnelParams = map[string]xdm.Sequence{"t": {xdm.NewString("T")}}
		}
		if r.Intn(30) == 0 {
			opts.MaxNodes = 5 + r.Intn(60)
		}
		groups, tries := 0, 0
		traceGroup = func() { groups++ }
		traceTry = func() { tries++ }
		co, ct := mainRun(c.sheet, c.doc, true, opts)
		traceGroup, traceTry = nil, nil
		tr, tt := mainRun(c.sheet, c.doc, false, opts)
		if groups > 0 {
			tally["streamed: a grouping ran"]++
		}
		if tries > 0 {
			tally["streamed: a try caught on the cursor"]++
		}
		if ct.Runs[streamtrace.Main] > 0 && strings.Contains(c.sheet, `<xsl:try rollback-output`) {
			tally["streamed: with a try rule"]++
		}
		tally["cases"]++
		if ct.Runs[streamtrace.Main] > 0 && strings.Contains(c.sheet, "<xsl:accumulator ") {
			tally["streamed: with accumulators"]++
		}
		if ct.Runs[streamtrace.Main] > 0 && strings.Contains(c.sheet, "for-each-group") {
			tally["streamed: with a grouping rule"]++
		}
		if ct.Runs[streamtrace.Main] > 0 {
			tally["streamed"]++
			if strings.HasPrefix(co, "error: ") {
				tally["streamed: an error"]++
			}
		}
		d := ""
		switch {
		case ct.Guards+tt.Guards != 0:
			d = "GUARD"
		case tt.Streamed() != 0:
			d = "STREAMED ON THE TREE"
		case co != tr:
			d = "RESULT"
		}
		if d != "" {
			tally["DIFFERENCES"]++
			if tally["DIFFERENCES"] <= 10 {
				t.Errorf("seed %d: %s\nreplay: GOXSLT_STREAM_FUZZ=1 GOXSLT_STREAM_FUZZ_SEED=%d go test ./xslt -run 'TestStreamFuzzMain$' -v\n"+
					"opts %+v\nstylesheet:\n%s\ndocument:\n%s\nstreamed %s\ntree     %s", seed, d, seed, opts, c.sheet, c.doc, co, tr)
			}
		}
	}
	keys := make([]string, 0, len(tally))
	for k := range tally {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("STREAMFUZZMAIN %-30s %d", k, tally[k])
	}
	t.Logf("STREAMFUZZMAIN seeds %d..%d", seed0, seed0+int64(n)-1)
}
