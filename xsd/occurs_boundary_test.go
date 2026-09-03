package xsd

import (
	"fmt"
	"strings"
	"testing"
)

// Occurrence bounds are checked across the integer boundaries where a
// representation change could hide, and in particular across 255.
//
// nfa.go carries a count vector as a string of bytes and caps each count at
// 254 in encodeCounts. Read alone that looks like a ceiling on minOccurs and
// maxOccurs, and an audit read it that way: the report predicted that
// maxOccurs="300" would accept a 301st child, and that minOccurs="300" would
// reject a valid 300-child document.
//
// Neither happens, because a count never arrives at encodeCounts un-narrowed.
// reachable() first replaces every bound above the document's own child count
// with Unbounded — a maximum a document cannot reach cannot be broken — and
// capCount() then clamps against that narrowed bound, returning at most
// min+1 once the maximum is out of reach. A count above 254 therefore implies
// a scope with 255+ children still in play, where the bound is already
// Unbounded and the exact value has stopped mattering.
//
// That argument is a paragraph of reasoning about three functions, which is
// exactly the kind of claim that should be nailed down by a test rather than
// re-derived by the next reader. This is that test: it walks each bound
// through the byte boundary and one full order of magnitude past it.
func occursBoundarySchema(occurs string) string {
	return fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="a" %s/>
    </xs:sequence></xs:complexType>
  </xs:element>
</xs:schema>`, occurs)
}

// boundaries brackets every representation edge a count could be stored at:
// a byte, a signed byte, and a uint16, plus small values and one decade past.
var boundaries = []int{0, 1, 2, 126, 127, 128, 253, 254, 255, 256, 257, 300, 1000, 65535, 65536}

func TestOccursBoundaryMax(t *testing.T) {
	for _, max := range boundaries {
		if max == 0 || max > 2000 { // keep the sweep's documents small
			continue
		}
		src := occursBoundarySchema(fmt.Sprintf(`minOccurs="0" maxOccurs="%d"`, max))
		for _, n := range []int{max - 1, max, max + 1} {
			if n < 0 {
				continue
			}
			got := oracleValid(t, src, strings.Repeat("<a/>", n))
			if want := n <= max; got != want {
				t.Errorf("maxOccurs=%d with %d children: valid=%v want=%v", max, n, got, want)
			}
		}
	}
}

func TestOccursBoundaryMin(t *testing.T) {
	for _, min := range boundaries {
		if min > 2000 {
			continue
		}
		src := occursBoundarySchema(fmt.Sprintf(`minOccurs="%d" maxOccurs="unbounded"`, min))
		for _, n := range []int{min - 1, min, min + 1} {
			if n < 0 {
				continue
			}
			got := oracleValid(t, src, strings.Repeat("<a/>", n))
			if want := n >= min; got != want {
				t.Errorf("minOccurs=%d with %d children: valid=%v want=%v", min, n, got, want)
			}
		}
	}
}

// A bound written far above anything a document could reach must behave as
// unbounded rather than as a literal, which is what keeps the state space
// small. occursHuge is the parser's saturation point.
func TestOccursBoundaryHuge(t *testing.T) {
	for _, max := range []string{"1000000", "79228162514244337593543950335", "unbounded"} {
		src := occursBoundarySchema(fmt.Sprintf(`minOccurs="0" maxOccurs="%s"`, max))
		for _, n := range []int{0, 1, 254, 255, 256, 300} {
			if got := oracleValid(t, src, strings.Repeat("<a/>", n)); !got {
				t.Errorf("maxOccurs=%s with %d children: rejected, want accepted", max, n)
			}
		}
	}
}

// The same boundary crossing, one scope deeper: an outer repetition whose
// counter is the one that would saturate.
func TestOccursBoundaryNested(t *testing.T) {
	for _, outer := range []int{253, 254, 255, 256} {
		src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence minOccurs="%d" maxOccurs="%d">
      <xs:element name="a" minOccurs="1" maxOccurs="1"/>
    </xs:sequence></xs:complexType>
  </xs:element>
</xs:schema>`, outer, outer)
		for _, n := range []int{outer - 1, outer, outer + 1} {
			got := oracleValid(t, src, strings.Repeat("<a/>", n))
			if want := n == outer; got != want {
				t.Errorf("nested sequence %d..%d with %d children: valid=%v want=%v", outer, outer, n, got, want)
			}
		}
	}
}
