package xsd

import (
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A key scope rebuilt its entries, and sorted its targets to do it, from the
// whole subtree at every level of a recursive document: 960 levels with two
// leaves each allocated 84 MB, and a 258 KB document 730 MB. With no sequence
// held twice the scope extends the entries it was handed, so the cost per
// level is the gap to the next scope. Measured: 1 MB here.
func TestKeyScopeDoesNotRebuildEntriesPerLevel(t *testing.T) {
	st, err := xdm.ParseString(identityBenchSchema(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := xdm.ParseString(identityBenchDoc(960, 2), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var verr error
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	verr = s.Validate(tr.Root, ValidateOptions{})
	runtime.ReadMemStats(&m1)
	if verr != nil {
		t.Fatal(verr)
	}
	if got := m1.TotalAlloc - m0.TotalAlloc; got > 16<<20 {
		t.Errorf("validation allocated %d MB, want under 16", got>>20)
	}
}

// A sequence two sibling scopes both hold is a duplicate in the scope that
// contains them, and is reported again by every scope above, so the fast path
// above must not apply once a table holds one.
func TestKeyScopeSiblingDuplicateStillReportedPerScope(t *testing.T) {
	const sch = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root"><xs:complexType><xs:sequence><xs:element ref="r"/></xs:sequence></xs:complexType></xs:element>
  <xs:element name="r"><xs:complexType><xs:sequence>
      <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded"><xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType></xs:element>
      <xs:element ref="r" minOccurs="0" maxOccurs="unbounded"/>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@id"/></xs:key>
  </xs:element></xs:schema>`
	st, err := xdm.ParseString(sch, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	tr, _ := xdm.ParseString("<root><r><r><r><leaf id='a'/></r><r><leaf id='a'/></r></r></r></root>", xdm.ParseOptions{})
	err = s.Validate(tr.Root, ValidateOptions{MaxErrors: -1})
	if err == nil {
		t.Fatal("duplicate key accepted")
	}
	if n := strings.Count(err.Error(), "duplicate key sequence"); n != 2 {
		t.Errorf("%d duplicate reports, want 2 (the scope holding both, and its parent): %v", n, err)
	}
}
