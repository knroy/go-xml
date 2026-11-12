package xslt

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestCrossModulesWalkedOnce: crossModules, which walks every element of
// every module, is asked once per module rule plan; it must walk them once
// per module set (A3-1: DocBook compile +37% time, +102% allocs).
func TestCrossModulesWalkedOnce(t *testing.T) {
	parse := func(s string) *xdm.Node {
		t.Helper()
		tr, err := xdm.ParseString(s, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return tr.Root
	}
	main := parse(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:import href="m.xsl"/><xsl:mode name="a" streamable="yes"/><xsl:mode name="b" streamable="no"/>
  <xsl:template match="x"><y><z/></y></xsl:template></xsl:stylesheet>`)
	mod := parse(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:mode name="a" streamable="no"/><xsl:mode name="b" streamable="yes"/>
  <xsl:mode name="c" streamable="yes"/><xsl:mode name="d" streamable="yes"/></xsl:stylesheet>`)
	mod2 := parse(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:mode name="d" streamable="no"/></xsl:stylesheet>`)
	c := &compiler{sheet: &Stylesheet{source: main}, staticDone: map[*xdm.Node]*xdm.Node{mod: mod}}
	xm := c.crossModules()
	if len(xm) != 2 {
		t.Fatalf("crossModules = %d modules, want 2", len(xm))
	}
	if n := testing.AllocsPerRun(10, func() { c.crossModules() }); n != 0 {
		t.Fatalf("crossModules repeated over the same modules: %v allocs, want 0 (not memoized)", n)
	}
	c.staticDone[mod2] = mod2
	if xm := c.crossModules(); len(xm) != 3 {
		t.Fatalf("crossModules after a module was added = %d modules, want 3", len(xm))
	}

	// walkElements visits without allocating.
	if n := testing.AllocsPerRun(10, func() { walkElements(firstElement(main), func(*xdm.Node) bool { return true }) }); n != 0 {
		t.Fatalf("walkElements: %v allocs, want 0", n)
	}

	// One walk answers every mode as the per-mode walks did: the principal's
	// declaration wins, otherwise the others must agree.
	tr := &declTrust{xmods: c.crossModules()}
	for _, w := range []struct {
		m          string
		yes, known bool
	}{{"a", true, true}, {"b", false, true}, {"c", true, true}, {"d", false, false}, {"e", false, false}} {
		if yes, known := tr.crossModeStreamable(w.m); yes != w.yes || known != w.known {
			t.Errorf("crossModeStreamable(%s) = %v, %v; want %v, %v", w.m, yes, known, w.yes, w.known)
		}
	}
	if got := tr.crossStreamableModes(); len(got) != 2 || !got["a"] || !got["c"] {
		t.Errorf("crossStreamableModes = %v, want a and c", got)
	}
}
