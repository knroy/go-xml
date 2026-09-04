package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A key sequence that two or more sibling subtrees each define is ambiguous in
// the scope that contains them: an ancestor's keyref cannot say which of them
// it resolves to, so it must resolve to none.
//
// Deleting the entry on the first clash is not enough to record that, because
// a key is absent both before it is first seen and after it has been dropped.
// The merge could not tell those apart, so a third sibling found nothing there
// and put the key back. The result oscillated with the sibling count — wrong
// at three and five, right at two and four — and the wrong direction was
// acceptance: a keyref resolved against a key no reading of the document makes
// unique.
//
// nodeTable.ambiguous makes the third state explicit, and mergeEntry is the
// single path everything folds through so the states cannot drift apart again.
func TestMergeAmbiguityIsTerminal(t *testing.T) {
	ic := &IdentityConstraint{Kind: ICKey, Name: xdm.QName{Local: "k"}}
	sibling := func(tag string) icTables {
		n := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: tag}}
		return icTables{ic: &nodeTable{
			entries: map[string]*xdm.Node{"A": n},
			targets: map[*xdm.Node]string{n: "A"},
		}}
	}
	for _, count := range []int{1, 2, 3, 4, 5, 6, 7} {
		var kids []icTables
		for i := 0; i < count; i++ {
			kids = append(kids, sibling(fmt.Sprintf("c%d", i)))
		}
		tbl := mergeTables(kids)[ic]
		_, resolvable := tbl.entries["A"]
		if want := count == 1; resolvable != want {
			t.Errorf("%d siblings defining A: resolvable=%v want %v", count, resolvable, want)
		}
		assertTableInvariant(t, fmt.Sprintf("%d siblings", count), tbl)
	}
}

// assertTableInvariant states the relationship the two maps must keep: if two
// distinct nodes produced the same sequence anywhere in the subtree, that
// sequence cannot still be resolvable. Checking it directly is cheaper than
// discovering the violation through a validity verdict several layers away.
func assertTableInvariant(t *testing.T, where string, tbl *nodeTable) {
	t.Helper()
	if tbl == nil {
		return
	}
	count := map[string]int{}
	for _, k := range tbl.targets {
		count[k]++
	}
	for k, n := range count {
		if n < 2 {
			continue
		}
		if _, resolvable := tbl.entries[k]; resolvable {
			t.Errorf("%s: %d targets produced sequence %q, but it is still resolvable",
				where, n, k)
		}
	}
}

// The same thing through the validator: each <section> is its own key scope, so
// a repeated id is not a duplicate, but at the root the merged table sees the
// sequence from every section. One section makes it resolvable; two or more
// make it ambiguous and the keyref must fail.
func TestMergeAmbiguityReachesAKeyref(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element ref="section" minOccurs="0" maxOccurs="unbounded"/>
      <xs:element name="use" minOccurs="0">
        <xs:complexType><xs:attribute name="ref" type="xs:string"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:keyref name="kr" refer="k"><xs:selector xpath=".//use"/><xs:field xpath="@ref"/></xs:keyref>
  </xs:element>
  <xs:element name="section">
    <xs:complexType><xs:sequence>
      <xs:element name="item" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//item"/><xs:field xpath="@id"/></xs:key>
  </xs:element>
</xs:schema>`
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, n := range []int{1, 2, 3, 4, 5, 6, 7} {
		var b strings.Builder
		b.WriteString("<root>")
		for i := 0; i < n; i++ {
			b.WriteString(`<section><item id="A"/></section>`)
		}
		b.WriteString(`<use ref="A"/></root>`)
		tr, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		invalid := s.Validate(tr.Root, ValidateOptions{}) != nil
		if want := n >= 2; invalid != want {
			t.Errorf("%d sections defining A: invalid=%v want %v", n, invalid, want)
		}
	}
}
