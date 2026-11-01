package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

const burstDoc = `<?xml version="1.0"?>
<account xmlns:p="urn:p" id="A1" xml:base="http://ex/acct/">
  <!-- header -->
  <owner>Bob</owner>
  <transaction value="13.24" date="2006-02-13"><p:memo>a</p:memo></transaction>
  <transaction value="-15.00" date="2006-02-15"> <p:memo>b</p:memo> </transaction>
  <transaction value="6.00" date="2006-02-16"/>
  <other><transaction value="99"/></other>
  <transaction value="-5.00" date="2006-02-20">text<x/></transaction>
</account>`

// TestBurstsMatchTree runs each stylesheet with bursts and without, and
// requires the same output -- and that bursts ran exactly when expected.
func TestBurstsMatchTree(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		bursts bool
		extra  string
	}{
		{"for-each copy-of", `<xsl:for-each select="account/transaction"><xsl:copy-of select="."/></xsl:for-each>`, true, ""},
		{"absolute path, predicate, position", `<out><xsl:for-each select="/account/transaction[@value &lt; 0]"><t nr="{position()}"><xsl:copy-of select="@*"/></t></xsl:for-each></out>`, true, ""},
		{"ancestor names and attributes", `<xsl:for-each select="account/transaction"><r p="{name(..)}" a="{../@id}" n="{count(ancestor::*)}" b="{base-uri(.)}"/></xsl:for-each>`, true, ""},
		{"iterate total", `<xsl:iterate select="account/transaction"><xsl:param name="t" select="0"/><xsl:on-completion><total><xsl:value-of select="$t"/></total></xsl:on-completion><xsl:next-iteration><xsl:with-param name="t" select="$t + @value"/></xsl:next-iteration></xsl:iterate>`, true, ""},
		{"iterate break", `<xsl:iterate select="account/transaction"><xsl:choose><xsl:when test="@value &lt; 0"><xsl:break><stop at="{@date}"/></xsl:break></xsl:when><xsl:otherwise><seen><xsl:value-of select="@value"/></seen></xsl:otherwise></xsl:choose></xsl:iterate>`, true, ""},
		{"strip-space", `<xsl:for-each select="account/transaction"><xsl:copy-of select="."/></xsl:for-each>`, true, `<xsl:strip-space elements="*"/>`},
		{"last()", `<xsl:for-each select="account/transaction"><xsl:value-of select="last()"/></xsl:for-each>`, false, ""},
		{"sibling axis", `<xsl:for-each select="account/transaction"><xsl:value-of select="count(preceding-sibling::*)"/></xsl:for-each>`, false, ""},
		{"positional predicate", `<xsl:for-each select="account/transaction[2]"><xsl:copy-of select="."/></xsl:for-each>`, false, ""},
		{"ancestor content", `<xsl:for-each select="account/transaction"><xsl:value-of select="string-length(string(..))"/></xsl:for-each>`, false, ""},
		{"identity", `<xsl:for-each select="account/transaction"><xsl:value-of select=". is ."/></xsl:for-each>`, false, ""},
		{"sort", `<xsl:for-each select="account/transaction"><xsl:sort select="@value"/><xsl:copy-of select="."/></xsl:for-each>`, false, ""},
		{"node carried", `<xsl:iterate select="account/transaction"><xsl:param name="prev" select="()"/><xsl:on-completion><xsl:copy-of select="$prev"/></xsl:on-completion><xsl:next-iteration><xsl:with-param name="prev" select="copy-of(.)"/></xsl:next-iteration></xsl:iterate>`, false, ""},
		{"sibling instruction", `<xsl:value-of select="count(account/transaction)"/>`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:p="urn:p" exclude-result-prefixes="p">` +
				tc.extra + `<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml">` +
				tc.body + `</xsl:source-document></xsl:template></xsl:stylesheet>`
			tree, err := burstRun(t, src, "doc.xml", burstDoc, true)
			got, ran := tree, burstRuns
			want, err2 := burstRun(t, src, "doc.xml", burstDoc, false)
			if (err == nil) != (err2 == nil) || got != want {
				t.Fatalf("bursts differ from the tree\nbursts: %q %v\ntree:   %q %v", got, err, want, err2)
			}
			if ran != tc.bursts {
				t.Fatalf("ran in bursts: %v, want %v (output %q)", ran, tc.bursts, got)
			}
		})
	}
}

var burstRuns bool

// burstRun compiles src, writes doc to a temporary directory as name, and
// runs the template "main" with or without bursts.
func burstRun(t testing.TB, src, name, doc string, bursts bool) (string, error) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return burstRunIn(t, src, dir, bursts)
}

func burstRunIn(t testing.TB, src, dir string, bursts bool) (string, error) {
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		t.Fatal(err)
	}
	burstRuns = false
	sheet, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		// Not streamable is XTSE3430 whichever way it would have run.
		return "", err
	}
	docs, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	noBursts = !bursts
	traceBursts = func(string) { burstRuns = true }
	defer func() { noBursts, traceBursts = false, nil }()
	res, err := sheet.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "main", Documents: docs})
	if err != nil {
		return "", err
	}
	return res.String(), nil
}

// A document malformed after the records is an error with bursts too, and
// after xsl:break as well.
func TestBurstsMalformedTail(t *testing.T) {
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml">
	<xsl:iterate select="a/r"><xsl:break/></xsl:iterate>
	</xsl:source-document></xsl:template></xsl:stylesheet>`
	_, err := burstRun(t, src, "doc.xml", "<a><r/><r/></b>", true)
	if !burstRuns || err == nil || !strings.Contains(err.Error(), "FODC0002") {
		t.Fatalf("bursts ran %v, error %v", burstRuns, err)
	}
	_, terr := burstRun(t, src, "doc.xml", "<a><r/><r/></b>", false)
	if terr == nil || !strings.Contains(terr.Error(), "FODC0002") {
		t.Fatalf("tree error %v", terr)
	}
}

// Many records, totalled by xsl:iterate: the answer is the tree's, and it
// came from bursts. (Memory is measured by the lane's harness, not here.)
func TestBurstsManyRecords(t *testing.T) {
	var b strings.Builder
	b.WriteString("<a>")
	for i := range 20000 {
		fmt.Fprintf(&b, "<r v=\"%d\"><name>record %d</name></r>\n", i%10, i)
	}
	b.WriteString("</a>")
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:template name="main"><xsl:source-document streamable="yes" href="big.xml">
	<xsl:iterate select="a/r"><xsl:param name="t" select="0"/>
	<xsl:on-completion><total><xsl:value-of select="$t"/></total></xsl:on-completion>
	<xsl:next-iteration><xsl:with-param name="t" select="$t + @v"/></xsl:next-iteration>
	</xsl:iterate></xsl:source-document></xsl:template></xsl:stylesheet>`
	out, err := burstRun(t, src, "big.xml", b.String(), true)
	if err != nil || !strings.HasSuffix(out, "<total>90000</total>") || !burstRuns {
		t.Fatalf("got %q, %v, bursts %v", out, err, burstRuns)
	}
}
