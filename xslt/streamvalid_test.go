package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamResultValidation: a constructor with validation or type in a
// streamed container runs on the core, validated as the tree validates it:
// the tree's output and error (streamvalid.go).
func TestStreamResultValidation(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range map[string]string{"flat": coreDocs["flat"], "wide": coreDocs["wide"]} {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{
			`<o><xsl:copy-of select="doc/rec" validation="strip"/></o>`,
			`<o><xsl:copy-of select="doc/rec" validation="preserve"/></o>`,
			`<xsl:element name="e" validation="strip"><xsl:copy-of select="doc/rec"/></xsl:element>`,
			`<e xsl:validation="preserve"><xsl:value-of select="sum(doc/rec/@v)"/></e>`,
			`<e xsl:validation="strip" n="{count(doc/rec)}"/>`,
			`<o><xsl:element name="n" type="xs:integer"><xsl:value-of select="count(doc/rec)"/></xsl:element></o>`,
			`<o><xsl:element name="n" type="xs:date"><xsl:value-of select="count(doc/rec)"/></xsl:element></o>`,
			`<xsl:document validation="strip"><o><xsl:copy-of select="doc/rec"/></o></xsl:document>`,
		} {
			coreCheck(t, coreSheet(body), dir, name+": "+body, true, TransformOptions{})
		}
		// xsl:copy assessing an attribute of the stream against a type: the
		// validator copies the attribute's whole tree, a view's (si-copy-116,
		// -117). Refused, or run on a copy: never a guard hit.
		for _, body := range []string{
			`<o><xsl:copy select="/*/*/@v" type="xs:decimal"/></o>`,
			`<o><xsl:copy select="doc/rec[1]/@k" type="xs:date"/></o>`,
			`<o><xsl:copy select="doc/rec[1]/@v" validation="strict"/></o>`,
			`<o><xsl:for-each select="doc/rec"><xsl:copy select="@v" type="xs:decimal"/></xsl:for-each></o>`,
			`<o><xsl:for-each select="doc/rec/@v"><xsl:copy type="xs:decimal"/></xsl:for-each></o>`,
		} {
			so, se, st := coreRun(coreSheet(body), dir, true, TransformOptions{})
			to, te, _ := coreRun(coreSheet(body), dir, false, TransformOptions{})
			if so != to || se != te || st.Guards != 0 {
				t.Errorf("%s: %s:\ncore %q %q (%d guard hits)\ntree %q %q", name, body, so, se, st.Guards, to, te)
			}
		}
	}
}
