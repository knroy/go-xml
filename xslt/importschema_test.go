package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// TestImportSchemaInline covers the form that needs no resolver: the schema is
// written inside the stylesheet.
func TestImportSchemaInline(t *testing.T) {
	src := `
	<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	                xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:import-schema>
	    <xs:schema targetNamespace="urn:t">
	      <xs:simpleType name="code">
	        <xs:restriction base="xs:string"><xs:maxLength value="3"/></xs:restriction>
	      </xs:simpleType>
	    </xs:schema>
	  </xsl:import-schema>
	  <xsl:template match="/"><out/></xsl:template>
	</xsl:stylesheet>`

	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("xsl:import-schema with an inline schema should compile: %v", err)
	}
	s := sheet.Schema()
	if s == nil {
		t.Fatal("the stylesheet has no schema")
	}
	if s.Types[xdm.QName{URI: "urn:t", Local: "code"}] == nil {
		t.Error("the imported type is missing")
	}
}

// TestImportSchemaByLocation covers loading through a resolver.
func TestImportSchemaByLocation(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
	  <xs:element name="root" type="xs:int"/>
	</xs:schema>`

	src := `
	<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:import-schema namespace="urn:t" schema-location="s.xsd"/>
	  <xsl:template match="/"><out/></xsl:template>
	</xsl:stylesheet>`

	sheet, err := Compile(mustParse(t, src), CompileOptions{
		SchemaResolver: &xsd.MapResolver{ByLocation: map[string]string{"s.xsd": schema}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if sheet.Schema().Elements[xdm.QName{URI: "urn:t", Local: "root"}] == nil {
		t.Error("the imported element declaration is missing")
	}
}

// TestImportSchemaWithoutResolver records that a schema-location is refused
// unless the caller configures a resolver.
//
// The reason is the one that governs xsl:include: following a location means
// fetching whatever the stylesheet names, which is the caller's decision to
// make rather than the stylesheet author's.
func TestImportSchemaWithoutResolver(t *testing.T) {
	src := `
	<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:import-schema namespace="urn:t" schema-location="s.xsd"/>
	  <xsl:template match="/"><out/></xsl:template>
	</xsl:stylesheet>`

	_, err := Compile(mustParse(t, src), CompileOptions{})
	if err == nil {
		t.Fatal("a schema-location with no resolver should be refused")
	}
	if !strings.Contains(err.Error(), "SchemaResolver") {
		t.Errorf("error %q does not say how to enable it", err)
	}
}

// TestImportSchemaNamespaceOnly covers the declaration that names a namespace
// and nothing else, which asserts availability rather than loading anything.
func TestImportSchemaNamespaceOnly(t *testing.T) {
	src := `
	<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:import-schema namespace="urn:t"/>
	  <xsl:template match="/"><out/></xsl:template>
	</xsl:stylesheet>`

	if _, err := Compile(mustParse(t, src), CompileOptions{}); err != nil {
		t.Errorf("a namespace-only import-schema should compile: %v", err)
	}
}

// TestImportSchemaMergesSeveral covers more than one declaration contributing
// to a single schema.
func TestImportSchemaMergesSeveral(t *testing.T) {
	src := `
	<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	                xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:import-schema>
	    <xs:schema targetNamespace="urn:a">
	      <xs:simpleType name="ta"><xs:restriction base="xs:string"/></xs:simpleType>
	    </xs:schema>
	  </xsl:import-schema>
	  <xsl:import-schema>
	    <xs:schema targetNamespace="urn:b">
	      <xs:simpleType name="tb"><xs:restriction base="xs:int"/></xs:simpleType>
	    </xs:schema>
	  </xsl:import-schema>
	  <xsl:template match="/"><out/></xsl:template>
	</xsl:stylesheet>`

	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	s := sheet.Schema()
	if s.Types[xdm.QName{URI: "urn:a", Local: "ta"}] == nil ||
		s.Types[xdm.QName{URI: "urn:b", Local: "tb"}] == nil {
		t.Error("both imported schemas should contribute")
	}
}

// TestImportSchemaEnablesValidation is the point of the feature: the schema the
// stylesheet declares is the one a caller can validate the source against,
// rather than loading it a second time and risking the two disagreeing.
func TestImportSchemaEnablesValidation(t *testing.T) {
	src := `
	<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	                xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:import-schema>
	    <xs:schema>
	      <xs:element name="root" type="xs:int"/>
	    </xs:schema>
	  </xsl:import-schema>
	  <xsl:template match="/"><out/></xsl:template>
	</xsl:stylesheet>`

	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	good, err := xdm.ParseString(`<root>42</root>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sheet.Schema().Validate(good.Root, xsd.ValidateOptions{}); err != nil {
		t.Errorf("a valid document should pass: %v", err)
	}

	bad, err := xdm.ParseString(`<root>notanint</root>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sheet.Schema().Validate(bad.Root, xsd.ValidateOptions{}); err == nil {
		t.Error("an invalid document should fail")
	}
}

func mustParse(t *testing.T, src string) *xdm.Node {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	return tree.Root
}
