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
		{"single instruction", `<xsl:value-of select="count(account/transaction)"/>`, true, ""},
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

// Inputs on which bursts once differed from the tree: whitespace-only
// attribute values read from released text blocks (a wrong value, or a
// panic), and a path predicate ending in a number, which is positional.
func TestBurstsMatchTreeOn(t *testing.T) {
	for _, tc := range []struct {
		name, body, doc string
		bursts          bool
	}{
		{"space attributes", `<xsl:for-each select="doc/rec"><v a="[{@a}]"/></xsl:for-each>`,
			`<doc><rec a="  "/><rec x="abcdefghij" a="  "/></doc>`, true},
		{"space attributes, three records", `<xsl:for-each select="doc/rec"><v a="[{@a}]"/></xsl:for-each>`,
			`<doc><rec x="hello" a="  "/><rec x="abcdefg" a="  "/><rec x="zz" a="  "/></doc>`, true},
		{"space attributes on the spine", `<xsl:for-each select="doc/g/rec"><v a="[{../@a}]" b="[{@b}]"/></xsl:for-each>`,
			`<doc><g a=" "><rec b=" "/><rec x="qqqq" b=" "/></g><g a=" "><rec b=" "/></g></doc>`, true},
		{"numeric path predicate", `<xsl:for-each select="doc/rec[@k/xs:integer(.)]"><xsl:copy-of select="."/></xsl:for-each>`,
			`<doc><rec k="1"/><rec k="2"/><rec k="3"/></doc>`, false},
		{"axis path predicate", `<xsl:for-each select="doc/rec[@k]"><xsl:copy-of select="."/></xsl:for-each>`,
			`<doc><rec k="1"/><rec/></doc>`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">` +
				`<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml">` +
				tc.body + `</xsl:source-document></xsl:template></xsl:stylesheet>`
			got, err := burstRun(t, src, "doc.xml", tc.doc, true)
			ran := burstRuns
			want, err2 := burstRun(t, src, "doc.xml", tc.doc, false)
			if (err == nil) != (err2 == nil) || got != want {
				t.Fatalf("bursts differ from the tree\nbursts: %q %v\ntree:   %q %v", got, err, want, err2)
			}
			if ran != tc.bursts {
				t.Fatalf("ran in bursts: %v, want %v (output %q)", ran, tc.bursts, got)
			}
		})
	}
}

// A document that fails to parse is not read, in bursts as in the tree: once
// xsl:try has caught the FODC0002, writing that URI is not XTDE1500.
func TestBurstsFailedReadIsNotRead(t *testing.T) {
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:template name="main"><xsl:try><xsl:source-document streamable="yes" href="doc.xml">
	<xsl:for-each select="a/r"><xsl:copy-of select="."/></xsl:for-each>
	</xsl:source-document><xsl:catch><caught/></xsl:catch></xsl:try>
	<xsl:result-document href="doc.xml"><a/></xsl:result-document></xsl:template></xsl:stylesheet>`
	for _, bursts := range []bool{true, false} {
		// The resolver records the path with symlinks resolved (macOS
		// temporary directories are under one), so the output must be too.
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte("<a><r/><r/></b>"), 0o600); err != nil {
			t.Fatal(err)
		}
		tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
		if err != nil {
			t.Fatal(err)
		}
		sheet, err := Compile(tree.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		docs, err := NewFileResolver(dir)
		if err != nil {
			t.Fatal(err)
		}
		burstRuns = false
		noBursts = !bursts
		traceBursts = func(string) { burstRuns = true }
		_, err = sheet.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main",
			Documents: docs, BaseOutputURI: fileURIOf(filepath.Join(dir, "out.xml"))})
		noBursts, traceBursts = false, nil
		if err != nil || burstRuns != bursts {
			t.Errorf("bursts %v (ran %v): %v", bursts, burstRuns, err)
		}
	}
}

// A value-of, copy-of or sequence over one record path runs in bursts when
// its consumer is bounded or pipelined, and is left to the tree otherwise.
// Each row runs with bursts, with bursts but no folding, and on the tree; all
// three must agree, error text included, and an error must have come from
// the tree path (fallback).
func TestBurstSelectsMatchTree(t *testing.T) {
	const recs = `<doc><rec k="a" v="3"/><rec k="B" v="1.5"><x>p</x><x>q</x></rec><other v="99"/><rec k="a" v="10"><x>r</x></rec></doc>`
	const nan = `<doc><rec v="2"/><rec v="NaN"/><rec v="1"/></doc>`
	const bad = `<doc><rec v="1"/><rec v="x"/><rec v="2"/></doc>`
	const tail = `<doc><rec v="1"/><rec v="2"/></dox>`
	const ci = `'http://www.w3.org/2005/xpath-functions/collation/html-ascii-case-insensitive'`
	for _, tc := range []struct {
		name, body, doc  string
		bursts, fallback bool
	}{
		// bounded reducers, folded
		{"count", `<xsl:value-of select="count(doc/rec)"/>`, recs, true, false},
		{"count, predicate", `<n><xsl:value-of select="count(doc/rec[@k='a'])"/></n>`, recs, true, false},
		{"count, several per record", `<xsl:value-of select="count(doc/rec[@v]/x)"/>`, recs, true, false},
		{"count, none", `<xsl:value-of select="count(doc/none)"/>`, recs, true, false},
		{"sum", `<xsl:value-of select="sum(doc/rec/@v)"/>`, recs, true, false},
		{"sum, empty", `<xsl:value-of select="sum(doc/none/@v)"/>`, recs, true, false},
		{"sum, zero", `<xsl:sequence select="sum(doc/none/@v, 'zero')"/>`, recs, true, false},
		{"sum, mixed numerics", `<xsl:value-of select="sum(doc/rec/(if (@k = 'a') then xs:decimal(@v) else xs:double(@v)))"/>`, recs, true, false},
		{"sum, untyped error", `<xsl:value-of select="sum(doc/rec/@v)"/>`, bad, true, true},
		{"sum, error before a malformed tail", `<xsl:value-of select="sum(doc/rec/xs:integer(@v))"/>`, tail, true, true},
		{"max", `<xsl:value-of select="max(doc/rec/@v) + 1"/>`, recs, true, false},
		{"min, NaN", `<xsl:value-of select="min(doc/rec/@v)"/>`, nan, true, false},
		{"max, strings by collation", `<xsl:value-of select="max(doc/rec/@k/string(), ` + ci + `)"/>`, recs, true, false},
		{"min, strings", `<xsl:value-of select="min(doc/rec/@k/string())"/>`, recs, true, false},
		{"max, mixed numerics", `<xsl:value-of select="max(doc/rec/(if (@k = 'a') then xs:decimal(@v) else xs:float(@v)))"/>`, recs, true, true},
		{"max, untyped error", `<xsl:value-of select="max(doc/rec/@k)"/>`, recs, true, true},
		{"max, empty", `<xsl:sequence select="empty(max(doc/none/@v))"/>`, recs, true, false},
		// bounded reducers, not folded
		{"avg", `<xsl:value-of select="avg(doc/rec/@v)"/>`, recs, true, false},
		{"exists", `<xsl:value-of select="exists(doc/rec[@k = 'z'])"/>`, recs, true, false},
		{"boolean, not", `<xsl:value-of select="not(boolean(doc/rec/x))"/>`, recs, true, false},
		{"general comparison", `<xsl:value-of select="doc/rec/@v = 10"/>`, recs, true, false},
		{"value comparison error", `<xsl:value-of select="doc/rec/@v eq 10"/>`, recs, true, true},
		{"some", `<xsl:value-of select="some $r in doc/rec satisfies $r/@v > 5"/>`, recs, true, false},
		{"some, type error", `<xsl:value-of select="some $r in doc/rec satisfies $r/@v gt 5"/>`, recs, true, true},
		{"literal position", `<xsl:value-of select="(doc/rec/@v)[2] + 1"/>`, recs, true, false},
		{"head", `<xsl:copy-of select="head(doc/rec/@k/string())"/>`, recs, true, false},
		{"exactly-one error", `<xsl:copy-of select="exactly-one(doc/rec)"/>`, recs, true, true},
		{"deep-equal", `<xsl:value-of select="deep-equal(doc/rec/@k/string(), ('a', 'B', 'a'))"/>`, recs, true, false},
		{"cast", `<xsl:value-of select="xs:string(doc/rec[@k = 'B']/@v)"/>`, recs, true, false},
		{"positional within the record", `<xsl:value-of select="count(doc/rec/x[1])"/>`, recs, true, false},
		{"positional record", `<xsl:copy-of select="doc/rec[2]"/>`, recs, true, false},
		// pipelined
		{"simple map", `<xsl:value-of select="doc/rec/@k ! string()"/>`, recs, true, false},
		{"string-join", `<xsl:value-of select="string-join(doc/rec/@k, '-')"/>`, recs, true, false},
		{"copy-of path", `<out><xsl:copy-of select="doc/rec[@v > 2]"/></out>`, recs, true, false},
		{"copy-of filtered", `<xsl:copy-of select="copy-of(doc/rec)[x = 'r']"/>`, recs, true, false},
		{"sequence data", `<xsl:sequence select="doc/rec/x/data()"/>`, recs, true, false},
		{"for", `<xsl:value-of select="for $v in doc/rec/@v/data() return $v * 2"/>`, recs, true, false},
		{"path from the items", `<xsl:value-of select="tail(doc/rec)/@v"/>`, recs, true, false},
		{"path from copies", `<xsl:value-of select="count(copy-of(doc/rec)/x)"/>`, recs, true, false},
		{"unordered", `<xsl:value-of select="sum(unordered(doc/rec/@v))"/>`, recs, true, false},
		{"malformed tail", `<xsl:value-of select="doc/rec/@v"/>`, tail, true, true},
		// refused
		{"distinct-values", `<xsl:value-of select="count(distinct-values(doc/rec/@k))"/>`, recs, false, false},
		{"reverse", `<xsl:value-of select="reverse(doc/rec/@k/string())"/>`, recs, false, false},
		{"last()", `<xsl:value-of select="(doc/rec/@k)[last()]"/>`, recs, false, false},
		{"union", `<xsl:value-of select="count(doc/rec union ())"/>`, recs, false, false},
		{"two paths", `<xsl:value-of select="sum(doc/rec/@v) div count(doc/rec)"/>`, recs, false, false},
		{"path from copies the path made", `<xsl:value-of select="(doc/rec/copy-of())/x"/>`, recs, false, false},
		{"let", `<xsl:value-of select="let $r := doc/rec return count($r)"/>`, recs, false, false},
		{"computed separator", `<xsl:value-of select="doc/rec/@k" separator="{count(doc/rec)}"/>`, recs, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">` +
				`<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml">` +
				tc.body + `</xsl:source-document></xsl:template></xsl:stylesheet>`
			fell := false
			traceBurstFallback = func(error) { fell = true }
			defer func() { traceBurstFallback, noBurstFold = nil, false }()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(tc.doc), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := burstRunIn(t, src, dir, true)
			ran := burstRuns
			noBurstFold = true
			kept, err1 := burstRunIn(t, src, dir, true)
			noBurstFold = false
			want, err2 := burstRunIn(t, src, dir, false)
			if got != want || kept != want || fmt.Sprint(err) != fmt.Sprint(err2) || fmt.Sprint(err1) != fmt.Sprint(err2) {
				t.Fatalf("bursts differ from the tree\nbursts: %q %v\nkept:   %q %v\ntree:   %q %v", got, err, kept, err1, want, err2)
			}
			if ran != tc.bursts || fell != tc.fallback {
				t.Fatalf("ran in bursts: %v, fell back %v; want %v, %v (output %q, %v)", ran, fell, tc.bursts, tc.fallback, got, err)
			}
		})
	}
}
