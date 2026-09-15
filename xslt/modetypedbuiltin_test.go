package xslt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// XTTE3100 reaches the nodes a built-in rule selects, not only the ones an
// explicit xsl:apply-templates names.
//
// 6.7.3 writes the shallow-copy built-in rule out as a template whose body is
// two xsl:apply-templates instructions in the same mode, so the descent into a
// copied element's attributes and children is itself an apply-templates and
// the elements it selects are within the error's reach. mode-1438 is the
// suite's case: a stylesheet that is nothing but <xsl:mode typed="yes"
// on-no-match="shallow-copy"/>, entered on an untyped source. Nothing but the
// built-in recursion ever selects an element there, so a check confined to
// xsl:apply-templates lets the transform succeed.
func TestModeTypedReachesBuiltInRuleSelection(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   version="3.0">
	  <xsl:mode name="s" on-no-match="shallow-copy" typed="yes"/>
	</xsl:stylesheet>`
	sheet := compileFor(t, src)

	doc, err := xdm.ParseString(`<book><t>x</t></book>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sheet.Transform(context.Background(), doc.Root,
		xslt.TransformOptions{InitialMode: "s"})
	if err == nil {
		t.Fatal("the transform succeeded, want XTTE3100")
	}
	if !strings.Contains(err.Error(), "XTTE3100") {
		t.Fatalf("got %v, want XTTE3100", err)
	}
}

// The mirror case: a mode that makes no assertion against untyped nodes must
// not have one invented for it now that the built-in descent reaches them.
//
// @typed is "boolean | strict | lax | unspecified", and a boolean here is any
// of yes/no, true/false, 1/0. Only the literal "no" was being tested, so
// " false " and "0" -- mode-1445 and mode-1446, which assert output rather
// than an error -- were read as their own opposite. "unspecified" asserts
// nothing either way. The capitalised "No" is deliberately absent: mode-1447
// writes it and expects XTSE0020, so these values are case-sensitive.
func TestModeTypedNoAcceptsUntypedUnderBuiltInRule(t *testing.T) {
	for _, typed := range []string{"no", "false", " false ", "0", "unspecified"} {
		t.Run(typed, func(t *testing.T) {
			sheet := compileFor(t, `<xsl:stylesheet
			   xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
			  <xsl:mode name="s" on-no-match="shallow-copy" typed="`+typed+`"/>
			</xsl:stylesheet>`)

			doc, err := xdm.ParseString(`<book><t>x</t></book>`, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sheet.Transform(context.Background(), doc.Root,
				xslt.TransformOptions{InitialMode: "s"}); err != nil {
				t.Fatalf("transforming: %v", err)
			}
		})
	}
}

// The affirmative spellings all still assert, so the widening above did not
// turn the error off for the values that do mean "yes".
func TestModeTypedYesSpellingsAllAssert(t *testing.T) {
	for _, typed := range []string{"yes", "true", "1", "strict", "lax"} {
		t.Run(typed, func(t *testing.T) {
			sheet := compileFor(t, `<xsl:stylesheet
			   xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
			  <xsl:mode name="s" on-no-match="shallow-copy" typed="`+typed+`"/>
			</xsl:stylesheet>`)

			doc, err := xdm.ParseString(`<book><t>x</t></book>`, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = sheet.Transform(context.Background(), doc.Root,
				xslt.TransformOptions{InitialMode: "s"})
			if err == nil || !strings.Contains(err.Error(), "XTTE3100") {
				t.Fatalf("got %v, want XTTE3100", err)
			}
		})
	}
}
