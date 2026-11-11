package xslt

import (
	"context"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestSMB1AbstractMarkerUnforgeable checks a stylesheet cannot write the
// internal abstract marker: under the old fixed namespace this made $z an
// abstract stub and the transform failed with XTDE3052.
func TestSMB1AbstractMarkerUnforgeable(t *testing.T) {
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:m="http://go-xml.invalid/xslt/abstract">
	  <xsl:variable name="z" m:abstract="z">1</xsl:variable>
	  <xsl:template name="main"><xsl:sequence select="$z"/></xsl:template>
	</xsl:stylesheet>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "main"}); err != nil {
		t.Fatalf("author-written marker took effect: %v", err)
	}
}
