package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestRedefineExtendsOriginal covers the case that gives xs:redefine its
// difficulty: inside <xs:redefine>, a type whose base is itself means "derives
// from the definition being replaced", not "derives from itself".
//
// Without displacing the original the base resolves to the redefinition and
// the type derives from itself, which is an infinite base chain.
func TestRedefineExtendsOriginal(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:t" targetNamespace="urn:t">
		  <xs:redefine schemaLocation="base.xsd">
		    <xs:complexType name="person">
		      <xs:complexContent>
		        <xs:extension base="t:person">
		          <xs:sequence><xs:element name="email" type="xs:string"/></xs:sequence>
		        </xs:extension>
		      </xs:complexContent>
		    </xs:complexType>
		  </xs:redefine>
		  <xs:element name="root" type="t:person"/>
		</xs:schema>`,
		"base.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
		  <xs:complexType name="person">
		    <xs:sequence><xs:element name="name" type="xs:string"/></xs:sequence>
		  </xs:complexType>
		</xs:schema>`,
	}
	s, err := loadFromMap(t, "main.xsd", docs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// The redefined type carries both the original's content and the
	// extension's.
	doc := `<root xmlns="urn:t"><name xmlns="">a</name><email xmlns="">b</email></root>`
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(tree.Root, ValidateOptions{}); err != nil {
		t.Errorf("the redefined type should accept both elements:\n%v", err)
	}

	// The base chain must terminate: a type deriving from itself would
	// hang any walk up it.
	ct := s.Types[xdm.QName{URI: "urn:t", Local: "person"}].(*ComplexType)
	if ct.Base == ct {
		t.Error("the redefinition derives from itself; the original was not displaced")
	}
}

// TestRedefineSimpleTypeRestriction covers redefining a simple type, where the
// restriction's base is the type being redefined.
func TestRedefineSimpleTypeRestriction(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:t" targetNamespace="urn:t">
		  <xs:redefine schemaLocation="base.xsd">
		    <xs:simpleType name="code">
		      <xs:restriction base="t:code"><xs:maxLength value="3"/></xs:restriction>
		    </xs:simpleType>
		  </xs:redefine>
		  <xs:element name="root" type="t:code"/>
		</xs:schema>`,
		"base.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
		  <xs:simpleType name="code">
		    <xs:restriction base="xs:string"><xs:maxLength value="10"/></xs:restriction>
		  </xs:simpleType>
		</xs:schema>`,
	}
	s, err := loadFromMap(t, "main.xsd", docs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, c := range []struct {
		doc   string
		valid bool
	}{
		{`<root xmlns="urn:t">abc</root>`, true},
		// The redefinition narrows to 3, so the original's 10 no longer
		// admits this.
		{`<root xmlns="urn:t">abcdefg</root>`, false},
	} {
		tree, err := xdm.ParseString(c.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		err = s.Validate(tree.Root, ValidateOptions{})
		if (err == nil) != c.valid {
			t.Errorf("%s: valid=%v, want %v (%v)", c.doc, err == nil, c.valid, err)
		}
	}
}

// TestRedefineGroup covers redefining a model group that references itself,
// which clause 6.1 permits exactly once with minOccurs and maxOccurs of 1.
func TestRedefineGroup(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:t" targetNamespace="urn:t">
		  <xs:redefine schemaLocation="base.xsd">
		    <xs:group name="g">
		      <xs:sequence>
		        <xs:group ref="t:g"/>
		        <xs:element name="extra" type="xs:string"/>
		      </xs:sequence>
		    </xs:group>
		  </xs:redefine>
		  <xs:element name="root">
		    <xs:complexType><xs:group ref="t:g"/></xs:complexType>
		  </xs:element>
		</xs:schema>`,
		"base.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
		  <xs:group name="g">
		    <xs:sequence><xs:element name="first" type="xs:string"/></xs:sequence>
		  </xs:group>
		</xs:schema>`,
	}
	s, err := loadFromMap(t, "main.xsd", docs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tree, err := xdm.ParseString(
		`<root xmlns="urn:t"><first xmlns="">a</first><extra xmlns="">b</extra></root>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(tree.Root, ValidateOptions{}); err != nil {
		t.Errorf("the redefined group should accept both elements:\n%v", err)
	}
}

// TestRedefineTerminates guards against the group self-reference building a
// cycle that a content-model walk would not escape.
func TestRedefineTerminates(t *testing.T) {
	docs := map[string]string{
		"main.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:t" targetNamespace="urn:t">
		  <xs:redefine schemaLocation="base.xsd">
		    <xs:group name="g">
		      <xs:sequence><xs:group ref="t:g"/></xs:sequence>
		    </xs:group>
		  </xs:redefine>
		  <xs:element name="root">
		    <xs:complexType><xs:group ref="t:g"/></xs:complexType>
		  </xs:element>
		</xs:schema>`,
		"base.xsd": `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t">
		  <xs:group name="g">
		    <xs:sequence><xs:element name="a" type="xs:string"/></xs:sequence>
		  </xs:group>
		</xs:schema>`,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s, err := loadFromMap(t, "main.xsd", docs)
		if err != nil {
			return
		}
		tree, err := xdm.ParseString(`<root xmlns="urn:t"><a xmlns="">x</a></root>`,
			xdm.ParseOptions{})
		if err != nil {
			return
		}
		s.Validate(tree.Root, ValidateOptions{})
	}()
	select {
	case <-done:
	case <-timeoutAfterSecond():
		t.Fatal("a self-referential redefined group did not terminate")
	}
}
