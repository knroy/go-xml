package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func subEl(n string) *ElementDecl { return &ElementDecl{Name: xdm.QName{Local: n}} }

func seq(min, max int, ps ...*Particle) *Particle {
	return &Particle{MinOccurs: min, MaxOccurs: max,
		Term: &ModelGroup{Compositor: CompositorSequence, Particles: ps}}
}

func alt(min, max int, ps ...*Particle) *Particle {
	return &Particle{MinOccurs: min, MaxOccurs: max,
		Term: &ModelGroup{Compositor: CompositorChoice, Particles: ps}}
}

func leaf(min, max int, name string) *Particle {
	return &Particle{MinOccurs: min, MaxOccurs: max, Term: subEl(name)}
}

// TestSubsumeRepetitionIsScoped pins the bug that a repetition must begin at a
// state of its own.
//
// The base is <choice>((c1,c1,c2?)* | (d1*,d2+)+)</choice> and the restriction
// is d1+. Laying the back edge of d1* onto the state the enclosing sequence
// was entered at put the outer choice's own exit back in reach, so a lone d1
// reached an accepting state and this invalid derivation was accepted.
// particlesM034 is the conformance case.
func TestSubsumeRepetitionIsScoped(t *testing.T) {
	base := alt(1, 1,
		seq(0, Unbounded, leaf(2, 2, "c1"), leaf(0, 1, "c2")),
		seq(1, Unbounded, leaf(0, Unbounded, "d1"), leaf(1, Unbounded, "d2")),
	)
	der := alt(1, 1, leaf(1, Unbounded, "d1"))

	err, ok := particleSubsumes(der, base)
	if !ok {
		t.Fatal("declined a shape it can decide")
	}
	if err == nil {
		t.Fatal("accepted d1+ as a restriction of ((c1,c1,c2?)* | (d1*,d2+)+): " +
			"the base admits no sequence of d1 alone")
	}
}

// TestSubsumeAcceptsSequenceOfChoiceBranch is the shape XSD 1.1 added and the
// 1.0 table forbids: a sequence restricting one branch of a choice.
// particlesHb008 and particlesHb011 are the conformance cases.
func TestSubsumeAcceptsChoiceBranch(t *testing.T) {
	base := alt(1, 1,
		leaf(1, 3, "e1"),
		seq(1, 2, leaf(1, 3, "e2"), leaf(0, 3, "e3"), leaf(0, 3, "e4")),
	)
	der := alt(1, 1,
		leaf(1, 2, "e1"),
		seq(1, 2, leaf(1, 1, "e2"),
			alt(1, 1, leaf(2, 3, "e3"), leaf(1, 3, "e4"))),
	)
	err, ok := particleSubsumes(der, base)
	if !ok {
		t.Fatal("declined a shape it can decide")
	}
	if err != nil {
		t.Fatalf("rejected a valid 1.1 restriction: %v", err)
	}
}

// TestSubsumeDeclinesAllGroup checks that an all group is handed back to
// allSubsumes rather than answered here: k members are k! interleavings, which
// unrolling cannot afford.
func TestSubsumeDeclinesAllGroup(t *testing.T) {
	all := &Particle{MinOccurs: 1, MaxOccurs: 1, Term: &ModelGroup{
		Compositor: CompositorAll,
		Particles:  []*Particle{leaf(1, 1, "a"), leaf(1, 1, "b")}}}
	if _, ok := particleSubsumes(all, all); ok {
		t.Fatal("decided an all group instead of declining")
	}
}
