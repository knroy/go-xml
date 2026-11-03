package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// fixedDocResolver serves one document under one URI, so a stylesheet can
// read back what xsl:result-document claims to have written.
type fixedDocResolver struct{ uri, xml string }

func (r *fixedDocResolver) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	t, err := xdm.ParseString(r.xml, xdm.ParseOptions{})
	if err != nil {
		return nil, err
	}
	t.Root.SetDocumentURI(r.uri)
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

// TestXTDE1500SymlinkedPath: a document read through a FileResolver is
// recorded under its real path, while an href resolves against the base URI
// as written; XTDE1500 compares the two by the file they name, in both
// directions. t.TempDir is under the symlinked /var on macOS; elsewhere the
// directory is reached through a link made here.
func TestXTDE1500SymlinkedPath(t *testing.T) {
	tmp := t.TempDir()
	data := filepath.Join(tmp, "data")
	if err := os.Mkdir(data, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"in.xml", "out.xml"} {
		if err := os.WriteFile(filepath.Join(data, f), []byte("<a/>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dirs := map[string]string{"temp": data}
	alias := filepath.Join(tmp, "alias")
	if err := os.Symlink(data, alias); err == nil {
		dirs["link"] = alias
	} else {
		t.Logf("no symbolic link (%v): the link case is skipped", err)
	}
	bodies := map[string]string{
		"read then write": `<xsl:value-of select="doc('in.xml')/*/name()"/><xsl:result-document href="in.xml"><b/></xsl:result-document>`,
		"write then read": `<xsl:result-document href="out.xml"><b/></xsl:result-document><xsl:value-of select="doc('out.xml')/*/name()"/>`,
	}
	for name, dir := range dirs {
		base := fileURIOf(filepath.Join(dir, "s.xsl"))
		for what, body := range bodies {
			src := `<xsl:transform version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
				`<xsl:template name="main"><out>` + body + `</out></xsl:template></xsl:transform>`
			tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: base})
			if err != nil {
				t.Fatal(err)
			}
			sheet, err := Compile(tree.Root, CompileOptions{})
			if err != nil {
				t.Fatalf("%s, %s: compile: %v", name, what, err)
			}
			res, err := NewFileResolver(tmp)
			if err != nil {
				t.Fatal(err)
			}
			_, err = sheet.Transform(context.Background(), nil, TransformOptions{
				InitialTemplate: "main", BaseOutputURI: base, Documents: res})
			if err == nil || !strings.Contains(err.Error(), "XTDE1500") {
				t.Errorf("%s, %s: got %v, want XTDE1500", name, what, err)
			}
		}
	}
}
