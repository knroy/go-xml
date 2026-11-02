package xslt

import (
	"strings"
	"testing"
)

// shallowDocSheet applies a shallow-copy mode to the document node inside a
// function declared as="document-node()", and reports what the copy carries.
// extra is added to the stylesheet; a rule that matches nothing in the source
// puts the mode on the path that builds a sequence and wraps it in a document.
func shallowDocSheet(extra string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:f="urn:f" exclude-result-prefixes="f">
<xsl:output omit-xml-declaration="yes"/>
<xsl:mode name="m" on-no-match="shallow-copy"/>` + extra + `
<xsl:function name="f:copy" as="document-node()">
 <xsl:param name="d"/><xsl:apply-templates select="$d" mode="m"/>
</xsl:function>
<xsl:template match="/"><xsl:variable name="c" select="f:copy(.)"/>
<out><xsl:value-of select="base-uri($c/*) , base-uri($c/*/*[2]) , count($c//node())"/>|<xsl:copy-of select="$c"/></out>
</xsl:template></xsl:stylesheet>`
}

const shallowDocSrc = `<!--c--><?pi x?><r xmlns:p="urn:p" xml:base="http://e/x/" a="1">` +
	`<p:k b="2"> t <!--in--><?q y?></p:k><k xml:base="y/">z</k>  <k/></r>`

// The in-place build of a rule-less mode must give what the build-then-wrap
// path gives, down to the base URIs.
func TestShallowCopyDocumentInPlaceMatchesWrapped(t *testing.T) {
	want := run(t, shallowDocSheet(`<xsl:template match="zz" mode="m"/>`), shallowDocSrc)
	got := run(t, shallowDocSheet(``), shallowDocSrc)
	if got != want {
		t.Errorf("in place:\n%s\nwrapped:\n%s", got, want)
	}
	if !strings.Contains(got, `<k xml:base="y/">z</k>`) {
		t.Errorf("copy lost content: %s", got)
	}
}

// An attribute produced for the content of a copied document node is still
// XTDE0420; it must not become an attribute of an enclosing element.
func TestShallowCopyDocumentAttributeStillFaults(t *testing.T) {
	sheet := shallowDocSheet(`<xsl:template match="r" mode="m"><xsl:attribute name="a">1</xsl:attribute></xsl:template>`)
	if _, err := runErr(t, sheet, `<r/>`); err == nil || !strings.Contains(err.Error(), "XTDE0420") {
		t.Errorf("got %v, want XTDE0420", err)
	}
}
