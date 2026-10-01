package xslt_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/internal/fileuri"
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
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

	base := fileuri.Of(filepath.Join(dir, "outer.xsl"))
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
	base := fileuri.Of(filepath.Join(dir, "outer.xsl"))
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
	base := fileuri.Of(filepath.Join(dir, "outer.xsl"))
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

// A static="yes" variable may call fn:transform. Section 9.7 gives a static
// expression the whole F&O library and excludes nothing from it, so the
// nested transformation runs during the STATIC PHASE of the outer
// compilation -- while Compile still holds compileMu and the package state
// that goes with it. transform-004 is the suite case.
func TestStaticTransform(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inner.xsl"), []byte(
		`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		   xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:f="f" version="3.0">
		  <xsl:template name="get-function"><xsl:sequence select="f:negative#1"/></xsl:template>
		  <xsl:function name="f:negative" as="xs:boolean">
		    <xsl:param name="in" as="xs:integer"/><xsl:sequence select="$in lt 0"/>
		  </xsl:function>
		</xsl:stylesheet>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outer := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:variable name="decider" static="yes" select="transform( map {
	    'stylesheet-location': 'inner.xsl',
	    'initial-template': QName('','get-function'),
	    'delivery-format': 'raw'})?output"/>
	  <xsl:template name="main">
	    <out xsl:use-when="$decider(-1)">42</out>
	    <out xsl:use-when="not($decider(-1))">24</out>
	  </xsl:template>
	</xsl:stylesheet>`
	base := fileuri.Of(filepath.Join(dir, "outer.xsl"))
	tree, err := xdm.ParseString(outer, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	// The resolver is the host's, exactly as at run time: without one the
	// static expression reaches no stylesheet at all, which the next test
	// pins.
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{
		BaseURI: base, Resolver: mustFileResolver(t, dir)})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil,
		xslt.TransformOptions{InitialTemplate: "main"})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		t.Fatal(err)
	}
	// 42 rather than 24: the function item the nested transformation returned
	// was CALLED from the use-when, one static expression later, and answered
	// true for -1. A 24 would mean the item came back but could not be
	// invoked; an error would mean it never came back at all.
	if !strings.Contains(b.String(), "<out>42</out>") {
		t.Fatalf("got %s, want <out>42</out>", b.String())
	}
}

// Nothing is fetched by default. A static fn:transform naming a
// stylesheet-location reaches it through the resolver the HOST gave Compile
// and through no other route, so a compilation configured with none is
// refused -- and the refusal names the missing configuration rather than the
// path it did not read.
func TestStaticTransformNoResolver(t *testing.T) {
	outer := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:variable name="v" static="yes" select="transform( map {
	    'stylesheet-location': '/etc/passwd.xsl'})?output"/>
	  <xsl:template name="main"><out xsl:use-when="true()">x</out></xsl:template>
	</xsl:stylesheet>`
	tree, err := xdm.ParseString(outer, xdm.ParseOptions{BaseURI: "file:///tmp/o.xsl"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = xslt.Compile(tree.Root, xslt.CompileOptions{BaseURI: "file:///tmp/o.xsl"})
	if err == nil {
		t.Fatal("a static fn:transform with no resolver was allowed to read a file")
	}
	if !strings.Contains(err.Error(), "FOXT0002") ||
		!strings.Contains(err.Error(), "document access is disabled") {
		t.Fatalf("got %v, want FOXT0002 naming the disabled access", err)
	}
	// The refusal names the option, not the path.
	if strings.Contains(err.Error(), "no such file") {
		t.Fatalf("the refusal leaked a filesystem probe: %v", err)
	}
}

// The outer compilation carries on correctly after a nested one has run
// inside it.
//
// compileNestedLocked deliberately saves and restores nothing, on the
// argument that every piece of package state is still zero at the one point a
// nested compilation can start -- the static phase, which runs before
// compileDocument writes any of it. This is the test that would notice if
// that stopped being true: the outer stylesheet imports a schema and names a
// type from it AFTER the static variable, so the name is resolved once the
// nested compilation has returned. A nested compile that clobbered
// compileSchema would leave t:code unknown.
func TestStaticTransformAfterANestedCompile(t *testing.T) {
	dir := t.TempDir()
	// The schema resolver confines itself to a real path, and TempDir hands
	// back one behind a symlink on macOS -- so the root, the files and the
	// base URI must all be spelled the same way or the relative
	// schema-location resolves outside it.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("t.xsd", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   targetNamespace="urn:t" xmlns:t="urn:t" elementFormDefault="qualified">
	  <xs:simpleType name="code"><xs:restriction base="xs:string"/></xs:simpleType>
	</xs:schema>`)
	// The nested stylesheet imports no schema of its own, so it is what would
	// clear compileSchema on its way through.
	write("inner.xsl", `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:template name="go"><xsl:sequence select="1"/></xsl:template>
	</xsl:stylesheet>`)
	outer := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:t="urn:t" version="3.0">
	  <xsl:import-schema namespace="urn:t" schema-location="t.xsd"/>
	  <xsl:variable name="v" static="yes" select="transform( map {
	    'stylesheet-location': 'inner.xsl',
	    'initial-template': QName('','go'),
	    'delivery-format': 'raw'})?output"/>
	  <xsl:template name="main">
	    <xsl:variable name="c" as="t:code" select="'x' cast as t:code"/>
	    <out><xsl:value-of select="$c"/></out>
	  </xsl:template>
	</xsl:stylesheet>`
	// A bare path rather than a file: URI: xsd.FileResolver joins a relative
	// schemaLocation onto filepath.Dir(base) without stripping a scheme, so
	// only a filesystem-shaped base resolves t.xsd inside the root.
	base := filepath.Join(dir, "outer.xsl")
	tree, err := xdm.ParseString(outer, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{
		BaseURI: base, Resolver: mustFileResolver(t, dir),
		SchemaResolver: &xsd.FileResolver{Root: dir}})
	if err != nil {
		// A complaint that t:code is unknown is the schema having been lost
		// to the nested compilation; anything else is a different failure.
		t.Fatalf("compiling after a nested compilation: %v", err)
	}
	out, err := sheet.Transform(context.Background(), nil,
		xslt.TransformOptions{InitialTemplate: "main"})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var b strings.Builder
	if err := out.Serialize(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), ">x</out>") {
		t.Fatalf("got %s, want an <out> holding x", b.String())
	}
}

// sourceLocationSheet runs an outer stylesheet, whose base URI is
// dir/outer.xsl, with a FileResolver rooted at root -- or with none when root
// is empty. The outer stylesheet has no source; everything the nested one
// sees arrives through the options map.
func sourceLocationSheet(t *testing.T, dir, root, options string) (string, error) {
	t.Helper()
	// xsl:strip-space is the outer stylesheet's, not the nested one's, so it
	// must not reach the document source-location loads. It is declared here
	// so the resolver is wrapped the way it is in real use.
	outer := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	  <xsl:strip-space elements="*"/>
	  <xsl:template name="xsl:initial-template">
	    <o><xsl:sequence select="transform(` + options + `)?output"/></o>
	  </xsl:template>
	</xsl:stylesheet>`
	base := fileuri.Of(filepath.Join(dir, "outer.xsl"))
	tree, err := xdm.ParseString(outer, xdm.ParseOptions{BaseURI: base})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{BaseURI: base})
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	topts := xslt.TransformOptions{}
	if root != "" {
		topts.Documents = mustFileResolver(t, root)
	}
	res, err := sheet.Transform(context.Background(), nil, topts)
	if err != nil {
		return "", err
	}
	return xslt.SerializeAsXML(res), nil
}

// sourceLocationDir writes a nested stylesheet and the document it runs over.
// The global variable proves the document is the global context item, and
// the text-node count that the outer xsl:strip-space was not applied to it.
func sourceLocationDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "src.xml"),
		[]byte(`<r> <v>7</v></r>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inner.xsl"), []byte(
		`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		  <xsl:variable name="g" select="string(//v)"/>
		  <xsl:template match="/"><w><xsl:value-of select="$g, //v, count(r/text())" separator=":"/></w></xsl:template>
		</xsl:stylesheet>`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// source-location names a document by URI, resolved against the static base
// URI of the call when relative, and templates are applied to it. GitHub
// issue #12; F&O 4.0 standardises what Saxon has read since 9.8.
func TestFnTransformSourceLocation(t *testing.T) {
	dir := sourceLocationDir(t)
	abs := fileuri.Of(filepath.Join(dir, "src.xml"))
	for name, loc := range map[string]string{"relative": "src.xml", "absolute": abs} {
		t.Run(name, func(t *testing.T) {
			got, err := sourceLocationSheet(t, dir, dir,
				`map{'stylesheet-location': 'inner.xsl', 'source-location': '`+loc+`'}`)
			if err != nil {
				t.Fatalf("transforming: %v", err)
			}
			if !strings.Contains(got, "<w>7:7:1</w>") {
				t.Fatalf("got %s, want <w>7:7:1</w>", got)
			}
		})
	}
}

// source-location reads through the same resolver as fn:doc, so it is
// refused with no resolver and outside the resolver's roots.
func TestFnTransformSourceLocationSandbox(t *testing.T) {
	dir := sourceLocationDir(t)
	opts := `map{'stylesheet-text': '&lt;xsl:stylesheet version=&quot;3.0&quot; xmlns:xsl=&quot;http://www.w3.org/1999/XSL/Transform&quot;/&gt;',
	  'source-location': 'src.xml'}`
	_, err := sourceLocationSheet(t, dir, "", opts)
	if code := xdm.ErrorCode(err); code != "FOXT0002" || !strings.Contains(err.Error(), "no resolver") {
		t.Errorf("no resolver: got %v, want FOXT0002 refusing document access", err)
	}
	// Rooted at a sibling directory, the document is outside every root.
	_, err = sourceLocationSheet(t, dir, t.TempDir(), opts)
	if code := xdm.ErrorCode(err); code != "FOXT0002" || !strings.Contains(err.Error(), "cannot retrieve source-location") {
		t.Errorf("outside the roots: got %v, want FOXT0002 refusing source-location", err)
	}
}

// F&O 4.0 asks for "exactly one of source-node, source-location, or
// initial-match-selection".
func TestFnTransformSourceLocationConflicts(t *testing.T) {
	dir := sourceLocationDir(t)
	for name, extra := range map[string]string{
		"source-node":             `'source-node': doc('src.xml')`,
		"initial-match-selection": `'initial-match-selection': doc('src.xml')`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := sourceLocationSheet(t, dir, dir,
				`map{'stylesheet-location': 'inner.xsl', 'source-location': 'src.xml', `+extra+`}`)
			if code := xdm.ErrorCode(err); code != "FOXT0002" || !strings.Contains(err.Error(), "mutually exclusive") {
				t.Fatalf("got %v, want FOXT0002 for mutually exclusive options", err)
			}
		})
	}
}

// F&O 3.1 applies the option parameter conventions to fn:transform's map: "It
// is not an error if the options map contains options with names other than
// those described in this specification." A misspelled string key and a
// vendor QName key are both ignored rather than refused.
func TestFnTransformIgnoresUnknownOptions(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template match="/">
	    <xsl:sequence select="transform(map{
	      'source-node': .,
	      'stylesheet-text': '&lt;xsl:stylesheet version=&quot;3.0&quot; xmlns:xsl=&quot;http://www.w3.org/1999/XSL/Transform&quot;&gt;&lt;xsl:template match=&quot;/&quot;&gt;&lt;ok/&gt;&lt;/xsl:template&gt;&lt;/xsl:stylesheet&gt;',
	      'post-proces': 1,
	      QName('http://example.com/vendor', 'v:tuning'): 2
	    })?output"/>
	  </xsl:template>
	</xsl:stylesheet>`
	got, err := runPP(t, sheet, `<r/>`)
	if err != nil {
		t.Fatalf("an option F&O does not define was refused: %v", err)
	}
	if !strings.Contains(got, "<ok/>") {
		t.Errorf("got %q, want the nested result <ok/>", got)
	}
}

// static-params binds the nested stylesheet's static parameters at its
// compilation (GitHub issue #15); it was read and dropped, so a static
// parameter kept its default. An unsupplied one still takes its select.
func TestFnTransformStaticParams(t *testing.T) {
	for _, tc := range []struct{ name, params, want string }{
		{"QName key", `map{QName('', 'p'): 'given'}`, "<o>given</o>"},
		{"string key", `map{'p': 'given'}`, "<o>given</o>"},
		{"absent", `map{}`, "<o>default</o>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			    xmlns:xs="http://www.w3.org/2001/XMLSchema" version="3.0">
			  <xsl:variable name="inner" as="xs:string"><![CDATA[
			    <xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
			      <xsl:param name="p" static="yes" select="'default'"/>
			      <xsl:template match="/"><o><xsl:value-of select="$p"/></o></xsl:template>
			    </xsl:stylesheet>]]></xsl:variable>
			  <xsl:template match="/">
			    <xsl:sequence select="transform(map{'stylesheet-text': $inner,
			        'source-node': /, 'static-params': ` + tc.params + `})?output"/>
			  </xsl:template>
			</xsl:stylesheet>`
			tree, _ := xdm.ParseString(src, xdm.ParseOptions{})
			sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{})
			if err != nil {
				t.Fatal(err)
			}
			doc, _ := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
			res, err := sheet.Transform(context.Background(), doc.Root, xslt.TransformOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got := xslt.SerializeAsXML(res); !strings.Contains(got, tc.want) {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
