package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The identity evaluator has three invariants that four reverted attempts were
// wrong about. They are recorded in comments on nodeTable and mergeEntry; these
// tests pin them as VERDICTS so a later change has to break an assertion rather
// than a comment.
//
//  1. entries / targets / ambiguous are three distinct states, and ambiguity is
//     TERMINAL: once two sibling subtrees define a sequence, no later sibling
//     makes it resolvable again. The bug this guards oscillated with the
//     sibling count -- wrong at three and five, right at two and four -- so the
//     odd counts are the ones that matter.
//  2. tables are seeded from below.targets BEFORE the walk, so a duplicate
//     spanning two levels is still found.
//  3. the pruned walk stays linear in the document.

// keyrefAmbiguitySchema puts a key on `s` and a keyref on `ref`, both under
// `root`, so that each `s` is a sibling scope defining its own keys and the
// root-level keyref resolves against the merge of them.
const keyrefAmbiguitySchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="s" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:sequence>
          <xs:element name="k" minOccurs="0" maxOccurs="unbounded">
            <xs:complexType><xs:attribute name="v" type="xs:string"/></xs:complexType>
          </xs:element>
        </xs:sequence></xs:complexType>
        <xs:key name="kk"><xs:selector xpath="k"/><xs:field xpath="@v"/></xs:key>
      </xs:element>
      <xs:element name="ref" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="v" type="xs:string"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:keyref name="rr" refer="kk"><xs:selector xpath="ref"/><xs:field xpath="@v"/></xs:keyref>
  </xs:element>
</xs:schema>`

// Invariant 1: ambiguity is terminal, and does not oscillate with the number of
// siblings that define the sequence.
//
// n sibling `s` elements each define key "x". For n == 1 the root keyref
// resolves. For every n >= 2 the sequence is ambiguous -- no reading of the
// document says which `s` the reference means -- and must stay unresolvable
// however many further siblings define it. The counts run past five because the
// reverted bug was correct at two and four and wrong at three and five.
func TestIdentityAmbiguityIsTerminal(t *testing.T) {
	st, err := xdm.ParseString(keyrefAmbiguitySchema, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for n := 1; n <= 6; n++ {
		var sb strings.Builder
		sb.WriteString("<root>")
		for i := 0; i < n; i++ {
			sb.WriteString(`<s><k v="x"/></s>`)
		}
		sb.WriteString(`<ref v="x"/></root>`)
		tr, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		got := s.Validate(tr.Root, ValidateOptions{}) != nil
		want := n >= 2 // one definer resolves; two or more are ambiguous forever
		if got != want {
			t.Errorf("%d sibling scopes defining \"x\": invalid=%v want %v\n%s",
				n, got, want, sb.String())
		}
	}
}

// Invariant 2: a table is seeded from below.targets before the walk, so a
// duplicate whose two halves live at different depths is still a duplicate in
// the scope that contains both.
//
// Seeding during the walk instead of before it lost exactly these, and produced
// 771 oracle disagreements. Each case places the two occurrences at a different
// pair of depths so that a seed that runs too late shows up somewhere.
func TestIdentitySeededFromBelowBeforeWalk(t *testing.T) {
	st, err := xdm.ParseString(icOracleSchema("unique"), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, tc := range []struct {
		name    string
		doc     string
		invalid bool
	}{
		{"outer leaf duplicates an inner one",
			`<root><r><leaf id="x"/><r><leaf id="x"/></r></r></root>`, true},
		{"inner-first ordering is still a duplicate",
			`<root><r><r><leaf id="x"/></r><leaf id="x"/></r></root>`, true},
		{"two levels apart",
			`<root><r><leaf id="x"/><r><r><leaf id="x"/></r></r></r></root>`, true},
		{"three levels apart",
			`<root><r><leaf id="x"/><r><r><r><leaf id="x"/></r></r></r></r></root>`, true},
		{"deep pair with distinct ids stays valid",
			`<root><r><leaf id="a"/><r><r><leaf id="b"/></r></r></r></root>`, false},
		{"duplicate only in the innermost scope",
			`<root><r><r><r><leaf id="x"/><leaf id="x"/></r></r></r></root>`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := xdm.ParseString(tc.doc, xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := s.Validate(tr.Root, ValidateOptions{}) != nil; got != tc.invalid {
				t.Errorf("invalid=%v want %v\n%s", got, tc.invalid, tc.doc)
			}
		})
	}
}

// Invariant 3: the pruned walk visits each node a bounded number of times,
// independent of depth.
//
// This is the linearity fix that took keyref from 3.98x to 2.00x per doubling.
// It is asserted on the work counter rather than on elapsed time, because a
// timing assertion on a shared machine is a flake. nodesVisited/node must stay
// flat as the document doubles; a return to the quadratic walk makes it grow
// with depth.
func TestIdentityWalkStaysLinear(t *testing.T) {
	const schema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="r">
    <xs:complexType><xs:sequence>
      <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
      </xs:element>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
    <xs:key name="c"><xs:selector xpath=".//leaf"/><xs:field xpath="@id"/></xs:key>
  </xs:element>
</xs:schema>`
	st, err := xdm.ParseString(schema, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// A deep chain with one leaf per level: every level is a scope, so a
	// walk that re-descends per scope is quadratic and one that prunes is
	// not.
	build := func(depth int) string {
		var sb strings.Builder
		for i := 0; i < depth; i++ {
			fmt.Fprintf(&sb, `<r><leaf id="v%d"/>`, i)
		}
		for i := 0; i < depth; i++ {
			sb.WriteString("</r>")
		}
		return sb.String()
	}
	ratio := func(depth int) float64 {
		tr, err := xdm.ParseString(build(depth), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		stats := validateWithStats(t, s, tr.Root)
		nodes := 2 * depth // one r and one leaf per level
		return float64(stats.NodesVisited) / float64(nodes)
	}
	base := ratio(100)
	for _, depth := range []int{200, 400, 800} {
		got := ratio(depth)
		// Linear work per node means this ratio is flat. A quadratic
		// walk doubles it with the document. The bound is generous so
		// that only a genuine return to per-scope re-descent trips it.
		if got > base*1.5+1 {
			t.Errorf("depth %d: nodesVisited/node = %.2f, baseline %.2f at depth 100: walk is no longer linear",
				depth, got, base)
		}
		t.Logf("depth %4d: nodesVisited/node = %.2f", depth, got)
	}
}
