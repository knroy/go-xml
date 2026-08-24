package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// mustLoadOK asserts that a schema loads, which is where Particle Valid
// (Restriction) runs.
func mustLoadOK(t *testing.T, src string) {
	t.Helper()
	if _, err := parseSchemaString(t, src); err != nil {
		t.Fatalf("schema should load, got: %v", err)
	}
}

// mustLoadFail asserts that a schema is rejected, and that the diagnostic says
// why rather than blaming something unrelated.
func mustLoadFail(t *testing.T, src, want string) {
	t.Helper()
	_, err := parseSchemaString(t, src)
	if err == nil {
		t.Fatal("schema should have been rejected")
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not mention %q", err, want)
	}
}

// A particle written minOccurs=maxOccurs=0 "corresponds to no component at
// all" (§3.9.2 and the parallel mapping rules), so it cannot make a
// restriction invalid. Particle Correct clause 2.2 is the confirmation: every
// particle that exists has {max occurs} >= 1.
//
// Pins particlesJq010: e1 is not in the namespace the base wildcard admits,
// but at 0..0 there is no particle to compare.
func TestRestrictionIgnoresZeroMaxOccursElement(t *testing.T) {
	mustLoadOK(t, `
	<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"
	            targetNamespace="http://xsdtesting" xmlns:x="http://xsdtesting">
	  <xsd:complexType name="B">
	    <xsd:sequence>
	      <xsd:any namespace="##targetNamespace" minOccurs="0"/>
	    </xsd:sequence>
	  </xsd:complexType>
	  <xsd:complexType name="R">
	    <xsd:complexContent>
	      <xsd:restriction base="x:B">
	        <xsd:sequence>
	          <xsd:element name="e1" minOccurs="0" maxOccurs="0"/>
	        </xsd:sequence>
	      </xsd:restriction>
	    </xsd:complexContent>
	  </xsd:complexType>
	</xsd:schema>`)
}

// The same rule inside a choice: dropping an alternative to 0..0 removes it,
// leaving a choice the base's order-preserving mapping still covers.
//
// Pins mgH014.
func TestRestrictionIgnoresZeroMaxOccursChoiceBranch(t *testing.T) {
	mustLoadOK(t, `
	<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
	  <xsd:complexType name="bar">
	    <xsd:choice>
	      <xsd:element name="e1"/>
	      <xsd:element name="e2"/>
	    </xsd:choice>
	  </xsd:complexType>
	  <xsd:complexType name="foo">
	    <xsd:complexContent>
	      <xsd:restriction base="bar">
	        <xsd:choice>
	          <xsd:element name="e1" minOccurs="0" maxOccurs="0"/>
	          <xsd:element name="e2"/>
	        </xsd:choice>
	      </xsd:restriction>
	    </xsd:complexContent>
	  </xsd:complexType>
	</xsd:schema>`)
}

// A 0..0 particle is gone for every purpose, not only for restriction: a whole
// group at 0..0 leaves the type with no content model at all.
//
// Pins particlesW006, whose base and derived groups are both 0..0. The W3C has
// queried the expected result of that test (bugzilla 4952), so it is the
// mapping rule rather than the test that this pins.
func TestZeroMaxOccursGroupLeavesEmptyContent(t *testing.T) {
	mustLoadOK(t, `
	<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"
	            targetNamespace="http://xsdtesting" xmlns:x="http://xsdtesting">
	  <xsd:complexType name="B">
	    <xsd:sequence minOccurs="0" maxOccurs="0">
	      <xsd:element name="e1" maxOccurs="3"/>
	      <xsd:element name="e2" maxOccurs="3"/>
	    </xsd:sequence>
	  </xsd:complexType>
	  <xsd:complexType name="R">
	    <xsd:complexContent>
	      <xsd:restriction base="x:B">
	        <xsd:sequence minOccurs="0" maxOccurs="0">
	          <xsd:element name="e1" minOccurs="4" maxOccurs="5"/>
	          <xsd:element name="e2" minOccurs="3" maxOccurs="3"/>
	        </xsd:sequence>
	      </xsd:restriction>
	    </xsd:complexContent>
	  </xsd:complexType>
	</xsd:schema>`)
}

// NSRecurseCheckCardinality clause 1 asks each member to restrict "the
// wildcard" — the term, which has no occurrence range. All the counting is
// clause 2's, over the group's effective total range.
//
// Pins particlesHa070: three unit-occurrence elements under a 3..3 wildcard.
// Each member "fails" a 1-vs-3 range comparison, yet the total is exactly 3.
func TestGroupRestrictingWildcardCountsTotalNotMembers(t *testing.T) {
	mustLoadOK(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:sequence>
	      <xs:any namespace="##any" minOccurs="3" maxOccurs="3"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:complexType name="derived">
	    <xs:complexContent>
	      <xs:restriction base="base">
	        <xs:all>
	          <xs:element name="e1" type="xs:string"/>
	          <xs:element name="e2" type="xs:string"/>
	          <xs:element name="e3" type="xs:string"/>
	        </xs:all>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	</xs:schema>`)
}

// The other half of the same rule, which is what stops the fix above from
// being a blanket "accept any group under a wildcard": the effective total
// range must still fit. Two elements cannot satisfy a 3..3 wildcard.
//
// Pins particlesHa071, the invalid twin of Ha070.
func TestGroupRestrictingWildcardRejectsShortTotal(t *testing.T) {
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:sequence>
	      <xs:any namespace="##any" minOccurs="3" maxOccurs="3"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:complexType name="derived">
	    <xs:complexContent>
	      <xs:restriction base="base">
	        <xs:all>
	          <xs:element name="e1" type="xs:string"/>
	          <xs:element name="e2" type="xs:string"/>
	        </xs:all>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	</xs:schema>`, "total range")
}

// A group whose total overshoots the wildcard is rejected too.
//
// Pins particlesHa081: three members under a 2..2 wildcard.
func TestGroupRestrictingWildcardRejectsLongTotal(t *testing.T) {
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:sequence>
	      <xs:any namespace="##any" minOccurs="2" maxOccurs="2"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:complexType name="derived">
	    <xs:complexContent>
	      <xs:restriction base="base">
	        <xs:sequence>
	          <xs:element name="e" type="xs:string"/>
	          <xs:element name="e" type="xs:string"/>
	          <xs:any namespace="##targetNamespace"/>
	        </xs:sequence>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	</xs:schema>`, "total range")
}

// Clause 1 still has work to do: it is the namespace test. A member the base
// wildcard does not admit is rejected however well the totals line up.
func TestGroupRestrictingWildcardStillChecksNamespace(t *testing.T) {
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           targetNamespace="urn:t" xmlns:t="urn:t">
	  <xs:complexType name="base">
	    <xs:sequence>
	      <xs:any namespace="##other" minOccurs="1" maxOccurs="1"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:complexType name="derived">
	    <xs:complexContent>
	      <xs:restriction base="t:base">
	        <xs:sequence>
	          <xs:element name="e" type="xs:string"/>
	        </xs:sequence>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	</xs:schema>`, "namespace")
}

// A nested group under a wildcard: the total is the product, so a 1..2
// sequence of two 2..2 elements contributes 4..8 and fits a 4..8 wildcard.
//
// Pins particlesQ013, which reached the Recurse path and complained that the
// restriction omitted the base's wildcard.
func TestNestedGroupRestrictingWildcardUsesProduct(t *testing.T) {
	mustLoadOK(t, `
	<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"
	            targetNamespace="http://xsdtesting" xmlns:x="http://xsdtesting">
	  <xsd:complexType name="B">
	    <xsd:sequence>
	      <xsd:element name="foo" minOccurs="1" maxOccurs="1"/>
	      <xsd:any namespace="##any" minOccurs="4" maxOccurs="8"/>
	    </xsd:sequence>
	  </xsd:complexType>
	  <xsd:complexType name="R">
	    <xsd:complexContent>
	      <xsd:restriction base="x:B">
	        <xsd:sequence>
	          <xsd:element name="foo" minOccurs="1" maxOccurs="1"/>
	          <xsd:sequence minOccurs="1" maxOccurs="2">
	            <xsd:element name="e1" minOccurs="2" maxOccurs="2"/>
	            <xsd:element name="e2" minOccurs="2" maxOccurs="2"/>
	          </xsd:sequence>
	        </xsd:sequence>
	      </xsd:restriction>
	    </xsd:complexContent>
	  </xsd:complexType>
	</xsd:schema>`)
}

// A repeating choice under a wildcard: Effective Total Range (choice) takes
// the minimum of the alternatives' minima, so a 3..4 choice of two
// unit-occurrence elements contributes 3..4 and fits a 3..8 wildcard.
//
// Pins particlesR013.
func TestRepeatingChoiceRestrictingWildcard(t *testing.T) {
	mustLoadOK(t, `
	<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"
	            targetNamespace="http://xsdtesting" xmlns:x="http://xsdtesting">
	  <xsd:complexType name="B">
	    <xsd:sequence>
	      <xsd:element name="foo" minOccurs="1" maxOccurs="1"/>
	      <xsd:any namespace="##any" minOccurs="3" maxOccurs="8"/>
	    </xsd:sequence>
	  </xsd:complexType>
	  <xsd:complexType name="R">
	    <xsd:complexContent>
	      <xsd:restriction base="x:B">
	        <xsd:sequence>
	          <xsd:element name="foo" minOccurs="1" maxOccurs="1"/>
	          <xsd:choice minOccurs="3" maxOccurs="4">
	            <xsd:element name="e1" minOccurs="1" maxOccurs="1"/>
	            <xsd:element name="e2" minOccurs="1" maxOccurs="1"/>
	          </xsd:choice>
	        </xsd:sequence>
	      </xsd:restriction>
	    </xsd:complexContent>
	  </xsd:complexType>
	</xsd:schema>`)
}

// Clause 2.1 expands a substitution group head into a choice over its members,
// and RecurseLax then maps the derived choice onto it with an *order-preserving*
// mapping. That makes the order of {substitution group} load-bearing, so it
// must not depend on Go's map iteration order.
//
// Pins elemZ027a, which passed or failed from run to run before the closure
// walk in linkSubstitutionGroups was made deterministic. Loading repeatedly is
// what makes the test meaningful: a single load used to succeed by luck.
func TestSubstitutionGroupExpansionIsOrderStable(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="head"/>
	  <xs:element name="m1" substitutionGroup="head"/>
	  <xs:element name="m2" substitutionGroup="head"/>
	  <xs:complexType name="base">
	    <xs:sequence><xs:element ref="head"/></xs:sequence>
	  </xs:complexType>
	  <xs:complexType name="derived">
	    <xs:complexContent>
	      <xs:restriction base="base">
	        <xs:sequence>
	          <xs:choice>
	            <xs:element ref="m1"/>
	            <xs:element ref="m2"/>
	          </xs:choice>
	        </xs:sequence>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	</xs:schema>`
	for i := 0; i < 32; i++ {
		if _, err := parseSchemaString(t, src); err != nil {
			t.Fatalf("load %d rejected a valid substitution restriction: %v", i, err)
		}
	}
}

// The member order the closure produces must itself be stable, since callers
// beyond restriction — the content-model automaton and UPA among them — walk
// it too.
func TestSubstitutionGroupMemberOrderIsStable(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="head"/>
	  <xs:element name="d" substitutionGroup="head"/>
	  <xs:element name="a" substitutionGroup="head"/>
	  <xs:element name="c" substitutionGroup="head"/>
	  <xs:element name="b" substitutionGroup="head"/>
	</xs:schema>`
	var first []string
	for i := 0; i < 32; i++ {
		s := mustParseSchema(t, src)
		var got []string
		for _, m := range s.Elements[xdm.QName{Local: "head"}].substitutable {
			got = append(got, m.Name.Local)
		}
		if first == nil {
			first = got
			continue
		}
		if strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("member order changed between loads: %v then %v", first, got)
		}
	}
}

// TestAllGroupBudgetsSubstitutionGroup covers clause 2.1 as it reaches the
// all-group case: a base particle naming a substitution group head stands for a
// choice over the whole group, so every member draws on that head's occurrence
// allowance — and on the *same* allowance, which makes their occurrences sum.
//
// Keyed by exact name, the members matched no budget at all and four valid
// schemas were rejected as elements the base "does not allow" (all221, all222,
// all225, all226).
func TestAllGroupBudgetsSubstitutionGroup(t *testing.T) {
	// These derivations are XSD 1.1: 1.0 does not let an all group take
	// part in a restriction this way at all.
	load11 := func(src string) error {
		t.Helper()
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing the test schema as XML: %v", err)
		}
		_, err = Load(tree.Root, "s.xsd", Options{Version: Version11})
		return err
	}
	ok11 := func(src string) {
		t.Helper()
		if err := load11(src); err != nil {
			t.Fatalf("schema should load, got: %v", err)
		}
	}
	fail11 := func(src, want string) {
		t.Helper()
		err := load11(src)
		if err == nil {
			t.Fatal("schema should have been rejected")
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	// A1 6..8 and A2 6..8 sum to 12..16, inside the base's 10..20.
	ok11(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="b">
	    <xs:all>
	      <xs:element ref="a" minOccurs="10" maxOccurs="20"/>
	      <xs:element name="pad" minOccurs="0" maxOccurs="1"/>
	    </xs:all>
	  </xs:complexType>
	  <xs:complexType name="r">
	    <xs:complexContent>
	      <xs:restriction base="b">
	        <xs:all>
	          <xs:element ref="A1" minOccurs="6" maxOccurs="8"/>
	          <xs:element ref="A2" minOccurs="6" maxOccurs="8"/>
	        </xs:all>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="a"/>
	  <xs:element name="A1" substitutionGroup="a"/>
	  <xs:element name="A2" substitutionGroup="a"/>
	</xs:schema>`)

	// The sum is what is bounded, so a pair exceeding the base's maximum
	// together is still an error even though each fits alone.
	fail11(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="b">
	    <xs:all>
	      <xs:element ref="a" minOccurs="0" maxOccurs="10"/>
	      <xs:element name="pad" minOccurs="0" maxOccurs="1"/>
	    </xs:all>
	  </xs:complexType>
	  <xs:complexType name="r">
	    <xs:complexContent>
	      <xs:restriction base="b">
	        <xs:all>
	          <xs:element ref="A1" minOccurs="0" maxOccurs="8"/>
	          <xs:element ref="A2" minOccurs="0" maxOccurs="8"/>
	        </xs:all>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="a"/>
	  <xs:element name="A1" substitutionGroup="a"/>
	  <xs:element name="A2" substitutionGroup="a"/>
	</xs:schema>`, "is not a valid restriction")

	// A name in no substitution group of the base is still refused, which
	// is the rule the budget exists to enforce.
	fail11(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="b">
	    <xs:all>
	      <xs:element ref="a" minOccurs="0" maxOccurs="10"/>
	      <xs:element name="pad" minOccurs="0" maxOccurs="1"/>
	    </xs:all>
	  </xs:complexType>
	  <xs:complexType name="r">
	    <xs:complexContent>
	      <xs:restriction base="b">
	        <xs:all>
	          <xs:element name="z" minOccurs="0" maxOccurs="1"/>
	        </xs:all>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="a"/>
	</xs:schema>`, "does not allow it")

	// The head's minimum is met by its members collectively; requiring each
	// member to meet it on its own would be a stricter, different rule.
	ok11(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="b">
	    <xs:all>
	      <xs:element ref="a" minOccurs="4" maxOccurs="10"/>
	      <xs:element name="pad" minOccurs="0" maxOccurs="1"/>
	    </xs:all>
	  </xs:complexType>
	  <xs:complexType name="r">
	    <xs:complexContent>
	      <xs:restriction base="b">
	        <xs:all>
	          <xs:element ref="A1" minOccurs="2" maxOccurs="5"/>
	          <xs:element ref="A2" minOccurs="2" maxOccurs="5"/>
	        </xs:all>
	      </xs:restriction>
	    </xs:complexContent>
	  </xs:complexType>
	  <xs:element name="a"/>
	  <xs:element name="A1" substitutionGroup="a"/>
	  <xs:element name="A2" substitutionGroup="a"/>
	</xs:schema>`)
}

// TestAnonymousRestrictionIsChecked pins the loop in checkParticleRestriction
// walking allComplexTypes rather than Types.
//
// Only named types live in Types, and the suite writes most of its restriction
// tests as an inline <xs:complexType> inside an element declaration — so every
// one of them was loading without the constraint ever running. particlesHb001
// is the shape: a wildcard restricting a named element, which the table
// (§3.9.6) has no cell for at all.
func TestAnonymousRestrictionIsChecked(t *testing.T) {
	load := func(src string) error {
		t.Helper()
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing the test schema as XML: %v", err)
		}
		_, err = Load(tree.Root, "s.xsd", Options{})
		return err
	}

	// A wildcard may not restrict an element declaration: Forbidden.
	if err := load(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:choice><xs:element name="e1" minOccurs="2" maxOccurs="10"/></xs:choice>
	  </xs:complexType>
	  <xs:element name="doc">
	    <xs:complexType><xs:complexContent>
	      <xs:restriction base="base">
	        <xs:choice><xs:any minOccurs="3" maxOccurs="9"/></xs:choice>
	      </xs:restriction>
	    </xs:complexContent></xs:complexType>
	  </xs:element>
	</xs:schema>`); err == nil {
		t.Fatal("a wildcard restricting an element should be rejected even in an anonymous type")
	}

	// The same restriction written as a named type was always caught; it
	// must stay caught.
	if err := load(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:choice><xs:element name="e1"/></xs:choice>
	  </xs:complexType>
	  <xs:complexType name="r"><xs:complexContent>
	    <xs:restriction base="base"><xs:choice><xs:any/></xs:choice></xs:restriction>
	  </xs:complexContent></xs:complexType>
	</xs:schema>`); err == nil {
		t.Fatal("the named form should still be rejected")
	}
}

// TestPointlessGroupInlinedIntoMembers pins inlineSameCompositor.
//
// Clause 2.2 calls a same-compositor group with unit occurrence pointless, and
// stripPointless only unwraps the particle being compared — never a wrapper
// sitting among a group's members. groupB003 is the case that exposed it: both
// sides reference the same <xs:group>, but R's single-member sequence unwraps
// to the group's own <sequence> while B keeps it nested, so the walk compared
// R's elements against B's group and reported the base "requires" a particle
// the restriction had in fact kept.
func TestPointlessGroupInlinedIntoMembers(t *testing.T) {
	tree, err := xdm.ParseString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="base">
	    <xs:sequence>
	      <xs:group ref="g1"/>
	      <xs:group ref="g2" minOccurs="0"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:element name="elem">
	    <xs:complexType><xs:complexContent>
	      <xs:restriction base="base"><xs:sequence><xs:group ref="g1"/></xs:sequence></xs:restriction>
	    </xs:complexContent></xs:complexType>
	  </xs:element>
	  <xs:group name="g1"><xs:sequence><xs:element name="r1"/><xs:element name="r2"/></xs:sequence></xs:group>
	  <xs:group name="g2"><xs:sequence><xs:element name="r3"/><xs:element name="r4"/></xs:sequence></xs:group>
	</xs:schema>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test schema as XML: %v", err)
	}
	if _, err := Load(tree.Root, "s.xsd", Options{}); err != nil {
		t.Fatalf("dropping an optional trailing group is a valid restriction, got: %v", err)
	}
}

// TestBlockSupersetOnRestriction pins NameAndTypeOK clause 3.2.4 and the
// bug-4144 reading of #all that goes with it.
//
// R's {disallowed substitutions} must be a superset of B's, or the derived
// element admits substitutes the base refuses. The subtlety is that block=
// names only three derivations, so #all and "substitution extension
// restriction" denote the same set — W3C bug 4144, whose test particlesIg004
// the working group ruled *valid*. Comparing the stored masks directly would
// reject it, because All is stored wide enough to cover list and union too.
func TestBlockSupersetOnRestriction(t *testing.T) {
	load := func(src string) error {
		t.Helper()
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing the test schema as XML: %v", err)
		}
		_, err = Load(tree.Root, "s.xsd", Options{})
		return err
	}
	schema := func(baseBlock, derivedBlock string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:complexType name="base">
		    <xs:choice><xs:element name="e1" block="` + baseBlock + `"/></xs:choice>
		  </xs:complexType>
		  <xs:complexType name="r"><xs:complexContent>
		    <xs:restriction base="base">
		      <xs:choice><xs:element name="e1" block="` + derivedBlock + `"/></xs:choice>
		    </xs:restriction>
		  </xs:complexContent></xs:complexType>
		</xs:schema>`
	}

	// Dropping a member the base blocks widens the derivation: invalid.
	// This is particlesIg006, which omits "restriction".
	if err := load(schema("#all", "substitution extension")); err == nil {
		t.Fatal("a restriction that blocks less than its base should be rejected")
	}

	// #all against the three members it stands for is the same set, not a
	// wider one. particlesIg004: the suite expects this schema to load.
	if err := load(schema("#all", "substitution extension restriction")); err != nil {
		t.Fatalf("#all and the full explicit list denote the same set, got: %v", err)
	}

	// Blocking more than the base is always fine. particlesIg005.
	if err := load(schema("substitution extension restriction", "#all")); err != nil {
		t.Fatalf("blocking more than the base is a narrowing, got: %v", err)
	}
}

// derivation-ok-restriction clause 2.1.2 (§3.4.6): where a restriction
// redeclares an attribute the base declares, the type it gives must be a
// restriction of the type the base gave.
//
// parse_decl.go checks the rest of clause 2 but leaves this sub-clause to the
// type-derivation machinery here, so nothing enforced it and a restriction
// could widen an attribute's type freely. particlesZ013 is the shape: att1
// goes from xs:integer to a union that admits floats, booleans and strings.
func TestAttributeTypeMustRestrictBase(t *testing.T) {
	mustLoadFail(t, `
	<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
	  <xsd:simpleType name="wide">
	    <xsd:union memberTypes="xsd:float xsd:integer xsd:boolean"/>
	  </xsd:simpleType>
	  <xsd:complexType name="CT1">
	    <xsd:attribute name="att1" type="xsd:integer"/>
	  </xsd:complexType>
	  <xsd:complexType name="CT2">
	    <xsd:complexContent>
	      <xsd:restriction base="CT1">
	        <xsd:attribute name="att1" type="wide"/>
	      </xsd:restriction>
	    </xsd:complexContent>
	  </xsd:complexType>
	</xsd:schema>`, "derivation-ok-restriction.2.1.2")
}

// The same clause must not fire on a genuine narrowing: xs:int derives from
// xs:integer, so redeclaring the attribute with it is exactly what a
// restriction is for. A rule written as type *equality* would reject this.
func TestAttributeTypeNarrowingAllowed(t *testing.T) {
	mustLoadOK(t, `
	<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
	  <xsd:complexType name="CT1">
	    <xsd:attribute name="att1" type="xsd:integer"/>
	  </xsd:complexType>
	  <xsd:complexType name="CT2">
	    <xsd:complexContent>
	      <xsd:restriction base="CT1">
	        <xsd:attribute name="att1" type="xsd:int"/>
	      </xsd:restriction>
	    </xsd:complexContent>
	  </xsd:complexType>
	</xsd:schema>`)
}

// Wildcard Subset (§3.10.6) clause 2 under XSD 1.1, where notNamespace names a
// *set*: not-S1 is a subset of not-S2 exactly when S2 is a subset of S1. The
// larger exclusion set is the smaller wildcard, so narrowing means excluding
// more, and restricting not-{cain, abel, adam} to not-{cain, abel, adam, eve}
// is legal while widening it to not-{adam} is not.
//
// Reading the clause as set equality — correct for 1.0, where a negation names
// one namespace — refuses every genuine 1.1 narrowing.
func TestElementWildcardNotNamespaceSubset(t *testing.T) {
	schema := func(baseNot, derivedNot string) string {
		return `
		<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
		  <xsd:complexType name="B">
		    <xsd:sequence>
		      <xsd:any notNamespace="` + baseNot + `" processContents="lax"/>
		    </xsd:sequence>
		  </xsd:complexType>
		  <xsd:complexType name="R">
		    <xsd:complexContent>
		      <xsd:restriction base="B">
		        <xsd:sequence>
		          <xsd:any notNamespace="` + derivedNot + `" processContents="lax"/>
		        </xsd:sequence>
		      </xsd:restriction>
		    </xsd:complexContent>
		  </xsd:complexType>
		</xsd:schema>`
	}
	t.Run("excluding more is a narrowing", func(t *testing.T) {
		src := schema("http://cain.com/ http://abel.com/",
			"http://cain.com/ http://abel.com/ http://eve.com/")
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		if _, err := Load(tree.Root, "s.xsd", Options{Version: Version11}); err != nil {
			t.Fatalf("excluding more namespaces must be a valid restriction: %v", err)
		}
	})
	t.Run("excluding fewer is a widening", func(t *testing.T) {
		src := schema("http://cain.com/ http://abel.com/ http://adam.com/",
			"http://adam.com/")
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		if _, err := Load(tree.Root, "s.xsd", Options{Version: Version11}); err == nil {
			t.Fatal("excluding fewer namespaces admits more, and must be rejected")
		}
	})
}
