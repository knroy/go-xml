package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// compileAttrSetSheet compiles src and returns the error, if any.
func compileAttrSetSheet(t *testing.T, src string) error {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		return err
	}
	_, err = s.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "initial-template", InitialTemplateURI: xdm.NSXSL})
	return err
}

// 10.2.4: "If any xsl:attribute-set declaration for an attribute set has the
// attribute streamable="yes", then every xsl:attribute-set declaration for
// that attribute set must have the attribute streamable="yes"."
//
// Both declarations here name "s", so they are one attribute set; only the
// first says streamable="yes". The set was composed silently, giving it a
// streamability the second declaration never claimed.
func TestAttributeSetStreamableMustAgree(t *testing.T) {
	err := compileAttrSetSheet(t, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		<xsl:attribute-set name="s" streamable="yes"><xsl:attribute name="a">1</xsl:attribute></xsl:attribute-set>
		<xsl:attribute-set name="s"><xsl:attribute name="b">2</xsl:attribute></xsl:attribute-set>
		<xsl:template name="xsl:initial-template"><out xsl:use-attribute-sets="s"/></xsl:template>
	</xsl:stylesheet>`)
	if err == nil {
		t.Fatal("a set declared streamable=\"yes\" once and not at all in its " +
			"second declaration compiled, want XTSE0020")
	}
	if !strings.Contains(err.Error(), "XTSE0020") {
		t.Errorf("got %v, want XTSE0020", err)
	}
}

// The rule is about disagreement, not about the attribute being present: two
// declarations that both say streamable="yes" are a legitimate streamable set,
// and so are two that both leave it out (attribute-set-0104/-0105 in the
// suite write the same value on every declaration).
func TestAttributeSetStreamableAgreeingIsAccepted(t *testing.T) {
	if err := compileAttrSetSheet(t, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		<xsl:attribute-set name="s" streamable="yes"><xsl:attribute name="a">1</xsl:attribute></xsl:attribute-set>
		<xsl:attribute-set name="s" streamable="yes"><xsl:attribute name="b">2</xsl:attribute></xsl:attribute-set>
		<xsl:template name="xsl:initial-template"><out xsl:use-attribute-sets="s"/></xsl:template>
	</xsl:stylesheet>`); err != nil {
		t.Errorf("two agreeing streamable declarations: %v", err)
	}
}

// 10.2.3: "If the visibility attribute is present on any of the
// xsl:attribute-set declarations making up the definition of an attribute set
// ... then it must be present, with the same value, on every xsl:attribute-set
// declaration making up the definition of that attribute set."
func TestAttributeSetVisibilityMustAgree(t *testing.T) {
	err := compileAttrSetSheet(t, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		<xsl:attribute-set name="s" visibility="public"><xsl:attribute name="a">1</xsl:attribute></xsl:attribute-set>
		<xsl:attribute-set name="s" visibility="private"><xsl:attribute name="b">2</xsl:attribute></xsl:attribute-set>
		<xsl:template name="xsl:initial-template"><out xsl:use-attribute-sets="s"/></xsl:template>
	</xsl:stylesheet>`)
	if err == nil {
		t.Fatal("one set declared both public and private compiled, want XTSE0020")
	}
	if !strings.Contains(err.Error(), "XTSE0020") {
		t.Errorf("got %v, want XTSE0020", err)
	}
}

// Present on one declaration and absent on the other is the other half of the
// same rule: "then it MUST BE PRESENT ... on every" declaration.
func TestAttributeSetVisibilityMustBePresentOnEvery(t *testing.T) {
	err := compileAttrSetSheet(t, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		<xsl:attribute-set name="s" visibility="public"><xsl:attribute name="a">1</xsl:attribute></xsl:attribute-set>
		<xsl:attribute-set name="s"><xsl:attribute name="b">2</xsl:attribute></xsl:attribute-set>
		<xsl:template name="xsl:initial-template"><out xsl:use-attribute-sets="s"/></xsl:template>
	</xsl:stylesheet>`)
	if err == nil {
		t.Fatal("visibility on one declaration only compiled, want XTSE0020")
	}
	if !strings.Contains(err.Error(), "XTSE0020") {
		t.Errorf("got %v, want XTSE0020", err)
	}
}

// A single declaration carrying visibility is untouched, and so are two
// declarations that agree.
func TestAttributeSetVisibilityAgreeingIsAccepted(t *testing.T) {
	if err := compileAttrSetSheet(t, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		<xsl:attribute-set name="s" visibility="public"><xsl:attribute name="a">1</xsl:attribute></xsl:attribute-set>
		<xsl:attribute-set name="s" visibility="public"><xsl:attribute name="b">2</xsl:attribute></xsl:attribute-set>
		<xsl:template name="xsl:initial-template"><out xsl:use-attribute-sets="s"/></xsl:template>
	</xsl:stylesheet>`); err != nil {
		t.Errorf("two agreeing visibility declarations: %v", err)
	}
}
