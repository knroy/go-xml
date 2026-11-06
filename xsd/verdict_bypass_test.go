package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xsdstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// vbCheck validates doc in tree and stream mode and fails unless both give
// the verdict want (true = valid), and, for an invalid document, unless the
// tree's error names code.
func vbCheck(t *testing.T, label string, s *Schema, doc string, valid bool, code string) {
	t.Helper()
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	terr := s.Validate(tree.Root, ValidateOptions{})
	_, serr := svRun(t, s, doc, xsdstream.Mode{Root: xsdstream.Strict, Doc: true}, true)
	if (terr == nil) != valid || (serr == nil) != valid {
		t.Errorf("%s: tree %v, stream %v, want valid %v", label, terr, serr, valid)
		return
	}
	if !valid && !strings.Contains(terr.Error(), code) {
		t.Errorf("%s: tree error %v, want %s", label, terr, code)
	}
	if !valid && terr.Error() != serr.Error() {
		t.Errorf("%s: tree error %q, stream error %q", label, terr, serr)
	}
}

func vbLoad(t *testing.T, src string, ver Version) *Schema {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(tree.Root, "s.xsd", Options{Version: ver})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const vbXSI = ` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`

// xsi:nil suppresses only the content checks of a complex type: the
// attributes, the type's abstractness and its xs:ID attributes still count.
func TestNilledComplexTypeChecksAttributes(t *testing.T) {
	s := vbLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="d"><xs:complexType><xs:sequence><xs:element name="r" nillable="true" maxOccurs="unbounded"><xs:complexType><xs:sequence><xs:element name="c" type="xs:string"/></xs:sequence><xs:attribute name="req" type="xs:int" use="required"/><xs:attribute name="id" type="xs:ID"/><xs:attribute name="cur"><xs:simpleType><xs:restriction base="xs:string"><xs:enumeration value="USD"/></xs:restriction></xs:simpleType></xs:attribute></xs:complexType></xs:element></xs:sequence></xs:complexType></xs:element></xs:schema>`, Version11)
	vbCheck(t, "valid nilled", s, `<d`+vbXSI+`><r xsi:nil="true" req="1" cur="USD"/></d>`, true, "")
	vbCheck(t, "undeclared attribute", s, `<d`+vbXSI+`><r xsi:nil="true" req="1" bogus="1"/></d>`, false, "cvc-complex-type.3.2.2")
	vbCheck(t, "required attribute missing", s, `<d`+vbXSI+`><r xsi:nil="true"/></d>`, false, "cvc-complex-type.4")
	vbCheck(t, "attribute value", s, `<d`+vbXSI+`><r xsi:nil="true" req="notanint"/></d>`, false, "req")
	vbCheck(t, "duplicate ID", s, `<d`+vbXSI+`><r xsi:nil="true" req="1" id="x"/><r xsi:nil="true" req="1" id="x"/></d>`, false, "cvc-id.2")
	a := vbLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="A" abstract="true"><xs:sequence/></xs:complexType><xs:element name="r" type="A" nillable="true"/></xs:schema>`, Version11)
	vbCheck(t, "abstract type", a, `<r`+vbXSI+` xsi:nil="true"/>`, false, "cvc-type.2")
	as := vbLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r" nillable="true"><xs:complexType><xs:sequence><xs:element name="c"/></xs:sequence><xs:attribute name="a" type="xs:int"/><xs:assert test="@a = 1"/></xs:complexType></xs:element></xs:schema>`, Version11)
	vbCheck(t, "assertion holds", as, `<r`+vbXSI+` xsi:nil="true" a="1"/>`, true, "")
	vbCheck(t, "assertion fails", as, `<r`+vbXSI+` xsi:nil="true" a="2"/>`, false, "cvc-assertion")
}
