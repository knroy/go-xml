package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// compileAccumSheet compiles a stylesheet and returns the compilation error.
func compileAccumSheet(t *testing.T, sheet string) error {
	t.Helper()
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	_, err = Compile(stree.Root, CompileOptions{})
	return err
}

// accumSheet wraps an xsl:accumulator declaration in a minimal stylesheet.
func accumSheet(decl string) string {
	return `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema">` + decl +
		`<xsl:template match="/"><out/></xsl:template></xsl:stylesheet>`
}

// §18.2.9 lists five conditions an accumulator declared streamable="yes" must
// satisfy to be guaranteed-streamable. Conditions 2 and 3 defer to §19.8.10's
// classification of patterns; conditions 4 and 5 require the initial-value and
// select expressions to be grounded and motionless.
//
// Each subtest below asserts one of those conditions directly, using either a
// pattern the spec itself classifies in §19.8.10's two worked lists or an
// expression whose posture and sweep §19.5 and §19.7 fix.
func TestAccumulatorStreamabilityXTSE3430(t *testing.T) {
	// Condition 3, and §19.8.10's "p[b] (the predicate is not motionless)".
	// The predicate reads a child of the matched node, which advances the
	// input stream, so the pattern is free-ranging. This is accumulator-029s.
	t.Run("rule match with a consuming predicate", func(t *testing.T) {
		err := compileAccumSheet(t, accumSheet(`
			<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
			  <xsl:accumulator-rule match="fig[caption]" select="$value + 2"/>
			</xsl:accumulator>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("match=\"fig[caption]\" has a non-motionless predicate, "+
				"so §18.2.9 condition 3 fails; want XTSE3430, got: %v", err)
		}
	})

	// Condition 5. "string-length(caption)" absorbs a child of the matched
	// node, which is consuming, not motionless. This is accumulator-030s.
	t.Run("rule select that consumes", func(t *testing.T) {
		err := compileAccumSheet(t, accumSheet(`
			<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
			  <xsl:accumulator-rule match="fig" select="$value + string-length(caption)"/>
			</xsl:accumulator>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("select reading a child is consuming, so §18.2.9 "+
				"condition 5 fails; want XTSE3430, got: %v", err)
		}
	})

	// Condition 5, the "grounded" half. "$value, ." returns the context node
	// itself, which under as="item()*" is not atomized: the accumulator's
	// value would hold a node of the streamed tree, so its posture is
	// striding rather than grounded. The sweep is motionless throughout,
	// which is what makes this test bite on posture alone. This is
	// accumulator-076.
	t.Run("rule select that is motionless but not grounded", func(t *testing.T) {
		err := compileAccumSheet(t, accumSheet(`
			<xsl:accumulator name="a" as="item()*" initial-value="0" streamable="yes">
			  <xsl:accumulator-rule match="fig" select="$value, ."/>
			</xsl:accumulator>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("select returning \".\" is motionless but not grounded, "+
				"so §18.2.9 condition 5 fails; want XTSE3430, got: %v", err)
		}
	})

	// Condition 2: the applies-to pattern is held to the same rule as a
	// rule's match pattern.
	t.Run("applies-to with a consuming predicate", func(t *testing.T) {
		err := compileAccumSheet(t, accumSheet(`
			<xsl:accumulator name="a" as="xs:integer" initial-value="0"
			                 streamable="yes" applies-to="doc[title]">
			  <xsl:accumulator-rule match="fig" select="$value + 1"/>
			</xsl:accumulator>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("applies-to=\"doc[title]\" is free-ranging, so §18.2.9 "+
				"condition 2 fails; want XTSE3430, got: %v", err)
		}
	})

	// Condition 4: the initial-value expression must be grounded and
	// motionless. Reading a child of the root is consuming.
	t.Run("initial-value that consumes", func(t *testing.T) {
		err := compileAccumSheet(t, accumSheet(`
			<xsl:accumulator name="a" as="xs:integer" initial-value="count(doc/fig)"
			                 streamable="yes">
			  <xsl:accumulator-rule match="fig" select="$value + 1"/>
			</xsl:accumulator>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("initial-value reading the tree is consuming, so §18.2.9 "+
				"condition 4 fails; want XTSE3430, got: %v", err)
		}
	})

	// Condition 1: none of this applies to an accumulator that does not claim
	// streamability. The same declaration that failed above must compile.
	t.Run("not streamable is unconstrained", func(t *testing.T) {
		if err := compileAccumSheet(t, accumSheet(`
			<xsl:accumulator name="a" as="xs:integer" initial-value="0">
			  <xsl:accumulator-rule match="fig[caption]" select="$value + string-length(caption)"/>
			</xsl:accumulator>`)); err != nil {
			t.Fatalf("§18.2.9 constrains only streamable=\"yes\"; this must "+
				"compile, got: %v", err)
		}
	})
}

// The other half of the rule, and the one that matters more: a
// guaranteed-streamable accumulator must still compile. A spurious XTSE3430
// rejects a valid stylesheet at compile time, with no way around it.
//
// Every pattern named here appears in §19.8.10's own list of motionless
// patterns, and every select expression is grounded and motionless by §19.5
// and §19.7.
func TestAccumulatorStreamabilityAccepts(t *testing.T) {
	cases := []struct {
		name string
		decl string
		why  string
	}{
		{
			name: "attribute select",
			decl: `<xsl:accumulator name="a" as="xs:double" initial-value="0" streamable="yes">
			         <xsl:accumulator-rule match="transaction" select="$value + @amount"/>
			       </xsl:accumulator>`,
			why: "@amount is on the attribute axis, whose nodes have no " +
				"children, so absorbing it is motionless (§19.8.1)",
		},
		{
			name: "conditional over attributes",
			decl: `<xsl:accumulator name="a" as="xs:double" initial-value="0" streamable="yes">
			         <xsl:accumulator-rule match="transaction"
			           select="if (@amount &lt; $value) then @amount else $value"/>
			       </xsl:accumulator>`,
			why: "as=\"xs:double\" atomizes the result under §18.2.1's " +
				"function conversion rules, which grounds it",
		},
		{
			name: "rooted path to a text node",
			decl: `<xsl:accumulator name="a" as="xs:string?" initial-value="()" streamable="yes">
			         <xsl:accumulator-rule match="/html/head/title/text()" select="string(.)"/>
			       </xsl:accumulator>`,
			why: "a leading \"/\" is not a RootedPath — §19.8.10 lists " +
				"\"/*\" and \"p/q\" as motionless — and a text node has no " +
				"children, so string(.) is motionless",
		},
		{
			name: "zero-arity string on a text node",
			decl: `<xsl:accumulator name="a" as="xs:string?" initial-value="()" streamable="yes">
			         <xsl:accumulator-rule match="header-item/value/text()" select="string()"/>
			       </xsl:accumulator>`,
			why: "string() is equivalent to string(.), so it must classify " +
				"identically on a text-node context item",
		},
		{
			name: "motionless predicate from the spec's list",
			decl: `<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
			         <xsl:accumulator-rule match="p[@status='red']" select="$value + 1"/>
			       </xsl:accumulator>`,
			why: "§19.8.10 lists p[@status='red'] as motionless",
		},
		{
			name: "predicate on an attribute step",
			decl: `<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
			         <xsl:accumulator-rule match="@price[starts-with(., '$')]" select="$value + 1"/>
			       </xsl:accumulator>`,
			why: "§19.8.10 lists @price[starts-with(., '$')] as motionless",
		},
		{
			name: "predicate on a text step",
			decl: `<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
			         <xsl:accumulator-rule match="text()[starts-with(., '$')]" select="$value + 1"/>
			       </xsl:accumulator>`,
			why: "§19.8.10 lists text()[starts-with(., '$')] as motionless",
		},
		{
			name: "union of motionless patterns",
			decl: `<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
			         <xsl:accumulator-rule match="p|q" select="$value + 1"/>
			       </xsl:accumulator>`,
			why: "§19.8.10 lists p|q as motionless",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := compileAccumSheet(t, accumSheet(c.decl)); err != nil {
				t.Fatalf("this accumulator is guaranteed-streamable (%s), so "+
					"it must compile; got: %v", c.why, err)
			}
		})
	}
}

// §19.8.10's RootedPath condition is about production [6] of the pattern
// grammar — a pattern that STARTS with a variable reference or one of the
// outer function calls — and not about a leading "/". The classification must
// abandon such a pattern rather than assess it, since the analysis does not
// model those forms.
func TestPatternRootedPathNotAssessed(t *testing.T) {
	// A key() pattern is a RootedPath and so is not motionless, but the
	// classification does not model it. What matters is that it is abandoned
	// rather than guessed at: no error either way.
	err := compileAccumSheet(t, `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema">
		<xsl:key name="k" match="fig" use="@id"/>
		<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes">
		  <xsl:accumulator-rule match="key('k','x')" select="$value + 1"/>
		</xsl:accumulator>
		<xsl:template match="/"><out/></xsl:template></xsl:stylesheet>`)
	if err != nil && strings.Contains(err.Error(), "XTSE3430") {
		t.Fatalf("a key() pattern is not modelled by §19.8.10's "+
			"classification, so it must be abandoned rather than reported: %v",
			err)
	}
}

// atomicSequenceType decides whether §18.2.1's function conversion rules
// atomize the accumulator's value, which is what grounds an expression that
// returns a node. Getting this wrong in the "yes" direction would suppress
// real errors, so the reading is asserted directly.
func TestAtomicSequenceType(t *testing.T) {
	atomic := []string{
		"xs:double", "xs:integer", "xs:string", "xs:anyAtomicType",
		"xs:double?", "xs:integer*", "xs:string+", " xs:decimal ",
		"xs:untypedAtomic", "xs:dayTimeDuration",
	}
	for _, s := range atomic {
		if !atomicSequenceType(s) {
			t.Errorf("%q names an atomic sequence type, so §18.2.1's "+
				"conversion atomizes and grounds the value", s)
		}
	}
	notAtomic := []string{
		"", "item()*", "node()", "element()", "element(fig)",
		"document-node()", "map(xs:integer, xs:string)", "array(*)",
		"function(*)", "my:type", "double",
	}
	for _, s := range notAtomic {
		if atomicSequenceType(s) {
			t.Errorf("%q does not name an atomic sequence type, so the "+
				"conversion does not atomize and the expression's own "+
				"posture must stand", s)
		}
	}
}
