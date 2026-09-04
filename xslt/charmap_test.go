package xslt

import (
	"fmt"
	"strings"
	"testing"
)

// The tests in this file cover xsl:character-map's use-character-maps
// inclusion graph: that a legal graph of any shape and any depth is merged,
// that a genuine cycle is reported as XTSE1600, and that the merge precedence
// is what it was.

// charMapSheet builds a stylesheet from a set of character-map declarations.
//
// Each map is given one xsl:output-character so the merge result can be read
// off the serialised output: the character a map maps is the map's own name
// letter, and the string it maps to names the map, so a wrong winner in the
// merge shows up as a wrong name rather than as a missing substitution.
func charMapSheet(decls, body string) string {
	return `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes" use-character-maps="top"/>
		` + decls + `
		<xsl:template match="/"><r>` + body + `</r></xsl:template>
	</xsl:stylesheet>`
}

// TestCharMapDeepLegalChain covers an acyclic inclusion chain far deeper than
// any fixed depth bound. Nothing about it is circular, so it must compile and
// the character the deepest map declares must actually be substituted.
func TestCharMapDeepLegalChain(t *testing.T) {
	const depth = 4096
	var b strings.Builder
	// top -> m1 -> m2 -> ... -> m4096, and only the deepest map maps anything.
	fmt.Fprintf(&b, `<xsl:character-map name="top" use-character-maps="m1"/>`)
	for i := 1; i < depth; i++ {
		fmt.Fprintf(&b,
			`<xsl:character-map name="m%d" use-character-maps="m%d"/>`, i, i+1)
	}
	fmt.Fprintf(&b,
		`<xsl:character-map name="m%d"><xsl:output-character character="@" string="DEEP"/></xsl:character-map>`,
		depth)

	got, err := runErr(t, charMapSheet(b.String(), "@"), `<a/>`)
	if err != nil {
		t.Fatalf("a legal acyclic chain %d deep must compile, got: %v", depth, err)
	}
	if got != "<r>DEEP</r>" {
		t.Errorf("got %q, want <r>DEEP</r>: the deepest map's entry must reach the top", got)
	}
}

// TestCharMapDiamond covers A including B and D, both of which include C. C is
// reached twice, which a global "already seen" set would mistake for a cycle,
// and every entry on every path must survive the merge.
func TestCharMapDiamond(t *testing.T) {
	decls := `
		<xsl:character-map name="top" use-character-maps="b d"/>
		<xsl:character-map name="b" use-character-maps="c">
			<xsl:output-character character="b" string="B"/>
		</xsl:character-map>
		<xsl:character-map name="d" use-character-maps="c">
			<xsl:output-character character="d" string="D"/>
		</xsl:character-map>
		<xsl:character-map name="c">
			<xsl:output-character character="c" string="C"/>
		</xsl:character-map>`
	got, err := runErr(t, charMapSheet(decls, "bcd"), `<a/>`)
	if err != nil {
		t.Fatalf("a diamond is not a cycle and must compile, got: %v", err)
	}
	if got != "<r>BCD</r>" {
		t.Errorf("got %q, want <r>BCD</r>: every branch of the diamond must merge", got)
	}
}

// TestCharMapCycles covers cycles of several shapes and depths. Every one must
// be reported as XTSE1600, and a cycle buried under a long legal prefix must be
// caught just as a shallow one is.
func TestCharMapCycles(t *testing.T) {
	deepCycle := func(n int) string {
		// top -> m1 -> ... -> mn -> m1: a legal prefix, then a cycle.
		var b strings.Builder
		fmt.Fprintf(&b, `<xsl:character-map name="top" use-character-maps="m1"/>`)
		for i := 1; i < n; i++ {
			fmt.Fprintf(&b,
				`<xsl:character-map name="m%d" use-character-maps="m%d"/>`, i, i+1)
		}
		fmt.Fprintf(&b,
			`<xsl:character-map name="m%d" use-character-maps="m1"/>`, n)
		return b.String()
	}

	cases := []struct {
		name  string
		decls string
	}{
		{"direct self-cycle", `
			<xsl:character-map name="top" use-character-maps="a"/>
			<xsl:character-map name="a" use-character-maps="a"/>`},
		{"cycle at depth 2", `
			<xsl:character-map name="top" use-character-maps="a"/>
			<xsl:character-map name="a" use-character-maps="b"/>
			<xsl:character-map name="b" use-character-maps="a"/>`},
		{"indirect cycle a b c a", `
			<xsl:character-map name="top" use-character-maps="a"/>
			<xsl:character-map name="a" use-character-maps="b"/>
			<xsl:character-map name="b" use-character-maps="c"/>
			<xsl:character-map name="c" use-character-maps="a"/>`},
		{"cycle buried 100 links deep", deepCycle(100)},
		{"cycle buried past the old depth bound", deepCycle(1000)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runErr(t, charMapSheet(tc.decls, "x"), `<a/>`)
			if err == nil {
				t.Fatal("a circular character-map inclusion must be an error")
			}
			if !strings.Contains(err.Error(), "XTSE1600") {
				t.Errorf("got %v, want XTSE1600", err)
			}
		})
	}
}

// TestCharMapMergePrecedence pins the merge order this code has always had, so
// that a change to how the graph is walked cannot quietly change who wins.
//
// Two rules are at stake. A map's own entries beat anything it includes, and
// among the maps it includes the later name in use-character-maps beats the
// earlier one.
func TestCharMapMergePrecedence(t *testing.T) {
	t.Run("own entries beat included ones", func(t *testing.T) {
		decls := `
			<xsl:character-map name="top" use-character-maps="a">
				<xsl:output-character character="x" string="OWN"/>
			</xsl:character-map>
			<xsl:character-map name="a">
				<xsl:output-character character="x" string="INCLUDED"/>
			</xsl:character-map>`
		if got := run(t, charMapSheet(decls, "x"), `<a/>`); got != "<r>OWN</r>" {
			t.Errorf("got %q, want <r>OWN</r>: a map's own entries win", got)
		}
	})

	t.Run("a later include beats an earlier one", func(t *testing.T) {
		decls := `
			<xsl:character-map name="top" use-character-maps="a b"/>
			<xsl:character-map name="a">
				<xsl:output-character character="x" string="A"/>
			</xsl:character-map>
			<xsl:character-map name="b">
				<xsl:output-character character="x" string="B"/>
			</xsl:character-map>`
		if got := run(t, charMapSheet(decls, "x"), `<a/>`); got != "<r>B</r>" {
			t.Errorf("got %q, want <r>B</r>: the later include wins", got)
		}
	})

	t.Run("an included map's own entries beat what it includes", func(t *testing.T) {
		// top includes a; a includes c and declares x itself. a's own entry
		// must beat c's, one level down from the top just as at the top.
		decls := `
			<xsl:character-map name="top" use-character-maps="a"/>
			<xsl:character-map name="a" use-character-maps="c">
				<xsl:output-character character="x" string="A"/>
			</xsl:character-map>
			<xsl:character-map name="c">
				<xsl:output-character character="x" string="C"/>
			</xsl:character-map>`
		if got := run(t, charMapSheet(decls, "x"), `<a/>`); got != "<r>A</r>" {
			t.Errorf("got %q, want <r>A</r>: an included map's own entries win over its includes", got)
		}
	})
}
