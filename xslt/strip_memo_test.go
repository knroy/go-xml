package xslt

import (
	"context"
	"strconv"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// stripsElement remembers its answer per package and element name, up to
// stripMemoCap names, and a remembered answer is the one the declarations
// give.
func TestStripsElementMemo(t *testing.T) {
	s := compileString(t, `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:strip-space elements="*"/>
		<xsl:preserve-space elements="pre"/>
		<xsl:template match="/"><xsl:copy-of select="."/></xsl:template>
	</xsl:stylesheet>`)
	doc := parseDoc(t, "<d>\n <a> </a>\n <pre> </pre>\n</d>")
	for rep := 0; rep < 2; rep++ {
		res, err := s.Transform(context.Background(), doc, TransformOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := res.String(), "<d><a/><pre> </pre></d>"; got != want {
			t.Fatalf("pass %d: got %q, want %q", rep, got, want)
		}
	}
	s.stripMu.RLock()
	n := len(s.stripMemo)
	s.stripMu.RUnlock()
	if n != 3 { // d, a and pre
		t.Errorf("stripMemo holds %d names after the transform, want 3", n)
	}
	for i := 0; i < stripMemoCap+100; i++ {
		if !s.stripsElement(0, xdm.QName{Local: "e" + strconv.Itoa(i)}) {
			t.Fatalf("e%d: not stripped", i)
		}
	}
	if s.stripsElement(0, xdm.QName{Local: "pre"}) {
		t.Error("pre: stripped")
	}
	if len(s.stripMemo) != stripMemoCap {
		t.Errorf("stripMemo holds %d names, want the cap of %d", len(s.stripMemo), stripMemoCap)
	}
}
