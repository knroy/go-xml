package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// copyAccumRun runs body (declarations after the accumulator n, which counts
// rec) over doc on the tree and streamed, and returns both results (or
// errors) and whether TransformReader streamed.
func copyAccumRun(t *testing.T, body, doc string) (tree, stream string, streamed bool) {
	t.Helper()
	tr, err := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
		`<xsl:accumulator name="n" initial-value="0" streamable="yes"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>`+
		body+`</xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tr.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	str := func(r *Result, err error) string {
		if err != nil {
			return "ERROR " + err.Error()
		}
		return r.String()
	}
	src, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tree = str(s.Transform(context.Background(), src.Root, TransformOptions{}))
	before := streamtrace.Snapshot()
	stream = str(s.TransformReader(context.Background(), strings.NewReader(doc), TransformOptions{}))
	return tree, stream, streamtrace.Snapshot().Sub(before).Streamed() > 0
}

// TestStreamCopyAccumulatorValues: §18.2.10 gives a copy made by fn:copy-of
// or fn:snapshot its original's accumulator values. Each row reads them on
// a copy of a streamed node, made by a different path of the stream; the
// stream must answer as the tree does (audit A2-1: the copy built off the
// cursor for copy-of(.) answered with its own values).
func TestStreamCopyAccumulatorValues(t *testing.T) {
	const doc = `<doc><rec><x/></rec><rec><x/><x/></rec><rec/></doc>`
	const read = `<xsl:template match="rec|x" mode="c"><r n="{accumulator-before('n')}" a="{accumulator-after('n')}"><xsl:apply-templates mode="c"/></r></xsl:template>`
	rows := []struct {
		name, body string
		stream     bool // the stream must stream it
	}{
		// copy-of(.) of a match applied in another mode (docCopyEval, buildElem).
		{"apply-copy-of", `<xsl:mode streamable="yes" use-accumulators="n"/><xsl:mode name="c" use-accumulators="n"/>
		  <xsl:template match="/doc"><o><xsl:apply-templates select="rec"/></o></xsl:template>
		  <xsl:template match="rec"><xsl:apply-templates select="copy-of(.)" mode="c"/></xsl:template>` + read, true},
		// The same with copy-of() (no argument).
		{"apply-copy-of-noarg", `<xsl:mode streamable="yes" use-accumulators="n"/><xsl:mode name="c" use-accumulators="n"/>
		  <xsl:template match="/doc"><o><xsl:apply-templates select="rec"/></o></xsl:template>
		  <xsl:template match="rec"><xsl:apply-templates select="copy-of()" mode="c"/></xsl:template>` + read, true},
		// No accumulator applicable to the streamed document: the copy's
		// read is XTDE3362 in the tree.
		{"apply-copy-of-not-applicable", `<xsl:mode streamable="yes"/><xsl:mode name="c" use-accumulators="n"/>
		  <xsl:template match="/doc"><o><xsl:apply-templates select="rec"/></o></xsl:template>
		  <xsl:template match="rec"><xsl:apply-templates select="copy-of(.)" mode="c"/></xsl:template>` + read, false},
		// copy-of(.) of the document node applied in another mode (buildDoc).
		{"apply-copy-of-doc", `<xsl:mode streamable="yes" use-accumulators="n"/><xsl:mode name="c" use-accumulators="n"/>
		  <xsl:template match="/"><o><xsl:apply-templates select="copy-of(.)/doc/rec" mode="c"/></o></xsl:template>` + read, false},
		{"apply-copy-of-doc-whole", `<xsl:mode streamable="yes" use-accumulators="n"/><xsl:mode name="c" use-accumulators="n"/>
		  <xsl:template match="/"><o><xsl:apply-templates select="copy-of(.)" mode="c"/></o></xsl:template>
		  <xsl:template match="/|doc" mode="c"><xsl:apply-templates mode="c"/></xsl:template>` + read, false},
		{"apply-copy-of-doc-not-applicable", `<xsl:mode streamable="yes"/><xsl:mode name="c" use-accumulators="n"/>
		  <xsl:template match="/"><o><xsl:apply-templates select="copy-of(.)" mode="c"/></o></xsl:template>
		  <xsl:template match="/|doc" mode="c"><xsl:apply-templates mode="c"/></xsl:template>` + read, false},
		// xsl:copy-of copy-accumulators="yes" into a variable, of a match and
		// of the document.
		{"copy-of-instr-variable", `<xsl:mode streamable="yes" use-accumulators="n"/>
		  <xsl:template match="/doc"><o><xsl:apply-templates select="rec"/></o></xsl:template>
		  <xsl:template match="rec"><xsl:variable name="v"><xsl:copy-of select="." copy-accumulators="yes"/></xsl:variable><r n="{$v/rec/accumulator-before('n')}" a="{$v/rec/accumulator-after('n')}"/></xsl:template>`, false},
		{"copy-of-instr-variable-doc", `<xsl:mode streamable="yes" use-accumulators="n"/>
		  <xsl:template match="/"><xsl:variable name="v"><xsl:copy-of select="." copy-accumulators="yes"/></xsl:variable><o><xsl:value-of select="$v//rec ! accumulator-after('n')"/></o></xsl:template>`, false},
		// A whole-document copy read by a select (streamcopy_whole.go).
		{"whole-doc-select", `<xsl:mode streamable="yes" use-accumulators="n"/>
		  <xsl:template match="/"><o><xsl:value-of select="copy-of(.)//rec ! accumulator-after('n')"/></o></xsl:template>`, false},
		// A variable bound to copy-of(.), and snapshot(.), per record.
		{"variable-copy-of", `<xsl:mode streamable="yes" use-accumulators="n"/>
		  <xsl:template match="/doc"><o><xsl:apply-templates select="rec"/></o></xsl:template>
		  <xsl:template match="rec"><xsl:variable name="c" select="copy-of(.)"/><r n="{$c/accumulator-before('n')}" a="{$c/accumulator-after('n')}"/></xsl:template>`, false},
		{"snapshot", `<xsl:mode streamable="yes" use-accumulators="n"/>
		  <xsl:template match="/doc"><o><xsl:apply-templates select="rec"/></o></xsl:template>
		  <xsl:template match="rec"><xsl:apply-templates select="snapshot(.)" mode="c"/></xsl:template>` + read, false},
	}
	for _, r := range rows {
		tree, stream, streamed := copyAccumRun(t, r.body, doc)
		if stream != tree {
			t.Errorf("%s (streamed %v):\nstream: %s\ntree:   %s", r.name, streamed, stream, tree)
		}
		if r.stream && !streamed {
			t.Errorf("%s: did not stream", r.name)
		}
		t.Logf("%s: streamed %v: %s", r.name, streamed, tree)
	}
}
