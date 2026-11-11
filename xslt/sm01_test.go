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

// TestSM01AttrSetLadderMemoised: a 40-level attribute-set ladder where each
// level's two sets both use both sets of the level below runs 2^40 set bodies
// when expanded per path. Each set must run once, and the diamond's last
// occurrence must still decide the value: b writes x="B" but c, used again
// after b through d, writes x="C" last.
func TestSM01AttrSetLadderMemoised(t *testing.T) {
	var b strings.Builder
	for i := 40; i >= 1; i-- {
		inc := ""
		if i > 1 {
			inc = fmt.Sprintf(` use-attribute-sets="a%d b%d"`, i-1, i-1)
		}
		fmt.Fprintf(&b, `<xsl:attribute-set name="a%d"%s/>`, i, inc)
		fmt.Fprintf(&b, `<xsl:attribute-set name="b%d"%s/>`, i, inc)
	}
	sheet := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes"/>` + b.String() + `
		<xsl:attribute-set name="top" use-attribute-sets="sb sd a40"/>
		<xsl:attribute-set name="sb" use-attribute-sets="sc"><xsl:attribute name="x">B</xsl:attribute></xsl:attribute-set>
		<xsl:attribute-set name="sd" use-attribute-sets="sc"/>
		<xsl:attribute-set name="sc"><xsl:attribute name="x">C</xsl:attribute></xsl:attribute-set>
		<xsl:template match="/"><r xsl:use-attribute-sets="top"/></xsl:template>
	</xsl:stylesheet>`
	if got := run(t, sheet, `<a/>`); got != `<r x="C"/>` {
		t.Errorf("got %q, want <r x=\"C\"/>", got)
	}
}
