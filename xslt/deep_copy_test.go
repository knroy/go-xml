package xslt

import (
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// deepDoc is d nested elements around a leaf; flatDoc is d sibling elements
// under one: the same nodes, one deep and one shallow.
func deepDoc(d int) string { return strings.Repeat("<a>", d) + "x" + strings.Repeat("</a>", d) }
func flatDoc(d int) string { return "<r>" + strings.Repeat("<a>x</a>", d-1) + "</r>" }

// streamedRun is outRun through TransformReader, failing unless the run was
// streamed with no fallback.
func streamedRun(t *testing.T, sheet *Stylesheet, doc string, opts TransformOptions) string {
	t.Helper()
	before := streamtrace.Snapshot()
	out, err := outRun(sheet, doc, true, false, opts)
	if err != nil {
		t.Fatal(err)
	}
	if c := streamtrace.Snapshot().Sub(before); c.Streamed() == 0 || c.Fallbacks != 0 {
		t.Errorf("not streamed: %+v", c)
	}
	return out
}

// TestDeepCopyOfIsLinear: copy-of of a deep document did work per node in
// proportion to its depth, in both modes -- the tree's copy asked each node
// for its base URI, a walk up to the document, and the stream's asked each
// element for its in-scope namespaces, a walk up its ancestors -- so 200 000
// levels did not finish in two minutes. Each now passes its parent's down.
// The deep copy must cost about what a flat one of as many nodes does
// (a quadratic copy is thousands of times slower at this depth), and agree
// across the modes.
func TestDeepCopyOfIsLinear(t *testing.T) {
	const d = 100000
	sheet := outSheet(t, `<xsl:mode streamable="yes"/>`, `<xsl:template match="/"><xsl:copy-of select="."/></xsl:template>`)
	var opts TransformOptions
	opts.SourceParseOptions.MaxDepth = 2 * d
	opts.MaxNodes, opts.MaxBytes = -1, -1
	run := func(doc string, main bool) (string, time.Duration) {
		t0 := time.Now()
		if main {
			return streamedRun(t, sheet, doc, opts), time.Since(t0)
		}
		out, err := outRun(sheet, doc, main, false, opts)
		if err != nil {
			t.Fatal(err)
		}
		return out, time.Since(t0)
	}
	var outs [2]string
	for i, main := range []bool{false, true} {
		_, flat := run(flatDoc(d), main)
		var deep time.Duration
		outs[i], deep = run(deepDoc(d), main)
		if deep > 20*flat && deep > 2*time.Second {
			t.Errorf("main %v: copy-of at depth %d took %v, a flat one of as many nodes %v", main, d, deep, flat)
		}
	}
	if outs[0] != outs[1] {
		t.Errorf("streamed copy differs from the tree's (%d vs %d bytes)", len(outs[1]), len(outs[0]))
	}
}

// TestDeepCopyOfScopes: the streamed copy works each element's in-scope
// namespaces out from its parent's and its own declarations; redeclared,
// undeclared and shadowed bindings at every level must come out as the
// tree's copy writes them.
func TestDeepCopyOfScopes(t *testing.T) {
	doc := `<a xmlns="urn:d" xmlns:p="urn:p"><b xmlns:q="urn:q"><p:c xmlns:p="urn:p2"><d xmlns=""><e xmlns:q="urn:q" xmlns:r="urn:r"><f/></e></d></p:c><g xmlns="urn:d"/></b><h/></a>`
	for _, body := range []string{
		`<xsl:template match="/"><xsl:copy-of select="."/></xsl:template>`,
		`<xsl:template match="/"><xsl:copy-of select="*/*"/></xsl:template>`,
		`<xsl:template match="*:c"><xsl:copy-of select="."/></xsl:template>`,
	} {
		sheet := outSheet(t, `<xsl:mode streamable="yes" on-no-match="shallow-skip"/>`, body)
		want, err := outRun(sheet, doc, false, false, TransformOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := streamedRun(t, sheet, doc, TransformOptions{}); got != want {
			t.Errorf("%s:\n got %s\nwant %s", body, got, want)
		}
	}
}

// TestDeepStreamHoldsNoMoreThanTree: a streamed run kept, per open element,
// besides the parse's record, a mark, a gen and two ordinals in the parse,
// and an entry of the select's walk (112 bytes for count(//a)): a document a
// million deep held 2.7 times the tree. Those are runs now, one for a whole
// stretch of evenly nested elements. What the streamed run allocates must
// stay within a tenth of what the tree's parse and run allocate: both are
// then the parse's records, give or take what the process has cached (it was
// five times the tree's for string(.), three for count(//a)).
func TestDeepStreamHoldsNoMoreThanTree(t *testing.T) {
	const d = 200000
	var opts TransformOptions
	opts.SourceParseOptions.MaxDepth = 2 * d
	opts.MaxNodes, opts.MaxBytes = -1, -1
	doc := deepDoc(d)
	for _, body := range []string{
		`<xsl:template match="/"><xsl:value-of select="string(.)"/></xsl:template>`,
		`<xsl:template match="/"><xsl:value-of select="count(//a)"/></xsl:template>`,
	} {
		sheet := outSheet(t, `<xsl:mode streamable="yes"/>`, body)
		alloc := func(main bool) (string, uint64) {
			var m goruntime.MemStats
			goruntime.ReadMemStats(&m)
			before := m.TotalAlloc
			var out string
			if main {
				out = streamedRun(t, sheet, doc, opts)
			} else {
				var err error
				if out, err = outRun(sheet, doc, false, false, opts); err != nil {
					t.Fatal(err)
				}
			}
			goruntime.ReadMemStats(&m)
			return out, m.TotalAlloc - before
		}
		want, tree := alloc(false)
		got, stream := alloc(true)
		t.Logf("%s: stream %d KB, tree %d KB", body, stream>>10, tree>>10)
		if got != want {
			t.Errorf("%s: streamed %q, tree %q", body, got, want)
		}
		if stream > tree+tree/10 {
			t.Errorf("%s: the streamed run allocated %d KB at depth %d, the tree's %d KB", body, stream>>10, d, tree>>10)
		}
	}
}
