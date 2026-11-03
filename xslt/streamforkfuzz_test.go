package xslt

// The fork, map and merge grammar of the stream fuzzers (C10). TestStreamFuzz
// gets, from a random stream of its own, containers holding an xsl:fork of
// 1-4 prongs or an xsl:map of 1-4 entries over the core grammar's record
// documents: aggregates, copies, loops, value templates, motionless prongs,
// prongs that fail at a record (FORG0001 at a bad @v) or only at the end,
// duplicate map keys (XTDE3365); at the document node (a tee when two or
// more read) or per record (a grounded copy); wrapped, under xsl:try, with
// node and byte limits. The modes grammar's rule bodies get forks and maps
// at the rule's element (fuzzForkRule), so TestStreamFuzzMain has them.
// TestStreamFuzzMerge merges 1-3 generated files (sorted or not, keys mixed
// in type now and then, records nested, some malformed) with 1-3 merge
// sources, a collected source before the streamed ones now and then.
// GOXSLT_STREAM_FUZZ_FORKS=0 leaves the fork and map cases out of
// TestStreamFuzz and TestStreamFuzzMain: every case is then the one the M3
// grammar made for its seed.
//
//	GOXSLT_STREAM_FUZZ=100000 GOXSLT_STREAM_FUZZ_SEED=1 go test ./xslt -run 'TestStreamFuzzMerge$' -v -timeout 0

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// fuzzForkShapes says the fuzzers may make fork and map cases.
var fuzzForkShapes = os.Getenv("GOXSLT_STREAM_FUZZ_FORKS") != "0"

// fuzzIsForks decides, from a stream of its own, whether the case of seed is
// one of fuzzMakeForks'.
func fuzzIsForks(seed int64) bool {
	return fuzzForkShapes && rand.New(rand.NewSource(seed*1000003+17)).Intn(100) < 20
}

// prong is one prong of a fork at the document node over the records P.
func (g *fuzzGen) prong(p string) string {
	switch g.n(14) {
	case 0:
		return `<xsl:sequence select="count(` + p + `)"/>`
	case 1:
		return `<xsl:sequence select="sum(` + p + `/@v)"/>` // FORG0001 at v="x"
	case 2:
		return `<xsl:sequence select="string-join(` + p + `/@k, ',')"/>`
	case 3:
		return `<xsl:sequence><a><xsl:value-of select="avg(` + p + `/number(@v))"/></a></xsl:sequence>`
	case 4:
		return `<xsl:sequence><b><xsl:copy-of select="` + p + `[@k = 'a']"/></b></xsl:sequence>`
	case 5:
		return `<xsl:sequence><xsl:for-each select="` + p + `"><r k="{@k}" n="{count(sub)}"/></xsl:for-each></xsl:sequence>`
	case 6:
		return `<xsl:sequence select="count(//sub)"/>`
	case 7:
		return `<xsl:sequence select="exists(//x)"/>`
	case 8:
		return `<xsl:sequence select="xs:integer(string-join(` + p + `/@k))"/>` // fails at the end
	case 9:
		return `<xsl:sequence select="max(` + p + `/xs:integer(@v))"/>` // fails at a bad record
	case 10:
		return `<xsl:sequence><c><xsl:value-of select="` + p + `/sub" separator="|"/></c></xsl:sequence>`
	case 11:
		return `<xsl:sequence select="'['"/>`
	case 12:
		return `<xsl:sequence><m/></xsl:sequence>`
	default:
		return `<xsl:sequence select="copy-of(` + p + `[1])"/>`
	}
}

// recordProng is a prong of a fork per record.
func (g *fuzzGen) recordProng() string {
	return g.pick(`<xsl:sequence><xsl:attribute name="k" select="@k"/></xsl:sequence>`,
		`<xsl:sequence><xsl:attribute name="n" select="count(sub)"/></xsl:sequence>`,
		`<xsl:sequence select="string(.)"/>`, `<xsl:sequence select="sum(sub/@x)"/>`,
		`<xsl:sequence><xsl:copy-of select="sub"/></xsl:sequence>`, `<xsl:sequence select="xs:integer(@v)"/>`,
		`<xsl:sequence select="'.'"/>`, `<xsl:sequence><xsl:value-of select="name()"/></xsl:sequence>`)
}

// entry is a map entry, its key one of a few (duplicates: XTDE3365).
func (g *fuzzGen) entry(sel string) string {
	return `<xsl:map-entry key="'k` + g.pick("0", "1", "2", "3", "4", "5", "0") + `'" select="` + sel + `"/>`
}

// fork is a container body holding a fork or map over the records P.
func (g *fuzzGen) fork(p string) string {
	n := 1 + g.n(4)
	var b strings.Builder
	switch g.n(4) {
	case 0, 1:
		b.WriteString("<xsl:fork>")
		for range n {
			b.WriteString(g.prong(p))
		}
		b.WriteString("</xsl:fork>")
	case 2:
		b.WriteString(`<xsl:variable name="m" as="map(*)"><xsl:map>`)
		for range n {
			b.WriteString(g.entry(g.pick("count("+p+")", "sum("+p+"/@v)", "string-join("+p+"/@k)",
				"count(//sub)", "copy-of("+p+"[@k = 'b'])", "1", "max("+p+"/xs:integer(@v))")))
		}
		b.WriteString(`</xsl:map></xsl:variable><m a="{$m?k0}" b="{$m?k1}" c="{$m?k2}" d="{$m?k3}" e="{$m('k4')}" f="{$m?k5}"/>`)
	default:
		inner := "<xsl:fork>"
		for range n {
			inner += g.recordProng()
		}
		inner += "</xsl:fork>"
		if g.p(30) {
			inner = `<xsl:variable name="m" as="map(*)"><xsl:map>` + g.entry("count(sub)") + g.entry("string(@k)") +
				`</xsl:map></xsl:variable><xsl:attribute name="m" select="$m?k0, $m?k1"/>`
		}
		b.WriteString(`<xsl:for-each select="` + p + `"><r>` + inner + `</r></xsl:for-each>`)
	}
	return b.String()
}

// fuzzMakeForks is a fork or map case over a record document.
func fuzzMakeForks(seed int64) fuzzCase {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*97 + 3))}
	g.root = g.pick("doc", "root", "account")
	g.rec = g.pick("rec", "rec", "item", "p:rec")
	g.defNS = g.p(10)
	if g.p(40) {
		g.wrappers = []string{g.pick("wrap", "data")}
	}
	doc := g.document()
	steps := strings.Join(append(append([]string{g.root}, g.wrappers...), g.rec), "/")
	content := g.fork(g.pick(steps, steps, "/"+steps, "//"+g.rec, "outermost(//"+g.rec+")", steps+"[@v != '0']"))
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
	try := g.p(10)
	if try {
		s.WriteString("<xsl:try>")
	}
	s.WriteString(`<xsl:source-document streamable="yes" href="doc.xml">` + content + `</xsl:source-document>`)
	if try {
		s.WriteString(`<xsl:catch><caught code="{local-name-from-QName($err:code)}"/></xsl:catch></xsl:try>`)
	}
	s.WriteString("\n</xsl:template>\n</xsl:stylesheet>\n")
	c := fuzzCase{seed: seed, sheet: s.String(), doc: doc, malform: g.malform, kind: "forks"}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	if g.p(3) {
		c.maxBytes = int64(50 + g.n(800))
	}
	return c
}

// fuzzForkRule is, for the modes grammar, a rule body holding a fork or a
// map at the rule's element, from r, a stream of its own; "" when this rule
// keeps its body.
func fuzzForkRule(r *rand.Rand, rec string) string {
	if !fuzzForkShapes || r.Intn(100) >= 15 {
		return ""
	}
	g := &fuzzGen{r: r, rec: rec}
	if g.p(30) {
		return `<xsl:variable name="m" as="map(*)"><xsl:map>` + g.entry("count(*)") + g.entry("string(@k)") +
			`</xsl:map></xsl:variable><f n="{$m?k0}"/>`
	}
	var b strings.Builder
	b.WriteString(`<f n="{name()}"><xsl:fork>`)
	for i, n := 0, 1+g.n(3); i < n; i++ {
		b.WriteString(g.pick(g.recordProng(), `<xsl:sequence><xsl:apply-templates/></xsl:sequence>`,
			`<xsl:sequence><xsl:apply-templates select="sub" mode="#current"/></xsl:sequence>`))
	}
	b.WriteString(`</xsl:fork></f>`)
	return b.String()
}

// mergeFuzzDoc is a log of records keyed by @t, sorted unless the draw says
// otherwise, perhaps malformed.
func (g *fuzzGen) mergeFuzzDoc() string {
	var b strings.Builder
	b.WriteString("<log>")
	t := g.n(3)
	for i, n := 0, g.n(8); i < n; i++ {
		switch {
		case g.p(8):
			t -= 1 + g.n(3) // out of order
		case g.p(40):
		default:
			t += g.n(4)
		}
		tv := strconv.Itoa(t)
		if g.p(3) {
			tv = "x" + tv // not a number
		}
		w := g.p(20)
		if w {
			b.WriteString("<g>")
		}
		fmt.Fprintf(&b, `<e t="%s" v="%s">`, tv, g.pick("a", "b", "c", "1", "2"))
		if g.p(50) {
			b.WriteString(`<n>` + g.pick("x", "y", "") + `</n>`)
		}
		b.WriteString("</e>")
		if w {
			b.WriteString("</g>")
		}
	}
	b.WriteString("</log>")
	s := b.String()
	if g.p(4) {
		s = s[:len(s)-3] // malformed
	}
	return s
}

// fuzzMakeMerge is a merge case: its sheet, and its documents by name.
func fuzzMakeMerge(seed int64) (fuzzCase, map[string]string) {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*7919 + 1))}
	docs := map[string]string{}
	names := []string{"a.xml", "b.xml", "c.xml"}[:1+g.n(3)]
	for _, n := range names {
		docs[n] = g.mergeFuzzDoc()
	}
	key := func() string {
		return g.pick(`xs:integer(@t)`, `xs:integer(@t)`, `number(@t)`, `@t`, `string(@t)`)
	}
	var keyFor string
	order := ""
	if g.p(15) {
		order = ` order="descending"`
	}
	var src strings.Builder
	if g.p(20) {
		keyFor = key()
		src.WriteString(`<xsl:merge-source name="C" select="doc('a.xml')/log/e"><xsl:merge-key select="` + keyFor + `"` + order + `/></xsl:merge-source>`)
	}
	for i, n := 0, 1+g.n(3); i < n; i++ {
		var uris []string
		for j, m := 0, 1+g.n(2); j < m; j++ {
			uris = append(uris, "'"+g.pick(names...)+"'")
		}
		k := keyFor
		if k == "" || g.p(10) {
			k = key()
			keyFor = k
		}
		fmt.Fprintf(&src, `<xsl:merge-source name="S%d" for-each-source="(%s)" select="%s"%s><xsl:merge-key select="%s"%s/></xsl:merge-source>`,
			i, strings.Join(uris, ", "), g.pick("log/e", "log/e", "*/e", "log/e[@v != 'c']", "log/g/e", "log/e[2]"),
			g.pick("", "", "", ` sort-before-merge="yes"`), k, order)
	}
	action := g.pick(`<g k="{current-merge-key()}" p="{position()}" n="{count(current-merge-group())}"/>`,
		`<g k="{current-merge-key()}"><xsl:copy-of select="current-merge-group()"/></g>`,
		`<g v="{current-merge-group()/@v}" s0="{current-merge-group('S0')/@v}"/>`,
		`<g u="{name(current-merge-group()[1]/..)}"><xsl:value-of select="current-merge-group()/n" separator="|"/></g>`,
		`<g d="{xs:integer(current-merge-group()[1]/@v)}"/>`, // FORG0001 at a letter
		`<xsl:for-each select="current-merge-group()"><i t="{@t}" p="{position()}"/></xsl:for-each>`,
		`<g n="{last()}"/>`) // refused
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">` +
		g.pick("", `<xsl:strip-space elements="*"/>`) +
		`<xsl:template name="main"><out><xsl:merge>` + src.String() + `<xsl:merge-action>` + action +
		`</xsl:merge-action></xsl:merge></out></xsl:template></xsl:stylesheet>`
	c := fuzzCase{seed: seed, sheet: sheet, kind: "merge"}
	if g.p(3) {
		c.maxNodes = 5 + g.n(60)
	}
	return c, docs
}

// TestStreamFuzzMerge: each merge case, its documents written, streamed and
// on the tree, must agree exactly (output, error text), with no guard hit
// and no re-run where the tree succeeds.
func TestStreamFuzzMerge(t *testing.T) {
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
	dir := t.TempDir()
	tally := map[string]int{}
	for i := 0; i < n; i++ {
		c, docs := fuzzMakeMerge(seed0 + int64(i))
		for name, d := range docs {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(d), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		co := fuzzRun(c, dir, true)
		tr := fuzzRun(c, dir, false)
		for name := range docs {
			os.Remove(filepath.Join(dir, name))
		}
		tally["cases"]++
		if co.trace.Runs[streamtrace.Merge] > 0 {
			tally["streamed"]++
			if co.trace.Reruns > 0 {
				tally["streamed: re-run on the tree"]++
			}
			if tr.err != "" {
				tally["streamed: an error"]++
			}
		}
		if d := fuzzDiff(c, co, tr); d != "" {
			tally["DIFFERENCES"]++
			if tally["DIFFERENCES"] <= 10 {
				t.Errorf("seed %d: %s\nreplay: GOXSLT_STREAM_FUZZ=1 GOXSLT_STREAM_FUZZ_SEED=%d go test ./xslt -run 'TestStreamFuzzMerge$' -v\n"+
					"stylesheet:\n%s\ndocuments:\n%v\nstreamed %+v\ntree     %+v", c.seed, d, c.seed, c.sheet, docs, co, tr)
			}
		}
	}
	keys := make([]string, 0, len(tally))
	for k := range tally {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("STREAMFUZZMERGE %-35s %d", k, tally[k])
	}
	t.Logf("STREAMFUZZMERGE seeds %d..%d", seed0, seed0+int64(n)-1)
}
