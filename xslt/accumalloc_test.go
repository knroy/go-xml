package xslt

import (
	"context"
	gort "runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// accumBytesPerNode is the bytes the whole transform allocates per source
// node for a scalar accumulator over n elements.
func accumBytesPerNode(t *testing.T, n int) float64 {
	const sheet = `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output method="text"/>
		<xsl:mode use-accumulators="a"/>
		<xsl:accumulator name="a" initial-value="0">
		  <xsl:accumulator-rule match="e" select="$value + 1"/>
		</xsl:accumulator>
		<xsl:template match="/"><xsl:value-of
		  select="r/e[last()]/accumulator-after('a')"/></xsl:template>
		</xsl:stylesheet>`
	st, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compile(st.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := xdm.ParseString("<r>"+strings.Repeat("<e>t</e>", n)+"</r>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var m0, m1 gort.MemStats
	gort.GC()
	gort.ReadMemStats(&m0)
	res, err := c.Transform(context.Background(), src.Root, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gort.ReadMemStats(&m1)
	var b strings.Builder
	if err := res.Serialize(&b); err != nil || b.String() != strconv.Itoa(n) {
		t.Fatalf("got %q, %v", b.String(), err)
	}
	return float64(m1.TotalAlloc-m0.TotalAlloc) / float64(src.Len())
}

// The accumulator tables are slices sized once from the source tree, not maps:
// the whole transform costs about 445 bytes a node, against about 605 with
// maps. The bound sits between them.
func TestAccumulatorAllocPerNode(t *testing.T) {
	if got := accumBytesPerNode(t, 50000); got > 520 {
		t.Fatalf("%.0f bytes allocated per source node, want under 520", got)
	}
}
