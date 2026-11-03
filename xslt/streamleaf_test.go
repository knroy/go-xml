package xslt

import (
	"fmt"
	"github.com/knroy/go-xml/v2/xpath"
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

// climbBodies climb from streamed matches to ancestors: each ancestor once,
// in document order, read for its name and attributes. stream says the core
// must take it: one reading an ancestor's content must not.
var climbBodies = []struct {
	body   string
	stream bool
}{
	{`<out><xsl:copy-of select="head(//sub/ancestor::*) ! name()"/></out>`, true},
	{`<out><xsl:copy-of select="(//sub/ancestor::*, 'x') ! name()"/></out>`, true},
	{`<out><xsl:copy-of select="subsequence(//rec/ancestor-or-self::*, 2) ! name()"/></out>`, true},
	{`<out><xsl:value-of select="//sub/ancestor-or-self::*/@*" separator="|"/></out>`, true},
	{`<out><xsl:value-of select="//rec/ancestor-or-self::*/data(@k)" separator="|"/></out>`, true},
	{`<out><xsl:value-of select="//sub/parent::node()/name()" separator="|"/></out>`, true},
	{`<out><xsl:value-of select="count(//sub/ancestor::*)"/></out>`, true},
	{`<out><xsl:value-of select="count(/doc/rec/sub/parent::node())"/></out>`, true},
	{`<out><xsl:value-of select="//sub/ancestor::*[1]/@k" separator="|"/></out>`, true},
	{`<out><xsl:copy-of select="if (//sub/ancestor::rec) then 0 else 1"/></out>`, true},
	{`<out><xsl:copy-of select="//sub/ancestor::*"/></out>`, false},
	{`<out><xsl:value-of select="//sub/ancestor::rec ! string()"/></out>`, false},
}

// TestStreamClimbMatchesTree: every climbing body over every document gives
// on the core the tree's output; those that may stream do.
func TestStreamClimbMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, d := range coreDecls {
			for _, c := range climbBodies {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}

// TestStreamClimbItemsChecked: a split whose climbed ancestors would be read
// for more than a view has (their content, by atomizing or copying them) is
// refused; reading their names, or passing them through, is not. The
// analysis refuses such reads too; this is the second line behind it.
func TestStreamClimbItemsChecked(t *testing.T) {
	ns := splitResolver()
	for src, ok := range map[string]bool{
		`head(//sub/ancestor::*) ! name()`:                true,
		`(subsequence(//sub/ancestor::*, 2), 1) ! name()`: true,
		`count(//sub/ancestor::*)`:                        true,
		`string-join(//sub/ancestor::*, ',')`:             false,
		`head(//sub/ancestor::*)`:                         false,
		`copy-of(//sub/ancestor::*)`:                      false,
		`//sub/ancestor::* ! string()`:                    false,
		`(//sub/ancestor::*)[. = 'x'] ! name()`:           false,
	} {
		base, err := xpath.CompileVersion(src, ns, xpath.XPath31)
		if err != nil {
			t.Fatal(err)
		}
		e, _ := xpath.ParseVersion(src, ns, xpath.XPath31)
		s, why := splitStream(e, base, true)
		if (s != nil) != ok {
			t.Errorf("%s: split %v (%s), want %v", src, s != nil, why, ok)
		}
	}
}
