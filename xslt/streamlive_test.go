package xslt

import (
	"context"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// liveOrders is a document of n records, four elements, four attributes and
// four text nodes each, in a default namespace.
func liveOrders(n int) string {
	var b strings.Builder
	b.WriteString(`<orders xmlns="urn:orders" source="gen">` + "\n")
	for i := range n {
		fmt.Fprintf(&b, `  <order id="o%d" v="%d"><customer>C %d</customer><item sku="s%d" qty="%d">W</item><note>L</note></order>`+"\n",
			i, i%97, i%1000, i%50, i%7+1)
	}
	b.WriteString("</orders>\n")
	return b.String()
}

// TestStreamLiveBuiltIn: the streamed identity transform (TransformReader,
// on-no-match="shallow-copy", no rules) runs the built-in rule on the
// parse's records, with no view per element, and writes the tree's output.
// Measured at the change: 1,384 bytes allocated per record; with a view per
// element, 1,942.
func TestStreamLiveBuiltIn(t *testing.T) {
	doc := liveOrders(3000)
	sheet := mainSheet(`<xsl:mode streamable="yes" on-no-match="shallow-copy"/>`)
	st, tr := mainRun(sheet, doc, true, TransformOptions{})
	if want, _ := mainRun(sheet, doc, false, TransformOptions{}); st != want || tr.Runs[streamtrace.Main] != 1 {
		t.Fatalf("streamed (%v runs) differs from the tree", tr.Runs)
	}
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	best := uint64(1 << 62)
	for range 3 {
		var m0, m1 goruntime.MemStats
		goruntime.ReadMemStats(&m0)
		if _, err := s.TransformReader(context.Background(), strings.NewReader(doc), TransformOptions{Output: io.Discard}); err != nil {
			t.Fatal(err)
		}
		goruntime.ReadMemStats(&m1)
		best = min(best, m1.TotalAlloc-m0.TotalAlloc)
	}
	t.Logf("%d bytes per record", best/3000)
	if per := best / 3000; per > 1700 {
		t.Errorf("%d bytes allocated per record, want at most 1,700 (a view per element: about 1,940)", per)
	}
}

// TestStreamDispatchAllocs: the streamed two-instruction identity transform
// (a rule for @*|node(): xsl:copy, apply-templates to @*, then to the
// children) allocates per record at most 0.95x what the tree's run of it
// does: an apply op's feed and a children's filter are made once per run,
// not per element, and a node a rule matches is given one runtime, its
// focus and selection in one copy (dispatchAt). Measured: 52.2 against the
// tree's 61.1; a runtime to match in besides made it 64.2, and 76.2 with
// a second per leaf, 88.2 with a feed and a filter per element.
func TestStreamDispatchAllocs(t *testing.T) {
	doc := liveOrders(3000)
	sheet := mainSheet(`<xsl:mode streamable="yes"/><xsl:template match="@*|node()">` +
		`<xsl:copy><xsl:apply-templates select="@*"/><xsl:apply-templates/></xsl:copy></xsl:template>`)
	if st, tr := mainRun(sheet, doc, true, TransformOptions{}); tr.Runs[streamtrace.Main] != 1 {
		t.Fatalf("not streamed: %v", tr.Runs)
	} else if want, _ := mainRun(sheet, doc, false, TransformOptions{}); st != want {
		t.Fatal("streamed differs from the tree")
	}
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opts := TransformOptions{Output: io.Discard}
	streamed := testing.AllocsPerRun(2, func() {
		if _, err := s.TransformReader(context.Background(), strings.NewReader(doc), opts); err != nil {
			t.Fatal(err)
		}
	})
	treeRun := testing.AllocsPerRun(2, func() {
		d, err := xdm.Parse(strings.NewReader(doc), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transform(context.Background(), d.Root, opts); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("allocations per record: streamed %.1f, tree %.1f", streamed/3000, treeRun/3000)
	if streamed > 0.95*treeRun {
		t.Errorf("streamed %.0f allocations, tree %.0f: more than 0.95x", streamed, treeRun)
	}
}

// TestStreamDispatchWarnings: the streamed dispatch raises the tree's mode
// warnings, in its order: no rule for an element (on the parse's record or
// a view), an attribute or a text node, and two rules for one node.
func TestStreamDispatchWarnings(t *testing.T) {
	doc := `<doc a="1"><rec k="a">t<sub/></rec><rec>u</rec></doc>`
	for _, decls := range []string{
		`<xsl:mode streamable="yes" on-no-match="shallow-copy" warning-on-no-match="yes"/>`,
		`<xsl:mode streamable="yes" on-no-match="shallow-copy" warning-on-no-match="yes" warning-on-multiple-match="yes"/>` +
			`<xsl:template match="rec"><r><xsl:apply-templates select="@*"/><xsl:apply-templates/></r></xsl:template>` +
			`<xsl:template match="*[@k]"><k/></xsl:template><xsl:template match="text()[. = 'u']"><u/></xsl:template>`,
	} {
		sheet := mainSheet(decls)
		tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(tree.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		before := streamtrace.Snapshot()
		st, err := s.TransformReader(context.Background(), strings.NewReader(doc), TransformOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if tr := streamtrace.Snapshot().Sub(before); tr.Runs[streamtrace.Main] != 1 {
			t.Fatalf("%s: not streamed", decls)
		}
		d, _ := xdm.Parse(strings.NewReader(doc), xdm.ParseOptions{})
		want, err := s.Transform(context.Background(), d.Root, TransformOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if g, w := strings.Join(st.Warnings, "\n"), strings.Join(want.Warnings, "\n"); g != w || st.String() != want.String() {
			t.Errorf("%s:\nstreamed %s\n%s\ntree     %s\n%s", decls, st, g, want, w)
		}
		if len(want.Warnings) == 0 {
			t.Errorf("%s: no warnings", decls)
		}
	}
}
