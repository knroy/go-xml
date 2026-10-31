package xsd

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// matchNames swaps two state sets instead of making one per child, which was
// three quarters of the DTD validator's allocation. Allocation per Match must
// not grow with the number of children.
func TestSequenceMatcherReusesStateSets(t *testing.T) {
	p := &Particle{MinOccurs: 0, MaxOccurs: Unbounded, Term: &ElementDecl{Name: xdm.QName{Local: "a"}}}
	m, err := NewSequenceMatcher(p)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]xdm.QName, 1000)
	for i := range names {
		names[i] = xdm.QName{Local: "a"}
	}
	if ok, _ := m.Match(names); !ok {
		t.Fatal("a* rejected a run of a")
	}
	if n := testing.AllocsPerRun(5, func() { m.Match(names) }); n > 100 {
		t.Errorf("Match over 1000 children made %v allocations, want at most 100", n)
	}
}
