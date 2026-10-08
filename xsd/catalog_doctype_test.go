package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A document the caller registered in a catalog may carry a DOCTYPE, as the
// W3C's schema for schemas does, without the caller opening DOCTYPE for every
// schema document. The same bytes read through the fallback are still refused.
func TestCatalogDocumentMayCarryDOCTYPE(t *testing.T) {
	const dep = `<!DOCTYPE xs:schema [<!ENTITY ns "urn:dep">]>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="&ns;">
  <xs:element name="d" type="xs:string"/>
</xs:schema>`
	const main = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    xmlns:d="urn:dep" targetNamespace="urn:main">
  <xs:import namespace="urn:dep" schemaLocation="http://example.org/dep.xsd"/>
  <xs:element name="m"><xs:complexType><xs:sequence>
    <xs:element ref="d:d"/>
  </xs:sequence></xs:complexType></xs:element>
</xs:schema>`
	root := func() *xdm.Node {
		tree, err := xdm.Parse(strings.NewReader(main), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return tree.Root
	}

	cat := NewCatalogResolver()
	cat.Add("urn:dep", []byte(dep), "http://example.org/dep.xsd")
	if _, err := Load(root(), "", Options{Resolver: cat}); err != nil {
		t.Fatalf("catalog document with a DOCTYPE: %v", err)
	}

	viaFallback := NewCatalogResolver()
	viaFallback.SetFallback(&MapResolver{ByLocation: map[string]string{
		"http://example.org/dep.xsd": dep,
	}})
	_, err := Load(root(), "", Options{Resolver: viaFallback})
	if err == nil || !strings.Contains(err.Error(), "DOCTYPE") {
		t.Fatalf("fallback document with a DOCTYPE: got %v, want a DOCTYPE refusal", err)
	}
}
