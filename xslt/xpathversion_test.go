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

// TestXPath20StylesheetRejects30Syntax is the other half of the same rule,
// and the reason the version cannot simply be raised for everyone: a 2.0
// processor is required to reject the 3.0 additions rather than accept them
// quietly, so a stylesheet relying on one must fail here exactly as it would
// on any other conforming processor.
func TestXPath20StylesheetRejects30Syntax(t *testing.T) {
	for _, expr := range []string{
		`for-each(1 to 3, function($x){$x})`,
		`map{'k':'v'}?k`,
		`[1,2,3]?1`,
	} {
		if _, err := runVersioned(t, sheetOf("2.0", expr), nil); err == nil {
			t.Errorf("version=2.0 accepted the 3.0 construct %q", expr)
		}
	}
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
	v31 := xpath.XPath31
	if _, err := runVersioned(t, sheetOf("2.0", `[1]?1`), &v31); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := runVersioned(t, sheetOf("2.0", `[1]?1`), nil); err == nil {
		t.Error("the override leaked into the next compilation")
	}
}
