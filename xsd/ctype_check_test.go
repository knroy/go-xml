package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// loadVer assembles a schema under an explicit version, so one document can be
// asserted to fail under 1.1 and load under 1.0.
func loadVer(t *testing.T, src string, v Version) error {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the schema: %v", err)
	}
	_, err = Load(tree.Root, "s.xsd", Options{Version: v})
	return err
}

// src-attribute.6 / src-element.4 (XSD 1.1): a local declaration may name a
// foreign namespace only when it is restricting a foreign declaration. These
// nine shapes are ibmData S3_2_3 si01-si09; every one loaded clean before the
// check in ctype_check.go existed.
func TestLocalTargetNamespaceRejected(t *testing.T) {
	cases := []struct{ name, doc string }{
		{"form with targetNamespace on attribute", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:b="b">
		  <xs:complexType name="ct"><xs:complexContent>
		   <xs:restriction base="xs:anyType">
		    <xs:attribute name="a1" form="qualified" type="xs:string" targetNamespace="b"/>
		   </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`},
		{"attribute at schema level", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:attributeGroup name="ag1">
		   <xs:attribute name="a2" targetNamespace="b"/>
		  </xs:attributeGroup></xs:schema>`},
		{"element in group not complexType", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:group name="g1"><xs:sequence>
		   <xs:element name="e2" targetNamespace="b"/>
		  </xs:sequence></xs:group></xs:schema>`},
		{"restriction of anyType with attribute", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="ct"><xs:complexContent>
		   <xs:restriction base="xs:anyType">
		    <xs:attribute name="a1" type="xs:string" targetNamespace="b"/>
		   </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`},
		{"restriction of anyType with element", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="ct"><xs:complexContent>
		   <xs:restriction base="xs:anyType"><xs:sequence>
		    <xs:element name="e1" type="xs:integer" targetNamespace="b"/>
		   </xs:sequence></xs:restriction></xs:complexContent></xs:complexType></xs:schema>`},
		{"extension not restriction", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="a" xmlns:a="a">
		  <xs:complexType name="type2"><xs:simpleContent>
		   <xs:extension base="xs:integer">
		    <xs:attribute name="w" type="xs:integer" targetNamespace="b"/>
		   </xs:extension></xs:simpleContent></xs:complexType></xs:schema>`},
		{"bare complexType no restriction", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="a" xmlns:a="a">
		  <xs:complexType name="type1" mixed="true">
		   <xs:attribute name="s" type="xs:integer" targetNamespace="b"/>
		  </xs:complexType></xs:schema>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := loadVer(t, c.doc, Version11); err == nil {
				t.Fatalf("schema loaded but src-attribute.6/src-element.4 should have rejected it")
			}
		})
	}
}

// The same documents must still load under 1.0, where targetNamespace on a
// local declaration is merely an unknown attribute in no namespace.
func TestLocalTargetNamespaceIgnoredIn10(t *testing.T) {
	doc := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	 <xs:complexType name="ct"><xs:complexContent>
	  <xs:restriction base="xs:anyType">
	   <xs:attribute name="a1" type="xs:string" targetNamespace="b"/>
	  </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`
	if err := loadVer(t, doc, Version10); err != nil {
		t.Fatalf("1.0 must ignore targetNamespace on a local declaration: %v", err)
	}
}

// Naming the document's own target namespace stays legal: it says no more than
// form="qualified" would, so clause .3 does not apply. This is the shape UBL
// and CII rely on, and an over-broad rule would reject them.
func TestLocalTargetNamespaceOwnNamespaceAllowed(t *testing.T) {
	doc := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	          targetNamespace="urn:a" xmlns:a="urn:a">
	 <xs:complexType name="ct">
	  <xs:attribute name="a1" type="xs:string" targetNamespace="urn:a"/>
	 </xs:complexType></xs:schema>`
	if err := loadVer(t, doc, Version11); err != nil {
		t.Fatalf("naming the document's own namespace must be allowed: %v", err)
	}
}
