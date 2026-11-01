package xslt

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

// The XTDE1490 check scanned every earlier result document, so n documents
// cost n^2/2 comparisons (40k documents took 9.9 s). It is now a set lookup.
func TestResultDocumentCollisionCheckIsLinear(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:param name="n" as="xs:integer" xmlns:xs="http://www.w3.org/2001/XMLSchema"/>
		<xsl:template match="/">
		  <xsl:for-each select="1 to $n">
		    <xsl:result-document href="d{.}.xml"><x/></xsl:result-document>
		  </xsl:for-each>
		  <xsl:if test="$n lt 0"><xsl:result-document href="d1.xml"><x/></xsl:result-document></xsl:if>
		</xsl:template>
		</xsl:stylesheet>`
	st, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compile(st.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src, _ := xdm.ParseString("<r/>", xdm.ParseOptions{})
	run := func(n int) time.Duration {
		start := time.Now()
		res, err := c.Transform(context.Background(), src.Root, TransformOptions{
			Params: map[string]xdm.Sequence{"n": {xdm.NewInteger(int64(n))}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Secondary) != n {
			t.Fatalf("n=%d: %d secondary results", n, len(res.Secondary))
		}
		return time.Since(start)
	}
	best := func(n int) time.Duration {
		best := run(n)
		for i := 0; i < 4; i++ {
			if d := run(n); d < best {
				best = d
			}
		}
		return best
	}
	small, large := best(6000), best(24000)
	// 4x the documents: about 4x the time when linear, about 16x when not.
	if large > 12*small {
		t.Fatalf("24000 documents took %v against %v for 6000", large, small)
	}

	// A repeated href is still XTDE1490.
	dup := strings.Replace(sheet, `<xsl:if test="$n lt 0">`, `<xsl:if test="$n ge 0">`, 1)
	st2, _ := xdm.ParseString(dup, xdm.ParseOptions{})
	c2, err := Compile(st2.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c2.Transform(context.Background(), src.Root, TransformOptions{
		Params: map[string]xdm.Sequence{"n": {xdm.NewInteger(3)}}})
	if err == nil || !strings.Contains(err.Error(), "XTDE1490") {
		t.Fatalf("duplicate href: err = %v, want XTDE1490", err)
	}
}
