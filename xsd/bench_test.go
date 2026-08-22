package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// XSD benchmarks over a schema shaped like the ones this is used on: a
// sequence of repeating records, each with attributes, simple-typed leaves and
// a facet or two.
//
// They are self-contained so they run on a fresh clone. The production corpora
// are larger and are covered by the xslt package's benchmarks, which validate
// against UBL and CII.

const benchSchemaSrc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    elementFormDefault="qualified">
  <xs:element name="root">
    <xs:complexType>
      <xs:sequence>
        <xs:element name="item" maxOccurs="unbounded">
          <xs:complexType>
            <xs:sequence>
              <xs:element name="name" type="xs:string"/>
              <xs:element name="value" type="Amount"/>
              <xs:element name="note" type="xs:string" minOccurs="0"
                          maxOccurs="unbounded"/>
            </xs:sequence>
            <xs:attribute name="id" type="xs:ID" use="required"/>
            <xs:attribute name="kind" type="Kind" default="a"/>
          </xs:complexType>
        </xs:element>
      </xs:sequence>
    </xs:complexType>
  </xs:element>
  <xs:simpleType name="Amount">
    <xs:restriction base="xs:decimal">
      <xs:minInclusive value="0"/>
      <xs:fractionDigits value="2"/>
    </xs:restriction>
  </xs:simpleType>
  <xs:simpleType name="Kind">
    <xs:restriction base="xs:token">
      <xs:enumeration value="a"/>
      <xs:enumeration value="b"/>
      <xs:enumeration value="c"/>
    </xs:restriction>
  </xs:simpleType>
</xs:schema>`

func benchSchema(b *testing.B) *Schema {
	b.Helper()
	tree, err := xdm.ParseString(benchSchemaSrc, xdm.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	s, err := Load(tree.Root, "", Options{})
	if err != nil {
		b.Fatal(err)
	}
	return s
}

func benchInstance(b *testing.B, n int) *xdm.Node {
	b.Helper()
	var sb strings.Builder
	sb.WriteString(`<root>`)
	for i := 0; i < n; i++ {
		sb.WriteString(`<item id="i`)
		sb.WriteString(benchItoa(i))
		sb.WriteString(`" kind="b"><name>a name</name><value>12.34</value>` +
			`<note>x</note></item>`)
	}
	sb.WriteString(`</root>`)
	tree, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	return tree.Root
}

func benchItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func BenchmarkValidateInstance(b *testing.B) {
	s := benchSchema(b)
	doc := benchInstance(b, 500)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Validate(doc, ValidateOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

// Loading builds the component model and runs the schema-validity checks,
// which is where a service spends its startup. A caller loads once and
// validates many times.
func BenchmarkLoadSchema(b *testing.B) {
	tree, err := xdm.ParseString(benchSchemaSrc, xdm.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Load(tree.Root, "", Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// The 1.1 path shares the validator and differs in the schema-validity rules,
// so this should track the 1.0 figure closely.
func BenchmarkLoadSchema11(b *testing.B) {
	tree, err := xdm.ParseString(benchSchemaSrc, xdm.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Load(tree.Root, "", Options{Version: Version11}); err != nil {
			b.Fatal(err)
		}
	}
}
