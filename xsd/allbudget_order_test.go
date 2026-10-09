package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A restriction of an all group naming several elements the base does not
// allow reports the least by name on every load. Twenty such names; ranging
// the branch's name map reported any of them.
func TestAllRestrictionUnknownNameReportedInNameOrder(t *testing.T) {
	var els strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&els, `<xs:element name="z%02d" minOccurs="0" maxOccurs="1"/>`, i)
	}
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="b">
	    <xs:all><xs:element name="a" minOccurs="0" maxOccurs="10"/><xs:element name="pad" minOccurs="0"/></xs:all>
	  </xs:complexType>
	  <xs:complexType name="r">
	    <xs:complexContent><xs:restriction base="b"><xs:all>` + els.String() + `</xs:all></xs:restriction></xs:complexContent>
	  </xs:complexType>
	</xs:schema>`
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const want = "element z00 may occur in the restriction but the base's all group does not allow it"
	for i := 0; i < 20; i++ {
		_, err := Load(tree.Root, "s.xsd", Options{Version: Version11})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("load %d: got %v, want %q", i, err, want)
		}
	}
}
