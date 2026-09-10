package xslt

import (
	"strings"
	"testing"
)

// An accumulator rule may read its own accumulator, and only SOME of those
// reads are circular. §18.2.4 records a node's pre-descent value before its
// children are visited, so an end-phase rule asking accumulator-before(.) is
// asking for a value that already exists; evaluate-046 does exactly that, to
// hand the static variables collected so far to xsl:evaluate. What XTDE3400
// forbids is a rule reading the value it is itself computing.
//
// The walk used to refuse every re-entrant read as circular, which was the
// recorded cause of evaluate-046's failure.

func accumReentrySheet(startRule, endRule string) string {
	return `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:mode use-accumulators="a"/>
		<xsl:accumulator name="a" initial-value="1" as="xs:integer">
		  <xsl:accumulator-rule match="r" select="` + startRule + `"/>
		  <xsl:accumulator-rule match="r" phase="end" select="` + endRule + `"/>
		</xsl:accumulator>
		<xsl:template match="/"><out><xsl:value-of select="accumulator-after('a')"/></out></xsl:template>
		</xsl:stylesheet>`
}

func TestAccumulatorRuleReadsOwnAccumulator(t *testing.T) {
	// The start rule leaves 2 as r's pre-descent value; the end rule reads
	// it back and multiplies. The document node's post-descent value is
	// what the walk leaves behind, 20.
	t.Run("end rule reads its own pre-descent value", func(t *testing.T) {
		got, err := runSheet(t, accumReentrySheet(
			"$value + 1", "accumulator-before('a') * 10"))
		if err != nil {
			t.Fatalf("an end-phase rule reading accumulator-before of its "+
				"own node asks for a value already recorded; got: %v", err)
		}
		if got != "<out>20</out>" {
			t.Fatalf("got %q, want <out>20</out>", got)
		}
	})

	// The post-descent value of r IS what the end rule computes.
	t.Run("end rule reading its own post-descent value is circular", func(t *testing.T) {
		_, err := runSheet(t, accumReentrySheet(
			"$value + 1", "accumulator-after('a')"))
		if err == nil || !strings.Contains(err.Error(), "XTDE3400") {
			t.Fatalf("want XTDE3400, got: %v", err)
		}
	})

	// The pre-descent value of r IS what the start rule computes.
	t.Run("start rule reading its own pre-descent value is circular", func(t *testing.T) {
		_, err := runSheet(t, accumReentrySheet(
			"accumulator-before('a')", "$value"))
		if err == nil || !strings.Contains(err.Error(), "XTDE3400") {
			t.Fatalf("want XTDE3400, got: %v", err)
		}
	})
}
