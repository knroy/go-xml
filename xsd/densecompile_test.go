package xsd

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
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

// NewSequenceMatcher is how dtd.Validate compiles a children model, and a DTD
// has no UPA gate in front of it: <!ELEMENT r (a?,a?,...)> was built in full,
// ~n²/2 follow edges with a linear dedup scan per edge, before the position
// budget refused it, and the build was repeated for every <r/>. The total
// follow representation is now bounded by maxSequenceMatcherEdges: a dense
// model's follow sets are distinct suffixes, one piece per edge, so sharing
// does not shrink it and it is still refused early.
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
	_, err := NewSequenceMatcher(dense(8192))
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), "follow entries") {
		t.Fatalf("an 8192-member dense model: err = %v, want the follow budget refusal", err)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 128<<20 {
		t.Errorf("refusing the dense model allocated %d MB, want under 128 MB", got>>20)
	}
	if _, err := NewSequenceMatcher(dense(1000)); err != nil {
		t.Errorf("a 1000-member dense model is within the edge budget and must compile: %v", err)
	}
}

// Nesting a fixed model in a chain of repeated group references adds no
// positions and no new edges: every level only re-lays the same loop-back
// edges. The compiler used to log each copy and fill a map per scope from its
// whole subtree's log, so the retained model grew by one copy of every edge
// per level — 216 MB at depth 32 from a 14.6 KB schema. Each edge is now
// recorded once, against the innermost scope it lies in.
func TestNestedScopesRecordEdgesOnce(t *testing.T) {
	nested := func(d int) *contentModel {
		ps := make([]*Particle, 20)
		for i := range ps {
			ps[i] = &Particle{MinOccurs: 1, MaxOccurs: 1,
				Term: &ElementDecl{Name: xdm.QName{Local: fmt.Sprintf("e%d", i)}}}
		}
		g := &ModelGroup{Compositor: CompositorChoice, Particles: ps}
		for i := 0; i < d; i++ {
			g = &ModelGroup{Compositor: CompositorSequence,
				Particles: []*Particle{{MinOccurs: 1, MaxOccurs: 2, Term: g}}}
		}
		m, err := compileContentModel(&Particle{MinOccurs: 1, MaxOccurs: 1, Term: g}, 0)
		if err != nil {
			t.Fatalf("depth %d: %v", d, err)
		}
		return m
	}
	shallow, deep := nested(2), nested(32)
	pieces := func(m *contentModel) (n int) {
		for _, ps := range m.pieces {
			n += len(ps)
		}
		return n
	}
	if got, want := pieces(deep), pieces(shallow); got != want {
		t.Errorf("depth 32 records %d scoped edges, depth 2 records %d: "+
			"the nesting is storing a copy of the edges per level", got, want)
	}
	// The counts are still charged at every level: two nested
	// maxOccurs="2" scopes admit four members and refuse a fifth.
	two := &SequenceMatcher{model: nested(2)}
	names := make([]xdm.QName, 5)
	for i := range names {
		names[i] = xdm.QName{Local: "e3"}
	}
	if ok, _ := two.Match(names[:4]); !ok {
		t.Error("four members fit two nested maxOccurs=\"2\" scopes, but were refused")
	}
	if ok, _ := two.Match(names); ok {
		t.Error("five members exceed two nested maxOccurs=\"2\" scopes, but were accepted")
	}
}

// A repeated choice of n alternatives has n follow sets of n, but every one
// is the choice's first set, so they share it: the stored representation is
// linear in n. Copying each set made (a1|...|a1100)* — deterministic, and
// legal in a DTD at any width — exceed the budget at about 1.2M edges. The
// follow sets the matcher and checkUPA see are unchanged.
func TestRepeatedChoiceSharesFollowSet(t *testing.T) {
	choice := func(n int) *Particle {
		ps := make([]*Particle, n)
		for i := range ps {
			ps[i] = &Particle{MinOccurs: 1, MaxOccurs: 1,
				Term: &ElementDecl{Name: xdm.QName{Local: fmt.Sprintf("a%d", i)}}}
		}
		return &Particle{MinOccurs: 0, MaxOccurs: Unbounded,
			Term: &ModelGroup{Compositor: CompositorChoice, Particles: ps}}
	}
	const n = 2000
	sm, err := NewSequenceMatcher(choice(n))
	if err != nil {
		t.Fatalf("(a0|...|a%d)* must compile: %v", n-1, err)
	}
	if got := sm.model.size; got > 3*n {
		t.Errorf("the repeated choice stored %d follow entries, want O(n) (at most %d)", got, 3*n)
	}
	names := []xdm.QName{{Local: "a1999"}, {Local: "a0"}, {Local: "a0"}, {Local: "a7"}}
	if ok, at := sm.Match(names); !ok {
		t.Errorf("a run of alternatives was refused at %d", at)
	}
	if ok, _ := sm.Match([]xdm.QName{{Local: "a0"}, {Local: "b"}}); ok {
		t.Error("a name outside the choice was accepted")
	}
	// The materialized follow sets are the full logical sets.
	m, err := compileForChecks(choice(50), 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range m.follow {
		if len(f) != 50 {
			t.Fatalf("follow[%d] has %d members, want 50", i, len(f))
		}
	}
}

// Sharing must not change the follow relation. For a set of models covering
// sequences, choices, nested repetition and overlapping loop-backs, the
// materialized follow sets, read as sets, equal the union of the pieces, the
// pieces of one position are disjoint and ascending, and innerEdge agrees with
// a direct scan of the pieces.
func TestFollowPiecesMatchFollowSets(t *testing.T) {
	el := func(name string, min, max int) *Particle {
		return &Particle{MinOccurs: min, MaxOccurs: max,
			Term: &ElementDecl{Name: xdm.QName{Local: name}}}
	}
	grp := func(c Compositor, min, max int, ps ...*Particle) *Particle {
		return &Particle{MinOccurs: min, MaxOccurs: max,
			Term: &ModelGroup{Compositor: c, Particles: ps}}
	}
	seq, cho := CompositorSequence, CompositorChoice
	models := []*Particle{
		// (a | (l, t?) | c)*: the loop-back from l must split around t.
		grp(cho, 0, Unbounded, el("a", 1, 1), grp(seq, 1, 1, el("l", 1, 1), el("t", 0, 1)), el("c", 1, 1)),
		// ((a|b)*, c?)*: nested loop-backs overlap.
		grp(seq, 0, Unbounded, grp(cho, 0, Unbounded, el("a", 1, 1), el("b", 1, 1)), el("c", 0, 1)),
		// (a*|b*|c*)*
		grp(cho, 0, Unbounded, el("a", 0, Unbounded), el("b", 0, Unbounded), el("c", 0, Unbounded)),
		// (a?, b?, c?){2,3} inside (x, ...)+
		grp(seq, 1, Unbounded, el("x", 1, 1), grp(seq, 2, 3, el("a", 0, 1), el("b", 0, 1), el("c", 0, 1))),
		// (((a, b?)*, c?)*, d?)*
		grp(seq, 0, Unbounded, grp(seq, 0, Unbounded, grp(seq, 0, Unbounded, el("a", 1, 1), el("b", 0, 1)), el("c", 0, 1)), el("d", 0, 1)),
	}
	for mi, p := range models {
		m, err := compileForChecks(p, 0, 0, 0)
		if err != nil {
			t.Fatalf("model %d: %v", mi, err)
		}
		for i := range m.positions {
			want := map[int]bool{}
			for _, x := range m.follow[i] {
				if want[x] {
					t.Errorf("model %d: follow[%d] lists %d twice", mi, i, x)
				}
				want[x] = true
			}
			got, prev := map[int]bool{}, -1
			for _, pc := range m.pieces[i] {
				for _, x := range pc.to {
					if x <= prev {
						t.Errorf("model %d: pieces of %d are not disjoint ascending runs", mi, i)
					}
					prev = x
					got[x] = true
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("model %d: pieces of %d hold %v, follow holds %v", mi, i, got, want)
			}
			for to := range m.positions {
				for c := -1; c < len(m.counters); c++ {
					scan := false
					for _, pc := range m.pieces[i] {
						if pc.scope == c && slices.Contains(pc.to, to) {
							scan = true
						}
					}
					if c >= 0 && m.innerEdge(c, i, to) != scan {
						t.Errorf("model %d: innerEdge(%d, %d, %d) disagrees with a scan", mi, c, i, to)
					}
				}
			}
		}
	}
}

// A DTD may write a repeated choice of identical alternatives, which compiles
// shared and is legal: <!ELEMENT r (a|a|...|a)*>. Its N alternatives are N
// live states after every <a/>, and each walked the same N-wide follow run,
// N² per child: 0.8 s per child at N=4096. Each run is walked once per child
// now, as the walk's result does not depend on which state reached it.
func TestSequenceMatcherIdenticalAlternativesLinear(t *testing.T) {
	const n, kids = 4096, 4
	ps := make([]*Particle, n)
	for i := range ps {
		ps[i] = &Particle{MinOccurs: 1, MaxOccurs: 1,
			Term: &ElementDecl{Name: xdm.QName{Local: "a"}}}
	}
	sm, err := NewSequenceMatcher(&Particle{MinOccurs: 0, MaxOccurs: Unbounded,
		Term: &ModelGroup{Compositor: CompositorChoice, Particles: ps}})
	if err != nil {
		t.Fatal(err)
	}
	names := slices.Repeat([]xdm.QName{{Local: "a"}}, kids)
	var ok bool
	stats := withStats(func() { ok, _ = sm.Match(names) })
	if !ok {
		t.Fatal("four <a/> match (a|a|...|a)*")
	}
	if got := stats.MatchSteps.Load(); got > 4*n*kids {
		t.Errorf("matching %d children examined %d follow members, want at most %d", kids, got, 4*n*kids)
	}
	if ok, at := sm.Match([]xdm.QName{{Local: "a"}, {Local: "b"}}); ok || at != 1 {
		t.Errorf("Match(a b) = %v at %d, want a rejection at 1", ok, at)
	}
}
