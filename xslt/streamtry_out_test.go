package xslt

import (
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
	{"after-text", ``, `<xsl:template match="/"><out>text<xsl:try><xsl:for-each select="//rec[position() lt 3]"><r/></xsl:for-each>more<xsl:catch/></xsl:try></out></xsl:template>`, false, ""},
	{"text-first", ``, `<xsl:template match="/"><out><a/><xsl:try>t1<xsl:value-of select="1, 2"/><xsl:sequence select="3, 4"/><b/>t2<xsl:catch/></xsl:try>t3<xsl:sequence select="5"/></out></xsl:template>`, true, ""},
	{"top", ``, `<xsl:template match="/"><xsl:try><a/><xsl:sequence select="1, 2"/><b/><xsl:catch/></xsl:try><c/></xsl:template>`, false, ""}, // the output method is not decided yet
	{"top-started", ``, `<xsl:template match="/"><z/><y/><xsl:try><a/><xsl:sequence select="1, 2"/><b/><xsl:catch/></xsl:try><c/></xsl:template>`, true, ""},
	{"top-fail", ``, `<xsl:template match="/"><z/><y/><xsl:try><a/><xsl:sequence select="1, 2"/><b/><xsl:sequence select="error()"/><xsl:catch><caught/></xsl:catch></xsl:try><c/></xsl:template>`, true, ""},
	{"first-attr", ``, `<xsl:template match="/"><out><xsl:try><xsl:attribute name="a">1</xsl:attribute><x/><y/><xsl:catch/></xsl:try></out></xsl:template>`, false, ""},
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
	// A document node's content (xsl:document, xsl:copy of the document
	// node) is not taken: held to the try's end, as before.
	{"document-level", ``, `<xsl:template match="/"><xsl:document><a/><b/><xsl:try><c/><d/><xsl:sequence select="error()"/><xsl:catch><e/></xsl:catch></xsl:try><f/></xsl:document></xsl:template>`, false, ""},
	{"document-level-ok", ``, `<xsl:template match="/"><xsl:document><a/><b/><xsl:try><c/><xsl:sequence select="1, 2"/><d/><xsl:catch/></xsl:try><f/></xsl:document></xsl:template>`, false, ""},
	{"copy-document", ``, `<xsl:template match="/"><xsl:copy><a/><xsl:try><c/><d/><xsl:sequence select="error()"/><xsl:catch><e/></xsl:catch></xsl:try><f/></xsl:copy></xsl:template>`, false, ""},
	{"held-element", ``, `<xsl:template match="/"><out><xsl:element name="h" namespace="urn:h" inherit-namespaces="no"><a/><xsl:try><c/><d/><xsl:sequence select="error()"/><xsl:catch><e/></xsl:catch></xsl:try></xsl:element></out></xsl:template>`, false, ""},
	{"html-head", `<xsl:output method="html"/>`, `<xsl:template match="/"><html><head><xsl:try><title>t</title><meta name="a" content="b"/><xsl:catch/></xsl:try></head><body/></html></xsl:template>`, true, ""},
	{"explicit-method-top", `<xsl:output method="xml"/>`, `<xsl:template match="/"><xsl:try><a/><b/><xsl:sequence select="error()"/><xsl:catch><c/></xsl:catch></xsl:try><xsl:try><d/><e/><xsl:catch/></xsl:try></xsl:template>`, true, ""},
	{"text-method", `<xsl:output method="text"/>`, `<xsl:template match="/"><out><xsl:try><a>1</a><b>2</b><xsl:sequence select="error()"/><xsl:catch>c</xsl:catch></xsl:try><xsl:try><a>3</a><b>4</b><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	// rollback-output="no": XTDE3530 when the body produced an item before
	// the error, a zero-length text node included; caught otherwise.
	{"no-written", ``, `<xsl:template match="/"><out><xsl:try rollback-output="no"><xsl:for-each select="//rec"><r><xsl:value-of select="xs:integer(@v)"/></r></xsl:for-each><xsl:catch><bad/></xsl:catch></xsl:try></out></xsl:template>`, true, "XTDE3530"},
	{"no-nothing", ``, `<xsl:template match="/"><out><a/><xsl:try rollback-output="no"><xsl:value-of select="xs:integer('x')"/><xsl:catch><caught/></xsl:catch></xsl:try></out></xsl:template>`, false, ""},
	{"no-empty-text", ``, `<xsl:template match="/"><out><a/><xsl:try rollback-output="no"><xsl:text/><xsl:value-of select="xs:integer('x')"/><xsl:catch><caught/></xsl:catch></xsl:try></out></xsl:template>`, false, "XTDE3530"},
	{"no-ok", ``, `<xsl:template match="/"><out><xsl:try rollback-output="no"><xsl:for-each select="//rec"><r/></xsl:for-each><xsl:catch/></xsl:try></out></xsl:template>`, true, ""},
	{"no-narrow", ``, `<xsl:template match="/"><out><xsl:try><xsl:try rollback-output="no"><a/><b/><xsl:sequence select="error()"/><xsl:catch errors="err:XPTY0004"/></xsl:try><xsl:catch><outer/></xsl:catch></xsl:try></out></xsl:template>`, false, ""},
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
		got, gerr := outRun(sheet, tryOutDoc, main, true, TransformOptions{})
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
