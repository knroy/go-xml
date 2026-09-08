package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestMergeActionContextSizeIsGroupCount covers the focus rules of section
// 15.7, which state the context size unconditionally: "The context size is
// the number of groups, that is, the number of distinct sets of merge key
// values."
//
// The rules make no mention of streamability, and the phrase "context size is
// absent" appears nowhere in the specification. A streamable="yes" merge
// source used to force the size to zero here, which spelled an absent size
// and made last() inside the action raise XPDY0002 — bug 29120, and what the
// suite's merge-084 tests.
//
// The two sources are merged over the same three keys, so there are three
// groups and last() must answer 3 in every one of them.
func TestMergeActionContextSizeIsGroupCount(t *testing.T) {
	for _, streamable := range []string{"no", "yes"} {
		t.Run("streamable="+streamable, func(t *testing.T) {
			src := `<xsl:stylesheet version="3.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			  <xsl:template name="main">
			    <out><xsl:merge>
			      <xsl:merge-source name="a" streamable="` + streamable + `"
			          select="$one/r/x">
			        <xsl:merge-key select="."/>
			      </xsl:merge-source>
			      <xsl:merge-source name="b" streamable="` + streamable + `"
			          select="$two/r/x">
			        <xsl:merge-key select="."/>
			      </xsl:merge-source>
			      <xsl:merge-action>
			        <g p="{position()}" l="{last()}"/>
			      </xsl:merge-action>
			    </xsl:merge></out>
			  </xsl:template>
			  <xsl:variable name="one">
			    <r><x>1</x><x>2</x><x>3</x></r>
			  </xsl:variable>
			  <xsl:variable name="two">
			    <r><x>1</x><x>2</x><x>3</x></r>
			  </xsl:variable>
			</xsl:stylesheet>`
			sheet, err := Compile(mustParse(t, src), CompileOptions{})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			res, err := sheet.Transform(context.Background(), nil,
				TransformOptions{InitialTemplate: "main"})
			if err != nil {
				t.Fatalf("last() inside xsl:merge-action failed: %v", err)
			}
			const want = `<g p="1" l="3"/><g p="2" l="3"/><g p="3" l="3"/>`
			if got := res.String(); !strings.Contains(got, want) {
				t.Errorf("output = %s, want it to contain %s", got, want)
			}
		})
	}
}

// TestMergeActionLastDoesNotRaise is the error-shaped half of the same rule.
//
// The failure this guards was not a wrong number but a raised error, so the
// assertion is on the code: a zero context size made last() report XPDY0002,
// and no context size should be missing inside a merge action.
func TestMergeActionLastDoesNotRaise(t *testing.T) {
	const src = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <out><xsl:merge>
	      <xsl:merge-source name="a" streamable="yes" select="$one/r/x">
	        <xsl:merge-key select="."/>
	      </xsl:merge-source>
	      <xsl:merge-action><xsl:value-of select="last()"/></xsl:merge-action>
	    </xsl:merge></out>
	  </xsl:template>
	  <xsl:variable name="one"><r><x>1</x><x>2</x></r></xsl:variable>
	</xsl:stylesheet>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "main"})
	if err != nil {
		if code := xdm.ErrorCode(err); code == "XPDY0002" {
			t.Fatalf("last() reported an absent context size: %v", err)
		}
		t.Fatalf("Transform: %v", err)
	}
	if got := res.String(); !strings.Contains(got, "<out>22</out>") {
		t.Errorf("output = %s, want <out>22</out>", got)
	}
}
