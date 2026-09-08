package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// runNamed compiles src and runs its named template with no source document.
func runNamed(t *testing.T, src, name string) (string, error) {
	t.Helper()
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: name})
	if err != nil {
		return "", err
	}
	return res.String(), nil
}

// TestAttributeFunctionItemIsFOTY0013 covers step 3 of section 5.8.2.
//
// Constructing simple content atomizes the sequence, and the specification
// notes in that step that doing so "may cause a dynamic error". Atomizing a
// function item is FOTY0013, so xsl:attribute over a sequence that holds one
// must fail rather than build an attribute from the items around it.
//
// constructedText matched neither its *xdm.Node arm nor its *xdm.Atomic arm
// for a function item and so dropped it in silence: select="1, 2, false#0"
// produced a="1 2", and the suite's si-attribute-057 saw a transform succeed
// where it expected an error.
func TestAttributeFunctionItemIsFOTY0013(t *testing.T) {
	const src = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <xsl:param name="s" select="false#0"/>
	    <out><xsl:attribute name="a" select="1, 2, $s"/></out>
	  </xsl:template>
	</xsl:stylesheet>`
	out, err := runNamed(t, src, "main")
	if err == nil {
		t.Fatalf("a function item in the attribute content was accepted: %s", out)
	}
	if code := xdm.ErrorCode(err); code != "FOTY0013" {
		t.Errorf("error code = %q (%v), want FOTY0013", code, err)
	}
}

// TestAttributeFunctionItemCaught is the same error seen from xsl:try.
//
// si-attribute-058 wraps the failing xsl:attribute in xsl:try and catches
// *:FOTY0013. It is a separate case from 057 because a dynamic error raised
// where the attribute is built is recoverable, while one raised during static
// analysis would not reach the catch at all.
func TestAttributeFunctionItemCaught(t *testing.T) {
	const src = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <xsl:param name="s" select="false#0"/>
	    <out><xsl:try>
	      <xsl:attribute name="a" select="1, 2, $s"/>
	      <xsl:catch errors="*:FOTY0013">caught</xsl:catch>
	    </xsl:try></out>
	  </xsl:template>
	</xsl:stylesheet>`
	out, err := runNamed(t, src, "main")
	if err != nil {
		t.Fatalf("the error escaped xsl:catch: %v", err)
	}
	if !strings.Contains(out, "<out>caught</out>") {
		t.Errorf("output = %s, want <out>caught</out>", out)
	}
}

// TestAttributeSequenceConstructorFunctionItem checks the other half of
// xsl:attribute: content from the sequence constructor rather than @select.
//
// Both halves call constructedText, so both had the same hole; only the
// @select one is covered by the suite.
func TestAttributeSequenceConstructorFunctionItem(t *testing.T) {
	const src = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <out><xsl:attribute name="a">
	      <xsl:sequence select="1, false#0"/>
	    </xsl:attribute></out>
	  </xsl:template>
	</xsl:stylesheet>`
	out, err := runNamed(t, src, "main")
	if err == nil {
		t.Fatalf("a function item from the sequence constructor was accepted: %s", out)
	}
	if code := xdm.ErrorCode(err); code != "FOTY0013" {
		t.Errorf("error code = %q (%v), want FOTY0013", code, err)
	}
}

// TestAttributeAtomizableContentStillWorks guards the fix against
// over-reach: everything that could be atomized before must still be.
//
// AtomizeChecked reports FOTY0013 for maps and for arrays that contain them,
// so an attribute built from an ordinary array — which atomizes to its
// members — must keep working rather than become an error.
func TestAttributeAtomizableContentStillWorks(t *testing.T) {
	const src = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <out><xsl:attribute name="a" select="1, [2, 3], 4"/></out>
	  </xsl:template>
	</xsl:stylesheet>`
	out, err := runNamed(t, src, "main")
	if err != nil {
		t.Fatalf("an array in the attribute content was rejected: %v", err)
	}
	if !strings.Contains(out, `a="1 2 3 4"`) {
		t.Errorf("output = %s, want a=\"1 2 3 4\"", out)
	}
}
