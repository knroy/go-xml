package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestJSONToXMLTyped covers the end of the chain fn:json-to-xml's validate
// option runs through: xsl:import-schema of the function namespace with no
// location supplies the built-in schema of F&O 3.1 §C.2, so the type names
// resolve, and the TreeValidator hook annotates the constructed tree, so the
// assertions about those names hold.
//
// It is json-to-xml-typed-001 to -007 in miniature; each of those was false
// for want of one half or the other.
func TestJSONToXMLTyped(t *testing.T) {
	for _, tc := range []struct{ name, json, test string }{
		{"map", `{}`, `json-to-xml($j,$o)/* instance of element(j:map,j:mapType)`},
		{"array", `[]`, `json-to-xml($j,$o)/* instance of element(j:array,j:arrayType)`},
		{"number", `[1]`, `json-to-xml($j,$o)/*/*[1] instance of element(j:number,j:numberType)`},
		{"string", `[&#34;a&#34;]`, `json-to-xml($j,$o)/*/*[1] instance of element(j:string,j:stringType)`},
		{"boolean", `[true]`, `json-to-xml($j,$o)/*/*[1] instance of element(j:boolean,xs:boolean)`},
		{"null", `[null]`, `json-to-xml($j,$o)/*/*[1] instance of element(j:null,j:nullType)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `
			<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			   xmlns:xs="http://www.w3.org/2001/XMLSchema"
			   xmlns:j="http://www.w3.org/2005/xpath-functions">
			  <xsl:import-schema namespace="http://www.w3.org/2005/xpath-functions"/>
			  <xsl:param name="o" select="map{'validate':true()}"/>
			  <xsl:param name="j" as="xs:string" select="'` + tc.json + `'"/>
			  <xsl:template name="main"><xsl:value-of select="` + tc.test + `"/></xsl:template>
			</xsl:stylesheet>`
			sheet, err := Compile(mustParse(t, src), CompileOptions{})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			res, err := sheet.Transform(context.Background(), nil,
				TransformOptions{InitialTemplate: "main"})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got := res.String(); !strings.HasSuffix(got, "true") {
				t.Errorf("%s: got %q, want a result ending in true", tc.test, got)
			}
		})
	}
}

// TestJSONToXMLTypedDefaultsAttributes covers the other half of what
// validate=true promises. F&O 3.1 §17.5.3: "If the result is typed, every
// element named string will have an attribute named escaped whose value is
// either true or false, and every element having an attribute named key will
// also have an attribute named escaped-key whose value is either true or
// false."
//
// Nothing in the construction rules writes those attributes when their value
// is false — untyped output must NOT carry them — so they can only arrive as
// the schema defaults that validation supplies.
func TestJSONToXMLTypedDefaultsAttributes(t *testing.T) {
	const src = `
	<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema"
	   xmlns:j="http://www.w3.org/2005/xpath-functions">
	  <xsl:template name="main">
	    <xsl:sequence select="json-to-xml('{&#34;a&#34;:&#34;x&#34;}',map{'validate':true()})"/>
	  </xsl:template>
	</xsl:stylesheet>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "main"})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	got := res.String()
	for _, want := range []string{`escaped="false"`, `escaped-key="false"`} {
		if !strings.Contains(got, want) {
			t.Errorf("a typed result should carry %s: %s", want, got)
		}
	}
}

// TestJSONToXMLUntypedOmitsAttributes is the converse, and the reason the
// defaults cannot simply be written unconditionally: without validate=true
// the same specification says the attributes "will either be present with the
// value true, or will be absent. They will never be present with the value
// false."
func TestJSONToXMLUntypedOmitsAttributes(t *testing.T) {
	const src = `
	<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:j="http://www.w3.org/2005/xpath-functions">
	  <xsl:template name="main">
	    <xsl:sequence select="json-to-xml('{&#34;a&#34;:&#34;x&#34;}')"/>
	  </xsl:template>
	</xsl:stylesheet>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "main"})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := res.String(); strings.Contains(got, "escaped") {
		t.Errorf("an untyped result should carry no escaped attributes: %s", got)
	}
}

// TestImportSchemaJSONNamespace checks that importing the function namespace
// with no schema-location brings the built-in components in, rather than
// leaving the empty schema an import with no location otherwise creates.
func TestImportSchemaJSONNamespace(t *testing.T) {
	const src = `
	<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:import-schema namespace="http://www.w3.org/2005/xpath-functions"/>
	  <xsl:template match="/"><out/></xsl:template>
	</xsl:stylesheet>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	s := sheet.Schema()
	if s == nil {
		t.Fatal("the stylesheet has no schema")
	}
	for _, local := range []string{"mapType", "arrayType", "stringType", "numberType", "nullType"} {
		if s.Types[xdm.QName{URI: xdm.NSFN, Local: local}] == nil {
			t.Errorf("the built-in schema for JSON should contribute %s", local)
		}
	}
	if s.Elements[xdm.QName{URI: xdm.NSFN, Local: "map"}] == nil {
		t.Error("the built-in schema for JSON should contribute the map declaration")
	}
}
