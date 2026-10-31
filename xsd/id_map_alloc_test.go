package xsd

import (
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// Each ID value is recorded in one map. It was two with the same keys, an
// owner map and a count map, which on a document of nothing but IDs was most
// of what validation allocated: 20,000 IDs took 3.50 MB with two maps and
// 2.63 MB with one. Map growth is deterministic, so the bound sits between.
func TestIDValuesRecordedOnce(t *testing.T) {
	s := mustParseSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="r"><xs:complexType><xs:sequence>
	    <xs:element name="e" maxOccurs="unbounded">
	      <xs:complexType><xs:attribute name="id" type="xs:ID"/></xs:complexType>
	    </xs:element>
	  </xs:sequence></xs:complexType></xs:element>
	</xs:schema>`)
	var b strings.Builder
	b.WriteString("<r>")
	for i := 0; i < 20000; i++ {
		b.WriteString(`<e id="i` + benchItoa(i) + `"/>`)
	}
	b.WriteString("</r>")
	tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	err = s.Validate(tree.Root, ValidateOptions{})
	runtime.ReadMemStats(&m1)
	if err != nil {
		t.Fatal(err)
	}
	if got := m1.TotalAlloc - m0.TotalAlloc; got > 3_000_000 {
		t.Errorf("validating 20,000 IDs allocated %d bytes, want at most 3,000,000", got)
	}
}
