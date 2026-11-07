package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
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
				// tail() leaves out the document element, which a copy
				// writes as events (copiesReadOut): the tree's <o/>.
				`<o><xsl:sequence select="tail(*)"/></o>`,
				`<o><xsl:copy-of select="tail(/*)"/></o>`,
			} {
				coreCheck(t, coreSheetWith(decls, body), dir, name+": "+decls+body, true, TransformOptions{})
			}
		}
	}
}

// dtdOpts let the rows' documents declare a DTD (the resolver refuses a
// DOCTYPE by default).
var dtdOpts = TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}}

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
			coreCheck(t, coreSheet(body), dir, name+": "+body, true, dtdOpts)
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
		coreCheck(t, coreSheet(body), dir, body, true, dtdOpts)
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

// TestStreamGroupByEventCopies: group-by over the children of a rule's
// context that may be the document element, its body reading only what a
// copy of each member has, holds the members as one fragment of copies made
// off the cursor (streamcopy_l3group.go).
func TestStreamGroupByEventCopies(t *testing.T) {
	dir := t.TempDir()
	docs := map[string]string{"nsbase": `<doc xmlns:p="urn:p" xml:base="http://x/a/"><rec k="a" v="1" xmlns:r="urn:r">` +
		`<p:sub xmlns="urn:d" xmlns:q="urn:q" q:y="1" xml:base="b/"><!--c--><?pi x?>t<e xmlns="">u</e></p:sub></rec>` +
		`<rec k="b" v="2" xmlns:p="urn:p2"><p:sub p:z="2"/></rec><rec k="a" v="3" xml:base="../c/"><sub/></rec></doc>`}
	for name, doc := range coreDocs {
		docs[name] = doc
	}
	for name, doc := range docs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			body   string
			stream bool
		}{
			{`<g p="{position()}" k="{current-grouping-key()}"><xsl:value-of select="sum(current-group()/@v[. castable as xs:double])"/></g>`, true},
			{`<xsl:variable name="m" select="map { 'x' : current-group()/sub/@x/string(), 'n' : count(current-group()) }"/><g n="{$m?n}"><xsl:value-of select="$m?x" separator=","/></g>`, true},
			{`<g k="{current-grouping-key()}"><xsl:copy-of select="current-group()/sub"/></g>`, true},
			{`<g k="{current-grouping-key()}"><xsl:copy-of select="current-group()"/></g>`, true},
			// An attribute value template on a namespaced attribute is read too.
			{`<g xmlns:q="urn:q" q:a="{count(ancestor::*)}" n="{count(current-group())}"/>`, true},
			// Two consuming operands (current-group() is consuming, §19.8.9.4):
			// roaming and free-ranging by §19.8.1's general rules, a verdict
			// the core does not trust.
			{`<g p="{position()}" k="{current-grouping-key()}" n="{count(current-group())}"><xsl:value-of select="sum(current-group()/@v[. castable as xs:double])"/></g>`, false},
			{`<xsl:variable name="m" select="map { 'x' : current-group()/sub/@x/string() }"/><g><xsl:value-of select="$m?x" separator=","/><xsl:copy-of select="current-group()/sub"/></g>`, false},
		} {
			for _, by := range []string{`group-by="@k"`, `group-by="@k, @v" composite="yes"`} {
				sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
<xsl:mode name="s" streamable="yes"/>
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml"><xsl:apply-templates select="." mode="s"/></xsl:source-document></top></xsl:template>
<xsl:template match="doc | rec" mode="s"><o><xsl:fork><xsl:for-each-group select="rec" ` + by + `>` + c.body + `</xsl:for-each-group></xsl:fork></o></xsl:template>
<xsl:template match="text()" mode="s"/>
</xsl:stylesheet>`
				coreCheck(t, sheet, dir, name+": "+by+c.body, c.stream, TransformOptions{})
			}
		}
	}
}

// TestStreamForkGroupByMaxNodes: the members a group-by in xsl:fork copies
// off the cursor (streamcopy_l3group.go) stand for the source's, which the
// tree does not charge to MaxNodes (A2-3): TransformReader succeeds where the
// tree does, and a body copying the members meets the tree's bound.
func TestStreamForkGroupByMaxNodes(t *testing.T) {
	var d strings.Builder
	d.WriteString("<doc>")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&d, `<rec k="%c" v="%d"><c>x</c></rec>`, 'a'+i%4, i)
	}
	d.WriteString("</doc>")
	for _, body := range []string{
		`<g k="{current-grouping-key()}" s="{sum(current-group()/@v)}"/>`,
		`<g><xsl:copy-of select="current-group()"/></g>`,
	} {
		sheet := mainSheet(`<xsl:mode streamable="yes"/><xsl:template match="/doc"><o><xsl:fork>` +
			`<xsl:for-each-group select="rec" group-by="@k">` + body + `</xsl:for-each-group></xsl:fork></o></xsl:template>`)
		for _, max := range []int{500, 5000} {
			opts := TransformOptions{MaxNodes: max}
			so, st := mainRun(sheet, d.String(), true, opts)
			to, _ := mainRun(sheet, d.String(), false, opts)
			if so != to {
				t.Errorf("MaxNodes %d, %s:\nstream %.200s\ntree   %.200s", max, body, so, to)
			}
			if st.Streamed() == 0 || st.Guards != 0 {
				t.Errorf("MaxNodes %d, %s: streamed %d, guards %d", max, body, st.Streamed(), st.Guards)
			}
		}
	}
}

// TestStreamForkGroupByCopiesAllocate: the members a group-by in xsl:fork
// copies off the cursor are written straight into one tree's records, no
// builder or view per node (A2-7): the run allocates about what the tree's
// parse and grouping do, where a builder per node allocated 2.3 times that.
func TestStreamForkGroupByCopiesAllocate(t *testing.T) {
	var d strings.Builder
	d.WriteString("<doc>\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&d, `<rec n="%d" k="%c" v="%d"><a>alpha %d</a><b x="%d">bravo %d</b><sub><c>%d</c><c>%d</c></sub></rec>`+"\n",
			i, 'a'+i/10%4, i%97, i, i%7, i, i%13, i%17)
	}
	d.WriteString("</doc>")
	sheet := mainSheet(`<xsl:mode streamable="yes"/><xsl:template match="/doc"><o><xsl:fork>` +
		`<xsl:for-each-group select="rec" group-by="@k"><g k="{current-grouping-key()}" s="{sum(current-group()/@v)}"/>` +
		`</xsl:for-each-group></xsl:fork></o></xsl:template>`)
	run := func(stream bool) (string, uint64) {
		var m0, m1 goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&m0)
		out, tr := mainRun(sheet, d.String(), stream, TransformOptions{})
		goruntime.ReadMemStats(&m1)
		if stream && (tr.Streamed() == 0 || tr.Guards != 0) {
			t.Fatalf("streamed %d, guards %d", tr.Streamed(), tr.Guards)
		}
		return out, m1.TotalAlloc - m0.TotalAlloc
	}
	so, sa := run(true)
	to, ta := run(false)
	if so != to {
		t.Fatalf("stream %.200s\ntree   %.200s", so, to)
	}
	t.Logf("allocated: stream %d, tree %d (%.2fx)", sa, ta, float64(sa)/float64(ta))
	if float64(sa) > 1.3*float64(ta) {
		t.Errorf("stream allocates %.2fx the tree's bytes (%d vs %d)", float64(sa)/float64(ta), sa, ta)
	}
}

// TestStreamForkGroupByMemberReads: what of each member a group-by in
// xsl:fork copies is what the key, the body and its sorts read through the
// context item and current-group(), by attribute and child name; any read
// that cannot be named copies members whole (nil) (A3-5).
func TestStreamForkGroupByMemberReads(t *testing.T) {
	for _, c := range []struct {
		by, body    string
		attrs, kids string // comma-separated local names; "-" for whole members
	}{
		{`@k`, `<g n="{count(current-group())}" e="{exists(current-group())}{empty(current-group())}"/>`, "k", ""},
		{`string(@k)`, `<g k="{current-grouping-key()}" p="{position()}/{last()}"/>`, "k", ""},
		{`@k`, `<g s="{sum(current-group()/@v)}" m="{max(current-group()/a)}"/>`, "k,v", "a"},
		{`@k, @v`, `<g j="{string-join(current-group()/a/b, ',')}" n="{name()}{local-name()}" f="{@w}"/>`, "k,v,w", "a"},
		{`a`, `<xsl:sort select="@s"/><xsl:copy-of select="current-group()/b[@x = 1]"/>`, "s", "a,b"},
		{`./@k`, `<xsl:value-of select="sub/c ! string(.)"/>`, "k", "sub"},
		{`@k`, `<g s="{sum(current-group()/@v[. castable as xs:double])}"/>`, "k,v", ""},
		{`@k`, `<xsl:copy-of select="current-group()"/>`, "-", ""},
		{`@k`, `<g n="{count(current-group()/a)}"/>`, "k", "a"},
		{`@k`, `<g s="{string(.)}"/>`, "-", ""},
		{`@k`, `<g s="{string()}"/>`, "-", ""},
		{`@k`, `<g s="{.}"/>`, "-", ""},
		{`@k`, `<g s="{current()/@v}"/>`, "-", ""},
		{`@k`, `<g s="{count(@*)}"/>`, "-", ""},
		{`@k`, `<g s="{count(*)}"/>`, "-", ""},
		{`@k`, `<g s="{count(text())}"/>`, "-", ""},
		{`@k`, `<g s="{count(.//c)}"/>`, "-", ""},
		{`@k`, `<g s="{count(current-group()[1]/a)}"/>`, "-", ""},
		{`@k`, `<g s="{sum(current-group() ! @v)}"/>`, "-", ""},
		{`@k`, `<xsl:variable name="m" select="current-group()"/><g n="{count($m)}"/>`, "-", ""},
		{`@k`, `<xsl:copy/>`, "-", ""},
		{`@k`, `<xsl:for-each-group select="current-group()/a" group-adjacent="."><g/></xsl:for-each-group>`, "-", ""},
		{`@k`, `<xsl:for-each select="1 to 2"><g n="{.}"/></xsl:for-each>`, "-", ""},
		{`*:k`, `<g/>`, "-", ""},
		{`@k`, `<g xmlns:q="urn:q" q:s="{sum(current-group()/@w)}"/>`, "k,w", ""},
	} {
		sheet := mainSheet(`<xsl:template match="/doc"><xsl:for-each-group select="rec" group-by="` + c.by + `">` +
			c.body + `</xsl:for-each-group></xsl:template>`)
		tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		el := tree.Root.FirstChild().FirstChild().FirstChild()
		if !copyFreeBody(el) {
			t.Fatalf("%s %s: not copy-free", c.by, c.body)
		}
		got, want := "-", c.attrs+"|"+c.kids
		if k := prunedMembers(el); k != nil {
			var as, ks []string
			for _, q := range k.attrs {
				as = append(as, q.Local)
			}
			for _, q := range k.kids {
				ks = append(ks, q.Local)
			}
			got = strings.Join(as, ",") + "|" + strings.Join(ks, ",")
		}
		if c.attrs == "-" {
			want = "-"
		}
		if got != want {
			t.Errorf("group-by=%q %s: keeps %s, want %s", c.by, c.body, got, want)
		}
	}
	// A pattern is not read: a sorted group-starting-with keeps members whole.
	tree, err := xdm.ParseString(mainSheet(`<xsl:template match="/doc"><xsl:for-each-group select="rec" group-starting-with="rec[@k]">`+
		`<xsl:sort select="@s"/><g/></xsl:for-each-group></xsl:template>`), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if k := prunedMembers(tree.Root.FirstChild().FirstChild().FirstChild()); k != nil {
		t.Errorf("group-starting-with keeps %v, want whole members", k)
	}
}

// TestStreamForkGroupByPrunedMembers: a group-by in xsl:fork whose key and
// body read members only by attribute and child name holds copies of only
// those (A3-5): the tree's own grouping runs over them, so values, the
// order of keys and bodies, and the errors either raises are the tree's,
// and a count-only body over bulky members allocates a fraction of the
// tree's bytes.
func TestStreamForkGroupByPrunedMembers(t *testing.T) {
	doc := `<doc><rec k="1" v="2" w="a"><a>x<b>1</b></a><b x="1">y</b><!--c-->t</rec><rec k="2" v="x"><a>z</a></rec>` +
		`<rec k="1" v="3"><b x="2"/></rec><rec k="02" v="4" w="b"/></doc>`
	bad := strings.Replace(doc, `k="02"`, `k="z"`, 1)
	for _, by := range []string{`@k`, `xs:integer(@k)`} {
		for _, body := range []string{
			`<g k="{current-grouping-key()}" n="{count(current-group())}"/>`,
			`<g e="{exists(current-group())}"/>`,
			`<g s="{sum(current-group()/@v)}"/>`,
			`<g m="{max(current-group()/@v ! number())}"/>`,
			`<g j="{string-join(current-group()/a, ',')}"/>`,
			`<g xmlns:q="urn:q" q:s="{sum(current-group()/@v)}"/>`,
			`<g><xsl:copy-of select="current-group()/b"/></g>`,
			`<xsl:if test="count(current-group()) gt 1"><g n="{name()}" p="{position()}"/></xsl:if>`,
		} {
			sheet := mainSheet(`<xsl:mode streamable="yes"/><xsl:template match="/doc"><o><xsl:fork>` +
				`<xsl:for-each-group select="rec" group-by="` + by + `">` + body + `</xsl:for-each-group></xsl:fork></o></xsl:template>`)
			for _, d := range []string{doc, bad} {
				so, st := mainRun(sheet, d, true, TransformOptions{})
				to, _ := mainRun(sheet, d, false, TransformOptions{})
				if so != to {
					t.Errorf("group-by=%q %s on %.60s:\nstream %.300s\ntree   %.300s", by, body, d[50:], so, to)
				}
				if st.Streamed() == 0 || st.Guards != 0 {
					t.Errorf("group-by=%q %s: streamed %d, guards %d", by, body, st.Streamed(), st.Guards)
				}
			}
		}
	}

	var d strings.Builder
	d.WriteString("<doc>\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&d, `<rec k="%c"><a>alpha %d</a><b x="%d">bravo text %d</b><sub><c>%d</c><c>%d</c><c>%d</c></sub></rec>`+"\n",
			'a'+i/10%4, i, i%7, i, i%13, i%17, i)
	}
	d.WriteString("</doc>")
	sheet := mainSheet(`<xsl:mode streamable="yes"/><xsl:template match="/doc"><o><xsl:fork>` +
		`<xsl:for-each-group select="rec" group-by="@k"><g k="{current-grouping-key()}" n="{count(current-group())}"/>` +
		`</xsl:for-each-group></xsl:fork></o></xsl:template>`)
	run := func(stream bool) (string, uint64) {
		var m0, m1 goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&m0)
		out, _ := mainRun(sheet, d.String(), stream, TransformOptions{})
		goruntime.ReadMemStats(&m1)
		return out, m1.TotalAlloc - m0.TotalAlloc
	}
	so, sa := run(true)
	to, ta := run(false)
	if so != to {
		t.Fatalf("stream %.200s\ntree   %.200s", so, to)
	}
	t.Logf("count-only body allocated: stream %d, tree %d (%.2fx)", sa, ta, float64(sa)/float64(ta))
	if float64(sa) > 0.6*float64(ta) {
		t.Errorf("a count-only body allocates %.2fx the tree's bytes (%d vs %d)", float64(sa)/float64(ta), sa, ta)
	}
}

// TestCopyFreeBodyReadsEveryRoute: copyFreeBody sees every expression a
// body evaluates, or refuses: attribute value templates on namespaced
// attributes of literal result elements, attribute sets (their bodies are
// elsewhere), shadow attributes (made at compile time), and grouping
// patterns, which may test a member's parent or position.
func TestCopyFreeBodyReadsEveryRoute(t *testing.T) {
	for _, c := range []struct {
		attrs, body string
		ok          bool
	}{
		{`group-by="@k"`, `<g xmlns:q="urn:q" q:a="{@k}"/>`, true},
		{`group-by="@k"`, `<g xmlns:q="urn:q" q:a="{count(ancestor::*)}"/>`, false},
		{`group-by="@k"`, `<g xmlns:q="urn:q" q:a="{generate-id(.)}"/>`, false},
		{`group-by="@k"`, `<g xsl:use-attribute-sets="s"/>`, false},
		{`group-by="@k"`, `<xsl:element name="g" use-attribute-sets="s"/>`, false},
		{`group-by="@k"`, `<xsl:copy select="current-group()[1]" use-attribute-sets="s"/>`, false},
		{`group-by="@k"`, `<g><xsl:value-of _select="{'path(.)'}"/></g>`, false},
		{`group-by="@k"`, `<g xsl:_use-attribute-sets="{'s'}"/>`, false},
		{`group-by="@k" xsl:use-when="true()"`, `<g/>`, true},
		{`group-starting-with="rec[@k = 'a']"`, `<xsl:sort select="@k"/><g/>`, true},
		{`group-starting-with="doc/rec"`, `<xsl:sort select="@k"/><g/>`, false},
		{`group-starting-with="rec[exists(parent::doc)]"`, `<xsl:sort select="@k"/><g/>`, false},
		{`group-ending-with="rec[root(.)]"`, `<xsl:sort select="@k"/><g/>`, false},
		{`group-ending-with="rec[2]"`, `<xsl:sort select="@k"/><g/>`, false},
		{`group-adjacent="count(ancestor::*)"`, `<xsl:sort select="@k"/><g/>`, false},
		{`group-by="@k"`, `<xsl:for-each-group select="current-group()" group-starting-with="//rec"><g/></xsl:for-each-group>`, false},
		{`group-by="@k"`, `<xsl:merge><xsl:merge-source for-each-item="root(.)" select="."><xsl:merge-key select="1"/></xsl:merge-source><xsl:merge-action/></xsl:merge>`, false},
	} {
		sheet := mainSheet(`<xsl:attribute-set name="s"><xsl:attribute name="p" select="path(.)"/></xsl:attribute-set>` +
			`<xsl:template match="/doc"><xsl:for-each-group select="rec" ` + c.attrs + `>` + c.body + `</xsl:for-each-group></xsl:template>`)
		tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		el := tree.Root.FirstChild().FirstChild().NextSibling().FirstChild()
		if got := copyFreeBody(el); got != c.ok {
			t.Errorf("%s %s: copy-free %v, want %v", c.attrs, c.body, got, c.ok)
		}
	}
}

// TestStreamValidatedCopyOnStream: a validated copy of the document element
// (copy-of select="*" with validation or a type: si-copy-of-121 to -128) is
// built off the cursor rather than the document re-read on the tree, and a
// schema-invalid construction (XTTE1510/1512/1540, §2.12) is returned as the
// stream meets it, the tree's error, with no re-run -- unless the rest of the
// document does not parse, which the tree reports first.
func TestStreamValidatedCopyOnStream(t *testing.T) {
	decls := `<xsl:import-schema><xs:schema>
 <xs:element name="r"><xs:complexType><xs:sequence><xs:element name="n" type="xs:integer" maxOccurs="unbounded"/></xs:sequence></xs:complexType></xs:element>
 <xs:element name="m" type="xs:integer"/></xs:schema></xsl:import-schema>`
	copyOf := func(attrs string) string {
		return `<xsl:variable name="c" as="element()"><xsl:copy-of select="*" ` + attrs + `/></xsl:variable>` +
			`<v><xsl:value-of select="$c/n[2] instance of xs:integer"/></v>`
	}
	for _, c := range []struct {
		name, doc, body string
		rerun           bool
	}{
		{"strict", `<r><n>1</n><n>2</n></r>`, copyOf(`validation="strict"`), false},
		{"lax", `<r><n>1</n><n>2</n></r>`, copyOf(`validation="lax"`), false},
		{"type", `<r><n>1</n><n>2</n></r>`, copyOf(`type="xs:anyType"`), false},
		{"strict invalid", `<r><n>1</n><n>x</n></r>`, copyOf(`validation="strict"`), false},
		{"undeclared", `<q><n>1</n></q>`, copyOf(`validation="strict"`), false},
		{"lre invalid", `<r><n>1</n><n>x</n></r>`,
			`<xsl:for-each select="r/n"><m xsl:validation="strict"><xsl:value-of select="."/></m></xsl:for-each>`, false},
		{"element type invalid", `<r><n>1</n><n>x</n></r>`,
			`<xsl:for-each select="r/n"><xsl:element name="m" type="xs:integer"><xsl:value-of select="."/></xsl:element></xsl:for-each>`, false},
		{"invalid, then malformed", `<r><n>x</n><n>1</n></r><junk`, copyOf(`validation="strict"`), true},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(c.doc), 0o600); err != nil {
			t.Fatal(err)
		}
		sheet := coreSheetWith(decls, c.body)
		so, se, st := coreRun(sheet, dir, true, TransformOptions{})
		to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
		if so != to || se != te {
			t.Errorf("%s:\ncore %q %q\ntree %q %q", c.name, so, se, to, te)
		}
		if st.Runs[streamtrace.Core] == 0 || st.Fallbacks != 0 || st.Guards != 0 || (st.Reruns != 0) != c.rerun {
			t.Errorf("%s: %+v (error %q), want a streamed run, re-run %v", c.name, st, se, c.rerun)
		}
	}
}
