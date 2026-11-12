package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xsdstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xsd"
)

// TestStreamAsReadMarked: a document the harness preloads as validated and
// marks (xsdstream.MarkSource), read by an xsl:source-document with the
// default validation, streams (it declined as "already parsed"), and the
// stream sees the harness's typing, as the tree does.
func TestStreamAsReadMarked(t *testing.T) {
	// The resolver keys its cache by the directory with symbolic links
	// resolved (macOS's /var is /private/var): the harness's tree carries
	// that path's URI, as a parse through the resolver gives it.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "doc.xml")
	if err := os.WriteFile(path, []byte(`<doc><v>3</v><v>10</v></doc>`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := xdm.ParseString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`+
		`<xs:element name="doc"><xs:complexType><xs:sequence>`+
		`<xs:element name="v" type="xs:integer" maxOccurs="unbounded"/>`+
		`</xs:sequence></xs:complexType></xs:element></xs:schema>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := xsd.Load(st.Root, "s.xsd", xsd.Options{})
	if err != nil {
		t.Fatal(err)
	}
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
		`<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml"><out>` +
		`<xsl:for-each select="doc/v"><t><xsl:value-of select="data(.) instance of xs:integer"/></t></xsl:for-each>` +
		`</out></xsl:source-document></xsl:template></xsl:stylesheet>`
	// The stylesheet sits in dir, so that href="doc.xml" is the preloaded
	// document on every OS (not one beside the test's working directory).
	st2, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := Compile(st2.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	run := func(bursts bool) string {
		docs, err := NewFileResolver(dir)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := xdm.ParseString(`<doc><v>3</v><v>10</v></doc>`, xdm.ParseOptions{BaseURI: fileURIOf(path), DocumentURI: fileURIOf(path)})
		if err != nil {
			t.Fatal(err)
		}
		typed, _ := schema.ValidateCopy(parsed.Root, xsd.ValidateOptions{}) // as the harness's annotate
		docs.Preload(fileURIOf(path), typed.Tree())
		xsdstream.MarkSource(docs, fileURIOf(path), schema)
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
	tree, streamed := run(false), run(true)
	if want := `<t>true</t><t>true</t></out>`; !strings.Contains(tree, want) { // <out> carries xmlns:xs
		t.Fatalf("tree: %s, want %s", tree, want)
	}
	if streamed != tree {
		t.Errorf("streamed %s, tree %s", streamed, tree)
	}
}
