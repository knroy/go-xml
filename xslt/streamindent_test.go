package xslt

import "testing"

// TestStreamOutIndent: under indent="yes" an element whose indentation is
// not yet decided is written into a buffer as it is made, not held as a
// tree: the elements are opened (written as far as their start tags), and
// the bytes are the tree's when a text child decides late, in the document
// element or in a record, when none comes, and under html's inline rules.
func TestStreamOutIndent(t *testing.T) {
	ident := `<xsl:mode on-no-match="shallow-copy"/>`
	for _, c := range []struct{ name, decls, body, src string }{
		{"no text", `<xsl:output indent="yes"/>` + ident, ``,
			`<doc><s><r k="1"><a/><b/></r><r k="2"/></s><s><r/></s></doc>`},
		{"late text in the document element", `<xsl:output indent="yes"/>` + ident, ``,
			`<doc><s><r><a/></r></s><s/>tail<s><r/></s></doc>`},
		{"text in a record", `<xsl:output indent="yes"/>` + ident, ``,
			`<doc><s><r><a/>x<b><c/></b></r><r><a/></r></s><s><r>y</r></s></doc>`},
		{"comments and whitespace", `<xsl:output indent="yes"/><xsl:preserve-space elements="*"/>` + ident, ``,
			"<doc>\n <s><!--c--> </s>\n <s> <r/> <?p d?></s>\n</doc>"},
		{"suppress-indentation", `<xsl:output indent="yes" suppress-indentation="s"/>` + ident, ``,
			`<doc><s><r><a/></r></s><t><r><a/></r></t></doc>`},
		// version="4.0": HTML5, the html default, is held (prefix
		// normalisation reads the whole tree).
		{"html inline", `<xsl:output method="html" version="4.0" indent="yes"/>`,
			`<xsl:template match="/"><div><xsl:for-each select="doc/r"><p><b>x</b><i/></p><span/><div><ul><li>a</li><li><em/></li></ul></div></xsl:for-each></div></xsl:template>`,
			`<doc><r/><r/><r/></doc>`},
		{"xhtml", `<xsl:output method="xhtml" indent="yes"/>`,
			`<xsl:template match="/"><div xmlns="http://www.w3.org/1999/xhtml"><xsl:for-each select="doc/r"><p><b>x</b></p><span/><table><tr><td/></tr></table></xsl:for-each></div></xsl:template>`,
			`<doc><r/><r/></doc>`},
	} {
		if n := outCheck(t, outSheet(t, c.decls, c.body), c.src, c.name, TransformOptions{}); n == 0 {
			t.Errorf("%s: nothing opened: the indented result was held", c.name)
		}
	}
}
