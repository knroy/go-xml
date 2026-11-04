package xslt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xdmsink"
)

// tryOutDoc has records whose @v is a number, except where it is not.
const tryOutDoc = `<doc><rec k="1" v="10"><n>one</n></rec><rec k="2" v="20"><n>two</n></rec>` +
	`<rec k="3" v="x"><n>three</n></rec><rec k="4" v="40"><n>four</n></rec></doc>`

// tryOutSheets are xsl:try shapes under TransformOptions.Output: fwd says
// whether the try's buffer must hand items on before the try ends.
var tryOutSheets = []struct {
	name, decls, body string
	fwd               bool
	code              string // Output and tree fail alike with it
}{
	{"loop-ok", ``, `<xsl:template match="/"><out><xsl:try><xsl:for-each select="//rec[@v castable as xs:integer]"><r k="{@k}"><xsl:value-of select="xs:integer(@v) * 2"/></r></xsl:for-each><xsl:catch><bad/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"loop-fail", ``, `<xsl:template match="/"><out><xsl:try><xsl:for-each select="//rec"><r k="{@k}"><xsl:value-of select="xs:integer(@v) * 2"/></r></xsl:for-each><xsl:catch><bad code="{$err:code}"/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"loop-fail-text-catch", ``, `<xsl:template match="/"><out><xsl:try><xsl:for-each select="//rec"><r k="{@k}"><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each><xsl:catch>caught</xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"loop-fail-empty-catch", ``, `<xsl:template match="/"><out><xsl:try><xsl:for-each select="//rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"after-child", ``, `<xsl:template match="/"><out><first/><xsl:try><xsl:for-each select="//rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each><xsl:catch><bad/></xsl:catch></xsl:try><xsl:attribute name="late">1</xsl:attribute></out></xsl:template>`, true, "XTDE0410"},
	// Text just before the try is left unwritten (tryfwd.go settle): what
	// the try hands on merges with it, and a rollback puts it back.
	{"after-text", ``, `<xsl:template match="/"><out>text<xsl:try><xsl:for-each select="//rec[position() lt 3]"><r/></xsl:for-each>more<xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"after-text-fail", ``, `<xsl:template match="/"><out>text<xsl:try><r/><s/><xsl:sequence select="error()"/><xsl:catch>caught<c/></xsl:catch></xsl:try>end</out></xsl:template>`, true, ""},
	{"after-text-merge", ``, `<xsl:template match="/"><out>text<xsl:try>more<xsl:value-of select="1, 2"/><r/><s/><xsl:catch>caught</xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"after-text-merge-fail", ``, `<xsl:template match="/"><out>text<xsl:try>more<r/>t2<s/><xsl:sequence select="error()"/><xsl:catch>caught<c/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"after-text-catch-empty", ``, `<xsl:template match="/"><out>text<xsl:try>more<r/><xsl:sequence select="error()"/><xsl:catch/></xsl:try>after<z/></out></xsl:template>`, true, ""},
	{"after-text-element", ``, `<xsl:template match="/"><out>text<xsl:try><all><xsl:for-each select="//rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each></all><xsl:catch>caught</xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"after-text-cdata", `<xsl:output cdata-section-elements="out"/>`, `<xsl:template match="/"><out>a]]<xsl:try>&gt;b<r/>c<xsl:sequence select="error()"/><xsl:catch>]]&gt;d</xsl:catch></xsl:try>e</out></xsl:template>`, true, ""},
	{"after-text-cdata-ok", `<xsl:output cdata-section-elements="out"/>`, `<xsl:template match="/"><out>a]]<xsl:try>&gt;b<r/>c<xsl:catch>x</xsl:catch></xsl:try>e</out></xsl:template>`, true, ""},
	{"after-space-indent", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><out><o/><xsl:text>  </xsl:text><xsl:try><r><n/></r>x<xsl:sequence select="error()"/><xsl:catch><c><d/></c></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"after-space-indent-text", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><out><o/><xsl:text>  </xsl:text><xsl:try><r><n/></r><xsl:sequence select="error()"/><xsl:catch>t<c><d/></c></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"after-text-nested", ``, `<xsl:template match="/"><out>t0<xsl:try>t1<xsl:try>t2<a/><xsl:sequence select="error()"/><xsl:catch>c2</xsl:catch></xsl:try>t3<b/><xsl:sequence select="error()"/><xsl:catch>c1</xsl:catch></xsl:try>t4</out></xsl:template>`, true, ""},
	{"after-text-html", `<xsl:output method="html"/>`, `<xsl:template match="/"><html><body>text<xsl:try><p>a</p><span>s</span><xsl:sequence select="error()"/><xsl:catch> caught <b>b</b></xsl:catch></xsl:try></body></html></xsl:template>`, true, ""},
	{"text-first", ``, `<xsl:template match="/"><out><a/><xsl:try>t1<xsl:value-of select="1, 2"/><xsl:sequence select="3, 4"/><b/>t2<xsl:catch/></xsl:try>t3<xsl:sequence select="5"/></out></xsl:template>`, true, ""},
	// The output method is not decided yet: the try decides it provisionally
	// (xdmbuild/tryfwd.go settle), a rollback leaves it to the catch.
	{"top", ``, `<xsl:template match="/"><xsl:try><a/><xsl:sequence select="1, 2"/><b/><xsl:catch/></xsl:try><c/></xsl:template>`, true, ""},
	{"top-html-fails", ``, `<xsl:template match="/"><xsl:try><html><body><p>a<br/></p></body></html><xsl:sequence select="error()"/><xsl:catch><doc><br/></doc></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"top-xml-fails-html-catch", ``, `<xsl:template match="/"><xsl:try><doc><br/></doc><xsl:sequence select="error()"/><xsl:catch><html><body><br/></body></html></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"top-html-ok", ``, `<xsl:template match="/"><xsl:try><html><head><title>t</title></head><body><xsl:for-each select="//rec"><p><xsl:value-of select="n"/><br/></p></xsl:for-each></body></html><xsl:catch><doc/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"top-text-first", ``, `<xsl:template match="/"><xsl:try><xsl:text>  </xsl:text><x/><y/><xsl:sequence select="error()"/><xsl:catch>text <html/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"top-nested", ``, `<xsl:template match="/"><xsl:try><xsl:try><html><body/></html><xsl:sequence select="error()"/><xsl:catch errors="err:XPTY0004"/></xsl:try><xsl:catch><xsl:try><doc/><e/><xsl:catch/></xsl:try></xsl:catch></xsl:try><f/></xsl:template>`, true, ""},
	{"top-two-tries", ``, `<xsl:template match="/"><xsl:try><html/><x/><xsl:sequence select="error()"/><xsl:catch/></xsl:try><xsl:try><doc/><y/><xsl:catch/></xsl:try></xsl:template>`, true, ""},
	{"top-catch-undecided", ``, `<xsl:template match="/"><xsl:try><html/><x/><xsl:sequence select="error()"/><xsl:catch>only text</xsl:catch></xsl:try></xsl:template>`, true, ""},
	// Items before the try that do not decide the method yet (whitespace,
	// atomics) are written once the sink begins, after the mark: a rollback
	// puts them back (TestStreamOutFuzz seed 2885, a built-in rule's
	// whitespace in a document level).
	{"top-space-before", ``, `<xsl:template match="/"><xsl:text> </xsl:text><xsl:text>&#10;</xsl:text><xsl:try>t<x><y/></x><xsl:sequence select="error()"/><xsl:catch><c/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"top-space-before-ok", ``, `<xsl:template match="/"><xsl:text> </xsl:text><xsl:try>t<x><y/></x><xsl:catch><c/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"top-atomic-before", ``, `<xsl:template match="/"><xsl:sequence select="' ', ' '"/><xsl:try><x><y/></x><xsl:sequence select="error()"/><xsl:catch>c<c/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"copy-document-space-before", ``, `<xsl:template match="/"><xsl:copy><xsl:text> </xsl:text><xsl:text>&#10;</xsl:text><xsl:try>t<x><y/></x><xsl:sequence select="error()"/><xsl:catch><c/></xsl:catch></xsl:try></xsl:copy></xsl:template>`, true, ""},
	{"builtin-text-only-copy", `<xsl:mode on-no-match="text-only-copy"/>`, `<xsl:template match="/"><xsl:text> </xsl:text><xsl:apply-templates/></xsl:template><xsl:template match="rec[@k = '1']"><xsl:try><xsl:text>t</xsl:text><tw><tz/><xsl:copy-of select="n"/><xsl:attribute name="late" select="'x'"/></tw><xsl:catch><caught/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"top-atomic", ``, `<xsl:template match="/"><xsl:try><xsl:sequence select="1, 2"/><html/><xsl:sequence select="error()"/><xsl:catch><html/><z/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"top-started", ``, `<xsl:template match="/"><z/><y/><xsl:try><a/><xsl:sequence select="1, 2"/><b/><xsl:catch/></xsl:try><c/></xsl:template>`, true, ""},
	{"top-fail", ``, `<xsl:template match="/"><z/><y/><xsl:try><a/><xsl:sequence select="1, 2"/><b/><xsl:sequence select="error()"/><xsl:catch><caught/></xsl:catch></xsl:try><c/></xsl:template>`, true, ""},
	// Attributes first go on to the element the try writes to while it has
	// nothing in it (tryfwd.go attrsOK); a rollback puts the element back.
	{"first-attr", ``, `<xsl:template match="/"><out><xsl:try><xsl:attribute name="a">1</xsl:attribute><x/><y/><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"first-attr-fail", ``, `<xsl:template match="/"><out k="0"><xsl:try><xsl:attribute name="a">1</xsl:attribute><xsl:attribute name="b">2</xsl:attribute><x/><y/><xsl:sequence select="error()"/><xsl:catch><xsl:attribute name="c">3</xsl:attribute><z/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"first-attr-dup", ``, `<xsl:template match="/"><out a="old" b="keep"><xsl:try><xsl:attribute name="a">new</xsl:attribute><x/><y/><xsl:sequence select="error()"/><xsl:catch><z/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"first-attr-dup-ok", ``, `<xsl:template match="/"><out a="old" b="keep"><xsl:try><xsl:attribute name="a">new</xsl:attribute><x/><y/><xsl:catch><z/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"first-attr-ns", ``, `<xsl:template match="/"><out><xsl:try><xsl:attribute name="p:a" namespace="urn:p">1</xsl:attribute><xsl:attribute name="q:b" namespace="urn:q">2</xsl:attribute><x/><y/><xsl:sequence select="error()"/><xsl:catch><xsl:attribute name="p:c" namespace="urn:other">3</xsl:attribute><z/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"first-attr-element", ``, `<xsl:template match="/"><out><xsl:try><xsl:attribute name="a">1</xsl:attribute><all><xsl:for-each select="//rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each></all><xsl:catch>caught</xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"first-attr-indent", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><doc><out><xsl:try><xsl:attribute name="a">1</xsl:attribute><x><y/></x><xsl:sequence select="error()"/><xsl:catch>text</xsl:catch></xsl:try></out></doc></xsl:template>`, true, ""},
	{"first-attr-only", ``, `<xsl:template match="/"><out><xsl:try><xsl:attribute name="a">1</xsl:attribute><xsl:attribute name="b">2</xsl:attribute><xsl:catch/></xsl:try><x/></out></xsl:template>`, true, ""},
	{"first-attr-no", ``, `<xsl:template match="/"><out><xsl:try rollback-output="no"><xsl:attribute name="a">1</xsl:attribute><x/><y/><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"first-attr-no-fail", ``, `<xsl:template match="/"><out><xsl:try rollback-output="no"><xsl:attribute name="a">1</xsl:attribute><x/><y/><xsl:sequence select="error()"/><xsl:catch/></xsl:try></out></xsl:template>`, true, "XTDE3530"},
	{"first-attr-nested", ``, `<xsl:template match="/"><out><xsl:try><xsl:attribute name="a">1</xsl:attribute><x/><w><xsl:try><xsl:attribute name="b">2</xsl:attribute><y/><z/><xsl:sequence select="error()"/><xsl:catch><c/></xsl:catch></xsl:try></w><xsl:sequence select="error()"/><xsl:catch><o/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"first-attr-after-content", ``, `<xsl:template match="/"><out><a/><xsl:try><xsl:attribute name="a">1</xsl:attribute><x/><y/><xsl:catch><c/></xsl:catch></xsl:try></out></xsl:template>`, false, ""},
	{"first-attr-level", ``, `<xsl:template match="/"><z/><xsl:try><xsl:attribute name="a">1</xsl:attribute><x/><y/><xsl:catch><c/></xsl:catch></xsl:try></xsl:template>`, false, "SENR0001"},
	{"attr-after-content", ``, `<xsl:template match="/"><out><xsl:try><x/><y/><xsl:attribute name="a">1</xsl:attribute><z/><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"nested", ``, `<xsl:template match="/"><out><xsl:try><xsl:for-each select="//rec"><xsl:try><r k="{@k}"/><s><xsl:value-of select="xs:integer(@v)"/></s><xsl:catch><bad k="{@k}"/></xsl:catch></xsl:try></xsl:for-each><xsl:catch><outer/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"nested-outer-fails", ``, `<xsl:template match="/"><out><xsl:try><xsl:for-each select="//rec"><xsl:try><r k="{@k}"/><s><xsl:value-of select="xs:integer(@v)"/></s><xsl:catch errors="err:XPTY0004"><bad/></xsl:catch></xsl:try></xsl:for-each><xsl:catch><outer code="{$err:code}"/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"nested-inner-uncaught", ``, `<xsl:template match="/"><out><xsl:try><a/><xsl:try><b/><c/><xsl:sequence select="error()"/><xsl:catch errors="err:XPTY0004"/></xsl:try><xsl:catch><outer/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"indent", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><out><xsl:try><xsl:for-each select="//rec"><r><n><xsl:value-of select="xs:integer(@v)"/></n></r></xsl:for-each><xsl:catch><bad/></xsl:catch></xsl:try><after/></out></xsl:template>`, true, ""},
	{"indent-ok", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><out><xsl:try><xsl:for-each select="//rec[@v castable as xs:integer]"><r><n><xsl:value-of select="@v"/></n></r></xsl:for-each><xsl:catch><bad/></xsl:catch></xsl:try><after/></out></xsl:template>`, true, ""},
	{"indent-flatten", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><out><o/><xsl:try><r><n/></r>text<xsl:sequence select="error()"/><xsl:catch><bad><x/></bad></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"indent-in-loop", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><out><xsl:for-each select="//rec"><xsl:try><r><n><xsl:value-of select="xs:integer(@v)"/></n></r><s/><xsl:catch>bad</xsl:catch></xsl:try></xsl:for-each></out></xsl:template>`, true, ""},
	{"ns", ``, `<xsl:template match="/"><out xmlns="urn:o"><xsl:try><a xmlns="urn:a"><b/></a><c xmlns=""/><p:d xmlns:p="urn:p"/><xsl:sequence select="error()"/><xsl:catch><e/></xsl:catch></xsl:try><f/></out></xsl:template>`, true, ""},
	{"ns-ok", ``, `<xsl:template match="/"><out xmlns="urn:o"><xsl:try><a xmlns="urn:a"><b/></a><c xmlns=""/><p:d xmlns:p="urn:p"/><xsl:catch><e/></xsl:catch></xsl:try><f/></out></xsl:template>`, true, ""},
	{"html", `<xsl:output method="html"/>`, `<xsl:template match="/"><html><body><xsl:try><p>a<b>b</b></p><span>s</span><script>if (a &lt; b) x();</script><xsl:sequence select="error()"/><xsl:catch><p>caught</p></xsl:catch></xsl:try></body></html></xsl:template>`, true, ""},
	{"cdata", `<xsl:output cdata-section-elements="n"/>`, `<xsl:template match="/"><out><n>a]]</n><xsl:try><n>]]&gt;b</n><m/><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"charmap", `<xsl:output use-character-maps="m"/><xsl:character-map name="m"><xsl:output-character character="o" string="0"/></xsl:character-map>`, `<xsl:template match="/"><out><xsl:try><xsl:for-each select="//rec"><n><xsl:value-of select="n"/></n></xsl:for-each><xsl:value-of select="xs:integer('x')"/><xsl:catch>no</xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"select", ``, `<xsl:template match="/"><out><xsl:try select="//rec/n"><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"select-fail", ``, `<xsl:template match="/"><out><xsl:try select="//rec/xs:integer(@v)"><xsl:catch>c</xsl:catch></xsl:try></out></xsl:template>`, false, ""},
	{"break", ``, `<xsl:template match="/"><out><xsl:iterate select="//rec"><xsl:try><r/><s/><xsl:if test="@k = 2"><xsl:break/></xsl:if><xsl:catch/></xsl:try></xsl:iterate></out></xsl:template>`, true, ""},
	{"map-item", ``, `<xsl:template match="/"><out><xsl:try><a/><b/><xsl:sequence select="map{1:2}"/><c/><xsl:catch>c</xsl:catch></xsl:try></out></xsl:template>`, true, "XTDE0450"},
	{"doc-item", ``, `<xsl:template match="/"><out><xsl:try><a/><xsl:document><d/>dt</xsl:document>x<xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"catch-opens", ``, `<xsl:template match="/"><out><xsl:try><a/><b/><xsl:sequence select="error()"/><xsl:catch><xsl:for-each select="//rec"><c/></xsl:for-each></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"empty-after-rollback", ``, `<xsl:template match="/"><out><xsl:try><a/><b/><xsl:sequence select="error()"/><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"two-tries", ``, `<xsl:template match="/"><out><xsl:try><a/><b/><xsl:sequence select="error()"/><xsl:catch><c/></xsl:catch></xsl:try><xsl:try><d/><e/><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"two-tries-indent", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><out><xsl:try><a><x/></a><b/><xsl:sequence select="error()"/><xsl:catch><c/></xsl:catch></xsl:try><xsl:try><d><y/></d><e/><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"per-record", ``, `<xsl:template match="/"><out><xsl:for-each select="//rec"><xsl:try><r k="{@k}"/><s><xsl:value-of select="xs:integer(@v)"/></s><xsl:catch><bad k="{@k}"/></xsl:catch></xsl:try></xsl:for-each></out></xsl:template>`, true, ""},
	// xsl:document builds its content in a builder of its own, held as a
	// tree and appended whole: a try in it is held with it. A document
	// node's content the sink streams (xsl:copy of the document node, the
	// built-in rule's) is opened by settle as its next append would.
	{"document-level", ``, `<xsl:template match="/"><xsl:document><a/><b/><xsl:try><c/><d/><xsl:sequence select="error()"/><xsl:catch><e/></xsl:catch></xsl:try><f/></xsl:document></xsl:template>`, false, ""},
	{"document-level-ok", ``, `<xsl:template match="/"><xsl:document><a/><b/><xsl:try><c/><xsl:sequence select="1, 2"/><d/><xsl:catch/></xsl:try><f/></xsl:document></xsl:template>`, false, ""},
	{"copy-document", ``, `<xsl:template match="/"><xsl:copy><a/><xsl:try><c/><d/><xsl:sequence select="error()"/><xsl:catch><e/></xsl:catch></xsl:try><f/></xsl:copy></xsl:template>`, true, ""},
	{"copy-document-ok", ``, `<xsl:template match="/"><xsl:copy><a/><xsl:try><c/>t<xsl:sequence select="1, 2"/><d/><xsl:catch/></xsl:try><f/></xsl:copy></xsl:template>`, true, ""},
	{"copy-document-first", ``, `<xsl:template match="/"><xsl:copy><xsl:try><c/><d/><xsl:sequence select="error()"/><xsl:catch>t<e/></xsl:catch></xsl:try></xsl:copy></xsl:template>`, true, ""},
	{"copy-document-element", ``, `<xsl:template match="/"><xsl:copy><xsl:try><all><xsl:for-each select="//rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each></all><xsl:catch><bad/></xsl:catch></xsl:try></xsl:copy></xsl:template>`, true, ""},
	{"copy-document-attr", ``, `<xsl:template match="/"><xsl:copy><a/><xsl:try><c/><xsl:attribute name="x">1</xsl:attribute><xsl:catch><e/></xsl:catch></xsl:try></xsl:copy></xsl:template>`, true, "XTDE0420"},
	{"builtin-document", ``, `<xsl:template match="doc"><xsl:try><out><xsl:for-each select="rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each></out><xsl:catch><bad/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"builtin-document-ok", ``, `<xsl:template match="doc"><xsl:try><out><xsl:for-each select="rec"><r><xsl:value-of select="@v"/></r></xsl:for-each></out><x/><xsl:catch><bad/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"held-element", ``, `<xsl:template match="/"><out><xsl:element name="h" namespace="urn:h" inherit-namespaces="no"><a/><xsl:try><c/><d/><xsl:sequence select="error()"/><xsl:catch><e/></xsl:catch></xsl:try></xsl:element></out></xsl:template>`, false, ""},
	{"html-head", `<xsl:output method="html"/>`, `<xsl:template match="/"><html><head><xsl:try><title>t</title><meta name="a" content="b"/><xsl:catch/></xsl:try></head><body/></html></xsl:template>`, true, ""},
	{"explicit-method-top", `<xsl:output method="xml"/>`, `<xsl:template match="/"><xsl:try><a/><b/><xsl:sequence select="error()"/><xsl:catch><c/></xsl:catch></xsl:try><xsl:try><d/><e/><xsl:catch/></xsl:try></xsl:template>`, true, ""},
	{"text-method", `<xsl:output method="text"/>`, `<xsl:template match="/"><out><xsl:try><a>1</a><b>2</b><xsl:sequence select="error()"/><xsl:catch>c</xsl:catch></xsl:try><xsl:try><a>3</a><b>4</b><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	// A body's top-level element is built in place in what the try writes
	// to (tryfwd.go start): its content is written provisionally as made.
	{"element-ok", ``, `<xsl:template match="/"><out><xsl:try><all><xsl:for-each select="//rec[@v castable as xs:integer]"><r k="{@k}"><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each></all><xsl:catch><bad/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"element-fail", ``, `<xsl:template match="/"><out><a/><xsl:try><all><xsl:for-each select="//rec"><r k="{@k}"><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each></all><xsl:catch><bad code="{$err:code}"/></xsl:catch></xsl:try><z/></out></xsl:template>`, true, ""},
	{"element-late-attr", ``, `<xsl:template match="/"><out><xsl:try><all><x/><y/><xsl:attribute name="a">1</xsl:attribute></all><xsl:catch><bad code="{$err:code}"/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"element-attrs", ``, `<xsl:template match="/"><out><xsl:try><all k="1"><xsl:attribute name="a">1</xsl:attribute><x/>t<y/></all><b/><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"element-indent", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><out><o/><xsl:try><all><xsl:for-each select="//rec"><r><n><xsl:value-of select="xs:integer(@v)"/></n></r></xsl:for-each></all><xsl:catch><bad><x/></bad></xsl:catch></xsl:try><after/></out></xsl:template>`, true, ""},
	{"element-indent-ok", `<xsl:output indent="yes"/>`, `<xsl:template match="/"><out><xsl:try><all><xsl:for-each select="//rec"><r><n><xsl:value-of select="@v"/></n></r></xsl:for-each></all><xsl:catch/></xsl:try><after/></out></xsl:template>`, true, ""},
	{"elements-text", ``, `<xsl:template match="/"><out><xsl:try><a><b/></a>text<c><d/>e</c><xsl:sequence select="1, 2"/><f/><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"elements-text-fail", ``, `<xsl:template match="/"><out><xsl:try><a><b/></a>text<c><d/>e</c><xsl:sequence select="error()"/><xsl:catch>c</xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"element-ns", ``, `<xsl:template match="/"><out xmlns="urn:o" xmlns:p="urn:p"><xsl:try><a xmlns="urn:a"><b/><p:c/><d xmlns=""><e/></d></a><xsl:catch/></xsl:try><f/></out></xsl:template>`, true, ""},
	{"element-held", ``, `<xsl:template match="/"><out><xsl:try><xsl:element name="h" namespace="urn:h" inherit-namespaces="no"><a/><b/></xsl:element><xsl:sequence select="error()"/><xsl:catch><e/></xsl:catch></xsl:try><xsl:try><xsl:element name="h" namespace="urn:h" inherit-namespaces="no"><a/><b/></xsl:element><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"element-top", `<xsl:output method="xml"/>`, `<xsl:template match="/"><xsl:try><all><xsl:for-each select="//rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each></all><xsl:catch><c/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"element-top-ok", `<xsl:output method="xml"/>`, `<xsl:template match="/"><xsl:try><all><xsl:for-each select="//rec"><r><xsl:value-of select="@v"/></r></xsl:for-each></all><xsl:catch><c/></xsl:catch></xsl:try></xsl:template>`, true, ""},
	{"element-html", `<xsl:output method="html"/>`, `<xsl:template match="/"><html><xsl:try><body><p>a<b>b</b></p><script>if (a &lt; b) x();</script><xsl:sequence select="error()"/></body><xsl:catch><body>caught</body></xsl:catch></xsl:try></html></xsl:template>`, true, ""},
	{"element-in-loop", ``, `<xsl:template match="/"><out><xsl:for-each select="//rec"><xsl:try><r k="{@k}"><s><xsl:value-of select="xs:integer(@v)"/></s></r><xsl:catch><bad k="{@k}"/></xsl:catch></xsl:try></xsl:for-each></out></xsl:template>`, true, ""},
	{"element-nested-tries", ``, `<xsl:template match="/"><out><xsl:try><a><xsl:try><b><c/></b><xsl:sequence select="error()"/><xsl:catch><d/></xsl:catch></xsl:try><e/></a><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"element-inner-first", ``, `<xsl:template match="/"><out><xsl:try><xsl:try><a><b/></a><xsl:catch/></xsl:try><c/><xsl:sequence select="error()"/><xsl:catch><o/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"element-inner-fails", ``, `<xsl:template match="/"><out><xsl:try><x/><xsl:try><a><b/><xsl:sequence select="error()"/></a><xsl:catch errors="err:XPTY0004"/></xsl:try><xsl:catch><o code="{$err:code}"/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	{"element-no", ``, `<xsl:template match="/"><out><xsl:try rollback-output="no"><all><xsl:for-each select="//rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each></all><xsl:catch><bad/></xsl:catch></xsl:try></out></xsl:template>`, true, "XTDE3530"},
	{"element-no-ok", ``, `<xsl:template match="/"><out><xsl:try rollback-output="no"><all><xsl:for-each select="//rec"><r/></xsl:for-each></all><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"element-then-attr", ``, `<xsl:template match="/"><out><xsl:try><a><b/></a><xsl:attribute name="late">1</xsl:attribute><xsl:catch><c/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
	// rollback-output="no": XTDE3530 when the body produced an item before
	// the error, a zero-length text node included; caught otherwise.
	{"no-written", ``, `<xsl:template match="/"><out><xsl:try rollback-output="no"><xsl:for-each select="//rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each><xsl:catch><bad/></xsl:catch></xsl:try></out></xsl:template>`, true, "XTDE3530"},
	{"no-nothing", ``, `<xsl:template match="/"><out><a/><xsl:try rollback-output="no"><xsl:value-of select="xs:integer('x')"/><xsl:catch><caught/></xsl:catch></xsl:try></out></xsl:template>`, false, ""},
	{"no-empty-text", ``, `<xsl:template match="/"><out><a/><xsl:try rollback-output="no"><xsl:text/><xsl:value-of select="xs:integer('x')"/><xsl:catch><caught/></xsl:catch></xsl:try></out></xsl:template>`, false, "XTDE3530"},
	{"no-ok", ``, `<xsl:template match="/"><out><xsl:try rollback-output="no"><xsl:for-each select="//rec"><r/></xsl:for-each><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"no-narrow", ``, `<xsl:template match="/"><out><xsl:try><xsl:try rollback-output="no"><a/><b/><xsl:sequence select="error()"/><xsl:catch errors="err:XPTY0004"/></xsl:try><xsl:catch><outer/></xsl:catch></xsl:try></out></xsl:template>`, false, ""},
	// An inner try's checkpoint hands the outer buffer's items on: after the
	// inner one rolls back, the outer one has produced items all the same.
	{"no-inner-rollback", ``, `<xsl:template match="/"><out><xsl:try rollback-output="no"><a/><b/><xsl:try><c/><d/><xsl:sequence select="error()"/><xsl:catch errors="err:XPTY0004"/></xsl:try><xsl:catch>caught</xsl:catch></xsl:try></out></xsl:template>`, true, "XTDE3530"},
	{"no-in-yes", ``, `<xsl:template match="/"><out><xsl:try><a/><xsl:try rollback-output="no"><b/><c/><xsl:catch/></xsl:try><xsl:sequence select="error()"/><xsl:catch><outer/></xsl:catch></xsl:try></out></xsl:template>`, true, ""},
}

func TestStreamTryOut(t *testing.T) {
	for _, c := range tryOutSheets {
		sheet := compileString(t, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" `+
			`xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:err="http://www.w3.org/2005/xqt-errors" `+
			`exclude-result-prefixes="xs err">`+c.decls+c.body+`</xsl:stylesheet>`)
		fwd := tryOutCheck(t, sheet, c.name, c.code)
		if (fwd > 0) != c.fwd {
			t.Errorf("%s: %d items handed on before the try ended, want some: %v", c.name, fwd, c.fwd)
		}
	}
}

// tryOutCheck compares Output with Result.Serialize for sheet over
// tryOutDoc (both fail with code, when it is set), for Transform and
// TransformReader, and returns how many items the streamed runs' try
// buffers handed on before their try ended.
func tryOutCheck(t *testing.T, sheet *Stylesheet, where, code string) int64 {
	t.Helper()
	var fwd int64
	for _, main := range []bool{false, true} {
		want, werr := outRun(sheet, tryOutDoc, main, false, TransformOptions{})
		before := xdmsink.Forwarded.Load()
		got, gerr := func() (got string, err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("panic: %v", p) // reported, not a crash of the test binary
				}
			}()
			return outRun(sheet, tryOutDoc, main, true, TransformOptions{})
		}()
		fwd += xdmsink.Forwarded.Load() - before
		switch {
		case code != "":
			if werr == nil || !strings.Contains(werr.Error(), code) || gerr == nil || gerr.Error() != werr.Error() {
				t.Errorf("%s (main %v): error %v, tree's %v, want %s", where, main, gerr, werr, code)
			}
		case (werr == nil) != (gerr == nil) || werr != nil && werr.Error() != gerr.Error():
			t.Errorf("%s (main %v): error %v, want %v", where, main, gerr, werr)
		case got != want:
			t.Errorf("%s (main %v):\n got %q\nwant %q", where, main, got, want)
		}
	}
	return fwd
}
