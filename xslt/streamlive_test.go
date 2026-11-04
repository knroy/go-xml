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
// children) allocates within 1.3x of the tree's run of it per record: an
// apply op's feed and a children's filter are made once per run, not per
// element. Measured at the change: 76.2 against the tree's 61.1; a feed
// and a filter per element made it 88.2.
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
	if streamed > 1.3*treeRun {
		t.Errorf("streamed %.0f allocations, tree %.0f: more than 1.3x", streamed, treeRun)
	}
}
