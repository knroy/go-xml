package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamGroupCopiesParam: a parameter copy-of(current-group()) of an
// xsl:apply-templates over a tree not of the stream, in a streamed group,
// is read at the instruction from the group's members, one group's copies
// held at a time (si-group-040); TransformReader streams it with the
// tree's result.
func TestStreamGroupCopiesParam(t *testing.T) {
	const tail = `<xsl:template match="d"><xsl:param name="c" tunnel="yes"/><c n="{count($c)}"><xsl:copy-of select="$c"/></c></xsl:template>`
	const param = `<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:param name="t"><r><d/></r></xsl:param>`
	mm5Rows(t, mm5Doc, []struct{ name, decls string }{
		{"adjacent chunks", param + `<xsl:template match="/"><o><xsl:for-each-group select="d/*" group-adjacent="(position() - 1) idiv 2">` +
			`<xsl:apply-templates select="$t/*"><xsl:with-param name="c" select="copy-of(current-group())" tunnel="yes"/></xsl:apply-templates>` +
			`</xsl:for-each-group></o></xsl:template>` + tail},
		{"the document element's children, starting-with", param + `<xsl:template match="/*"><o><xsl:for-each-group select="*" group-starting-with="rec[@k = 'a']">` +
			`<g k="{current-grouping-key()}"/><xsl:apply-templates select="$t/*"><xsl:with-param name="c" select="copy-of(current-group())" tunnel="yes"/></xsl:apply-templates>` +
			`</xsl:for-each-group></o></xsl:template>` + tail},
	})
}

// TestStreamLoopCopiesSized: an xsl:for-each over copies of the stream's
// matches whose body reads last() collects the copies before the body
// runs, as the tree evaluates the select whole (si-group-065:
// current-group()!copy-of(), a group of the document element's children
// too); P!copy-of() is copy-of(P).
func TestStreamLoopCopiesSized(t *testing.T) {
	const mode = `<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`
	const body = `<i n="{name()}" p="{position()}" l="{last()}"><xsl:copy-of select="node()"/></i>`
	mm5Rows(t, mm5Doc, []struct{ name, decls string }{
		{"group of the document element's children", mode + `<xsl:template match="/*"><o><xsl:for-each-group select="*" group-adjacent="name()">` +
			`<g><xsl:for-each select="current-group()!copy-of()">` + body + `</xsl:for-each></g></xsl:for-each-group></o></xsl:template>`},
		{"copy-of of the group", mode + `<xsl:template match="/*"><o><xsl:for-each-group select="*" group-starting-with="x">` +
			`<g><xsl:for-each select="copy-of(current-group())">` + body + `</xsl:for-each></g></xsl:for-each-group></o></xsl:template>`},
		{"a path's copies", mode + `<xsl:template match="/"><o><xsl:for-each select="d/rec!copy-of()">` + body + `</xsl:for-each></o></xsl:template>`},
		{"an error after last()", mode + `<xsl:template match="/"><o><xsl:for-each select="d/rec/copy-of()">` +
			`<i l="{last()}" v="{xs:integer(@k)}"/></xsl:for-each></o></xsl:template>`},
	})
}

// TestStreamSnapshotClimb: a path from snapshots of the stream's matches
// climbs into the snapshots' own ancestors, copied with their attributes
// and namespaces (sf-snapshot-0101c): the split grounds the matches, the
// outer makes the snapshots and runs the steps on the tree's code.
func TestStreamSnapshotClimb(t *testing.T) {
	dir := t.TempDir()
	doc := `<b:r xmlns:b="urn:b" xmlns:g="urn:g" g:id="R"><x a="1"><g:id c="s">one</g:id><y/></x><g:id c="t">two<z/></g:id><x><x><g:id/></x></x></b:r>`
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{
		`<xsl:sequence select="snapshot(//*:id)/self::*:id/ancestor::node()[last()]"/>`,
		`<xsl:sequence select="snapshot(//*:id)/ancestor::*[1]"/>`,
		`<n><xsl:value-of select="count(snapshot(//*:id)/ancestor-or-self::node())"/></n>`,
		`<xsl:copy-of select="snapshot(/*/*)/../@*"/>`,
		`<xsl:sequence select="snapshot(//x)/parent::*[@a = 1]"/>`,
		`<n><xsl:value-of select="snapshot(//*:id)/ancestor::node()[last()] ! (document-uri(.), base-uri(.))"/></n>`,
		`<n><xsl:value-of select="count(snapshot(//*:id)/ancestor::*/namespace::*)"/></n>`,
	} {
		coreCheck(t, coreSheet(body), dir, fmt.Sprint("row ", i), true, TransformOptions{})
	}
}

// TestStreamInstrBodyBurst: a call in a tail on a function whose body is
// instructions, given the match itself, runs on the match's grounded copy
// when the body sets no focus and reads its parameters only downward
// (function-5012); one climbing from its parameter or from the focus an
// xsl:for-each sets leaves the rule on the tree.
func TestStreamInstrBodyBurst(t *testing.T) {
	const head = `<xsl:mode streamable="yes"/><xsl:template match="/">`
	const fn = `<xsl:function name="p:f" as="node()*" streamability="absorbing"><xsl:param name="n"/>`
	rows := []struct {
		name, decls string
		stream      bool
	}{
		{"absorbing", head + `<o><xsl:copy-of select="(d/rec/p:f(.)[self::new]/v)[1]"/></o></xsl:template>` +
			fn + `<new><xsl:copy-of select="$n/v"/></new></xsl:function>`, true},
		{"choose and value-of", head + `<o><xsl:copy-of select="d/rec/p:f(.)/self::*"/></o></xsl:template>` +
			fn + `<xsl:choose><xsl:when test="true()"><w><xsl:value-of select="$n//text()"/></w></xsl:when>` +
			`<xsl:otherwise><xsl:element name="e"/></xsl:otherwise></xsl:choose></xsl:function>`, true},
		{"climbs", head + `<o><xsl:copy-of select="d/rec/p:f(.)/self::*"/></o></xsl:template>` +
			fn + `<u><xsl:value-of select="$n/../@k"/></u></xsl:function>`, false},
		{"climbs from its focus", head + `<o><xsl:copy-of select="d/rec/p:f(.)/self::*"/></o></xsl:template>` +
			fn + `<xsl:for-each select="$n/v"><u><xsl:value-of select="name(..)"/></u></xsl:for-each></xsl:function>`, false},
	}
	for _, r := range rows {
		sheet := mainSheet(r.decls)
		so, st := mainRun(sheet, mm5Doc, true, TransformOptions{})
		to, _ := mainRun(sheet, mm5Doc, false, TransformOptions{})
		if so != to {
			t.Errorf("%s:\nstreamed %s\ntree     %s", r.name, so, to)
		}
		if got := st.Runs[streamtrace.Main] != 0; got != r.stream {
			t.Errorf("%s: streamed %v, want %v", r.name, got, r.stream)
		}
	}
}

// TestStreamOneMatchTail: outermost(D/T), D the document element, is
// D/outermost(T), T a tail run on D's grounded copy; a call there on a
// function whose body is an xsl:for-each over its node() parameter raises
// the tree's type error when the argument is absent (function-5006).
func TestStreamOneMatchTail(t *testing.T) {
	const decls = `<xsl:mode streamable="yes"/><xsl:template match="/"><o>` +
		`<xsl:for-each select="outermost(d/p:f(s[1]))/text()"><xsl:copy-of select="self::text()"/></xsl:for-each></o></xsl:template>` +
		`<xsl:function name="p:f" as="node()*" streamability="deep-descent"><xsl:param name="n" as="node()"/>` +
		`<xsl:for-each select="$n"><xsl:sequence select="self::node()//s//p"/></xsl:for-each></xsl:function>`
	rows := []struct{ name, decls string }{{"deep descent", decls}}
	mm5Rows(t, `<d><s><s><p>a<p>in</p></p><p>b</p></s></s><s><s><p>c</p></s></s></d>`, rows)
	mm5Rows(t, `<d><x/></d>`, rows)
}

// TestStreamDynCallCopies: a dynamic call given only copies of the stream
// and values, its function item a variable's or a tunnel parameter's whatever
// it holds, streams in a rule that may match the document element; and
// snapshot(@a) applied to a mode that is not streamable (use-package-152).
// A call given the match itself does not stream.
func TestStreamDynCallCopies(t *testing.T) {
	const doc = `<d k="0"><rec k="a" v="1">t1</rec><rec k="b"><v>x<s/></v></rec><x k="z"/></d>`
	const fn = `<xsl:variable name="f" select="function($e) { string($e/@k) || '/' || count($e//*) || '/' || name($e/..) }"/>`
	rows := []struct {
		name, decls string
		stream      bool
	}{
		{"snapshot", `<xsl:mode streamable="yes"/>` + fn + `<xsl:template match="*"><r><xsl:value-of select="$f(snapshot(.))"/></r></xsl:template>`, true},
		{"snapshot below", `<xsl:mode streamable="yes" on-no-match="shallow-copy"/>` + fn + `<xsl:template match="rec"><r><xsl:value-of select="$f(snapshot(.))"/></r></xsl:template>`, true},
		{"copy-of and a value", `<xsl:mode streamable="yes"/><xsl:variable name="g" select="function($e, $s) { $s || count($e//*) || name($e/..) }"/>` +
			`<xsl:template match="*"><r><xsl:value-of select="$g(copy-of(.), 'n')"/></r></xsl:template>`, true},
		{"tunnel from a map, never called", `<xsl:mode streamable="yes"/><xsl:mode name="k" streamable="no" on-no-match="fail"/><xsl:variable name="o" as="map(*)" select="map{}"/>` +
			`<xsl:template match="/"><o><xsl:apply-templates><xsl:with-param name="f" select="$o('f')" tunnel="yes"/></xsl:apply-templates></o></xsl:template>` +
			`<xsl:template match="*"><xsl:param name="f" as="(function(element()) as xs:string)?" tunnel="yes"/>` +
			`<xsl:choose><xsl:when test="exists($f)"><xsl:value-of select="$f(snapshot(.))"/></xsl:when>` +
			`<xsl:otherwise><r><xsl:apply-templates select="snapshot(@k)" mode="k"/></r></xsl:otherwise></xsl:choose></xsl:template>` +
			`<xsl:template match="*[@k != 'z']/@k" mode="k"><a v="{.}" p="{name(..)}" n="{count(../*)}"/></xsl:template>`, true},
		{"tunnel from a map, called", `<xsl:mode streamable="yes"/><xsl:variable name="o" as="map(*)" select="map{'f': function($e) { name($e) || count($e//node()) }}"/>` +
			`<xsl:template match="/"><o><xsl:apply-templates><xsl:with-param name="f" select="$o('f')" tunnel="yes"/></xsl:apply-templates></o></xsl:template>` +
			`<xsl:template match="*"><xsl:param name="f" as="(function(element()) as xs:string)?" tunnel="yes"/>` +
			`<xsl:if test="exists($f)"><xsl:value-of select="$f(snapshot(.))"/></xsl:if></xsl:template>`, true},
		{"the match itself", `<xsl:mode streamable="yes"/>` + fn + `<xsl:template match="*"><r><xsl:value-of select="$f(.)"/></r></xsl:template>`, false},
	}
	for _, r := range rows {
		sheet := mainSheet(r.decls)
		so, st := mainRun(sheet, doc, true, TransformOptions{})
		to, _ := mainRun(sheet, doc, false, TransformOptions{})
		if so != to {
			t.Errorf("%s:\nstreamed %s\ntree     %s", r.name, so, to)
		}
		if got := st.Runs[streamtrace.Main] != 0; got != r.stream {
			t.Errorf("%s: streamed %v, want %v", r.name, got, r.stream)
		}
	}
}
