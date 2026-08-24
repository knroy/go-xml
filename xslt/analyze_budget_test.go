package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// The backtracking matcher reports an exhausted step budget through Err(),
// because the Regexp interface returns a bare bool. xsl:analyze-string used
// the bool without checking, so an exhausted budget was indistinguishable
// from a genuine non-match: the instruction emitted its non-matching branch
// and the transform succeeded with the wrong output, silently, on exactly the
// input where the answer was hardest to get.
//
// Precondition: the backtracking matcher, which is off by default.
func TestAnalyzeStringReportsBudgetExhaustion(t *testing.T) {
	xpath.SetBacktrackingRegex(true)
	defer xpath.SetBacktrackingRegex(false)

	ss := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:template match="/"><out><xsl:analyze-string select="'` +
		strings.Repeat("a", 60) + `'" regex="(a*)*\1b">
<xsl:matching-substring>MATCH</xsl:matching-substring>
<xsl:non-matching-substring>NONMATCH</xsl:non-matching-substring>
</xsl:analyze-string></out></xsl:template></xsl:stylesheet>`

	st, err := xdm.ParseString(ss, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sty, err := Compile(st.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sty.Transform(context.Background(), doc.Root, TransformOptions{})
	if err == nil {
		t.Fatal("the exhausted budget was reported as a non-match")
	}
	if !strings.Contains(err.Error(), "FORX0002") {
		t.Fatalf("wrong error: %v", err)
	}
}
