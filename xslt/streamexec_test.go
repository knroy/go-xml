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
func coreSheet(body string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:p="urn:p" exclude-result-prefixes="xs p">
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml">` + body +
		`</xsl:source-document></top></xsl:template></xsl:stylesheet>`
}

var coreDocs = map[string]string{
	"flat": `<doc id="d"><rec k="a" v="1"><sub x="1">t1</sub><sub x="2"/></rec><rec k="b" v="2"><sub x="3">t3</sub></rec>` +
		`<other/><rec k="a" v="3"/></doc>`,
	"nested": `<doc><rec k="a" v="1"><rec k="b" v="2"><sub x="1"/></rec></rec><w><rec k="c" v="x"><sub>s</sub></rec></w></doc>`,
	"mixed":  `<?pi a?><!--c--><doc>text <rec k="a" v="5">in<![CDATA[ cdata ]]><sub/>tail</rec> more <!--c2--><?p2 x?></doc>`,
	"ns":     `<doc xmlns="urn:d" xmlns:p="urn:p"><p:rec k="a" v="1"/><rec k="b" v="2" p:z="q"/></doc>`,
	"deep":   `<doc><a><b><c><rec k="a" v="1"><sub x="1"/></rec></c></b></a><a><rec k="b" v="2"/></a></doc>`,
	"empty":  `<doc/>`,
	"bad":    `<doc><rec k="a" v="1"/><rec k="b"></doc>`,
	"wide":   "<doc>" + strings.Repeat(`<rec k="a" v="1"><sub x="1">s</sub></rec>`, 500) + "</doc>",
}

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
	// Nested container.
	{`<out><xsl:source-document href="doc.xml" streamable="yes"><n><xsl:value-of select="count(//rec)"/></n></xsl:source-document>` +
		`<xsl:value-of select="count(//sub)"/></out>`, true},
	// Refused: the tree runs them (the analysis, the split, the memory rule).
	{`<xsl:for-each select="//rec"><r><xsl:value-of select="."/></r></xsl:for-each>`, false},
	{`<xsl:for-each select="//rec"><r n="{count(.//node())}"/></xsl:for-each>`, false},
	{`<out><xsl:value-of select="(//rec)[2]/@k"/></out>`, false},
	{`<out><xsl:copy-of select="//rec"/></out>`, false},
	{`<xsl:for-each select="//rec"><xsl:sort select="@k"/><r/></xsl:for-each>`, false},
	{`<xsl:apply-templates select="//rec"/>`, false},
}

// TestStreamCoreMatchesTree: every body over every document gives, on the
// core, the tree's output or the tree's error; the ones that must stream ran
// on the core and nothing hit a guard.
func TestStreamCoreMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range coreBodies {
			sheet := coreSheet(c.body)
			so, se, st := coreRun(sheet, dir, true, TransformOptions{})
			to, te, tt := coreRun(sheet, dir, false, TransformOptions{})
			where := fmt.Sprintf("doc %s, body %s", name, c.body)
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
			if c.stream && core == 0 {
				t.Errorf("%s: not on the core", where)
			}
			if !c.stream && core != 0 {
				t.Errorf("%s: on the core", where)
			}
			if core > 0 && st.Reruns == 0 {
				streamed++
			}
			// A re-run would hide a wrong stream behind the tree's answer:
			// only a document the tree also fails on may need one.
			if core > 0 && st.Reruns != 0 && te == "" {
				t.Errorf("%s: re-run on the tree (%d), which succeeds", where, st.Reruns)
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}
