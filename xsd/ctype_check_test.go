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

// Under 1.0 the attribute is a fault rather than something to ignore.
//
// targetNamespace on a local declaration does not exist in XSD 1.0, and it is
// not a foreign attribute either: it is unprefixed, so it is in no namespace,
// and the wildcard that admits foreign attributes to the schema for schemas is
// namespace="##other", which excludes the absent namespace.
//
// The suite settles it. This document is s3_2_3si05, the one member of the
// ibmData S3_2_3 group that carries no version="1.1" gate -- its eight
// siblings all do -- so it is the only one expected invalid under 1.0 as well,
// and this attribute is the only thing wrong with it there.
func TestLocalTargetNamespaceRejectedIn10(t *testing.T) {
	doc := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	 <xs:complexType name="ct"><xs:complexContent>
	  <xs:restriction base="xs:anyType">
	   <xs:attribute name="a1" type="xs:string" targetNamespace="b"/>
	  </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`
	if err := loadVer(t, doc, Version10); err == nil {
		t.Fatal("1.0 has no targetNamespace on a local declaration; " +
			"the schema should have been rejected")
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

// src-ct.1 and src-ct.2.1 (§3.4.3): which base shape each content form may
// sit on. Every one of these is a suite schema the W3C expects rejected that
// loaded clean before checkContentDerivationForm existed.
func TestContentDerivationFormRejected(t *testing.T) {
	cases := []struct{ name, doc string }{
		{"complexContent extension of a named simple type (ctJ002)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="my"><xs:restriction base="xs:string"/></xs:simpleType>
		  <xs:complexType name="foo"><xs:complexContent>
		   <xs:extension base="my"><xs:all>
		    <xs:element name="e" type="xs:string"/></xs:all>
		   </xs:extension></xs:complexContent></xs:complexType></xs:schema>`},
		{"complexContent extension of xs:string (ctJ003)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="foo"><xs:complexContent>
		   <xs:extension base="xs:string"><xs:all>
		    <xs:element name="e" type="xs:string"/></xs:all>
		   </xs:extension></xs:complexContent></xs:complexType></xs:schema>`},
		{"simpleContent restriction of a simple type (ctM001)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="my"><xs:restriction base="xs:string"/></xs:simpleType>
		  <xs:complexType name="foo"><xs:simpleContent>
		   <xs:restriction base="my"><xs:length value="5"/></xs:restriction>
		  </xs:simpleContent></xs:complexType></xs:schema>`},
		{"simpleContent restriction of xs:string (ctD001)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="foo"><xs:simpleContent>
		   <xs:restriction base="xs:string"/>
		  </xs:simpleContent></xs:complexType></xs:schema>`},
		{"simpleContent extension of element-only content (ctE003)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="my"><xs:sequence>
		   <xs:element name="e" type="xs:string"/></xs:sequence></xs:complexType>
		  <xs:complexType name="foo"><xs:simpleContent>
		   <xs:extension base="my"/></xs:simpleContent></xs:complexType></xs:schema>`},
		{"simpleContent extension of xs:anyType (ctE004)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="foo"><xs:simpleContent>
		   <xs:extension base="xs:anyType"/>
		  </xs:simpleContent></xs:complexType></xs:schema>`},
		{"simpleContent restriction of a mixed base with no simpleType child (ctD004)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="foo"><xs:simpleContent>
		   <xs:restriction base="xs:anyType"/>
		  </xs:simpleContent></xs:complexType></xs:schema>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := loadVer(t, c.doc, Version10); err == nil {
				t.Error("the schema loaded; src-ct.1/src-ct.2 must reject it")
			}
		})
	}
}

// The two legal simpleContent shapes must keep loading. Extending a simple
// type is src-ct.2.1.3, and restricting a complex type whose content is a
// simple type is 2.1.1 — the two forms every schema with attributes on a
// text-only element uses.
func TestContentDerivationFormAccepted(t *testing.T) {
	doc := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	 <xs:complexType name="a"><xs:simpleContent>
	  <xs:extension base="xs:string">
	   <xs:attribute name="u" type="xs:string"/></xs:extension>
	 </xs:simpleContent></xs:complexType>
	 <xs:complexType name="b"><xs:simpleContent>
	  <xs:restriction base="a"><xs:maxLength value="4"/></xs:restriction>
	 </xs:simpleContent></xs:complexType></xs:schema>`
	if err := loadVer(t, doc, Version10); err != nil {
		t.Fatalf("the two legal simpleContent shapes must load: %v", err)
	}
}

// cos-ct-extends.1.4.3.2.2.1 and derivation-ok-restriction.5.4.1.2 are not the
// same rule, and reading them as one symmetric "mixedness must match" cost
// addB150, ctZ010h, particlesL012 and cta0001. An extension may not change
// mixedness in either direction; a restriction may only take it away.
func TestMixedConsistency(t *testing.T) {
	mixedBase := `<xs:complexType name="base" mixed="true"><xs:sequence>
	  <xs:element name="e" type="xs:string" minOccurs="0"/></xs:sequence></xs:complexType>`
	plainBase := `<xs:complexType name="base"><xs:sequence>
	  <xs:element name="e" type="xs:string" minOccurs="0"/></xs:sequence></xs:complexType>`
	wrap := func(base, derived string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
			base + derived + `</xs:schema>`
	}
	ext := func(mixed string) string {
		return `<xs:complexType name="d"><xs:complexContent` + mixed + `>
		  <xs:extension base="base"><xs:sequence>
		   <xs:element name="f" type="xs:string" minOccurs="0"/></xs:sequence>
		  </xs:extension></xs:complexContent></xs:complexType>`
	}
	res := func(mixed string) string {
		return `<xs:complexType name="d"><xs:complexContent` + mixed + `>
		  <xs:restriction base="base"><xs:sequence>
		   <xs:element name="e" type="xs:string" minOccurs="0"/></xs:sequence>
		  </xs:restriction></xs:complexContent></xs:complexType>`
	}
	cases := []struct {
		name string
		doc  string
		ok   bool
	}{
		{"mixed extension of an element-only base (ctZ010a)",
			wrap(plainBase, ext(` mixed="true"`)), false},
		{"element-only extension of a mixed base (ctZ010d)",
			wrap(mixedBase, ext(``)), false},
		{"mixed restriction of an element-only base (ctZ010e)",
			wrap(plainBase, res(` mixed="true"`)), false},
		{"element-only restriction of a mixed base (ctZ010h)",
			wrap(mixedBase, res(``)), true},
		{"mixed extension of a mixed base",
			wrap(mixedBase, ext(` mixed="true"`)), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := loadVer(t, c.doc, Version10)
			if c.ok && err != nil {
				t.Errorf("the schema must load: %v", err)
			}
			if !c.ok && err == nil {
				t.Error("the schema loaded; the mixedness rule must reject it")
			}
		})
	}
}

// derivation-ok-restriction.4 (§3.4.6) through Wildcard Subset (§3.10.6).
func TestAttributeWildcardRestriction(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		v    Version
		ok   bool
	}{
		{"a wildcard on a restriction of a base with none (ctO005)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="b"><xs:sequence>
		   <xs:element name="e" type="xs:string" minOccurs="0"/></xs:sequence></xs:complexType>
		  <xs:complexType name="d"><xs:complexContent><xs:restriction base="b">
		   <xs:sequence><xs:element name="e" type="xs:string"/></xs:sequence>
		   <xs:anyAttribute namespace="##other"/>
		  </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`,
			Version10, false},
		{"##any is not a subset of ##other (ctO007)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="b"><xs:sequence>
		   <xs:element name="e" type="xs:string" minOccurs="0"/></xs:sequence>
		   <xs:anyAttribute namespace="##other"/></xs:complexType>
		  <xs:complexType name="d"><xs:complexContent><xs:restriction base="b">
		   <xs:sequence><xs:element name="e" type="xs:string"/></xs:sequence>
		   <xs:anyAttribute namespace="##any"/>
		  </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`,
			Version10, false},
		{"##other is a subset of ##any", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="b"><xs:sequence>
		   <xs:element name="e" type="xs:string" minOccurs="0"/></xs:sequence>
		   <xs:anyAttribute namespace="##any"/></xs:complexType>
		  <xs:complexType name="d"><xs:complexContent><xs:restriction base="b">
		   <xs:sequence><xs:element name="e" type="xs:string"/></xs:sequence>
		   <xs:anyAttribute namespace="##other"/>
		  </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`,
			Version10, true},
		// wild017: XSD 1.1 notNamespace names a *set*, and excluding more
		// namespaces is narrower, not incomparable.
		{"notNamespace excluding more is a subset (wild017)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="b"><xs:sequence/>
		   <xs:anyAttribute notNamespace="urn:a urn:b" processContents="lax"/></xs:complexType>
		  <xs:complexType name="d"><xs:complexContent><xs:restriction base="b">
		   <xs:sequence/>
		   <xs:anyAttribute notNamespace="urn:b urn:a urn:c" processContents="lax"/>
		  </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`,
			Version11, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := loadVer(t, c.doc, c.v)
			if c.ok && err != nil {
				t.Errorf("the schema must load: %v", err)
			}
			if !c.ok && err == nil {
				t.Error("the schema loaded; derivation-ok-restriction.4 must reject it")
			}
		})
	}
}

// The open-content clause of cos-ct-restricts: a restriction that declares
// {open content} needs a base that declares one too, and may not interleave
// where the base only suffixes.
//
// Both halves are conditioned on the derived particle admitting more than the
// empty sequence, which is what separates the four cases here. open016 and
// open019 are the rejections; open020/open021 (interleave over a suffix base)
// and open022 (no base open content at all) have empty derived models, where
// the two modes denote the same language and the base's own particle already
// admits what the wildcard would. Both pairs loaded clean before this rule.
//
// The sub-clause numbering under cos-ct-restricts is deliberately not asserted
// here; see the note in checkOpenContentRestriction.
func TestOpenContentRestrictionMode(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		ok   bool
	}{
		{"open016: derived opens what a closed base rejects", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="B"><xs:sequence>
		   <xs:element name="a" maxOccurs="unbounded"/>
		   <xs:element name="b" minOccurs="0"/></xs:sequence></xs:complexType>
		  <xs:complexType name="R"><xs:complexContent><xs:restriction base="B">
		   <xs:openContent mode="suffix">
		    <xs:any namespace="http://open.com/" processContents="lax"/>
		   </xs:openContent>
		   <xs:sequence><xs:element name="a"/></xs:sequence>
		  </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`, false},

		{"open019: interleave may not restrict suffix over a non-empty model", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="B">
		   <xs:openContent mode="suffix">
		    <xs:any namespace="http://open.com/" processContents="lax"/>
		   </xs:openContent>
		   <xs:sequence><xs:element name="a" maxOccurs="unbounded"/>
		    <xs:element name="b" minOccurs="0"/></xs:sequence></xs:complexType>
		  <xs:complexType name="R"><xs:complexContent><xs:restriction base="B">
		   <xs:openContent mode="interleave">
		    <xs:any namespace="http://open.com/" processContents="lax"/>
		   </xs:openContent>
		   <xs:sequence><xs:element name="a"/></xs:sequence>
		  </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`, false},

		{"open020: an empty derived model makes the two modes the same", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="B">
		   <xs:openContent mode="suffix">
		    <xs:any namespace="http://open.com/" processContents="lax"/>
		   </xs:openContent>
		   <xs:sequence><xs:element name="a" minOccurs="0" maxOccurs="unbounded"/>
		    <xs:element name="b" minOccurs="0"/></xs:sequence></xs:complexType>
		  <xs:complexType name="R"><xs:complexContent><xs:restriction base="B">
		   <xs:openContent mode="interleave">
		    <xs:any namespace="http://open.com/" processContents="lax"/>
		   </xs:openContent>
		   <xs:sequence/>
		  </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`, true},

		{"open022: a closed base whose particle carries the wildcard", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="B"><xs:sequence>
		   <xs:any namespace="http://open.com/" processContents="lax"
		    minOccurs="0" maxOccurs="unbounded"/></xs:sequence></xs:complexType>
		  <xs:complexType name="R"><xs:complexContent><xs:restriction base="B">
		   <xs:openContent mode="interleave">
		    <xs:any namespace="http://open.com/" processContents="lax"/>
		   </xs:openContent>
		   <xs:sequence/>
		  </xs:restriction></xs:complexContent></xs:complexType></xs:schema>`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := loadVer(t, c.doc, Version11)
			if c.ok && err != nil {
				t.Errorf("the schema must load: %v", err)
			}
			if !c.ok && err == nil {
				t.Error("the schema loaded; cos-ct-restricts must reject it")
			}
		})
	}
}
