package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamStringOfTyped: fn:string of a consumed node on a validated
// stream is the node's string value, its lexical form, as the tree gives it,
// not its typed value's: string(.) of a typed xs:decimal "4.50" is "4.50",
// not "4.5" (attr/streamable/streamable-014).
func TestStreamStringOfTyped(t *testing.T) {
	dir := t.TempDir()
	doc := `<list><item r="2.00"><price>4.50</price></item><item r="1.0"><price>10.0</price></item></list>`
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	sheet := func(body string) string {
		return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:import-schema><xs:schema>
 <xs:element name="list"><xs:complexType><xs:sequence><xs:element name="item" maxOccurs="unbounded"><xs:complexType>
  <xs:sequence><xs:element name="price" type="xs:decimal"/></xs:sequence><xs:attribute name="r" type="xs:decimal"/>
 </xs:complexType></xs:element></xs:sequence></xs:complexType></xs:element></xs:schema></xsl:import-schema>
<xsl:mode name="s" streamable="yes"/>
<xsl:template match="text()" mode="s"/>
<xsl:template match="item" mode="s"><i><xsl:apply-templates mode="s"/></i></xsl:template>
<xsl:template match="price" mode="s"><xsl:attribute name="{name(.)}" select="string(.)"/></xsl:template>
<xsl:template name="main"><top><xsl:source-document streamable="yes" validation="strict" href="doc.xml">` + body +
			`</xsl:source-document></top></xsl:template></xsl:stylesheet>`
	}
	for _, body := range []string{
		`<xsl:apply-templates select="." mode="s"/>`,
		`<xsl:for-each select="list/item"><v><xsl:value-of select="string(price)"/></v></xsl:for-each>`,
		`<v><xsl:value-of select="string(list/item[1])"/></v>`,
		`<v><xsl:value-of select="string(list/item[1]/@r)"/></v>`,
	} {
		coreCheck(t, sheet(body), dir, body, true, TransformOptions{})
	}
}
