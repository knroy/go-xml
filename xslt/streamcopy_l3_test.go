package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamDocCopyEvents: a copy of the document, or of the document
// element, at the document node runs on the core, read off the cursor as
// events (into an open element) or built once (anywhere else), and its
// string value is read off the text events: the tree's output and error
// (streamcopy_l3.go).
func TestStreamDocCopyEvents(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, decls := range coreDecls {
			for _, body := range []string{
				`<o><h/><xsl:copy-of select="."/><t/></o>`,
				`<o><xsl:sequence select="copy-of(.)"/></o>`,
				`<o><xsl:sequence select="snapshot(.)"/></o>`,
				`<o><xsl:sequence select="."/></o>`,
				`<o><xsl:value-of select="."/></o>`,
				`<o><xsl:value-of select="string(.)"/></o>`,
				`<o><xsl:copy-of select="*"/></o>`,
				`<o><xsl:copy-of select="node()"/></o>`,
				`<xsl:variable name="d" select="copy-of(.)"/><o><xsl:value-of select="count($d//rec), name($d/*)"/></o>`,
				`<xsl:variable name="d" as="node()"><xsl:copy-of select="."/></xsl:variable><o><xsl:copy-of select="$d/*"/></o>`,
			} {
				coreCheck(t, coreSheetWith(decls, body), dir, name+": "+decls+body, true, TransformOptions{})
			}
		}
	}
}

// TestStreamCopyDocNode: xsl:copy of the document node runs on the core
// whatever its body reads, or when it reads nothing: the copy has the
// document's DOCTYPE (its unparsed entities) and base URI, as the tree's
// (streamcopy_l3doc.go).
func TestStreamCopyDocNode(t *testing.T) {
	dir := t.TempDir()
	docs := map[string]string{
		"flat":  coreDocs["flat"],
		"mixed": coreDocs["mixed"],
		"ns":    coreDocs["ns"],
		"bad":   coreDocs["bad"],
		"dtd": `<!DOCTYPE doc [<!NOTATION gif SYSTEM "g.txt"><!ENTITY pic SYSTEM "pic.gif" NDATA gif>]>` +
			`<doc><rec k="a" v="1"/></doc>`,
	}
	ent := `<xsl:value-of select="ends-with(unparsed-entity-uri('pic', $c), 'pic.gif'), ends-with(base-uri($c), 'doc.xml'), count($c/*)"/>`
	for name, doc := range docs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{
			`<o><xsl:copy><xsl:copy-of select="child::node()"/></xsl:copy></o>`,
			`<o><xsl:copy select="."><xsl:copy-of select="node()"/></xsl:copy></o>`,
			`<o><xsl:copy><xsl:for-each select="*"><e n="{name()}"/></xsl:for-each></xsl:copy></o>`,
			`<xsl:variable name="c" as="document-node()"><xsl:copy><xsl:copy-of select="*"/></xsl:copy></xsl:variable><o>` + ent + `</o>`,
			`<xsl:variable name="c" as="document-node()"><xsl:copy/></xsl:variable><o>` + ent + `</o>`,
			`<o><xsl:where-populated><xsl:copy><xsl:if test="false()"><b/></xsl:if></xsl:copy></xsl:where-populated><xsl:on-empty><e/></xsl:on-empty></o>`,
		} {
			coreCheck(t, coreSheet(body), dir, name+": "+body, true, TransformOptions{})
		}
	}
}
