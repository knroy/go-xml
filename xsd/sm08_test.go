package xsd

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

// sm08Within fails the test if f does not return within d. Every walk below
// was exponential or quadratic before round SM, so a regression hangs rather
// than fails; the deadline turns that into a failure.
func sm08Within(t *testing.T, d time.Duration, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("did not finish within %v: the walk is following paths, not the graph", d)
	}
}

// emptyDAGGroups writes g0..g(n-1), each a sequence of two refs to the next
// carrying refOccurs, ending in an empty <xs:sequence/>: valid, acyclic, a few
// KB, and 2^(n-1) paths deep.
func emptyDAGGroups(n int, refOccurs string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:group name="g%d"><xs:sequence>`, i)
		if i < n-1 {
			fmt.Fprintf(&b, `<xs:group ref="t:g%d" %s/><xs:group ref="t:g%d" %s/>`,
				i+1, refOccurs, i+1, refOccurs)
		}
		b.WriteString(`</xs:sequence></xs:group>`)
	}
	return b.String()
}

const sm08Head = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:t" targetNamespace="urn:t">`

// The content-model compile expanded a shared group once per path, and a
// group yielding no position charged no budget, so the empty-leaf DAG cost
// 2^(n-1) visits and, with minOccurs="0" refs, retained 2^n counters.
func TestSM08ZeroPositionGroupDAGCompilesInGraphTime(t *testing.T) {
	const n = 40
	for _, occurs := range []string{``, `minOccurs="0"`} {
		src := sm08Head + emptyDAGGroups(n, occurs) +
			`<xs:complexType name="C"><xs:sequence><xs:group ref="t:g0" ` + occurs +
			`/></xs:sequence></xs:complexType><xs:element name="root" type="t:C"/></xs:schema>`
		st, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var s *Schema
		sm08Within(t, 10*time.Second, func() { s, err = Load(st.Root, "", Options{}) })
		if err != nil {
			t.Fatalf("occurs %q: Load: %v", occurs, err)
		}
		ct := s.Types[xdm.QName{URI: "urn:t", Local: "C"}].(*ComplexType)
		m, err := compileForChecks(ct.Particle, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(m.counters); got > 2*n {
			t.Errorf("occurs %q: the compiled model retains %d counters, want at most %d",
				occurs, got, 2*n)
		}
		// The validation-time compile (modelFor) takes the same build.
		doc, _ := xdm.ParseString(`<t:root xmlns:t="urn:t"/>`, xdm.ParseOptions{})
		sm08Within(t, 10*time.Second, func() { err = s.Validate(doc.Root, ValidateOptions{}) })
		if err != nil {
			t.Errorf("occurs %q: Validate: %v", occurs, err)
		}
	}
}

func sm08Elems(n, min int) []*Particle {
	ps := make([]*Particle, n)
	for i := range ps {
		ps[i] = &Particle{MinOccurs: min, MaxOccurs: 1,
			Term: &ElementDecl{Name: xdm.QName{Local: "a"}}}
	}
	return ps
}

// Every a? had a counter of its own, so no two positions of (a?|...|a?)* or
// of a dense (a?,...,a?) shared a class, and each child cost live states x
// follow width x counter-vector length: 4 s at n=1024, 6 s at n=400.
func TestSM08OptionalAlternativesMatchLinear(t *testing.T) {
	const n, kids = 1024, 4
	sm, err := NewSequenceMatcher(&Particle{MinOccurs: 0, MaxOccurs: Unbounded,
		Term: &ModelGroup{Compositor: CompositorChoice, Particles: sm08Elems(n, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	var ok bool
	stats := withStats(func() { ok, _ = sm.Match(slices.Repeat([]xdm.QName{{Local: "a"}}, kids)) })
	if !ok {
		t.Fatal("four <a/> match (a?|a?|...)*")
	}
	if got := stats.MatchSteps.Load(); got > 4*n*kids {
		t.Errorf("(a?|...)*: %d children examined %d follow members, want at most %d", kids, got, 4*n*kids)
	}

	const d = 400
	sm, err = NewSequenceMatcher(&Particle{MinOccurs: 1, MaxOccurs: 1,
		Term: &ModelGroup{Compositor: CompositorSequence, Particles: sm08Elems(d, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	stats = withStats(func() { ok, _ = sm.Match(slices.Repeat([]xdm.QName{{Local: "a"}}, d)) })
	if !ok {
		t.Fatalf("%d <a/> match a dense (a?,...) of %d", d, d)
	}
	if got := stats.MatchSteps.Load(); got > 4*d*d {
		t.Errorf("dense (a?,...): %d children examined %d follow members, want at most %d", d, got, 4*d*d)
	}
	if ok, _ := sm.Match(slices.Repeat([]xdm.QName{{Local: "a"}}, d+1)); ok {
		t.Errorf("%d <a/> must not match a dense (a?,...) of %d", d+1, d)
	}

	// A counter enclosing a bounded one still decides restarts and is
	// kept: (a{2,3})* takes 4 a's only by restarting the inner scope.
	sm, err = NewSequenceMatcher(&Particle{MinOccurs: 0, MaxOccurs: Unbounded,
		Term: &ModelGroup{Compositor: CompositorSequence, Particles: []*Particle{{
			MinOccurs: 2, MaxOccurs: 3, Term: &ElementDecl{Name: xdm.QName{Local: "a"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range []bool{true, false, true, true, true, true} {
		if ok, _ := sm.Match(slices.Repeat([]xdm.QName{{Local: "a"}}, k)); ok != want {
			t.Errorf("(a{2,3})* on %d a: %v, want %v", k, ok, want)
		}
	}
}

// The redefine check ran the restriction walk from a postFixup, before
// checkGroupCycles, so a replacement reaching a circular group spun forever or
// overflowed the stack fatally.
func TestSM08RedefinedGroupReachingCycleIsRejected(t *testing.T) {
	for _, c := range []struct {
		name, base, body string
		v                Version
	}{
		{"1.0", `<xs:group name="G"><xs:sequence><xs:element name="a"/></xs:sequence></xs:group>`,
			`<xs:group name="G"><xs:sequence><xs:group ref="t:C"/></xs:sequence></xs:group>`, Version10},
		{"1.1", `<xs:group name="G"><xs:sequence><xs:element name="a"/></xs:sequence></xs:group>`,
			`<xs:group name="G"><xs:sequence><xs:group ref="t:C"/></xs:sequence></xs:group>`, Version11},
		{"recurse", `<xs:group name="G"><xs:sequence><xs:element name="a"/>` +
			`<xs:element name="b" maxOccurs="unbounded"/></xs:sequence></xs:group>`,
			`<xs:group name="G"><xs:sequence><xs:element name="a"/><xs:group ref="t:D"/></xs:sequence></xs:group>`,
			Version10},
	} {
		t.Run(c.name, func(t *testing.T) {
			docs := map[string]string{
				"base.xsd": sm08Head + c.base + `</xs:schema>`,
				"main.xsd": sm08Head + `<xs:redefine schemaLocation="base.xsd">` + c.body +
					`</xs:redefine><xs:group name="C"><xs:sequence><xs:group ref="t:C"/></xs:sequence></xs:group>` +
					`<xs:group name="D"><xs:sequence><xs:element name="b"/><xs:group ref="t:D"/></xs:sequence></xs:group>` +
					`</xs:schema>`,
			}
			var err error
			sm08Within(t, 10*time.Second, func() { _, err = loadFromMapVersion(t, docs, c.v) })
			if err == nil || !strings.Contains(err.Error(), "mg-props-correct.2") {
				t.Fatalf("Load = %v, want mg-props-correct.2", err)
			}
		})
	}
}

// loadFromMapVersion is loadFromMap with a schema version.
func loadFromMapVersion(t *testing.T, docs map[string]string, v Version) (*Schema, error) {
	t.Helper()
	tree, err := xdm.ParseString(docs["main.xsd"], xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return Load(tree.Root, "main.xsd", Options{Version: v, Resolver: &MapResolver{ByLocation: docs}})
}

// sm08SeqDAG builds n shared sequence groups, each two 1..1 particles of the
// next, the last empty, and returns a particle of the first.
func sm08SeqDAG(n int) *Particle {
	g := &ModelGroup{Compositor: CompositorSequence}
	for i := 1; i < n; i++ {
		ref := func() *Particle { return &Particle{MinOccurs: 1, MaxOccurs: 1, Term: g} }
		g = &ModelGroup{Compositor: CompositorSequence, Particles: []*Particle{ref(), ref()}}
	}
	return &Particle{MinOccurs: 1, MaxOccurs: 1, Term: g}
}

// allBranchCounts recomputed a shared group's branches on every path, and a
// sequence-only DAG yields one branch per level, so the budget never tripped.
func TestSM08BranchCountsInGraphTime(t *testing.T) {
	var brs []branchCount
	var err error
	sm08Within(t, 5*time.Second, func() { brs, err = allBranchCounts(sm08SeqDAG(40)) })
	if err != nil || len(brs) != 1 {
		t.Fatalf("allBranchCounts = %d branches, %v; want 1, nil", len(brs), err)
	}
}

// recurse flattened same-compositor wrappers once per path: an empty-group DAG
// under a restricted base cost 2^n calls at the XSD 1.0 default.
func TestSM08InlineSameCompositorInGraphTime(t *testing.T) {
	var out []*Particle
	sm08Within(t, 5*time.Second, func() {
		out = inlineSameCompositor(sm08SeqDAG(40).Term.(*ModelGroup), map[*ModelGroup][]*Particle{})
	})
	if len(out) != 0 {
		t.Fatalf("an empty DAG inlines to %d particles, want 0", len(out))
	}
	src := sm08Head + emptyDAGGroups(40, ``) +
		`<xs:complexType name="B"><xs:sequence><xs:element name="e"/><xs:group ref="t:g0"/></xs:sequence></xs:complexType>` +
		`<xs:complexType name="R"><xs:complexContent><xs:restriction base="t:B"><xs:sequence>` +
		`<xs:element name="e"/></xs:sequence></xs:restriction></xs:complexContent></xs:complexType></xs:schema>`
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sm08Within(t, 10*time.Second, func() { _, err = Load(st.Root, "", Options{}) })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// inlinedTooLong did not count all-group flattening, so under 1.1 a ladder of
// all groups each holding two refs to the previous was flattened to 2^N
// particles by allSubsumes before anything refused it.
func TestSM08AllGroupLadderRefusedBeforeFlattening(t *testing.T) {
	const levels = 20
	var b strings.Builder
	b.WriteString(sm08Head + `<xs:group name="g0"><xs:all><xs:element name="a"/></xs:all></xs:group>`)
	for i := 1; i <= levels; i++ {
		fmt.Fprintf(&b, `<xs:group name="g%d"><xs:all><xs:group ref="t:g%d"/><xs:group ref="t:g%d"/></xs:all></xs:group>`, i, i-1, i-1)
	}
	fmt.Fprintf(&b, `<xs:complexType name="b"><xs:group ref="t:g%d"/></xs:complexType>`, levels)
	b.WriteString(`<xs:complexType name="r"><xs:complexContent><xs:restriction base="t:b"><xs:all>` +
		`<xs:element name="a"/></xs:all></xs:restriction></xs:complexContent></xs:complexType></xs:schema>`)
	st, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err = Load(st.Root, "", Options{Version: Version11})
	runtime.ReadMemStats(&after)
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("Load = %v, want a resource-limit refusal", err)
	}
	// The flattening allocated 538 MB at this size.
	if got := after.TotalAlloc - before.TotalAlloc; got > 64<<20 {
		t.Errorf("Load allocated %d MB before refusing, want under 64", got>>20)
	}
}

// typeRestricts recursed into every member of a pure union once per path.
func TestSM08UnionLadderTypeRestrictsInGraphTime(t *testing.T) {
	atomic := func(name string) *SimpleType {
		return &SimpleType{Name: xdm.QName{Local: name}, Variety: VarietyAtomic}
	}
	u := &SimpleType{Variety: VarietyUnion, MemberTypes: []*SimpleType{atomic("int"), atomic("date")}}
	for i := 0; i < 40; i++ {
		u = &SimpleType{Variety: VarietyUnion, MemberTypes: []*SimpleType{u, u}}
	}
	var ok bool
	sm08Within(t, 5*time.Second, func() { ok = typeRestricts(atomic("boolean"), u) })
	if ok {
		t.Fatal("an unrelated type restricts the union ladder")
	}
	if !typeRestricts(u.MemberTypes[0].MemberTypes[0], u) {
		t.Fatal("a nested member must restrict the pure union ladder")
	}
}

// wildcardSubset scanned super's namespace list once per sub namespace.
func TestSM08WildcardSubsetLinear(t *testing.T) {
	const k = 100000
	super := &Wildcard{Kind: NSEnumerated}
	for i := 0; i < k; i++ {
		super.Namespace = append(super.Namespace, fmt.Sprintf("u%d", i))
	}
	sub := &Wildcard{Kind: NSEnumerated, Namespace: slices.Repeat([]string{fmt.Sprintf("u%d", k-1)}, k)}
	var ok bool
	sm08Within(t, 5*time.Second, func() { ok = wildcardSubset(sub, super) })
	if !ok {
		t.Fatal("a repeated member of super is a subset of it")
	}
	for _, c := range []struct {
		sub, super *Wildcard
		want       bool
	}{
		{&Wildcard{Kind: NSEnumerated, Namespace: []string{"x"}}, super, false},
		{&Wildcard{Kind: NSEnumerated, Namespace: []string{"x", ""}}, &Wildcard{Kind: NSNot, Namespace: []string{"y"}}, true},
		{&Wildcard{Kind: NSEnumerated, Namespace: []string{""}}, &Wildcard{Kind: NSNot, Namespace: []string{"y"}, ExcludesAbsent: true}, false},
		{&Wildcard{Kind: NSEnumerated, Namespace: []string{"y"}}, &Wildcard{Kind: NSNot, Namespace: []string{"y"}}, false},
		{&Wildcard{Kind: NSNot, Namespace: []string{"a", "b"}}, &Wildcard{Kind: NSNot, Namespace: []string{"b"}}, true},
		{&Wildcard{Kind: NSNot, Namespace: []string{"b"}}, &Wildcard{Kind: NSNot, Namespace: []string{"a", "b"}}, false},
		{&Wildcard{Kind: NSAny, DisallowedNames: []xdm.QName{{URI: "y", Local: "n"}}},
			&Wildcard{Kind: NSAny, DisallowedNames: []xdm.QName{{URI: "y", Local: "n"}}}, true},
		{&Wildcard{Kind: NSAny}, &Wildcard{Kind: NSAny, DisallowedNames: []xdm.QName{{URI: "y", Local: "n"}}}, false},
		{&Wildcard{Kind: NSEnumerated, Namespace: []string{"x"}},
			&Wildcard{Kind: NSAny, DisallowedNames: []xdm.QName{{URI: "y", Local: "n"}}}, true},
	} {
		if got := wildcardSubset(c.sub, c.super); got != c.want {
			t.Errorf("wildcardSubset(%+v, %+v) = %v, want %v", c.sub, c.super, got, c.want)
		}
	}
}
