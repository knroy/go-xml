package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// k2Bodies are container bodies of lane K2's shapes (streamsplit_k2.go);
// stream says the core must take them.
var k2Bodies = []struct {
	body   string
	stream bool
}{
	// (F | P)/T as (F union ())/T union P/T (foreignPath).
	{`<xsl:variable name="ins" as="element()*"><rec k="i"><sub x="9"/></rec></xsl:variable><out><xsl:copy-of select="($ins | /doc/rec)/sub"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><rec k="i"><sub x="9"/></rec></xsl:variable><out><xsl:value-of select="(/doc/rec union $ins)/sub/@x" separator=","/></out>`, true},
	{`<xsl:variable name="ins" select="1"/><out><xsl:copy-of select="($ins | /doc/rec)/sub"/></out>`, true},
	// P ! (* union $v) and the other set operators (mapSetSafe).
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b/></xsl:variable><out><xsl:copy-of select="/doc/rec[1] ! (* union $ins)"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b/></xsl:variable><out><xsl:copy-of select="/doc/rec ! ($ins union sub)"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b/></xsl:variable><out><xsl:copy-of select="/doc/rec[1] ! (* except $ins)"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b/></xsl:variable><out><xsl:copy-of select="/doc/rec ! ($ins intersect *)"/></out>`, true},
	{`<xsl:variable name="ins" as="element()*"><a>A</a><b/></xsl:variable><out><xsl:value-of select="/doc/rec ! ($ins except node()) ! name()" separator=","/></out>`, true},
	{`<xsl:variable name="ins" select="2"/><out><xsl:copy-of select="/doc/rec ! (* union $ins)"/></out>`, true},
	// for / some / every over values, the return reading only its variables (valueFor).
	{`<out><xsl:value-of select="for $x in //rec/@k/node-name() return ('Q{' || namespace-uri-from-QName($x) || '}' || local-name-from-QName($x))"/></out>`, true},
	{`<out><xsl:value-of select="some $x in //sub/@x/node-name() satisfies (local-name-from-QName($x) || 'y')"/></out>`, true},
	{`<out><xsl:value-of select="sum(for $v in /doc/rec/xs:integer(@v) return let $w := $v * 2 return xs:decimal($w) div 4)"/></out>`, true},
	{`<out><xsl:value-of select="every $v in //rec/xs:integer(@v) satisfies xs:integer($v) gt 0"/></out>`, true},
	// A consuming part of an instruction's value template (instrAVTOp).
	{`<out><xsl:value-of select="1 to 4" separator="{substring(head(//sub/@x), 1, 1)}"/></out>`, true},
	{`<out><xsl:attribute name="a" select="1 to 4" separator="{string(head(//rec/@k))}"/></out>`, true},
	{`<out><xsl:element name="{translate(head(//rec/@k), 'ab', 'xy')}e">value</xsl:element></out>`, true},
	{`<out><xsl:attribute name="a" namespace="http://{lower-case(head(//rec/@k))}.com/">v</xsl:attribute></out>`, true},
	{`<out><xsl:attribute name="{concat('n', count(//sub))}">v</xsl:attribute></out>`, true},
	// generate-id() of an element's view in a motionless part (viewIdentOK).
	{`<out><xsl:for-each select="doc/rec"><e n="{string-length(generate-id()) gt 1}"><xsl:value-of select="generate-id() = generate-id(.)"/></e></xsl:for-each></out>`, true},
	// (if (c) then A else B) ! R as if (c) then A ! R else B ! R (hoistMapChoice).
	{`<xsl:variable name="c" select="true()"/><out><xsl:value-of select="(if ($c) then /doc/rec[1]/sub/ancestor-or-self::* else /doc/rec[1]/sub/ancestor::*) ! name()" separator=","/></out>`, true},
	{`<xsl:variable name="c" select="false()"/><out><xsl:value-of select="(if ($c) then /doc/rec[1]/sub/ancestor-or-self::* else /doc/rec[1]/sub/ancestor::*) ! name()" separator=","/></out>`, true},
}

func TestStreamK2MatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range k2Bodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}
