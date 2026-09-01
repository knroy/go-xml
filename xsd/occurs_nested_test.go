package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A repeated group whose only child is itself repeating gets its occurrence
// bounds wrong in both directions.
//
// <sequence minOccurs="M" maxOccurs="N"> over <element c minOccurs="p"
// maxOccurs="q"/> admits exactly the totals in [M*p, N*q] that can be written
// as a sum of N' counts each in [p, q] for some N' in [M, N]. The validator
// instead brackets the repetition count into a low and a high reading and
// tests each bound against a different one, so a document is admitted when no
// single consistent reading admits it, and refused when one does.
//
// counterAllows reads the low count while countersSatisfied reads the high
// one; the two cannot be reconciled locally, because the group's FIRST and
// LAST positions coincide when it holds a single particle, which makes the
// wraparound edge indistinguishable from the inner element's own repeat edge.
// A correct fix tracks the set of reachable count-vectors — bounded by
// maxOccurs, so it stays small — and accepts only if some vector satisfies
// every bound.
//
// The false-accept direction is the more serious: a minOccurs floor is
// silently not enforced. Predates the dual-count work (verified at 06e8a75),
// which documented the false-reject half of this and not the other.
//
// A group with two or more distinct child names is unaffected, which is why
// no case in either W3C suite covers this.
func TestNestedOccursBoundsAreWrong(t *testing.T) {
	t.Skip("known gap: see comment; nested occurrence bounds are wrong in both directions")

	valid := func(oMin, oMax, iMin, iMax string, n int) bool {
		src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root"><xs:complexType>
    <xs:sequence minOccurs="%s" maxOccurs="%s">
      <xs:element name="c" type="xs:string" minOccurs="%s" maxOccurs="%s"/>
    </xs:sequence>
  </xs:complexType></xs:element></xs:schema>`, oMin, oMax, iMin, iMax)
		st, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Load(st.Root, "", Options{})
		if err != nil {
			t.Fatal(err)
		}
		doc, err := xdm.ParseString("<root>"+strings.Repeat("<c>x</c>", n)+"</root>",
			xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return s.Validate(doc.Root, ValidateOptions{}) == nil
	}

	// <sequence 5..5> over c{2,2}: ten is the only valid document.
	for n := 0; n <= 12; n++ {
		if got, want := valid("5", "5", "2", "2", n), n == 10; got != want {
			t.Errorf("seq{5,5} over c{2,2}, %d c: got valid=%v want %v", n, got, want)
		}
	}
	// <sequence 2..2> over c{2,4}: four through eight.
	for n := 0; n <= 10; n++ {
		if got, want := valid("2", "2", "2", "4", n), n >= 4 && n <= 8; got != want {
			t.Errorf("seq{2,2} over c{2,4}, %d c: got valid=%v want %v", n, got, want)
		}
	}
	// <sequence 1..unbounded> over c{2,2}: any even count from two.
	for n := 0; n <= 8; n++ {
		if got, want := valid("1", "unbounded", "2", "2", n), n >= 2 && n%2 == 0; got != want {
			t.Errorf("seq{1,unbounded} over c{2,2}, %d c: got valid=%v want %v", n, got, want)
		}
	}
}
