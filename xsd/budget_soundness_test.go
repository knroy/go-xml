package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The invariant this file enforces.
//
// Three internal budgets bound work in this package: maxPositions
// (automaton.go), branchLimit (restrict.go), and subsumeMaxStates /
// subsumeMaxProduct (subsume.go). Exceeding one means the exact procedure
// DECLINED to decide. It never means "no violation was found".
//
// Soundness is therefore one-directional:
//
//	fallback ACCEPTS  =>  the exact answer is also ACCEPT
//
// The converse is allowed. A conservative fallback may reject something a
// full computation would have admitted; that is a false reject, which is
// safe. What must never happen is a false ACCEPT — an invalid document or an
// illegal derivation passing because a budget ran out.
//
// Reading the code cannot settle this, because the failure mode is an error
// that is returned correctly and then swallowed by some caller higher up. So
// the budgets are package-level vars (never assigned in production) and these
// tests force them pathologically low, so that EVERY input exceeds them, then
// compare the forced verdict against the normal one on the same input.
//
// Both valid and invalid inputs are required. A suite of valid inputs alone
// cannot distinguish a sound fallback from one that accepts everything, which
// is the exact bug being hunted.

// withBudgets runs fn with the four budgets set to the given values, restoring
// them afterwards. A value of -1 leaves that budget alone.
func withBudgets(positions, branches, states, product int, fn func()) {
	op, ob, os, opr := maxPositions, branchLimit, subsumeMaxStates, subsumeMaxProduct
	defer func() {
		maxPositions, branchLimit, subsumeMaxStates, subsumeMaxProduct = op, ob, os, opr
	}()
	if positions >= 0 {
		maxPositions = positions
	}
	if branches >= 0 {
		branchLimit = branches
	}
	if states >= 0 {
		subsumeMaxStates = states
	}
	if product >= 0 {
		subsumeMaxProduct = product
	}
	fn()
}

// loadAndValidate reports whether doc is schema-valid against src, treating a
// failure to load the schema as a rejection too: a schema that will not
// compile accepts nothing.
func loadAndValidate(src, instance string) (accepted bool, why string) {
	sdoc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		return false, "schema parse: " + err.Error()
	}
	s, err := Load(sdoc.Root, "", Options{})
	if err != nil {
		return false, "schema load: " + err.Error()
	}
	idoc, err := xdm.ParseString(instance, xdm.ParseOptions{})
	if err != nil {
		return false, "instance parse: " + err.Error()
	}
	if err := s.Validate(idoc.Root, ValidateOptions{}); err != nil {
		return false, "validate: " + err.Error()
	}
	return true, ""
}

// loadSchema reports whether a schema is accepted at all. Restriction and
// subsumption checks run at schema-assembly time, so this is the verdict those
// budgets influence.
func loadSchema(src string, v Version) (accepted bool, why string) {
	sdoc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		return false, "parse: " + err.Error()
	}
	if _, err := Load(sdoc.Root, "", Options{Version: v}); err != nil {
		return false, "load: " + err.Error()
	}
	return true, ""
}

// ---------------------------------------------------------------------------
// Corpus
// ---------------------------------------------------------------------------

// contentModelCase is a schema plus an instance whose verdict is cheap to
// compute at the normal budget.
type contentModelCase struct {
	name     string
	schema   string
	instance string
	// valid records the verdict expected at the normal budget. It is
	// asserted, so a case that is mislabelled fails loudly rather than
	// weakening the differential comparison.
	valid bool
}

func wrap(body string) string {
	return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` + body + `</xs:schema>`
}

// contentModelCases exercise the automaton built under maxPositions. Each
// shape appears with an instance that satisfies it and one that does not.
func contentModelCases() []contentModelCase {
	const seq = `<xs:element name="r"><xs:complexType><xs:sequence>
	  <xs:element name="a" type="xs:string"/>
	  <xs:element name="b" type="xs:string" minOccurs="0"/>
	  <xs:element name="c" type="xs:string" maxOccurs="3"/>
	</xs:sequence></xs:complexType></xs:element>`

	const choice = `<xs:element name="r"><xs:complexType><xs:choice>
	  <xs:element name="a" type="xs:string"/>
	  <xs:element name="b" type="xs:string"/>
	</xs:choice></xs:complexType></xs:element>`

	const nested = `<xs:element name="r"><xs:complexType><xs:sequence maxOccurs="2">
	  <xs:choice>
	    <xs:element name="a" type="xs:string"/>
	    <xs:sequence>
	      <xs:element name="b" type="xs:string"/>
	      <xs:element name="c" type="xs:string" minOccurs="0"/>
	    </xs:sequence>
	  </xs:choice>
	</xs:sequence></xs:complexType></xs:element>`

	const wildcard = `<xs:element name="r"><xs:complexType><xs:sequence>
	  <xs:element name="a" type="xs:string"/>
	  <xs:any namespace="##any" processContents="skip" minOccurs="0"/>
	</xs:sequence></xs:complexType></xs:element>`

	const allg = `<xs:element name="r"><xs:complexType><xs:all>
	  <xs:element name="a" type="xs:string"/>
	  <xs:element name="b" type="xs:string" minOccurs="0"/>
	</xs:all></xs:complexType></xs:element>`

	const empty = `<xs:element name="r"><xs:complexType/></xs:element>`

	return []contentModelCase{
		{"seq/valid-min", wrap(seq), `<r><a/><c/></r>`, true},
		{"seq/valid-full", wrap(seq), `<r><a/><b/><c/><c/><c/></r>`, true},
		{"seq/missing-a", wrap(seq), `<r><c/></r>`, false},
		{"seq/missing-c", wrap(seq), `<r><a/><b/></r>`, false},
		{"seq/too-many-c", wrap(seq), `<r><a/><c/><c/><c/><c/></r>`, false},
		{"seq/out-of-order", wrap(seq), `<r><c/><a/></r>`, false},
		{"seq/undeclared", wrap(seq), `<r><a/><c/><z/></r>`, false},
		{"seq/empty", wrap(seq), `<r/>`, false},

		{"choice/a", wrap(choice), `<r><a/></r>`, true},
		{"choice/b", wrap(choice), `<r><b/></r>`, true},
		{"choice/both", wrap(choice), `<r><a/><b/></r>`, false},
		{"choice/neither", wrap(choice), `<r/>`, false},

		{"nested/one", wrap(nested), `<r><a/></r>`, true},
		{"nested/bc", wrap(nested), `<r><b/><c/></r>`, true},
		{"nested/two-reps", wrap(nested), `<r><a/><b/></r>`, true},
		{"nested/three-reps", wrap(nested), `<r><a/><a/><a/></r>`, false},
		{"nested/c-without-b", wrap(nested), `<r><c/></r>`, false},

		{"wildcard/bare", wrap(wildcard), `<r><a/></r>`, true},
		{"wildcard/filled", wrap(wildcard), `<r><a/><zz/></r>`, true},
		{"wildcard/two", wrap(wildcard), `<r><a/><zz/><yy/></r>`, false},
		{"wildcard/no-a", wrap(wildcard), `<r><zz/></r>`, false},

		{"all/both", wrap(allg), `<r><b/><a/></r>`, true},
		{"all/one", wrap(allg), `<r><a/></r>`, true},
		{"all/missing", wrap(allg), `<r><b/></r>`, false},
		{"all/dup", wrap(allg), `<r><a/><a/></r>`, false},

		{"empty/ok", wrap(empty), `<r/>`, true},
		{"empty/child", wrap(empty), `<r><a/></r>`, false},
	}
}

// TestContentModelBudgetSoundness forces maxPositions so low that every model
// with even one element particle is declined, and asserts the fallback never
// accepts what the exact path rejects.
func TestContentModelBudgetSoundness(t *testing.T) {
	for _, c := range contentModelCases() {
		t.Run(c.name, func(t *testing.T) {
			exact, exactWhy := loadAndValidate(c.schema, c.instance)
			if exact != c.valid {
				t.Fatalf("case is mislabelled: at the normal budget got accepted=%v (%s), want %v",
					exact, exactWhy, c.valid)
			}
			for _, limit := range []int{0, 1, 2} {
				var forced bool
				var why string
				withBudgets(limit, -1, -1, -1, func() {
					forced, why = loadAndValidate(c.schema, c.instance)
				})
				if forced && !exact {
					t.Errorf("UNSOUND: maxPositions=%d accepted a document the exact path rejects\n"+
						"  schema:   %s\n  instance: %s\n  exact verdict: reject (%s)\n"+
						"  budgeted verdict: ACCEPT", limit, c.schema, c.instance, exactWhy)
				}
				_ = why
			}
		})
	}
}

// TestSequenceMatcherBudgetSoundness covers the second caller of
// compileContentModel, which returns a bare yes-or-no with nowhere to put an
// error and so must turn an undecidable model into a rejection.
func TestSequenceMatcherBudgetSoundness(t *testing.T) {
	p := &Particle{MinOccurs: 1, MaxOccurs: 1, Term: &ModelGroup{
		Compositor: CompositorSequence,
		Particles: []*Particle{
			{MinOccurs: 1, MaxOccurs: 1, Term: &ElementDecl{Name: xdm.QName{Local: "a"}}},
			{MinOccurs: 0, MaxOccurs: 1, Term: &ElementDecl{Name: xdm.QName{Local: "b"}}},
		},
	}}
	names := func(ns ...string) []xdm.QName {
		out := make([]xdm.QName, len(ns))
		for i, n := range ns {
			out[i] = xdm.QName{Local: n}
		}
		return out
	}
	inputs := [][]xdm.QName{names("a"), names("a", "b"), names(), names("b"), names("a", "a"), names("z")}

	exact := map[int]bool{}
	m, err := NewSequenceMatcher(p)
	if err != nil {
		t.Fatalf("compiling at the normal budget: %v", err)
	}
	for i, in := range inputs {
		ok, _ := m.Match(in)
		exact[i] = ok
	}

	withBudgets(1, -1, -1, -1, func() {
		fm, err := NewSequenceMatcher(p)
		if err != nil {
			// Declining to build at all is the conservative answer:
			// the caller gets no matcher, so nothing is accepted.
			return
		}
		for i, in := range inputs {
			ok, _ := fm.Match(in)
			if ok && !exact[i] {
				t.Errorf("UNSOUND: SequenceMatcher accepted %v under a forced budget; exact path rejects it", in)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Restriction / branch enumeration
// ---------------------------------------------------------------------------

// derivationCase is a schema whose acceptance turns on a restriction check.
type derivationCase struct {
	name   string
	schema string
	valid  bool
	ver    Version
}

func restrictionSchema(baseBody, derBody string) string {
	return wrap(`
	  <xs:complexType name="B">` + baseBody + `</xs:complexType>
	  <xs:complexType name="D">
	    <xs:complexContent>
	      <xs:restriction base="B">` + derBody + `</xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="r" type="D"/>`)
}

func derivationCases() []derivationCase {
	el := func(n, occ string) string {
		return `<xs:element name="` + n + `" type="xs:string" ` + occ + `/>`
	}
	baseAll := `<xs:all>` + el("a", `minOccurs="0" maxOccurs="1"`) + el("b", `minOccurs="0" maxOccurs="1"`) + `</xs:all>`
	baseSeq := `<xs:sequence>` + el("a", `minOccurs="0" maxOccurs="3"`) + el("b", `minOccurs="0" maxOccurs="2"`) + `</xs:sequence>`

	return []derivationCase{
		// An all group restricted by a choice: each branch must fit the
		// base's per-name budgets. This is the shape allBranchCounts
		// exists for.
		{"all-by-choice/ok", restrictionSchema(baseAll,
			`<xs:choice>`+el("a", `minOccurs="0"`)+el("b", `minOccurs="0"`)+`</xs:choice>`), true, Version11},
		// A branch exceeding the base's maxOccurs for a name must be
		// rejected. "a" twice against a base allowing one.
		{"all-by-choice/over-budget", restrictionSchema(baseAll,
			`<xs:choice><xs:sequence>`+el("a", `minOccurs="2" maxOccurs="2"`)+`</xs:sequence>`+
				el("b", `minOccurs="0"`)+`</xs:choice>`), false, Version11},
		{"all-by-seq/ok", restrictionSchema(baseAll,
			`<xs:sequence>`+el("a", `minOccurs="0"`)+el("b", `minOccurs="0"`)+`</xs:sequence>`), true, Version11},
		{"all-by-seq/dup-name", restrictionSchema(baseAll,
			`<xs:sequence>`+el("a", `minOccurs="1" maxOccurs="1"`)+el("a", `minOccurs="1" maxOccurs="1"`)+`</xs:sequence>`), false, Version11},
		{"all-by-seq/undeclared", restrictionSchema(baseAll,
			`<xs:sequence>`+el("z", `minOccurs="1"`)+`</xs:sequence>`), false, Version11},

		// Sequence bases, which land in the 1.0 table and the
		// subsumption check rather than branch counting.
		{"seq/narrowed", restrictionSchema(baseSeq,
			`<xs:sequence>`+el("a", `minOccurs="0" maxOccurs="1"`)+el("b", `minOccurs="0" maxOccurs="1"`)+`</xs:sequence>`), true, Version11},
		{"seq/widened", restrictionSchema(baseSeq,
			`<xs:sequence>`+el("a", `minOccurs="0" maxOccurs="9"`)+el("b", `minOccurs="0" maxOccurs="1"`)+`</xs:sequence>`), false, Version11},
		{"seq/reordered", restrictionSchema(baseSeq,
			`<xs:sequence>`+el("b", `minOccurs="1" maxOccurs="1"`)+el("a", `minOccurs="1" maxOccurs="1"`)+`</xs:sequence>`), false, Version11},
		{"seq/new-name", restrictionSchema(baseSeq,
			`<xs:sequence>`+el("a", `minOccurs="0" maxOccurs="1"`)+el("q", `minOccurs="1"`)+`</xs:sequence>`), false, Version11},
		{"seq/choice-of-base", restrictionSchema(baseSeq,
			`<xs:choice>`+el("a", `minOccurs="1" maxOccurs="1"`)+`</xs:choice>`), true, Version11},

		// The same shapes under 1.0, where the structural table is the
		// only decision procedure.
		{"seq10/narrowed", restrictionSchema(baseSeq,
			`<xs:sequence>`+el("a", `minOccurs="0" maxOccurs="1"`)+el("b", `minOccurs="0" maxOccurs="1"`)+`</xs:sequence>`), true, Version10},
		{"seq10/widened", restrictionSchema(baseSeq,
			`<xs:sequence>`+el("a", `minOccurs="0" maxOccurs="9"`)+el("b", `minOccurs="0" maxOccurs="1"`)+`</xs:sequence>`), false, Version10},
	}
}

// TestRestrictionBudgetSoundness forces branchLimit and the subsumption caps
// to zero, so every enumeration and every unrolling declines, and asserts no
// derivation is accepted that the exact path rejects.
func TestRestrictionBudgetSoundness(t *testing.T) {
	for _, c := range derivationCases() {
		t.Run(c.name, func(t *testing.T) {
			exact, exactWhy := loadSchema(c.schema, c.ver)
			if exact != c.valid {
				t.Fatalf("case is mislabelled: at the normal budget got accepted=%v (%s), want %v",
					exact, exactWhy, c.valid)
			}
			forcings := []struct {
				name                                 string
				positions, branches, states, product int
			}{
				{"branchLimit=0", -1, 0, -1, -1},
				{"subsumeMaxStates=0", -1, -1, 0, -1},
				{"subsumeMaxProduct=0", -1, -1, -1, 0},
				{"all-budgets=0", -1, 0, 0, 0},
			}
			for _, f := range forcings {
				var forced bool
				withBudgets(f.positions, f.branches, f.states, f.product, func() {
					forced, _ = loadSchema(c.schema, c.ver)
				})
				if forced && !exact {
					t.Errorf("UNSOUND: with %s the schema was ACCEPTED, but the exact path rejects it\n"+
						"  schema: %s\n  exact verdict: reject (%s)", f.name, c.schema, exactWhy)
				}
			}
		})
	}
}

// TestSubsumeDeclineIsNotAcceptance pins the contract of particleSubsumes
// directly: under a forced budget it must report ok=false ("declined"), never
// (nil, true) ("no violation"). A decline with a nil error and ok=true would be
// read by restrict.go:360 as a clean pass.
func TestSubsumeDeclineIsNotAcceptance(t *testing.T) {
	mk := func(names ...string) *Particle {
		ps := make([]*Particle, len(names))
		for i, n := range names {
			ps[i] = &Particle{MinOccurs: 1, MaxOccurs: 1, Term: &ElementDecl{Name: xdm.QName{Local: n}}}
		}
		return &Particle{MinOccurs: 1, MaxOccurs: 1,
			Term: &ModelGroup{Compositor: CompositorSequence, Particles: ps}}
	}
	// A derived model that admits a word the base does not: the exact
	// answer is a counterexample, i.e. an error with ok=true.
	der, base := mk("a", "b"), mk("a")
	if err, ok := particleSubsumes(der, base); !ok || err == nil {
		t.Fatalf("at the normal budget want (err!=nil, true), got (%v, %v)", err, ok)
	}
	for _, f := range []struct {
		name            string
		states, product int
	}{{"states=0", 0, -1}, {"product=0", -1, 0}} {
		withBudgets(-1, -1, f.states, f.product, func() {
			err, ok := particleSubsumes(der, base)
			if ok && err == nil {
				t.Errorf("UNSOUND: with %s particleSubsumes reported a clean pass; "+
					"a budget-exceeded check must decline (ok=false)", f.name)
			}
		})
	}
}

// TestBranchCountsDeclineIsNotAcceptance pins allBranchCounts the same way: a
// declined enumeration must be (nil, false), never an empty-but-successful
// ([]branchCount{}, true), which the caller would read as "every branch fits".
func TestBranchCountsDeclineIsNotAcceptance(t *testing.T) {
	p := &Particle{MinOccurs: 1, MaxOccurs: 1, Term: &ModelGroup{
		Compositor: CompositorChoice,
		Particles: []*Particle{
			{MinOccurs: 1, MaxOccurs: 1, Term: &ElementDecl{Name: xdm.QName{Local: "a"}}},
			{MinOccurs: 1, MaxOccurs: 1, Term: &ElementDecl{Name: xdm.QName{Local: "b"}}},
		},
	}}
	if brs, ok := allBranchCounts(p); !ok || len(brs) != 2 {
		t.Fatalf("at the normal budget want two branches, got %d (ok=%v)", len(brs), ok)
	}
	withBudgets(-1, 0, -1, -1, func() {
		brs, ok := allBranchCounts(p)
		if ok {
			t.Errorf("UNSOUND: with branchLimit=0 allBranchCounts reported success with %d branches; "+
				"an exceeded budget must decline (ok=false), because the caller reads "+
				"ok=true as \"every branch was checked\"", len(brs))
		}
		if brs != nil {
			t.Errorf("a declined enumeration must return a nil slice, got %v", brs)
		}
	})
}

// ---------------------------------------------------------------------------
// Boundaries
// ---------------------------------------------------------------------------

// wideModel builds a sequence of n optional element particles, so the compiled
// model has exactly n positions.
func wideModel(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:element name="r"><xs:complexType><xs:sequence>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:element name="e%d" type="xs:string" minOccurs="0"/>`, i)
	}
	b.WriteString(`</xs:sequence></xs:complexType></xs:element>`)
	return wrap(b.String())
}

// TestMaxPositionsBoundary checks the edges of the position budget with both a
// document the model admits and one it does not. Below the limit both verdicts
// must be exact; at or above it the model is declined, and the decline must
// reject rather than accept.
func TestMaxPositionsBoundary(t *testing.T) {
	// The budget is lowered rather than building an 8192-particle schema,
	// because the comparison of interest is n against the limit, and which
	// absolute number plays the role of the limit does not change the code
	// path. The real 8192 is covered by the sweep below.
	const limit = 64
	for _, n := range []int{limit - 1, limit, limit + 1} {
		schema := wideModel(n)
		good := `<r><e0/></r>`
		bad := `<r><nope/></r>`
		for _, tc := range []struct {
			label    string
			instance string
			valid    bool
		}{{"admitted", good, true}, {"refused", bad, false}} {
			name := fmt.Sprintf("n=%d/%s", n, tc.label)
			t.Run(name, func(t *testing.T) {
				exact, why := loadAndValidate(schema, tc.instance)
				if exact != tc.valid {
					t.Fatalf("normal budget: accepted=%v (%s), want %v", exact, why, tc.valid)
				}
				var forced bool
				withBudgets(limit, -1, -1, -1, func() {
					forced, _ = loadAndValidate(schema, tc.instance)
				})
				switch {
				case n <= limit && forced != exact:
					t.Errorf("at or below the limit the verdict must be exact: got %v, want %v", forced, exact)
				case n > limit && forced:
					t.Errorf("UNSOUND: at n=%d with a limit of %d the model exceeds the budget, "+
						"yet the document was ACCEPTED", n, limit)
				}
			})
		}
	}
}

// TestMaxPositionsRealBoundary drives the guard at its edges. The guard reads
// len(m.positions) >= maxPositions *before* the position is appended, so
// exactly maxPositions compile and maxPositions+1 declines. What matters is
// that the decline is an error and a nil model: a truncated model would
// silently admit sequences the schema forbids.
//
// The edges are driven at a forced budget rather than the production 8192.
// compileContentModel is a Glushkov construction whose follow-set cost grows
// as the cube of the particle count -- measured on an idle 12-core laptop,
// 1024 particles compile in 0.16s, 2048 in 1.4s, 4096 in 12s and 8192 in 90s,
// each doubling costing about nine times the last. Three models at ~8k
// therefore cost about four and a half minutes on a fast machine and, under
// -race on a two-core CI runner, long enough that the whole `go test` run
// blew its 25m deadline and the build reported a panic rather than a result.
// That is what broke CI from 84735c8 onward.
//
// The off-by-one being asserted is a property of the comparison, not of the
// constant's magnitude: at a forced budget of 64 the same three cases run in
// microseconds and fail in exactly the same way if the >= is ever written as
// >. TestMaxPositionsProductionValue below pins the production number, so a
// change to it still has to be deliberate.
func TestMaxPositionsRealBoundary(t *testing.T) {
	const limit = 64
	withBudgets(limit, -1, -1, -1, func() {
		for _, n := range []int{limit - 1, limit, limit + 1} {
			t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
				ps := make([]*Particle, n)
				for i := range ps {
					ps[i] = &Particle{MinOccurs: 0, MaxOccurs: 1,
						Term: &ElementDecl{Name: xdm.QName{Local: fmt.Sprintf("e%d", i)}}}
				}
				p := &Particle{MinOccurs: 1, MaxOccurs: 1,
					Term: &ModelGroup{Compositor: CompositorSequence, Particles: ps}}
				m, err := compileContentModel(p)
				if n <= limit {
					if err != nil {
						t.Fatalf("n=%d is within the budget but failed: %v", n, err)
					}
					if len(m.positions) != n {
						t.Fatalf("built %d positions, want %d", len(m.positions), n)
					}
					return
				}
				if err == nil {
					t.Fatalf("n=%d exceeds maxPositions=%d but compiled with %d positions; "+
						"a truncated model would admit sequences the schema forbids",
						n, maxPositions, len(m.positions))
				}
				if m != nil {
					t.Errorf("a declined build must return a nil model, got %p", m)
				}
			})
		}
	})
}

// TestMaxPositionsProductionValue pins the shipped budget. The boundary above
// is driven at a forced value because the production one is too expensive to
// compile three times; this is what keeps that substitution honest, so that
// lowering the real budget is a deliberate edit rather than a silent one.
func TestMaxPositionsProductionValue(t *testing.T) {
	if maxPositions != 8192 {
		t.Errorf("maxPositions is %d, want 8192; if this change is deliberate, "+
			"update this test and docs/options.md", maxPositions)
	}
}

// TestBranchLimitBoundary drives the production branchLimit at its edges. A
// choice of n alternatives yields exactly n branches.
func TestBranchLimitBoundary(t *testing.T) {
	for _, n := range []int{4095, 4096, 4097} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			ps := make([]*Particle, n)
			for i := range ps {
				ps[i] = &Particle{MinOccurs: 1, MaxOccurs: 1,
					Term: &ElementDecl{Name: xdm.QName{Local: fmt.Sprintf("e%d", i)}}}
			}
			p := &Particle{MinOccurs: 1, MaxOccurs: 1,
				Term: &ModelGroup{Compositor: CompositorChoice, Particles: ps}}
			brs, ok := allBranchCounts(p)
			// The check is len(out) > branchLimit *after* appending,
			// so n up to and including the limit succeeds.
			if n <= branchLimit {
				if !ok {
					t.Fatalf("n=%d is within branchLimit=%d but was declined", n, branchLimit)
				}
				if len(brs) != n {
					t.Fatalf("got %d branches, want %d", len(brs), n)
				}
				return
			}
			if ok {
				t.Fatalf("n=%d exceeds branchLimit=%d but the enumeration reported success "+
					"with %d branches; the caller would read that as a completed check",
					n, branchLimit, len(brs))
			}
			if brs != nil {
				t.Errorf("a declined enumeration must return nil, got a %d-element slice", len(brs))
			}
		})
	}
}

// upa.go:178 skips the UPA and EDC checks for a model it cannot compile.
// That is a check being SKIPPED on a budget failure, which would be unsound
// if such a type could then validate a document. It cannot: the same
// compile failure recurs at validate.go:985, which fails the element. This
// pins that, so the "continue" stays paired with the rejection that
// justifies it.
func TestUPASkipStillRejectsDocuments(t *testing.T) {
	// An ambiguous model: UPA would normally reject this schema outright.
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="r"><xs:complexType><xs:sequence>
	    <xs:choice>
	      <xs:element name="a" type="xs:string"/>
	      <xs:element name="a" type="xs:string"/>
	    </xs:choice>
	  </xs:sequence></xs:complexType></xs:element>
	</xs:schema>`
	sdoc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	idoc, err := xdm.ParseString(`<r><a/></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse instance: %v", err)
	}

	op := maxPositions
	defer func() { maxPositions = op }()
	maxPositions = 0

	s, lerr := Load(sdoc.Root, "", Options{})
	if lerr != nil {
		t.Logf("schema refused at load under the forced budget: %v", lerr)
		return
	}
	// The schema loaded because UPA was skipped. Validation must still fail.
	verr := s.Validate(idoc.Root, ValidateOptions{})
	if verr == nil {
		t.Fatalf("UNSOUND: the UPA check was skipped for an uncompilable model, " +
			"and a document then validated against it with no error")
	}
	if !strings.Contains(verr.Error(), "content model") {
		t.Logf("rejected, though not by the compile failure: %v", verr)
	}
}

// ---------------------------------------------------------------------------
// UPA scan budget
// ---------------------------------------------------------------------------

// withUPABudgets runs fn with the two UPA budgets set to the given values,
// restoring them afterwards. A value of -1 leaves that budget alone. It is
// separate from withBudgets because the UPA budgets bound a *check*, not a
// construction: the four budgets above all decline to build something, whereas
// these decline to examine something already built.
func withUPABudgets(width, pairs int, fn func()) {
	ow, op := maxUPAStateWidth, maxUPAPairTests
	defer func() { maxUPAStateWidth, maxUPAPairTests = ow, op }()
	if width >= 0 {
		maxUPAStateWidth = width
	}
	if pairs >= 0 {
		maxUPAPairTests = pairs
	}
	fn()
}

// upaCases are schemas whose acceptance turns on the UPA check: each ambiguous
// shape paired with the unambiguous shape it is one edit away from. Both
// polarities are required, because a suite of valid schemas alone cannot tell a
// sound skip from one that has stopped checking anything.
func upaCases() []derivationCase {
	body := func(inner string) string {
		return wrap(`<xs:element name="r"><xs:complexType>` + inner +
			`</xs:complexType></xs:element>`)
	}
	return []derivationCase{
		// Two branches of a choice on the same element name: the
		// textbook ambiguity, and the shape checkUPA exists to reject.
		{"upa/choice-same-name", body(`<xs:choice>
		  <xs:element name="a" type="xs:string"/>
		  <xs:element name="a" type="xs:string"/>
		</xs:choice>`), false, Version10},
		{"upa/choice-distinct", body(`<xs:choice>
		  <xs:element name="a" type="xs:string"/>
		  <xs:element name="b" type="xs:string"/>
		</xs:choice>`), true, Version10},

		// An optional element followed by the same name: the automaton
		// cannot say which particle the first "a" belongs to.
		{"upa/optional-then-same", body(`<xs:sequence>
		  <xs:element name="a" type="xs:string" minOccurs="0"/>
		  <xs:element name="a" type="xs:string"/>
		</xs:sequence>`), false, Version10},
		{"upa/optional-then-other", body(`<xs:sequence>
		  <xs:element name="a" type="xs:string" minOccurs="0"/>
		  <xs:element name="b" type="xs:string"/>
		</xs:sequence>`), true, Version10},

		// Two overlapping wildcards, which compete under both versions.
		{"upa/two-wildcards", body(`<xs:choice>
		  <xs:any namespace="##any" processContents="skip"/>
		  <xs:any namespace="##any" processContents="skip"/>
		</xs:choice>`), false, Version10},

		// Element against wildcard: ambiguous under 1.0, resolved in
		// favour of the element under 1.1. Both polarities of the same
		// text, so a skip that ignored Version would show up here.
		{"upa/element-vs-wildcard-10", body(`<xs:choice>
		  <xs:element name="a" type="xs:string"/>
		  <xs:any namespace="##any" processContents="skip"/>
		</xs:choice>`), false, Version10},
		{"upa/element-vs-wildcard-11", body(`<xs:choice>
		  <xs:element name="a" type="xs:string"/>
		  <xs:any namespace="##any" processContents="skip"/>
		</xs:choice>`), true, Version11},
	}
}

// TestUPABudgetSoundness forces the UPA budgets so low that every state is
// declined, and asserts the skip is only ever MORE permissive.
//
// This is the one direction that matters. A skipped UPA check makes an
// ambiguous schema load, which is a false ACCEPT of a *schema* — but the
// invariant this file enforces is stated over the budgeted path against the
// exact path, and the guarantee is that the budget never turns an accepted
// schema into a rejected one. A budget that could reject is a budget that
// changes the set of schemas that load, which is a conformance failure; the
// permissive direction is a declined check, which is what the skip is.
//
// TestUPASkipStillRejectsDocuments above is the companion for the other half:
// where a skip arises from a model that would not compile, documents are still
// refused at validate time.
func TestUPABudgetSoundness(t *testing.T) {
	for _, c := range upaCases() {
		t.Run(c.name, func(t *testing.T) {
			exact, exactWhy := loadSchema(c.schema, c.ver)
			if exact != c.valid {
				t.Fatalf("case is mislabelled: at the normal budget got accepted=%v (%s), want %v",
					exact, exactWhy, c.valid)
			}
			forcings := []struct {
				name         string
				width, pairs int
			}{
				{"maxUPAStateWidth=0", 0, -1},
				{"maxUPAStateWidth=1", 1, -1},
				{"maxUPAPairTests=0", -1, 0},
				{"both=0", 0, 0},
			}
			for _, f := range forcings {
				var forced bool
				var why string
				withUPABudgets(f.width, f.pairs, func() {
					forced, why = loadSchema(c.schema, c.ver)
				})
				// The load direction: a budget must never turn a
				// schema that loads into one that does not.
				if exact && !forced {
					t.Errorf("UNSOUND IN THE REJECT DIRECTION: with %s the schema was REJECTED (%s), "+
						"but the exact path accepts it. A budget must decline a check, "+
						"never fail one.\n  schema: %s", f.name, why, c.schema)
				}
			}
		})
	}
}

// TestUPABudgetDeclineIsRecorded pins that a skip is observable. A declined
// check and a clean one both return nil from checkUPA, so without the record on
// the model there is no way for a caller or a test to tell "this model is
// unambiguous" from "nobody looked". The two tests below depend on this being
// true, and so does the claim in docs/security.md.
func TestUPABudgetDeclineIsRecorded(t *testing.T) {
	m := compileWideOptional(t, 8)
	if err := checkUPA(m, "t", CheckOptions{}); err != nil {
		t.Fatalf("a sequence of distinct optional elements is unambiguous: %v", err)
	}
	if m.upaSkipped {
		t.Fatalf("at the production budget a width-8 model must not be skipped")
	}
	withUPABudgets(2, -1, func() {
		m := compileWideOptional(t, 8)
		if err := checkUPA(m, "t", CheckOptions{}); err != nil {
			t.Fatalf("a declined check must not report a violation: %v", err)
		}
		if !m.upaSkipped {
			t.Errorf("with maxUPAStateWidth=2 a width-8 model was scanned in full; " +
				"the budget did not fire, so nothing bounds the triangular scan")
		}
	})
}

// compileWideOptional builds a sequence of n optional element particles. Its
// follow relation is dense — every position may follow every earlier one — so
// its states are as wide as the position count, which is what makes it the
// worst case for checkUPA's triangular scan.
func compileWideOptional(t *testing.T, n int) *contentModel {
	t.Helper()
	ps := make([]*Particle, n)
	for i := range ps {
		ps[i] = &Particle{MinOccurs: 0, MaxOccurs: 1,
			Term: &ElementDecl{Name: xdm.QName{Local: fmt.Sprintf("e%d", i)}}}
	}
	m, err := compileContentModel(&Particle{MinOccurs: 1, MaxOccurs: 1,
		Term: &ModelGroup{Compositor: CompositorSequence, Particles: ps}})
	if err != nil {
		t.Fatalf("compiling %d optional particles: %v", n, err)
	}
	return m
}

// TestUPABudgetFiresAndSchemaStillLoads drives the adversarial shape and pins
// both halves of the policy: the budget FIRES, and the schema still LOADS.
//
// The second half is the load-bearing one. checkContentModelConstraints'
// existing compile-failure path (upa.go:179) skips the check and keeps the
// schema, and this budget follows it exactly: a model too expensive to examine
// is declined, never rejected. Were it a rejection instead, the set of schemas
// that load would depend on a resource bound, and the conformance marks in
// tests/ratchet.txt would move.
//
// The gate is PER STATE, so a model may have some states declined and others
// scanned exactly, and a violation found in a state that WAS scanned is a real
// violation which must still be reported. The shape here is chosen so that
// every state is wide: a repeating choice of n same-named elements has n
// positions, and the follow set of each is all n, so forcing the width below n
// declines every state and nothing is examined at all.
//
// It runs at a forced budget rather than driving 2,048 particles, for the
// reason TestMaxPositionsRealBoundary records: at the production numbers this
// shape costs 12 seconds and 115MB per load, and three of them once blew CI's
// deadline under -race. The comparison being asserted is len(state) against the
// limit, which does not care which absolute number plays the limit's part.
func TestUPABudgetFiresAndSchemaStillLoads(t *testing.T) {
	// An AMBIGUOUS model in which EVERY state is n wide: a repeating
	// choice of n elements all named "a". At the normal budget the schema
	// is rejected; under a forced budget every state is declined and it
	// must load.
	const n = 32
	var b strings.Builder
	b.WriteString(`<xs:element name="r"><xs:complexType><xs:choice maxOccurs="unbounded">`)
	for i := 0; i < n; i++ {
		b.WriteString(`<xs:element name="a" type="xs:string"/>`)
	}
	b.WriteString(`</xs:choice></xs:complexType></xs:element>`)
	src := wrap(b.String())

	if ok, why := loadSchema(src, Version10); ok {
		t.Fatalf("a repeating choice of %d particles all named \"a\" is ambiguous "+
			"and must be rejected at the production budget, but it loaded", n)
	} else if !strings.Contains(why, "cos-nonambig") {
		t.Fatalf("rejected, but not by UPA: %s", why)
	}

	for _, f := range []struct {
		name         string
		width, pairs int
	}{
		{"maxUPAStateWidth=8", 8, -1},
		{"maxUPAPairTests=0", -1, 0},
	} {
		t.Run(f.name, func(t *testing.T) {
			withUPABudgets(f.width, f.pairs, func() {
				ok, why := loadSchema(src, Version10)
				if !ok {
					t.Fatalf("with %s the schema was REJECTED (%s); a model too "+
						"expensive to check must be SKIPPED, not refused — "+
						"otherwise a resource bound decides which schemas load",
						f.name, why)
				}
			})
			// And the budget really is what let it through: the same
			// model, checked directly, must record the skip.
			withUPABudgets(f.width, f.pairs, func() {
				ps := make([]*Particle, n)
				for i := range ps {
					ps[i] = &Particle{MinOccurs: 1, MaxOccurs: 1,
						Term: &ElementDecl{Name: xdm.QName{Local: "a"}}}
				}
				m, err := compileContentModel(&Particle{MinOccurs: 1, MaxOccurs: Unbounded,
					Term: &ModelGroup{Compositor: CompositorChoice, Particles: ps}})
				if err != nil {
					t.Fatalf("compile: %v", err)
				}
				if err := checkUPA(m, "t", CheckOptions{}); err != nil {
					t.Fatalf("with %s every state exceeds the budget, so nothing "+
						"was examined, yet a violation was reported: %v", f.name, err)
				}
				if !m.upaSkipped {
					t.Errorf("with %s the ambiguous %d-wide model was scanned in "+
						"full rather than declined; the schema above therefore "+
						"loaded for some other reason and this test proves nothing",
						f.name, n)
				}
			})
		})
	}
}

// TestUPABudgetDoesNotFireOnRealSchemas pins the threshold against the
// measurement that chose it.
//
// Instrumented over every schema in this tree, the widest state a real schema
// produces is 19 positions in testdata/xsdtests (15,464 schemas) and 72 in
// testdata/xslt30-test, whose widest file is
// tests/expr/type-expr/variousTypesSchemaExpr.xsd. maxUPAStateWidth is 256:
// 3.5x the widest real state and 8x the XSD suite's.
//
// A regression that lowered the threshold — or that made the gate fire at or
// below the width it is supposed to clear — would not show up as a test
// failure anywhere else, because a skipped check returns nil exactly as a
// passed one does. The conformance marks would not move either: skipping makes
// MORE schemas load, and the suite counts agreement, so a schema wrongly
// skipped that happens to be unambiguous still agrees. This is the only thing
// standing between the budget and silently switching UPA off.
func TestUPABudgetDoesNotFireOnRealSchemas(t *testing.T) {
	const widestReal = 72
	if maxUPAStateWidth <= widestReal {
		t.Fatalf("maxUPAStateWidth is %d, at or below the widest state a real schema "+
			"in this tree produces (%d, in testdata/xslt30-test/tests/expr/type-expr/"+
			"variousTypesSchemaExpr.xsd). The budget would decline the UPA check on "+
			"legitimate input, which switches the constraint off silently.",
			maxUPAStateWidth, widestReal)
	}
	m := compileWideOptional(t, widestReal)
	if err := checkUPA(m, "t", CheckOptions{}); err != nil {
		t.Fatalf("a %d-wide model of distinct optional elements is unambiguous: %v",
			widestReal, err)
	}
	if m.upaSkipped {
		t.Errorf("the UPA check was DECLINED on a %d-position state, which is the "+
			"widest any real schema in this tree produces. The budget is too low: "+
			"legitimate schemas are loading unchecked.", widestReal)
	}
}

// TestUPABudgetProductionValues pins the shipped numbers, for the reason
// TestMaxPositionsProductionValue does: the boundary tests above run at forced
// values, and this is what keeps that substitution honest.
func TestUPABudgetProductionValues(t *testing.T) {
	if maxUPAStateWidth != 256 {
		t.Errorf("maxUPAStateWidth is %d, want 256; if this change is deliberate, "+
			"update this test and docs/security.md", maxUPAStateWidth)
	}
	if maxUPAPairTests != 1<<22 {
		t.Errorf("maxUPAPairTests is %d, want %d; if this change is deliberate, "+
			"update this test and docs/security.md", maxUPAPairTests, 1<<22)
	}
}
