package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A static expression may call fn:doc, and the empty reference denotes the
// stylesheet module itself.
//
// Section 9.7's dynamic-context table leaves "Available documents"
// implementation-defined for a static expression, so the URI space is this
// processor's to decide; the constraints the section does impose are on what
// the stylesheet may be asked about, not on whether documents resolve at all.
// The suite's package-version-011 is the case that depends on it: it derives
// the package version from the module's own @version, through an empty
// document reference in a shadow attribute, and Saxon 9.8 passes it.
//
// Before the fix the static phase built a context with no document resolver,
// so this failed with "FODC0002: document access is disabled (no resolver
// configured)".
func TestStaticExpressionCanReadItsOwnModule(t *testing.T) {
	dir := t.TempDir()
	src := `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		name="urn:simple"
		_package-version="{doc('')/xsl:package/@version}"
		version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:template name="xsl:initial-template" visibility="public"><res>Success</res></xsl:template>
	</xsl:package>`
	path := filepath.Join(dir, "pkg.xsl")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
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
			InitialTemplateURI: xdm.NSXSL})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := res.String(); !strings.Contains(got, "<res>Success</res>") {
		t.Errorf("got %q, want the package to compile and run", got)
	}
}

// An XSLT 2.0 processor resolves no documents in a static expression however
// it is configured.
//
// XSLT 2.0's 3.13 table fixes "Available documents" at None, where 3.0's 9.7
// makes it implementation-defined. The suite's use-when-0406 is the case, and
// its modification note states the difference outright: "Marked test as
// 2.0-only: in 3.0, use-when expressions can access documents". Granting 3.0
// the access ungated cost that case, 6149 -> 6148 on the 2.0 target.
func TestStaticExpressionHasNoDocumentsAtXSLT20(t *testing.T) {
	dir := t.TempDir()
	// doc-available('') is the empty reference, which names the stylesheet
	// module itself -- a document that certainly exists and that a resolver
	// rooted at its directory certainly reaches. That is exactly what
	// use-when-0406 asks, and at the 2.0 target the answer must still be
	// false.
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		version="2.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:template match="/"><out><xsl:value-of select="'x'" use-when="doc-available('')"/></out></xsl:template>
	</xsl:stylesheet>`
	path := filepath.Join(dir, "s.xsl")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	stree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: path})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(stree.Root, CompileOptions{
		Resolver: r, BaseURI: path, MaxVersion: 2.0})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	dtree, _ := xdm.ParseString(`<d/>`, xdm.ParseOptions{})
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := res.String(); got != "<out/>" {
		t.Errorf("got %q, want <out/>: fn:doc-available must be false in a "+
			"static expression at the 2.0 target", got)
	}
}

// A host that supplied no resolver gets no document access in a static
// expression either. The static phase is not a way around the decision a nil
// Resolver states, which is that the stylesheet may not reach the filesystem.
func TestStaticExpressionWithoutResolverHasNoDocuments(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		version="3.0">
		<xsl:variable name="v" static="yes" select="doc('other.xml')/*"/>
		<xsl:template match="/"><r/></xsl:template>
	</xsl:stylesheet>`
	stree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: "file:///tmp/s.xsl"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(stree.Root, CompileOptions{BaseURI: "file:///tmp/s.xsl"})
	if err == nil || !strings.Contains(err.Error(), "FODC0002") {
		t.Errorf("err = %v, want FODC0002 with no resolver configured", err)
	}
}
