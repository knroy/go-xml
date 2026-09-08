package xslt

import (
	"context"
	"strings"
	"testing"
)

// TestMergeStreamableSourceSelectsSnapshots covers section 15.4.
//
// streamable="yes" on an xsl:merge-source makes the select expression
// "implicitly used as the argument of a call on the snapshot function ...
// whether or not streamed processing is actually used, and whether or not the
// processor supports streaming". That is not an optimisation a non-streaming
// engine may skip: a snapshot keeps a node's ancestors but none of their other
// children, so navigating out of it "will typically not cause an error, but
// will return empty results".
//
// Both sources select r/rec/c, and the action then reads back up to
// ancestor::rec and down into its other child. Without the snapshot the
// original tree answers and t is "9 9" — one temp per source. With it the
// ancestor is present but childless apart from the copied c, so t is empty.
// This is merge-079, whose expected result asserts @temp="".
func TestMergeStreamableSourceSelectsSnapshots(t *testing.T) {
	run := func(t *testing.T, streamable string) string {
		t.Helper()
		src := `<xsl:stylesheet version="3.0"
		    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		  <xsl:template name="main">
		    <out><xsl:merge>
		      <xsl:merge-source name="a" streamable="` + streamable + `"
		          select="$one/r/rec/c">
		        <xsl:merge-key select="n"/>
		      </xsl:merge-source>
		      <xsl:merge-source name="b" streamable="` + streamable + `"
		          select="$two/r/rec/c">
		        <xsl:merge-key select="n"/>
		      </xsl:merge-source>
		      <xsl:merge-action>
		        <xsl:variable name="g" select="current-merge-group()"/>
		        <g k="{current-merge-key()}"
		           t="{$g/ancestor::rec[1]/temp}"/>
		      </xsl:merge-action>
		    </xsl:merge></out>
		  </xsl:template>
		  <xsl:variable name="one">
		    <r><rec><c><n>x</n></c><temp>9</temp></rec></r>
		  </xsl:variable>
		  <xsl:variable name="two">
		    <r><rec><c><n>x</n></c><temp>9</temp></rec></r>
		  </xsl:variable>
		</xsl:stylesheet>`
		sheet, err := Compile(mustParse(t, src), CompileOptions{})
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		res, err := sheet.Transform(context.Background(), nil,
			TransformOptions{InitialTemplate: "main"})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		return res.String()
	}

	t.Run("streamable", func(t *testing.T) {
		if got := run(t, "yes"); !strings.Contains(got, `t=""`) {
			t.Errorf("output = %s, want t=\"\": the selected nodes were not "+
				"snapshots, so the action navigated outside them", got)
		}
	})
	// The control: without streamable="yes" there is no implicit snapshot,
	// the original tree is merged, and the same step finds the sibling.
	t.Run("not streamable", func(t *testing.T) {
		if got := run(t, "no"); !strings.Contains(got, `t="9 9"`) {
			t.Errorf("output = %s, want t=\"9 9\": a non-streamable source "+
				"must merge the original nodes", got)
		}
	})
}
