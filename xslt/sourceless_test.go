package xslt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// compileSheet compiles a stylesheet from source, failing the test if it will
// not build.
func compileSheet(t *testing.T, src string) *xslt.Stylesheet {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	return sheet
}

// TestTransformWithoutSource covers the entry points that need no source
// document. XSLT 2.0 section 2.3 makes the source optional when the transform
// starts at a named template, which is how a stylesheet that generates its own
// content is run.
func TestTransformWithoutSource(t *testing.T) {
	const named = `<xsl:stylesheet version="2.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:output method="text"/>
	  <xsl:template name="main"><xsl:text>ran</xsl:text></xsl:template>
	</xsl:stylesheet>`

	const defaultEntry = `<xsl:stylesheet version="2.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:output method="text"/>
	  <xsl:template name="xsl:initial-template"><xsl:text>ran</xsl:text></xsl:template>
	</xsl:stylesheet>`

	t.Run("named template", func(t *testing.T) {
		sheet := compileSheet(t, named)
		res, err := sheet.Transform(context.Background(), nil,
			xslt.TransformOptions{InitialTemplate: "main"})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		if got := res.String(); got != "ran" {
			t.Errorf("got %q, want %q", got, "ran")
		}
	})

	// A stylesheet declaring xsl:initial-template is asking to be started
	// there when the caller names neither a source nor a template, which is
	// what lets the command line run it with no arguments beyond -xsl.
	t.Run("xsl:initial-template default", func(t *testing.T) {
		sheet := compileSheet(t, defaultEntry)
		res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		if got := res.String(); got != "ran" {
			t.Errorf("got %q, want %q", got, "ran")
		}
	})

	// With no source and no entry point there is nothing to run, and the
	// error has to say which of the three ways out the caller can take.
	t.Run("no entry point", func(t *testing.T) {
		sheet := compileSheet(t, named)
		_, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{})
		if err == nil {
			t.Fatal("expected an error, got none")
		}
		for _, want := range []string{"source document", "initial-template"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})
}
