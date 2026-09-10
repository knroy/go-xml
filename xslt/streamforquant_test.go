package xslt

import (
	"strings"
	"testing"
)

// The tests here assert §19.8.8.1 (for expressions), §19.8.8.2 (quantified
// expressions), and the threading of the stylesheet's own xsl:function
// declarations into the instruction-body analysis, which §19.8.5 needs so
// that a call on a declared-streamable function is assessed rather than
// abandoned.
//
// As everywhere in this analysis, the streamable arms matter at least as much
// as the unstreamable ones: a construct wrongly found roaming becomes a
// spurious XTSE3430, which rejects a valid stylesheet at compile time.

// TestForExpressionIsModelled checks the second rule of §19.8.8.1: a for
// expression whose "in" operand is grounded is assessed by the general rules
// rather than abandoned. Before §19.8.8.1 was implemented every for
// expression reached analyzer.unknown(), which suppressed the verdict of the
// whole enclosing construct.
func TestForExpressionIsModelled(t *testing.T) {
	p, known := analyze31(t, "for $i in 1 to 3 return $i * 2")
	if !known {
		t.Fatal("a for expression over a grounded range was not modelled; " +
			"§19.8.8.1 gives it the general rules")
	}
	if !p.streamable() {
		t.Errorf("for $i in 1 to 3 return $i*2 = %v and %v, want streamable: "+
			"§19.8.8.1's note calls it \"clearly streamable\"", p.posture, p.sweep)
	}
}

// TestForExpressionRejectsNonGroundedIn checks the FIRST rule of §19.8.8.1:
// "If S is not grounded, then roaming and free-ranging."
//
// The spec explains why in the following note: the rule "prevents the
// variable being bound to a node in a streamed document. This disallows
// expressions of the form for $x in child::section return $x/para".
//
// forExpr does not test the posture itself; it gives S the usage navigation,
// which §19.8.1 makes free-ranging from every non-grounded posture, so rule 2
// carries rule 1. The pair of cases below is what holds that reading honest:
// the striding "in" must be rejected, and the grounded-but-consuming one --
// the spec's own contrasting example, "the in expression can also be
// consuming, for example for $e in copy-of(emp) return $e/salary" -- must not
// be. Giving S any usage other than navigation breaks one or the other.
func TestForExpressionRejectsNonGroundedIn(t *testing.T) {
	p, known := analyze31(t, "for $x in child::section return $x/para")
	if !known {
		t.Fatal("the expression was not modelled, so the rule was not reached")
	}
	if p.streamable() {
		t.Errorf("for $x in child::section return $x/para = %v and %v, want "+
			"roaming and free-ranging: §19.8.8.1's first rule rejects a "+
			"non-grounded \"in\" expression", p.posture, p.sweep)
	}

	// The other side of the same rule: a grounded "in" expression is allowed
	// even when it consumes. This is the spec's own example.
	p, known = analyze31(t, "for $e in copy-of(emp) return $e/salary")
	if !known {
		t.Fatal("the grounded-in expression was not modelled")
	}
	if !p.streamable() {
		t.Errorf("for $e in copy-of(emp) return $e/salary = %v and %v, want "+
			"streamable: §19.8.8.1's note gives it as a consuming but "+
			"grounded \"in\" expression", p.posture, p.sweep)
	}
}

// TestForReturnIsHigherOrder checks §19.8.8.1's operand roles: "The return
// expression (R). This is a higher-order operand with usage transmission."
//
// The spec's note is the test: "The fact that the return clause is a
// higher-order operand prevents it from being a consuming expression, for
// example for $i in 1 to 3 return salary."
func TestForReturnIsHigherOrder(t *testing.T) {
	p, known := analyze31(t, "for $i in 1 to 3 return salary")
	if !known {
		t.Fatal("the expression was not modelled, so the rule was not reached")
	}
	if p.streamable() {
		t.Errorf("for $i in 1 to 3 return salary = %v and %v, want roaming and "+
			"free-ranging: §19.8.8.1 makes the return clause a higher-order "+
			"operand, which may not consume", p.posture, p.sweep)
	}
}

// TestQuantifiedSatisfiesIsHigherOrder checks §19.8.8.2's operand roles.
// Its note gives both directions:
//
//	"some $i in 1 to 3 satisfies author[$i] eq "Kay" is not streamable.
//	 Use of a motionless expression that accesses streamed nodes is however
//	 allowed, for example some $i in 1 to 3 satisfies @grade = $i."
func TestQuantifiedSatisfiesIsHigherOrder(t *testing.T) {
	p, known := analyze31(t, `some $i in 1 to 3 satisfies author[$i] eq "Kay"`)
	if !known {
		t.Fatal("the quantified expression was not modelled")
	}
	if p.streamable() {
		t.Errorf("some $i in 1 to 3 satisfies author[$i] eq \"Kay\" = %v and %v, "+
			"want roaming and free-ranging (§19.8.8.2's note)", p.posture, p.sweep)
	}

	p, known = analyze31(t, "some $i in 1 to 3 satisfies @grade = $i")
	if !known {
		t.Fatal("the motionless quantified expression was not modelled")
	}
	if !p.streamable() {
		t.Errorf("some $i in 1 to 3 satisfies @grade = $i = %v and %v, want "+
			"streamable: §19.8.8.2's note allows a motionless expression that "+
			"accesses streamed nodes", p.posture, p.sweep)
	}
}

// TestStreamingParamInHigherOrderOperandIsRoaming checks the §19.8.8.11 table
// for the "no" (non-singular) row: a reference to the streaming parameter of
// an absorbing function that sits inside a higher-order operand is roaming,
// not grounded.
//
// This is su-absorbing-906 in the W3C suite, whose body is
// "for $i in 1 to 3 return name($element[$i])" and which the catalog expects
// to be rejected with XTSE3430.
func TestStreamingParamInHigherOrderOperandIsRoaming(t *testing.T) {
	doc := parseSheet(t, `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:f="urn:f">
  <xsl:function name="f:enumerate" as="xs:string*" streamability="absorbing">
    <xsl:param name="element" as="node()*"/>
    <xsl:sequence select="for $i in 1 to 3 return name($element[$i])"/>
  </xsl:function>
</xsl:stylesheet>`)
	funcs := collectStreamFuncs(doc)
	f, ok := funcs[funcKey{uri: "urn:f", local: "enumerate", arity: 1}]
	if !ok {
		t.Fatal("f:enumerate not collected")
	}
	p, known := analyzeFunctionBody(f, funcs)
	if !known {
		t.Fatal("the function body was not modelled, so no verdict was reached")
	}
	if satisfiesCategory(f, p) {
		t.Errorf("the body is %v and %v, which was accepted for the absorbing "+
			"category; §19.8.8.11 makes a reference inside a higher-order "+
			"operand roaming, so the body must fail §19.8.5.2", p.posture, p.sweep)
	}
}

// TestInstructionBodySeesStylesheetFunctions checks that a call on a
// declared-streamable stylesheet function inside a streamable instruction is
// assessed under §19.8.5's call rules rather than left unmodelled.
//
// The function table was built by checkStreamability for the body check but
// never reached the instruction analyser, so every such call fell to
// funcCall's "not modelled" branch. That cleared known for the whole body,
// which suppressed the XTSE3430 the call rules exist to raise. This is
// su-shallow-descent-907: the second argument of a shallow-descent call is
// consuming, which §19.8.5.5 makes roaming and free-ranging.
func TestInstructionBodySeesStylesheetFunctions(t *testing.T) {
	doc := parseSheet(t, `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:f="urn:f">
  <xsl:function name="f:g" streamability="shallow-descent">
    <xsl:param name="n" as="node()"/>
    <xsl:param name="o"/>
    <xsl:sequence select="$n/*[empty($o)]"/>
  </xsl:function>
  <xsl:template name="main">
    <xsl:source-document streamable="yes" href="in.xml">
      <xsl:for-each select="/BOOKLIST/BOOKS/ITEM">
        <xsl:sequence select="f:g(., *)"/>
      </xsl:for-each>
    </xsl:source-document>
  </xsl:template>
</xsl:stylesheet>`)
	err := checkStreamability(doc)
	if err == nil {
		t.Fatal("no error: a shallow-descent call whose second argument is " +
			"consuming is roaming and free-ranging by §19.8.5.5, so the " +
			"streamable source-document is not guaranteed-streamable")
	}
	if !strings.Contains(err.Error(), "XTSE3430") {
		t.Errorf("error = %v, want an XTSE3430", err)
	}
}

// TestValidStylesheetFunctionCallStillCompiles is the other half of the test
// above, and the one that matters more: threading the function table must not
// turn a VALID call into a rejection. This is the shape of the spec's own
// §19.8.5.5 example, and of su-shallow-descent-A in the W3C suite.
func TestValidStylesheetFunctionCallStillCompiles(t *testing.T) {
	doc := parseSheet(t, `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:f="urn:f">
  <xsl:function name="f:alternate-children" as="node()*"
                streamability="shallow-descent">
    <xsl:param name="input" as="element()"/>
    <xsl:sequence select="$input/*[position() mod 2 = 1]"/>
  </xsl:function>
  <xsl:template name="main">
    <xsl:source-document streamable="yes" href="in.xml">
      <xsl:sequence select="f:alternate-children(/BOOKLIST/BOOKS)"/>
    </xsl:source-document>
  </xsl:template>
</xsl:stylesheet>`)
	if err := checkStreamability(doc); err != nil {
		t.Errorf("a valid shallow-descent call was rejected: %v", err)
	}
}
