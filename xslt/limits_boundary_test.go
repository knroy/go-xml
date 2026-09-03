package xslt

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TransformOptions.MaxDepth at its edges. See xdm/limits_boundary_test.go for
// why this class of test exists.
//
// The boundary matters more here than the doc comment lets on: MaxDepth counts
// the ordinary descent of an identity transform as well as runaway recursion,
// so a limit set one too low refuses documents the parser had just accepted.
// The at-limit / one-over pair is what pins that.
func TestTransformMaxDepthBoundaries(t *testing.T) {
	// A named template that calls itself exactly $n times, so the recursion
	// depth is a number the test chooses rather than one it has to measure.
	const depth = 5
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template name="go">
    <xsl:param name="n"/>
    <xsl:if test="$n &gt; 0">
      <xsl:call-template name="go">
        <xsl:with-param name="n" select="$n - 1"/>
      </xsl:call-template>
    </xsl:if>
  </xsl:template>
  <xsl:template match="/">
    <out><xsl:call-template name="go"><xsl:with-param name="n" select="4"/></xsl:call-template></out>
  </xsl:template>
</xsl:stylesheet>`

	sd, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	doc, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}

	tests := []struct {
		name    string
		max     int
		wantErr string
	}{
		// Deliberate: zero means DefaultMaxDepth (1000), matching
		// xdm.DefaultMaxDepth so a document the parser accepts can be
		// transformed. A fixed 300 once left this refusing 500-deep documents
		// it had just parsed.
		{"zero is the default", 0, ""},
		// Deliberate: the field documents "a negative value means no limit".
		{"negative is unlimited", -1, ""},
		{"the smallest limit refuses", 1, "template recursion exceeded 1 levels"},
		{"one under refuses", depth - 1, "template recursion exceeded 4 levels"},
		{"exactly at the limit is accepted", depth, ""},
		{"one over is accepted", depth + 1, ""},
		{"MaxInt does not overflow", math.MaxInt, ""},
		{"MaxInt-1 is its neighbour", math.MaxInt - 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := st.Transform(context.Background(), doc.Root,
				TransformOptions{MaxDepth: tt.max})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("accepted transform was refused: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("transform succeeded; want an error matching %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("error %q does not name the limit; want it to contain %q",
					err, tt.wantErr)
			}
		})
	}
}
