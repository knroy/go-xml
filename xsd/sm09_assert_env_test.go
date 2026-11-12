package xsd

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestSM09AssertionScopeKeepsSchemaTypeEnv: the assertion scope copy answers
// "instance of" from the validating schema's derivations, not from the
// process-wide table a later schema with the same type name overwrites.
func TestSM09AssertionScopeKeepsSchemaTypeEnv(t *testing.T) {
	load := func(src string, v Version) *Schema {
		t.Helper()
		d, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Load(d.Root, "", Options{Version: v})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	mk := func(test string) *Schema {
		return load(`<xs:schema xmlns:xs='http://www.w3.org/2001/XMLSchema' xmlns:x='urn:sm09:env' targetNamespace='urn:sm09:env'>`+
			`<xs:simpleType name='T'><xs:restriction base='xs:int'/></xs:simpleType>`+
			`<xs:element name='r'><xs:complexType><xs:attribute name='a' type='x:T'/><xs:assert test='`+test+`'/></xs:complexType></xs:element></xs:schema>`, Version11)
	}
	pos := mk("data(@a) instance of xs:int")
	neg := mk("not(data(@a) instance of xs:int)")
	// A later schema registers the same {urn:sm09:env}T as a string.
	load(`<xs:schema xmlns:xs='http://www.w3.org/2001/XMLSchema' targetNamespace='urn:sm09:env'>`+
		`<xs:simpleType name='T'><xs:restriction base='xs:string'/></xs:simpleType></xs:schema>`, Version10)

	check := func(s *Schema, wantValid bool) {
		t.Helper()
		d, _ := xdm.ParseString(`<r xmlns='urn:sm09:env' a='5'/>`, xdm.ParseOptions{})
		if err := s.Validate(d.Root, ValidateOptions{}); (err == nil) != wantValid {
			t.Errorf("err=%v, want valid=%v", err, wantValid)
		}
	}
	check(pos, true)
	check(neg, false)
}
