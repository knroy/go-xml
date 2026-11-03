package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// streamMergeDocs are sorted inputs for the merge tests: keys repeated within and
// across files, a key on one file only, an empty file, unsorted and mixed.
var streamMergeDocs = map[string]string{
	"a.xml":     `<log><e t="1" v="a1"/><e t="3" v="a3"/><e t="3" v="a3b"><n>x</n></e><e t="7" v="a7"/></log>`,
	"b.xml":     `<log><e t="2" v="b2"/><e t="3" v="b3"/><e t="9" v="b9"/></log>`,
	"c.xml":     `<log xmlns:p="urn:p"><g><e t="0" v="c0" p:q="1"/></g><g><e t="3" v="c3"/><e t="10" v="c10"/></g></log>`,
	"empty.xml": `<log/>`,
	"uns.xml":   `<log><e t="5" v="u5"/><e t="4" v="u4"/></log>`,
	"str.xml":   `<log><e t="x" v="s1"/></log>`,
	"desc.xml":  `<log><e t="9" v="d9"/><e t="3" v="d3"/><e t="1" v="d1"/></log>`,
	"bad.xml":   `<log><e t="1"/><e t="2"></log>`,
}

func mergeSheet(sources, action string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:template name="main"><out><xsl:merge>` + sources + `<xsl:merge-action>` + action +
		`</xsl:merge-action></xsl:merge></out></xsl:template></xsl:stylesheet>`
}

func mergeSrc(name, uris, sel, key string, extra string) string {
	return `<xsl:merge-source name="` + name + `" for-each-source="` + uris + `" select="` + sel + `" ` + extra + `>` +
		`<xsl:merge-key select="` + key + `"/></xsl:merge-source>`
}

// TestStreamMergeMatchesTree: merges of streamed sources (several files per
// source, sources mixed with a collected one) give the tree's output or
// error; those that must stream ran as a streamed merge, without a re-run
// unless the tree fails too.
func TestStreamMergeMatchesTree(t *testing.T) {
	dir := t.TempDir()
	for n, d := range streamMergeDocs {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(d), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	num := `xs:integer(@t)`
	group := `<g k="{current-merge-key()}" p="{position()}" n="{count(current-merge-group())}" a="{current-merge-group('A')/@v}" b="{current-merge-group('B')/@v}"/>`
	copyAll := `<g k="{current-merge-key()}"><xsl:copy-of select="current-merge-group()"/></g>`
	cases := []struct {
		sources, action string
		stream          bool
	}{
		{mergeSrc("A", "'a.xml'", "log/e", num, "") + mergeSrc("B", "'b.xml'", "log/e", num, ""), group, true},
		{mergeSrc("A", "('a.xml', 'b.xml')", "log/e", num, "") + mergeSrc("B", "'c.xml'", "log/g/e", num, ""), group, true},
		{mergeSrc("A", "('a.xml', 'empty.xml')", "*/e", num, "") + mergeSrc("B", "('c.xml', 'b.xml')", "log/g/e", num, ""), copyAll, true},
		// String keys compare as strings: "10" before "2".
		{mergeSrc("A", "'a.xml'", "log/e", "@t", "") + mergeSrc("B", "'c.xml'", "log/g/e", "string(@t)", ""), group, true},
		// A collected source before streamed ones; after one, on the tree.
		{`<xsl:merge-source name="B" select="doc('b.xml')/log/e"><xsl:merge-key select="xs:integer(@t)"/></xsl:merge-source>` + mergeSrc("A", "('a.xml', 'c.xml')", "*/e", num, ""), group, true},
		{`<xsl:merge-source name="B" select="doc('b.xml')/log/e"><xsl:merge-key select="xs:integer(@t)"/></xsl:merge-source>` + mergeSrc("A", "'a.xml'", "log/e", num, ""),
			`<g k="{current-merge-key()}"><xsl:copy-of select="current-merge-group()"/><xsl:value-of select="current-merge-group()/@v"/></g>`, true},
		{mergeSrc("A", "'a.xml'", "log/e", num, "") + `<xsl:merge-source name="B" select="doc('b.xml')/log/e"><xsl:merge-key select="xs:integer(@t)"/></xsl:merge-source>`, group, false},
		{mergeSrc("A", "'a.xml'", "log/e[@t != '3']", num, "") + mergeSrc("B", "'b.xml'", "log/e[2]", num, ""), group, true},
		// Descending, the snapshot's ancestors, a namespace kept.
		{`<xsl:merge-source name="A" for-each-source="'desc.xml'" select="log/e"><xsl:merge-key select="xs:integer(@t)" order="descending"/></xsl:merge-source>`,
			`<g k="{current-merge-key()}" up="{name(current-merge-group()/..)}" sib="{count(current-merge-group()/../*)}"/>`, true},
		{mergeSrc("A", "'c.xml'", "log/g/e", num, ""), `<xsl:copy-of select="current-merge-group()"/>`, true},
		// Errors, which the tree reports before any action: an input out of
		// order (XTDE2220), keys that cannot be compared (XTTE2230), a
		// malformed document, a document not there; the action's own.
		{mergeSrc("A", "'a.xml'", "log/e", num, "") + mergeSrc("B", "'uns.xml'", "log/e", num, ""), group, true},
		{mergeSrc("A", "'a.xml'", "log/e", num, "") + mergeSrc("B", "'str.xml'", "log/e", "string(@t)", ""), group, false}, // keys of two types: the tree's
		{mergeSrc("A", "'a.xml'", "log/e", num, "") + mergeSrc("B", "'bad.xml'", "log/e", num, ""), group, true},
		{mergeSrc("A", "'a.xml'", "log/e", num, "") + mergeSrc("B", "'none.xml'", "log/e", num, ""), group, true},
		{mergeSrc("A", "'a.xml'", "log/e", num, "") + mergeSrc("B", "'b.xml'", "log/e", num, ""), `<g v="{xs:integer(current-merge-group('B')/@t) div 0}"/>`, true},
		// Refused: last(), sort-before-merge, keys of two types (the tree's
		// sort is no consistent order across them).
		{mergeSrc("A", "'a.xml'", "log/e", num, "") + mergeSrc("B", "'b.xml'", "log/e", "@t", ""), group, false},
		{mergeSrc("A", "'a.xml'", "log/e", num, "") + mergeSrc("B", "'b.xml'", "log/e", num, ""), `<g n="{last()}"/>`, false},
		{mergeSrc("A", "'uns.xml'", "log/e", num, `sort-before-merge="yes"`), group, false},
	}
	streamed := 0
	for i, c := range cases {
		sheet := mergeSheet(c.sources, c.action)
		so, se, st := coreRun(sheet, dir, true, TransformOptions{})
		to, te, tt := coreRun(sheet, dir, false, TransformOptions{})
		where := fmt.Sprintf("case %d %s", i, c.sources)
		if so != to || se != te {
			t.Errorf("%s:\nstream %q %q\ntree   %q %q", where, so, se, to, te)
		}
		if st.Guards != 0 || tt.Streamed() != 0 {
			t.Errorf("%s: guards %d, streamed with streaming off %d", where, st.Guards, tt.Streamed())
		}
		ran := st.Runs[streamtrace.Merge] > 0
		switch {
		case ran != c.stream:
			t.Errorf("%s: streamed %v, want %v", where, ran, c.stream)
		case ran && st.Reruns > 0 && te == "":
			t.Errorf("%s: re-run on the tree, which succeeds", where)
		case ran && st.Reruns == 0:
			streamed++
		}
	}
	if streamed < 8 {
		t.Errorf("only %d merges streamed without a re-run", streamed)
	}
}

// TestStreamMergeMemory: a streamed merge of two files of 60,000 records
// each holds a head per input and a group, never the documents.
func TestStreamMergeMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("memory test")
	}
	dir := t.TempDir()
	for _, f := range []struct {
		name string
		off  int
	}{{"a.xml", 0}, {"b.xml", 1}} {
		var b strings.Builder
		b.WriteString("<log>")
		for i := range 60000 {
			fmt.Fprintf(&b, `<e t="%d" v="%d"><m>message %d</m></e>`, 2*i+f.off, i%97, i)
		}
		b.WriteString("</log>")
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sheet := mergeSheet(mergeSrc("A", "'a.xml'", "log/e", "xs:integer(@t)", "")+mergeSrc("B", "'b.xml'", "log/e", "xs:integer(@t)", ""),
		`<xsl:if test="current-merge-group()/@v = '0'"><x t="{current-merge-key()}"/></xsl:if>`)
	var st streamtrace.Counts
	core := splitPeak(func() { _, _, st = coreRun(sheet, dir, true, TransformOptions{}) })
	tree := splitPeak(func() { coreRun(sheet, dir, false, TransformOptions{}) })
	t.Logf("core %.1f MB, tree %.1f MB", float64(core)/1e6, float64(tree)/1e6)
	if st.Runs[streamtrace.Merge] != 1 || st.Reruns != 0 {
		t.Errorf("trace %+v", st)
	}
	if float64(core) > 0.25*float64(tree) && !raceEnabled {
		t.Errorf("peak live heap %.1f MB, tree %.1f MB (bound 25%%)", float64(core)/1e6, float64(tree)/1e6)
	}
}
