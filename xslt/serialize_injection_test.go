package xslt

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// runSecondary compiles src, transforms doc through it and returns the one
// secondary document the stylesheet writes. Both defects below are reached
// through xsl:result-document, whose serialization attributes are attribute
// value templates: the SOURCE DOCUMENT supplies the parameter value, which is
// what makes them reachable from untrusted input rather than only from a
// stylesheet the caller wrote.
func runSecondary(t *testing.T, src, doc string) (string, error) {
	t.Helper()
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	source, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	res, err := sheet.Transform(context.Background(), source.Root,
		TransformOptions{})
	if err != nil {
		return "", err
	}
	if len(res.Secondary) != 1 {
		t.Fatalf("got %d secondary results, want 1", len(res.Secondary))
	}
	// Serialize rather than String: the serialization error is what both
	// defects are about, and String discards it. This is the call the CLI
	// makes when it writes a result document to disk.
	var out bytes.Buffer
	sec := res.Secondary[0]
	if err := sec.Serialize(&out, nil); err != nil {
		return "", err
	}
	return out.String(), nil
}

// mediaTypeSheet drives media-type from the source document, which is the
// shape the defect was reproduced in. The method is a parameter because the
// html method writes <meta> unclosed -- correct for HTML, unparseable by an
// XML parser -- so the re-parse assertion uses xhtml, which reaches the same
// injected-element code.
func mediaTypeSheet(method string) string {
	return `<xsl:stylesheet version="3.0"
    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template match="/">
    <xsl:result-document href="report.html" method="` + method + `"
        omit-xml-declaration="yes" media-type="{/doc/mt}">
      <html xmlns="http://www.w3.org/1999/xhtml"><head><title>Report</title></head><body>hi</body></html>
    </xsl:result-document>
  </xsl:template>
</xsl:stylesheet>`
}

// TestMetaContentTypeEscapesMediaType pins the escaping of media-type where
// the html and xhtml methods write it, which is the content attribute of the
// meta element they inject.
//
// Every other attribute the serialiser writes goes through escapeAttrRunes;
// this one was concatenated raw, so a media-type holding `">` closed the
// attribute and the tag and everything after it became live markup inside
// <head>. The route is not contrived: media-type is an attribute value
// template on xsl:result-document, and DocBook XSL 1.79.1 -- vendored in this
// repository's own testdata -- writes media-type="{$media-type}" from a
// caller-settable parameter in xhtml/chunker.xsl.
func TestMetaContentTypeEscapesMediaType(t *testing.T) {
	const payload = `text/html"><script>alert(document.domain)</script><meta x="`
	doc := `<doc><mt>` +
		strings.NewReplacer("<", "&lt;", ">", "&gt;", `"`, "&quot;").
			Replace(payload) + `</mt></doc>`
	got, err := runSecondary(t, mediaTypeSheet("html"), doc)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	// A media type containing "<" or `"` is legal, so it must be escaped
	// rather than refused: no script element may appear in the output.
	if strings.Contains(got, "<script>") {
		t.Errorf("media-type escaped its attribute: %q", got)
	}
	// Re-parsing is the assertion that matters. If the value stayed inside
	// the attribute there is exactly one meta element, and its content
	// attribute holds the payload literally. The xhtml method closes the
	// element, so its output is XML an XML parser can read back; the meta
	// element and its escaping are written by the same code.
	xhtml, err := runSecondary(t, mediaTypeSheet("xhtml"), doc)
	if err != nil {
		t.Fatalf("transform xhtml: %v", err)
	}
	head, err := xdm.ParseString(xhtml, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("output does not parse: %v\n%s", err, xhtml)
	}
	metas := collectElements(head.Root, "meta")
	if len(metas) != 1 {
		t.Fatalf("got %d meta elements, want 1: %q", len(metas), xhtml)
	}
	if len(collectElements(head.Root, "script")) != 0 {
		t.Fatalf("output contains a script element: %q", xhtml)
	}
	want := payload + "; charset=UTF-8"
	if v := attrValue(metas[0], "content"); v != want {
		t.Errorf("meta content = %q, want %q", v, want)
	}
}

// TestMetaContentTypeLeavesLegitimateMediaTypeAlone is the control: a media
// type with nothing to escape serialises byte for byte as it did before.
func TestMetaContentTypeLeavesLegitimateMediaTypeAlone(t *testing.T) {
	got, err := runSecondary(t, mediaTypeSheet("html"),
		`<doc><mt>text/html</mt></doc>`)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	const want = `<meta http-equiv="Content-Type" content="text/html; charset=UTF-8">`
	if !strings.Contains(got, want) {
		t.Errorf("got %q, want it to contain %q", got, want)
	}
}

// collectElements returns every element with the given local name, in
// document order.
func collectElements(n *xdm.Node, local string) []*xdm.Node {
	var out []*xdm.Node
	if n.Kind == xdm.KindElement && n.Name.Local == local {
		out = append(out, n)
	}
	for _, c := range n.Children {
		out = append(out, collectElements(c, local)...)
	}
	return out
}

// attrValue returns the value of an element's unprefixed attribute.
func attrValue(n *xdm.Node, local string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == local && a.Name.URI == "" {
			return a.Value
		}
	}
	return ""
}
