package xslt

import (
	"strings"
	"testing"
)

// The XSLT 3.0 PredicatePattern, "." with an optional PredicateList, follows
// the processor's version rather than the module's.
//
// It is the same rule variablePatternAllowed already applies to the "$v"
// form, and for the same reason: si-fork-115 is a version="2.0" module scoped
// XSLT30+ whose template rule is written match=".", and it is the only
// match="." below version 3.0 anywhere in the suite. Before the fix the
// module's version decided, and the rule was rejected with
// "XTSE0340: pattern \".\": not a valid pattern: .".
//
// The placement rule travels with the form. Wherever "." is admitted it is
// admitted only as a whole pattern, so a 2.0 module writing it as a union
// operand or in parentheses gets the same XTSE0340 a 3.0 module gets --
// before the fix ".|a" quietly compiled to the "a" alternative alone.
func TestPredicatePatternFollowsProcessorVersion(t *testing.T) {
	const src = `<doc><i/><i/></doc>`
	for _, v := range []string{"2.0", "3.0"} {
		sheet := func(match string) string {
			return `<xsl:stylesheet version="` + v + `" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
				<xsl:template match="/"><out><xsl:apply-templates select="/doc/i" mode="m"/></out></xsl:template>
				<xsl:template match="` + match + `" mode="m"><h/></xsl:template>
			</xsl:stylesheet>`
		}
		t.Run("whole pattern "+v, func(t *testing.T) {
			got, err := runErr(t, sheet("."), src)
			if err != nil {
				t.Fatalf("match=\".\": %v", err)
			}
			if want := "<out><h/><h/></out>"; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
		for _, bad := range []string{".|a", ".[1]|a", "(.)"} {
			t.Run("misplaced "+bad+" "+v, func(t *testing.T) {
				if _, err := runErr(t, sheet(bad), src); err == nil {
					t.Errorf("match=%q compiled, want XTSE0340", bad)
				} else if got := err.Error(); !strings.Contains(got, "XTSE0340") {
					t.Errorf("match=%q: %v, want XTSE0340", bad, err)
				}
			})
		}
	}
}
