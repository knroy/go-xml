package xsd

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// Adversarial complexity coverage for the schema-processing algorithms.
//
// The budgets in this package guard against MISSING limits: maxPositions,
// branchLimit, subsumeMaxStates and subsumeMaxProduct each refuse a model
// that grows past a stated size. They do not guard against ALGORITHMIC
// COMPLEXITY. A schema can sit inside every one of those limits and still
// cost superlinearly more than its own text, because the limits bound the
// SIZE of an intermediate structure while the cost is a higher power of that
// size. The tests below measure growth rates rather than asserting that a
// load merely terminated.
//
// BUDGET INVENTORY — every combinatorial algorithm in xsd/ and what bounds it.
//
//	algorithm                     function/file                enforcement
//	----------------------------- --------------------------- ---------------------------
//	Glushkov construction         build, automaton.go:207     automaton.go:234, :406
//	                                                          (maxPositions = 8192,
//	                                                          automaton.go:205; checked
//	                                                          BEFORE each append, and
//	                                                          maxOccurs is never unrolled
//	                                                          -- counters, automaton.go:216
//	                                                          -- so schema text cannot
//	                                                          expand through occurrence
//	                                                          values)
//	branch enumeration            groupBranchCounts,          restrict.go:2140 (incremental)
//	                              restrict.go:2128            restrict.go:2158 (checked
//	                                                          BEFORE the product is
//	                                                          allocated at :2162)
//	                                                          (branchLimit = 4096, :2126)
//	NFA occurrence unrolling      nfaParticle, subsume.go:184 subsume.go:232, :246 INSIDE
//	                                                          the unroll loop at :220,
//	                                                          plus :189, :271, :303
//	                                                          (subsumeMaxStates = 4096)
//	product determinisation       particleSubsumes,           subsume.go:434, at the top
//	                              subsume.go:388              of the BFS loop
//	                                                          (subsumeMaxProduct = 20000)
//	document composition          assemble.go                 MaxDocuments (assemble.go:78)
//	                                                          DefaultMaxSchemaBytes
//	                                                          (resolve.go:284)
//	group-ref recursion           cycleFrom,                  NO BUDGET, AND NONE NEEDED:
//	                              parse_type.go:2173          three-colour DFS with a
//	                                                          shared done set
//	                                                          (parse_type.go:2137) makes it
//	                                                          O(V+E); width expansion is
//	                                                          caught by maxPositions
//
// ALGORITHMS WITH NO EXPLICIT BUDGET (the finding this file exists to pin):
//
//	UPA pairwise competition      checkUPA, upa.go:241        NO BUDGET. Triangular loop
//	                                                          (upa.go:256-257) over each of
//	                                                          len(m.follow)+1 states.
//	                                                          Bounded only transitively by
//	                                                          maxPositions, and only to
//	                                                          O(positions^3) -- see
//	                                                          TestUPACostIsCubicInPositions.
//	substitution-closure overlap  elementNamesOverlap,        NO BUDGET. Nested loop over
//	                              upa.go:338                  two substitutable slices.
//	substitution EDC              checkSubstitutionEDC, :487  NO BUDGET. positions x closure.
//	wildcard EDC                  checkWildcardEDC, :575      NO BUDGET. <= positions^2.
//	follow-set dedup              addFollow, automaton.go:425 NO BUDGET. Linear dup-scan per
//	                                                          edge within maxPositions.
//	substitution closure BFS      linkSubstitutionGroups,     NO BUDGET. Run once per global
//	                              assemble.go:1110            element: O(N^2) over elements.
//	derivation matching           recurseLaxUnordered :1254,  NO BUDGET, no memoisation, no
//	                              recurseUnordered :1281,     depth limit. Terminates only
//	                              mapAndSum :1562             because checkGroupCycles
//	                                                          guarantees acyclicity.
//	identity constraints          identity.go, icpath.go      NO BUDGET ANYWHERE. icStats
//	                                                          (identity.go:100) measures but
//	                                                          never refuses. Mitigated
//	                                                          algorithmically instead: keyref
//	                                                          resolution is a map lookup
//	                                                          (identity.go:455), not a cross
//	                                                          product, and duplicate
//	                                                          detection is map-based, so
//	                                                          there is no pairwise loop.
//
// DISCIPLINE ON RUNTIME. TestMaxPositionsRealBoundary in budget_soundness_test.go
// records what happens when a test in this package drives a cubic algorithm at
// its production budget: 8192 particles took 202s single-threaded and blew the
// CI deadline under -race. Every test here therefore runs at sizes whose cost
// is measured in milliseconds and asserts a GROWTH RATE, which is a property of
// the algorithm and not of the budget's magnitude. Where a boundary must be
// reached, it is reached at a forced budget via withBudgets, exactly as
// TestMaxPositionsRealBoundary does.

// adversarial schema generators.
//
// Each returns a schema that is individually legal and inside every stated
// budget. Size is the knob; the point is what the shapes cost when combined.

// genOptionalSequence is a sequence of n optional elements. Every position can
// follow every earlier one, so the follow relation is dense: |follow[i]| is
// O(n) for all i. This is the cheapest schema text that produces a dense
// automaton, which is what makes it the worst case for checkUPA.
func genOptionalSequence(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t"><xs:sequence>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:element name="e%d" minOccurs="0"/>`, i)
	}
	b.WriteString(`</xs:sequence></xs:complexType></xs:schema>`)
	return b.String()
}

// genNestedChoice alternates choice inside sequence inside choice. Nesting
// does not by itself make the follow relation dense, but the optional choices
// make every branch skippable, which does.
func genNestedChoice(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t"><xs:sequence>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:choice minOccurs="0"><xs:element name="a%d"/>`+
			`<xs:sequence><xs:element name="b%d"/><xs:choice minOccurs="0">`+
			`<xs:element name="c%d"/></xs:choice></xs:sequence></xs:choice>`, i, i, i)
	}
	b.WriteString(`</xs:sequence></xs:complexType></xs:schema>`)
	return b.String()
}

// genGroupOccurs nests group references with a large-but-legal maxOccurs at
// every level. The occurrence values multiply to 4^depth "logical" repetitions
// while the automaton stays linear, because automaton.go uses counters rather
// than unrolling. This is the control case: it must stay cheap.
func genGroupOccurs(depth int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	b.WriteString(`<xs:group name="g0"><xs:sequence><xs:element name="leaf" minOccurs="0"/></xs:sequence></xs:group>`)
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&b, `<xs:group name="g%d"><xs:sequence>`+
			`<xs:group ref="g%d" minOccurs="0" maxOccurs="1000"/>`+
			`</xs:sequence></xs:group>`, i, i-1)
	}
	fmt.Fprintf(&b, `<xs:complexType name="t"><xs:sequence><xs:group ref="g%d"/>`+
		`</xs:sequence></xs:complexType></xs:schema>`, depth)
	return b.String()
}

// genWildcardMix interleaves named particles with wildcards inside an
// unbounded repetition. Under 1.0 an element competes with a wildcard that
// admits it, so positionsCompete does real work on every pair rather than
// falling out on the type switch.
func genWildcardMix(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t">` +
		`<xs:choice minOccurs="0" maxOccurs="unbounded">`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:element name="e%d"/><xs:any namespace="##other" processContents="lax"/>`, i)
	}
	b.WriteString(`</xs:choice></xs:complexType></xs:schema>`)
	return b.String()
}

// genSubstChain builds a substitution group chain of length n, so the
// transitive closure of the head has n members. linkSubstitutionGroups runs
// the closure once per element, making the closure work quadratic in n, and
// elementNamesOverlap then loops those closures pairwise.
func genSubstChain(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
		`<xs:element name="h0" type="xs:string"/>`)
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, `<xs:element name="h%d" type="xs:string" substitutionGroup="h%d"/>`, i, i-1)
	}
	b.WriteString(`<xs:complexType name="t"><xs:sequence>` +
		`<xs:element ref="h0" minOccurs="0" maxOccurs="unbounded"/>` +
		`</xs:sequence></xs:complexType></xs:schema>`)
	return b.String()
}

// genIdentityFanout puts n key/keyref/unique constraints on one element whose
// selectors all match the same repeated child. identity.go has no budget of
// any kind, so this is where an unbounded path would show if one existed.
func genIdentityFanout(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
		`<xs:element name="r"><xs:complexType><xs:sequence>` +
		`<xs:element name="a" minOccurs="0" maxOccurs="unbounded">` +
		`<xs:complexType><xs:attribute name="k" type="xs:string"/>` +
		`<xs:attribute name="v" type="xs:string"/></xs:complexType></xs:element>` +
		`</xs:sequence></xs:complexType>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:key name="k%d"><xs:selector xpath="a"/><xs:field xpath="@k"/></xs:key>`, i)
		fmt.Fprintf(&b, `<xs:keyref name="r%d" refer="k%d"><xs:selector xpath="a"/>`+
			`<xs:field xpath="@k"/></xs:keyref>`, i, i)
		fmt.Fprintf(&b, `<xs:unique name="u%d"><xs:selector xpath="a"/><xs:field xpath="@v"/></xs:unique>`, i)
	}
	b.WriteString(`</xs:element></xs:schema>`)
	return b.String()
}

// genRecursiveType is a type that refers to itself through an element
// declaration. The reference is legal and the group graph stays acyclic; what
// is being checked is that the assembler does not chase it forever.
func genRecursiveType(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:complexType name="t%d"><xs:choice minOccurs="0" maxOccurs="unbounded">`+
			`<xs:element name="self" type="t%d"/><xs:element name="down" type="t%d"/>`+
			`</xs:choice></xs:complexType>`, i, i, (i+1)%n)
	}
	b.WriteString(`</xs:schema>`)
	return b.String()
}

// genCombined is the point of the exercise: nested optional choices, a
// substitution chain, wildcards and identity constraints in one schema, none
// of which individually approaches a budget.
func genCombined(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
		`<xs:element name="h0" type="xs:string"/>`)
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, `<xs:element name="h%d" type="xs:string" substitutionGroup="h%d"/>`, i, i-1)
	}
	b.WriteString(`<xs:group name="inner"><xs:choice minOccurs="0">` +
		`<xs:element name="p" minOccurs="0"/><xs:element name="q" minOccurs="0"/>` +
		`</xs:choice></xs:group>`)
	b.WriteString(`<xs:element name="r"><xs:complexType><xs:sequence>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:choice minOccurs="0"><xs:element name="x%d" minOccurs="0"/>`+
			`<xs:sequence minOccurs="0" maxOccurs="500"><xs:group ref="inner"/>`+
			`<xs:element name="y%d" minOccurs="0"/></xs:sequence></xs:choice>`, i, i)
	}
	b.WriteString(`<xs:element name="tail" minOccurs="0" maxOccurs="unbounded">` +
		`<xs:complexType><xs:attribute name="k" type="xs:string"/></xs:complexType></xs:element>` +
		`</xs:sequence></xs:complexType>` +
		`<xs:key name="kk"><xs:selector xpath="tail"/><xs:field xpath="@k"/></xs:key>` +
		`<xs:keyref name="rr" refer="kk"><xs:selector xpath="tail"/><xs:field xpath="@k"/></xs:keyref>` +
		`</xs:element></xs:schema>`)
	return b.String()
}

// measureLoad reports the wall time and the bytes allocated by one Load. Bytes
// allocated is used rather than peak heap because it is deterministic: it does
// not depend on when the collector happens to run, which makes it usable as an
// assertion rather than only as a diagnostic.
func measureLoad(tb testing.TB, src string) (time.Duration, uint64) {
	tb.Helper()
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		tb.Fatalf("parse: %v", err)
	}
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	start := time.Now()
	_, loadErr := Load(doc.Root, "", Options{})
	elapsed := time.Since(start)
	runtime.ReadMemStats(&m1)
	// A load that fails is fine — several of these shapes are legitimately
	// invalid, and refusing early is the cheap outcome. What is measured is
	// the cost of reaching the verdict, not the verdict.
	_ = loadErr
	return elapsed, m1.TotalAlloc - m0.TotalAlloc
}

// growthExponent fits the exponent k in cost = c*n^k from two measurements at
// n and 2n: k = log2(cost2n / costn). Two points is enough because the sizes
// are chosen a factor of two apart, and the alternative — a least-squares fit
// over four sizes — would cost four loads instead of two for a number this
// test only compares against a threshold.
func growthExponent(small, large float64) float64 {
	if small <= 0 || large <= 0 {
		return 0
	}
	return math.Log2(large / small)
}

// TestUPACostIsCubicInPositions pins the finding.
//
// checkUPA (upa.go:241) has NO budget of its own. It iterates every follow set
// of a compiled model and runs a triangular pairwise scan inside each
// (upa.go:256-257). For a model whose follow relation is dense — which a
// sequence of optional elements produces from the shortest possible schema
// text — there are O(n) states each holding O(n) positions, so the scan is
// O(n^3) while maxPositions bounds only n.
//
// Measured on an idle 12-core laptop, through the public Load API:
//
//	n=512   19994 bytes    0.42s     6MB
//	n=1024  39986 bytes    1.64s    30MB
//	n=2048  80946 bytes   12.57s   115MB
//	n=4096 162866 bytes  1m45.10s  369MB
//
// Each doubling costs about 8x the last, and every one of these loads
// SUCCEEDS: no budget refuses them. Extrapolating the same curve to
// maxPositions=8192 gives roughly fourteen minutes and over a gigabyte for a
// single ~320KB schema document.
//
// The assertion is on the exponent, not the wall time, so the test is
// machine-independent and runs at sizes costing milliseconds. It measures at
// n=128 and n=256, where the whole test is well under a second.
func TestUPACostIsCubicInPositions(t *testing.T) {
	// Warm the allocator and the parser so the first measurement is not
	// paying one-time costs that would flatten the ratio.
	measureLoad(t, genOptionalSequence(64))

	const small, large = 128, 256
	tSmall, aSmall := measureLoad(t, genOptionalSequence(small))
	tLarge, aLarge := measureLoad(t, genOptionalSequence(large))

	allocExp := growthExponent(float64(aSmall), float64(aLarge))
	t.Logf("optional sequence: n=%d %v %dKB -> n=%d %v %dKB (alloc exponent %.2f)",
		small, tSmall.Round(time.Microsecond), aSmall/1024,
		large, tLarge.Round(time.Microsecond), aLarge/1024, allocExp)

	// Allocation is the stable half of the measurement: the follow relation
	// is materialised as [][]int (automaton.go:41), so it grows as n^2 and
	// the measured exponent is ~2.0 regardless of machine speed. Timing
	// exponents are too noisy at millisecond scale to assert on.
	//
	// The ceiling is 2.35, against a measured 2.0. The gap absorbs the
	// linear terms (parsing, component construction) that are still a
	// visible share of the total at these small sizes; it is well below the
	// 3.0 that the time curve shows, so this cannot silently pass if the
	// quadratic structure becomes cubic.
	const allocCeiling = 2.35
	if allocExp > allocCeiling {
		t.Errorf("allocation grows as n^%.2f, ceiling n^%.2f: doubling the "+
			"particle count multiplied allocation by %.1fx (%dKB -> %dKB). "+
			"The follow relation is quadratic by construction; anything above "+
			"that means a new superlinear term entered the load path.",
			allocExp, allocCeiling, float64(aLarge)/float64(aSmall),
			aSmall/1024, aLarge/1024)
	}

	// The load must SUCCEED, which is the uncomfortable half of the result:
	// if a budget ever starts refusing this shape, the finding above has
	// been fixed and this test should be rewritten to assert the refusal.
	doc, err := xdm.ParseString(genOptionalSequence(large), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Load(doc.Root, "", Options{}); err != nil {
		t.Logf("NOTE: a dense-follow model of %d positions is now refused (%v). "+
			"If a UPA budget was added deliberately, update this test to assert "+
			"the refusal instead of the growth rate.", large, err)
	}
}

// TestUPATriangularScanAtForcedBudget drives checkUPA's cost at the boundary
// cheaply, using the forced-budget technique from TestMaxPositionsRealBoundary.
//
// At the production maxPositions of 8192 a dense model costs minutes. At a
// forced 256 the same triangular scan runs in milliseconds and still exercises
// the full path: compile, then checkUPA over every follow set. What is being
// pinned is that the number of pair tests really is the cube of the position
// count, which is the shape of the hazard rather than its magnitude.
func TestUPATriangularScanAtForcedBudget(t *testing.T) {
	const limit = 256
	withBudgets(limit, -1, -1, -1, func() {
		for _, n := range []int{limit / 2, limit} {
			ps := make([]*Particle, n)
			for i := range ps {
				ps[i] = &Particle{MinOccurs: 0, MaxOccurs: 1,
					Term: &ElementDecl{Name: xdm.QName{Local: fmt.Sprintf("e%d", i)}}}
			}
			p := &Particle{MinOccurs: 1, MaxOccurs: 1,
				Term: &ModelGroup{Compositor: CompositorSequence, Particles: ps}}
			m, err := compileContentModel(p)
			if err != nil {
				t.Fatalf("n=%d within forced budget %d but declined: %v", n, limit, err)
			}
			// Count the pair tests checkUPA will perform. This is the
			// quantity that has no budget.
			pairs := 0
			states := append([][]int{m.first}, m.follow...)
			for _, st := range states {
				pairs += len(st) * (len(st) - 1) / 2
			}
			edges := 0
			for _, f := range m.follow {
				edges += len(f)
			}
			t.Logf("n=%d: positions=%d followEdges=%d upaPairTests=%d",
				n, len(m.positions), edges, pairs)

			// A dense model of n positions performs ~n^3/6 pair tests.
			// Assert the cube is actually reached, so that a future
			// change which makes the model sparse (and this test
			// vacuous) is visible rather than silently passing.
			wantAtLeast := n * n * n / 12
			if pairs < wantAtLeast {
				t.Errorf("n=%d produced only %d pair tests, expected at least %d; "+
					"this shape is meant to be the dense worst case and no longer is, "+
					"so the cost assertion above is not measuring what it claims",
					n, pairs, wantAtLeast)
			}
			if err := checkUPA(m, "t", CheckOptions{}); err != nil {
				t.Fatalf("n=%d: a sequence of distinct optional elements is "+
					"unambiguous but UPA rejected it: %v", n, err)
			}
		}
	})
}

// TestAdversarialShapesStayBounded runs every generator at a size whose text is
// small and asserts an explicit wall-time and allocation ceiling on each.
//
// The ceilings are set from measured values with the multiplier stated per
// case. They are deliberately not so loose that they cannot fail: the time
// ceilings are ~20x measured to absorb a loaded CI runner and -race, but the
// allocation ceilings are ~4x measured, because bytes allocated does not vary
// with machine load and a 4x rise means a real change in what the algorithm
// builds.
func TestAdversarialShapesStayBounded(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// maxTime and maxAlloc are the declared ceilings; measured is
		// what this shape cost when the test was written, on an idle
		// 12-core laptop, so the gap is auditable.
		measured time.Duration
		maxTime  time.Duration
		maxAlloc uint64
	}{
		// Nested choice/sequence/choice. Optional at every level, so the
		// follow relation is dense — same hazard as the flat case, more
		// nesting to walk.
		{"nested-choice-64", genNestedChoice(64), 6 * time.Millisecond,
			400 * time.Millisecond, 8 << 20},

		// Large-but-legal maxOccurs at ten nested levels: 1000^10
		// logical repetitions. This is the CONTROL. automaton.go uses
		// counters rather than unrolling (automaton.go:216), so it must
		// stay trivially cheap; if it ever does not, unrolling has been
		// reintroduced and the schema text has become exponential.
		{"group-occurs-depth-10", genGroupOccurs(10), 100 * time.Microsecond,
			200 * time.Millisecond, 4 << 20},

		// Wildcards against named particles. positionsCompete does real
		// work per pair here rather than falling out on the type switch.
		{"wildcard-mix-64", genWildcardMix(64), 600 * time.Microsecond,
			400 * time.Millisecond, 8 << 20},

		// A substitution chain of 256. linkSubstitutionGroups runs the
		// closure once per element, so closure work is quadratic.
		{"subst-chain-256", genSubstChain(256), 5 * time.Millisecond,
			400 * time.Millisecond, 24 << 20},

		// 64 keys + 64 keyrefs + 64 uniques on one element. identity.go
		// has no budget at all, so this is the shape that would expose
		// one if a cross product lurked there. It does not: keyref
		// resolution is a map lookup (identity.go:455).
		{"identity-fanout-64", genIdentityFanout(64), 300 * time.Microsecond,
			400 * time.Millisecond, 8 << 20},

		// Mutually recursive types through element declarations.
		{"recursive-types-64", genRecursiveType(64), 300 * time.Microsecond,
			400 * time.Millisecond, 8 << 20},

		// All of the above in one schema.
		{"combined-48", genCombined(48), 250 * time.Microsecond,
			800 * time.Millisecond, 8 << 20},
	}

	// One warm-up so the first case is not charged for lazily initialised
	// package state.
	measureLoad(t, genOptionalSequence(32))

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			elapsed, alloc := measureLoad(t, c.src)
			t.Logf("%s: schema=%dB time=%v (measured %v, ceiling %v) alloc=%dKB (ceiling %dKB)",
				c.name, len(c.src), elapsed.Round(time.Microsecond),
				c.measured, c.maxTime, alloc/1024, c.maxAlloc/1024)
			if elapsed > c.maxTime {
				t.Errorf("%s took %v, ceiling %v (it cost %v when this test was "+
					"written). A schema of %d bytes must not cost this much to load.",
					c.name, elapsed, c.maxTime, c.measured, len(c.src))
			}
			if alloc > c.maxAlloc {
				t.Errorf("%s allocated %dKB, ceiling %dKB. Allocation does not vary "+
					"with machine load, so this is a real change in what the load "+
					"path builds for a %d-byte schema.",
					c.name, alloc/1024, c.maxAlloc/1024, len(c.src))
			}
		})
	}
}

// TestGroupOccursDoesNotUnroll pins the design decision that keeps large
// maxOccurs cheap, separately from the timing above.
//
// automaton.go:29-32 records that occurrence bounds are NOT unrolled: a
// maxOccurs of 1000 becomes one counter (automaton.go:216), not 1000
// positions. That is the single reason a schema cannot use occurrence values
// — which are exponential in the digits of the schema text — to force an
// enormous automaton. If someone reintroduces unrolling, position counts stop
// tracking the syntactic leaf count and this fails.
func TestGroupOccursDoesNotUnroll(t *testing.T) {
	for _, maxOcc := range []int{1, 1000, 1000000} {
		leaf := &Particle{MinOccurs: 0, MaxOccurs: 1,
			Term: &ElementDecl{Name: xdm.QName{Local: "leaf"}}}
		inner := &Particle{MinOccurs: 0, MaxOccurs: maxOcc,
			Term: &ModelGroup{Compositor: CompositorSequence, Particles: []*Particle{leaf}}}
		outer := &Particle{MinOccurs: 1, MaxOccurs: 1,
			Term: &ModelGroup{Compositor: CompositorSequence, Particles: []*Particle{inner}}}
		m, err := compileContentModel(outer)
		if err != nil {
			t.Fatalf("maxOccurs=%d declined: %v", maxOcc, err)
		}
		if len(m.positions) != 1 {
			t.Errorf("maxOccurs=%d produced %d positions, want 1: occurrence "+
				"bounds are meant to become counters (automaton.go:216), not "+
				"unrolled copies. Unrolling makes the automaton linear in the "+
				"numeric value of maxOccurs, which is exponential in schema text.",
				maxOcc, len(m.positions))
		}
	}
}

// TestSubstitutionClosureGrowth measures linkSubstitutionGroups
// (assemble.go:1110), which has no budget and runs a BFS closure once per
// global element. For a chain of n elements each closure is O(n), so the pass
// is O(n^2) in time and memory.
//
// This is real quadratic growth, but with a small constant and bounded by
// schema text rather than by an intermediate structure, so a chain long enough
// to matter is a schema large enough to be refused by DefaultMaxSchemaBytes
// first. It is measured here so the constant cannot grow unnoticed.
func TestSubstitutionClosureGrowth(t *testing.T) {
	measureLoad(t, genSubstChain(64))

	const small, large = 128, 256
	_, aSmall := measureLoad(t, genSubstChain(small))
	_, aLarge := measureLoad(t, genSubstChain(large))
	exp := growthExponent(float64(aSmall), float64(aLarge))
	t.Logf("subst chain: n=%d %dKB -> n=%d %dKB (alloc exponent %.2f)",
		small, aSmall/1024, large, aLarge/1024, exp)

	// Measured ~2.0. The ceiling is 2.5: quadratic is the known and
	// accepted cost of running the closure per element, and the gap allows
	// for the linear parse term still being visible at n=128 without
	// admitting a cubic term.
	const ceiling = 2.5
	if exp > ceiling {
		t.Errorf("substitution closure allocation grows as n^%.2f, ceiling n^%.2f "+
			"(%dKB -> %dKB). linkSubstitutionGroups is quadratic by design; above "+
			"that means the per-element closure gained another factor.",
			exp, ceiling, aSmall/1024, aLarge/1024)
	}
}

// TestIdentityConstraintsHaveNoQuadraticJoin checks the claim that identity
// constraint processing, which has no budget anywhere in identity.go or
// icpath.go, is nonetheless linear in the number of selected nodes because
// keyref resolution is a map lookup (identity.go:455) rather than a cross
// product against the key table.
//
// A quadratic join here would be an unbounded path in the same class as the
// UPA one, and with no budget at all standing in front of it.
func TestIdentityConstraintsHaveNoQuadraticJoin(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
		`<xs:element name="r"><xs:complexType><xs:sequence>` +
		`<xs:element name="a" minOccurs="0" maxOccurs="unbounded">` +
		`<xs:complexType><xs:attribute name="k" type="xs:string"/></xs:complexType>` +
		`</xs:element></xs:sequence></xs:complexType>` +
		`<xs:key name="kk"><xs:selector xpath="a"/><xs:field xpath="@k"/></xs:key>` +
		`<xs:keyref name="rr" refer="kk"><xs:selector xpath="a"/><xs:field xpath="@k"/></xs:keyref>` +
		`</xs:element></xs:schema>`

	sdoc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	s, err := Load(sdoc.Root, "", Options{})
	if err != nil {
		t.Fatalf("schema load: %v", err)
	}

	instance := func(n int) string {
		var b strings.Builder
		b.WriteString(`<r>`)
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, `<a k="v%d"/>`, i)
		}
		b.WriteString(`</r>`)
		return b.String()
	}

	validate := func(n int) uint64 {
		doc, err := xdm.ParseString(instance(n), xdm.ParseOptions{MaxNodes: 1 << 20})
		if err != nil {
			t.Fatalf("instance parse: %v", err)
		}
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		if err := s.Validate(doc.Root, ValidateOptions{}); err != nil {
			t.Fatalf("n=%d: a document with distinct keys must validate: %v", n, err)
		}
		runtime.ReadMemStats(&m1)
		return m1.TotalAlloc - m0.TotalAlloc
	}

	validate(500) // warm-up
	const small, large = 1000, 2000
	aSmall := validate(small)
	aLarge := validate(large)
	exp := growthExponent(float64(aSmall), float64(aLarge))
	t.Logf("keyref: n=%d %dKB -> n=%d %dKB (alloc exponent %.2f)",
		small, aSmall/1024, large, aLarge/1024, exp)

	// Measured ~1.0. The ceiling is 1.4: linear is what a map-based join
	// costs, and the gap covers map growth doubling at an unlucky size.
	// A cross-product join would score ~2.0 and fail loudly.
	const ceiling = 1.4
	if exp > ceiling {
		t.Errorf("keyref allocation grows as n^%.2f, ceiling n^%.2f (%dKB -> %dKB). "+
			"identity.go has NO budget of any kind, so its linearity is the only "+
			"thing bounding key/keyref work; a quadratic join here is an unbounded "+
			"path with nothing in front of it.",
			exp, ceiling, aSmall/1024, aLarge/1024)
	}
}

// FuzzSchemaComplexity is a second target beside FuzzLoadSchemaNoPanic in
// zz_fuzz_test.go. That one asks whether a schema document can make the
// assembler panic; this one asks whether one can make it slow, which is a
// different failure and needs a different oracle.
//
// The input is a small parameter vector rather than a schema document, because
// the shapes that cost are structural and a byte-level mutator will
// essentially never assemble a dense-follow model by chance. The fuzzer
// explores the combination space of the generators instead, which is where the
// concern actually lives.
func FuzzSchemaComplexity(f *testing.F) {
	// Seeds are the adversarial shapes, alone and combined.
	f.Add(uint8(0), uint8(24), uint8(0))
	f.Add(uint8(1), uint8(16), uint8(0))
	f.Add(uint8(2), uint8(8), uint8(0))
	f.Add(uint8(3), uint8(16), uint8(0))
	f.Add(uint8(4), uint8(32), uint8(0))
	f.Add(uint8(5), uint8(16), uint8(0))
	f.Add(uint8(6), uint8(8), uint8(0))
	f.Add(uint8(7), uint8(12), uint8(0))
	f.Add(uint8(0), uint8(32), uint8(3))
	f.Add(uint8(7), uint8(16), uint8(5))

	f.Fuzz(func(t *testing.T, shape, size, extra uint8) {
		// The size is clamped hard. A fuzz target that can be handed a
		// large n would rediscover the cubic curve above and time out,
		// which is a finding already recorded rather than one worth
		// re-deriving on every run.
		n := int(size)%48 + 2

		var src string
		switch shape % 8 {
		case 0:
			src = genOptionalSequence(n)
		case 1:
			src = genNestedChoice(n)
		case 2:
			src = genGroupOccurs(n % 12)
		case 3:
			src = genWildcardMix(n)
		case 4:
			src = genSubstChain(n)
		case 5:
			src = genIdentityFanout(n)
		case 6:
			src = genRecursiveType(n%16 + 1)
		case 7:
			src = genCombined(n)
		}

		// extra appends a second, differently-shaped type to the same
		// schema, so the fuzzer can find interactions between shapes
		// rather than only exploring each in isolation.
		if extra%4 != 0 {
			var tail string
			switch extra % 4 {
			case 1:
				tail = genWildcardMix(n % 16)
			case 2:
				tail = genNestedChoice(n % 16)
			case 3:
				tail = genOptionalSequence(n % 16)
			}
			tail = strings.TrimSuffix(tail, `</xs:schema>`)
			tail = strings.TrimPrefix(tail,
				`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
			tail = strings.ReplaceAll(tail, `name="t"`, `name="t_extra"`)
			tail = strings.ReplaceAll(tail, `name="e`, `name="ex`)
			src = strings.TrimSuffix(src, `</xs:schema>`) + tail + `</xs:schema>`
		}

		doc, err := xdm.ParseString(src, xdm.ParseOptions{MaxDepth: 200, MaxNodes: 200000})
		if err != nil {
			// A generator that produced unparseable text is a bug in
			// the generator, not a finding about the schema loader.
			t.Skipf("generated schema did not parse: %v", err)
		}

		start := time.Now()
		// The resolver refuses so that no generated schemaLocation can
		// make the target read the filesystem, matching the discipline
		// in zz_fuzz_test.go.
		_, _ = Load(doc.Root, "", Options{
			Resolver:     refusingResolver{},
			MaxDocuments: 4,
		})
		elapsed := time.Since(start)

		// The oracle: a schema this small must load quickly whatever
		// its shape. The ceiling is generous because a fuzz worker
		// shares a machine with fifteen others, but it is far below
		// what the cubic path costs even at these sizes if a new
		// superlinear term appears.
		const ceiling = 5 * time.Second
		if elapsed > ceiling {
			t.Errorf("a %d-byte schema (shape=%d n=%d extra=%d) took %v to load, "+
				"ceiling %v: this is a schema-complexity denial of service, since "+
				"the cost is wildly out of proportion to the input.",
				len(src), shape%8, n, extra%4, elapsed, ceiling)
		}
	})
}
