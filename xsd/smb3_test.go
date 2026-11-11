package xsd

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// TestSMB3InstanceLocalPathNeverReadsAsScheme: a relative location whose
// cleaned join begins "file:" or "http:" must come back as an absolute path, so
// a resolver re-parsing it cannot open the file: URI (or fetch the URL) it
// spells. Both separators are exercised; on Unix the backslash form is one
// odd file name, on Windows it cleans like the slash form.
func TestSMB3InstanceLocalPathNeverReadsAsScheme(t *testing.T) {
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, loc := range []string{
		"./file:/etc/passwd",
		"x/../file:/etc/passwd",
		`.\file:\Windows\win.ini`,
		`x\..\file:\Windows\win.ini`,
		"./FILE:/etc/passwd",
		"x/../http:/evil.example/a.xsd",
		`x\..\https:\evil.example\a.xsd`,
	} {
		got, err := instanceLocalPath(loc, ".", []string{dir})
		if err != nil {
			// Refusing is also safe.
			continue
		}
		if !filepath.IsAbs(got) {
			t.Errorf("%q -> %q, not absolute", loc, got)
		}
		if u, err := url.Parse(got); err == nil {
			switch strings.ToLower(u.Scheme) {
			case "file", "http", "https", "ftp":
				t.Errorf("%q -> %q, re-read as scheme %q", loc, got, u.Scheme)
			}
		}
	}
}

// TestSMB3GroupWildcardMemoisedPerGroup: one question about the top of a
// chain answers every group on it, and a cycle's members share one answer.
func TestSMB3GroupWildcardMemoisedPerGroup(t *testing.T) {
	const n = 2000
	gs := make([]*AttributeGroupDef, n)
	for i := range gs {
		gs[i] = &AttributeGroupDef{AttributeWildcard: &Wildcard{
			Kind: NSEnumerated, Namespace: []string{"a", "b"}}}
		if i > 0 {
			gs[i].refs = []*AttributeGroupDef{gs[i-1]}
		}
	}
	gs[0].AttributeWildcard = &Wildcard{Kind: NSEnumerated, Namespace: []string{"a"}}
	p := &parser{}
	w := p.groupWildcard(gs[n-1])
	if len(p.groupWild) != n {
		t.Fatalf("memoised %d groups after one walk, want %d", len(p.groupWild), n)
	}
	if w == nil || fmt.Sprint(w.Namespace) != "[a]" {
		t.Fatalf("chain wildcard = %+v, want {a}", w)
	}

	a := &AttributeGroupDef{AttributeWildcard: &Wildcard{Kind: NSEnumerated, Namespace: []string{"a", "b"}}}
	b := &AttributeGroupDef{AttributeWildcard: &Wildcard{Kind: NSEnumerated, Namespace: []string{"b", "c"}}}
	a.refs, b.refs = []*AttributeGroupDef{b}, []*AttributeGroupDef{a}
	p = &parser{}
	for _, g := range []*AttributeGroupDef{a, b} {
		if w := p.groupWildcard(g); w == nil || fmt.Sprint(w.Namespace) != "[b]" {
			t.Errorf("cycle wildcard = %+v, want {b}", w)
		}
	}
}

// TestSMB3UnionCyclesOneWalk: every union on a ring is reported, a union that
// only refers into a cycle is not, and a long acyclic chain is clean.
func TestSMB3UnionCyclesOneWalk(t *testing.T) {
	const n = 3000
	chain := func(ring bool) string {
		var b strings.Builder
		first := "xs:int"
		if ring {
			first = fmt.Sprintf("u%d", n-1)
		}
		fmt.Fprintf(&b, `<xs:simpleType name="u0"><xs:union memberTypes="%s"/></xs:simpleType>`, first)
		for i := 1; i < n; i++ {
			fmt.Fprintf(&b, `<xs:simpleType name="u%d"><xs:union memberTypes="u%d"/></xs:simpleType>`, i, i-1)
		}
		return b.String()
	}
	if _, err := Load(sm09Schema(t, chain(false)), "", Options{}); err != nil {
		t.Fatalf("acyclic chain: %v", err)
	}
	_, err := Load(sm09Schema(t, chain(true)), "", Options{})
	if got := strings.Count(fmt.Sprint(err), "st-props-correct.2"); got != n {
		t.Fatalf("ring of %d unions: %d st-props-correct.2 reports", n, got)
	}

	_, err = Load(sm09Schema(t,
		`<xs:simpleType name="u1"><xs:union memberTypes="u2"/></xs:simpleType>`+
			`<xs:simpleType name="u2"><xs:union memberTypes="u1"/></xs:simpleType>`+
			`<xs:simpleType name="w"><xs:union memberTypes="u1"/></xs:simpleType>`), "", Options{})
	if msg := fmt.Sprint(err); !strings.Contains(msg, `"u1"`) ||
		!strings.Contains(msg, `"u2"`) || strings.Contains(msg, `"w"`) {
		t.Fatalf("err = %v, want u1 and u2 circular, w not", err)
	}
}

// TestSMB3UnionIsPureEndsOnBaseCycle: a circular base chain of unions ends
// the purity walk instead of looping.
func TestSMB3UnionIsPureEndsOnBaseCycle(t *testing.T) {
	a := &SimpleType{Variety: VarietyUnion}
	b := &SimpleType{Variety: VarietyUnion, Base: a}
	a.Base = b
	if !unionIsPure(a) {
		t.Fatal("facet-free union cycle reported impure")
	}
}

// TestSMB3OptionalAllMembersIndexed: positions come from the first
// occurrence of each member in the flattened list.
func TestSMB3OptionalAllMembersIndexed(t *testing.T) {
	const n = 4000
	inner := &ModelGroup{Compositor: CompositorAll}
	for i := 0; i < n; i++ {
		inner.Particles = append(inner.Particles,
			&Particle{MinOccurs: 1, MaxOccurs: 1, Term: &ElementDecl{}})
	}
	outerOwn := &Particle{MinOccurs: 1, MaxOccurs: 1, Term: &ElementDecl{}}
	g := &ModelGroup{Compositor: CompositorAll, Particles: []*Particle{
		outerOwn, {MinOccurs: 0, MaxOccurs: 1, Term: inner}}}
	flat := append([]*Particle{outerOwn}, inner.Particles...)
	flat = append(flat, inner.Particles[0]) // a later duplicate is ignored
	got := optionalAllMembers(g, flat)
	if len(got) != n || got[0] != 1 || got[n-1] != n {
		t.Fatalf("got %d indices (first %v), want %d starting at 1", len(got), got[:1], n)
	}
}
