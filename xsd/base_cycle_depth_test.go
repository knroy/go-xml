package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// checkTypeBaseCycles walked a type's {base type definition} chain looking for
// a return to the type itself, and gave up after a fixed 4096 steps:
//
//	for steps := 0; cur != nil && steps < 4096; steps++ {
//
// Running out of steps appended no error, which is the permissive verdict — so
// a schema whose base-type cycle was longer than the count loaded clean. That
// is a FALSE ACCEPT of exactly the ct-props-correct.3 / st-props-correct.2
// violation the function exists to diagnose. The cliff was sharp: a 4096-type
// cycle reported every link, a 4097-type one reported nothing at all.
//
// The count is now a visited set keyed on the component pointer, matching the
// other converted walks in this package. These tests pin both sides: a legal
// acyclic chain past the old bound still loads and still derives, and a cyclic
// chain past it is still rejected.
//
// The loop provably iterates at these depths — unlike the facet chains that
// collapse during parsing because SimpleType.Primitive is filled in eagerly.
// TestBaseChainActuallyIterates measures the built component's real chain
// length before anything else is concluded.
var baseCycleDepths = []int{1, 2, 63, 64, 65, 128, 256, 512, 1024,
	4094, 4095, 4096, 4097}

// baseCycleChain builds C0 (a plain complexType) then n extensions of it, so
// C{n} sits n links above C0 and n+1 links below xs:anyType. Acyclic.
func baseCycleChain(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	b.WriteString(`<xs:complexType name="C0"><xs:sequence>` +
		`<xs:element name="a" type="xs:string"/></xs:sequence></xs:complexType>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<xs:complexType name="C%d"><xs:complexContent>`+
			`<xs:extension base="C%d"/></xs:complexContent></xs:complexType>`, i, i-1)
	}
	fmt.Fprintf(&b, `<xs:element name="root" type="C%d"/>`, n)
	b.WriteString(`</xs:schema>`)
	return b.String()
}

// baseCycleRing builds the same chain but closes it: C0 extends C{n}. Every
// one of the n+1 types is then on a single cycle of length n+1, and every one
// of them violates ct-props-correct.3.
func baseCycleRing(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	fmt.Fprintf(&b, `<xs:complexType name="C0"><xs:complexContent>`+
		`<xs:extension base="C%d"/></xs:complexContent></xs:complexType>`, n)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<xs:complexType name="C%d"><xs:complexContent>`+
			`<xs:extension base="C%d"/></xs:complexContent></xs:complexType>`, i, i-1)
	}
	b.WriteString(`</xs:schema>`)
	return b.String()
}

// TestBaseChainActuallyIterates establishes that the walk under test really
// does take one step per link at these depths, so that a passing result below
// means the guard was crossed rather than never reached.
//
// This repo has twice recorded a negative result from a probe that cleared a
// guard without approaching it. Measuring the built component's chain length
// is what makes the rest of this file evidence.
func TestBaseChainActuallyIterates(t *testing.T) {
	for _, n := range []int{2, 128, 4097} {
		s := deepLoad(t, baseCycleChain(n))
		cur := Type(s.Types[xdm.QName{Local: fmt.Sprintf("C%d", n)}])
		if cur == nil {
			t.Fatalf("depth %d: C%d not found", n, n)
		}
		steps := 0
		for cur != nil {
			next := baseOf(cur)
			if next == nil || next == cur {
				break
			}
			cur, steps = next, steps+1
		}
		// n extensions plus the step from C0 to xs:anyType.
		if steps != n+1 {
			t.Errorf("depth %d: base chain is %d links, want %d — the walk "+
				"does not iterate here and this file proves nothing",
				n, steps, n+1)
		}
	}
}

// TestBaseCycleAcyclicChainAccepted is the baseline and the regression guard
// together: a legal chain must load without a ct-props-correct.3 error at
// every depth, including well past the old 4096 count.
func TestBaseCycleAcyclicChainAccepted(t *testing.T) {
	for _, n := range baseCycleDepths {
		st, err := xdm.ParseString(baseCycleChain(n), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("depth %d: parse: %v", n, err)
		}
		if _, err := Load(st.Root, "", Options{}); err != nil {
			t.Errorf("depth %d: legal acyclic chain rejected: %v", n, err)
		}
	}
}

// TestBaseCycleDeepChainStillDerives asserts the semantic property, not merely
// that loading returned no error: the type at the top of a chain longer than
// the old bound still derives from the type at the bottom, and a document
// using it still validates against the content C0 declared.
func TestBaseCycleDeepChainStillDerives(t *testing.T) {
	for _, n := range []int{2, 4095, 4097} {
		s := deepLoad(t, baseCycleChain(n))
		top := s.Types[xdm.QName{Local: fmt.Sprintf("C%d", n)}]
		bottom := s.Types[xdm.QName{Local: "C0"}]
		if top == nil || bottom == nil {
			t.Fatalf("depth %d: types missing", n)
		}
		// Walk up from the top and require the bottom to be on the chain.
		found := false
		for cur := Type(top); cur != nil; {
			if cur == bottom {
				found = true
				break
			}
			next := baseOf(cur)
			if next == nil || next == cur {
				break
			}
			cur = next
		}
		if !found {
			t.Errorf("depth %d: C%d does not derive from C0", n, n)
		}
		// And the inherited content model still applies.
		doc, err := xdm.ParseString(`<root><a>x</a></root>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("depth %d: parse doc: %v", n, err)
		}
		if err := s.Validate(doc.Root, ValidateOptions{}); err != nil {
			t.Errorf("depth %d: document valid against the inherited content "+
				"model was rejected: %v", n, err)
		}
	}
}

// TestBaseCycleRingRejected is the bug. Before the visited set, a ring of 4097
// types loaded with no error whatever, while a ring of 4096 reported all of
// them: the cycle was simply further from each type than the step count
// allowed the walk to look.
func TestBaseCycleRingRejected(t *testing.T) {
	for _, n := range baseCycleDepths {
		st, err := xdm.ParseString(baseCycleRing(n), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("ring %d: parse: %v", n, err)
		}
		_, err = Load(st.Root, "", Options{})
		if err == nil {
			t.Errorf("ring of %d types: circular base chain accepted; "+
				"ct-props-correct.3 requires it be rejected", n+1)
			continue
		}
		if !strings.Contains(err.Error(), "ct-props-correct.3") {
			t.Errorf("ring of %d types: rejected, but not as a base cycle: %v",
				n+1, err)
		}
	}
}

// TestBaseCycleSelfReference keeps the shortest cycle — a type naming itself —
// covered, since it takes the separate `baseOf(t) == t` path above the walk.
func TestBaseCycleSelfReference(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
		`<xs:complexType name="sAddress"><xs:complexContent>` +
		`<xs:extension base="sAddress"/></xs:complexContent></xs:complexType>` +
		`</xs:schema>`
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(st.Root, "", Options{}); err == nil {
		t.Fatal("a type that is its own base was accepted")
	}
}

// TestBaseCycleUrTypeStillTerminates guards the exception that makes every
// other chain finite: xs:anyType is its own base, and treating that as a cycle
// once rejected 11,044 of 14,405 suite schemas.
func TestBaseCycleUrTypeStillTerminates(t *testing.T) {
	s := deepLoad(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`+
		`<xs:complexType name="C"><xs:complexContent>`+
		`<xs:extension base="xs:anyType"/></xs:complexContent></xs:complexType>`+
		`<xs:element name="root" type="C"/></xs:schema>`)
	if s.Types[xdm.QName{Local: "C"}] == nil {
		t.Fatal("C did not load")
	}
}
