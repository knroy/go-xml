package xsd

import (
	"fmt"
	"math/big"
	"strings"
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

		// The leading-zero rule applies only when the integer part is
		// zero; once there is an integer digit the coefficient is
		// already at least as long as the scale.
		{"1.001", 4, 3},
		{"100.001", 6, 3},

		// Trailing zeros, on either side of the point.
		{"0.0", 1, 0},
		{"10.0", 2, 0},
		{"1000", 4, 0},
		{"100.0", 3, 0},
		{"1.500", 2, 1},
		{"0.10", 1, 1},
		{"-0.001", 3, 3},
	}
	for _, c := range cases {
		v, ok := new(big.Rat).SetString(c.lexical)
		if !ok {
			t.Fatalf("bad test input %q", c.lexical)
		}
		total, frac, ok := countDigits(v)
		if !ok {
			t.Errorf("countDigits(%s) reported no terminating expansion", c.lexical)
			continue
		}
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
	if AllDerivations.String() != "#all" {
		t.Errorf("AllDerivations rendered %q, want #all", AllDerivations.String())
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

// "length and minLength or maxLength" (Part 2 §4.3.1.4) is not the flat
// prohibition it is easy to read it as. minLength alongside length is an error
// *unless* the minLength does not exceed the length and some type further up
// the chain states that same minLength without stating a length.
//
// The escape clause is not an edge case: every built-in list type carries
// minLength="1", so reading the constraint flatly rejects every restriction of
// xs:IDREFS or xs:NMTOKENS that sets a length. IDREFS_length006 is marked valid
// in the suite against W3C bug 6446, noting "WG decided spec. has a special
// case which allows this".
func TestLengthWithMinLengthEscapeClause(t *testing.T) {
	// xs:IDREFS supplies minLength="1" and no length, so length="5" here
	// satisfies both halves of the clause.
	if _, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="t">
	    <xs:restriction base="xs:IDREFS">
	      <xs:length value="5"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`); err != nil {
		t.Errorf("a length over a list type's inherited minLength is legal: %v", err)
	}

	// Both written on one step, with nothing above supplying the minLength:
	// no witness for clause 1.2, so this is the error the constraint is for.
	if _, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="t">
	    <xs:restriction base="xs:string">
	      <xs:length value="5"/>
	      <xs:minLength value="1"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`); err == nil {
		t.Error("length beside a minLength no ancestor supplies should be rejected")
	}

	// The witness exists but the values contradict: minLength 7 exceeds
	// length 5, so clause 1.1 fails however clause 1.2 comes out.
	if _, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="base">
	    <xs:restriction base="xs:string"><xs:minLength value="7"/></xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="t">
	    <xs:restriction base="base"><xs:length value="5"/></xs:restriction>
	  </xs:simpleType>
	</xs:schema>`); err == nil {
		t.Error("a minLength greater than the length should be rejected")
	}
}

// totalDigits is a positiveInteger, unlike the length facets beside it, which
// are nonNegativeIntegers. Part 2 §4.3.11 says so outright, and it follows from
// what the facet means: a value space restricted to numbers expressible in at
// most zero digits is empty, so totalDigits="0" describes no type at all.
//
// The suite writes it against every integer type in turn — int, short, byte,
// long, the unsigned four, and the four integer subtypes — which is thirteen
// schemas turning on one rule.
func TestTotalDigitsMustBePositive(t *testing.T) {
	for _, base := range []string{"xs:int", "xs:decimal", "xs:unsignedByte", "xs:integer"} {
		if _, err := parseSchemaString(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="t">
		    <xs:restriction base="`+base+`">
		      <xs:totalDigits value="0"/>
		    </xs:restriction>
		  </xs:simpleType>
		</xs:schema>`); err == nil {
			t.Errorf("totalDigits=0 on %s should be rejected", base)
		}
	}
	// fractionDigits is a nonNegativeInteger, so zero is legal there — it
	// is how a schema says "no fractional part".
	if _, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="t">
	    <xs:restriction base="xs:decimal">
	      <xs:fractionDigits value="0"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`); err != nil {
		t.Errorf("fractionDigits=0 is legal: %v", err)
	}
}

// "final" on a simple type names the derivations it forbids from itself. It was
// parsed and then never consulted, so a schema could declare
// final="restriction" and restrict the type on the very next line.
//
// Each variety is its own clause, which is why one comparison will not do:
// restricting a type is forbidden by "restriction", using it as a list's item
// type by "list", and naming it in a union by "union". The suite's
// ST_final00101m set walks all three.
func TestSimpleTypeFinalIsEnforced(t *testing.T) {
	head := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="Test" final="%s">
	    <xs:restriction base="xs:string"><xs:pattern value="1|2"/></xs:restriction>
	  </xs:simpleType>`

	derivations := map[string]string{
		"restriction": `<xs:simpleType name="D">
		    <xs:restriction base="Test"><xs:pattern value="1"/></xs:restriction>
		  </xs:simpleType>`,
		"list":  `<xs:simpleType name="D"><xs:list itemType="Test"/></xs:simpleType>`,
		"union": `<xs:simpleType name="D"><xs:union memberTypes="Test xs:int"/></xs:simpleType>`,
	}

	for forbidden, body := range derivations {
		// The derivation the type declares final must be refused.
		src := fmt.Sprintf(head, forbidden) + body + `</xs:schema>`
		if _, err := parseSchemaString(t, src); err == nil {
			t.Errorf("final=%q should forbid the matching derivation", forbidden)
		}
		// And a *different* one must still be allowed, so the check is
		// reading which derivation is named rather than treating any
		// final at all as blocking everything.
		for other, otherBody := range derivations {
			if other == forbidden {
				continue
			}
			src := fmt.Sprintf(head, forbidden) + otherBody + `</xs:schema>`
			if _, err := parseSchemaString(t, src); err != nil {
				t.Errorf("final=%q should not forbid %s: %v",
					forbidden, other, err)
			}
		}
	}
}

// TestCountDigitsUnbounded pins that the fraction-digit count is exact at any
// scale.
//
// countDigits used to expand the value one decimal digit at a time and stop at
// a fixed bound of 4096, returning the truncated count. That is not a
// defensive limit: a value with 4600 fraction digits reported 4096 of them and
// so passed fractionDigits="4500", which it violates. The counts here run well
// past the old bound so that reintroducing any bound fails this test rather
// than merely moving the value that triggers it.
func TestCountDigitsUnbounded(t *testing.T) {
	for _, n := range []int{0, 1, 18, 64, 1024, 4095, 4096, 4097, 8192, 10000, 100000} {
		// 10^-n written out: "0." then n-1 zeros then "1".
		lex := "1"
		if n > 0 {
			lex = "0." + strings.Repeat("0", n-1) + "1"
		}
		v, ok := new(big.Rat).SetString(lex)
		if !ok {
			t.Fatalf("bad test input for n=%d", n)
		}
		total, frac, ok := countDigits(v)
		if !ok {
			t.Fatalf("n=%d: countDigits reported no terminating expansion", n)
		}
		wantTotal, wantFrac := uint64(n), uint64(n)
		if n == 0 {
			wantTotal = 1
		}
		if total != wantTotal || frac != wantFrac {
			t.Errorf("n=%d: countDigits = (%d, %d), want (%d, %d)",
				n, total, frac, wantTotal, wantFrac)
		}
	}
}

// TestCountDigitsLargeInteger pins the total-digit count for values whose size
// is in the integer part rather than the fraction.
func TestCountDigitsLargeInteger(t *testing.T) {
	for _, n := range []int{100, 4097, 20000} {
		lex := "1" + strings.Repeat("0", n)
		v, _ := new(big.Rat).SetString(lex)
		total, frac, ok := countDigits(v)
		if !ok {
			t.Fatalf("n=%d: no terminating expansion", n)
		}
		if total != uint64(n+1) || frac != 0 {
			t.Errorf("n=%d: countDigits = (%d, %d), want (%d, 0)", n, total, frac, n+1)
		}
		// Same magnitude, but with the digits after the point.
		lex = "1." + strings.Repeat("0", n-1) + "1"
		v, _ = new(big.Rat).SetString(lex)
		total, frac, ok = countDigits(v)
		if !ok {
			t.Fatalf("n=%d fraction: no terminating expansion", n)
		}
		if total != uint64(n+1) || frac != uint64(n) {
			t.Errorf("n=%d fraction: countDigits = (%d, %d), want (%d, %d)",
				n, total, frac, n+1, n)
		}
	}
}

// TestCountDigitsNonTerminating pins that a value with no finite decimal
// expansion is reported as such rather than given a truncated count.
//
// No xs:decimal literal produces one — isDecimalLexical admits only sign,
// digits and a single point, so the denominator is always a power of ten — but
// countDigits must not answer with a number if one ever arrives.
func TestCountDigitsNonTerminating(t *testing.T) {
	for _, lex := range []string{"1/3", "2/7", "-1/6"} {
		v, ok := new(big.Rat).SetString(lex)
		if !ok {
			t.Fatalf("bad test input %q", lex)
		}
		if _, _, ok := countDigits(v); ok {
			t.Errorf("countDigits(%s) claimed a terminating expansion", lex)
		}
	}
	// The upstream lexical check is what makes this unreachable in
	// practice; assert it rather than assume it.
	for _, lex := range []string{"1/3", "1e-5000", "0x10"} {
		if isDecimalLexical(lex) {
			t.Errorf("isDecimalLexical(%q) = true, want false", lex)
		}
	}
}

// TestDigitFacetsPastOldBound validates whole documents on either side of a
// fractionDigits and a totalDigits limit set beyond the old 4096 expansion
// bound.
//
// This is the end-to-end form of the false accept: a document with 4600
// fraction digits was reported as having 4096 and so was accepted against
// fractionDigits="4500". Accepting an invalid document is the worst failure a
// validator has, so both facets are pinned here at values the old code could
// not count.
func TestDigitFacetsPastOldBound(t *testing.T) {
	frac := func(n int) string { return "0." + strings.Repeat("0", n-1) + "1" }

	cases := []struct {
		name   string
		facet  string
		limit  int
		value  string
		accept bool
	}{
		{"fraction under limit", "fractionDigits", 4500, frac(4400), true},
		{"fraction at limit", "fractionDigits", 4500, frac(4500), true},
		{"fraction just over limit", "fractionDigits", 4500, frac(4501), false},
		{"fraction far over limit", "fractionDigits", 4500, frac(4600), false},
		{"fraction far past old bound", "fractionDigits", 4500, frac(5000), false},
		{"fraction over a small limit", "fractionDigits", 2, "0.001", false},

		// The exact boundary of the old expansion bound: 4096 fraction
		// digits was counted correctly and rejected, 4097 counted as
		// 4096 and was accepted.
		{"fraction at old bound", "fractionDigits", 4095, frac(4096), false},
		{"fraction one past old bound", "fractionDigits", 4096, frac(4097), false},
		{"total one past old bound", "totalDigits", 4096, frac(4097), false},

		{"total under limit", "totalDigits", 4500, frac(4400), true},
		{"total at limit", "totalDigits", 4500, frac(4500), true},
		{"total just over limit", "totalDigits", 4500, frac(4501), false},
		{"total far over limit", "totalDigits", 4500, frac(6000), false},
		{"total over limit as integer", "totalDigits", 4500,
			"1" + strings.Repeat("0", 4500), false},
		{"total at limit as integer", "totalDigits", 4500,
			"1" + strings.Repeat("0", 4499), true},

		// Purely integral values never entered the old scaling loop and
		// so were always counted correctly. They are the regression
		// guard: the fix must not disturb them at any length.
		{"huge integer under limit", "totalDigits", 20001,
			"1" + strings.Repeat("0", 20000), true},
		{"huge integer over limit", "totalDigits", 20000,
			"1" + strings.Repeat("0", 20000), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			schema := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="v" type="d"/>
  <xs:simpleType name="d">
    <xs:restriction base="xs:decimal">
      <xs:%s value="%d"/>
    </xs:restriction>
  </xs:simpleType>
</xs:schema>`, c.facet, c.limit)
			err := validateString(t, schema, "<v>"+c.value+"</v>")
			if c.accept && err != nil {
				t.Errorf("want accept, got error: %v", err)
			}
			if !c.accept && err == nil {
				t.Error("want reject, got accept")
			}
		})
	}
}
