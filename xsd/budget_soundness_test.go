package xsd

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
				m, err := compileContentModel(p, 0)
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
	// The exported default is the one hosts actually get, and it is a
	// memory bound: at ~400 bytes per position 8192 caps one model at about
	// 3.3 MB. Raising it grants proportional memory to whoever wrote the
	// schema, so it must not drift silently either.
	if DefaultMaxContentModelPositions != 8192 {
		t.Errorf("DefaultMaxContentModelPositions is %d, want 8192; raising the "+
			"default authorises proportional memory for every host that does not "+
			"set MaxContentModelPositions — see docs/security.md",
			DefaultMaxContentModelPositions)
	}
}

// TestMaxContentModelPositionsOptionTakesEffect proves the option is wired to
// the thing it names, in both directions: a model refused at the default must
// load when the budget admits it, and a budget below the model must refuse.
//
// The shape is a plain sequence of n elements, so exactly n positions — the
// arithmetic is visible rather than inferred.
func TestMaxContentModelPositionsOptionTakesEffect(t *testing.T) {
	const n = 300
	var b strings.Builder
	b.WriteString(`<xs:element name="r"><xs:complexType><xs:sequence>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:element name="e%d" type="xs:string"/>`, i)
	}
	b.WriteString(`</xs:sequence></xs:complexType></xs:element>`)
	src := wrap(b.String())

	load := func(limit int) error {
		sdoc, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		_, lerr := Load(sdoc.Root, "", Options{MaxContentModelPositions: limit})
		return lerr
	}

	// Below the model: refused, and refused as a LIMIT rather than as a
	// verdict on a schema that is in fact perfectly valid.
	if err := load(n - 1); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("MaxContentModelPositions=%d is below the model's %d positions, "+
			"so the load must be refused as a resource limit; got %v", n-1, n, err)
	}
	// At and above the model: loads and is fully checked.
	if err := load(n); err != nil {
		t.Errorf("MaxContentModelPositions=%d exactly admits a %d-position model, "+
			"so the schema must load: %v", n, n, err)
	}
	// Zero selects the default, which is far above this model.
	if err := load(0); err != nil {
		t.Errorf("MaxContentModelPositions=0 means DefaultMaxContentModelPositions "+
			"(%d), which admits a %d-position model: %v",
			DefaultMaxContentModelPositions, n, err)
	}
}

// TestMaxContentModelPositionsAppliesToValidation is the plumbing invariant.
//
// The budget is retained on the Schema rather than read from a global so that
// validation compiles under the SAME limit the load-time constraint checks
// used. If validation silently fell back to the default, a schema loaded at a
// raised budget would pass its checks and then fail to validate anything —
// or, worse in the other direction, a schema whose constraints were never
// decided could still enforce a content model. The load and the validation
// must agree about which models exist.
func TestMaxContentModelPositionsAppliesToValidation(t *testing.T) {
	const n = 300
	var b strings.Builder
	b.WriteString(`<xs:element name="r"><xs:complexType><xs:sequence>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:element name="e%d" type="xs:string"/>`, i)
	}
	b.WriteString(`</xs:sequence></xs:complexType></xs:element>`)

	sdoc, err := xdm.ParseString(wrap(b.String()), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Loaded at a budget that admits the model exactly.
	s, err := Load(sdoc.Root, "", Options{MaxContentModelPositions: n})
	if err != nil {
		t.Fatalf("load at MaxContentModelPositions=%d: %v", n, err)
	}

	var inst strings.Builder
	inst.WriteString(`<r>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&inst, `<e%d>x</e%d>`, i, i)
	}
	inst.WriteString(`</r>`)
	idoc, err := xdm.ParseString(inst.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse instance: %v", err)
	}
	// The assertion with teeth. The package default is forced below the
	// model BEFORE the first validation, so the model has not yet been
	// compiled and cached: whichever budget modelFor consults is the one
	// actually used. If it read the global, the model would fail to compile
	// and the document would be refused; because it reads the budget the
	// schema was loaded with, the document validates.
	//
	// Ordering matters here. Validating once first would populate the
	// model cache, after which lowering the global proves nothing at all —
	// the compile never happens a second time.
	op := maxPositions
	defer func() { maxPositions = op }()
	maxPositions = 1
	if err := s.Validate(idoc.Root, ValidateOptions{}); err != nil {
		t.Errorf("validation compiled the content model against the package "+
			"default rather than the budget the schema was loaded with "+
			"(MaxContentModelPositions=%d): %v", n, err)
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

// loadSchemaErr is loadSchema, but hands back the error itself rather than its
// text. The UPA budget refusal must be distinguishable from a cos-nonambig
// verdict by errors.Is, and a string cannot carry that.
func loadSchemaErr(t *testing.T, src string, v Version) error {
	t.Helper()
	sdoc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, lerr := Load(sdoc.Root, "", Options{Version: v})
	return lerr
}

// ambiguousChoice builds a complex type whose content model is ambiguous at
// EVERY n: a sequence repeated twice around a choice of n identically named
// elements. Every branch of the choice competes with every other, and the
// repetition makes the follow set of each position the whole choice, so every
// state is n wide.
//
// It is the shape that exposed the defect this block now guards. At n=4 the
// state is narrow enough to scan and the schema is rejected as cos-nonambig; at
// n=300 it crosses maxUPAStateWidth. Before the fix the second case LOADED —
// the same violation, the opposite verdict, decided by a resource bound.
func ambiguousChoice(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:complexType name="t"><xs:sequence maxOccurs="2"><xs:choice>`)
	for i := 0; i < n; i++ {
		b.WriteString(`<xs:element name="dup" type="xs:string"/>`)
	}
	b.WriteString(`</xs:choice></xs:sequence></xs:complexType>`)
	return wrap(b.String())
}

// TestUPAOverBudgetAmbiguousSchemaIsRefused is the regression test for the
// false accept.
//
// UPA is a normative schema-component constraint: a schema that violates it is
// invalid. A budget that skipped the check therefore did not decline to answer,
// it answered "valid" without looking. The n=300 schema below is ambiguous, and
// must now FAIL to load — with a resource-limit refusal, because what the
// checker actually knows is "I could not decide", not "this is ambiguous".
func TestUPAOverBudgetAmbiguousSchemaIsRefused(t *testing.T) {
	err := loadSchemaErr(t, ambiguousChoice(300), Version10)
	if err == nil {
		t.Fatalf("FALSE ACCEPT: a genuinely ambiguous schema with a state wider "+
			"than maxUPAStateWidth (%d) loaded with no error. A budget must never "+
			"turn \"I could not prove the constraint\" into \"the constraint holds\".",
			maxUPAStateWidth)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot tell "+
			"\"too complex to check\" from \"your schema is ambiguous\"", err)
	}
	if !strings.Contains(err.Error(), "cannot be checked") {
		t.Errorf("message %q does not say the check was declined", err)
	}
	// A refusal is NOT a verdict, and must not claim to be one.
	if strings.Contains(err.Error(), "cos-nonambig") {
		t.Errorf("the refusal %v reports the cos-nonambig code, but nothing was "+
			"examined; a declined check must not masquerade as a violation", err)
	}
}

// TestUPAGenuineViolationIsNotAResourceLimit is the other polarity. The same
// shape narrow enough to scan must report the real constraint, and must NOT
// carry the sentinel — a caller that retried on ErrResourceLimit would retry a
// load that can never succeed.
func TestUPAGenuineViolationIsNotAResourceLimit(t *testing.T) {
	err := loadSchemaErr(t, ambiguousChoice(4), Version10)
	if err == nil {
		t.Fatal("a choice of 4 identically named elements is ambiguous and must be rejected")
	}
	if !strings.Contains(err.Error(), "cos-nonambig") {
		t.Errorf("rejected, but not by UPA: %v", err)
	}
	if errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("a genuine cos-nonambig violation %v reports as a resource limit; "+
			"a caller would retry a load that can never succeed", err)
	}
}

// TestUncompilableModelIsRefusedNotSkipped closes the case 2c461c7 left open
// and recorded in docs/security.md.
//
// checkContentModelConstraints compiles every content model and ran its checks
// on the ones that compiled; a model that did not compile was skipped with a
// bare `continue`, and the schema went on to load with Unique Particle
// Attribution, Element Declarations Consistent and both wildcard and
// substitution EDC never performed. The schema was accepted having proven
// nothing — the same false accept the width gate above corrects, one level out
// and gating every content-model constraint rather than UPA alone.
//
// The shape is the ambiguousChoice model, which is ambiguous at every n, with
// maxPositions forced below its position count so the compile fails. Before
// the fix it LOADED.
func TestUncompilableModelIsRefusedNotSkipped(t *testing.T) {
	op := maxPositions
	defer func() { maxPositions = op }()
	maxPositions = 2

	err := loadSchemaErr(t, ambiguousChoice(8), Version10)
	if err == nil {
		t.Fatalf("FALSE ACCEPT: a genuinely ambiguous schema whose content model "+
			"could not be compiled (maxPositions=%d) loaded with no error. Every "+
			"content-model constraint was skipped, so the schema was accepted "+
			"having proven nothing.", maxPositions)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot tell "+
			"\"too complex to check\" from \"your schema is ambiguous\". Note that "+
			"checkContentModelConstraints sorts its errors, and sorting that "+
			"rebuilds them from their text would strip the sentinel", err)
	}
	if !strings.Contains(err.Error(), "cannot be checked") {
		t.Errorf("message %q does not say the checks were declined", err)
	}
	// A refusal is not a verdict. Nothing was examined, so no constraint
	// code may appear.
	if strings.Contains(err.Error(), "cos-nonambig") ||
		strings.Contains(err.Error(), "cos-element-consistent") {
		t.Errorf("the refusal %v reports a constraint code, but no constraint "+
			"was checked; a declined check must not masquerade as a violation", err)
	}
}

// TestCompilableAmbiguousModelIsStillAVerdict is the other polarity, and it is
// what keeps the two paths distinguishable. The same shape small enough to
// compile must be rejected on the merits, with the constraint's own code and
// WITHOUT the sentinel: a caller that retried on ErrResourceLimit would retry a
// load that can never succeed however much budget it is given.
func TestCompilableAmbiguousModelIsStillAVerdict(t *testing.T) {
	err := loadSchemaErr(t, ambiguousChoice(8), Version10)
	if err == nil {
		t.Fatal("a choice of 8 identically named elements is ambiguous and must be rejected")
	}
	if !strings.Contains(err.Error(), "cos-nonambig") {
		t.Errorf("rejected, but not by UPA: %v", err)
	}
	if errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("a genuine cos-nonambig violation %v reports as a resource limit", err)
	}
}

// TestUPAOverBudgetUnambiguousSchemaIsAlsoRefused pins the price of the fix,
// deliberately and in the open.
//
// This is a BEHAVIOUR CHANGE. A legitimate, unambiguous schema with a state
// wider than maxUPAStateWidth loaded before the budget existed and loaded while
// the budget skipped; it now FAILS. That is the unavoidable cost of refusing to
// guess: the checker cannot tell a wide-and-fine model from a wide-and-broken
// one without doing the work the budget forbids, and of the two available
// answers only the refusal is honest.
//
// It is safe in practice only because the threshold is far above anything real
// — 256 against a widest measured state of 19 across 15,464 W3C schemas and 72
// in the XSLT corpus. TestUPABudgetDoesNotFireOnRealSchemas is what keeps that
// gap; it matters more now than it did, because firing no longer means
// "unchecked", it means "rejected".
func TestUPAOverBudgetUnambiguousSchemaIsAlsoRefused(t *testing.T) {
	// Distinct names throughout: this model is unambiguous, and at a
	// normal budget it loads.
	const n = 8
	var b strings.Builder
	b.WriteString(`<xs:complexType name="t"><xs:sequence>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:element name="e%d" type="xs:string" minOccurs="0"/>`, i)
	}
	b.WriteString(`</xs:sequence></xs:complexType>`)
	src := wrap(b.String())

	if err := loadSchemaErr(t, src, Version10); err != nil {
		t.Fatalf("a sequence of %d distinctly named optional elements is "+
			"unambiguous and must load at the production budget: %v", n, err)
	}
	withUPABudgets(2, -1, func() {
		err := loadSchemaErr(t, src, Version10)
		if err == nil {
			t.Fatalf("with maxUPAStateWidth=2 the %d-wide model was scanned in "+
				"full; the budget did not fire, so this test proves nothing", n)
		}
		if !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("an unambiguous schema refused over budget must be refused as "+
				"a RESOURCE LIMIT, not as a constraint violation: %v", err)
		}
	})
}

// TestUPABudgetSoundness states the property the UPA budget actually has.
//
// The invariant the rest of this file enforces — "budgeted accepts => exact
// accepts" — is the WRONG property for UPA, and permitted exactly the false
// accept above: a budget that skips its check accepts everything, which
// satisfies "never rejects what the exact path accepts" while silently
// admitting invalid schemas. The four construction budgets bound how much of a
// thing gets BUILT, and declining to build it makes the thing unusable later;
// this budget bounds a normative CHECK, and declining a check has no later
// safety net, because the schema has already loaded.
//
// So the property here is two-sided:
//
//	budgeted accepts  => exact accepts   (no new rejections of valid schemas
//	                                      dressed up as verdicts)
//	budgeted declines => an ERROR carrying xdm.ErrResourceLimit
//	                                     (never silent acceptance)
//
// Every case below is forced fully over budget, so the second clause is what is
// exercised: under every forcing the load must fail, and fail as a resource
// limit — for the invalid cases AND the valid ones alike, because a declined
// check cannot tell them apart. That indistinguishability is the point: it is
// why the refusal is labelled a resource limit rather than a verdict.
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
				var err error
				withUPABudgets(f.width, f.pairs, func() {
					err = loadSchemaErr(t, c.schema, c.ver)
				})
				// A declined check must never be an acceptance.
				// This is the clause the old harness lacked.
				if err == nil {
					t.Errorf("FALSE ACCEPT: with %s every state is over budget, so "+
						"UPA was never checked, yet the schema LOADED. A budget may "+
						"decline to answer; it must not answer \"valid\".\n  schema: %s",
						f.name, c.schema)
					continue
				}
				// And the refusal must be labelled as one, so a
				// caller is never told an undecided schema is
				// invalid.
				if !errors.Is(err, xdm.ErrResourceLimit) {
					t.Errorf("with %s the schema was refused as %v, without "+
						"xdm.ErrResourceLimit; a budget refusal must be "+
						"distinguishable from a constraint verdict.\n  schema: %s",
						f.name, err, c.schema)
				}
			}
		})
	}
}

// TestUPABudgetRefusalIsObservable replaces TestUPABudgetDeclineIsRecorded.
//
// That test existed because a declined check and a clean one both returned nil,
// so the decline had to be recorded on the model (contentModel.upaSkipped) for
// anything to observe it. Refusing instead makes the decline its own signal:
// checkUPA returns an error, so the field was dead and is gone. What remains
// worth pinning is that the two outcomes are still told apart — a width-8 model
// passes at the production budget and is refused under a forced one.
func TestUPABudgetRefusalIsObservable(t *testing.T) {
	m := compileWideOptional(t, 8)
	if err := checkUPA(m, "t", CheckOptions{}); err != nil {
		t.Fatalf("a sequence of distinct optional elements is unambiguous: %v", err)
	}
	withUPABudgets(2, -1, func() {
		m := compileWideOptional(t, 8)
		err := checkUPA(m, "t", CheckOptions{})
		if err == nil {
			t.Fatalf("with maxUPAStateWidth=2 a width-8 model was scanned in full; " +
				"the budget did not fire, so nothing bounds the triangular scan")
		}
		if !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("the refusal %v does not carry xdm.ErrResourceLimit", err)
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
		Term: &ModelGroup{Compositor: CompositorSequence, Particles: ps}}, 0)
	if err != nil {
		t.Fatalf("compiling %d optional particles: %v", n, err)
	}
	return m
}

// TestUPABudgetFiresAndSchemaIsRefused replaces
// TestUPABudgetFiresAndSchemaStillLoads, whose second half asserted precisely
// the property that was wrong: that an over-budget schema still loads.
//
// It pins both halves of the corrected policy: the budget FIRES, and the schema
// is REFUSED as a resource limit. The shape is ambiguous and every one of its
// states is n wide, so forcing the width below n puts every state over budget
// and nothing is examined at all — which is exactly the case that must not be
// mistaken for a pass.
//
// It runs at a forced budget rather than driving 2,048 particles, for the
// reason TestMaxPositionsRealBoundary records: at the production numbers this
// shape costs 12 seconds and 115MB per load. The comparison being asserted is
// len(state) against the limit, which does not care which absolute number plays
// the limit's part.
func TestUPABudgetFiresAndSchemaIsRefused(t *testing.T) {
	const n = 32
	var b strings.Builder
	b.WriteString(`<xs:element name="r"><xs:complexType><xs:choice maxOccurs="unbounded">`)
	for i := 0; i < n; i++ {
		b.WriteString(`<xs:element name="a" type="xs:string"/>`)
	}
	b.WriteString(`</xs:choice></xs:complexType></xs:element>`)
	src := wrap(b.String())

	// At the production budget it is scanned, and rejected on the merits.
	err := loadSchemaErr(t, src, Version10)
	if err == nil {
		t.Fatalf("a repeating choice of %d particles all named \"a\" is ambiguous "+
			"and must be rejected at the production budget, but it loaded", n)
	}
	if !strings.Contains(err.Error(), "cos-nonambig") {
		t.Fatalf("rejected, but not by UPA: %v", err)
	}
	if errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("a scanned violation must not report as a resource limit: %v", err)
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
				err := loadSchemaErr(t, src, Version10)
				if err == nil {
					t.Fatalf("with %s the schema LOADED. Every state is over "+
						"budget, so UPA examined nothing — and an ambiguous "+
						"schema was accepted because the checker declined to "+
						"look at it.", f.name)
				}
				if !errors.Is(err, xdm.ErrResourceLimit) {
					t.Errorf("with %s the refusal %v does not carry "+
						"xdm.ErrResourceLimit", f.name, err)
				}
			})
			// And the budget really is what refused it: the same
			// model, checked directly, must refuse rather than
			// report a violation it never observed.
			withUPABudgets(f.width, f.pairs, func() {
				ps := make([]*Particle, n)
				for i := range ps {
					ps[i] = &Particle{MinOccurs: 1, MaxOccurs: 1,
						Term: &ElementDecl{Name: xdm.QName{Local: "a"}}}
				}
				m, err := compileContentModel(&Particle{MinOccurs: 1, MaxOccurs: Unbounded,
					Term: &ModelGroup{Compositor: CompositorChoice, Particles: ps}}, 0)
				if err != nil {
					t.Fatalf("compile: %v", err)
				}
				cerr := checkUPA(m, "t", CheckOptions{})
				if cerr == nil {
					t.Fatalf("with %s the ambiguous %d-wide model returned no error",
						f.name, n)
				}
				if !errors.Is(cerr, xdm.ErrResourceLimit) {
					t.Errorf("with %s every state exceeds the budget, so nothing "+
						"was examined, yet the error is a verdict rather than a "+
						"refusal: %v", f.name, cerr)
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
// This test matters MORE than it did when the budget skipped. A regression that
// lowered the threshold used to switch UPA off silently on legitimate input;
// now it REFUSES that input, and a valid schema stops loading. The failure is
// louder but the guard is the same, and it is the only thing standing between
// the budget and rejecting real work.
func TestUPABudgetDoesNotFireOnRealSchemas(t *testing.T) {
	const widestReal = 72
	if maxUPAStateWidth <= widestReal {
		t.Fatalf("maxUPAStateWidth is %d, at or below the widest state a real schema "+
			"in this tree produces (%d, in testdata/xslt30-test/tests/expr/type-expr/"+
			"variousTypesSchemaExpr.xsd). The budget would refuse the UPA check on "+
			"legitimate input, and valid schemas would stop loading.",
			maxUPAStateWidth, widestReal)
	}
	m := compileWideOptional(t, widestReal)
	if err := checkUPA(m, "t", CheckOptions{}); err != nil {
		t.Fatalf("a %d-wide model of distinct optional elements is unambiguous and "+
			"is the widest any real schema in this tree produces; the budget "+
			"refused it, so legitimate schemas are being rejected: %v",
			widestReal, err)
	}
}

// TestUPABudgetProductionValues pins the shipped numbers, for the reason
// TestMaxPositionsProductionValue does: the boundary tests above run at forced
// values, and this is what keeps that substitution honest.
//
// It is load-bearing in a second way since the budget began refusing rather
// than skipping: these two numbers now decide which valid schemas load, so
// lowering either is a compatibility change, not a tuning knob.
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

// ---------------------------------------------------------------------------
// Substitution closure budget
// ---------------------------------------------------------------------------

// withSubstBudget runs fn with maxSubstitutionClosure set to n, restoring it
// afterwards.
//
// Like the UPA budgets and unlike the four at the top of this file, this one
// bounds the input to a normative CHECK rather than the construction of
// something that becomes unusable when it is declined. Substitution membership
// decides which elements a particle matches, so a closure that is not computed
// is not a smaller correct answer — it is a content model whose UPA and
// Element Declarations Consistent verdicts are both unknown.
func withSubstBudget(n int, fn func()) {
	o := maxSubstitutionClosure
	defer func() { maxSubstitutionClosure = o }()
	maxSubstitutionClosure = n
	fn()
}

// substCases are schemas whose acceptance turns on the substitution closure:
// each invalid shape paired with the valid shape it is one edit away from.
//
// Both polarities are required for the same reason the UPA cases are. A budget
// that truncated the closure instead of refusing would still accept every
// valid case here, and would also accept the invalid ones — because the member
// that causes the violation is exactly the member truncation drops. A suite of
// valid schemas alone cannot see that.
func substCases() []derivationCase {
	// A head, and a member substituting for it under a name that a LOCAL
	// particle in the same content model also declares, with a different
	// type. That is cos-element-consistent: "n" means two things in one
	// model, once directly and once through the group.
	//
	// The conflict must come from the like-named LOCAL particle, not from
	// the member's own type. e-props-correct.4 (parse_decl.go) already
	// requires a member's type to derive from its head's, and it is checked
	// at parse time, before the closure exists — so a member typed
	// incompatibly with its head never reaches this budget at all. The
	// member here is typed xs:string like its head; it is the local "n" of
	// type xs:date beside it that makes the model inconsistent.
	edcBad := wrap(`
	  <xs:element name="head" type="xs:string"/>
	  <xs:element name="n" type="xs:string" substitutionGroup="head"/>
	  <xs:complexType name="t"><xs:sequence>
	    <xs:element name="n" type="xs:date"/>
	    <xs:element ref="head"/>
	  </xs:sequence></xs:complexType>`)
	// The same schema with the local particle's type agreed: valid, and
	// valid only after the closure has been consulted.
	edcGood := wrap(`
	  <xs:element name="head" type="xs:string"/>
	  <xs:element name="n" type="xs:string" substitutionGroup="head"/>
	  <xs:complexType name="t"><xs:sequence>
	    <xs:element name="n" type="xs:string"/>
	    <xs:element ref="head"/>
	  </xs:sequence></xs:complexType>`)
	// A choice between a head and one of its own members: the two branches
	// can both match the member's name, so the model is ambiguous. This is
	// UPA reached THROUGH the closure — elementNamesOverlap is the only
	// reason the two particles compete at all.
	upaBad := wrap(`
	  <xs:element name="head" type="xs:string"/>
	  <xs:element name="mem" type="xs:string" substitutionGroup="head"/>
	  <xs:complexType name="t"><xs:choice>
	    <xs:element ref="head"/>
	    <xs:element ref="mem"/>
	  </xs:choice></xs:complexType>`)
	// The same choice where the second branch is NOT in the group, so the
	// names cannot overlap.
	upaGood := wrap(`
	  <xs:element name="head" type="xs:string"/>
	  <xs:element name="mem" type="xs:string" substitutionGroup="head"/>
	  <xs:element name="other" type="xs:string"/>
	  <xs:complexType name="t"><xs:choice>
	    <xs:element ref="head"/>
	    <xs:element ref="other"/>
	  </xs:choice></xs:complexType>`)
	return []derivationCase{
		{"subst/edc-conflicting-types", edcBad, false, Version11},
		{"subst/edc-agreeing-types", edcGood, true, Version11},
		{"subst/upa-head-vs-member", upaBad, false, Version10},
		{"subst/upa-head-vs-outsider", upaGood, true, Version10},
	}
}

// TestSubstitutionClosureBudgetSoundness states the two-sided property, the
// same one TestUPABudgetSoundness states and for the same reason.
//
// maxSubstitutionClosure bounds a structure that two normative constraints are
// decided FROM — Unique Particle Attribution and Element Declarations
// Consistent. Declining to build it therefore leaves both undecided, and an
// undecided normative constraint must be refused, never assumed to hold:
//
//	budgeted accepts  => exact accepts
//	budgeted declines => an ERROR carrying xdm.ErrResourceLimit
//
// Every case is forced fully over budget, so the second clause is what is
// exercised — and it is exercised on the VALID cases as well as the invalid
// ones, because a closure that was never computed cannot tell them apart. That
// indistinguishability is why the refusal reports a limit rather than a
// verdict.
func TestSubstitutionClosureBudgetSoundness(t *testing.T) {
	for _, c := range substCases() {
		t.Run(c.name, func(t *testing.T) {
			exact, exactWhy := loadSchema(c.schema, c.ver)
			if exact != c.valid {
				t.Fatalf("case is mislabelled: at the normal budget got accepted=%v (%s), want %v",
					exact, exactWhy, c.valid)
			}
			// 0 is the only forcing guaranteed to be over budget for
			// every case: the gate counts members VISITED, so a
			// one-member closure fits within a limit of 1.
			for _, limit := range []int{0} {
				var err error
				withSubstBudget(limit, func() {
					err = loadSchemaErr(t, c.schema, c.ver)
				})
				if err == nil {
					t.Errorf("FALSE ACCEPT: with maxSubstitutionClosure=%d the "+
						"substitution closure was never computed, so neither UPA nor "+
						"Element Declarations Consistent could be decided over it, "+
						"yet the schema LOADED. A budget may decline to answer; it "+
						"must not answer \"valid\".\n  schema: %s", limit, c.schema)
					continue
				}
				if !errors.Is(err, xdm.ErrResourceLimit) {
					t.Errorf("with maxSubstitutionClosure=%d the schema was refused as "+
						"%v, without xdm.ErrResourceLimit; a budget refusal must be "+
						"distinguishable from a constraint verdict.\n  schema: %s",
						limit, err, c.schema)
				}
			}
		})
	}
}

// TestSubstitutionClosureGenuineViolationIsNotAResourceLimit is the other
// polarity: at the production budget the same schemas must report the real
// constraint, and must NOT carry the sentinel. A caller that retried on
// ErrResourceLimit would otherwise retry a load that can never succeed.
func TestSubstitutionClosureGenuineViolationIsNotAResourceLimit(t *testing.T) {
	for _, c := range substCases() {
		if c.valid {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			err := loadSchemaErr(t, c.schema, c.ver)
			if err == nil {
				t.Fatal("case is labelled invalid but loaded at the production budget")
			}
			if errors.Is(err, xdm.ErrResourceLimit) {
				t.Errorf("a genuine constraint violation %v reports as a resource "+
					"limit; a caller would retry a load that can never succeed", err)
			}
		})
	}
}

// TestSubstitutionClosureRefusalIsNotATruncation is the property that
// separates this budget from the tempting cheap alternative.
//
// Truncating the closure — keeping the first N members and dropping the rest —
// would let every schema load, which looks like the conservative choice and is
// the opposite of one. The dropped member is precisely the one that causes a
// violation, so truncation converts "invalid" into "accepted" without ever
// reporting that it did. This test drives an invalid schema whose fault lies in
// the LAST member of a chain and checks that the over-budget outcome is a
// refusal rather than a load.
func TestSubstitutionClosureRefusalIsNotATruncation(t *testing.T) {
	// A chain h0 <- h1 <- ... <- tail, every member typed xs:string like the
	// head so e-props-correct.4 is satisfied and the schema survives to the
	// closure. The content model names a LOCAL "tail" of type xs:date, which
	// conflicts with the chain's last member. Only the full closure of h0
	// reaches that member, so only the full closure sees the violation.
	const n = 12
	var b strings.Builder
	b.WriteString(`<xs:element name="h0" type="xs:string"/>`)
	for i := 1; i < n-1; i++ {
		fmt.Fprintf(&b, `<xs:element name="h%d" type="xs:string" substitutionGroup="h%d"/>`, i, i-1)
	}
	fmt.Fprintf(&b, `<xs:element name="tail" type="xs:string" substitutionGroup="h%d"/>`, n-2)
	b.WriteString(`<xs:complexType name="t"><xs:sequence>` +
		`<xs:element name="tail" type="xs:date"/>` +
		`<xs:element ref="h0"/>` +
		`</xs:sequence></xs:complexType>`)
	src := wrap(b.String())

	err := loadSchemaErr(t, src, Version11)
	if err == nil {
		t.Fatalf("the chain's last member declares \"tail\" as xs:string beside a "+
			"local \"tail\" of xs:date; that is cos-element-consistent and must "+
			"be rejected at the production budget (chain length %d)", n)
	}
	if errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("rejected as a resource limit at the production budget: %v", err)
	}

	// Now force the budget below the chain length. The member carrying the
	// fault is beyond it, so a truncating implementation would load this
	// schema clean.
	withSubstBudget(n/2, func() {
		err := loadSchemaErr(t, src, Version11)
		if err == nil {
			t.Fatalf("FALSE ACCEPT: with maxSubstitutionClosure=%d the closure "+
				"stopped short of the member that makes this schema invalid, and "+
				"the schema LOADED. A budget that truncates a substitution closure "+
				"does not give a smaller correct answer, it hides the violation "+
				"the dropped member causes.", n/2)
		}
		if !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("over budget the refusal %v does not carry "+
				"xdm.ErrResourceLimit", err)
		}
	})
}

// TestSubstitutionClosureBudgetDoesNotFireOnRealSchemas pins the threshold
// against the census that chose it.
//
// Instrumented over all 15,702 .xsd files in this tree — testdata/xsdtests,
// testdata/xslt30-test, testdata/qt3tests, testdata/relaxng, testdata/xsltng,
// testdata/xspec and w3cschemas — loaded at both 1.0 and 1.1, the largest total
// closure any real schema produces is 50 membership entries, in
// testdata/xslt30-test/admin/catalog-schema.xsd, whose widest single closure is
// 26 members. maxSubstitutionClosure is 65,536: over 1,300x the widest real
// schema in this tree.
//
// The gap matters because firing REJECTS. A regression that lowered this
// threshold would not quietly degrade an analysis, it would stop valid schemas
// from loading.
func TestSubstitutionClosureBudgetDoesNotFireOnRealSchemas(t *testing.T) {
	const widestReal = 50
	if maxSubstitutionClosure <= widestReal {
		t.Fatalf("maxSubstitutionClosure is %d, at or below the largest total "+
			"substitution closure a real schema in this tree produces (%d, in "+
			"testdata/xslt30-test/admin/catalog-schema.xsd). The budget would "+
			"refuse legitimate input and valid schemas would stop loading.",
			maxSubstitutionClosure, widestReal)
	}
	// And the widest real schema really does load, rather than merely
	// comparing below a constant. A star of one head with widestReal
	// members reproduces that census maximum in a single closure.
	var b strings.Builder
	b.WriteString(`<xs:element name="head" type="xs:string"/>`)
	for i := 0; i < widestReal; i++ {
		fmt.Fprintf(&b, `<xs:element name="m%d" type="xs:string" substitutionGroup="head"/>`, i)
	}
	b.WriteString(`<xs:complexType name="t"><xs:sequence>` +
		`<xs:element ref="head" minOccurs="0"/></xs:sequence></xs:complexType>`)
	if err := loadSchemaErr(t, wrap(b.String()), Version11); err != nil {
		t.Fatalf("a substitution group of %d members is the largest any real schema "+
			"in this tree produces, and it must load: %v", widestReal, err)
	}
}

// TestSubstitutionClosureProductionValue pins the shipped number, for the
// reason TestUPABudgetProductionValues does: the boundary tests above run at
// forced values, and this keeps that substitution honest. Since the budget
// refuses rather than truncates, this number decides which valid schemas load,
// so lowering it is a compatibility change rather than a tuning knob.
func TestSubstitutionClosureProductionValue(t *testing.T) {
	if maxSubstitutionClosure != 1<<16 {
		t.Errorf("maxSubstitutionClosure is %d, want %d; if this change is "+
			"deliberate, update this test and docs/security.md",
			maxSubstitutionClosure, 1<<16)
	}
}

// TestElementNamesOverlapIsLinearInClosureSize guards the algorithm change that
// made a budget here unnecessary.
//
// elementNamesOverlap once looped one substitution closure inside the other, so
// a single pair test cost O(|a|*|b|) while checkUPA's maxUPAPairTests counted it
// as ONE. No budget in the package measured the closure, so the quadratic factor
// was invisible to all of them: 32 positions at closure 2048 spent 90 seconds
// inside checkUPA with maxPositions, maxUPAStateWidth and maxUPAPairTests all
// satisfied. Intersecting through a map made the same shape cost 574ms.
//
// The property asserted is a GROWTH RATE, not a wall-clock threshold, for the
// reason complexity_fuzz_test.go records. Doubling the closure size must roughly
// double the cost; the quadratic form would quadruple it.
func TestElementNamesOverlapIsLinearInClosureSize(t *testing.T) {
	// Two heads whose closures are disjoint and equal in size, so every
	// comparison runs to completion instead of short-circuiting on a hit.
	build := func(n int) (a, b *ElementDecl) {
		mk := func(prefix string) *ElementDecl {
			h := &ElementDecl{Name: xdm.QName{Local: prefix}}
			subs := make([]*ElementDecl, n)
			for i := range subs {
				subs[i] = &ElementDecl{
					Name: xdm.QName{Local: fmt.Sprintf("%s_m%d", prefix, i)}}
			}
			h.substitutable = subs
			return h
		}
		return mk("x"), mk("y")
	}
	cost := func(n, iters int) time.Duration {
		a, b := build(n)
		start := time.Now()
		for i := 0; i < iters; i++ {
			if elementNamesOverlap(a, b) {
				t.Fatal("disjoint closures must not overlap")
			}
		}
		return time.Since(start)
	}
	const iters = 200
	// Warm up, so the first measurement does not pay for lazy allocation.
	cost(1024, 10)
	small := cost(1024, iters)
	large := cost(4096, iters)
	// A 4x increase in closure size. Linear predicts ~4x cost; the old
	// nested form predicts ~16x. 8x is the midpoint, and a generous
	// allowance for timer noise on a loaded machine.
	if large > small*8 {
		t.Errorf("elementNamesOverlap cost grew from %v at closure 1024 to %v at "+
			"closure 4096 — more than the 8x that separates linear from "+
			"quadratic growth over a 4x size increase. The map intersection has "+
			"regressed to a nested loop, and checkUPA's pair budget does not "+
			"bound the difference.", small, large)
	}
}
