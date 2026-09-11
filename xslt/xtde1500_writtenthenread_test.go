package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// fixedDocResolver serves one document under one URI, so a stylesheet can
// read back what xsl:result-document claims to have written.
type fixedDocResolver struct{ uri, xml string }

func (r *fixedDocResolver) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	t, err := xdm.ParseString(r.xml, xdm.ParseOptions{})
	if err != nil {
		return nil, err
	}
	t.Root.DocumentURI = r.uri
	return t, nil
}

var _ xpath.DocumentResolver = (*fixedDocResolver)(nil)

// TestXTDE1500WrittenThenRead pins the direction of XTDE1500 that was never
// detected.
//
// XSLT 3.0 (xslt-lcwd30.xml, [ERR XTDE1500]): "It is a dynamic error for a
// stylesheet to write to an external resource and read from the same resource
// during a single transformation, if the same absolute URI is used to access
// the resource in both cases."
//
// The conjunction is symmetric: it says "write ... and read", not "read then
// write". Only read-then-write was caught, so a stylesheet that wrote a
// document and then read it back got whatever the resolver happened to
// return, which the spec's own note calls out as the hazard -- "it is
// implementation-dependent whether the document that is read from the
// resource reflects its state before or after the result tree is written".
func TestXTDE1500WrittenThenRead(t *testing.T) {
	const uri = "file:///w/out.xml"
	src := `<xsl:transform version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <xsl:result-document href="out.xml"><a/></xsl:result-document>
	    <out><xsl:value-of select="doc('` + uri + `')/*/name()"/></out>
	  </xsl:template>
	</xsl:transform>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = sheet.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "main",
		BaseOutputURI:   "file:///w/x.xml",
		Documents:       &fixedDocResolver{uri: uri, xml: `<a/>`},
	})
	if err == nil {
		t.Fatal("reading back a written document was allowed; want XTDE1500")
	}
	if !strings.Contains(err.Error(), "XTDE1500") {
		t.Errorf("got %v, want XTDE1500", err)
	}
}

// TestXTDE1500UnwrittenDocumentStillReadable is the guard: the check must fire
// on the written URI and on nothing else, or every doc() becomes an error.
func TestXTDE1500UnwrittenDocumentStillReadable(t *testing.T) {
	src := `<xsl:transform version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <xsl:result-document href="out.xml"><a/></xsl:result-document>
	    <out><xsl:value-of select="doc('file:///w/other.xml')/*/name()"/></out>
	  </xsl:template>
	</xsl:transform>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "main",
		BaseOutputURI:   "file:///w/x.xml",
		Documents:       &fixedDocResolver{uri: "file:///w/other.xml", xml: `<b/>`},
	})
	if err != nil {
		t.Fatalf("reading an unwritten document was refused: %v", err)
	}
	if got := res.String(); !strings.Contains(got, "b") {
		t.Errorf("got %q, want the document's element name", got)
	}
}
