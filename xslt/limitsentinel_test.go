package xslt_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xslt"
)

// The limit a runaway stylesheet actually reaches must be classifiable.
//
// A caller has only errors.Is to decide whether a failure means "your input
// is bad" or "I declined to spend more", and for a while the commonest
// refusal in this package was the one that answered neither: template
// recursion returned a bare string while fn:transform's nesting refusal, in
// the same package and for the same kind of bound, wrapped the sentinel.
func TestTemplateRecursionCarriesTheResourceSentinel(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:template name="go"><xsl:call-template name="go"/></xsl:template>` +
		`</xsl:stylesheet>`
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := xslt.Compile(doc.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := xdm.ParseString("<d/>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, terr := st.Transform(context.Background(), src.Root,
		xslt.TransformOptions{InitialTemplate: "go", MaxDepth: 20})
	if terr == nil {
		t.Fatal("unbounded template recursion returned no error")
	}
	if !errors.Is(terr, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell a refusal to spend from a fault in its input", terr)
	}
}

// xsl:analyze-string is the one regex wrapper outside the function library: it
// compiles through xpath.CompileRegexpVersion and reads the budget back with
// xpath.RegexpErr, then re-wraps the result in its own XTDE1140. That re-wrap
// is the thing this pins -- the instruction has to substitute its own error
// code, because a stylesheet matching on one needs XTDE1140 rather than the
// library's FORX0002, and a code substitution done with %v instead of %w is
// exactly how the sentinel gets lost between one wrapper and the next.
//
// The XPath-level siblings are covered in xpath/regex_wrapper_limit_test.go.
func TestAnalyzeStringBudgetCarriesTheResourceSentinel(t *testing.T) {
	old := xpath.BacktrackingRegexEnabled()
	xpath.SetBacktrackingRegex(true)
	defer xpath.SetBacktrackingRegex(old)

	// Nested unbounded quantification closed by a backreference, against
	// sixty "a"s: the shape the backtracking budget exists to bound.
	const sheet = `<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:template match="/"><out>` +
		`<xsl:analyze-string select="string(/d)" regex="(a*)*\1b">` +
		`<xsl:matching-substring>m</xsl:matching-substring>` +
		`</xsl:analyze-string></out></xsl:template></xsl:stylesheet>`
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := xslt.Compile(doc.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := xdm.ParseString("<d>"+strings.Repeat("a", 60)+"</d>",
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, terr := st.Transform(context.Background(), src.Root,
		xslt.TransformOptions{})
	if terr == nil {
		t.Fatal("an exhausted backtracking budget produced a result; the " +
			"instruction would be reporting substrings it never found")
	}
	if !errors.Is(terr, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; xsl:analyze-string "+
			"loses the classification its XPath siblings keep", terr)
	}
	if code := xdm.ErrorCode(terr); code != "XTDE1140" {
		t.Errorf("code = %q, want XTDE1140; the sentinel must be ADDED to the "+
			"instruction's own code, not replace it", code)
	}
}
