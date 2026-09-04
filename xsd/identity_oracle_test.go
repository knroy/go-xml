package xsd

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The identity-constraint evaluator is checked against an oracle derived from
// the spec's definition rather than from the engine.
//
// docs/security.md records four attempts to make this evaluator cheaper, all
// four reverted. Two of them were correct and merely slower; the danger in the
// next attempt is one that is faster and quietly wrong, because the suites
// exercise identity constraints on shallow documents where a cross-level bug
// cannot show. This oracle exists so that a redesign has something to be wrong
// against.
//
// What it computes: for a `key` or `unique` declared on an element, the
// qualified targets are the nodes its selector reaches within that element's
// subtree, and the document is invalid exactly when two distinct targets in
// one subtree carry the same key sequence (and, for `key`, when any target
// lacks one). The oracle evaluates that directly over a generated tree it
// already knows the shape of — it never calls selectNodes, buildNodeTable or
// mergeTables, so it cannot inherit their mistakes.

// icNode is a generated element: a recursive `r` carrying `leaf` children.
type icNode struct {
	ids      []string // the id attribute of each leaf child, "" for absent
	child    *icNode  // the nested r, if any
	haveKids bool
}

// icGen builds a random recursive document. Depth and width stay small: the
// point is to cover many *shapes*, not to be large.
func icGen(rnd *rand.Rand, depth, maxWidth, distinct int) *icNode {
	n := &icNode{}
	w := rnd.Intn(maxWidth + 1)
	for i := 0; i < w; i++ {
		switch rnd.Intn(10) {
		case 0:
			n.ids = append(n.ids, "") // leaf with no id attribute
		default:
			n.ids = append(n.ids, fmt.Sprintf("v%d", rnd.Intn(distinct)))
		}
	}
	if depth > 1 && rnd.Intn(4) != 0 {
		n.child = icGen(rnd, depth-1, maxWidth, distinct)
		n.haveKids = true
	}
	return n
}

func (n *icNode) render(sb *strings.Builder) {
	sb.WriteString("<r>")
	for _, id := range n.ids {
		if id == "" {
			sb.WriteString("<leaf/>")
		} else {
			fmt.Fprintf(sb, `<leaf id=%q/>`, id)
		}
	}
	if n.child != nil {
		n.child.render(sb)
	}
	sb.WriteString("</r>")
}

// subtreeIDs returns every leaf id at or below n, in document order, with ""
// standing for a leaf that carries no id.
func (n *icNode) subtreeIDs() []string {
	if n == nil {
		return nil
	}
	out := append([]string{}, n.ids...)
	return append(out, n.child.subtreeIDs()...)
}

// oracleInvalid decides validity independently of the engine.
//
// The constraint is declared on `r`, so every `r` in the document is a scope.
// Within one scope the targets are all `leaf` descendants; the scope fails if
// two of them share an id, and for `key` also if any of them has no id.
func (n *icNode) oracleInvalid(isKey bool) bool {
	if n == nil {
		return false
	}
	seen := map[string]bool{}
	for _, id := range n.subtreeIDs() {
		if id == "" {
			if isKey {
				return true // a key target with no value
			}
			continue // unique simply drops it
		}
		if seen[id] {
			return true
		}
		seen[id] = true
	}
	return n.child.oracleInvalid(isKey)
}

func icOracleSchema(kind string) string {
	return fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
  </xs:element>
  <xs:element name="r">
    <xs:complexType><xs:sequence>
      <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
      </xs:element>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
    <xs:%s name="c"><xs:selector xpath=".//leaf"/><xs:field xpath="@id"/></xs:%s>
  </xs:element>
</xs:schema>`, kind, kind)
}

func TestIdentityConstraintOracle(t *testing.T) {
	for _, kind := range []string{"unique", "key"} {
		isKey := kind == "key"
		st, err := xdm.ParseString(icOracleSchema(kind), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("%s: parse schema: %v", kind, err)
		}
		s, err := Load(st.Root, "", Options{})
		if err != nil {
			t.Fatalf("%s: load schema: %v", kind, err)
		}

		rnd := rand.New(rand.NewSource(20260903))
		var wrong, checked int
		for i := 0; i < 3000; i++ {
			doc := icGen(rnd, 1+rnd.Intn(5), 3, 3)
			var sb strings.Builder
			sb.WriteString("<root>")
			doc.render(&sb)
			sb.WriteString("</root>")

			tr, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("%s: parse instance: %v\n%s", kind, err, sb.String())
			}
			got := s.Validate(tr.Root, ValidateOptions{}) != nil
			want := doc.oracleInvalid(isKey)
			checked++
			if got != want {
				wrong++
				if wrong <= 3 {
					t.Errorf("%s: engine invalid=%v, oracle invalid=%v\n%s",
						kind, got, want, sb.String())
				}
			}
		}
		t.Logf("%s: %d documents checked, %d disagreements", kind, checked, wrong)
	}
}

// The oracle above covers the shapes the generator makes. This pins the two
// cross-level cases by hand, because they are the ones an incremental
// implementation is most likely to get wrong: a key defined at one depth and
// repeated at another is a duplicate only within a scope that contains both.
func TestIdentityConstraintCrossLevel(t *testing.T) {
	st, _ := xdm.ParseString(icOracleSchema("unique"), xdm.ParseOptions{})
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, tc := range []struct {
		name    string
		doc     string
		invalid bool
	}{
		{"same id at two depths is a duplicate in the outer scope",
			`<root><r><leaf id="x"/><r><leaf id="x"/></r></r></root>`, true},
		{"distinct ids at two depths are fine",
			`<root><r><leaf id="x"/><r><leaf id="y"/></r></r></root>`, false},
		{"a duplicate confined to the inner scope still fails",
			`<root><r><r><leaf id="x"/><leaf id="x"/></r></r></root>`, true},
		{"three levels, outermost sees all of them",
			`<root><r><leaf id="a"/><r><leaf id="b"/><r><leaf id="a"/></r></r></r></root>`, true},
	} {
		tr, err := xdm.ParseString(tc.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := s.Validate(tr.Root, ValidateOptions{}) != nil; got != tc.invalid {
			t.Errorf("%s: invalid=%v want %v\n%s", tc.name, got, tc.invalid, tc.doc)
		}
	}
}
