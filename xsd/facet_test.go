package xsd

import (
	"math/big"
	"testing"
	"time"
)

func TestWhiteSpaceNormalize(t *testing.T) {
	cases := []struct {
		mode WhiteSpace
		in   string
		want string
	}{
		{WhitePreserve, " a\tb\n", " a\tb\n"},
		{WhiteReplace, " a\tb\n", " a b "},
		{WhiteCollapse, " a\tb\n", "a b"},
		{WhiteCollapse, "   ", ""},
		{WhiteCollapse, "a", "a"},
		{WhiteCollapse, "  a  b  c  ", "a b c"},

		// U+00A0 is a space to unicode.IsSpace but not to XML. Collapsing
		// it would accept values the spec rejects, so it must survive.
		{WhiteCollapse, "a b", "a b"},
	}
	for _, c := range cases {
		if got := c.mode.Normalize(c.in); got != c.want {
			t.Errorf("%s.Normalize(%q) = %q, want %q", c.mode, c.in, got, c.want)
		}
	}
}

func TestCountDigits(t *testing.T) {
	cases := []struct {
		lexical   string
		wantTotal uint64
		wantFrac  uint64
	}{
		{"0", 1, 0},
		{"1", 1, 0},
		{"123", 3, 0},
		{"-123", 3, 0},
		{"1.5", 2, 1},

		// Trailing zeros in the fraction are not significant: the value
		// 1.50 is 3/2, which has two total digits and one fraction digit.
		// Counting the literal would give three and two.
		{"1.50", 2, 1},

		// Leading zeros of a fraction do count toward totalDigits.
		{"0.001", 3, 3},
	}
	for _, c := range cases {
		v, ok := new(big.Rat).SetString(c.lexical)
		if !ok {
			t.Fatalf("bad test input %q", c.lexical)
		}
		total, frac := countDigits(v)
		if total != c.wantTotal || frac != c.wantFrac {
			t.Errorf("countDigits(%s) = (%d, %d), want (%d, %d)",
				c.lexical, total, frac, c.wantTotal, c.wantFrac)
		}
	}
}

func TestFacetApplicable(t *testing.T) {
	prim := func(name string) *SimpleType {
		s := &SimpleType{Variety: VarietyAtomic}
		s.Name.Local = name
		s.Primitive = s
		return s
	}

	str := prim("string")
	if !FacetApplicable(str, FacetMaxLength) {
		t.Error("xs:string should admit maxLength")
	}
	if FacetApplicable(str, FacetTotalDigits) {
		t.Error("xs:string should not admit totalDigits")
	}

	// xs:boolean admits pattern and whiteSpace but not enumeration. This is
	// the irregular cell in the table, so it gets its own assertion.
	b := prim("boolean")
	if !FacetApplicable(b, FacetPattern) {
		t.Error("xs:boolean should admit pattern")
	}
	if FacetApplicable(b, FacetEnumeration) {
		t.Error("xs:boolean should not admit enumeration")
	}

	dec := prim("decimal")
	if !FacetApplicable(dec, FacetFractionDigits) {
		t.Error("xs:decimal should admit fractionDigits")
	}
	if FacetApplicable(dec, FacetMaxLength) {
		t.Error("xs:decimal should not admit maxLength")
	}

	// A union admits only pattern and enumeration, whatever its members
	// admit — in particular it has no whiteSpace, because normalisation
	// belongs to the member type that validates the value.
	u := &SimpleType{Variety: VarietyUnion, MemberTypes: []*SimpleType{str, dec}}
	if !FacetApplicable(u, FacetPattern) || !FacetApplicable(u, FacetEnumeration) {
		t.Error("a union should admit pattern and enumeration")
	}
	if FacetApplicable(u, FacetWhiteSpace) {
		t.Error("a union should not admit whiteSpace")
	}
	if FacetApplicable(u, FacetMaxLength) {
		t.Error("a union should not admit maxLength even when a member does")
	}

	lst := &SimpleType{Variety: VarietyList, ItemType: dec}
	if !FacetApplicable(lst, FacetMaxLength) {
		t.Error("a list should admit maxLength")
	}
	if FacetApplicable(lst, FacetMaxInclusive) {
		t.Error("a list should not admit maxInclusive")
	}
}

// TestEffectiveWhiteSpaceTerminatesOnSelfLoop guards the xs:anyType self-loop.
//
// The spec makes xs:anyType its own base type definition. A walk up the base
// chain that tests for nil rather than for self would not terminate.
func TestEffectiveWhiteSpaceTerminatesOnSelfLoop(t *testing.T) {
	self := &SimpleType{Variety: VarietyAtomic}
	self.Base = self

	done := make(chan WhiteSpace, 1)
	go func() { done <- EffectiveWhiteSpace(self) }()
	select {
	case got := <-done:
		if got != WhitePreserve {
			t.Errorf("got %s, want preserve", got)
		}
	case <-timeoutAfterSecond():
		t.Fatal("EffectiveWhiteSpace did not terminate on a self-referential base")
	}
}

// TestFacetChainTerminatesOnCycle guards the same hazard in facetChain, which
// walks the chain to collect derivation steps.
func TestFacetChainTerminatesOnCycle(t *testing.T) {
	a := &SimpleType{Variety: VarietyAtomic, Facets: &FacetSet{}}
	b := &SimpleType{Variety: VarietyAtomic, Facets: &FacetSet{}}
	a.Base = b
	b.Base = a

	done := make(chan int, 1)
	go func() { done <- len(facetChain(a)) }()
	select {
	case n := <-done:
		if n != 2 {
			t.Errorf("got %d steps, want 2", n)
		}
	case <-timeoutAfterSecond():
		t.Fatal("facetChain did not terminate on a cyclic base chain")
	}
}

func TestEffectiveWhiteSpaceInherits(t *testing.T) {
	collapse := WhiteCollapse
	base := &SimpleType{Variety: VarietyAtomic, Facets: &FacetSet{WhiteSpace: &collapse}}
	derived := &SimpleType{Variety: VarietyAtomic, Base: base, Facets: &FacetSet{}}

	if got := EffectiveWhiteSpace(derived); got != WhiteCollapse {
		t.Errorf("got %s, want collapse inherited from the base", got)
	}
}

func TestDerivationSetString(t *testing.T) {
	var s DerivationSet
	if s.String() != "" {
		t.Errorf("empty set rendered %q", s.String())
	}
	if All.String() != "#all" {
		t.Errorf("All rendered %q, want #all", All.String())
	}
	s = s.With(DerivationExtension).With(DerivationRestriction)
	if got := s.String(); got != "extension restriction" {
		t.Errorf("got %q", got)
	}
	if !s.Has(DerivationExtension) || s.Has(DerivationList) {
		t.Error("Has disagrees with With")
	}
}

func TestWildcardAllows(t *testing.T) {
	any := &Wildcard{Kind: NSAny}
	if !any.Allows("urn:x") || !any.Allows("") {
		t.Error("##any should allow every namespace including the absent one")
	}

	// ##other excludes the absent namespace as well as the named one.
	// Clause 2.3 of Wildcard allows Namespace Name says so explicitly, and
	// authors near-universally expect the opposite. The parser sets
	// ExcludesAbsent for ##other and leaves it clear for XSD 1.1's
	// notNamespace, which excludes only what it lists.
	other := &Wildcard{Kind: NSNot, Namespace: []string{"urn:mine"}, ExcludesAbsent: true}
	if other.Allows("urn:mine") {
		t.Error("##other should exclude its own namespace")
	}
	if other.Allows("") {
		t.Error("##other should exclude unqualified names (clause 2.3)")
	}
	if !other.Allows("urn:other") {
		t.Error("##other should allow a different namespace")
	}

	enum := &Wildcard{Kind: NSEnumerated, Namespace: []string{"urn:a", ""}}
	if !enum.Allows("urn:a") || !enum.Allows("") {
		t.Error("an enumerated constraint should allow its members, including ##local")
	}
	if enum.Allows("urn:b") {
		t.Error("an enumerated constraint should exclude non-members")
	}

	// XSD 1.1's notNamespace excludes only the namespaces it lists, so an
	// unqualified name is permitted unless ##local appears. Applying
	// ##other's rule here rejects every unqualified attribute such a
	// wildcard was written to admit.
	notNS := &Wildcard{Kind: NSNot, Namespace: []string{"urn:a"}}
	if !notNS.Allows("") {
		t.Error("notNamespace without ##local should allow unqualified names")
	}
	if notNS.Allows("urn:a") {
		t.Error("notNamespace should exclude what it lists")
	}
}

// timeoutAfterSecond returns a channel that fires after a second, used by the
// termination tests above.
func timeoutAfterSecond() <-chan time.Time { return time.After(time.Second) }
