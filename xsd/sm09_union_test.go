package xsd

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

// sm09Ladder is u0 = union(xs:int xs:int), u_i = union(u_{i-1} u_{i-1}): a
// DAG of n+1 unions with 2^n paths to its leaves.
func sm09Ladder(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:simpleType name="u0"><xs:union memberTypes="xs:int xs:int"/></xs:simpleType>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<xs:simpleType name="u%d"><xs:union memberTypes="u%d u%d"/></xs:simpleType>`, i, i-1, i-1)
	}
	return b.String()
}

func sm09Schema(t *testing.T, body string) *xdm.Node {
	t.Helper()
	d, err := xdm.ParseString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`+body+`</xs:schema>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d.Root
}

// sm09Within fails the test when f does not return in time: an unmemoised
// walk over the 2^40 paths of the ladder never would.
func sm09Within(t *testing.T, label string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: walk did not finish; it is per path, not per type", label)
	}
}

// TestSM09UnionDAGWalksLinear covers validateUnionValueIn, derivedFrom and
// typeDerivedFrom on a union DAG with 2^40 paths.
func TestSM09UnionDAGWalksLinear(t *testing.T) {
	const n = 40
	ladder := sm09Ladder(n)

	sm09Within(t, "load default", func() {
		_, err := Load(sm09Schema(t, ladder+fmt.Sprintf(
			`<xs:element name="e"><xs:complexType><xs:attribute name="a" type="u%d" default="x"/></xs:complexType></xs:element>`, n)), "", Options{})
		if err == nil {
			t.Error("default x accepted by a union of xs:int")
		}
	})

	sm09Within(t, "instance attribute", func() {
		s, err := Load(sm09Schema(t, ladder+fmt.Sprintf(
			`<xs:element name="e"><xs:complexType><xs:attribute name="a" type="u%d"/></xs:complexType></xs:element>`, n)), "", Options{})
		if err != nil {
			t.Fatal(err)
		}
		for v, ok := range map[string]bool{"x": false, "7": true} {
			doc, _ := xdm.ParseString(`<e a="`+v+`"/>`, xdm.ParseOptions{})
			if err := s.Validate(doc.Root, ValidateOptions{}); (err == nil) != ok {
				t.Errorf("a=%q: err=%v, want valid=%v", v, err, ok)
			}
		}
	})

	sm09Within(t, "xsi:type derivedFrom", func() {
		s, err := Load(sm09Schema(t, ladder+fmt.Sprintf(
			`<xs:simpleType name="other"><xs:restriction base="xs:boolean"/></xs:simpleType><xs:element name="e" type="u%d"/>`, n)), "", Options{})
		if err != nil {
			t.Fatal(err)
		}
		doc, _ := xdm.ParseString(`<e xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="other">true</e>`, xdm.ParseOptions{})
		if s.Validate(doc.Root, ValidateOptions{}) == nil {
			t.Error("xsi:type naming an unrelated type accepted")
		}
		doc, _ = xdm.ParseString(`<e xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xs="http://www.w3.org/2001/XMLSchema" xsi:type="xs:int">3</e>`, xdm.ParseOptions{})
		if err := s.Validate(doc.Root, ValidateOptions{}); err != nil {
			t.Errorf("xsi:type naming a transitive member rejected: %v", err)
		}
	})

	sm09Within(t, "alternative typeDerivedFrom", func() {
		_, err := Load(sm09Schema(t, ladder+fmt.Sprintf(
			`<xs:element name="e" type="u%d"><xs:alternative test="@a" type="xs:boolean"/></xs:element>`, n)), "", Options{Version: Version11})
		if err == nil {
			t.Error("alternative type not derived from the declared type accepted")
		}
	})
}

// TestSM09CyclicUnionRefusedBeforeValueWalks: a union that is a member of
// itself is st-props-correct.2, reported as such rather than overflowing the
// stack in a value or derivation walk that runs first.
func TestSM09CyclicUnionRefusedBeforeValueWalks(t *testing.T) {
	for name, body := range map[string]string{
		"attr-default":   `<xs:simpleType name="u"><xs:union memberTypes="u"/></xs:simpleType><xs:element name="e"><xs:complexType><xs:attribute name="a" type="u" default="x"/></xs:complexType></xs:element>`,
		"mutual-default": `<xs:simpleType name="u1"><xs:union memberTypes="u2"/></xs:simpleType><xs:simpleType name="u2"><xs:union memberTypes="u1"/></xs:simpleType><xs:element name="e"><xs:complexType><xs:attribute name="a" type="u1" default="x"/></xs:complexType></xs:element>`,
		"enumeration":    `<xs:simpleType name="u"><xs:union memberTypes="u"/></xs:simpleType><xs:simpleType name="r"><xs:restriction base="u"><xs:enumeration value="x"/></xs:restriction></xs:simpleType>`,
		"substgroup":     `<xs:simpleType name="u"><xs:union memberTypes="u"/></xs:simpleType><xs:element name="h" type="u"/><xs:element name="m" type="xs:boolean" substitutionGroup="h"/>`,
	} {
		sm09Within(t, name, func() {
			_, err := Load(sm09Schema(t, body), "", Options{})
			if err == nil || !strings.Contains(err.Error(), "st-props-correct.2") {
				t.Errorf("%s: err=%v, want st-props-correct.2", name, err)
			}
		})
	}
}
