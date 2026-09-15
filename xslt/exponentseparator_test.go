package xslt

import (
	"context"
	"strings"
	"testing"
)

// TestDecimalFormatExponentSeparator covers xsl:decimal-format's
// exponent-separator attribute, which XSLT 3.0 §8.4.2 lists in the syntax
// summary of the element and the schema-for-stylesheets types xsl:char with a
// default of "e".
//
// The attribute was missing from the element table, so format-number-069a --
// which writes it in a version="2.0" module and is scoped XSLT30+ -- was
// refused outright with XTSE0090. Accepting the name is only half of it: the
// suite asserts the formatted value, so the declared character has to reach
// the picture parser and be the one that introduces the exponent.
func TestDecimalFormatExponentSeparator(t *testing.T) {
	const picture = `format-number(123.456, '0.0000E0')`
	for _, tc := range []struct {
		name string
		decl string
		want string // the formatted number, or "" when an error is wanted
		err  string
	}{
		// format-number-069a itself: with "E" declared as the separator the
		// picture's "E" introduces the exponent and the number formats.
		{name: "declared E", decl: `<xsl:decimal-format exponent-separator="E"/>`,
			want: "1.2346E2"},
		// The default is "e", so the same picture's "E" is an ordinary
		// character between active characters and the picture is invalid.
		// This is what pins that the *declared value* is honoured rather than
		// the attribute merely being tolerated.
		{name: "undeclared keeps e", decl: ``, err: "FODF1310"},
		// A declaration of the default spelling is not a no-op reachable by
		// accident: it has to round-trip through the same table.
		{name: "declared e", decl: `<xsl:decimal-format exponent-separator="e"/>`,
			err: "FODF1310"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:stylesheet version="2.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + tc.decl + `
			  <xsl:template name="main"><out><xsl:value-of select="` +
				picture + `"/></out></xsl:template>
			</xsl:stylesheet>`
			sheet, err := Compile(mustParse(t, src), CompileOptions{})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			res, err := sheet.Transform(context.Background(), nil,
				TransformOptions{InitialTemplate: "main"})
			if tc.err != "" {
				if err == nil {
					t.Fatalf("got %q, want error %s", res.String(), tc.err)
				}
				if !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got error %v, want %s", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got := res.String(); !strings.Contains(got, ">"+tc.want+"<") {
				t.Errorf("got %q, want a result containing %q", got, tc.want)
			}
		})
	}
}

// TestDecimalFormatExponentSeparatorNotInXTSE1300 pins the deliberate
// omission of exponent-separator from the duplicate-symbol check.
//
// XSLT 3.0 §8.4.2 closes the XTSE1300 list at decimal-separator,
// grouping-separator, percent, per-mille, zero-digit, digit and
// pattern-separator. exponent-separator is not among them, so sharing a
// character with one of those is legal and must compile.
func TestDecimalFormatExponentSeparatorNotInXTSE1300(t *testing.T) {
	src := `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:decimal-format exponent-separator="%"/>
	  <xsl:template name="main"><out/></xsl:template>
	</xsl:stylesheet>`
	if _, err := Compile(mustParse(t, src), CompileOptions{}); err != nil {
		t.Fatalf("exponent-separator sharing the percent sign is legal: %v", err)
	}
}
