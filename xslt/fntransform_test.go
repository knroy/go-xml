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
	if err == nil || !strings.Contains(err.Error(), "FOXT0001") {
		t.Fatalf("got %v, want FOXT0001", err)
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
