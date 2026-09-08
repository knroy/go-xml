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

// xsl:source-document/@href resolves against the base URI of the instruction
// element, which an xml:base on it or an ancestor moves.
//
// 18.1 obtains the document "the same as for the doc function", and fn:doc
// resolves a relative reference against the static base URI of the expression.
// That is a per-element property: xml:base on the xsl:template (or on any
// ancestor of the instruction) changes it. The engine used the stylesheet
// module's own base instead, so an href beside a relocated base was looked for
// beside the module, and the suite's non-stream-004 and stream-004 -- whose
// template carries xml:base="../../.." and asks for catalog.xml -- failed with
// FODC0002 naming a path under the stylesheet's own directory.
func TestSourceDocumentResolvesAgainstXMLBase(t *testing.T) {
	// A stylesheet in <root>/sub/deep, and the document one level up in
	// <root>/sub, reached by an xml:base of "..". Built with filepath.Join so
	// the layout is native on every platform.
	root := t.TempDir()
	deep := filepath.Join(root, "sub", "deep")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "sub", "catalog.xml")
	if err := os.WriteFile(target, []byte(`<catalog n="up"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A decoy of the same name beside the stylesheet. Without the fix the
	// engine finds this one, so the test would pass for the wrong reason if
	// the resolution were merely "some catalog.xml".
	decoy := filepath.Join(deep, "catalog.xml")
	if err := os.WriteFile(decoy, []byte(`<catalog n="beside"/>`), 0o600); err != nil {
		t.Fatal(err)
	}

	src := `<xsl:transform xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:template name="xsl:initial-template" xml:base="..">
			<xsl:source-document streamable="no" href="catalog.xml">
				<xsl:copy-of select="."/>
			</xsl:source-document>
		</xsl:template>
	</xsl:transform>`
	// A file: URI, not a bare path: xml:base is resolved as a URI reference,
	// and filepath.ToSlash keeps that correct where the separator is "\\".
	path := "file://" + filepath.ToSlash(filepath.Join(deep, "s.xsl"))
	r, err := NewFileResolver(root)
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
	if got, want := strings.TrimSpace(res.String()), `<catalog n="up"/>`; got != want {
		t.Errorf("xml:base was not applied: got %q, want %q", got, want)
	}
}

// An @href that is not a URI at all is FODC0005, not FODC0002.
//
// F&O separates the two: FODC0002 is a resource that could not be retrieved,
// FODC0005 an argument that is not a valid URI. The distinction is whether
// retrieval was ever attempted, so the check runs before the resolver is
// consulted -- and therefore does not depend on one being configured.
//
// The suite's non-stream-006 and stream-006 ask for "c:\my\doc\books.xml": a
// native Windows filename. Its backslashes are not legal URI characters, so it
// is not a URI reference on any platform, and the leading "c:" must not be
// read as a URI scheme -- which is what produced the wrong FODC0002 ("scheme
// \"c\" is not permitted"). The assertion is on the code, because an error of
// either kind would otherwise look alike.
func TestSourceDocumentInvalidURIIsFODC0005(t *testing.T) {
	for _, tc := range []struct {
		name string
		href string
		want string
	}{
		// A Windows drive-letter path. The case that matters: it must be
		// rejected as a non-URI rather than dispatched to a "c" scheme, and
		// it must behave the same on Windows, macOS and Linux, since a
		// filesystem path is not a URI reference anywhere.
		{"windows drive letter path", `c:\my\doc\books.xml`, "FODC0005"},
		// A bare backslash is enough; the drive letter is not what makes it
		// invalid.
		{"backslash separator", `my\doc.xml`, "FODC0005"},
		// A truncated percent-escape is the other non-URI form.
		{"bad percent escape", `books%zz.xml`, "FODC0005"},
		// A well-formed URI for a document that is not there stays
		// FODC0002: it is a retrieval failure, not a malformed argument.
		// This is what keeps the new check from swallowing the old error.
		{"absent document is still FODC0002", `absent.xml`, "FODC0002"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := `<xsl:transform xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
				<xsl:template name="xsl:initial-template">
					<xsl:source-document streamable="no" href="` + tc.href + `">
						<in/>
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
			_, err = s.Transform(context.Background(), nil,
				TransformOptions{InitialTemplate: "initial-template",
					InitialTemplateURI: xdm.NSXSL, Documents: r})
			if err == nil {
				t.Fatalf("href %q: expected %s, got no error", tc.href, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("href %q: expected %s, got: %v", tc.href, tc.want, err)
			}
		})
	}
}
