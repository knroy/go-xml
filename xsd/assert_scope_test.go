package xsd

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestCheckOnlyTypingOnlyUnderAssertions: a check-only run holds typing aside
// for an assertion's copy to read, and keeps none where no assertion is in
// scope. TestCheckOnlyAssertionSeesUnwrittenTyping checks what is kept.
func TestCheckOnlyTypingOnlyUnderAssertions(t *testing.T) {
	s := loadAssertionSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:simpleType name="u"><xs:union memberTypes="xs:integer xs:NCName"/></xs:simpleType>
  <xs:element name="r"><xs:complexType><xs:sequence>
    <xs:element name="free" maxOccurs="unbounded"><xs:complexType><xs:attribute name="a" type="u"/></xs:complexType></xs:element>
    <xs:element name="held"><xs:complexType><xs:sequence>
      <xs:element name="i"><xs:complexType><xs:attribute name="a" type="u"/></xs:complexType></xs:element>
    </xs:sequence><xs:assert test="true()"/></xs:complexType></xs:element>
  </xs:sequence></xs:complexType></xs:element>
</xs:schema>`)
	tree, err := xdm.ParseString(`<r><free a="1"/><free a="x"/><held><i a="2"/></held></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v := s.newValidator(nil, ValidateOptions{})
	if err := v.run(tree.Root); err != nil {
		t.Fatal(err)
	}
	if len(v.typing) != 1 {
		t.Errorf("typing held for %d nodes, want 1 (the attribute under the assertion)", len(v.typing))
	}
}
