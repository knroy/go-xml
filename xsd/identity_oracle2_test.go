package xsd

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The oracle in identity_oracle_test.go covers one shape: ".//leaf" with an
// "@id" field, on a recursive element. That is the shape the pathological
// benchmark uses, and it is not enough to license a rewrite.
//
// This widens it along the dimensions an incremental evaluator is most likely
// to get wrong:
//
//   - selector depth, so that ".//a/b" is exercised as well as ".//b" — a
//     one-pass matcher has to decide a multi-step path from a node's own
//     ancestry rather than from where the walk started.
//   - a nested wrapper between the scope and its targets, so the targets are
//     not children of the scope element.
//   - both constraint kinds, since key additionally fails on an absent field
//     and unique merely drops the node.
//
// The oracle still computes validity from the spec's definition — the targets
// of a scope are the nodes its selector reaches inside that scope's subtree,
// and the scope fails when two distinct targets carry the same sequence — and
// never calls selectNodes, buildNodeTable or mergeTables.

// oracle2Doc is a generated tree: a recursive <r>, each level holding some
// <box> wrappers, each wrapper holding some <leaf> elements.
type oracle2Doc struct {
	boxes [][]string // per box, the id of each leaf ("" for absent)
	// loose are <leaf> elements directly under <r>, outside any <box>.
	// ".//box/leaf" must not select them, and without them in the corpus
	// the leading "box" step is unobservable: every leaf would be inside a
	// box, so a matcher that ignored the step would agree anyway. They
	// deliberately reuse the same id space as the boxed ones, so a matcher
	// that wrongly selects them produces a duplicate that is not there.
	loose []string
	child *oracle2Doc
}

func oracle2Gen(rnd *rand.Rand, depth, maxBox, maxLeaf, distinct int) *oracle2Doc {
	d := &oracle2Doc{}
	for i := rnd.Intn(maxBox + 1); i > 0; i-- {
		var leaves []string
		for j := rnd.Intn(maxLeaf + 1); j > 0; j-- {
			if rnd.Intn(8) == 0 {
				leaves = append(leaves, "")
			} else {
				leaves = append(leaves, fmt.Sprintf("v%d", rnd.Intn(distinct)))
			}
		}
		d.boxes = append(d.boxes, leaves)
	}
	for i := rnd.Intn(3); i > 0; i-- {
		d.loose = append(d.loose, fmt.Sprintf("v%d", rnd.Intn(distinct)))
	}
	if depth > 1 && rnd.Intn(3) != 0 {
		d.child = oracle2Gen(rnd, depth-1, maxBox, maxLeaf, distinct)
	}
	return d
}

func (d *oracle2Doc) render(sb *strings.Builder) {
	sb.WriteString("<r>")
	for _, id := range d.loose {
		fmt.Fprintf(sb, `<leaf id=%q/>`, id)
	}
	for _, box := range d.boxes {
		sb.WriteString("<box>")
		for _, id := range box {
			if id == "" {
				sb.WriteString("<leaf/>")
			} else {
				fmt.Fprintf(sb, `<leaf id=%q/>`, id)
			}
		}
		sb.WriteString("</box>")
	}
	if d.child != nil {
		d.child.render(sb)
	}
	sb.WriteString("</r>")
}

// ids returns every leaf id at or below this scope, in document order.
func (d *oracle2Doc) ids() []string {
	if d == nil {
		return nil
	}
	var out []string
	for _, box := range d.boxes {
		out = append(out, box...)
	}
	return append(out, d.child.ids()...)
}

// oracle2Invalid decides validity per scope, independently of the engine.
func (d *oracle2Doc) oracle2Invalid(isKey bool) bool {
	if d == nil {
		return false
	}
	seen := map[string]bool{}
	for _, id := range d.ids() {
		if id == "" {
			if isKey {
				return true
			}
			continue
		}
		if seen[id] {
			return true
		}
		seen[id] = true
	}
	return d.child.oracle2Invalid(isKey)
}

// The selector is ".//box/leaf": two steps below a descendant-or-self, so the
// engine must decide a node's membership from its own parent rather than from
// the scope it started at.
func oracle2Schema(kind string) string {
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
      <xs:element name="box" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:sequence>
          <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded">
            <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
          </xs:element>
        </xs:sequence></xs:complexType>
      </xs:element>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
    <xs:%s name="c"><xs:selector xpath=".//box/leaf"/><xs:field xpath="@id"/></xs:%s>
  </xs:element>
</xs:schema>`, kind, kind)
}

func TestIdentityConstraintOracleMultiStep(t *testing.T) {
	for _, kind := range []string{"unique", "key"} {
		isKey := kind == "key"
		st, err := xdm.ParseString(oracle2Schema(kind), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("%s: parse schema: %v", kind, err)
		}
		s, err := Load(st.Root, "", Options{})
		if err != nil {
			t.Fatalf("%s: load schema: %v", kind, err)
		}
		rnd := rand.New(rand.NewSource(20260904))
		var wrong int
		const n = 2000
		for i := 0; i < n; i++ {
			d := oracle2Gen(rnd, 1+rnd.Intn(4), 2, 3, 3)
			var sb strings.Builder
			sb.WriteString("<root>")
			d.render(&sb)
			sb.WriteString("</root>")
			tr, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
			if err != nil {
				t.Fatalf("%s: parse instance: %v\n%s", kind, err, sb.String())
			}
			got := s.Validate(tr.Root, ValidateOptions{}) != nil
			want := d.oracle2Invalid(isKey)
			if got != want {
				wrong++
				if wrong <= 3 {
					t.Errorf("%s: engine invalid=%v oracle invalid=%v\n%s",
						kind, got, want, sb.String())
				}
			}
		}
		t.Logf("%s (.//box/leaf): %d documents checked, %d disagreements", kind, n, wrong)
	}
}

// A keyref must resolve only against keys in a scope that contains it. A key
// in a sibling subtree must not satisfy it — the case a bottom-up merge is
// most likely to get wrong, because the merged table an ancestor holds does
// contain both siblings' entries.
func TestIdentityConstraintKeyrefScope(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element ref="box" minOccurs="0" maxOccurs="unbounded"/>
    </xs:sequence></xs:complexType>
  </xs:element>
  <xs:element name="box">
    <xs:complexType><xs:sequence>
      <xs:element name="def" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
      </xs:element>
      <xs:element name="use" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="ref" type="xs:string"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//def"/><xs:field xpath="@id"/></xs:key>
    <xs:keyref name="kr" refer="k"><xs:selector xpath=".//use"/><xs:field xpath="@ref"/></xs:keyref>
  </xs:element>
</xs:schema>`
	st, _ := xdm.ParseString(src, xdm.ParseOptions{})
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, tc := range []struct {
		name    string
		doc     string
		invalid bool
	}{
		{"a use resolves against a def in its own box",
			`<root><box><def id="a"/><use ref="a"/></box></root>`, false},
		{"a use must NOT resolve against a def in a sibling box",
			`<root><box><def id="a"/></box><box><use ref="a"/></box></root>`, true},
		{"an unresolved use fails",
			`<root><box><def id="a"/><use ref="zzz"/></box></root>`, true},
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
