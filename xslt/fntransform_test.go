package xslt_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// fn:transform runs a transformation named by an options map. It is bound per
// transform by the xslt layer, because xpath cannot depend on xslt and so
// registers a stub that declines with FOXT0004.
func TestFnTransform(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("env.xsl", `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:variable name="env" select="'ENV'"/>
	</xsl:stylesheet>`)
	write(filepath.Join("sub", "inner.xsl"), `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:import href="../env.xsl"/>
	  <xsl:param name="greeting" select="'hi'"/>
	  <xsl:template match="/"><w><xsl:value-of select="$greeting"/>:<xsl:value-of select="$env"/>:<xsl:value-of select="//v"/></w></xsl:template>
	</xsl:stylesheet>`)

	// xsl:strip-space matters: Transform wraps the caller's resolver in a
	// stripSpaceResolver only when the stylesheet declares one, and that
	// wrapper hid the ModuleResolver from the nested compile. Without this
	// declaration the test passes even with that bug present.
	outer := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:strip-space elements="ignored"/>
	  <xsl:template match="/">
	    <o><xsl:sequence select="transform(map{
	      'stylesheet-location': 'sub/inner.xsl',
	      'source-node': /,
	      'stylesheet-params': map{QName('','greeting'): 'HELLO'}
	    })?output"/></o>
	  </xsl:template>
	</xsl:stylesheet>`

	base := "file://" + dir + "/outer.xsl"
	tree, err := xdm.ParseString(outer, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{BaseURI: base})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	doc, err := xdm.ParseString(`<r><v>42</v></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := sheet.Transform(context.Background(), doc.Root, xslt.TransformOptions{
		Documents: &xslt.FileResolver{Roots: []string{dir}},
	})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	got := xslt.SerializeAsXML(res)
	if !strings.Contains(got, "<w>HELLO:ENV:42</w>") {
		t.Fatalf("got %s, want a <w> holding HELLO:ENV:42", got)
	}
}

// With no resolver a nested stylesheet cannot be fetched, and fn:transform
// says so with its own code rather than letting fn:doc's escape. The sandbox
// is the same one every other remote reference obeys.
// A stylesheet-location that cannot be retrieved identifies no stylesheet, so
// the code is FOXT0002 and not FOXT0001. FOXT0001 is reserved for a
// transformation this processor cannot RUN -- an unavailable vendor named in
// requested-properties -- which is why transform-001 asserts FOXT0002 for a
// file that is simply not there.
func TestFnTransformRefusesWithoutAResolver(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:template match="/">
	    <o><xsl:sequence select="transform(map{'stylesheet-location': 'x.xsl', 'source-node': /})?output"/></o>
	  </xsl:template>
	</xsl:stylesheet>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	_, err = sheet.Transform(context.Background(), doc.Root, xslt.TransformOptions{})
	if err == nil || !strings.Contains(err.Error(), "FOXT0002") {
		t.Fatalf("got %v, want FOXT0002", err)
	}
	if strings.Contains(err.Error(), "FOXT0001") {
		t.Fatalf("got %v, want FOXT0002 and not FOXT0001", err)
	}
}

// The options must identify a stylesheet somehow.
func TestFnTransformNeedsAStylesheet(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:template match="/">
	    <o><xsl:sequence select="transform(map{'source-node': /})?output"/></o>
	  </xsl:template>
	</xsl:stylesheet>`
	tree, _ := xdm.ParseString(src, xdm.ParseOptions{})
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	_, err = sheet.Transform(context.Background(), doc.Root, xslt.TransformOptions{})
	if err == nil || !strings.Contains(err.Error(), "FOXT0002") {
		t.Fatalf("got %v, want FOXT0002", err)
	}
}

// delivery-format decides what the map's values are.
func TestFnTransformDeliveryFormats(t *testing.T) {
	for _, tc := range []struct{ format, want string }{
		{"serialized", "instance of xs:string"},
		{"document", "instance of document-node()"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			   xmlns:xs="http://www.w3.org/2001/XMLSchema"
			   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
			  <xsl:variable name="inner" as="document-node()">
			    <xsl:document>
			      <xsl:element name="xsl:stylesheet">
			        <xsl:attribute name="version">3.0</xsl:attribute>
			        <xsl:element name="xsl:template">
			          <xsl:attribute name="match">/</xsl:attribute>
			          <xsl:element name="w"/>
			        </xsl:element>
			      </xsl:element>
			    </xsl:document>
			  </xsl:variable>
			  <xsl:template match="/">
			    <o><xsl:value-of select="transform(map{
			      'stylesheet-node': $inner, 'source-node': /,
			      'delivery-format': '` + tc.format + `'})?output ` + tc.want + `"/></o>
			  </xsl:template>
			</xsl:stylesheet>`
			tree, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
			if err != nil {
				t.Fatalf("compiling: %v", err)
			}
			doc, _ := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
			res, err := sheet.Transform(context.Background(), doc.Root, xslt.TransformOptions{})
			if err != nil {
				t.Fatalf("transforming: %v", err)
			}
			if got := xslt.SerializeAsXML(res); !strings.Contains(got, ">true<") {
				t.Fatalf("got %s, want the type test to hold", got)
			}
		})
	}
}

// A stylesheet-location naming a file that does not exist is FOXT0002, not
// FOXT0001. This is the distinction transform-001 turns on: its description is
// "module is not available (non-existent file)" and it asserts FOXT0002. The
// resolver is present and working here, so the failure is the missing file
// itself rather than a disabled resolver.
func TestFnTransformMissingLocationIsFOXT0002(t *testing.T) {
	dir := t.TempDir()
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:template match="/">
	    <o><xsl:sequence select="transform(map{'stylesheet-location': 'non-existent.xsl', 'initial-match-selection': 42})?*"/></o>
	  </xsl:template>
	</xsl:stylesheet>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	_, err = sheet.Transform(context.Background(), doc.Root, xslt.TransformOptions{
		Documents: mustFileResolver(t, dir),
	})
	if err == nil {
		t.Fatal("a non-existent stylesheet-location must be an error")
	}
	if !strings.Contains(err.Error(), "FOXT0002") {
		t.Fatalf("got %v, want FOXT0002", err)
	}
	if strings.Contains(err.Error(), "FOXT0001") {
		t.Fatalf("got %v, want FOXT0002 and not FOXT0001", err)
	}
}

// An option written as element content arrives as a text node rather than a
// string, and stands for its string value. Refusing it raised XPTY0004 on a
// map that says exactly what a string-valued one says, which is what
// transform-008 exercises with <xsl:map-entry>transform-008a.xsl</xsl:map-entry>.
func TestFnTransformOptionFromTextNode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inner.xsl"), []byte(
		`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		  <xsl:template name="xsl:initial-template"><a>892</a></xsl:template>
		</xsl:stylesheet>`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:template name="xsl:initial-template">
	    <xsl:variable name="opts" as="map(*)">
	      <xsl:map>
	        <xsl:map-entry key="'delivery-format'" select="'raw'"/>
	        <xsl:map-entry key="'stylesheet-location'">inner.xsl</xsl:map-entry>
	        <xsl:map-entry key="'initial-template'"
	          select="QName('http://www.w3.org/1999/XSL/Transform','xsl:initial-template')"/>
	      </xsl:map>
	    </xsl:variable>
	    <o><xsl:sequence select="transform($opts)?output"/></o>
	  </xsl:template>
	</xsl:stylesheet>`
	base := "file://" + dir + "/outer.xsl"
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		Documents:       mustFileResolver(t, dir),
		InitialTemplate: "xsl:initial-template",
	})
	if err != nil {
		t.Fatalf("a text-node option value must be read as its string value, got %v", err)
	}
	var buf strings.Builder
	if err := res.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "<a>892</a>") {
		t.Fatalf("got %q, want the nested result", buf.String())
	}
}

// An xsl:result-document with no href IS the principal output, so it is keyed
// under "output" and not under the empty string. Section 24.3 changes the
// current output URI only for an instruction WITH an href; with none it stays
// the base output URI. Keying it as a secondary left ?output holding the empty
// tree the stylesheet never wrote to, which is transform-009's symptom: the
// principal serialization came out empty.
func TestFnTransformHreflessResultDocumentIsPrincipal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inner.xsl"), []byte(
		`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		  <xsl:output method="text"/>
		  <xsl:template match="/">
		    <xsl:result-document method="xml"><principal>main output</principal></xsl:result-document>
		    <xsl:result-document href="secondary2.xml" method="xml" omit-xml-declaration="yes"><secondary2>xml doc</secondary2></xsl:result-document>
		  </xsl:template>
		</xsl:stylesheet>`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:template match="/">
	    <o><xsl:sequence select="transform(map{
	      'stylesheet-location': 'inner.xsl',
	      'source-node': .,
	      'delivery-format': 'serialized'})?output"/></o>
	  </xsl:template>
	</xsl:stylesheet>`
	base := "file://" + dir + "/outer.xsl"
	tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	res, err := sheet.Transform(context.Background(), doc.Root, xslt.TransformOptions{
		Documents: mustFileResolver(t, dir),
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := res.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	// ?output must carry the href-less document, not the empty principal tree.
	// delivery-format is "serialized", so the value is a string and the outer
	// serializer escapes its markup -- the content is what matters here.
	if !strings.Contains(buf.String(), "&lt;principal&gt;main output&lt;/principal&gt;") {
		t.Fatalf("got %q, want ?output to hold the href-less result document", buf.String())
	}
	// The document that DOES name an href stays a secondary and must not have
	// been folded into the principal entry.
	if strings.Contains(buf.String(), "secondary2") {
		t.Fatalf("got %q, want the href-ed document to stay a secondary", buf.String())
	}
}

// package-name selects a stylesheet by name and version range rather than by
// location, exactly as xsl:use-package does. It resolves through the same
// PackageResolver the outer compilation was given; without the option being
// read at all the options looked like they identified no stylesheet, which is
// what transform-005, -006 and -007 failed on.
func TestFnTransformPackageName(t *testing.T) {
	pkgSrc := `<xsl:package name="http://example.com/p" package-version="1.0.5"
	   xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:template name="main" visibility="public"><in>1.0.5</in></xsl:template>
	</xsl:package>`
	pkg, err := xdm.ParseString(pkgSrc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:template name="main">
	    <out><xsl:copy-of select="transform(map{
	      'package-name': 'http://example.com/p',
	      'package-version': '1.*',
	      'initial-template': QName('','main'),
	      'delivery-format': 'raw'})?output"/></out>
	  </xsl:template>
	</xsl:stylesheet>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{
		PackageResolver: fixedPackageResolver{root: pkg.Root},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialTemplate: "main",
	})
	if err != nil {
		t.Fatalf("package-name must identify a stylesheet, got %v", err)
	}
	var buf strings.Builder
	if err := res.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "<in>1.0.5</in>") {
		t.Fatalf("got %q, want the package's result", buf.String())
	}
}

// A package-name with no resolver to look it up in identifies no stylesheet,
// which is FOXT0002 rather than a silent fall-through to "no stylesheet named"
// or a nil dereference.
func TestFnTransformPackageNameWithoutResolver(t *testing.T) {
	src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:map="http://www.w3.org/2005/xpath-functions/map" version="3.0">
	  <xsl:template name="main">
	    <out><xsl:copy-of select="transform(map{
	      'package-name': 'http://example.com/p',
	      'initial-template': QName('','main')})?output"/></out>
	  </xsl:template>
	</xsl:stylesheet>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sheet.Transform(context.Background(), nil, xslt.TransformOptions{
		InitialTemplate: "main",
	})
	if err == nil || !strings.Contains(err.Error(), "FOXT0002") {
		t.Fatalf("got %v, want FOXT0002", err)
	}
}

// fixedPackageResolver answers any request with one package, which is all the
// tests above need: the version matching itself is xsl:use-package's and is
// covered where that is tested.
type fixedPackageResolver struct{ root *xdm.Node }

func (f fixedPackageResolver) ResolvePackage(name, versionMatch string) (*xdm.Node, error) {
	return f.root, nil
}

// mustFileResolver builds a resolver rooted at dir, failing the test rather
// than the transform if the root itself is unusable.
func mustFileResolver(t *testing.T, dir string) *xslt.FileResolver {
	t.Helper()
	r, err := xslt.NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
