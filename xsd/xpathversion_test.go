package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// assertSchema is a 1.1 schema whose single element carries one assertion.
func assertSchema(test string) string {
	return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:attribute name="a" type="xs:integer"/>
	      <xs:assert test="` + test + `"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`
}

func loadAssert(t *testing.T, test string, v xpath.Version) error {
	t.Helper()
	tree, err := xdm.ParseString(assertSchema(test), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Load(tree.Root, "s.xsd", Options{Version: Version11, XPathVersion: v})
	return err
}

// TestAssertionXPathVersionDefaultsTo20 is the conformance default: XSD 1.1
// defines assertions against a subset of XPath 2.0, so a schema using a 3.0
// construct is not portable and must not quietly work here.
func TestAssertionXPathVersionDefaultsTo20(t *testing.T) {
	for _, test := range []string{
		`let $x := @a return $x &gt; 0`,
		`[1,2]?1 = 1`,
		`map{'k':1}?k = 1`,
	} {
		if err := loadAssert(t, test, xpath.XPath20); err == nil {
			t.Errorf("the default accepted the 3.0 construct %q", test)
		}
	}
}

// TestAssertionXPathVersionRaised is the option: a host that controls its own
// schemas can have the later language in an assertion.
func TestAssertionXPathVersionRaised(t *testing.T) {
	for _, test := range []string{
		`let $x := @a return $x &gt; 0`,
		`[1,2]?1 = 1`,
		`map{'k':1}?k = 1`,
	} {
		if err := loadAssert(t, test, xpath.XPath31); err != nil {
			t.Errorf("XPath31 rejected %q: %v", test, err)
		}
	}
}

// TestAssertionXPathVersionEvaluates pins that a raised assertion is not
// merely compiled but evaluated in the same version — the function library
// has to match the parser, or a map constructor would parse and then find no
// map:get to call.
func TestAssertionXPathVersionEvaluates(t *testing.T) {
	// The map: prefix is bound explicitly: XSD has no predeclared prefixes,
	// which is right — predeclaring them is a rule of XSLT 3.0 section 3.1
	// and says nothing about a schema document.
	sheet := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	    xmlns:map="http://www.w3.org/2005/xpath-functions/map">
	  <xs:element name="e">
	    <xs:complexType>
	      <xs:attribute name="a" type="xs:integer"/>
	      <xs:assert test="map:get(map{'k':@a}, 'k') &gt; 0"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s, err := Load(tree.Root, "s.xsd", Options{
		Version: Version11, XPathVersion: xpath.XPath31})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, tc := range []struct {
		doc   string
		valid bool
	}{
		{`<e a="5"/>`, true},
		{`<e a="-1"/>`, false},
	} {
		inst, err := xdm.ParseString(tc.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse %s: %v", tc.doc, err)
		}
		err = s.Validate(inst.Root, ValidateOptions{})
		if got := err == nil; got != tc.valid {
			t.Errorf("%s: valid=%v, want %v (err %v)", tc.doc, got, tc.valid, err)
		}
	}
}
