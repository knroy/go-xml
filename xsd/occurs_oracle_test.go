package xsd

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The content-model matcher is checked against an oracle derived from
// arithmetic rather than from the engine.
//
// Every other test in this package states an expected answer that somebody
// worked out by hand, which bounds how many shapes get checked: the two
// occurrence bugs in nfa.go both lived in a corner nobody had written a case
// for, and neither W3C suite covers them — a group with two or more distinct
// child names was always decided correctly, so 80,879 suite agreements said
// nothing about the single-child case.
//
// What found both was generating the schema, generating the document, and
// comparing against a count computed independently. That is what this is. The
// oracle here never calls Validate, never consults an automaton, and never
// asks the engine anything: for a sequence repeated i times over a child
// occurring iMin..iMax times, the admissible totals are the union over i in
// [oMin, oMax] of the interval [i*iMin, i*iMax], which is a fact about the
// language and not about this implementation. An oracle derived from the
// engine would agree with the engine's bugs, which is exactly why the suites
// missed these.
//
// The two bugs this would have caught:
//
//   - <sequence 5..5> over c{2,2} accepted five c and rejected ten, when ten
//     is the only valid document. The matcher bracketed the repetition count
//     into a low and a high reading and answered each bound from a different
//     one, so a minOccurs floor went unenforced.
//   - with an emptiable inner particle (iMin=0) and oMin>=2, zero and two
//     children were accepted but one was rejected — not the language of any
//     particle. The outer scope was not credited for an iteration that
//     consumed nothing.
//
// Shapes deliberately left out, because the oracle would have been a guess:
// a choice whose branches themselves repeat, or whose branches have differing
// lengths, since the language is then an interleaving whose membership needs
// the same reasoning the matcher does, and an oracle that reasons the same way
// is not independent. Only the shapes below have a language that falls out of
// arithmetic.
//
// Keep this fast. It runs on every `go test ./...`; the default sweep is a few
// thousand documents. GOXSLT_OCCURS_WIDE=1 widens every sweep for an
// occasional deeper run.

// oracleWide reports whether the deeper sweep was asked for.
func oracleWide() bool { return os.Getenv("GOXSLT_OCCURS_WIDE") != "" }

// oracleValid loads src and reports whether doc validates against it. A schema
// that will not load is a failure of the generator, not a validity answer.
func oracleValid(t *testing.T, src, doc string) bool {
	t.Helper()
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v\n%s", err, src)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load schema: %v\n%s", err, src)
	}
	d, err := xdm.ParseString("<root>"+doc+"</root>", xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse instance: %v\n%s", err, doc)
	}
	return s.Validate(d.Root, ValidateOptions{}) == nil
}

// oracleOccurs renders a bound pair, where -1 means unbounded.
func oracleOccurs(min, max int) string {
	if max < 0 {
		return fmt.Sprintf(`minOccurs="%d" maxOccurs="unbounded"`, min)
	}
	return fmt.Sprintf(`minOccurs="%d" maxOccurs="%d"`, min, max)
}

// oracleReachable reports whether k children can be produced by i repetitions, for
// some i in [oMin, oMax], each repetition contributing between iMin and iMax
// (a negative max meaning unbounded).
//
// This is the oracle. It is interval arithmetic over the occurrence bounds and
// has no path back into the engine.
func oracleReachable(oMin, oMax, iMin, iMax, k int) bool {
	hi := oMax
	if hi < 0 {
		// An unbounded outer scope needs no more than k+1 iterations to
		// reach k children: past that, iterations must contribute
		// nothing, which adds no total the smaller counts did not.
		hi = k + 1
		if hi < oMin {
			hi = oMin
		}
	}
	for i := oMin; i <= hi; i++ {
		if k < i*iMin {
			continue
		}
		if iMax < 0 {
			// No per-iteration ceiling: i iterations reach any k at
			// or above the floor, so long as there is an iteration
			// to put the children in.
			if i > 0 || k == 0 {
				return true
			}
			continue
		}
		if k <= i*iMax {
			return true
		}
	}
	return false
}

// oracleBounds is the set of (min, max) pairs swept, with -1 for unbounded.
// maxOccurs="0" is excluded — it has its own test, and inside a sweep it would
// make every total trivially zero.
func oracleBounds(maxMin, maxMax int, unbounded bool) [][2]int {
	var out [][2]int
	for min := 0; min <= maxMin; min++ {
		for max := min; max <= maxMax; max++ {
			if max == 0 {
				continue
			}
			out = append(out, [2]int{min, max})
		}
		if unbounded {
			out = append(out, [2]int{min, -1})
		}
	}
	return out
}

// oracleBinaryDocs returns every string over {a, b} of length 0..maxLen, so wrong
// orderings are offered as well as wrong lengths.
func oracleBinaryDocs(maxLen int) []string {
	var out []string
	for n := 0; n <= maxLen; n++ {
		for mask := 0; mask < 1<<n; mask++ {
			var sb strings.Builder
			for i := 0; i < n; i++ {
				if mask&(1<<i) != 0 {
					sb.WriteString("b")
				} else {
					sb.WriteString("a")
				}
			}
			out = append(out, sb.String())
		}
	}
	return out
}

// oracleElements renders a name string as child elements.
func oracleElements(names string) string {
	var sb strings.Builder
	for _, r := range names {
		fmt.Fprintf(&sb, "<%c>x</%c>", r, r)
	}
	return sb.String()
}

// TestOccursOracleSequence is the harness that found both bugs, made permanent
// and given the unbounded bounds it did not have.
//
//	<sequence oMin..oMax> <element c iMin..iMax/> </sequence>
//
// Oracle: k children are admitted exactly when k lies in [i*iMin, i*iMax] for
// some i in [oMin, oMax].
func TestOccursOracleSequence(t *testing.T) {
	maxOuterMin, maxOuterMax := 3, 4
	maxInnerMin, maxInnerMax := 3, 3
	maxChildren := 10
	if oracleWide() {
		maxOuterMin, maxOuterMax = 5, 6
		maxInnerMin, maxInnerMax = 4, 5
		maxChildren = 16
	}

	models, checked := 0, 0
	for _, o := range oracleBounds(maxOuterMin, maxOuterMax, true) {
		for _, in := range oracleBounds(maxInnerMin, maxInnerMax, true) {
			if o[1] < 0 && in[1] < 0 {
				// Both scopes unbounded. The oracle is right
				// here too, but the language is every count
				// from the floor up, which exercises nothing
				// the singly-unbounded cases do not.
				continue
			}
			src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root"><xs:complexType>
    <xs:sequence %s><xs:element name="c" type="xs:string" %s/></xs:sequence>
  </xs:complexType></xs:element></xs:schema>`,
				oracleOccurs(o[0], o[1]), oracleOccurs(in[0], in[1]))
			models++
			for k := 0; k <= maxChildren; k++ {
				got := oracleValid(t, src, strings.Repeat("<c>x</c>", k))
				want := oracleReachable(o[0], o[1], in[0], in[1], k)
				checked++
				if got != want {
					t.Errorf("sequence %s over c %s: %d children: valid=%v want %v\n%s",
						oracleOccurs(o[0], o[1]), oracleOccurs(in[0], in[1]), k, got, want, src)
				}
			}
		}
	}
	t.Logf("%d models, %d documents", models, checked)
}

// TestOccursOracleEmptiableInner covers the region the second bug lived in,
// densely: an inner particle that can match nothing, under an outer scope that
// must repeat. The engine accepted 0 and 2 children here but rejected 1.
//
// Oracle: with iMin=0 the union of [0, i*iMax] over i in [oMin, oMax] collapses
// to [0, oMax*iMax] — every count up to the ceiling is reachable, because an
// iteration is allowed to contribute nothing.
func TestOccursOracleEmptiableInner(t *testing.T) {
	maxOuter, maxInnerMax, maxChildren := 5, 4, 12
	if oracleWide() {
		maxOuter, maxInnerMax, maxChildren = 7, 6, 18
	}
	checked := 0
	for oMin := 0; oMin <= maxOuter; oMin++ {
		for oMax := oMin; oMax <= maxOuter; oMax++ {
			if oMax == 0 {
				continue
			}
			for iMax := 1; iMax <= maxInnerMax; iMax++ {
				src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root"><xs:complexType>
    <xs:sequence minOccurs="%d" maxOccurs="%d">
      <xs:element name="c" type="xs:string" minOccurs="0" maxOccurs="%d"/>
    </xs:sequence>
  </xs:complexType></xs:element></xs:schema>`, oMin, oMax, iMax)
				for k := 0; k <= maxChildren; k++ {
					got := oracleValid(t, src, strings.Repeat("<c>x</c>", k))
					want := k <= oMax*iMax
					checked++
					if got != want {
						t.Errorf("sequence %d..%d over emptiable c 0..%d: %d children: valid=%v want %v",
							oMin, oMax, iMax, k, got, want)
					}
				}
			}
		}
	}
	t.Logf("%d documents", checked)
}

// TestOccursOracleTwoChildren checks sequences of names, not just counts.
//
//	<sequence oMin..oMax> <a/> <b/> </sequence>
//
// Each iteration contributes exactly "ab", so the language is the strings
// (ab)^i for i in [oMin, oMax] and nothing else. Checking the name string
// rather than its length is the point: a matcher that counted correctly but
// ordered wrongly would pass a count-only check.
func TestOccursOracleTwoChildren(t *testing.T) {
	maxLen := 6
	if oracleWide() {
		maxLen = 9
	}
	docs := oracleBinaryDocs(maxLen)

	checked := 0
	for oMin := 0; oMin <= 3; oMin++ {
		for oMax := oMin; oMax <= 3; oMax++ {
			if oMax == 0 {
				continue
			}
			src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root"><xs:complexType>
    <xs:sequence minOccurs="%d" maxOccurs="%d">
      <xs:element name="a" type="xs:string"/>
      <xs:element name="b" type="xs:string"/>
    </xs:sequence>
  </xs:complexType></xs:element></xs:schema>`, oMin, oMax)
			for _, names := range docs {
				want := false
				for i := oMin; i <= oMax; i++ {
					if names == strings.Repeat("ab", i) {
						want = true
						break
					}
				}
				got := oracleValid(t, src, oracleElements(names))
				checked++
				if got != want {
					t.Errorf("sequence %d..%d over <a/><b/>: names %q: valid=%v want %v",
						oMin, oMax, names, got, want)
				}
			}
		}
	}
	t.Logf("%d documents", checked)
}

// TestOccursOracleChoiceTwoBranches: a repeating choice between two single
// elements.
//
//	<choice oMin..oMax> <a/> <b/> </choice>
//
// Each iteration contributes exactly one child, either name, so the language is
// every string over {a, b} whose length lies in [oMin, oMax] — order entirely
// free. That is the one choice shape whose language falls out of arithmetic,
// which is why the branches here are bare elements with default bounds; a
// branch that repeats, or two branches of differing length, would need an
// interleaving argument rather than a count, and is left out.
func TestOccursOracleChoiceTwoBranches(t *testing.T) {
	maxLen := 6
	if oracleWide() {
		maxLen = 9
	}
	docs := oracleBinaryDocs(maxLen)

	checked := 0
	for _, o := range oracleBounds(3, 4, true) {
		oMin, oMax := o[0], o[1]
		src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root"><xs:complexType>
    <xs:choice %s>
      <xs:element name="a" type="xs:string"/>
      <xs:element name="b" type="xs:string"/>
    </xs:choice>
  </xs:complexType></xs:element></xs:schema>`, oracleOccurs(oMin, oMax))
		for _, names := range docs {
			n := len(names)
			want := n >= oMin && (oMax < 0 || n <= oMax)
			got := oracleValid(t, src, oracleElements(names))
			checked++
			if got != want {
				t.Errorf("choice %s over <a/>|<b/>: names %q: valid=%v want %v",
					oracleOccurs(oMin, oMax), names, got, want)
			}
		}
	}
	t.Logf("%d documents", checked)
}

// TestOccursOracleNestedThree composes the arithmetic three deep.
//
//	<sequence a..b> <sequence c..d> <element e..f/> </sequence> </sequence>
//
// The middle scope admits the totals U = union over j in [c,d] of [j*e, j*f];
// the outer admits every sum of i values drawn from U, for i in [a,b]. Rather
// than a closed form, the oracle enumerates that sum set — still arithmetic,
// still nothing to do with the engine. Bounds are small so the enumeration
// stays trivial.
func TestOccursOracleNestedThree(t *testing.T) {
	// oracleReachableSet returns the totals a scope repeated min..max times over
	// a per-iteration total set can produce, capped at limit.
	oracleReachableSet := func(min, max int, per map[int]bool, limit int) map[int]bool {
		cur := map[int]bool{0: true} // totals from exactly i iterations
		out := map[int]bool{}
		if min == 0 {
			out[0] = true
		}
		for i := 1; i <= max; i++ {
			next := map[int]bool{}
			for tot := range cur {
				for p := range per {
					if tot+p <= limit {
						next[tot+p] = true
					}
				}
			}
			cur = next
			if len(cur) == 0 {
				break
			}
			if i >= min {
				for tot := range cur {
					out[tot] = true
				}
			}
		}
		return out
	}

	const limit = 12
	maxChildren := 8
	if oracleWide() {
		maxChildren = 12
	}
	checked := 0
	for a := 1; a <= 2; a++ {
		for b := a; b <= 3; b++ {
			for c := 0; c <= 2; c++ {
				for d := c; d <= 3; d++ {
					if d == 0 {
						continue
					}
					for e := 0; e <= 2; e++ {
						for f := e; f <= 2; f++ {
							if f == 0 {
								continue
							}
							inner := map[int]bool{}
							for k := e; k <= f; k++ {
								inner[k] = true
							}
							mid := oracleReachableSet(c, d, inner, limit)
							top := oracleReachableSet(a, b, mid, limit)

							src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root"><xs:complexType>
    <xs:sequence minOccurs="%d" maxOccurs="%d">
      <xs:sequence minOccurs="%d" maxOccurs="%d">
        <xs:element name="c" type="xs:string" minOccurs="%d" maxOccurs="%d"/>
      </xs:sequence>
    </xs:sequence>
  </xs:complexType></xs:element></xs:schema>`, a, b, c, d, e, f)
							for k := 0; k <= maxChildren; k++ {
								got := oracleValid(t, src, strings.Repeat("<c>x</c>", k))
								want := top[k]
								checked++
								if got != want {
									t.Errorf("sequence %d..%d over sequence %d..%d over c %d..%d: %d children: valid=%v want %v",
										a, b, c, d, e, f, k, got, want)
								}
							}
						}
					}
				}
			}
		}
	}
	t.Logf("%d documents", checked)
}

// TestOccursOracleMaxZero: maxOccurs="0" is legal and means the particle cannot
// appear at all. It is easy to mishandle — a naive counter reads it as "no
// ceiling", or as a bound never reached — and the language is unambiguous, so
// it is worth stating on its own rather than folding into a sweep.
func TestOccursOracleMaxZero(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   func(k int) bool // admissible counts of c
	}{{
		name: "element max 0",
		body: `<xs:sequence>
		  <xs:element name="c" type="xs:string" minOccurs="0" maxOccurs="0"/>
		</xs:sequence>`,
		ok: func(k int) bool { return k == 0 },
	}, {
		name: "sequence max 0 over required element",
		body: `<xs:sequence minOccurs="0" maxOccurs="0">
		  <xs:element name="c" type="xs:string"/>
		</xs:sequence>`,
		ok: func(k int) bool { return k == 0 },
	}, {
		name: "element max 0 beside a live sibling",
		body: `<xs:sequence>
		  <xs:element name="c" type="xs:string" minOccurs="0" maxOccurs="0"/>
		  <xs:element name="d" type="xs:string" minOccurs="0" maxOccurs="3"/>
		</xs:sequence>`,
		ok: func(k int) bool { return k == 0 },
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root"><xs:complexType>%s</xs:complexType></xs:element></xs:schema>`, tc.body)
			for k := 0; k <= 3; k++ {
				got := oracleValid(t, src, strings.Repeat("<c>x</c>", k))
				if want := tc.ok(k); got != want {
					t.Errorf("%d c: valid=%v want %v\n%s", k, got, want, src)
				}
			}
		})
	}
}
