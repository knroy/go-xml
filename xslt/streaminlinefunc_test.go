package xslt

import "testing"

// §19.8.8.16: "An inline function declaration that textually contains a
// variable reference bound to a streaming parameter (of some containing
// stylesheet function) is roaming and free-ranging. All other inline function
// declarations are grounded and motionless."
//
// The parenthetical is the whole rule. The streaming parameter belongs to the
// enclosing xsl:function, not to the inline function -- an inline function's
// own parameters cannot be streaming -- so what is asked is whether the
// declaration's TEXT mentions the outer parameter. Nothing about what it then
// does with it, and so nothing about posture or sweep inside the body.
func TestInlineFunctionStreamability(t *testing.T) {
	body := func(sel string) string {
		return `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:f="urn:f">
  <xsl:function name="f:g" as="item()*" streamability="absorbing">
    <xsl:param name="element" as="node()*"/>
    <xsl:sequence select="` + sel + `"/>
  </xsl:function>
</xsl:stylesheet>`
	}

	for _, tc := range []struct {
		name    string
		sel     string
		roaming bool
		why     string
	}{
		{
			name: "no reference at all",
			sel:  "function($x) { 1 }",
			why:  "the second sentence: all other declarations are grounded",
		},
		{
			name: "its own parameter is not the streaming one",
			sel:  "function($x) { $x }",
			why: "an inline function's own parameter cannot be streaming; " +
				"the rule names the CONTAINING stylesheet function's",
		},
		{
			name:    "direct reference to the streaming parameter",
			sel:     "function($x) { $element }",
			roaming: true,
			why:     "the streamed node would be captured in the closure",
		},
		{
			name:    "reference buried in a path",
			sel:     "function($x) { $element/a/b[c = 1] }",
			roaming: true,
			why:     "textual containment, whatever the reference is used for",
		},
		{
			name:    "reference inside a nested inline function",
			sel:     "function($x) { function($y) { $element } }",
			roaming: true,
			why: "still within the outer declaration's text. The section's " +
				"note says the rule exists to stop a streamed node reaching " +
				"a closure, and a nested function is exactly such a closure",
		},
		{
			name: "nested function referring only to its own parameter",
			sel:  "function($x) { function($y) { $y } }",
			why: "neither declaration mentions $element, so the walk must " +
				"not treat any nested variable reference as a hit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseSheet(t, body(tc.sel))
			funcs := collectStreamFuncs(doc)
			f, ok := funcs[funcKey{uri: "urn:f", local: "g", arity: 1}]
			if !ok {
				t.Fatal("f:g not collected")
			}
			p, known := analyzeFunctionBody(f, funcs)
			if !known {
				t.Fatalf("the body was not modelled, so the rule under test "+
					"was never reached: %s", tc.sel)
			}
			gotRoaming := p.posture == postureRoaming
			if gotRoaming != tc.roaming {
				t.Errorf("%s gave posture %v sweep %v; want roaming=%v — %s",
					tc.sel, p.posture, p.sweep, tc.roaming, tc.why)
			}
		})
	}
}
