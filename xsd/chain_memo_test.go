package xsd

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// chainedValueSchema builds a document element whose repeated child is typed by
// the last link of an n-step restriction chain, so validating one value walks
// the whole chain.
//
// Every link writes a facet of its own, so no step is skipped by the chain
// walk, and the element type is xs:string rather than a numeric one so that a
// one-byte value is valid at every link.
func chainedValueSchema(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	b.WriteString(`<xs:simpleType name="T0"><xs:restriction base="xs:string">` +
		`<xs:maxLength value="100000"/></xs:restriction></xs:simpleType>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<xs:simpleType name="T%d"><xs:restriction base="T%d">`+
			`<xs:maxLength value="%d"/></xs:restriction></xs:simpleType>`,
			i, i-1, 100000-i)
	}
	fmt.Fprintf(&b, `<xs:element name="root"><xs:complexType><xs:sequence>`+
		`<xs:element name="v" type="T%d" maxOccurs="unbounded"/>`+
		`</xs:sequence></xs:complexType></xs:element>`, n)
	b.WriteString(`</xs:schema>`)
	return b.String()
}

// chainedValueDoc is m one-byte values under that root.
func chainedValueDoc(m int) string {
	var b strings.Builder
	b.WriteString(`<root>`)
	for i := 0; i < m; i++ {
		b.WriteString(`<v>x</v>`)
	}
	b.WriteString(`</root>`)
	return b.String()
}

// validateAllocs reports the bytes allocated by validating the document, with
// the schema load and the document parse left outside the measurement: those
// are paid once, and it is the per-value cost this test is about.
func validateAllocs(t *testing.T, chain, values int) uint64 {
	t.Helper()
	st, err := xdm.ParseString(chainedValueSchema(chain), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	doc, err := xdm.ParseString(chainedValueDoc(values), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse document: %v", err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := s.Validate(doc.Root, ValidateOptions{}); err != nil {
		t.Fatalf("document should be valid, got: %v", err)
	}
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestValidateAllocationFlatInChainDepth pins the per-value cost of validating
// against a long restriction chain, which is the shape a real industry
// vocabulary has: UBL and CII derive their types through hundreds of
// restriction steps.
//
// Three walks of the base chain ran once per validated value — facetChain
// rebuilding the whole chain as a slice, descendsFromInteger allocating a map
// per call purely as a cycle guard, and idKind's atomic walk — and together
// they held 98% of an allocation profile of this exact shape. A 500-link chain
// validating 8,000 one-byte values allocated 999 MB in 527 ms, 16 MB per KB of
// input, none of it retained: live heap was 5 MB before and after. It is pure
// transient churn, but a gigabyte allocated in half a second still kills a
// container on peak RSS, because the collector cannot keep up. Memoising the
// three answers per type took the same run to 2.7 MB.
//
// The test bounds allocation rather than time, which is what makes it stable:
// the figure does not move with machine load, and the race detector does not
// inflate it, so no separate budget is needed for the gate's race lane.
//
// The bound is deliberately loose in absolute terms — most of what is left is
// the document tree the validator walks, not the chain — and it is the *ratio*
// between the two chain lengths that catches a regression. A 500-link chain is
// ten times the 50-link one, so if any of the three walks runs per value again
// the deep case costs roughly ten times the shallow one; memoised, the two are
// within noise of each other. Measured after the fix: 2.6 MB at 50 links and
// 2.7 MB at 500, a ratio of 1.04. Before it: 67 MB and 999 MB, a ratio of 14.9.
func TestValidateAllocationFlatInChainDepth(t *testing.T) {
	if testing.Short() {
		t.Skip("allocation test")
	}
	const values = 8000

	shallow := validateAllocs(t, 50, values)
	deep := validateAllocs(t, 500, values)

	// A ceiling on the deep case on its own, so that a regression which
	// somehow lifted both ends equally is still caught. 64 MB is over 20x
	// the measured 2.7 MB and under a fifteenth of the 999 MB it cost
	// before.
	const ceiling = 64 << 20
	if deep > ceiling {
		t.Errorf("validating %d values against a 500-link chain allocated %.1f MB, "+
			"over the %d MB budget.\nA base-chain walk is running per value "+
			"again; see chainFactsOf.",
			values, float64(deep)/(1<<20), ceiling>>20)
	}

	// The ratio is the real assertion. 4x leaves ample room for noise in
	// two independent MemStats readings while sitting far under the 14.9x
	// a per-value walk produces.
	if ratio := float64(deep) / float64(shallow); ratio > 4 {
		t.Errorf("a 500-link chain allocated %.1fx what a 50-link one did "+
			"(%.1f MB vs %.1f MB); the per-value cost is scaling with chain "+
			"length again, so a base-chain walk is no longer memoised.",
			ratio, float64(deep)/(1<<20), float64(shallow)/(1<<20))
	}
}

// TestChainFactsAgreeWithWalk is the correctness half, and it is the half that
// matters: a memo that returns a wrong answer changes a validation verdict
// silently, which is far worse than a slow validation.
//
// Each case pins one of the three answers against a chain built to make it
// non-trivial: the fact is several steps up, so a memo that answered from the
// type's own step alone would get it wrong.
func TestChainFactsAgreeWithWalk(t *testing.T) {
	s, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="i1"><xs:restriction base="xs:integer"/></xs:simpleType>
	  <xs:simpleType name="i2"><xs:restriction base="i1"/></xs:simpleType>
	  <xs:simpleType name="i3">
	    <xs:restriction base="i2"><xs:maxInclusive value="10"/></xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="d1"><xs:restriction base="xs:decimal"/></xs:simpleType>
	  <xs:simpleType name="d2"><xs:restriction base="d1"/></xs:simpleType>
	  <xs:simpleType name="r1"><xs:restriction base="xs:IDREF"/></xs:simpleType>
	  <xs:simpleType name="r2"><xs:restriction base="r1"/></xs:simpleType>
	  <xs:simpleType name="k1"><xs:restriction base="xs:ID"/></xs:simpleType>
	  <xs:simpleType name="k2"><xs:restriction base="k1"/></xs:simpleType>
	  <xs:simpleType name="s1"><xs:restriction base="xs:string"/></xs:simpleType>
	</xs:schema>`)
	if err != nil {
		t.Fatalf("schema should load, got: %v", err)
	}
	typ := func(name string) *SimpleType {
		st, ok := s.Types[xdm.QName{Local: name}].(*SimpleType)
		if !ok {
			t.Fatalf("type %q missing", name)
		}
		return st
	}

	for _, c := range []struct {
		name string
		want bool
	}{
		{"i1", true}, {"i2", true}, {"i3", true},
		{"d1", false}, {"d2", false}, {"s1", false},
	} {
		if got := descendsFromInteger(typ(c.name)); got != c.want {
			t.Errorf("descendsFromInteger(%s) = %v, want %v", c.name, got, c.want)
		}
		// Asked twice: the second ask comes from the memo, and must
		// agree with the first.
		if got := descendsFromInteger(typ(c.name)); got != c.want {
			t.Errorf("descendsFromInteger(%s) memoised = %v, want %v",
				c.name, got, c.want)
		}
	}

	for _, c := range []struct {
		name string
		want string
	}{
		{"r1", "IDREF"}, {"r2", "IDREF"},
		{"k1", "ID"}, {"k2", "ID"},
		{"s1", ""}, {"i3", ""},
	} {
		if got := idKind(typ(c.name), "abc"); got != c.want {
			t.Errorf("idKind(%s) = %q, want %q", c.name, got, c.want)
		}
		if got := idKind(typ(c.name), "abc"); got != c.want {
			t.Errorf("idKind(%s) memoised = %q, want %q", c.name, got, c.want)
		}
	}

	// facetChain must report every step that carries facets, nearest
	// first, and must hand back the same slice on a second ask.
	steps := facetChain(typ("i3"))
	if len(steps) == 0 || steps[0].typ != typ("i3") {
		t.Fatalf("facetChain(i3): got %d steps, want the type's own step first",
			len(steps))
	}
	again := facetChain(typ("i3"))
	if len(again) != len(steps) {
		t.Errorf("facetChain(i3) memoised: %d steps, want %d", len(again), len(steps))
	}
	for i := range steps {
		if steps[i] != again[i] {
			t.Errorf("facetChain(i3) memoised: step %d differs", i)
		}
	}
}

// TestChainFactsTerminateOnCycle pins that memoising did not cost the walk its
// cycle guard.
//
// checkTypeBaseCycles rejects a circular schema at load, but it roots only at
// named global types, so an anonymous type on a cycle never reaches it. The
// guard is skipped for short chains as an optimisation, so this builds a cycle
// longer than that threshold as well as a minimal one.
func TestChainFactsTerminateOnCycle(t *testing.T) {
	for _, n := range []int{2, 200} {
		ring := make([]*SimpleType, n)
		for i := range ring {
			ring[i] = &SimpleType{Variety: VarietyAtomic, Facets: &FacetSet{}}
		}
		for i := range ring {
			ring[i].Base = ring[(i+1)%n]
		}

		done := make(chan int, 1)
		go func() { done <- len(facetChain(ring[0])) }()
		select {
		case got := <-done:
			if got != n {
				t.Errorf("ring of %d: got %d steps, want %d", n, got, n)
			}
		case <-timeoutAfterSecond():
			t.Fatalf("facetChain did not terminate on a %d-link cycle", n)
		}
	}
}
