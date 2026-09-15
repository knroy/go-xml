package xslt

import (
	"strings"
	"testing"
)

// §19.8.9.2 gives fn:accumulator-before a two-line rule of its own: "If the
// argument to accumulator-before is motionless, the function call is grounded
// and motionless. Otherwise, the function call is roaming and free-ranging."
//
// The rule matters less for what it refuses than for what it lets the
// analysis SEE. Before it existed a call on accumulator-before was
// unmodelled, and an expression containing one was abandoned whole -- so a
// consuming call on accumulator-after standing beside it in
// "accumulator-after('w') - accumulator-before('w')" was never assessed, and
// accumulator-059 compiled.

// accumModeSheet declares a streamable accumulator "a" and a streamable
// unnamed mode, and wraps body in a template rule for "section" in it.
func accumModeSheet(body string) string {
	return `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema">
		<xsl:accumulator name="a" initial-value="0" streamable="yes" as="xs:integer">
		  <xsl:accumulator-rule match="text()" select="$value + 1"/>
		</xsl:accumulator>
		<xsl:mode streamable="yes" on-no-match="shallow-copy" use-accumulators="a"/>
		<xsl:template match="section">` + body + `</xsl:template>
		</xsl:stylesheet>`
}

func TestAccumulatorBeforeStreamability(t *testing.T) {
	// accumulator-059's shape: the difference of the two values is asked
	// for BEFORE the descent. accumulator-before is grounded and motionless
	// by §19.8.9.2; accumulator-after has no consuming preceding sibling,
	// so §19.8.9.1 rule 8 makes it consuming; and the xsl:apply-templates
	// that follows is consuming too. Two consuming operands in one sequence
	// constructor are roaming and free-ranging (§19.8.1).
	t.Run("pre-descent difference is refused", func(t *testing.T) {
		err := compileModeSheet(t, accumModeSheet(
			`<xsl:copy>`+
				`<xsl:apply-templates select="@*"/>`+
				`<p><xsl:value-of select="accumulator-after('a') - accumulator-before('a')"/></p>`+
				`<xsl:apply-templates/>`+
				`</xsl:copy>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("a consuming accumulator-after before a consuming "+
				"apply-templates; want XTSE3430, got: %v", err)
		}
	})

	// THE ACCEPTANCE DIRECTION: §18.2's own example. "The stylesheet will
	// output at the end of each section a count of the number of words in
	// that section" -- the same expression after the descent.
	t.Run("post-descent difference is the spec's example", func(t *testing.T) {
		if err := compileModeSheet(t, accumModeSheet(
			`<xsl:apply-templates/>`+
				`<xsl:value-of select="accumulator-after('a') - accumulator-before('a')"/>`)); err != nil {
			t.Fatalf("§18.2's worked example must compile; got: %v", err)
		}
	})

	// A pre-descent value is always available, wherever it is asked for.
	t.Run("accumulator-before before the descent is motionless", func(t *testing.T) {
		if err := compileModeSheet(t, accumModeSheet(
			`<xsl:value-of select="accumulator-before('a')"/>`+
				`<xsl:apply-templates/>`)); err != nil {
			t.Fatalf("accumulator-before is grounded and motionless and "+
				"the stylesheet is valid; got: %v", err)
		}
	})

	// "Otherwise, the function call is roaming and free-ranging": a name
	// computed by reading the streamed children is not motionless.
	t.Run("a consuming argument is free-ranging", func(t *testing.T) {
		err := compileModeSheet(t, accumModeSheet(
			`<xsl:value-of select="accumulator-before(string(*))"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("an accumulator name read from the stream; want "+
				"XTSE3430, got: %v", err)
		}
	})
}
