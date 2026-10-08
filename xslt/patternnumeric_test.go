package xslt

import "testing"

// A pattern predicate whose value is numeric selects by position (XPath 3.1
// section 3.3.2, XSLT 3.0 section 5.5.3), however the number is computed.
// Before the fix only a numeric literal, a variable, arithmetic or a
// position()/last() call was treated as positional, so item[number(@n)] was
// evaluated with the position pinned at 1 and matched the item whose @n is 1.
//
// The expected strings are Saxon-HE 12.10's answers for the single-step and
// union cases. For the multi-step case Saxon's pattern matcher answers "---"
// (and fails with a NullPointerException for xs:integer(@n)), while Saxon's
// own evaluation of the equivalent path root(.)//(item[number(@n)]/sub)
// selects the second sub, which is what section 5.5.3 defines.
func TestNumericPatternPredicateIsPositional(t *testing.T) {
	const src = `<r><item n="2" s="ab"><sub/></item><item n="2" s="ab"><sub/></item><item n="1" s="a"><sub/></item><other n="1"/></r>`
	cases := []struct{ match, sel, want string }{
		{"item[number(@n)]", "r/*", "-M--"},
		{"r/item[xs:integer(@n)]", "r/*", "-M--"},
		{"item[string-length(@s)]", "r/*", "-M--"},
		{"item[sum(@n)]", "r/*", "-M--"},
		{"other | item[number(@n)]", "r/*", "-M-M"},
		{"item[number(@n)]/sub", "r/item/sub", "-M-"},
		{"item[number(@n)][1]", "r/*", "-M--"},
		// Not numeric: string and boolean predicates stay non-positional.
		{"item[string(@n)]", "r/*", "MMM-"},
		{"item[@n = 2]", "r/*", "MM--"},
	}
	for _, c := range cases {
		t.Run(c.match, func(t *testing.T) {
			sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
				<xsl:output method="text"/>
				<xsl:template match="/"><xsl:apply-templates select="` + c.sel + `"/></xsl:template>
				<xsl:template match="` + c.match + `">M</xsl:template>
				<xsl:template match="node()">-</xsl:template>
			</xsl:stylesheet>`
			got, err := runErr(t, sheet, src)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("match=%q: got %q, want %q", c.match, got, c.want)
			}
		})
	}
}
