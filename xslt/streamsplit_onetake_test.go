package xslt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestStreamMinOfContextBelowRoot: min(.) and max(.) in a rule below the
// document node (streamable-054's shape) stream -- they were refused as
// unable to fall back to the tree -- and give the tree's result. One per
// rule: "min(.), max(.)" is two consuming operands, roaming and
// free-ranging by §19.8.1, so XTSE3430.
func TestStreamMinOfContextBelowRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"),
		[]byte(`<doc><a><v>3.5</v><v>-2</v><w>4</w></a><a><v>10</v><w>-1.5</w></a></doc>`), 0o600); err != nil {
		t.Fatal(err)
	}
	sheet := compileString(t, `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
		`<xsl:mode name="s" streamable="yes"/>`+
		`<xsl:template name="main"><out><xsl:source-document streamable="yes" href="`+fileURIOf(filepath.Join(dir, "doc.xml"))+`">`+
		`<xsl:apply-templates select="." mode="s"/></xsl:source-document></out></xsl:template>`+
		`<xsl:template match="*" mode="s"><xsl:copy><xsl:apply-templates mode="s"/></xsl:copy></xsl:template>`+
		`<xsl:template match="v" mode="s"><m><xsl:value-of select="format-number(min(.), '0.00')"/></m></xsl:template>`+
		`<xsl:template match="w" mode="s"><m><xsl:value-of select="max(.)"/></m></xsl:template>`+
		`</xsl:stylesheet>`)
	run := func(bursts bool) string {
		docs, err := NewFileResolver(dir)
		if err != nil {
			t.Fatal(err)
		}
		ran := false
		noBursts, traceBursts = !bursts, func(string) { ran = true }
		defer func() { noBursts, traceBursts = false, nil }()
		res, err := sheet.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
		if err != nil {
			t.Fatal(err)
		}
		if ran != bursts {
			t.Fatalf("bursts ran %v, want %v", ran, bursts)
		}
		return res.String()
	}
	if tree, streamed := run(false), run(true); streamed != tree {
		t.Errorf("streamed %s, tree %s", streamed, tree)
	}
}
