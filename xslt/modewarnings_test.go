package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A transform that warns once per node delivers maxWarnings warnings and one
// summary, not one per node, so a large input cannot grow Result.Warnings
// without bound.
func TestModeWarningsBounded(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:mode on-no-match="shallow-skip" warning-on-no-match="yes"/>
</xsl:stylesheet>`
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := xdm.ParseString("<r>"+strings.Repeat("<a/>", 5*maxWarnings)+"</r>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Transform(context.Background(), d.Root, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(res.Warnings); n != maxWarnings+1 ||
		!strings.Contains(res.Warnings[maxWarnings], "suppressed after 100") ||
		!strings.Contains(res.Warnings[0], "no template rule matches") {
		t.Fatalf("%d warnings, first %q, last %q", n, res.Warnings[0], res.Warnings[n-1])
	}
}
