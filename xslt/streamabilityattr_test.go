package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestFunctionStreamabilityWithoutParams pins XTSE3155.
//
// §19: "It is a static error if an xsl:function element with no xsl:param
// children has a streamability attribute with any value other than
// unclassified." The other classifications describe how a function consumes
// its streamed argument, so a function taking none cannot be any of them —
// which is why the rule holds with no streamability analysis behind it, and
// why a processor that does not stream can still enforce it.
//
// The negative arms carry the weight: the attribute is legal on a function
// that DOES take a parameter, and "unclassified" is legal either way. A check
// that refused the attribute outright would pass the first arm and break
// every streaming-annotated stylesheet.
func TestFunctionStreamabilityWithoutParams(t *testing.T) {
	compile := func(t *testing.T, body string) error {
		t.Helper()
		doc, err := xdm.ParseString(
			`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"`+
				` xmlns:f="urn:f" version="3.0">`+body+
				`<xsl:template match="/"><out/></xsl:template>`+
				`</xsl:stylesheet>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		_, err = Compile(doc.Root, CompileOptions{})
		return err
	}

	t.Run("no params and a classification is XTSE3155", func(t *testing.T) {
		err := compile(t, `<xsl:function name="f:x" streamability="absorbing">`+
			`<xsl:sequence select="22"/></xsl:function>`)
		if err == nil {
			t.Fatal("accepted, want XTSE3155")
		}
		if !strings.Contains(err.Error(), "XTSE3155") {
			t.Errorf("error = %v, want XTSE3155", err)
		}
	})

	t.Run("no params and unclassified is legal", func(t *testing.T) {
		if err := compile(t, `<xsl:function name="f:x" streamability="unclassified">`+
			`<xsl:sequence select="22"/></xsl:function>`); err != nil {
			t.Errorf("unclassified refused: %v", err)
		}
	})

	t.Run("a param makes any classification legal", func(t *testing.T) {
		// The body returns string($p), not $p. This subtest is about the
		// streamability ATTRIBUTE being accepted once the function has a
		// parameter -- it is not about §19.8.5's body rule, and the two must
		// not be confused.
		//
		// The fixture used to be "select=$p", which is a reference to the
		// streaming parameter and therefore striding (§19.8.5.2). Absorbing
		// requires a grounded body, so that function is genuinely not
		// guaranteed-streamable and XTSE3430 is the right answer for it. The
		// subtest passed only because varPosture wrongly reported grounded;
		// once that was corrected to match the spec, the fixture began
		// failing for a reason that has nothing to do with what it tests.
		// string() atomizes, which grounds the result and keeps the subtest
		// on its own subject.
		if err := compile(t, `<xsl:function name="f:x" streamability="absorbing">`+
			`<xsl:param name="p"/><xsl:sequence select="string($p)"/></xsl:function>`); err != nil {
			t.Errorf("classification with a param refused: %v", err)
		}
	})

	t.Run("no attribute at all is legal", func(t *testing.T) {
		if err := compile(t, `<xsl:function name="f:x">`+
			`<xsl:sequence select="22"/></xsl:function>`); err != nil {
			t.Errorf("plain function refused: %v", err)
		}
	})
}
