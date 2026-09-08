package xslt

import (
	"strings"
	"testing"
)

// TestAttributeSetStreamableValue covers the @streamable and @visibility
// attributes of xsl:attribute-set, which XSLT 3.0 §10.2 adds to the element's
// syntax summary: "streamable? = boolean" and
// "visibility? = public | private | final | abstract".
//
// Neither was listed in the element table, so no value written for either was
// ever checked. si-lre-906 writes streamable="Yes" -- capitalised, and so not
// one of the six xsl:yes-or-no spellings -- and expects XTSE0020; it compiled
// silently instead.
func TestAttributeSetStreamableValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		attr string
		bad  bool
	}{
		{name: "si-lre-906 capital Yes", attr: `streamable="Yes"`, bad: true},
		{name: "yes", attr: `streamable="yes"`},
		{name: "boolean synonym", attr: `streamable="1"`},
		{name: "visibility public", attr: `visibility="public"`},
		{name: "visibility bogus", attr: `visibility="protected"`, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:transform version="3.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			  <xsl:attribute-set name="as" ` + tc.attr + `>
			    <xsl:attribute name="x" select="1"/>
			  </xsl:attribute-set>
			  <xsl:template name="main"><out/></xsl:template>
			</xsl:transform>`
			_, err := Compile(mustParse(t, src), CompileOptions{})
			if !tc.bad {
				if err != nil {
					t.Fatalf("%s is legal: %v", tc.attr, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s must be XTSE0020", tc.attr)
			}
			if !strings.Contains(err.Error(), "XTSE0020") {
				t.Fatalf("got %v, want XTSE0020", err)
			}
		})
	}
}

// TestAttributeSetStreamableXTSE0730 covers XTSE0730: "If an xsl:attribute set
// element specifies streamable="yes" then every attribute set referenced in
// its use-attribute-sets attribute (if present) must also specify
// streamable="yes"."
//
// error-0730a is the "c" case below: streamable="0" is the boolean false, so
// a streamable set using it breaks the rule.
func TestAttributeSetStreamableXTSE0730(t *testing.T) {
	for _, tc := range []struct {
		name string
		sets string
		bad  bool
	}{
		{
			name: "error-0730a",
			sets: `<xsl:attribute-set name="a" streamable="yes" use-attribute-sets="b c">
			         <xsl:attribute name="x" select="1"/></xsl:attribute-set>
			       <xsl:attribute-set name="b" streamable="yes">
			         <xsl:attribute name="y" select="1"/></xsl:attribute-set>
			       <xsl:attribute-set name="c" streamable="0">
			         <xsl:attribute name="z" select="1"/></xsl:attribute-set>`,
			bad: true,
		},
		{
			name: "all streamable",
			sets: `<xsl:attribute-set name="a" streamable="yes" use-attribute-sets="b">
			         <xsl:attribute name="x" select="1"/></xsl:attribute-set>
			       <xsl:attribute-set name="b" streamable="true">
			         <xsl:attribute name="y" select="1"/></xsl:attribute-set>`,
		},
		{
			// The rule is one-directional: a set that is not streamable may
			// use one that is. Raising the error here would refuse a legal
			// stylesheet.
			name: "non-streamable using streamable",
			sets: `<xsl:attribute-set name="a" use-attribute-sets="b">
			         <xsl:attribute name="x" select="1"/></xsl:attribute-set>
			       <xsl:attribute-set name="b" streamable="yes">
			         <xsl:attribute name="y" select="1"/></xsl:attribute-set>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:stylesheet version="3.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + tc.sets + `
			  <xsl:template name="main"><out/></xsl:template>
			</xsl:stylesheet>`
			_, err := Compile(mustParse(t, src), CompileOptions{})
			if !tc.bad {
				if err != nil {
					t.Fatalf("legal stylesheet refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want XTSE0730")
			}
			if !strings.Contains(err.Error(), "XTSE0730") {
				t.Fatalf("got %v, want XTSE0730", err)
			}
		})
	}
}
