package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// dfSheet is a stylesheet with the given top-level declarations whose output
// is [format-number(NaN)][format-number(-INF)] in the decimal format "f".
func dfSheet(decls string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		decls + `<xsl:output method="text"/><xsl:template match="/">` +
		`[<xsl:value-of select="format-number(number('x'), '0', 'f')"/>]` +
		`[<xsl:value-of select="format-number(-1 div 0e0, 'a0b', 'f')"/>]` +
		`</xsl:template></xsl:stylesheet>`
}

// TestDecimalFormatEmptyValuesArePresent: an xsl:decimal-format attribute is
// stated by being present. NaN and infinity are xs:string, so NaN="" formats
// NaN as nothing; they were read as absent and gave "NaN" and "Infinity". The
// single-character attributes make "" an XTSE0020, as Saxon reports; they too
// were read as absent and the declaration was silently accepted.
func TestDecimalFormatEmptyValuesArePresent(t *testing.T) {
	if got := run(t, dfSheet(`<xsl:decimal-format name="f" NaN="" infinity=""/>`), `<r/>`); got != "[][-ab]" {
		t.Errorf(`NaN="" infinity="": got %q, want "[][-ab]"`, got)
	}

	for _, attr := range []string{"decimal-separator", "grouping-separator",
		"percent", "per-mille", "zero-digit", "digit", "pattern-separator",
		"minus-sign", "exponent-separator"} {
		_, err := runErr(t, dfSheet(`<xsl:decimal-format name="f" `+attr+`=""/>`), `<r/>`)
		if err == nil || !strings.Contains(err.Error(), "XTSE0020") {
			t.Errorf(`%s="": want XTSE0020, got %v`, attr, err)
		}
	}

	// §16.4.1 at one precedence: an empty value is a value, so it conflicts
	// with a different one and combines with a declaration that leaves the
	// attribute out.
	_, err := runErr(t, dfSheet(`<xsl:decimal-format name="f" NaN=""/>`+
		`<xsl:decimal-format name="f" NaN="x"/>`), `<r/>`)
	if err == nil || !strings.Contains(err.Error(), "XTSE1290") {
		t.Errorf(`NaN="" beside NaN="x": want XTSE1290, got %v`, err)
	}
	if got := run(t, dfSheet(`<xsl:decimal-format name="f" NaN=""/>`+
		`<xsl:decimal-format name="f" minus-sign="~"/>`), `<r/>`); got != "[][~aInfinityb]" {
		t.Errorf(`NaN="" with minus-sign="~": got %q, want "[][~aInfinityb]"`, got)
	}

	// Across precedences the higher one wins attribute by attribute, empty
	// or not, and the lower one still supplies what the higher leaves out.
	for _, c := range []struct{ main, imported, want string }{
		{`NaN=""`, `NaN="x" infinity="i"`, "[][-aib]"},
		{`infinity="i"`, `NaN=""`, "[][-aib]"},
	} {
		mods := memModules{"b.xsl": `<xsl:stylesheet version="3.0" ` +
			`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
			`<xsl:decimal-format name="f" ` + c.imported + `/></xsl:stylesheet>`}
		sheet := dfSheet(`<xsl:import href="b.xsl"/><xsl:decimal-format name="f" ` + c.main + `/>`)
		tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(tree.Root, CompileOptions{Resolver: mods})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		src, _ := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
		res, err := s.Transform(context.Background(), src.Root, TransformOptions{})
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if got := res.String(); got != c.want {
			t.Errorf("main %s over imported %s: got %q, want %q", c.main, c.imported, got, c.want)
		}
	}
}
