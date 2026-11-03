package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// identitySheet is a stylesheet whose unnamed mode is streamable, with the
// rules rules.
func identitySheet(rules string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:mode streamable="yes"/>` + rules + `</xsl:stylesheet>`
}

// The identity template written the guaranteed-streamable way: attributes,
// then children, by two instructions.
const identityRule = `<xsl:template match="node()|@*"><xsl:copy>` +
	`<xsl:apply-templates select="@*"/><xsl:apply-templates select="node()"/></xsl:copy></xsl:template>`

// TestStreamIdentity: the identity template in a streamable mode streams the
// principal input through TransformReader, byte-identical to Parse +
// Transform; the @*|node() form is not guaranteed-streamable (§19.8.8.4
// makes the union crawling, §19.8.4.5 a crawling select roaming), and is
// XTSE3430 as the catalog's sx-union-202 is.
func TestStreamIdentity(t *testing.T) {
	opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true,
		BaseURI: "file:///d/doc.xml", DocumentURI: "file:///d/doc.xml"}}
	sheets := []string{
		identityRule,
		`<xsl:template match="node()|@*"><xsl:copy><xsl:apply-templates select="node()"/></xsl:copy></xsl:template>`,
		identityRule + `<xsl:template match="sub"/>`,
		identityRule + `<xsl:template match="@k"><xsl:attribute name="key" select="."/></xsl:template>`,
		identityRule + `<xsl:template match="text()"><xsl:value-of select="upper-case(.)"/></xsl:template>`,
		`<xsl:strip-space elements="*"/>` + identityRule,
	}
	docs := map[string]string{}
	for name, d := range coreDocs {
		docs[name] = d
	}
	docs["attrs"] = `<?xml version="1.0"?>` + "\n" + `<!--c--><doc a="1" b="2" xmlns:q="urn:q"><r q:x="y">t<s/><?p q?></r>tail<!--z--><e></e></doc>`
	docs["doctype"] = `<!DOCTYPE doc [<!ATTLIST doc d CDATA "def">]><doc><r k="v"/></doc>`
	var why string
	traceMain = func(m, w string) {
		if m == "" {
			why = w
		}
	}
	defer func() { traceMain = nil }()
	for name, doc := range docs {
		for _, rules := range sheets {
			sheet := identitySheet(rules)
			so, st := mainRun(sheet, doc, true, opts)
			to, _ := mainRun(sheet, doc, false, opts)
			where := fmt.Sprintf("doc %s, rules %s", name, rules)
			if so != to {
				t.Errorf("%s:\nstreamed %s\ntree     %s", where, so, to)
			}
			if st.Guards != 0 {
				t.Errorf("%s: %d guard hits", where, st.Guards)
			}
			if st.Runs[streamtrace.Main] != 1 && !strings.HasPrefix(to, "error:") {
				t.Errorf("%s: not streamed: %s", where, why)
			}
		}
	}
	out, _ := mainRun(identitySheet(`<xsl:template match="node()|@*"><xsl:copy>`+
		`<xsl:apply-templates select="@*|node()"/></xsl:copy></xsl:template>`), docs["attrs"], true, opts)
	if !strings.Contains(out, "XTSE3430") {
		t.Errorf("@*|node(): %s, want XTSE3430", out)
	}
}

// popBodies are container bodies whose loop, grouping or select population
// is more than a bare path of the stream (streampop.go); stream says the
// core must take them.
var popBodies = []struct {
	body   string
	stream bool
}{
	// Positional step predicates, counted per context (positionPred).
	{`<out><xsl:for-each select="doc/rec[position() lt 3]"><e><xsl:value-of select="@k"/></e></xsl:for-each></out>`, true},
	{`<out><xsl:value-of select="count(//sub[position() mod 2 = 1])"/></out>`, true},
	{`<out><xsl:copy-of select="doc/rec[@k = 'a'][position() = 2]"/></out>`, true},
	{`<out><xsl:copy-of select="doc/rec[position() = 2][@k = 'a']"/></out>`, true},
	{`<out><xsl:for-each-group select="doc/rec[position() &lt; 3]" group-adjacent="@k"><g k="{current-grouping-key()}" n="{count(current-group())}"/></xsl:for-each-group></out>`, true},
	// Populations (streamPop): windows, motionless parts, attributes, copies, choices, values.
	{`<out><xsl:for-each select="head(doc/rec)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="tail(doc/rec)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="subsequence(doc/rec, 2)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="subsequence(doc/rec, $three - 1, 1.6)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="subsequence(doc/rec, 0.5)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="remove(doc/rec, 2)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="unordered(doc/rec)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="one-or-more(doc/rec)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="insert-before(doc/rec, 2, $extra)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="insert-before(doc/rec, 0, $extra)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="insert-before(doc/rec, 9, $extra)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="insert-before($extra, 2, doc/rec)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="($extra, doc/rec)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="(100, 101, doc/rec/@k)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="(doc/rec, $extra/@v)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="doc/rec/@k"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="data(doc/rec/@v), 101"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="copy-of(doc/rec)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="if ($cond) then () else doc/rec"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="if (not($cond)) then doc/rec/@v else 7"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="if (doc/rec) then 0 else 1"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="subsequence(data(doc/rec/@v), 2, 1)"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="head(tail(doc/rec))"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="//rec/@k"><e p="{position()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{`<out><xsl:iterate select="tail(doc/rec)"><xsl:param name="n" select="0"/><xsl:on-completion><n><xsl:value-of select="$n"/></n></xsl:on-completion><xsl:next-iteration><xsl:with-param name="n" select="$n + 1"/></xsl:next-iteration></xsl:iterate></out>`, true},
	{`<out><xsl:for-each select="doc/rec"><xsl:value-of select="@v + 1 || ' '"/></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="doc/rec"><xsl:value-of select="string(.) || '|'"/></xsl:for-each></out>`, true},
	// Refused: a window argument that reads the stream.
	{`<out><xsl:for-each select="subsequence(doc/rec, count(doc/rec))"><e/></xsl:for-each></out>`, false},
}

func TestStreamPopMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range popBodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d+popDecls, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}

// popDecls are the variables the population bodies read.
const popDecls = `<xsl:variable name="extra" as="element()*"><x v="10">X</x><y v="20">Y</y></xsl:variable>` +
	`<xsl:variable name="three" select="3"/><xsl:variable name="cond" select="false()"/>`
