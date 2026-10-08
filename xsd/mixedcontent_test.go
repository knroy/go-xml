package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestValidateRecordsMixedContent pins the MixedContent property the
// serializers read for Serialization 3.1 §5.1.4: set for an element of a
// mixed type, even an anonymous one that annotates as "anyType", and unset
// for element-only content and for a genuine xs:anyType element, whose
// content the same section lets the serializer indent.
func TestValidateRecordsMixedContent(t *testing.T) {
	s := loadProbe(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="r"><xs:complexType><xs:sequence>
    <xs:element name="m"><xs:complexType mixed="true"><xs:sequence>
      <xs:element name="b" type="xs:string"/></xs:sequence></xs:complexType></xs:element>
    <xs:element name="k" type="xs:anyType"/>
  </xs:sequence></xs:complexType></xs:element>
</xs:schema>`)
	tree, err := xdm.ParseString(`<r><m> <b>x</b> </m><k><c/></k></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(tree.Root, ValidateOptions{Annotate: true}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r := tree.Root.Children[0]
	m, k := r.Children[0], r.Children[1]
	if r.MixedContent || !r.NoTypedValue {
		t.Errorf("r (element-only): MixedContent=%v NoTypedValue=%v", r.MixedContent, r.NoTypedValue)
	}
	if !m.MixedContent || m.TypeAnnotation != "anyType" {
		t.Errorf("m (anonymous mixed): MixedContent=%v annotation=%q", m.MixedContent, m.TypeAnnotation)
	}
	if k.MixedContent {
		t.Errorf("k (xs:anyType): MixedContent=true")
	}
}
