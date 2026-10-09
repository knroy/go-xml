package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestIndentDefaultsByMethod pins XSLT 3.0 §26's default for an unstated
// indent: "yes in the case of the html and xhtml output methods, no in the
// case of the xml output method" (issue #17). The html cases cover both ways
// the method is chosen: stated on xsl:output, and defaulted from an <html>
// root element after the transform has run.
func TestIndentDefaultsByMethod(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		indented     bool
	}{
		{"html stated", `<xsl:output method="html"/>`, true},
		{"html from root", ``, true},
		{"xhtml stated", `<xsl:output method="xhtml"/>`, true},
		{"html indent=no", `<xsl:output method="html" indent="no"/>`, false},
		{"xml stated", `<xsl:output method="xml"/>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">` +
				tc.output + `<xsl:template name="main"><html><body><p>a</p><p>b</p></body></html></xsl:template>
			</xsl:stylesheet>`
			sh, err := Compile(mustParse(t, src), CompileOptions{})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			res, err := sh.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main"})
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			got := res.String()
			if strings.Contains(got, "\n  <body>") != tc.indented {
				t.Errorf("indented = %v, want %v:\n%s", !tc.indented, tc.indented, got)
			}
		})
	}
}

// TestIndentDefaultIsXSLTOnly pins that the html default stays a stylesheet
// rule. Serialize is shared with XQuery, whose default indent is no for every
// method, so settings built outside a stylesheet must not pick it up.
func TestIndentDefaultIsXSLTOnly(t *testing.T) {
	doc := mustParse(t, `<html><body><p>a</p></body></html>`)
	for _, opts := range []OutputSettings{{}, {Method: "html"}} {
		var sb strings.Builder
		if err := Serialize(&sb, xdm.One(doc), opts, nil); err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		if strings.Contains(sb.String(), "\n") {
			t.Errorf("method %q indented outside a stylesheet:\n%s", opts.Method, sb.String())
		}
	}
}
