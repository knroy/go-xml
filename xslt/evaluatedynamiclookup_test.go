package xslt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xslt"
)

// 10.4.1 keeps the XSLT-defined functions out of the target expression's
// STATIC context, so a call written out in the expression is XTDE3160 --
// evaluate-047 writes document('http://www.w3.org') and asks for exactly that.
func TestEvaluateStaticCallToDocumentIsHidden(t *testing.T) {
	sheet := compileFor(t, `<xsl:stylesheet
	   xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:param name="p">document('http://www.w3.org')</xsl:param>
	  <xsl:template name="xsl:initial-template">
	    <out><xsl:evaluate xpath="$p"/></out>
	  </xsl:template>
	</xsl:stylesheet>`)

	_, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialTemplate: "{http://www.w3.org/1999/XSL/Transform}initial-template",
	})
	if err == nil {
		t.Fatal("the transform succeeded, want XTDE3160")
	}
	if !strings.Contains(err.Error(), "XTDE3160") {
		t.Fatalf("got %v, want XTDE3160", err)
	}
}

// The dynamic context is another matter. 10.4.2 makes it "the same as the
// dynamic context for the xsl:evaluate instruction itself", and F&O 16.1.1
// resolves fn:function-lookup against the dynamic context's named functions,
// leaving the outcome implementation-defined where the static context lacks
// the name. So the lookup must find fn:document rather than answer with the
// empty sequence -- evaluate-048 calls what it returns, and the empty sequence
// made that an XPTY0004 the case does not allow.
//
// What the call then does is a separate question: with no resolver configured
// it is FODC0002. The assertion here is only that the name resolved.
func TestEvaluateDynamicLookupFindsDocument(t *testing.T) {
	sheet := compileFor(t, `<xsl:stylesheet
	   xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:param name="p">exists(function-lookup(QName(
	    'http://www.w3.org/2005/xpath-functions', 'document'), 1))</xsl:param>
	  <xsl:template name="xsl:initial-template">
	    <out><xsl:evaluate xpath="$p"/></out>
	  </xsl:template>
	</xsl:stylesheet>`)

	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialTemplate: "{http://www.w3.org/1999/XSL/Transform}initial-template",
	})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	if got := res.String(); !strings.Contains(got, ">true<") {
		t.Fatalf("got %s, want the lookup to find fn:document", got)
	}
}

// A private stylesheet function stays hidden from the dynamic lookup too: that
// restriction is about which components this package may reach at all, not
// about when the name is resolved, so relaxing the XSLT-only hiding must not
// have relaxed it as well. evaluate-045 is the static half of the same rule.
//
// It has to be an xsl:package: visibility is a package notion, and
// evaluateMayCall lets every function through a plain xsl:stylesheet because
// there is no package boundary for one to be outside of.
func TestEvaluateDynamicLookupStillHidesPrivateFunctions(t *testing.T) {
	sheet := compileFor(t, `<xsl:package
	   xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   xmlns:f="f.uri" version="3.0">
	  <xsl:param name="p">exists(function-lookup(QName('f.uri','square'), 1))</xsl:param>
	  <xsl:template name="xsl:initial-template" visibility="public">
	    <out><xsl:evaluate xpath="$p"/></out>
	  </xsl:template>
	  <xsl:function name="f:square" as="xs:integer" visibility="private">
	    <xsl:param name="x" as="xs:integer"/>
	    <xsl:sequence select="$x*$x"/>
	  </xsl:function>
	</xsl:package>`)

	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialTemplate: "{http://www.w3.org/1999/XSL/Transform}initial-template",
	})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	if got := res.String(); !strings.Contains(got, ">false<") {
		t.Fatalf("got %s, want the private function to stay hidden", got)
	}
}
