package xslt

import (
	"strings"
	"testing"
)

// §19.8.9.1 gives fn:accumulator-after its own ordered cascade, which the
// general rules of §19.8.1 do not describe. The rule that decides the cases
// below is the last one:
//
//	If no enclosing node N of the function call has a preceding sibling node
//	P such that (a) N and P are part of the same sequence constructor, and
//	(b) the sweep of P is consuming, then the function call is consuming.
//
// Each subtest asserts a verdict the spec states or its own notes work out.

// accumSheet wraps a template body in a stylesheet with a streamable
// accumulator and a streamable xsl:source-document over it.
func accumAfterSheet(body string) string {
	return `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema">
		<xsl:accumulator name="a" initial-value="0" streamable="yes" as="xs:integer">
		  <xsl:accumulator-rule match="*" select="$value + 1"/>
		</xsl:accumulator>
		<xsl:mode streamable="yes" on-no-match="shallow-copy" use-accumulators="a"/>
		<xsl:template name="main">
		  <out>
		    <xsl:source-document streamable="yes" href="in.xml" use-accumulators="a">` +
		body + `</xsl:source-document>
		  </out>
		</xsl:template>
		</xsl:stylesheet>`
}

func TestAccumulatorAfterSweepXTSE3430(t *testing.T) {
	// error-3420a. The call on accumulator-after has no preceding consuming
	// sibling, so rule 8 makes it consuming; xsl:copy-of is consuming too,
	// and §19.8.1 makes a construct with two consuming operands roaming and
	// free-ranging. The stylesheet's own comment states this reasoning.
	t.Run("consuming call before a consuming instruction", func(t *testing.T) {
		err := compileModeSheet(t, accumAfterSheet(
			`<xsl:value-of select="accumulator-after('a')"/>`+
				`<xsl:copy-of select="."/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("two consuming sub-expressions in one sequence "+
				"constructor; want XTSE3430, got: %v", err)
		}
	})

	// THE ACCEPTANCE DIRECTION, and the one that matters. §19.8.9.1's own
	// note: "In a sequence constructor that contains a consuming instruction
	// such as <xsl:apply-templates/>, it allows any number of calls on
	// accumulator-after to appear in instructions that follow the call on
	// <xsl:apply-templates/>." Reversing the order of the two instructions
	// above must therefore compile.
	t.Run("a call after a consuming instruction is motionless", func(t *testing.T) {
		if err := compileModeSheet(t, accumAfterSheet(
			`<xsl:copy-of select="."/>`+
				`<xsl:value-of select="accumulator-after('a')"/>`)); err != nil {
			t.Fatalf("a call on accumulator-after following a consuming "+
				"instruction is motionless and the stylesheet is valid, "+
				"but it was refused: %v", err)
		}
	})

	// The same acceptance case with the call nested one level deeper, inside
	// a literal result element. Rule 8 asks about "enclosing nodes" -- the
	// call's ancestors -- so the consuming preceding sibling found in the
	// OUTER sequence constructor still answers for a call in the inner one.
	// This is accumulator-058's shape, and getting it wrong refused five
	// valid stylesheets.
	t.Run("a nested call inherits the outer preceding sibling", func(t *testing.T) {
		if err := compileModeSheet(t, accumAfterSheet(
			`<xsl:copy-of select="."/>`+
				`<result><xsl:value-of select="accumulator-after('a')"/></result>`)); err != nil {
			t.Fatalf("the consuming preceding sibling belongs to the outer "+
				"sequence constructor, which rule 8's \"enclosing node\" "+
				"reaches, so the stylesheet is valid; got: %v", err)
		}
	})

	// Two calls, each in its own instruction, after a consuming one. The
	// note: "subsequent instructions containing such a call are motionless.
	// So it is possible to have two or more calls on accumulator-after
	// provided they appear in different instructions."
	t.Run("several calls after a consuming instruction", func(t *testing.T) {
		if err := compileModeSheet(t, accumAfterSheet(
			`<xsl:copy-of select="."/>`+
				`<xsl:value-of select="accumulator-after('a')"/>`+
				`<xsl:value-of select="accumulator-after('a')"/>`)); err != nil {
			t.Fatalf("several calls in distinct instructions after a "+
				"consuming one are motionless; got: %v", err)
		}
	})
}

// accumulatorAfterSweep is the cascade itself. The two early rules are
// unreachable from the suite -- every case there has a striding context item
// that can have children -- so they are asserted directly.
func TestAccumulatorAfterSweepRules(t *testing.T) {
	// Rule 2: "If the context posture is grounded, the function is
	// motionless." The accumulator's target is not a streamed node.
	t.Run("grounded context posture", func(t *testing.T) {
		sw, ok := accumulatorAfterSweep(accumAfterState{}, postureGrounded, true)
		if !ok || sw != sweepMotionless {
			t.Errorf("a grounded context posture makes the call motionless; "+
				"got %v, ok=%v", sw, ok)
		}
	})

	// Rule 3: a context item that cannot have children -- a text node, say --
	// makes the call motionless whatever surrounds it, because both the
	// pre- and post-descent values are known before any construct sees it.
	t.Run("childless context item", func(t *testing.T) {
		sw, ok := accumulatorAfterSweep(accumAfterState{}, postureStriding, false)
		if !ok || sw != sweepMotionless {
			t.Errorf("a childless context item makes the call motionless; "+
				"got %v, ok=%v", sw, ok)
		}
	})

	// Rule 8 and the closing "otherwise", the two the suite exercises.
	t.Run("no preceding consuming sibling is consuming", func(t *testing.T) {
		sw, ok := accumulatorAfterSweep(
			accumAfterState{known: true}, postureStriding, true)
		if !ok || sw != sweepConsuming {
			t.Errorf("rule 8 makes the call consuming; got %v, ok=%v", sw, ok)
		}
	})
	t.Run("a preceding consuming sibling leaves it motionless", func(t *testing.T) {
		sw, ok := accumulatorAfterSweep(
			accumAfterState{known: true, precedingConsuming: true},
			postureStriding, true)
		if !ok || sw != sweepMotionless {
			t.Errorf("the cascade's \"otherwise\" is motionless; got %v, "+
				"ok=%v", sw, ok)
		}
	})

	// A position the walk could not describe leaves the call unmodelled,
	// which is what keeps the check from inventing a rejection.
	t.Run("an undescribed position is withheld", func(t *testing.T) {
		if _, ok := accumulatorAfterSweep(
			accumAfterState{}, postureStriding, true); ok {
			t.Error("a position the walk did not describe must be withheld")
		}
	})
}
