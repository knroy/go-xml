package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// compileModeSheet compiles a stylesheet and returns the compilation error.
func compileModeSheet(t *testing.T, sheet string) error {
	t.Helper()
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	_, err = Compile(stree.Root, CompileOptions{})
	return err
}

// modeSheet wraps declarations in a stylesheet that declares a streamable
// mode "s" and applies templates in it from a streamable xsl:source-document.
func modeSheet(decls string) string {
	return `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema">
		<xsl:mode name="s" streamable="yes"/>` + decls +
		`<xsl:template name="main" match="/"><out/></xsl:template></xsl:stylesheet>`
}

// §19.6's third clause gives a template rule in a mode declared
// streamable="yes" a context posture of striding, which is what makes
// §19.8.10 apply to that rule's match pattern. §19.8.10 then says a pattern is
// motionless if and only if it has no RootedPath, every top-level predicate is
// motionless and non-positional, and it references no streaming parameter; a
// pattern that is not motionless is free-ranging, and a free-ranging construct
// in a streamable context is XTSE3430.
//
// Every pattern named below is one §19.8.10 itself classifies, in the two
// worked lists it closes with, so each subtest asserts the spec's own verdict
// rather than this implementation's.
func TestStreamableModePatternXTSE3430(t *testing.T) {
	// §19.8.10 lists "p[1] (contains a positional predicate: return type is
	// numeric)" among its non-motionless patterns. This is streamable-143,
	// and streamable-123 in its schema-aware form.
	t.Run("numeric predicate", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="p[1]" mode="s"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.10 classifies \"p[1]\" as not motionless because "+
				"the predicate is numeric; want XTSE3430, got: %v", err)
		}
	})

	// §19.8.10: "p[last()] (contains a positional predicate: calls last())".
	// This is streamable-144.
	t.Run("predicate calling last", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="p[last()]" mode="s"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.10 classifies \"p[last()]\" as not motionless "+
				"because the predicate calls last(); want XTSE3430, got: %v", err)
		}
	})

	// §19.8.10: "p[position() gt 2] (contains a positional predicate: calls
	// position())".
	t.Run("predicate calling position", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="p[position() gt 2]" mode="s"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.10 classifies \"p[position() gt 2]\" as not "+
				"motionless; want XTSE3430, got: %v", err)
		}
	})

	// §19.8.10: "p[b] (the predicate is not motionless)". Reading a child of
	// the matched node advances the input stream.
	t.Run("predicate reading a child", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="p[b]" mode="s"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.10 classifies \"p[b]\" as not motionless because "+
				"the predicate is not motionless; want XTSE3430, got: %v", err)
		}
	})

	// §19.8.10: "p[starts-with(., '$')] (the predicate is not motionless)".
	// The context item of the predicate is an element, so atomizing it reads
	// the element's descendants. This is streamable-142's
	// match=".[starts-with(., ':B:')]", which the test's own stylesheet
	// annotates "NOT MOTIONLESS".
	t.Run("predicate atomizing an element", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="p[starts-with(., '$')]" mode="s"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.10 classifies \"p[starts-with(., '$')]\" as not "+
				"motionless; want XTSE3430, got: %v", err)
		}
	})

	// §19.8.10: "p[preceding-sibling::p[1] = ''] (the predicate is not
	// motionless)". This is streamable-122's shape -- a predicate that
	// navigates a reverse axis and then compares.
	t.Run("predicate on a reverse axis", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="p[preceding-sibling::p[1] = '']" mode="s"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.10 classifies \"p[preceding-sibling::p[1] = '']\" "+
				"as not motionless; want XTSE3430, got: %v", err)
		}
	})
}

// The other half of §19.8.10 is the list of patterns it declares motionless.
// A motionless pattern is grounded, and a grounded construct in a streamable
// context is guaranteed-streamable, so none of these may be refused. This is
// the arm that catches an over-eager rule: a check that rejected every
// predicate would pass the table above and fail here.
func TestStreamableModeMotionlessPatternsAccepted(t *testing.T) {
	// Each of these appears verbatim in §19.8.10's list of motionless
	// patterns, except where noted.
	motionless := []string{
		`*`,
		`p`,
		`p|q`,
		`p/q`,
		`p[@status='red']`,
		`p[@class or @style]`,
		`p[@status]`,
		`p[@class | @style]`,
		`p[contains(@class, ':')]`,
		`p[substring-after(@class, ':')]`,
		// §19.8.10 lists "text()[starts-with(., '$')]" and
		// "@price[starts-with(., '$')]": atomizing an attribute or a text
		// node reads nothing further, so the predicate stays motionless
		// where the same predicate on an element does not.
		`text()[starts-with(., '$')]`,
		`@price`,
		`@price[starts-with(., '$')]`,
	}
	for _, pat := range motionless {
		t.Run(pat, func(t *testing.T) {
			err := compileModeSheet(t, modeSheet(
				`<xsl:template match="`+pat+`" mode="s"/>`))
			if err != nil && strings.Contains(err.Error(), "XTSE3430") {
				t.Fatalf("§19.8.10 lists %q among its motionless patterns, "+
					"so it is guaranteed-streamable and must not be "+
					"refused; got: %v", pat, err)
			}
		})
	}
}

// §19.8.10 decides "non-positional" from the predicate's STATIC TYPE, not from
// whether a numeric literal appears in its text: the predicate is non-numeric
// when the intersection of its static type with U{xs:decimal, xs:double,
// xs:float} is empty. A comparison returns xs:boolean whatever its operands
// are, so a numeric literal inside one changes nothing.
//
// The spec's own two lists make the distinction, and both entries are asserted
// here: "p[@status = $status-codes[1]]" is motionless, while "p[$pnum + 1]" is
// not. accumulator-055s is the case that turns on it in the suite.
func TestStreamableModeNumericLiteralInsideComparison(t *testing.T) {
	// §19.8.10 lists this verbatim among its MOTIONLESS patterns, even
	// though "[1]" appears in it, because the predicate is a comparison.
	t.Run("numeric literal inside a comparison", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:variable name="status-codes" select="1,2,3"/>
			 <xsl:template match="p[@status = $status-codes[1]]" mode="s"/>`))
		if err != nil && strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.10 lists \"p[@status = $status-codes[1]]\" among "+
				"its motionless patterns: the predicate's static type is "+
				"xs:boolean, so it is non-positional; got: %v", err)
		}
	})

	// The contrast, listed among the patterns that are NOT motionless:
	// "p[$pnum + 1] (contains a positional predicate: return type is
	// numeric)". Arithmetic really is numeric, so the rescue must not reach
	// it.
	t.Run("arithmetic predicate stays positional", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:variable name="pnum" select="1"/>
			 <xsl:template match="p[$pnum + 1]" mode="s"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("§19.8.10 lists \"p[$pnum + 1]\" as not motionless "+
				"because its return type is numeric; want XTSE3430, got: %v", err)
		}
	})

	// A comparison that calls position() is still positional: §19.8.10's
	// first condition names the function outright and does not consult the
	// static type at all.
	t.Run("comparison calling position stays positional", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="p[position() gt 2]" mode="s"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("\"p[position() gt 2]\" is a comparison but calls "+
				"position(), which §19.8.10 disqualifies outright; want "+
				"XTSE3430, got: %v", err)
		}
	})
}

// A pattern is only judged when its rule's mode is declared streamable.
// §19.6's clause is about "a template rule whose mode is declared with
// streamable='yes'"; a rule in any other mode has a roaming context posture
// and §19.8.10 does not apply to it at all. Refusing one would reject a
// stylesheet that is perfectly valid, which is the failure mode the whole
// analysis is built to avoid.
func TestNonStreamableModePatternsUnjudged(t *testing.T) {
	// "p[1]" is the pattern §19.8.10 calls positional, and the test above
	// requires XTSE3430 for it inside mode "s". In the unnamed mode, which
	// no xsl:mode declares streamable, the very same pattern is fine.
	t.Run("positional pattern in an undeclared mode", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match="p[1]"/>`))
		if err != nil && strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("the unnamed mode is not declared streamable, so "+
				"§19.8.10 does not apply to a rule in it; got: %v", err)
		}
	})

	// And with no streamable mode declared anywhere, nothing is judged.
	t.Run("positional pattern with no streamable mode at all", func(t *testing.T) {
		err := compileModeSheet(t, `<xsl:stylesheet version="3.0"
			xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			<xsl:template match="p[1]"/>
			<xsl:template match="/"><out/></xsl:template></xsl:stylesheet>`)
		if err != nil && strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("no mode is declared streamable, so no pattern is "+
				"assessed; got: %v", err)
		}
	})
}

// The unnamed mode can itself be declared streamable, and then a rule with no
// @mode belongs to it. streamable-143 and -144 are written exactly this way --
// an xsl:mode with no @name and streamable="yes", and a single rule carrying
// no mode attribute -- so the unnamed case is what those two cases turn on.
func TestStreamableUnnamedModePattern(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:mode streamable="yes" on-no-match="shallow-copy"/>
		<xsl:template match="p[1]"/>
		</xsl:stylesheet>`
	err := compileModeSheet(t, sheet)
	if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
		t.Fatalf("an xsl:mode with no name declares the unnamed mode "+
			"streamable, so a rule with no @mode is in it and \"p[1]\" is "+
			"not motionless; want XTSE3430, got: %v", err)
	}
}
