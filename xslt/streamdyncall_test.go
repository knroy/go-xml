package xslt

import (
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// §19.8.8.11: "The posture and sweep of a dynamic function call such as
// $F(X, Y) are determined by the 19.8.1 General Rules for Streamability. The
// operands and their usages are as follows: The base expression that computes
// the function value itself (here $F). This has usage inspection. The argument
// expressions excluding any ? placeholders (here X and Y). These have
// type-determined usage dependent on ancillary information associated with
// the static type of the base expression, where available [...]. If no
// function signature is available, then the usage of each of the argument
// expressions is navigation."
//
// The analyzer sees no declared types, so no signature is ever available and
// the last sentence governs every argument: a streamed argument makes the
// call roaming, which the section's note calls the expected outcome for a
// function "not known statically" to be a map or array.
func TestDynamicCallStreamability(t *testing.T) {
	for _, tc := range []struct {
		expr  string
		ctx   posture
		want  props
		known bool
		why   string
	}{
		{"$f(1)", postureStriding, groundedMotionless, true,
			"§19.8.8.12 grounds $f; a literal argument is grounded and " +
				"motionless under any usage (§19.8.1: if P is grounded, " +
				"then S′ is S), so the general rules leave nothing to read"},
		{"1 => $f()", postureStriding, groundedMotionless, true,
			"the note: the section applies equally to the arrow syntax"},
		{"$f(?, 1)", postureStriding, groundedMotionless, true,
			"'excluding any ? placeholders': the placeholder is not an operand"},
		{"$f(string(child::x))", postureStriding, props{postureGrounded, sweepConsuming}, true,
			"a grounded consuming argument keeps its own sweep whatever " +
				"the usage, and §19.8.1 rule 2 makes the call grounded and consuming"},
		{"$f(following-sibling::*)", postureStriding, roamingFreeRanging, true,
			"a free-ranging operand is decisive under any usage (§19.8.1 rule 1)"},
		{"(name#0)()", postureGrounded, groundedMotionless, true,
			"the note on focus-dependent function items: their dynamic error " +
				"'does not affect the static streamability analysis'; the base " +
				"is an inspection operand, and §19.8.8.15 grounds name#0 here"},

		// "If no function signature is available, then the usage of each of
		// the argument expressions is navigation" -- and navigation from a
		// streamed posture is free-ranging (§19.8.1). The note: such a call
		// "will generally be roaming and free-ranging. This means it is
		// desirable to declare the type of any variable holding a map or
		// array."
		{"$f(.)", postureStriding, roamingFreeRanging, true,
			"a streamed context item as the argument of an undeclared $f"},
		{"$f(child::x)", postureStriding, roamingFreeRanging, true,
			"a streamed node sequence as the argument of an undeclared $f"},
		{"(name#0)()", postureStriding, roamingFreeRanging, true,
			"not this section's doing: §19.8.8.15 makes name#0 roaming from " +
				"a streamed context, and the inspected base operand carries that"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			expr, err := xpath.ParseVersion(tc.expr, nil, xpath.XPath31)
			if err != nil {
				t.Fatalf("parsing %q: %v", tc.expr, err)
			}
			got, known := analyzeExpr(expr, tc.ctx)
			if known != tc.known {
				t.Fatalf("%s: known=%v, want %v — %s", tc.expr, known, tc.known, tc.why)
			}
			if got != tc.want {
				t.Errorf("%s is %v and %v; want %v and %v — %s",
					tc.expr, got.posture, got.sweep, tc.want.posture, tc.want.sweep, tc.why)
			}
		})
	}
}

// A reference to the streaming parameter of an absorbing function is grounded
// by §19.8.8.12 but denotes a streamed node all the same, and navigation away
// from it is free-ranging. With no signature for $fn -- its "as" is not
// visible to the analyzer -- the fallback sentence applies and the body is
// roaming, so the function is refused.
func TestDynamicCallOnStreamingParameter(t *testing.T) {
	doc := parseSheet(t, `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:f="urn:f">
  <xsl:function name="f:g" as="item()*" streamability="absorbing">
    <xsl:param name="element" as="node()*"/>
    <xsl:param name="fn" as="function(*)"/>
    <xsl:sequence select="$fn($element)"/>
  </xsl:function>
</xsl:stylesheet>`)
	funcs := collectStreamFuncs(doc)
	f, ok := funcs[funcKey{uri: "urn:f", local: "g", arity: 2}]
	if !ok {
		t.Fatal("f:g not collected")
	}
	p, known := analyzeFunctionBody(f, funcs)
	if !known {
		t.Fatal("$fn($element) was not modelled")
	}
	if p != roamingFreeRanging {
		t.Errorf("$fn($element) is %v and %v; want roaming and free-ranging: "+
			"no signature, so the argument navigates from a streamed node",
			p.posture, p.sweep)
	}
}
