package xsd

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// facetedChain builds T0 restricting xs:string, then n links each restricting
// its predecessor and writing a facet of its own, so no link is skipped by the
// chain walk and every merge has n steps to consider.
func facetedChain(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	b.WriteString(`<xs:simpleType name="T0"><xs:restriction base="xs:string"><xs:maxLength value="100000"/></xs:restriction></xs:simpleType>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b,
			`<xs:simpleType name="T%d"><xs:restriction base="T%d"><xs:maxLength value="%d"/></xs:restriction></xs:simpleType>`,
			i, i-1, 100000-i)
	}
	b.WriteString(`</xs:schema>`)
	return b.String()
}

// TestFacetMergeLinearInChainDepth pins the cost of the Part 2 facet
// constraints on a deep restriction chain. mergedFacets used to flatten the
// whole chain for every type it was asked about, so a schema of N chained
// restrictions cost O(N²) at load: 10,000 links took 15.4 s. The merged set
// is memoised per parser now and the merge no longer appears in a profile of
// the load. The 2.8 s that remained sat in checkTypeBaseCycles, a separate
// walk with the same shape, memoised in its turn and pinned by
// TestBaseCycleCheckLinearInChainDepth; the load is 0.05 s now. The bound
// stays where it was: well above anything a slow CI host will see, and well
// below the quadratic merge, which overshoots it twice over.
func TestFacetMergeLinearInChainDepth(t *testing.T) {
	const n = 10000
	st, err := xdm.ParseString(facetedChain(n), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	start := time.Now()
	if _, err := Load(st.Root, "", Options{}); err != nil {
		t.Fatalf("load schema: %v", err)
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Fatalf("loading a %d-link restriction chain took %v, want under 8s", n, d)
	}
}

// TestMergedFacetsNearestWinsAndFixedCarry pins the two properties of the merge
// that the memo has to preserve: a facet set nearer the type wins over the
// base's, and {fixed} travels with the step that set the value, so a facet
// fixed two steps up and not restated between still arrives fixed.
func TestMergedFacetsNearestWinsAndFixedCarry(t *testing.T) {
	s, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="a">
	    <xs:restriction base="xs:string">
	      <xs:maxLength value="3" fixed="true"/>
	      <xs:minLength value="1"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="b">
	    <xs:restriction base="a">
	      <xs:minLength value="2"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="c">
	    <xs:restriction base="b"/>
	  </xs:simpleType>
	</xs:schema>`)
	if err != nil {
		t.Fatalf("schema should load, got: %v", err)
	}
	c := s.Types[xdm.QName{Local: "c"}].(*SimpleType)
	p := &parser{}
	// Ask for the base first so c's answer is built on a memoised entry.
	p.mergedFacets(c.Base.(*SimpleType))
	m := p.mergedFacets(c)
	if m.MinLength == nil || *m.MinLength != 2 {
		t.Errorf("minLength: nearest step wins, got %v", m.MinLength)
	}
	if m.MaxLength == nil || *m.MaxLength != 3 {
		t.Errorf("maxLength: inherited, got %v", m.MaxLength)
	}
	if !m.isFixed(FacetMaxLength) {
		t.Error("maxLength: fixed two steps up, lost its {fixed}")
	}
	if m.isFixed(FacetMinLength) {
		t.Error("minLength: never fixed, reported fixed")
	}
	if m2 := p.mergedFacets(c); m2 != m {
		t.Error("second ask was not the memoised set")
	}

	// The same shapes as load-time rejections: a derived minLength against
	// an inherited maxLength, and a value that contradicts a fixed facet set
	// two steps up.
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="a">
	    <xs:restriction base="xs:string"><xs:maxLength value="3"/></xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="b">
	    <xs:restriction base="a"/>
	  </xs:simpleType>
	  <xs:simpleType name="c">
	    <xs:restriction base="b"><xs:minLength value="5"/></xs:restriction>
	  </xs:simpleType>
	</xs:schema>`, "minLength-less-than-equal-to-maxLength")
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="a">
	    <xs:restriction base="xs:string"><xs:maxLength value="3" fixed="true"/></xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="b">
	    <xs:restriction base="a"><xs:minLength value="2"/></xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="c">
	    <xs:restriction base="b"><xs:maxLength value="2"/></xs:restriction>
	  </xs:simpleType>
	</xs:schema>`, "fixed-facet-value")
}
