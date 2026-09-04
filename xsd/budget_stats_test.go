package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The declines in subsume.go and restrict.go are SOUND -- budget_soundness_test.go
// proves the conservative fallback never accepts what the exact answer rejects,
// and nothing here changes any of that. What they were not is OBSERVABLE:
// particleSubsumes and allBranchCounts answer (nil, false), the caller falls
// through to the 1.0 structural table, and a schema refused because a budget
// ran out is indistinguishable from one the table genuinely forbids.
//
// budgetStats closes that gap the way identity.go's icStats does: counters
// behind a hook, nil in every ordinary build, read by nothing that makes a
// decision. These tests pin two things at once -- the decline is counted, and
// the verdict is EXACTLY what it was before the counting existed.

// withStats runs fn with the decline counters attached, the way
// validateWithStats attaches the identity counters.
func withStats(fn func()) *budgetStats {
	st := &budgetStats{}
	budgetStatsHook = func() *budgetStats { return st }
	defer func() { budgetStatsHook = nil }()
	fn()
	return st
}

func seqParticle(names ...string) *Particle {
	ps := make([]*Particle, len(names))
	for i, n := range names {
		ps[i] = &Particle{MinOccurs: 1, MaxOccurs: 1,
			Term: &ElementDecl{Name: xdm.QName{Local: n}}}
	}
	return &Particle{MinOccurs: 1, MaxOccurs: 1,
		Term: &ModelGroup{Compositor: CompositorSequence, Particles: ps}}
}

// A budget decline is counted as a budget decline, and the verdict is the same
// (nil, false) it always was.
func TestSubsumeBudgetDeclineIsCounted(t *testing.T) {
	der, base := seqParticle("a", "b"), seqParticle("a")

	// The control. At the normal budgets the exact answer is computed, so
	// nothing is declined and nothing is counted. Without this the test
	// could pass against an implementation that counts every call.
	st := withStats(func() {
		if err, ok := particleSubsumes(der, base); !ok || err == nil {
			t.Fatalf("at the normal budget want (err!=nil, true), got (%v, %v)", err, ok)
		}
	})
	if n := st.SubsumeBudget.Load() + st.SubsumeStructural.Load(); n != 0 {
		t.Errorf("an exact answer counted %d declines, want 0", n)
	}

	for _, f := range []struct {
		name            string
		states, product int
	}{{"states=0", 0, -1}, {"product=0", -1, 0}} {
		t.Run(f.name, func(t *testing.T) {
			st := withStats(func() {
				withBudgets(-1, -1, f.states, f.product, func() {
					err, ok := particleSubsumes(der, base)
					// The verdict is unchanged: a decline, never an
					// acceptance. This is the same assertion
					// TestSubsumeDeclineIsNotAcceptance makes, repeated
					// here so that breaking it while adding observability
					// fails THIS test too.
					if ok || err != nil {
						t.Errorf("verdict changed: got (%v, %v), want (nil, false)", err, ok)
					}
				})
			})
			if st.SubsumeBudget.Load() == 0 {
				t.Error("a budget decline was not counted; a caller still " +
					"cannot learn that raising the budget would decide this")
			}
			if st.SubsumeStructural.Load() != 0 {
				t.Errorf("a budget decline was counted as structural (%d); the "+
					"two are separated precisely because only one is fixable "+
					"by raising a limit", st.SubsumeStructural.Load())
			}
		})
	}
}

// A structural decline is counted separately, because no budget affects it:
// an all group is outside what the unrolling can decide at all, and
// allSubsumes owns that cluster.
func TestSubsumeStructuralDeclineIsCountedSeparately(t *testing.T) {
	all := &Particle{MinOccurs: 1, MaxOccurs: 1,
		Term: &ModelGroup{Compositor: CompositorAll, Particles: []*Particle{
			{MinOccurs: 1, MaxOccurs: 1,
				Term: &ElementDecl{Name: xdm.QName{Local: "a"}}}}}}
	st := withStats(func() {
		if _, ok := particleSubsumes(all, all); ok {
			t.Error("verdict changed: an all group must still decline")
		}
	})
	if st.SubsumeStructural.Load() == 0 {
		t.Error("a structural decline was not counted")
	}
	if st.SubsumeBudget.Load() != 0 {
		t.Errorf("a structural decline was counted as a budget decline (%d); "+
			"a maintainer would raise a limit that cannot help",
			st.SubsumeBudget.Load())
	}
}

// The same two properties for restrict.go's branch enumeration.
func TestBranchCountDeclinesAreCounted(t *testing.T) {
	choice := &Particle{MinOccurs: 1, MaxOccurs: 1,
		Term: &ModelGroup{Compositor: CompositorChoice, Particles: []*Particle{
			{MinOccurs: 1, MaxOccurs: 1, Term: &ElementDecl{Name: xdm.QName{Local: "a"}}},
			{MinOccurs: 1, MaxOccurs: 1, Term: &ElementDecl{Name: xdm.QName{Local: "b"}}},
		}}}

	// Control: the normal budget enumerates exactly and counts nothing.
	st := withStats(func() {
		if _, ok := allBranchCounts(choice); !ok {
			t.Fatal("the normal budget must enumerate this")
		}
	})
	if n := st.BranchBudget.Load() + st.BranchStructural.Load(); n != 0 {
		t.Errorf("an exact enumeration counted %d declines, want 0", n)
	}

	st = withStats(func() {
		withBudgets(-1, 0, -1, -1, func() {
			if br, ok := allBranchCounts(choice); ok || br != nil {
				t.Errorf("verdict changed: got (%v, %v), want (nil, false)", br, ok)
			}
		})
	})
	if st.BranchBudget.Load() == 0 {
		t.Error("a branchLimit decline was not counted")
	}

	// A wildcard has no per-name count at any budget.
	wild := &Particle{MinOccurs: 1, MaxOccurs: 1, Term: &Wildcard{}}
	st = withStats(func() {
		if br, ok := allBranchCounts(wild); ok || br != nil {
			t.Errorf("verdict changed: got (%v, %v), want (nil, false)", br, ok)
		}
	})
	if st.BranchStructural.Load() == 0 {
		t.Error("a wildcard decline was not counted as structural")
	}
	if st.BranchBudget.Load() != 0 {
		t.Errorf("a wildcard decline was counted as a budget decline (%d)",
			st.BranchBudget.Load())
	}
}

// The counters must cost nothing and change nothing when no one is watching,
// which is the property that lets them sit on a hot path. With the hook nil --
// every ordinary build -- the verdicts are identical to those measured above.
func TestDeclinesAreUnchangedWithNoHook(t *testing.T) {
	if budgetStatsHook != nil {
		t.Fatal("the hook leaked from another test")
	}
	der, base := seqParticle("a", "b"), seqParticle("a")
	if err, ok := particleSubsumes(der, base); !ok || err == nil {
		t.Errorf("got (%v, %v), want (err!=nil, true)", err, ok)
	}
	withBudgets(-1, -1, 0, -1, func() {
		if err, ok := particleSubsumes(der, base); ok || err != nil {
			t.Errorf("got (%v, %v), want (nil, false)", err, ok)
		}
	})
}
