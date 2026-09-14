package xslt

import (
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// namedRefNS resolves the two prefixes these tests use: f: for a stylesheet
// function the sheet declares, ext: for a namespace nothing declares.
type namedRefNS struct{}

func (namedRefNS) ResolvePrefix(p string) (string, bool) {
	switch p {
	case "f":
		return "urn:f", true
	case "ext":
		return "urn:ext", true
	}
	return "", false
}

func (namedRefNS) DefaultElementNamespace() string { return "" }

func (namedRefNS) DefaultFunctionNamespace() string {
	return "http://www.w3.org/2005/xpath-functions"
}

// §19.8.8.15: "Let F be the function to which the NamedFunctionRef refers.
// If F is focus-dependent and the context posture is not grounded, then the
// NamedFunctionRef is roaming and free-ranging. If F is an extension
// function, the posture and sweep are implementation-defined. Otherwise,
// the NamedFunctionRef is grounded and motionless."
//
// Focus dependence is declared per arity by the F&O 3.1 and XSLT 3.0
// "Properties" paragraphs, which streamfocus.go transcribes; the cases here
// pair each branch of the rule with a row of that table or its absence.
func TestNamedFunctionRefStreamability(t *testing.T) {
	doc := parseSheet(t, `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:f="urn:f">
  <xsl:function name="f:g" as="xs:string">
    <xsl:param name="x" as="xs:string"/>
    <xsl:sequence select="$x"/>
  </xsl:function>
</xsl:stylesheet>`)
	funcs := collectStreamFuncs(doc)

	for _, tc := range []struct {
		src   string
		ctx   posture
		want  props
		known bool
		why   string
	}{
		{"name#0", postureStriding, roamingFreeRanging, true,
			"F&O §13.1: the zero-argument form is focus-dependent"},
		{"position#0", postureStriding, roamingFreeRanging, true,
			"F&O §15.1: fn:position is focus-dependent"},
		{"current#0", postureStriding, roamingFreeRanging, true,
			"XSLT §20.4.1: fn:current is focus-dependent"},
		{"name#1", postureStriding, groundedMotionless, true,
			"F&O §13.1: the one-argument form is focus-independent"},
		{"upper-case#1", postureStriding, groundedMotionless, true,
			"no arity of fn:upper-case depends on the focus"},
		{"f:g#1", postureStriding, groundedMotionless, true,
			"§5.3.3.1: the focus within a stylesheet function's body is absent"},
		{"ext:foo#0", postureStriding, props{}, false,
			"an extension function's posture is implementation-defined; no opinion"},
		{"name#0", postureGrounded, groundedMotionless, true,
			"the condition 'and the context posture is not grounded' fails"},
	} {
		expr, err := xpath.ParseVersion(tc.src, namedRefNS{}, xpath.XPath31)
		if err != nil {
			t.Fatalf("parsing %q: %v", tc.src, err)
		}
		got, known := analyzeExprFuncs(expr, tc.ctx, funcs)
		if known != tc.known {
			t.Errorf("%s in a %v context: known=%v, want %v — %s",
				tc.src, tc.ctx, known, tc.known, tc.why)
			continue
		}
		if known && got != tc.want {
			t.Errorf("%s in a %v context = %v and %v, want %v and %v — %s",
				tc.src, tc.ctx, got.posture, got.sweep,
				tc.want.posture, tc.want.sweep, tc.why)
		}
	}
}
