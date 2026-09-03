package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Four walks over the schema graph used to stop at `depth > 32`, which
// conflated two different things: a graph that reaches itself, which must be
// stopped, and one that is merely deep, which must not. Cycle detection tells
// them apart; a depth bound cannot.
//
// What made this worth fixing rather than raising the constant is that three
// of the four returned a *definite answer* on running out of depth rather than
// a refusal, and in each case the answer was the permissive one. Raising 32 to
// 1024 moves the cliff without removing it.
//
// Both cases below are legal, acyclic, and rejected by the spec. They passed
// at nesting depth 31 and were accepted at 32.

// Element Declarations Consistent, the shape of the suite's saxonData wild068:
// a base type declares <e> as a date/time union, a derived type replaces it
// with a lax wildcard, and a global <e> of type xs:duration is matched through
// that wildcard. XSD 1.1 requires the mismatch to be caught.
//
// collectElementDecls gathers the base's declarations. Truncated, it returned
// an empty map, which reads as "the base declares nothing here" and skips the
// check entirely — a false accept.
func TestDepthAcyclicElementDeclsConsistent(t *testing.T) {
	for _, n := range []int{0, 31, 32, 33, 64} {
		open, close := strings.Repeat("<xs:sequence>", n), strings.Repeat("</xs:sequence>", n)
		src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
<xs:complexType name="zing"><xs:sequence>
  %s
  <xs:element name="e" minOccurs="0">
    <xs:simpleType><xs:union memberTypes="xs:date xs:time"/></xs:simpleType>
  </xs:element>
  <xs:element name="f" type="xs:integer"/>
  <xs:any namespace="##local" processContents="lax"/>
  %s
</xs:sequence></xs:complexType>
<xs:complexType name="zang"><xs:complexContent><xs:restriction base="zing">
  <xs:sequence>
    <xs:element name="f" type="xs:integer"/>
    <xs:any namespace="##local" processContents="lax"/>
  </xs:sequence>
</xs:restriction></xs:complexContent></xs:complexType>
<xs:element name="doc" type="zang"/>
<xs:element name="e" type="xs:duration"/>
</xs:schema>`, open, close)

		st, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("nesting %d: parse schema: %v", n, err)
		}
		s, err := Load(st.Root, "", Options{Version: Version11})
		if err != nil {
			t.Fatalf("nesting %d: load: %v", n, err)
		}
		doc, err := xdm.ParseString("<doc><f>42</f><e>PT12H</e></doc>", xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("nesting %d: parse doc: %v", n, err)
		}
		if err := s.Validate(doc.Root, ValidateOptions{}); err == nil {
			t.Errorf("nesting %d: accepted, want cvc-complex-type.2.4.k", n)
		}
	}
}

// cos-list-of-atomic: a list's item type may not itself be a list, and a union
// standing between them does not change that. nonAtomicUnionMember walks the
// union chain looking for the list; truncated, it returned nil, which is the
// same value as a clean result, so the ill-formed schema loaded without error.
func TestDepthAcyclicListOfAtomic(t *testing.T) {
	for _, n := range []int{2, 31, 32, 33, 64} {
		var b strings.Builder
		b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:t" targetNamespace="urn:t">`)
		for i := 0; i < n; i++ {
			next := fmt.Sprintf("t:u%d", i+1)
			if i == n-1 {
				next = "t:theList"
			}
			b.WriteString(fmt.Sprintf(`<xs:simpleType name="u%d"><xs:union memberTypes="%s"/></xs:simpleType>`, i, next))
		}
		b.WriteString(`<xs:simpleType name="theList"><xs:list itemType="xs:string"/></xs:simpleType>`)
		b.WriteString(`<xs:simpleType name="bad"><xs:list itemType="t:u0"/></xs:simpleType>`)
		b.WriteString(`<xs:element name="root" type="t:bad"/></xs:schema>`)

		st, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("chain %d: parse: %v", n, err)
		}
		if _, err := Load(st.Root, "", Options{}); err == nil {
			t.Errorf("chain %d: schema loaded, want cos-list-of-atomic", n)
		}
	}
}

// A union chain that reaches itself must still terminate rather than hang,
// which is what the depth bound was there for in the first place.
func TestDepthCyclicUnionTerminates(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:t" targetNamespace="urn:t">
  <xs:simpleType name="a"><xs:union memberTypes="t:b"/></xs:simpleType>
  <xs:simpleType name="b"><xs:union memberTypes="t:a"/></xs:simpleType>
  <xs:element name="root" type="t:a"/>
</xs:schema>`
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Either verdict is acceptable; hanging is not.
	_, _ = Load(st.Root, "", Options{})
}
