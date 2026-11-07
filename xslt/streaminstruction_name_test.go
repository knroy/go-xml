package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestStreamIsNotAnInstruction pins that xsl:stream, the last-call working
// draft's name for §18.1's instruction, is refused under version 3.0. The
// Recommendation renamed it (testdata/xslt30-test/specs/xslt-30.html, change
// log, Bug29747): "The xsl:stream instruction has been generalized to handle
// both streamed and unstreamed processing, and it has accordingly been
// renamed xsl:source-document". §3.4 (XTSE0010) then refuses an element in
// the XSLT namespace that the specification does not define, outside
// forwards-compatible mode. No suite case writes <xsl:stream.
func TestStreamIsNotAnInstruction(t *testing.T) {
	sheet := func(version, name, body string) string {
		return `<xsl:stylesheet version="` + version + `" ` +
			`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
			`<xsl:template name="main"><out>` +
			`<xsl:` + name + ` href="in.xml">` + body + `</xsl:` + name + `>` +
			`</out></xsl:template></xsl:stylesheet>`
	}
	compile := func(t *testing.T, src string) (*Stylesheet, error) {
		t.Helper()
		doc, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		return Compile(doc.Root, CompileOptions{})
	}

	t.Run("source-document compiles", func(t *testing.T) {
		if _, err := compile(t, sheet("3.0", "source-document", "<x/>")); err != nil {
			t.Errorf("xsl:source-document was refused: %v", err)
		}
	})
	t.Run("stream is XTSE0010 under 3.0", func(t *testing.T) {
		_, err := compile(t, sheet("3.0", "stream", "<x/>"))
		if err == nil || !strings.Contains(err.Error(), "XTSE0010") {
			t.Errorf("xsl:stream: got %v, want XTSE0010", err)
		}
	})
	t.Run("stream takes its fallback in forwards-compatible mode", func(t *testing.T) {
		s, err := compile(t, sheet("4.0", "stream", "<xsl:fallback>fb</xsl:fallback>"))
		if err != nil {
			t.Fatalf("forwards-compatible xsl:stream was refused: %v", err)
		}
		res, err := s.Transform(context.Background(), nil,
			TransformOptions{InitialTemplate: "main"})
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if out := res.String(); !strings.Contains(out, "<out>fb</out>") {
			t.Errorf("got %q; want the fallback's output", out)
		}
	})
}
