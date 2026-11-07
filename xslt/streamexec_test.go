package xslt

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash/fnv"
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
	defer func() { noBursts = false; tr = streamtrace.Snapshot().Sub(before) }()
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		return "", "SHEET: " + err.Error(), tr
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		return "", "COMPILE: " + err.Error(), tr
	}
	docs, _ := NewFileResolver(dir)
	docs.AllowDOCTYPE = opts.SourceParseOptions.AllowDOCTYPE // a DTD document's rows
	opts.InitialTemplate, opts.Documents = "main", docs
	noBursts = !core
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
	// A local variable whose select consumes, bound where the sequence
	// constructor reaches it (J1, streamvariable.go).
	{`<xsl:variable name="a" select="avg(/doc/rec/@v)"/><out a="{$a}"/>`, true},
	{`<xsl:variable name="s" select="//sub ! string(@x)"/><out><xsl:value-of select="$s" separator=","/></out>`, true},
	{`<xsl:variable name="m" as="map(*)" select="map{1: count(//rec)}"/><out n="{$m(1)}"/>`, true},
	{`<out><xsl:for-each select="doc/rec"><xsl:variable name="n" select="count(sub)"/><e n="{$n}" k="{@k}"/></xsl:for-each></out>`, true},
	{`<xsl:variable name="u" select="count(//rec)"/><out/>`, true},
	{`<xsl:variable name="e" select="sum(//rec/@k)"/><out e="{$e}"/>`, true},
	{`<xsl:variable name="a" select="count(//rec)"/><out><xsl:value-of select="count(//sub)"/></out>`, false},
	// A motionless xsl:for-each: over a variable's items (any body), or over
	// nodes of the view read as a view (J1, streamvariable.go).
	{`<out><xsl:for-each select="doc/rec"><xsl:variable name="t" as="xs:string*" select="(@k, @v)"/><e><xsl:for-each select="$t"><t v="{.}" p="{position()}" l="{last()}"/></xsl:for-each></e></xsl:for-each></out>`, true},
	{`<xsl:variable name="n" select="count(//rec)"/><out><xsl:for-each select="1 to $n"><i v="{.}"/></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="//sub"><s><xsl:for-each select="ancestor::*"><xsl:value-of select="concat(name(.), '/')"/></xsl:for-each></s></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="doc/rec"><r><xsl:for-each select="@*"><a n="{name()}" v="{.}"/></xsl:for-each></r></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="doc/rec"><r><xsl:for-each select="@*"><xsl:sort select="."/><a v="{.}"/></xsl:for-each></r></xsl:for-each></out>`, false},
	// ... a body reading more than a view has: refused (it would re-run).
	{`<out><xsl:for-each select="//sub"><s><xsl:for-each select="ancestor::*"><xsl:value-of select="has-children()"/></xsl:for-each></s></xsl:for-each></out>`, false},
	{`<out><xsl:for-each select="doc/rec"><r><xsl:for-each select="@*"><a c="{has-children(..)}"/></xsl:for-each></r></xsl:for-each></out>`, false},
	// A loop over head() of a descendant path whose body reads the match's
	// content: a match inside the first is no item (headOnly,
	// streamlast_mm2.go; si-lre-118).
	// The body's validation keeps it from a grounded copy.
	{`<out><xsl:for-each select="head(//rec)"><xsl:element name="r" validation="strip"><xsl:copy-of select="*"/></xsl:element></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="head(//*[@k])"><r xsl:validation="strip"><xsl:copy-of select="node()"/></r></xsl:for-each></out>`, true},
	// Select at the document node, under wrappers.
	{`<out><xsl:value-of select="count(//rec)"/></out>`, true},
	// xsl:comment and xsl:processing-instruction with a consuming select
	// (si-where-populated s-019, s-020; si-on-empty s-008).
	{`<out><xsl:comment select="count(//rec)"/></out>`, true},
	{`<out><xsl:processing-instruction name="p" select="sum(//rec/@v)"/></out>`, true},
	{`<out><xsl:for-each select="doc/rec"><xsl:comment select="count(sub)"/></xsl:for-each></out>`, true},
	{`<out><xsl:where-populated><xsl:processing-instruction name="p" select="head(//sub)"/></xsl:where-populated></out>`, true},
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
	{`<out><xsl:where-populated><w><xsl:value-of select="count(//rec[@k = 'zz'])"/></w></xsl:where-populated></out>`, true},
	{`<out><xsl:where-populated><w><xsl:value-of select="count(//rec[@k = 'a'])"/></w></xsl:where-populated></out>`, true},
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
	// A body that reads only some matches: the others are skipped.
	{`<xsl:for-each select="/*/rec"><xsl:if test="@k = 'a'"><r><xsl:value-of select="."/></r></xsl:if><n/></xsl:for-each>`, true},
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
	{`<out><xsl:value-of select="//rec/text()"/></out>`, true}, // kept leaves (G4)
	// Text, comment and PI matches are kept since G4 (streamleaf.go).
	{`<out><xsl:value-of select="//rec/node()"/></out>`, true},
	// The burst plans' shapes the core took at M1.6: values made from each
	// grounded match (sf-deep-equal, si-lre-C), a choice under simple
	// content (sx-ifExpr), a simple map the analysis calls motionless
	// (sx-bang), a body on a copy of a match that may be the document
	// element (si-lre-A).
	{`<out><xsl:sequence select="/*/rec/sub/number()"/></out>`, true},
	{`<out><xsl:sequence select="/*/rec[position() lt 3]/(@v * 2)"/></out>`, true},
	{`<out><xsl:value-of select="if (false()) then () else data(/*/rec/@v)"/></out>`, true},
	{`<out><xsl:sequence select="subsequence(data(*/rec/@v), 1, 2) ! (position(), .)"/></out>`, true},
	{`<xsl:for-each select="outermost(//rec)"><r><xsl:sequence select="text()"/></r></xsl:for-each>`, true},
	// A tail that climbs: the tree makes the ancestors once each before
	// their values (sf-insert-before-035), and so does the climbing split.
	{`<out><xsl:value-of select="//sub/ancestor-or-self::*/data(@k)" separator="|"/></out>`, true},
	// Nested container.
	{`<out><xsl:source-document href="doc.xml" streamable="yes"><n><xsl:value-of select="count(//rec)"/></n></xsl:source-document>` +
		`<xsl:value-of select="count(//sub)"/></out>`, true},
	// What the document node's stand-in has beside its names: its URIs, its
	// kind (streamtee_docnode.go; si-choose-009, si-on-empty-025, stream-005).
	{`<xsl:choose><xsl:when test="document-uri(.) = 'urn:no'"><n/></xsl:when><xsl:when test=".//rec"><y/></xsl:when>` +
		`<xsl:otherwise><z/></xsl:otherwise></xsl:choose>`, true},
	{`<o b="{ends-with(base-uri(.), 'doc.xml')}" d="{ends-with(document-uri(), 'doc.xml')}" p="{position()}"` +
		` i="{. instance of document-node()}"><xsl:value-of select="count(//rec)"/></o>`, true},
	{`<o><xsl:sequence select="ends-with(base-uri(), 'doc.xml')[exists(1)]"/><xsl:copy-of select="doc/rec"/></o>`, true},
	// Refused: the tree runs them (the analysis, the split, the memory rule).
	{`<xsl:for-each select="//rec"><r><xsl:value-of select="."/></r></xsl:for-each>`, false},
	{`<xsl:for-each select="//rec"><r n="{count(.//node())}"/></xsl:for-each>`, false},
	{`<out><xsl:value-of select="(//rec)[2]/@k"/></out>`, false},
	// A body on a grounded copy of what may be the document element: the
	// copy costs what its tree does (the burst path's shape, si-lre-C).
	{`<xsl:for-each select="/*"><xsl:copy-of select="."/></xsl:for-each>`, true},
	{`<xsl:for-each select="//rec"><xsl:sort select="@k"/><r/></xsl:for-each>`, false},
	// Copies of the whole document or its element, the select run on the
	// document built off the cursor (streamcopy_whole.go; sf-innermost-001,
	// sf-reverse-001, sf-snapshot-0101a/b): no memory saved.
	{`<o><xsl:value-of select="innermost(snapshot(/*)//rec)/@k"/></o>`, true},
	{`<o><xsl:value-of select="reverse(snapshot(/doc)//rec)/@k"/></o>`, true},
	{`<o><xsl:value-of select="count(copy-of(/)//node())"/></o>`, true},
	{`<o><xsl:copy-of select="snapshot(/*)"/></o>`, true},
	{`<xsl:variable name="v" select="snapshot(/)"/><o e="{deep-equal(doc('doc.xml'), $v)}" i="{$v is doc('doc.xml')}"/>`, true},
	{`<xsl:variable name="w" select="snapshot(/*)"/><o e="{deep-equal(doc('doc.xml')/*, $w)}" p="{exists($w/..)}" n="{name($w/..)}{count($w/../node())}"` +
		` i="{$w is doc('doc.xml')/*, $w/.. is doc('doc.xml')}"/>`, true},
	{`<o><xsl:copy-of select="snapshot(/*)//rec[@k = current()//rec/@k]"/></o>`, false},
	{`<xsl:variable name="w" select="snapshot(/*)"/><o p="{exists($w/..)}"><xsl:variable name="w" select="1"/><xsl:value-of select="$w"/></o>`, false},
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
				if raceSkip(where) {
					continue
				}
				streamed += coreCheck(t, coreSheetWith(d, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}

// raceStride: under -race a stream-vs-tree differential checks one case in
// raceStride. The streaming paths these walk run on one goroutine, so the race
// detector learns nothing more from the other cases; the race lane only paid
// for them in minutes (the xslt package passed 25 minutes on CI). The plain
// run checks every case.
const raceStride = 4

// raceSkip reports whether to skip the differential case named by key under
// -race. The pick hashes the case's own values, not its place in a map's
// iteration order, so every race run checks the same cases.
func raceSkip(key ...any) bool {
	if !raceEnabled {
		return false
	}
	h := fnv.New32a()
	fmt.Fprint(h, key...)
	return h.Sum32()%raceStride != 0
}

// coreCheck runs sheet over dir's doc.xml on the core and on the tree and
// reports any difference; it returns 1 when the core ran without a re-run.
func coreCheck(t *testing.T, sheet, dir, where string, stream bool, opts TransformOptions) int {
	t.Helper()
	so, se, st := coreRun(sheet, dir, true, opts)
	to, te, tt := coreRun(sheet, dir, false, opts)
	if so != to || !streamtrace.SameError(se, te) { // an input's invalidity by code: SameError
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
	// Under -race the instrumentation's heap moves the ratios, so the bounds
	// are not checked there: the race lane would pay minutes for no assertion.
	if testing.Short() || raceEnabled {
		t.Skip("measures the live heap")
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
		// Its output is a copy of nearly the whole document: what the
		// tree holds beside the document (65-76% of the tree's peak,
		// measured); collecting grounded copies first would pass the tree.
		{`<out><xsl:copy-of select="//rec"/></out>`, 0.9},
		// Values collected: their own strings, not ones into the parse's
		// text blocks, each of which would keep its block.
		{`<out><xsl:value-of select="string-length(string-join(//rec/@k, ','))"/></out>`, 0.27},
		// The document built off the stream is the snapshot of its
		// element: one copy, where the tree holds its document and the
		// snapshot (audit A2-7: the built document and a snapshot of it).
		{`<out><xsl:value-of select="count(snapshot(/*)//sub)"/></out>`, 0.9},
	} {
		sheet := coreSheet(c.body)
		var st streamtrace.Counts
		core := splitPeak(func() { _, _, st = coreRun(sheet, dir, true, TransformOptions{}) })
		tree := splitPeak(func() { coreRun(sheet, dir, false, TransformOptions{}) })
		t.Logf("%s: core %.1f MB, tree %.1f MB", c.body, float64(core)/1e6, float64(tree)/1e6)
		if st.Runs[streamtrace.Core] != 1 || st.Reruns != 0 {
			t.Errorf("%s: trace %+v", c.body, st)
		}
		// Under -race the instrumentation's own heap moves the ratios (the
		// string-join row read 27.6% against its 27% bound, base and head
		// alike): the figures are logged, the bounds held without it.
		if float64(core) > c.frac*float64(tree) && !raceEnabled {
			t.Errorf("%s: peak live heap %.1f MB, tree %.1f MB (bound %.0f%%)", c.body,
				float64(core)/1e6, float64(tree)/1e6, 100*c.frac)
		}
	}
}

// TestStreamCoreLimits: under tight result budgets, streamed loops succeed or
// fail as the burst path did before M1.6 retired it into the core (its
// results at 7ee0ebb9, pinned: the error text, or the output's SHA-256
// prefix), the temporaries a loop item builds handed back per item and the
// output it writes kept.
func TestStreamCoreLimits(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 3000 {
		fmt.Fprintf(&b, `<rec k="K%05d" v="%d"><a>t</a></rec>`, i, i%97)
	}
	b.WriteString("</doc>")
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	bodies := []string{
		// Temporaries only: handed back per item.
		`<xsl:for-each select="/doc/rec"><xsl:variable name="t"><x><xsl:value-of select="@k"/></x></xsl:variable>` +
			`<xsl:if test="string($t) = 'never'"><hit/></xsl:if></xsl:for-each>`,
		// The same, the body reading its match (matchBody).
		`<xsl:for-each select="/doc/rec"><xsl:variable name="t"><x><xsl:value-of select="."/></x></xsl:variable>` +
			`<xsl:if test="string($t) = 'never'"><hit/></xsl:if></xsl:for-each>`,
		// Output per item: kept.
		`<xsl:for-each select="/doc/rec"><r k="{@k}"/></xsl:for-each>`,
		// Carried state.
		`<xsl:iterate select="/doc/rec"><xsl:param name="t" select="0"/><xsl:on-completion><t v="{$t}"/></xsl:on-completion>` +
			`<xsl:next-iteration><xsl:with-param name="t" select="$t + 1"/></xsl:next-iteration></xsl:iterate>`,
	}
	limits := []TransformOptions{{MaxNodes: 200}, {MaxNodes: 4000}, {MaxBytes: 2000}, {MaxBytes: 40000},
		{MaxNodes: 4000, MaxNodesBuilt: 9000}, {MaxBytes: 40000, MaxBytesBuilt: 90000}}
	// The burst path's results, by body then limit.
	want := []struct{ err, sha string }{
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"", "d58e6668c5d00778"},
		{"XPDY0130: evaluation constructed more than 200 result-tree nodes; the transformation is building a tree too large to hold: processor resource limit exceeded", "e3b0c44298fc1c14"},
		{"XPDY0130: evaluation constructed more than 4000 result-tree nodes; the transformation is building a tree too large to hold: processor resource limit exceeded", "e3b0c44298fc1c14"},
		{"XPDY0130: evaluation built more than 2000 bytes of string content; the expression is building a string too large to hold: processor resource limit exceeded", "e3b0c44298fc1c14"},
		{"", "cc412448ee8b0b06"},
		{"XPDY0130: evaluation constructed more than 4000 result-tree nodes; the transformation is building a tree too large to hold: processor resource limit exceeded", "e3b0c44298fc1c14"},
		{"", "cc412448ee8b0b06"},
		{"", "a972ffcac06d8df5"},
		{"", "a972ffcac06d8df5"},
		{"", "a972ffcac06d8df5"},
		{"", "a972ffcac06d8df5"},
		{"", "a972ffcac06d8df5"},
		{"", "a972ffcac06d8df5"},
	}
	for bi, body := range bodies {
		sheet := coreSheet(body)
		for li, o := range limits {
			co, ce, ct := coreRun(sheet, dir, true, o)
			where := fmt.Sprintf("%s, nodes %d bytes %d built %d/%d", body, o.MaxNodes, o.MaxBytes, o.MaxNodesBuilt, o.MaxBytesBuilt)
			if ct.Runs[streamtrace.Core] == 0 {
				t.Errorf("%s: %+v: must stream", where, ct)
			}
			w := want[bi*len(limits)+li]
			if sum := fmt.Sprintf("%x", sha256.Sum256([]byte(co)))[:16]; ce != w.err || sum != w.sha {
				t.Errorf("%s:\ngot  %q %s %.200q\nwant %q %s", where, ce, sum, co, w.err, w.sha)
			}
		}
	}
}

// A streamed xsl:source-document or xsl:merge that fails because the
// context was cancelled returns the context's error: it is not run again on
// a tree, which would parse the whole document first (audit A2-4). The
// context is cancelled from inside the stream, by environment-variable().
func TestCancelledStreamIsNotRerun(t *testing.T) {
	dir := t.TempDir()
	var doc strings.Builder
	doc.WriteString("<doc>")
	for i := range 20000 {
		fmt.Fprintf(&doc, `<rec v="%06d"/>`, i)
	}
	doc.WriteString("</doc>")
	if err := os.WriteFile(filepath.Join(dir, "d.xml"), []byte(doc.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{
		"source-document": `<xsl:source-document streamable="yes" href="d.xml">` +
			`<xsl:for-each select="doc/rec"><xsl:value-of select="environment-variable('x')"/></xsl:for-each></xsl:source-document>`,
		"merge": `<xsl:merge><xsl:merge-source for-each-source="'d.xml'" streamable="yes" select="doc/rec">` +
			`<xsl:merge-key select="string(@v)"/></xsl:merge-source>` +
			`<xsl:merge-action><xsl:value-of select="environment-variable('x')"/></xsl:merge-action></xsl:merge>`,
	}
	for name, body := range bodies {
		docs, err := NewFileResolver(dir) // a tree rerun would cache the document
		if err != nil {
			t.Fatal(err)
		}
		src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
			`<xsl:template name="main">` + body + `</xsl:template></xsl:stylesheet>`
		tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(tree.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		before := streamtrace.Snapshot()
		_, err = s.Transform(ctx, nil, TransformOptions{InitialTemplate: "main", Documents: docs, Environment: cancelEnv(cancel)})
		tr := streamtrace.Snapshot().Sub(before)
		cancel()
		if err != context.Canceled || tr.Reruns != 0 {
			t.Errorf("%s: err = %v, reruns %d; want context.Canceled, no rerun", name, err, tr.Reruns)
		}
	}
}

// cancelEnv cancels at the first environment-variable() call.
type cancelEnv func()

func (c cancelEnv) LookupEnvironment(string) (string, bool) { c(); return "", false }
func (c cancelEnv) EnvironmentNames() []string              { return nil }
