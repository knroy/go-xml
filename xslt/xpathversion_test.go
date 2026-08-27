package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// runVersioned compiles and runs a stylesheet against a trivial source,
// optionally pinning the XPath version.
func runVersioned(t *testing.T, sheet string, pin *xpath.Version) (string, error) {
	t.Helper()
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		return "", err
	}
	s, err := Compile(stree.Root, CompileOptions{XPathVersion: pin})
	if err != nil {
		return "", err
	}
	dtree, err := xdm.ParseString(`<doc/>`, xdm.ParseOptions{})
	if err != nil {
		return "", err
	}
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		return "", err
	}
	out := res.String()
	if i := strings.Index(out, "?>"); i >= 0 {
		out = out[i+2:]
	}
	return strings.TrimSpace(out), nil
}

func sheetOf(version, expr string) string {
	return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="` +
		version + `"><xsl:template match="/"><out><xsl:value-of select="` +
		expr + `"/></out></xsl:template></xsl:stylesheet>`
}

// TestXPathVersionFollowsStylesheet is the rule of XSLT 3.0 section 2.2: a
// stylesheet declaring version 3.0 is written in XPath 3.1, so maps, arrays,
// inline functions and the lookup operator are available to it. Before the
// version was threaded through, every stylesheet compiled as XPath 2.0
// whatever it declared, and each of these was a syntax error.
func TestXPathVersionFollowsStylesheet(t *testing.T) {
	cases := []struct{ name, expr, want string }{
		{"inline function", `string-join(for-each(1 to 3, function($x){$x*2}), ',')`, "2,4,6"},
		{"map constructor", `map{'k':'v'}?k`, "v"},
		{"array constructor", `[1,2,3]?2`, "2"},
		{"array function", `array:size([1,2,3])`, "3"},
		{"map function", `map:size(map{1:'a',2:'b'})`, "2"},
		{"let expression", `let $x := 6 return $x * 7`, "42"},
		{"string concat", `'a' || 'b'`, "ab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := runVersioned(t, sheetOf("3.0", c.expr), nil)
			if err != nil {
				t.Fatalf("version=3.0 rejected %s: %v", c.expr, err)
			}
			if want := "<out>" + c.want + "</out>"; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// TestXPath20StylesheetRejects30Syntax pins which processor refuses the 3.0
// additions, which is not the same question as which module declares them.
//
// This test previously required a version="2.0" module to be refused them by
// a 3.0 processor. That was an assumption, not a rule. XSLT 3.0 defines only
// XPath *1.0* compatibility mode -- there is no XPath 2.0 mode -- and section
// 3.10.2 makes it implementation-defined whether backwards compatible
// behaviour is supported for an earlier XSLT version at all. The conformance
// suite settles it in the other direction: it runs version="2.0" modules
// scoped XSLT30+ that write maps, arrays and inline functions and expects
// them to work.
//
// What a 2.0 PROCESSOR must refuse is unchanged, and that is what is tested
// here. MaxVersion is how a host says which processor it is.
func TestXPath20StylesheetRejects30Syntax(t *testing.T) {
	for _, expr := range []string{
		`for-each(1 to 3, function($x){$x})`,
		`map{'k':'v'}?k`,
		`[1,2,3]?1`,
	} {
		if _, err := runAsProcessor(t, sheetOf("2.0", expr), 2.0); err == nil {
			t.Errorf("an XSLT 2.0 processor accepted the 3.0 construct %q", expr)
		}
		// The same module under a 3.0 processor is accepted: the grammar
		// follows the processor, as the regex dialect, the function library
		// and named function references already do.
		if _, err := runAsProcessor(t, sheetOf("2.0", expr), 0); err != nil {
			t.Errorf("an XSLT 3.0 processor refused %q in a 2.0 module: %v",
				expr, err)
		}
	}
}

// runAsProcessor compiles and runs a stylesheet as a processor of the given
// version. Zero means uncapped, and so 3.0.
func runAsProcessor(t *testing.T, sheet string, maxVersion float64) (string, error) {
	t.Helper()
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		return "", err
	}
	s, err := Compile(stree.Root, CompileOptions{MaxVersion: maxVersion})
	if err != nil {
		return "", err
	}
	dtree, err := xdm.ParseString(`<doc/>`, xdm.ParseOptions{})
	if err != nil {
		return "", err
	}
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.String()), nil
}

// TestXPathVersionOverride pins the version against what the stylesheet
// declares, in both directions.
func TestXPathVersionOverride(t *testing.T) {
	v31, v20 := xpath.XPath31, xpath.XPath20

	// A 2.0 stylesheet raised to 3.1 by the host.
	got, err := runVersioned(t, sheetOf("2.0", `[1,2,3]?3`), &v31)
	if err != nil {
		t.Fatalf("raising a 2.0 stylesheet to 3.1: %v", err)
	}
	if want := "<out>3</out>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// A 3.0 stylesheet held down to 2.0 by the host, which is the case a
	// caller running an untrusted stylesheet under a smaller surface wants.
	if _, err := runVersioned(t, sheetOf("3.0", `[1,2,3]?3`), &v20); err == nil {
		t.Error("pinning XPath20 still accepted an array constructor")
	}
}

// TestXPathVersionOverrideDoesNotLeak pins the package-state discipline: the
// override is cleared when Compile returns, so the next compilation reads the
// stylesheet's own version again.
func TestXPathVersionOverrideDoesNotLeak(t *testing.T) {
	// Pinned DOWN rather than up, because a raised version is now the
	// default under a 3.0 processor and so could not tell a leak from
	// ordinary behaviour. Holding a 3.0 module down to XPath 2.0 is a state
	// nothing else produces, which makes its absence afterwards conclusive.
	v20 := xpath.XPath20
	if _, err := runVersioned(t, sheetOf("3.0", `[1]?1`), &v20); err == nil {
		t.Fatal("setup: pinning XPath20 accepted an array constructor")
	}
	if _, err := runVersioned(t, sheetOf("3.0", `[1]?1`), nil); err != nil {
		t.Errorf("the override leaked into the next compilation: %v", err)
	}
}
