package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestStreamAndSourceDocumentAreOneInstruction pins that xsl:stream is
// accepted beside xsl:source-document, and that each carries its own
// attribute list.
//
// The vendored working draft (testdata/xslt30-test/specs/xslt-lcwd30.xml,
// §18.1) names the instruction xsl:stream and mentions it 160 times;
// "source-document" appears there not at all. The W3C suite is the mirror
// image -- 500 stylesheets write xsl:source-document and none writes
// xsl:stream -- because the rename happened between the two texts. The
// element table carried only the suite's spelling, so a stylesheet written
// against the specification this repository vendors was rejected outright
// with XTSE0010.
//
// §18.1's signature is href, use-accumulators, validation and type. It has no
// @streamable: xsl:stream streams by definition, where xsl:source-document
// takes the attribute to say whether it does. That asymmetry is the reason
// the two need separate table entries rather than an alias.
func TestStreamAndSourceDocumentAreOneInstruction(t *testing.T) {
	const head = `<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`

	// Both spellings compile. The href is never dereferenced -- no resolver is
	// configured -- so the transform fails at run time with FODC0002, which is
	// itself the proof that compilation reached the instruction rather than
	// rejecting the element.
	for _, name := range []string{"source-document", "stream"} {
		t.Run(name+" compiles", func(t *testing.T) {
			src := head + `<xsl:template name="main"><out>` +
				`<xsl:` + name + ` href="in.xml"><x/></xsl:` + name + `>` +
				`</out></xsl:template></xsl:stylesheet>`
			doc, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			sheet, err := Compile(doc.Root, CompileOptions{})
			if err != nil {
				t.Fatalf("xsl:%s was refused: %v", name, err)
			}
			_, err = sheet.Transform(context.Background(), nil,
				TransformOptions{InitialTemplate: "main"})
			if err == nil {
				t.Fatalf("xsl:%s: expected FODC0002 with no resolver", name)
			}
			if !strings.Contains(err.Error(), "FODC0002") {
				t.Errorf("xsl:%s: got %v, want FODC0002", name, err)
			}
		})
	}

	// @streamable belongs to xsl:source-document alone.
	t.Run("streamable is source-document's alone", func(t *testing.T) {
		ok := head + `<xsl:template name="main"><out>` +
			`<xsl:source-document href="in.xml" streamable="yes"><x/></xsl:source-document>` +
			`</out></xsl:template></xsl:stylesheet>`
		doc, err := xdm.ParseString(ok, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := Compile(doc.Root, CompileOptions{}); err != nil {
			t.Errorf("source-document/@streamable was refused: %v", err)
		}

		bad := head + `<xsl:template name="main"><out>` +
			`<xsl:stream href="in.xml" streamable="yes"><x/></xsl:stream>` +
			`</out></xsl:template></xsl:stylesheet>`
		doc, err = xdm.ParseString(bad, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		_, err = Compile(doc.Root, CompileOptions{})
		if err == nil {
			t.Fatal("xsl:stream accepted @streamable, which §18.1 does not give it")
		}
		if !strings.Contains(err.Error(), "XTSE0090") {
			t.Errorf("got %v, want XTSE0090", err)
		}
	})
}
