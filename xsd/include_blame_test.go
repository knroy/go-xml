package xsd

import (
	"strings"
	"testing"
)

// A fault in an included document must say which document it was in.
//
// The parser already knows: schemaDoc.baseURI is in hand while the document is
// read. Discarding it produced the issue-#4 shape — a src-resolve complaint
// about a prefix that is undeclared in inner.xsd, read by someone looking at
// main.xsd, where that prefix is declared and everything is in order.
func TestErrorInsideIncludedDocumentNamesTheDocument(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:t" targetNamespace="urn:t">
		  <xs:include schemaLocation="inner.xsd"/>
		  <xs:element name="root" type="xs:string"/>
		</xs:schema>`,
		"inner.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
		  <xs:element name="bad" type="t:NoSuchType"/>
		</xs:schema>`,
	}
	_, err := loadFromMap(t, "main.xsd", docs)
	if err == nil {
		t.Fatal("an undeclared prefix in the included document should be refused")
	}
	// The identity of the complaint survives: the code is still there.
	if !strings.Contains(err.Error(), "src-resolve") {
		t.Errorf("error = %v, should still carry the src-resolve code", err)
	}
	if !strings.Contains(err.Error(), "inner.xsd") {
		t.Errorf("error = %v, should name inner.xsd as the document the fault "+
			"is in; main.xsd declares the prefix and is not at fault", err)
	}
}

// The same through <xs:import>, the other composition edge.
func TestErrorInsideImportedDocumentNamesTheDocument(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:o="urn:other" targetNamespace="urn:main">
		  <xs:import namespace="urn:other" schemaLocation="other.xsd"/>
		  <xs:element name="root" type="xs:string"/>
		</xs:schema>`,
		"other.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:other">
		  <xs:element name="bad" type="q:NoSuchType"/>
		</xs:schema>`,
	}
	_, err := loadFromMap(t, "main.xsd", docs)
	if err == nil {
		t.Fatal("an undeclared prefix in the imported document should be refused")
	}
	if !strings.Contains(err.Error(), "other.xsd") {
		t.Errorf("error = %v, should name other.xsd", err)
	}
}

// The top-level document is the one the caller is already looking at, so its
// faults read exactly as they did before: no location prefix, and in
// particular not a redundant "in main.xsd" on every line.
func TestErrorInMainDocumentIsNotAttributed(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
		  <xs:element name="bad" type="q:NoSuchType"/>
		</xs:schema>`,
	}
	_, err := loadFromMap(t, "main.xsd", docs)
	if err == nil {
		t.Fatal("an undeclared prefix should be refused")
	}
	if strings.Contains(err.Error(), "main.xsd") {
		t.Errorf("error = %v, must not name the document the caller passed in; "+
			"only a nested document earns the attribution", err)
	}
	if !strings.Contains(err.Error(), "src-resolve") {
		t.Errorf("error = %v, should carry the src-resolve code", err)
	}
}
