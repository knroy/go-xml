package xslt

import (
	"strings"
	"testing"
)

// TestLiteralResultXSLAttrValue covers XTSE0020 for the xsl:-prefixed
// attributes of a literal result element.
//
// XTSE0805 checked only that the *name* was one the specification defines
// there; no value was ever examined. namespace-2633 writes
// xsl:inherit-namespaces=" " and expects XTSE0020 -- §3.5 compares "the value
// of the attribute after removing leading and trailing whitespace", so " " is
// the empty string and not one of the permitted values.
//
// The silent acceptance was the worse half: the reader that then asked whether
// the value was "yes" treated the unrecognised spelling as "no", so a typo
// inverted the element's namespace behaviour rather than being reported.
func TestLiteralResultXSLAttrValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		attr string
		bad  bool
	}{
		{name: "namespace-2633 whitespace", attr: `xsl:inherit-namespaces=" "`, bad: true},
		{name: "empty", attr: `xsl:inherit-namespaces=""`, bad: true},
		{name: "typo", attr: `xsl:inherit-namespaces="nope"`, bad: true},
		{name: "yes", attr: `xsl:inherit-namespaces="yes"`},
		{name: "no", attr: `xsl:inherit-namespaces="no"`},
		// The 3.0 boolean synonyms stay legal: refusing them here would
		// reject stylesheets the schema-for-stylesheets admits.
		{name: "false synonym", attr: `xsl:inherit-namespaces="false"`},
		{name: "one synonym", attr: `xsl:inherit-namespaces="1"`},
		// Surrounding whitespace is stripped before the comparison, so a
		// padded but otherwise valid value is accepted.
		{name: "padded yes", attr: `xsl:inherit-namespaces=" yes "`},
		{name: "bad validation", attr: `xsl:validation="sloppy"`, bad: true},
		{name: "good validation", attr: `xsl:validation="strip"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:stylesheet version="3.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			  <xsl:template match="/">
			    <a xmlns:n="http://n/" ` + tc.attr + `><b/></a>
			  </xsl:template>
			</xsl:stylesheet>`
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

// TestLiteralResultXSLAttrValueUnenumerated guards the other direction: an
// xsl: attribute whose value no table can enumerate -- a prefix list, a
// collation URI, an attribute-set name -- must not be value-checked at all.
func TestLiteralResultXSLAttrValueUnenumerated(t *testing.T) {
	src := `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:attribute-set name="as"><xsl:attribute name="x">1</xsl:attribute></xsl:attribute-set>
	  <xsl:template match="/">
	    <a xmlns:n="http://n/" xsl:use-attribute-sets="as"
	       xsl:exclude-result-prefixes="n"><b/></a>
	  </xsl:template>
	</xsl:stylesheet>`
	if _, err := Compile(mustParse(t, src), CompileOptions{}); err != nil {
		t.Fatalf("unenumerated xsl: attributes must not be value-checked: %v", err)
	}
}
