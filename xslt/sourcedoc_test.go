package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A fragment identifier on xsl:source-document/@href selects within the
// retrieved document rather than being discarded.
//
// 18.1 says "the process of obtaining a document node given a URI is the same
// as for the doc function", and for an XML media type RFC 7303 admits a bare
// name -- an XML Name naming the element with that ID -- as a fragment
// identifier. The resolver strips the fragment before the filesystem sees it,
// because a fragment names a part of a resource and not a different resource
// (XSLT 2.0 section 16.1); applying it is the instruction's job.
//
// Before the fix nothing applied it, so the whole document came back and the
// body ran with the document node as its focus. The suite's docbook-004 is the
// case: it asserts the context item is the section carrying the named xml:id,
// and it failed with "assertion is false:
// /Q{http://docbook.org/ns/docbook}section/@xml:id" against a whole-document
// result.
func TestSourceDocumentAppliesXMLIDFragment(t *testing.T) {
	dir := t.TempDir()
	doc := `<book><section xml:id="one"><title>One</title></section>` +
		`<section xml:id="two"><title>Two</title></section></book>`
	if err := os.WriteFile(filepath.Join(dir, "book.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		href string
		want string
	}{
		// The bare name selects the element carrying that xml:id.
		{"fragment selects the element", "book.xml#two",
			`<section xml:id="two"><title>Two</title></section>`},
		// No fragment is still the document node, which is the behaviour
		// every other source-document case in the suite depends on.
		{"no fragment is the whole document", "book.xml",
			`<book><section xml:id="one"><title>One</title></section>` +
				`<section xml:id="two"><title>Two</title></section></book>`},
		// A well-formed fragment naming no element falls back to the
		// document node: XTRE1160 is for a fragment that is malformed for
		// the media type, not for one that simply matches nothing.
		{"unmatched fragment falls back", "book.xml#missing",
			`<book><section xml:id="one"><title>One</title></section>` +
				`<section xml:id="two"><title>Two</title></section></book>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:transform xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
				<xsl:output omit-xml-declaration="yes"/>
				<xsl:template name="xsl:initial-template">
					<xsl:source-document streamable="no" href="` + tc.href + `">
						<xsl:copy-of select="."/>
					</xsl:source-document>
				</xsl:template>
			</xsl:transform>`
			path := filepath.Join(dir, "s.xsl")
			r, err := NewFileResolver(dir)
			if err != nil {
				t.Fatal(err)
			}
			stree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: path})
			if err != nil {
				t.Fatal(err)
			}
			s, err := Compile(stree.Root, CompileOptions{Resolver: r, BaseURI: path})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			res, err := s.Transform(context.Background(), nil,
				TransformOptions{InitialTemplate: "initial-template",
					InitialTemplateURI: xdm.NSXSL, Documents: r})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got := strings.TrimSpace(res.String()); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
