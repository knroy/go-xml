package xslt

import (
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// TestStreamPatternMatchesNoNode: a rule whose pattern is ".[. instance of
// map(...)]" (or array, atomic) matches no node, so it does not keep
// TransformReader from streaming the node rules beside it; ".[. instance of
// node()]" still does.
func TestStreamPatternMatchesNoNode(t *testing.T) {
	rules := `<xsl:template match="rec"><r k="{@k}"/></xsl:template>`
	cases := []struct {
		pat    string
		stream bool
	}{
		{`.[. instance of map(xs:string, xs:decimal)]`, true},
		{`.[. instance of array(*)]`, true},
		{`.[. instance of xs:integer]`, true},
		{`.[. instance of node()]`, false},
		{`.[. instance of element()]`, false},
	}
	for i, c := range cases {
		sheet := mainSheet(`<xsl:mode streamable="yes"/>` + rules + `<xsl:template match="` + c.pat + `" priority="5"><m/></xsl:template>`)
		so, st := mainRun(sheet, coreDocs["flat"], true, TransformOptions{})
		to, _ := mainRun(sheet, coreDocs["flat"], false, TransformOptions{})
		if so != to {
			t.Errorf("case %d:\nstream %q\ntree   %q", i, so, to)
		}
		if got := st.Runs[streamtrace.Main] > 0; got != c.stream {
			t.Errorf("case %d %s: streamed %v, want %v", i, c.pat, got, c.stream)
		}
	}
}
