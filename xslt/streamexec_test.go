package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// coreRun runs the stylesheet sheet over doc.xml in dir, with the core on
// (core) or every container on the tree, and returns its output or error and
// what streamtrace counted.
func coreRun(sheet, dir string, core bool, opts TransformOptions) (out, errText string, tr streamtrace.Counts) {
	before := streamtrace.Snapshot()
	defer func() { noCore, noBurstPlan, noBursts = false, false, false; tr = streamtrace.Snapshot().Sub(before) }()
	noBurstPlan = core
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		return "", "SHEET: " + err.Error(), tr
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		return "", "COMPILE: " + err.Error(), tr
	}
	docs, _ := NewFileResolver(dir)
	opts.InitialTemplate, opts.Documents = "main", docs
	noCore, noBursts = !core, !core
	res, err := s.Transform(context.Background(), nil, opts)
	if err != nil {
		return "", err.Error(), tr
	}
	return res.String(), "", tr
}

// coreSheet wraps a container body in a stylesheet.
func coreSheet(body string) string { return coreSheetWith("", body) }

// coreSheetWith is coreSheet with top-level declarations decls.
func coreSheetWith(decls, body string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:p="urn:p" exclude-result-prefixes="xs p">` + decls + `
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml">` + body +
		`</xsl:source-document></top></xsl:template></xsl:stylesheet>`
}

var coreDocs = map[string]string{
	"flat": `<doc id="d"><rec k="a" v="1"><sub x="1">t1</sub><sub x="2"/></rec><rec k="b" v="2"><sub x="3">t3</sub></rec>` +
		`<other/><rec k="a" v="3"/></doc>`,
	"nested":  `<doc><rec k="a" v="1"><rec k="b" v="2"><sub x="1"/></rec></rec><w><rec k="c" v="x"><sub>s</sub></rec></w></doc>`,
	"mixed":   `<?pi a?><!--c--><doc>text <rec k="a" v="5">in<![CDATA[ cdata ]]><sub/>tail</rec> more <!--c2--><?p2 x?></doc>`,
	"ns":      `<doc xmlns="urn:d" xmlns:p="urn:p"><p:rec k="a" v="1"/><rec k="b" v="2" p:z="q"/></doc>`,
	"deep":    `<doc><a><b><c><rec k="a" v="1"><sub x="1"/></rec></c></b></a><a><rec k="b" v="2"/></a></doc>`,
	"empty":   `<doc/>`,
	"recroot": `<rec k="r" v="0"><rec k="a" v="1"><sub x="1"/></rec><sub x="2">t</sub></rec>`,
	"bad":     `<doc><rec k="a" v="1"/><rec k="b"></doc>`,
	"wide":    "<doc>" + strings.Repeat(`<rec k="a" v="1"><sub x="1">s</sub></rec>`, 500) + "</doc>",
	"spaces": "<doc>\n <rec k=\"a\" v=\"1\">\n  <sub x=\"1\"> </sub>\n  <sub x=\"2\">\n t </sub>\n </rec>\n" +
		" <w xml:space=\"preserve\">\n  <rec k=\"b\" v=\"2\">\n   <sub x=\"3\">  </sub>\n  </rec>\n" +
		"  <rec k=\"c\" v=\"3\" xml:space=\"default\">\n <sub/>\n </rec>\n </w>\n</doc>",
}

// coreDecls are the whitespace declarations the bodies run under.
var coreDecls = []string{"", `<xsl:strip-space elements="*"/>`,
	`<xsl:strip-space elements="*"/><xsl:preserve-space elements="sub"/>`}

// coreBodies are container bodies. stream says the core must take the
// container: a body that only the tree may run has false.
var coreBodies = []struct {
	body   string
	stream bool
}{
	// sTree only: the document is read through, nothing of it used.
	{`<out a="x">hello</out>`, true},
	{`<xsl:variable name="v" select="1"/><out n="{$v + 1}"/>`, true},
	// Select at the document node, under wrappers.
	{`<out><xsl:value-of select="count(//rec)"/></out>`, true},
	{`<out><xsl:value-of select="sum(//rec/@v)"/></out>`, true},
	{`<out><xsl:value-of select="max(//rec/@k)"/></out>`, true},
	{`<out><xsl:value-of select="max(//rec/@v)"/></out>`, true},
	{`<out><xsl:value-of select="string-join(//rec/@k, ',')"/></out>`, true},
	{`<out><xsl:sequence select="exists(//sub)"/></out>`, true},
	{`<out><xsl:attribute name="n" select="count(/doc/rec)"/></out>`, true},
	{`<xsl:element name="e{1 + 1}"><xsl:value-of select="count(/*/*)"/></xsl:element>`, true},
	{`<out><in><xsl:copy-of select="count(//rec[@k = 'a'])"/></in></out>`, true},
	{`<xsl:if test="true()"><out><xsl:value-of select="count(//rec)"/></out></xsl:if>`, true},
	{`<xsl:choose><xsl:when test="false()"><a/></xsl:when><xsl:otherwise><b><xsl:value-of select="count(//sub)"/></b></xsl:otherwise></xsl:choose>`, true},
	{`<out><xsl:where-populated><w><xsl:value-of select="count(//rec[@k = 'zz'])"/></w></xsl:where-populated></out>`, false},
	{`<out><xsl:value-of select="(//rec)[2]"/>|<xsl:value-of select="'x'"/></out>`, true},
	// Loops: body on the view, motionless.
	{`<xsl:for-each select="//rec"><r k="{@k}" p="{position()}"/></xsl:for-each>`, true},
	{`<xsl:for-each select="/doc/rec"><r n="{name()}" d="{count(ancestor::*)}"/></xsl:for-each>`, true},
	{`<out><xsl:for-each select="//rec[@v = '2']"><xsl:copy/></xsl:for-each></out>`, true},
	// Loops: body reads the match at its start tag.
	{`<xsl:for-each select="/doc/rec"><r><xsl:value-of select="."/></r></xsl:for-each>`, true},
	{`<xsl:for-each select="/doc/rec"><r k="{@k}"><xsl:copy-of select="sub"/></r></xsl:for-each>`, true},
	{`<xsl:for-each select="*/rec"><xsl:copy-of select="."/></xsl:for-each>`, true},
	{`<xsl:for-each select="/doc/rec"><r><xsl:for-each select="sub"><s x="{@x}"/></xsl:for-each></r></xsl:for-each>`, true},
	{`<xsl:for-each select="/*/*"><xsl:copy><xsl:value-of select="count(*)"/></xsl:copy></xsl:for-each>`, true},
	{`<xsl:iterate select="/doc/rec"><xsl:param name="t" select="0"/><xsl:on-completion><t v="{$t}"/></xsl:on-completion>` +
		`<xsl:next-iteration><xsl:with-param name="t" select="$t + 1"/></xsl:next-iteration></xsl:iterate>`, true},
	{`<xsl:iterate select="/doc/rec"><xsl:if test="@k = 'b'"><xsl:break/></xsl:if><i><xsl:value-of select="@k"/></i></xsl:iterate>`, false},
	{`<xsl:iterate select="/doc/rec"><i><xsl:value-of select="."/></i></xsl:iterate>`, true},
	// Loops: the body on a grounded copy (nested matches, consuming body).
	{`<xsl:for-each select="//rec"><xsl:copy-of select="."/></xsl:for-each>`, false},
	{`<xsl:iterate select="/doc/rec"><xsl:param name="t" select="0"/><xsl:on-completion><t v="{$t}"/></xsl:on-completion>` +
		`<xsl:next-iteration><xsl:with-param name="t" select="$t + count(.//node())"/></xsl:next-iteration></xsl:iterate>`, true},
	{`<xsl:iterate select="//rec"><xsl:param name="t" select="0"/><xsl:on-completion><t v="{$t}"/></xsl:on-completion>` +
		`<xsl:next-iteration><xsl:with-param name="t" select="$t + count(.//node())"/></xsl:next-iteration></xsl:iterate>`, false},
	{`<xsl:for-each select="outermost(//rec)"><r><xsl:value-of select="."/></r></xsl:for-each>`, true},
	{`<xsl:for-each select="/doc/rec"><r n="{count(.//node())}"/></xsl:for-each>`, true},
	// copy-of streamed to the output, a copy at a time; one that would
	// ground the document element copies it from the tree.
	{`<out><xsl:copy-of select="//rec"/></out>`, true},
	{`<out><xsl:copy-of select="/*/rec/sub"/></out>`, true},
	{`<out><xsl:copy-of select="//sub/@x"/></out>`, true},
	{`<out><xsl:copy-of select="//*"/></out>`, true},
	{`<xsl:copy-of select="outermost(//rec)"/>`, true},
	{`<xsl:for-each select="/*/rec"><r><xsl:copy-of select="sub"/></r></xsl:for-each>`, true},
	// Simple content over nodes with no text node: their values read off
	// the cursor; with text nodes, refused (they merge first).
	{`<out><xsl:value-of select="/*/rec/sub"/></out>`, true},
	{`<out><xsl:value-of select="//rec" separator="|"/></out>`, true},
	{`<out><xsl:value-of select="(//sub)[2]"/></out>`, true},
	{`<e><xsl:attribute name="a" select="//sub/@x"/></e>`, true},
	{`<e><xsl:attribute name="a" select="//rec" separator="-"/></e>`, true},
	{`<out><xsl:value-of select="//rec/text()"/></out>`, false},
	{`<out><xsl:value-of select="//rec/node()"/></out>`, false},
	// Nested container.
	{`<out><xsl:source-document href="doc.xml" streamable="yes"><n><xsl:value-of select="count(//rec)"/></n></xsl:source-document>` +
		`<xsl:value-of select="count(//sub)"/></out>`, true},
	// Refused: the tree runs them (the analysis, the split, the memory rule).
	{`<xsl:for-each select="//rec"><r><xsl:value-of select="."/></r></xsl:for-each>`, false},
	{`<xsl:for-each select="//rec"><r n="{count(.//node())}"/></xsl:for-each>`, false},
	{`<out><xsl:value-of select="(//rec)[2]/@k"/></out>`, false},
	// Refused: copy-of(.) of what may be the document element.
	{`<xsl:for-each select="/*"><xsl:copy-of select="."/></xsl:for-each>`, false},
	{`<xsl:for-each select="//rec"><xsl:sort select="@k"/><r/></xsl:for-each>`, false},
	{`<xsl:apply-templates select="//rec"/>`, false},
}

// TestStreamCoreMatchesTree: every body over every document, under every
// whitespace declaration, gives on the core the tree's output or the tree's
// error; the ones that must stream ran on the core without a re-run, and
// nothing hit a guard.
func TestStreamCoreMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range coreBodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}

// coreCheck runs sheet over dir's doc.xml on the core and on the tree and
// reports any difference; it returns 1 when the core ran without a re-run.
func coreCheck(t *testing.T, sheet, dir, where string, stream bool, opts TransformOptions) int {
	t.Helper()
	so, se, st := coreRun(sheet, dir, true, opts)
	to, te, tt := coreRun(sheet, dir, false, opts)
	if so != to || se != te {
		t.Errorf("%s:\ncore %q %q\ntree %q %q", where, so, se, to, te)
	}
	if st.Guards+tt.Guards != 0 {
		t.Errorf("%s: %d guard hits", where, st.Guards+tt.Guards)
	}
	if tt.Streamed() != 0 {
		t.Errorf("%s: streamed with the core off", where)
	}
	core := st.Runs[streamtrace.Core]
	switch {
	case stream && core == 0:
		t.Errorf("%s: not on the core", where)
	case !stream && core != 0:
		t.Errorf("%s: on the core", where)
	case core > 0 && st.Reruns != 0 && te == "":
		// A re-run would hide a wrong stream behind the tree's answer:
		// only a document the tree also fails on may need one.
		t.Errorf("%s: re-run on the tree (%d), which succeeds", where, st.Reruns)
	case core > 0 && st.Reruns == 0:
		return 1
	}
	return 0
}

// TestStreamCoreCopyOfRoot: a streamed copy-of that meets the document
// element as a match copies the document from the tree, nothing of the
// stream having been copied, and goes on: one fallback, no re-run, the
// tree's output. Without the give-up it would ground the whole document.
func TestStreamCoreCopyOfRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(coreDocs["recroot"]), 0o600); err != nil {
		t.Fatal(err)
	}
	sheet := coreSheet(`<out><xsl:copy-of select="//rec"/></out><n><xsl:value-of select="1"/></n>`)
	so, se, st := coreRun(sheet, dir, true, TransformOptions{})
	to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
	if so != to || se != te {
		t.Fatalf("core %q %q\ntree %q %q", so, se, to, te)
	}
	if st.Runs[streamtrace.Core] != 1 || st.Fallbacks != 1 || st.Reruns != 0 || st.Guards != 0 {
		t.Errorf("trace %+v: want one core run, one fallback, no re-run", st)
	}
}

// TestStreamCoreMemory: a container on the core holds what its plan says --
// a count or a drain next to nothing, a loop or a streamed copy-of its
// output -- never the tree. Each is measured against the tree's run of the
// same transform: the tree holds the document and the output.
func TestStreamCoreMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("memory test")
	}
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 150000 {
		fmt.Fprintf(&b, `<rec k="%c" v="%d">%d<sub x="%d"/><sub x="1">x</sub></rec>`, 'a'+i%5, i%100, i%10, i%7)
	}
	b.WriteString("</doc>")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		body string
		frac float64 // the most of the tree's peak the core may take
	}{
		{`<out><xsl:value-of select="count(//rec)"/></out>`, 0.2},
		{`<out>static</out>`, 0.2},
		{`<out><xsl:for-each select="//rec"><r k="{@k}"/></xsl:for-each></out>`, 0.6},
		{`<xsl:for-each select="/*/rec"><r><xsl:value-of select="."/></r></xsl:for-each>`, 0.6},
		{`<out><xsl:copy-of select="//rec"/></out>`, 0.75},
	} {
		sheet := coreSheet(c.body)
		var st streamtrace.Counts
		core := splitPeak(func() { _, _, st = coreRun(sheet, dir, true, TransformOptions{}) })
		tree := splitPeak(func() { coreRun(sheet, dir, false, TransformOptions{}) })
		t.Logf("%s: core %.1f MB, tree %.1f MB", c.body, float64(core)/1e6, float64(tree)/1e6)
		if st.Runs[streamtrace.Core] != 1 || st.Reruns != 0 {
			t.Errorf("%s: trace %+v", c.body, st)
		}
		if float64(core) > c.frac*float64(tree) {
			t.Errorf("%s: peak live heap %.1f MB, tree %.1f MB (bound %.0f%%)", c.body,
				float64(core)/1e6, float64(tree)/1e6, 100*c.frac)
		}
	}
}
