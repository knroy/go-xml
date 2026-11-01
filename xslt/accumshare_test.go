package xslt

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A sequence-valued accumulator, ($value, 1), used to keep a full copy of its
// value at every node: n nodes held n^2/2 items, none of them charged. Now a
// value that extends its predecessor shares its backing array, and what is
// stored counts against MaxItems.
func TestAccumulatorSequenceValueIsChargedAndShared(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output method="text"/>
		<xsl:mode use-accumulators="a"/>
		<xsl:accumulator name="a" initial-value="()">
		  <xsl:accumulator-rule match="e" select="($value, 1)"/>
		</xsl:accumulator>
		<xsl:template match="/"><xsl:value-of
		  select="count(r/e[last()]/accumulator-after('a'))"/></xsl:template>
		</xsl:stylesheet>`
	run := func(n, max int) (string, error) {
		st, err := xdm.ParseString(sheet, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		c, err := Compile(st.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		src, err := xdm.ParseString("<r>"+strings.Repeat("<e/>", n)+"</r>", xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		res, err := c.Transform(context.Background(), src.Root, TransformOptions{MaxItems: max})
		if err != nil {
			return "", err
		}
		var b strings.Builder
		if err := res.Serialize(&b); err != nil {
			return "", err
		}
		return b.String(), nil
	}
	// The stored items number n, so a bound of n+100 accepts the walk and
	// the output is unchanged.
	if got, err := run(2000, 2100); err != nil || got != strconv.Itoa(2000) {
		t.Fatalf("n=2000: got %q, %v", got, err)
	}
	// A bound just under n refuses it.
	_, err := run(2000, 1900)
	if err == nil || !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("n=2000 with MaxItems 1900: err = %v, want a resource limit", err)
	}
}
