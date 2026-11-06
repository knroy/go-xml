package xsd

import (
	"errors"
	"runtime"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A dense content model must be refused before it is built in full. A
// sequence of 2048 optional elements fits the position budget, but its follow
// sets are as wide as the sequence, and checkUPA refuses any state wider than
// maxUPAStateWidth. The compile used to build every edge first: 229 MB and
// about half a second, for a refusal the width gate then made in
// microseconds. Bounding the build at the same width makes the refusal cost
// what the gate allows.
func TestDenseModelRefusedBeforeFullCompile(t *testing.T) {
	doc, err := xdm.ParseString(genOptionalSequence(2048), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, lerr := Load(doc.Root, "", Options{})
	runtime.ReadMemStats(&after)
	if !errors.Is(lerr, xdm.ErrResourceLimit) {
		t.Fatalf("Load = %v, want a refusal wrapping ErrResourceLimit", lerr)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 32<<20 {
		t.Errorf("refusing a 2048-wide dense model allocated %d MB, want under 32 MB: "+
			"the model was built in full before the width gate refused it", got>>20)
	}
	// The narrow end of the same shape still loads.
	doc, _ = xdm.ParseString(genOptionalSequence(200), xdm.ParseOptions{})
	if _, err := Load(doc.Root, "", Options{}); err != nil {
		t.Errorf("a 200-wide optional sequence is within maxUPAStateWidth and must load: %v", err)
	}
}

// The width bound belongs to the shared build, not only to the schema-check
// caller. NewSequenceMatcher is how dtd.Validate compiles a children model,
// and a DTD has no UPA gate in front of it: <!ELEMENT r (a?,a?,...)> was built
// in full, ~n²/2 follow edges with a linear dedup scan per edge, before the
// position budget refused it, and the build was repeated for every <r/>.
func TestSequenceMatcherRefusesDenseModel(t *testing.T) {
	dense := func(n int) *Particle {
		ps := make([]*Particle, n)
		for i := range ps {
			ps[i] = &Particle{MinOccurs: 0, MaxOccurs: 1,
				Term: &ElementDecl{Name: xdm.QName{Local: "a"}}}
		}
		return &Particle{MinOccurs: 1, MaxOccurs: 1,
			Term: &ModelGroup{Compositor: CompositorSequence, Particles: ps}}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := NewSequenceMatcher(dense(1024))
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("a 1024-wide dense model compiled; its follow sets exceed maxUPAStateWidth")
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 8<<20 {
		t.Errorf("refusing the dense model allocated %d MB, want under 8 MB", got>>20)
	}
	if _, err := NewSequenceMatcher(dense(200)); err != nil {
		t.Errorf("a 200-wide model is within maxUPAStateWidth and must compile: %v", err)
	}
}
