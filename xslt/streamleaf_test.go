package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// leafBodies keep text, comment and PI matches as nodes: copied to the
// output, in order, read through their parents, compared, a loop's matches.
var leafBodies = []string{
	`<out><xsl:copy-of select="head(//text())"/></out>`,
	`<out><xsl:sequence select="1, //sub/text(), 2"/></out>`,
	`<out><xsl:copy-of select="(//text())[position() lt 3]"/></out>`,
	`<out><xsl:copy-of select="//comment()"/></out>`,
	`<out><xsl:copy-of select="//processing-instruction()"/></out>`,
	`<out n="{count(head(//rec/text()))}"/>`,
	`<out><xsl:value-of select="//text()" separator="|"/></out>`,
	`<out><xsl:for-each select="//sub/text()"><v p="{name(..)}" x="{../@x}"><xsl:value-of select="."/></v></xsl:for-each></out>`,
	`<out><xsl:for-each select="/doc/rec/text()"><v><xsl:value-of select="."/></v></xsl:for-each></out>`,
	`<out><xsl:copy-of select="/doc/rec/node()"/></out>`,
	`<out><xsl:attribute name="a" select="1, head(//text())"/></out>`,
	`<out d="{deep-equal(//sub/text(), ())}"/>`,
}

// leafRuleDecls and leafRuleBody match text nodes through their parents in
// a streamable mode: the pattern is decided on the live node, the rule's
// body runs on the copy.
const leafRuleDecls = `<xsl:mode name="m" streamable="yes" on-no-match="shallow-skip"/>
<xsl:template match="sub/text()" mode="m"><s p="{name(..)}" x="{../@x}"><xsl:value-of select="."/></s></xsl:template>
<xsl:template match="rec//text()[not(parent::sub)]" mode="m"><r><xsl:value-of select="."/></r></xsl:template>`

// TestStreamLeavesMatchTree: every body keeping leaves, and the leaf rules,
// over every document, give on the core the tree's output; they stream.
func TestStreamLeavesMatchTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, d := range coreDecls {
			for _, b := range leafBodies {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, b)
				streamed += coreCheck(t, coreSheetWith(d, b), dir, where, true, TransformOptions{})
			}
			b := `<out><xsl:apply-templates mode="m"/></out>`
			where := fmt.Sprintf("doc %s, decls %q, leaf rules", name, d)
			streamed += coreCheck(t, coreSheetWith(d+leafRuleDecls, b), dir, where, true, TransformOptions{})
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}
