package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Nested occurrence bounds must be decided by one coherent reading of the
// content, not by two separately-valid ones.
//
// <sequence minOccurs="M" maxOccurs="N"> over <element c minOccurs="p"
// maxOccurs="q"/> admits exactly the totals that can be written as a sum of N'
// counts each in [p, q] for some N' in [M, N]. The matcher used to bracket the
// repetition count into a low and a high reading and test each bound against a
// different one, so it admitted a document when no single consistent reading
// admitted it, and refused one when a consistent reading existed.
//
// The witness is <sequence 5..5> over c{2,2}: ten c is the only valid document
// and it was refused, while five c — which no reading admits — was accepted, so
// a minOccurs floor went silently unenforced. The false-accept direction is the
// serious one for a validator anybody relies on.
//
// The matcher now carries a set of whole count vectors, so every bound is
// answered from the same execution. See nfa.go.
//
// A group with two or more distinct child names was decided correctly all
// along, which is why no case in either W3C suite covers this and why the bug
// survived 39,347 XSD 1.0 agreements and 41,532 on 1.1.
func TestNestedOccursBounds(t *testing.T) {
	valid := func(t *testing.T, body string, doc string) bool {
		t.Helper()
		src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root"><xs:complexType>%s</xs:complexType></xs:element></xs:schema>`, body)
		st, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Load(st.Root, "", Options{})
		if err != nil {
			t.Fatal(err)
		}
		d, err := xdm.ParseString("<root>"+doc+"</root>", xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return s.Validate(d.Root, ValidateOptions{}) == nil
	}
	cs := func(n int) string { return strings.Repeat("<c>x</c>", n) }

	// A repeated group whose only child repeats. The two scopes' FIRST and
	// LAST positions coincide, so the wraparound edge and the element's own
	// repeat edge are the same edge, and which one a step took is decided
	// only by the rest of the input.
	t.Run("single-child", func(t *testing.T) {
		cases := []struct {
			name   string
			body   string
			counts []int
			want   func(n int) bool
		}{{
			// The headline case. Ten is the only valid document:
			// five repetitions of the sequence, two c in each.
			name: "seq5x5 over c2x2",
			body: `<xs:sequence minOccurs="5" maxOccurs="5">
			  <xs:element name="c" type="xs:string" minOccurs="2" maxOccurs="2"/>
			</xs:sequence>`,
			counts: []int{0, 4, 5, 9, 10, 11, 20},
			want:   func(n int) bool { return n == 10 },
		}, {
			// A range on both scopes: two repetitions of two to
			// four c is four through eight, and every answer in
			// between was inverted.
			name: "seq2x2 over c2x4",
			body: `<xs:sequence minOccurs="2" maxOccurs="2">
			  <xs:element name="c" type="xs:string" minOccurs="2" maxOccurs="4"/>
			</xs:sequence>`,
			counts: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			want:   func(n int) bool { return n >= 4 && n <= 8 },
		}, {
			// The nested unbounded case: any even count from two,
			// and nothing odd. Odd counts were admitted.
			name: "seq1xN over c2x2",
			body: `<xs:sequence minOccurs="1" maxOccurs="unbounded">
			  <xs:element name="c" type="xs:string" minOccurs="2" maxOccurs="2"/>
			</xs:sequence>`,
			counts: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 20, 21},
			want:   func(n int) bool { return n >= 2 && n%2 == 0 },
		}, {
			// An unbounded scope inside a bounded one: at most
			// three repetitions of three or more c, so nine is the
			// floor and there is no ceiling.
			name: "seq3x3 over c3xN",
			body: `<xs:sequence minOccurs="3" maxOccurs="3">
			  <xs:element name="c" type="xs:string" minOccurs="3" maxOccurs="unbounded"/>
			</xs:sequence>`,
			counts: []int{0, 8, 9, 10, 30},
			want:   func(n int) bool { return n >= 9 },
		}}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, n := range tc.counts {
					if got, want := valid(t, tc.body, cs(n)), tc.want(n); got != want {
						t.Errorf("%d c: valid=%v want %v", n, got, want)
					}
				}
			})
		}
	})

	// A choice inside a repeating sequence. Each repetition contributes
	// exactly two children, one branch or the other, so the total is even
	// and between four and six — and which branch each pair took must not
	// change the count.
	t.Run("choice-in-repeating-sequence", func(t *testing.T) {
		body := `<xs:sequence minOccurs="2" maxOccurs="3">
		  <xs:choice>
		    <xs:sequence>
		      <xs:element name="a" type="xs:string"/>
		      <xs:element name="b" type="xs:string"/>
		    </xs:sequence>
		    <xs:sequence>
		      <xs:element name="c" type="xs:string"/>
		      <xs:element name="d" type="xs:string"/>
		    </xs:sequence>
		  </xs:choice>
		</xs:sequence>`
		cases := []struct {
			doc  string
			want bool
		}{
			{"", false},
			{"<a/><b/>", false},
			{"<a/><b/><a/><b/>", true},
			{"<a/><b/><c/><d/>", true},
			{"<c/><d/><a/><b/>", true},
			{"<a/><b/><c/><d/><a/><b/>", true},
			{"<a/><b/><c/><d/><a/><b/><c/><d/>", false},
			{"<a/><b/><a/>", false},
			{"<a/><c/>", false},
		}
		for _, tc := range cases {
			if got := valid(t, body, tc.doc); got != tc.want {
				t.Errorf("%q: valid=%v want %v", tc.doc, got, tc.want)
			}
		}
	})

	// The particlesZ040 shape: a repeating sequence whose members are all
	// optional, with an unbounded wildcard between them. Every position is
	// both a FIRST and a LAST of the outer scope, so every wildcard step
	// reads as a restart — which is what drove the outer count far past its
	// maximum and refused a legal document. Twenty-three children against a
	// maximum of three is the suite's own case.
	t.Run("wildcard-beside-repeating-scope", func(t *testing.T) {
		body := `<xs:sequence maxOccurs="3">
		  <xs:element name="a" type="xs:string" minOccurs="0"/>
		  <xs:any namespace="##other" minOccurs="0" maxOccurs="unbounded"
		          processContents="skip"/>
		  <xs:element name="b" type="xs:string" minOccurs="0"/>
		</xs:sequence>`
		q := func(n int) string { return strings.Repeat(`<x:q xmlns:x="o"/>`, n) }
		cases := []struct {
			name string
			doc  string
			want bool
		}{
			// One repetition, however many wildcards it holds: the
			// wildcard is unbounded, so no number of them spends
			// the sequence's own maximum.
			{"one iteration, three wildcards", "<a/>" + q(3) + "<b/>", true},
			{"one iteration, twenty wildcards", "<a/>" + q(20) + "<b/>", true},
			// Two iterations, the second beginning on a wildcard.
			{"two iterations", "<a/>" + q(2) + "<b/>" + q(2), true},
			// b once per iteration and three iterations, so three
			// is the ceiling. The wildcards are what let a fourth
			// look plausible: the outer scope can restart on a
			// wildcard step, and reading every one of them as a
			// restart is how the count ran away.
			{"three b", "<b/><b/><b/>", true},
			{"four b", "<b/><b/><b/><b/>", false},
			{"three b around wildcards", "<b/>" + q(2) + "<b/>" + q(2) + "<b/>", true},
			{"four b around wildcards", "<b/>" + q(2) + "<b/>" + q(2) + "<b/>" + q(2) + "<b/>", false},
			// a once per iteration, so four a needs four
			// iterations and there are only three.
			{"three a", "<a/><a/><a/>", true},
			{"four a", "<a/><a/><a/><a/>", false},
		}
		for _, tc := range cases {
			if got := valid(t, body, tc.doc); got != tc.want {
				t.Errorf("%s: valid=%v want %v", tc.name, got, tc.want)
			}
		}
	})

	// An emptiable inner particle: an iteration that matches nothing is
	// still an iteration.
	//
	// XSD satisfies a particle by partitioning the content into between
	// minOccurs and maxOccurs consecutive parts each matching the term, and
	// nothing in that rule requires a part to be non-empty. When the term is
	// nullable — <element c minOccurs="0"/> — an empty part satisfies it, so
	// the legal totals are still the union over i in [oMin, oMax] of
	// [i*iMin, i*iMax], which for iMin=0 is simply [0, oMax*iMax].
	//
	// The first runtime advanced a count only on a transition between two
	// matched positions, so a scope could reach its minimum only by
	// consuming an element per iteration. <sequence 2..2> over
	// <element c 0..2/> then refused a single c while accepting both zero
	// and two of them — zero only because the empty document short-circuits
	// through the model's own nullability and never consults a counter at
	// all. Accepting 0 and 2 but not 1 is not the language of any particle,
	// which is what made it a bug rather than a reading.
	t.Run("emptiable-inner", func(t *testing.T) {
		for _, oMin := range []int{2, 3} {
			for _, oMax := range []int{oMin, oMin + 1} {
				for iMax := 1; iMax <= 3; iMax++ {
					body := fmt.Sprintf(
						`<xs:sequence minOccurs="%d" maxOccurs="%d">
					  <xs:element name="c" type="xs:string" minOccurs="0" maxOccurs="%d"/>
					</xs:sequence>`, oMin, oMax, iMax)
					name := fmt.Sprintf("seq%dx%d over c0x%d", oMin, oMax, iMax)
					t.Run(name, func(t *testing.T) {
						for n := 0; n <= 8; n++ {
							// Union over i of [0, i*iMax]
							// is [0, oMax*iMax].
							want := n <= oMax*iMax
							if got := valid(t, body, cs(n)); got != want {
								t.Errorf("%d c: valid=%v want %v", n, got, want)
							}
						}
					})
				}
			}
		}
	})

	// Three scopes deep, to check that the vector really is a vector: the
	// innermost count has to restart when the middle one does, and the
	// middle when the outer does. Two repetitions of (two repetitions of
	// (two c)) is eight, and nothing else.
	t.Run("three-deep", func(t *testing.T) {
		body := `<xs:sequence minOccurs="2" maxOccurs="2">
		  <xs:sequence minOccurs="2" maxOccurs="2">
		    <xs:element name="c" type="xs:string" minOccurs="2" maxOccurs="2"/>
		  </xs:sequence>
		</xs:sequence>`
		for _, n := range []int{0, 4, 6, 7, 8, 9, 12, 16} {
			if got, want := valid(t, body, cs(n)), n == 8; got != want {
				t.Errorf("%d c: valid=%v want %v", n, got, want)
			}
		}
	})
}

// A schema may write an occurrence bound far larger than any document will
// reach, and treating it literally is what makes the vector set grow: every
// count below the bound stays a distinct reading. The matcher narrows each
// maximum to what the document can actually reach, which merges them.
//
// particlesZ036 in the W3C suite is the case — <choice maxOccurs="100000"> over
// <sequence maxOccurs="100000000"> over <element a maxOccurs="unbounded"/> —
// and it is a few thousand children, so all three bounds are out of reach and
// every step's three readings collapse into one.
func TestHugeOccurrenceBoundsStayTractable(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root"><xs:complexType>
    <xs:choice maxOccurs="100000">
      <xs:sequence maxOccurs="100000000">
        <xs:element name="a" type="xs:string" maxOccurs="unbounded"/>
      </xs:sequence>
      <xs:element name="b" type="xs:string"/>
    </xs:choice>
  </xs:complexType></xs:element></xs:schema>`
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := xdm.ParseString("<root>"+strings.Repeat("<a>x</a>", 5000)+"</root>",
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc.Root, ValidateOptions{}); err != nil {
		t.Errorf("five thousand a should be valid: %v", err)
	}
}
