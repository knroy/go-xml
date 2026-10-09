package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// cvc-id.2 failures are reported in the order the second definition of each
// value appears in the document, where a streaming validator meets them, and
// MaxErrors keeps the first of that order. Twenty duplicated IDs reported in
// map order would come out shuffled on almost every one of twenty runs.
func TestDuplicateIDsReportedInDocumentOrder(t *testing.T) {
	s := mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="item" maxOccurs="unbounded">
	          <xs:complexType><xs:attribute name="id" type="xs:ID"/></xs:complexType>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`)
	var doc, want strings.Builder
	doc.WriteString("<root>")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&doc, `<item id="d%d"/>`, i)
	}
	// Second definitions in reverse, then a third d5: the report follows the
	// second definitions, and d5's count is its final one.
	for i := 19; i >= 0; i-- {
		fmt.Fprintf(&doc, `<item id="d%d"/>`, i)
		n := 2
		if i == 5 {
			n = 3
		}
		fmt.Fprintf(&want, "\n  /: cvc-id.2: ID value \"d%d\" is defined %d times", i, n)
	}
	doc.WriteString(`<item id="d5"/></root>`)
	tree, err := xdm.ParseString(doc.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	all := "20 validation errors:" + want.String()
	for i := 0; i < 20; i++ {
		if err := s.Validate(tree.Root, ValidateOptions{MaxErrors: -1}); err == nil || err.Error() != all {
			t.Fatalf("run %d: got\n%v\nwant\n%s", i, err, all)
		}
		const first = `/: cvc-id.2: ID value "d19" is defined 2 times`
		if err := s.Validate(tree.Root, ValidateOptions{MaxErrors: 1}); err == nil || err.Error() != first {
			t.Fatalf("run %d, MaxErrors 1: got %v, want %s", i, err, first)
		}
	}
}
