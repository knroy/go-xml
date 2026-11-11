package xslt

import (
	"fmt"
	"strings"
	"testing"
)

// The tests in this file are the round SM lane-01 regressions: compile-time
// walks over shared declaration graphs and name scans that were exponential or
// quadratic in stylesheet size.

// TestSM01CharMapLadderMemoised: a 40-level ladder where each level's two maps
// both include both maps of the level below. Re-expanding per path visits
// 2^40 nodes; resolving each map once visits 80.
func TestSM01CharMapLadderMemoised(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<xsl:character-map name="top" use-character-maps="a40 b40"/>`)
	for i := 40; i >= 1; i-- {
		inc := ""
		if i > 1 {
			inc = fmt.Sprintf(` use-character-maps="a%d b%d"`, i-1, i-1)
		}
		fmt.Fprintf(&b, `<xsl:character-map name="a%d"%s/>`, i, inc)
		fmt.Fprintf(&b, `<xsl:character-map name="b%d"%s/>`, i, inc)
	}
	// The deepest pair carries the substitution, so it must reach the top.
	s := strings.Replace(b.String(), `<xsl:character-map name="a1"/>`,
		`<xsl:character-map name="a1"><xsl:output-character character="@" string="DEEP"/></xsl:character-map>`, 1)
	got, err := runErr(t, charMapSheet(s, "@"), `<a/>`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "<r>DEEP</r>" {
		t.Errorf("got %q, want <r>DEEP</r>", got)
	}
}
