package xslt

import (
	"strings"
	"testing"
)

const accumFragmentSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:accumulator name="c" initial-value="0">
    <xsl:accumulator-rule match="x" select="$value + 1"/>
  </xsl:accumulator>
  <xsl:template match="/">
    <xsl:variable name="a" as="element()"><r><x/><x/><x/></r></xsl:variable>
    <xsl:variable name="b" as="element()"><r>BKIDS</r></xsl:variable>
    <out><xsl:value-of select="READ"/></out>
  </xsl:template>
</xsl:stylesheet>`

// Two constructed elements live in two fragments, which Node.Tree reports as
// nil alike: a cache keyed on the tree gave both one table, so $b read $a's
// value at the same index, or XTDE3400 past the end of $a's table.
func TestAccumulatorFragmentsKeyedApart(t *testing.T) {
	for _, c := range []struct{ kids, read, want string }{
		{`<y/><x/>`, `$a/x[3]/accumulator-after('c'), $b/x/accumulator-after('c')`, "<out>3 1</out>"},
		{strings.Repeat(`<x/>`, 6), `$a/x[3]/accumulator-after('c'), $b/x[6]/accumulator-after('c')`, "<out>3 6</out>"},
	} {
		sheet := strings.NewReplacer("BKIDS", c.kids, "READ", c.read).Replace(accumFragmentSheet)
		got, err := optsRun(t, sheet, `<r/>`, TransformOptions{})
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %s", c.read, got, err, c.want)
		}
	}
}

// Each accumulator read over a tree keeps a table slot per node for the
// rest of the transform; the slots are retained items, totalled across
// accumulators. Four accumulators over 302 nodes hold 1,208 slots.
func TestAccumulatorTablesCharged(t *testing.T) {
	var decls, reads strings.Builder
	for _, n := range []string{"a1", "a2", "a3", "a4"} {
		decls.WriteString(`<xsl:accumulator name="` + n + `" initial-value="0"><xsl:accumulator-rule match="e" select="$value + 1"/></xsl:accumulator>`)
		reads.WriteString(`<xsl:value-of select="r/e[last()]/accumulator-after('` + n + `')"/>`)
	}
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:mode use-accumulators="#all"/>` + decls.String() + `<xsl:template match="/"><out>` + reads.String() + `</out></xsl:template></xsl:stylesheet>`
	src := `<r>` + strings.Repeat(`<e/>`, 300) + `</r>`
	if _, err := optsRun(t, sheet, src, TransformOptions{MaxItems: 1000}); !refused(err, "") {
		t.Fatalf("want the item refusal, got %v", err)
	}
	got, err := optsRun(t, sheet, src, TransformOptions{MaxItems: 2000})
	if err != nil || got != "<out>300300300300</out>" {
		t.Fatalf("under the bound: %q, %v", got, err)
	}
}
