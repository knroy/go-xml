package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestWildcardEmptyNamespaceList covers the difference between a namespace
// attribute that is absent and one that is present but empty.
//
// §3.10.2 defaults {namespace constraint} to ##any only when the attribute is
// *absent*. namespace="" is a present xs:namespaceList with no members, so it
// denotes the empty set and matches nothing. The two cannot share a code path:
// defaulting the empty string to ##any turns a wildcard that admits no element
// into one that admits every element, which is a false accept rather than a
// cosmetic difference.
//
// wildZ010 is the suite case — the TSTF ruled its instance invalid because "no
// defaulting of the empty string to ##any is licensed by the spec" — and its
// sibling shape, a wildcard with no namespace attribute at all, is what must
// keep working.
func TestWildcardEmptyNamespaceList(t *testing.T) {
	validate := func(t *testing.T, schema, instance string) error {
		t.Helper()
		s := mustParseSchema(t, schema)
		tree, err := xdm.ParseString(instance, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing the instance: %v", err)
		}
		return s.Validate(tree.Root, ValidateOptions{})
	}

	// namespace="" admits nothing, so an element in any namespace fails.
	t.Run("empty list matches no namespace", func(t *testing.T) {
		err := validate(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           targetNamespace="ns-a" xmlns="ns-a"
		           elementFormDefault="qualified">
		  <xs:element name="doc">
		    <xs:complexType>
		      <xs:sequence>
		        <xs:any namespace="" processContents="lax"/>
		      </xs:sequence>
		    </xs:complexType>
		  </xs:element>
		</xs:schema>`, `<a:doc xmlns:a="ns-a" xmlns="blah"><a/></a:doc>`)
		if err == nil {
			t.Error(`namespace="" admitted an element: the empty list was defaulted to ##any`)
		}
	})

	// The same shape with no namespace attribute keeps the ##any default,
	// which is the behaviour the fix must not disturb.
	t.Run("absent attribute still defaults to ##any", func(t *testing.T) {
		err := validate(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           targetNamespace="ns-a" xmlns="ns-a"
		           elementFormDefault="qualified">
		  <xs:element name="doc">
		    <xs:complexType>
		      <xs:sequence>
		        <xs:any processContents="lax"/>
		      </xs:sequence>
		    </xs:complexType>
		  </xs:element>
		</xs:schema>`, `<a:doc xmlns:a="ns-a" xmlns="blah"><a/></a:doc>`)
		if err != nil {
			t.Errorf("an absent namespace attribute stopped defaulting to ##any: %v", err)
		}
	})

	// An all-whitespace value is still a present list with no members: the
	// list type collapses the whitespace away, leaving the empty set rather
	// than restoring the absent-attribute default.
	t.Run("whitespace-only list matches no namespace", func(t *testing.T) {
		err := validate(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           targetNamespace="ns-a" xmlns="ns-a"
		           elementFormDefault="qualified">
		  <xs:element name="doc">
		    <xs:complexType>
		      <xs:sequence>
		        <xs:any namespace="   " processContents="lax"/>
		      </xs:sequence>
		    </xs:complexType>
		  </xs:element>
		</xs:schema>`, `<a:doc xmlns:a="ns-a" xmlns="blah"><a/></a:doc>`)
		if err == nil {
			t.Error(`namespace="   " admitted an element: whitespace was read as an absent attribute`)
		}
	})
}
