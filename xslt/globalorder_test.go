package xslt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// A global's dependency reaches it by three routes and only one of them is a
// scan of its own select expression. These are the other two, both found by
// running DocBook xslTNG rather than by the W3C suites, which never combine
// an imported module with a sequence-constructor global.
func TestGlobalOrderingSeesIndirectDependencies(t *testing.T) {
	for _, tc := range []struct{ name, sheet, want string }{{
		// The dependency is named in a sequence constructor rather than in a
		// select attribute. globalRefs scanned only Select, so this global
		// declared no dependency at all and was bound in declaration order --
		// which xsl:import puts before the module declaring what it needs.
		name: "sequence constructor",
		sheet: `<xsl:variable name="v:flag" as="xs:boolean" select="true()"/>
			<xsl:variable name="out" as="xs:string">
			  <xsl:choose>
			    <xsl:when test="$v:flag"><xsl:sequence select="'yes'"/></xsl:when>
			    <xsl:otherwise><xsl:sequence select="'no'"/></xsl:otherwise>
			  </xsl:choose>
			</xsl:variable>`,
		want: "yes",
	}, {
		// The dependency is named by the body of a function the global calls,
		// so it appears nowhere in the global's own text.
		name: "through a function body",
		sheet: `<xsl:variable name="out" select="f:pick(1)"/>
			<xsl:function name="f:pick" as="xs:string">
			  <xsl:param name="n"/>
			  <xsl:sequence select="if ($v:flag) then 'yes' else 'no'"/>
			</xsl:function>
			<xsl:variable name="v:flag" as="xs:boolean" select="true()"/>`,
		want: "yes",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			   xmlns:xs="http://www.w3.org/2001/XMLSchema"
			   xmlns:f="http://example.com/f" xmlns:v="http://example.com/v"
			   version="3.0">` + tc.sheet +
				`<xsl:template match="/"><o><xsl:value-of select="$out"/></o></xsl:template>
			</xsl:stylesheet>`
			got := transformTo(t, src, `<a/>`)
			if !strings.Contains(got, ">"+tc.want+"<") {
				t.Fatalf("got %s, want an <o> holding %q", got, tc.want)
			}
		})
	}
}

// A global that reaches its own name through a function body is not
// circular unless the reference is evaluated. param-0301 in the W3C suite
// covers it, and suppressing the self-reference is what keeps that passing:
// the first cut of the dependency walk above reported XPST0008 for it.
func TestSelfReferenceThroughAFunctionIsNotACircularity(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:my="http://example.com/my" version="2.0">
	  <xsl:variable name="x" select="my:func(1)"/>
	  <xsl:function name="my:func">
	    <xsl:param name="a"/>
	    <xsl:variable name="unused" select="$x"/>
	    <xsl:sequence select="$a + 2"/>
	  </xsl:function>
	  <xsl:template match="/"><o><xsl:value-of select="$x"/></o></xsl:template>
	</xsl:stylesheet>`
	if got := transformTo(t, src, `<a/>`); !strings.Contains(got, ">3<") {
		t.Fatalf("got %s, want an <o> holding 3", got)
	}
}

func transformTo(t *testing.T, sheetSrc, docSrc string) string {
	t.Helper()
	tree, err := xdm.ParseString(sheetSrc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	doc, err := xdm.ParseString(docSrc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the source: %v", err)
	}
	res, err := sheet.Transform(context.Background(), doc.Root, xslt.TransformOptions{})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	return xslt.SerializeAsXML(res)
}

// "$x => f()" passes $x as f's first argument, so the pattern's function
// checker must count one more argument than the parentheses hold. XSpec
// matches on "x:expect[node() => empty()]", and fn:empty has no nullary
// form, so miscounting reported XPST0017 for a valid pattern.
func TestArrowOperatorArityInAPattern(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:template match="a[node() => empty()]"><empty/></xsl:template>
	  <xsl:template match="a"><full/></xsl:template>
	  <xsl:template match="/"><o><xsl:apply-templates select="//a"/></o></xsl:template>
	</xsl:stylesheet>`
	got := transformTo(t, src, `<r><a/><a>x</a></r>`)
	if !strings.Contains(got, "<empty/>") || !strings.Contains(got, "<full/>") {
		t.Fatalf("got %s, want both an <empty/> and a <full/>", got)
	}
}
