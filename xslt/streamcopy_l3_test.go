package xslt

import (
	"os"
	"path/filepath"
	"strconv"
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

// TestStreamCopyChoice: in a rule that may match the document element, a
// copy-of or a sequence in element content whose select is a motionless
// choice of paths copies the chosen branch's matches as they are read, and
// a value-of of copies of attributes and children reads values: no
// grounded copies collected (streamcopy_l3choice.go).
func TestStreamCopyChoice(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, rule := range []string{
			`<r><xsl:sequence select="if (@k = 'a') then sub else if (@k = 'b') then sub[@x = '3'] else ()"/></r>`,
			`<r><xsl:copy-of select="if (@k = 'a') then sub else rec"/></r>`,
			`<r><xsl:copy-of select="if (@k) then () else *"/></r>`,
			`<r><xsl:value-of select="copy-of(@*), sub" separator=":"/></r>`,
		} {
			sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:mode name="s" streamable="yes"/>
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml"><xsl:apply-templates select="." mode="s"/></xsl:source-document></top></xsl:template>
<xsl:template match="rec" mode="s">` + rule + `</xsl:template>
<xsl:template match="text()" mode="s"/>
</xsl:stylesheet>`
			coreCheck(t, sheet, dir, name+": "+rule, true, TransformOptions{})
		}
	}
}

// TestStreamLeafAtomicSequence: a rule for a text, comment or PI node may
// return, or bind, atomic values made of its copy with xsl:sequence or
// xsl:variable: no node of the copy is handed on (streamcopy_l3leaf.go).
func TestStreamLeafAtomicSequence(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, rules := range []string{
			`<xsl:template match="text()" mode="s"><xsl:sequence select="concat('[', ., ']')"/></xsl:template>`,
			`<xsl:template match="text()" mode="s"><xsl:sequence select="string()"/></xsl:template>` +
				`<xsl:template match="comment() | processing-instruction()" mode="s"><xsl:variable name="v" select="string-length(.)"/><c n="{$v}"/></xsl:template>`,
		} {
			sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:mode name="s" streamable="yes"/>
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml"><o><xsl:apply-templates select="." mode="s"/></o></xsl:source-document></top></xsl:template>
` + rules + `
</xsl:stylesheet>`
			coreCheck(t, sheet, dir, name+": "+rules, true, TransformOptions{})
		}
	}
}

// TestStreamAtomicTypedVariable: a variable declared with an atomic type
// atomizes its value, so its select is read as values off the cursor, also
// of a rule's context that may be the document element (streamcopy_l3.go,
// noteStreamParam).
func TestStreamAtomicTypedVariable(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, rule := range []string{
			`<xsl:variable name="n" as="xs:string" select="."/><r><xsl:value-of select="string-length($n)"/></r>`,
			`<xsl:variable name="n" as="xs:string*" select="sub"/><r><xsl:value-of select="$n" separator="|"/></r>`,
		} {
			sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
<xsl:mode name="s" streamable="yes"/>
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml"><xsl:apply-templates select="." mode="s"/></xsl:source-document></top></xsl:template>
<xsl:template match="rec" mode="s">` + rule + `</xsl:template>
<xsl:template match="text()" mode="s"/>
</xsl:stylesheet>`
			coreCheck(t, sheet, dir, name+": "+rule, true, TransformOptions{})
		}
		body := `<xsl:variable name="n" as="xs:string" select="."/><o><xsl:value-of select="string-length($n)"/></o>`
		coreCheck(t, coreSheet(body), dir, name+": "+body, true, TransformOptions{})
	}
}

// TestStreamApplyLeafMatches: an apply-templates whose select may match
// text, comment or PI nodes dispatches each such match on its copy, in its
// place among the element matches, with its position (streamapply.go,
// lane L3).
func TestStreamApplyLeafMatches(t *testing.T) {
	dir := t.TempDir()
	rules := `<xsl:template match="rec" mode="s"><r p="{position()}"/></xsl:template>
<xsl:template match="text()" mode="s"><t p="{position()}"><xsl:value-of select="."/></t></xsl:template>
<xsl:template match="comment()" mode="s"><c p="{position()}"/></xsl:template>`
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, sel := range []string{"doc/rec/sub/text()", "doc/node()", "node()", "doc/rec/text()", "doc/comment()"} {
			for _, rs := range []string{rules, ""} {
				sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:mode name="s" streamable="yes"/>
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml"><o><xsl:apply-templates select="` + sel + `" mode="s"/></o></xsl:source-document></top></xsl:template>
` + rs + `
</xsl:stylesheet>`
				coreCheck(t, sheet, dir, name+": "+sel+" rules="+strconv.FormatBool(rs != ""), true, TransformOptions{})
			}
		}
	}
}
