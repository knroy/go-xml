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

// TestStreamDocCopyIDs: a copy of the document or of the document element
// made off the cursor keeps what the tree's copy keeps of the DTD's typing
// -- is-id under validation="strip", all of it otherwise -- so id() over the
// copy finds what the tree's finds (streamcopy_l3.go, copiedTyping).
func TestStreamDocCopyIDs(t *testing.T) {
	dir := t.TempDir()
	doc := `<!DOCTYPE doc [<!ATTLIST rec id ID #IMPLIED ref IDREFS #IMPLIED>]>` +
		`<doc><rec id="x1" ref="x2"/><rec id="x2"><sub/></rec></doc>`
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := `<o><xsl:value-of select="count(id('x1 x2', $c)), count(idref('x2', $c)), name(id('x2', $c))"/></o>`
	for _, body := range []string{
		`<xsl:variable name="c"><e><xsl:copy-of select="."/></e></xsl:variable>` + probe,
		`<xsl:variable name="c"><e><xsl:sequence select="."/></e></xsl:variable>` + probe,
		`<xsl:variable name="c"><e><xsl:copy-of select="*"/></e></xsl:variable>` + probe,
		`<xsl:variable name="c"><e><xsl:copy-of select="." validation="preserve"/></e></xsl:variable>` + probe,
		`<xsl:variable name="c" select="copy-of(.)"/>` + probe,
		`<xsl:variable name="c" as="node()"><xsl:copy-of select="."/></xsl:variable>` + probe,
	} {
		coreCheck(t, coreSheet(body), dir, body, true, TransformOptions{})
	}
}

// TestStreamDocEntities: unparsed-entity-uri and unparsed-entity-public-id
// of the document node, after an unconditional consumer or with nothing
// consuming, read the DOCTYPE the stand-in is given (streamcopy_l3doc.go,
// docEntities).
func TestStreamDocEntities(t *testing.T) {
	dir := t.TempDir()
	docs := map[string]string{
		"dtd": `<!DOCTYPE doc [<!NOTATION gif SYSTEM "g.txt"><!ENTITY pic PUBLIC "-//P//EN" "pic.gif" NDATA gif>]>` +
			`<doc><rec k="a"/></doc>`,
		"flat": coreDocs["flat"],
		"bad":  coreDocs["bad"],
	}
	ent := `<xsl:value-of select="ends-with(unparsed-entity-uri('pic'), 'pic.gif'), unparsed-entity-public-id('pic')"/>`
	for name, doc := range docs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{
			`<o><xsl:comment select="head(//rec/@k)"/>` + ent + `</o>`,
			`<o>` + ent + `</o>`,
			`<o><xsl:where-populated><xsl:text>1:</xsl:text><xsl:comment select="head(//rec/@k)"/><xsl:text>2:</xsl:text><xsl:comment select="unparsed-entity-uri('pic')"/></xsl:where-populated></o>`,
		} {
			coreCheck(t, coreSheet(body), dir, name+": "+body, true, TransformOptions{})
		}
	}
}

// TestStreamCommentSelectValues: xsl:comment and xsl:processing-instruction
// make simple content of their select, so a select of elements is read as
// their values, not collected as grounded copies, also below a loop whose
// match may be the document element (streamexec.go noteStreamSelect,
// streamcopy_l3leaf.go).
func TestStreamCommentSelectValues(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{
			`<o><xsl:for-each select="outermost(//rec)"><in><xsl:where-populated><xsl:comment select="sub[parent::rec]"/></xsl:where-populated>` +
				`<xsl:where-populated><xsl:comment select="count(ancestor::node())"/></xsl:where-populated></in></xsl:for-each></o>`,
			`<o><xsl:for-each select="outermost(//rec)"><in><xsl:sequence select="()"/><xsl:on-empty><xsl:comment select="sub[parent::rec]"/>` +
				`<xsl:comment select="count(ancestor::node())"/></xsl:on-empty></in></xsl:for-each></o>`,
			`<o><xsl:for-each select="outermost(//rec)"><in><xsl:processing-instruction name="p" select="sub"/></in></xsl:for-each></o>`,
			`<o><xsl:comment select="doc/rec"/></o>`,
		} {
			coreCheck(t, coreSheet(body), dir, name+": "+body, true, TransformOptions{})
		}
	}
}

// TestStreamValueOfTextBeside: value-of of text nodes beside a consuming
// path of elements reads the path as values and keeps the text nodes'
// merging: two text nodes with no match between them are one string, as in
// the tree (streamcopy_l3text.go).
func TestStreamValueOfTextBeside(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, rule := range []string{
			`<r><xsl:value-of select="$t, sub, $t"/></r>`,
			`<r><xsl:value-of select="$t, sub[@x = '9'], $t" separator=":"/></r>`,
			`<r><xsl:value-of select="$t, $e, sub, $t, $t"/></r>`,
			`<r a="{$t, sub, $t}"/>`,
		} {
			sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:variable name="t" as="text()"><xsl:text>##</xsl:text></xsl:variable>
<xsl:variable name="e" as="text()"><xsl:value-of select="''"/></xsl:variable>
<xsl:mode name="s" streamable="yes"/>
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml"><xsl:apply-templates select="." mode="s"/></xsl:source-document></top></xsl:template>
<xsl:template match="rec" mode="s">` + rule + `</xsl:template>
<xsl:template match="text()" mode="s"/>
</xsl:stylesheet>`
			coreCheck(t, sheet, dir, name+": "+rule, true, TransformOptions{})
		}
	}
}

// TestStreamApplyFreshCopies: apply-templates over copies -- copy-of() of a
// rule's context element, text() ! snapshot(.) -- in a mode that need not be
// streamable reads the copies off the cursor and applies the templates to
// them as the tree does (streamcopy_l3apply.go).
func TestStreamApplyFreshCopies(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, rule := range []string{
			`<xsl:template match="rec"><xsl:apply-templates select="copy-of()" mode="change"/></xsl:template>`,
			`<xsl:template match="rec"><xsl:apply-templates select="copy-of(.)" mode="change"/></xsl:template>`,
			`<xsl:template match="rec"><xsl:apply-templates select="sub/text() ! snapshot(.)" mode="change"/></xsl:template>`,
		} {
			sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:mode streamable="yes" on-no-match="shallow-copy"/>
<xsl:mode name="change" on-no-match="shallow-copy"/>
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml"><xsl:apply-templates select="."/></xsl:source-document></top></xsl:template>
` + rule + `
<xsl:template match="rec[@k = 'a']/sub" mode="change"><s n="{count(ancestor::*)}" b="{ends-with(base-uri(), 'doc.xml')}"><xsl:value-of select="@x"/></s></xsl:template>
<xsl:template match="text()" mode="change"><t a="{string-join(ancestor::*/name(), '/')}"><xsl:value-of select="."/></t></xsl:template>
</xsl:stylesheet>`
			coreCheck(t, sheet, dir, name+": "+rule, true, TransformOptions{})
		}
	}
}
