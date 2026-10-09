package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A schema that imports the XSD 1.1 schema for schemas by its www.w3.org URL,
// as the XSLT 3.0 schema does, must load under -catalog from local copies --
// the real W3C file, DOCTYPE and all -- and must not load without it, since
// the CLI fetches nothing. The schema also includes a file beside it, so the
// catalog is seen to fall back to the usual confined reads.
func TestValidateCatalogAnswersW3CImports(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "w3cschemas", "schemas", "XMLSchema.xsd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "<!DOCTYPE") {
		t.Fatal("the bundled schema for schemas no longer carries a DOCTYPE; " +
			"this test then proves nothing about permitting one")
	}
	catalog := filepath.Join(t.TempDir(), "w3c")
	writeSchema(t, filepath.Join(catalog, "XMLSchema.xsd"), string(src))

	dir := t.TempDir()
	writeSchema(t, filepath.Join(dir, "part.xsd"),
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">`+
			`<xs:element name="note" type="xs:string"/></xs:schema>`)
	main := filepath.Join(dir, "main.xsd")
	writeSchema(t, main,
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		    xmlns:t="urn:t" targetNamespace="urn:t" elementFormDefault="qualified">
		  <xs:import namespace="http://www.w3.org/2001/XMLSchema"
		      schemaLocation="http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd"/>
		  <xs:include schemaLocation="part.xsd"/>
		  <xs:element name="doc"><xs:complexType><xs:sequence>
		    <xs:element ref="t:note"/>
		    <xs:element ref="xs:schema"/>
		  </xs:sequence></xs:complexType></xs:element>
		</xs:schema>`)

	_, err = schemaValidator(main, "", "1.1", "2.0", "", "", 1)
	if err == nil || !strings.Contains(err.Error(), `"xs:schema"`) {
		t.Fatalf("without -catalog: got %v, want the unresolved xs:schema", err)
	}

	validate, err := schemaValidator(main, "", "1.1", "2.0", "", catalog, 1)
	if err != nil {
		t.Fatalf("with -catalog: %v", err)
	}
	doc, err := xdm.ParseString(`<doc xmlns="urn:t"><note>n</note>`+
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/></doc>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(doc.Root); err != nil {
		t.Errorf("a valid instance failed: %v", err)
	}
	bad, err := xdm.ParseString(`<doc xmlns="urn:t"><note>n</note><other/></doc>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(bad.Root); err == nil {
		t.Error("an invalid instance passed")
	}
}

// A directory holding none of the W3C files is named in the error rather than
// accepted as an empty catalog, which would fail later and less clearly.
func TestValidateCatalogWithoutTheFilesIsRefused(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.xsd")
	writeSchema(t, main, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`)
	empty := t.TempDir()
	_, err := schemaValidator(main, "", "1.0", "2.0", "", empty, 1)
	if err == nil || !strings.Contains(err.Error(), "holds none of") {
		t.Fatalf("got %v, want the empty-catalog refusal", err)
	}
}

// The W3C ships schema-for-xslt30.xsd beside its own XMLSchema.xsd, imported
// by the bare name, and that copy carries a DOCTYPE. With -catalog the
// catalog's file must answer the bare name too; reading the sibling first
// refused its DOCTYPE and never tried the catalog.
func TestValidateCatalogWinsOverASiblingCopy(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "w3cschemas", "schemas", "XMLSchema.xsd"))
	if err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(t.TempDir(), "w3c")
	writeSchema(t, filepath.Join(catalog, "XMLSchema.xsd"), string(src))

	dir := t.TempDir()
	writeSchema(t, filepath.Join(dir, "XMLSchema.xsd"), string(src))
	main := filepath.Join(dir, "main.xsd")
	writeSchema(t, main,
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:import namespace="http://www.w3.org/2001/XMLSchema" schemaLocation="XMLSchema.xsd"/>
		  <xs:element name="doc"><xs:complexType><xs:sequence>
		    <xs:element ref="xs:schema"/>
		  </xs:sequence></xs:complexType></xs:element>
		</xs:schema>`)
	if _, err := schemaValidator(main, "", "1.1", "2.0", "", catalog, 1); err != nil {
		t.Fatalf("with -catalog and a sibling XMLSchema.xsd: %v", err)
	}
}
