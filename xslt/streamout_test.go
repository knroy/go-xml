package xslt

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// outRun is one transform of src by sheet, serialized: through Result.Serialize
// (out false), or to TransformOptions.Output (out true); main reads src
// through TransformReader.
func outRun(sheet *Stylesheet, src string, main, out bool, opts TransformOptions) (string, error) {
	var buf bytes.Buffer
	if out {
		opts.Output = &buf
	}
	var res *Result
	var err error
	if main {
		res, err = sheet.TransformReader(context.Background(), strings.NewReader(src), opts)
	} else {
		var doc *xdm.Tree
		if doc, err = xdm.ParseString(src, opts.SourceParseOptions); err == nil {
			res, err = sheet.Transform(context.Background(), doc.Root, opts)
		}
	}
	if err == nil && !out {
		if res.Tree() == nil && res.BuildsTree() {
			return "", errNoTreeTest
		}
		err = res.Serialize(&buf)
	}
	if err == nil && out && (res.Nodes != nil || res.Tree() != nil) {
		return "", errNoTreeTest
	}
	return buf.String(), err
}

var errNoTreeTest = errorString("a streamed result kept a tree, or a tree result has none")

type errorString string

func (e errorString) Error() string { return string(e) }

// outCheck compares Output's bytes with Result.Serialize's for sheet over src,
// for Transform and TransformReader, and returns how many nodes the streamed
// runs opened before their end.
func outCheck(t *testing.T, sheet *Stylesheet, src, where string, opts TransformOptions) int {
	return outCheckErr(t, sheet, src, where, opts, "")
}

// outCheckErr is outCheck for a case where Output must fail with code
// instead (when code is set).
func outCheckErr(t *testing.T, sheet *Stylesheet, src, where string, opts TransformOptions, code string) int {
	t.Helper()
	opened := 0
	defer func(f func()) { traceOutOpen = f }(traceOutOpen)
	for _, main := range []bool{false, true} {
		traceOutOpen = nil
		want, werr := outRun(sheet, src, main, false, opts)
		traceOutOpen = func() { opened++ }
		got, gerr := outRun(sheet, src, main, true, opts)
		traceOutOpen = nil
		switch {
		case code != "":
			if gerr == nil || !strings.Contains(gerr.Error(), code) {
				t.Errorf("%s (main %v): error %v, want %s", where, main, gerr, code)
			}
		case (werr == nil) != (gerr == nil) || werr != nil && werr.Error() != gerr.Error():
			t.Errorf("%s (main %v): error %v, want %v", where, main, gerr, werr)
		case werr == nil && got != want:
			t.Errorf("%s (main %v):\n got %q\nwant %q", where, main, got, want)
		}
	}
	return opened
}

const outDoc = `<?xml version="1.0"?>
<doc xmlns:p="urn:p">
  <rec k="1"><name>one</name><v>10</v><!--c1--></rec>
  <rec k="2"><name>two &amp; &lt;2&gt;</name><v>20</v><?pi data?></rec>
  <p:rec k="3"><name xmlns="urn:d">three</name><v>30</v>tail</p:rec>
  <rec k="4"/>
  text <b>bold</b> more
</doc>`

var outSheets = []struct{ name, decls, body, outErr string }{
	{"identity", `<xsl:mode on-no-match="shallow-copy"/>`, ``, ""},
	{"identity-streamable", `<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, ``, ""},
	{"filter", `<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="rec[@k = '2']"/><xsl:template match="v"/>`, ``, ""},
	{"lre", ``, `<xsl:template match="/"><out a="1"><xsl:for-each select="//rec"><r k="{@k}"><xsl:value-of select="name"/></r></xsl:for-each><end/></out></xsl:template>`, ""},
	{"lre-ns", `<xsl:mode on-no-match="shallow-copy"/>`, `<xsl:template match="/"><out xmlns="urn:o"><xsl:apply-templates/><x xmlns=""/></out></xsl:template>`, ""},
	{"text", `<xsl:output method="text"/>`, `<xsl:template match="/"><xsl:for-each select="//rec"><xsl:value-of select="@k"/>:<xsl:value-of select="name"/>;</xsl:for-each></xsl:template>`, ""},
	{"html", `<xsl:output method="html"/>`, `<xsl:template match="/"><html><head><title>t</title></head><body><xsl:for-each select="//rec"><p><xsl:value-of select="name"/><br/></p></xsl:for-each><script>if (a &lt; b) x();</script></body></html></xsl:template>`, ""},
	{"html-default", ``, `<xsl:template match="/"><html><body><xsl:copy-of select="//rec"/></body></html></xsl:template>`, ""},
	{"indent", `<xsl:output indent="yes"/><xsl:mode on-no-match="shallow-copy"/>`, ``, ""},
	{"indent-text", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><out>x<xsl:copy-of select="//rec"/></out></xsl:template>`, ""},
	{"charmap", `<xsl:output use-character-maps="m" omit-xml-declaration="yes"/><xsl:character-map name="m"><xsl:output-character character="&amp;" string="AMP"/><xsl:output-character character="o" string="0"/></xsl:character-map><xsl:mode on-no-match="shallow-copy"/>`, ``, ""},
	{"atomics", ``, `<xsl:template match="/"><xsl:sequence select="1, 2, 'a'"/><e/><xsl:sequence select="3"/><xsl:text/><xsl:sequence select="4"/></xsl:template>`, ""},
	{"copy-doc", ``, `<xsl:template match="/"><xsl:copy><xsl:sequence select="1, 2"/><xsl:apply-templates/></xsl:copy></xsl:template><xsl:template match="rec"><xsl:copy><xsl:copy-of select="@*"/>[<xsl:value-of select="."/>]</xsl:copy></xsl:template>`, ""},
	{"cdata", `<xsl:output cdata-section-elements="name"/><xsl:mode on-no-match="shallow-copy"/>`, ``, ""},
	{"no-inherit", ``, `<xsl:template match="/"><xsl:element name="o" namespace="urn:x" inherit-namespaces="no"><a/><b><c/></b></xsl:element></xsl:template>`, ""},
	{"comment-error", ``, `<xsl:template match="/"><out><a/><xsl:comment>bad--</xsl:comment><b/></out></xsl:template>`, ""},
	{"attr-after-child", ``, `<xsl:template match="/"><out><a/><b/><xsl:attribute name="x">1</xsl:attribute></out></xsl:template>`, ""},
	{"terminate", ``, `<xsl:template match="/"><out><xsl:for-each select="//rec"><r/><xsl:if test="@k = 3"><xsl:message terminate="yes">stop</xsl:message></xsl:if></xsl:for-each></out></xsl:template>`, ""},
	{"map-top", ``, `<xsl:template match="/"><out/><xsl:sequence select="map{1:2}"/></xsl:template>`, ""},
	{"nested-transform", ``, `<xsl:template match="/"><out><xsl:sequence select="transform(map{'stylesheet-text': '&lt;xsl:stylesheet version=&quot;3.0&quot; xmlns:xsl=&quot;http://www.w3.org/1999/XSL/Transform&quot;&gt;&lt;xsl:template match=&quot;/&quot;&gt;&lt;in&gt;&lt;xsl:value-of select=&quot;count(//rec)&quot;/&gt;&lt;/in&gt;&lt;/xsl:template&gt;&lt;/xsl:stylesheet&gt;', 'source-node': .})?output"/></out></xsl:template>`, ""},
	// The tree admits a namespace node after content, which §5.7.1 makes
	// XTDE0410; the stream has written the start tag, and raises it.
	{"ns-after-child", ``, `<xsl:template match="/"><out><a/><b/><xsl:namespace name="q" select="'urn:q'"/><c/></out></xsl:template>`, "XTDE0410"},
	{"ns-node-after-child", ``, `<xsl:template match="/"><out><a/><b/><xsl:copy-of select="//*:name[1]/namespace::*"/><c/></out></xsl:template>`, "XTDE0410"},
	{"try", `<xsl:mode on-no-match="shallow-copy"/>`, `<xsl:template match="rec[@k = '2']"><xsl:try><bad><xsl:sequence select="error()"/></bad><xsl:catch><caught/></xsl:catch></xsl:try></xsl:template>`, ""},
}

func outSheet(t *testing.T, decls, body string) *Stylesheet {
	return compileString(t, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:p="urn:p" exclude-result-prefixes="p">`+
		decls+body+`</xsl:stylesheet>`)
}

// TestStreamOutMatchesTree: what Output receives is what Result.Serialize
// writes, error for error, and most of these shapes are written as they are
// made.
func TestStreamOutMatchesTree(t *testing.T) {
	opened := 0
	for _, c := range outSheets {
		n := outCheckErr(t, outSheet(t, c.decls, c.body), outDoc, c.name, TransformOptions{}, c.outErr)
		t.Logf("%s: %d opened", c.name, n)
		opened += n
	}
	if opened == 0 {
		t.Error("nothing was opened before its end")
	}
}

// outRecords is a document of n records, each three result nodes when copied,
// with pad repeated in each one's text.
func outRecords(n int, pad string) string {
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range n {
		fmt.Fprintf(&b, `<r k="%d">t%d%s</r>`, i, i%10, pad)
	}
	b.WriteString("</doc>")
	return b.String()
}

// TestStreamOutCap: written nodes are handed back to the result-node count,
// so MaxNodes bounds what a streamed result holds; a held result, and any
// result without Output, keeps the cumulative count and its error.
func TestStreamOutCap(t *testing.T) {
	src := outRecords(3000, "") // 9,000 nodes and more
	ident := outSheet(t, `<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, ``)
	want, err := outRun(ident, src, false, false, TransformOptions{MaxNodes: -1})
	if err != nil {
		t.Fatal(err)
	}
	capped := TransformOptions{MaxNodes: 2000}
	for _, main := range []bool{false, true} {
		got, err := outRun(ident, src, main, true, capped)
		if err != nil || got != want {
			t.Errorf("main %v: streamed under MaxNodes 2000: err %v, same bytes %v", main, err, got == want)
		}
		_, terr := outRun(ident, src, main, false, capped)
		if terr == nil || !strings.Contains(terr.Error(), "XPDY0130") {
			t.Errorf("main %v: tree result under MaxNodes 2000: err %v, want XPDY0130", main, terr)
		}
	}
	// indent="yes" and no text in doc: its content is held to its end.
	held := outSheet(t, `<xsl:output indent="yes"/><xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, ``)
	_, herr := outRun(held, src, false, true, capped)
	_, terr := outRun(held, src, false, false, capped)
	if herr == nil || terr == nil || herr.Error() != terr.Error() {
		t.Errorf("held result under MaxNodes 2000: err %v, tree's %v", herr, terr)
	}
}

// TestStreamOutMemory: the identity transform and a filter through
// TransformReader hold their output without Output; with it they hold what
// they have not written, so the peak live heap is a part of the held run's,
// and never more. (What is left grows with the input, as it does for the
// filter, whose output is small: the streamed input's own share.)
func TestStreamOutMemory(t *testing.T) {
	// Under -race the instrumentation's heap moves the ratios, so the bounds
	// are not checked there: the race lane would pay minutes for no assertion.
	if testing.Short() || raceEnabled {
		t.Skip("measures the live heap")
	}
	for _, c := range []struct {
		name, decls string
		frac        float64 // the most of the held run's peak Output may take
	}{
		{"identity", `<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`, 0.5},
		// The result in a fragment (a literal result element at its top),
		// whose text is compacted too.
		{"wrapped", `<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><out><xsl:apply-templates/></out></xsl:template>`, 0.5},
		{"filter", `<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="r[@k mod 10 ne 0]"/>`, 1},
	} {
		sheet := outSheet(t, c.decls, ``)
		peak := func(n int, out bool) uint64 {
			// 100 bytes of text a record, which the result's tree stores
			// and must let go of (xdmtrim.CompactText).
			src := outRecords(n, strings.Repeat("x", 100))
			var err error
			p := splitPeak(func() {
				opts := TransformOptions{MaxNodes: -1}
				if out {
					opts.Output = io.Discard
				}
				var res *Result
				res, err = sheet.TransformReader(context.Background(), strings.NewReader(src), opts)
				if err == nil && !out {
					err = res.Serialize(io.Discard)
				}
			})
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			return p
		}
		small, big, held := peak(50000, true), peak(200000, true), peak(200000, false)
		t.Logf("%s: Output %.1f MB (50,000 records), %.1f MB (200,000); held %.1f MB",
			c.name, float64(small)/1e6, float64(big)/1e6, float64(held)/1e6)
		if raceEnabled {
			continue
		}
		if float64(big) > c.frac*float64(held) {
			t.Errorf("%s: Output's peak %.1f MB, held %.1f MB (bound %.0f%%)",
				c.name, float64(big)/1e6, float64(held)/1e6, 100*c.frac)
		}
	}
}
