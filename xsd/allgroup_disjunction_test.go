package xsd

import (
	"strings"
	"testing"
)

// An <all> group with minOccurs="0" is a DISJUNCTION — either the group is
// skipped entirely, or it matches once with every member meeting its own floor
// — and not a per-name occurrence range with the floors scaled to zero.
//
// Both readings agree on a group whose members are all optional. They part
// company the moment a member is required, and every case below turns on that.

// mgO029: base and derived are spelled identically, both
// <all minOccurs="0"> around a required e1. Under any correct reading a type
// restricts itself, so a rejection here is a false REJECT of a valid schema.
//
// The budget reading rejected it by taking e1's own minOccurs as the budget's
// floor while allBranchCounts scaled the derived side's e1 to 0..1: the two
// sides disagreed about the same group. The disjunction reading forks both
// sides the same way, and R's full-match branch meets B's full-match
// alternative.
const allSelfRestriction = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <xsd:element name="doc" type="foo"/>
 <xsd:complexType name="foo">
  <xsd:complexContent><xsd:restriction base="bar">
   <xsd:all maxOccurs="1" minOccurs="0">
    <xsd:element name="e1"/>
   </xsd:all>
  </xsd:restriction></xsd:complexContent>
 </xsd:complexType>
 <xsd:complexType name="bar">
  <xsd:all maxOccurs="1" minOccurs="0">
   <xsd:element name="e1"/>
  </xsd:all>
 </xsd:complexType>
</xsd:schema>`

// particlesK006: B is <all minOccurs="0"> requiring a1, R is a bare a1 at
// 0..1. The suite scores it INVALID, and its sibling K005 — identical but for
// a1's minOccurs="1" — valid, so the floor is the whole of the distinction.
//
// R's branch produces a1 zero-or-once. That is neither certainly nothing nor
// certainly a full match of B's group, so it straddles B's two alternatives
// and belongs to neither. Scaling B's floors to zero would flatten the
// disjunction into 0..1 and accept it — the false ACCEPT this test guards.
const allSkippableStraddle = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"
    targetNamespace="http://xsdtesting" xmlns:x="http://xsdtesting">
 <xsd:complexType name="B">
  <xsd:all minOccurs="0">
   <xsd:element name="a0" minOccurs="0"/>
   <xsd:element name="a1" minOccurs="1" maxOccurs="1"/>
   <xsd:element name="a2" minOccurs="0"/>
  </xsd:all>
 </xsd:complexType>
 <xsd:complexType name="R">
  <xsd:complexContent><xsd:restriction base="x:B">
   <xsd:sequence>
    <xsd:element name="a1" minOccurs="0" maxOccurs="1"/>
   </xsd:sequence>
  </xsd:restriction></xsd:complexContent>
 </xsd:complexType>
 <xsd:element name="doc" type="x:R"/>
</xsd:schema>`

// particlesK005, the sibling that must stay valid: a1 at 1..1 produces a full
// match of B's group, so it takes the second alternative cleanly. Without it,
// a rule that simply rejected every skippable base would look correct on K006.
const allSkippableFullMatch = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"
    targetNamespace="http://xsdtesting" xmlns:x="http://xsdtesting">
 <xsd:complexType name="B">
  <xsd:all minOccurs="0">
   <xsd:element name="a0" minOccurs="0"/>
   <xsd:element name="a1" minOccurs="1" maxOccurs="1"/>
   <xsd:element name="a2" minOccurs="0"/>
  </xsd:all>
 </xsd:complexType>
 <xsd:complexType name="R">
  <xsd:complexContent><xsd:restriction base="x:B">
   <xsd:sequence>
    <xsd:element name="a1" minOccurs="1" maxOccurs="1"/>
   </xsd:sequence>
  </xsd:restriction></xsd:complexContent>
 </xsd:complexType>
 <xsd:element name="doc" type="x:R"/>
</xsd:schema>`

// The coupling between two required members, which no suite case covers and
// which a per-name range cannot express at all. B admits {} and {a1,a2} and
// nothing else; R makes a2 optional and so admits {a1} alone, content the base
// forbids. Flattening each name to 0..1 accepts it — a false ACCEPT, the
// dangerous direction, and the reason the two alternatives must stay apart
// rather than being merged into one range.
const allSkippableCoupling = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <xsd:element name="doc" type="R"/>
 <xsd:complexType name="B">
  <xsd:all minOccurs="0">
   <xsd:element name="a1"/>
   <xsd:element name="a2"/>
  </xsd:all>
 </xsd:complexType>
 <xsd:complexType name="R">
  <xsd:complexContent><xsd:restriction base="B">
   <xsd:all minOccurs="0">
    <xsd:element name="a1"/>
    <xsd:element name="a2" minOccurs="0"/>
   </xsd:all>
  </xsd:restriction></xsd:complexContent>
 </xsd:complexType>
</xsd:schema>`

// Dropping the group's content entirely takes B's skip alternative, which is
// the half of the disjunction that makes mgO029 decidable in the first place.
const allSkippableEmptied = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <xsd:element name="doc" type="R"/>
 <xsd:complexType name="B">
  <xsd:all minOccurs="0">
   <xsd:element name="a1"/>
  </xsd:all>
 </xsd:complexType>
 <xsd:complexType name="R">
  <xsd:complexContent><xsd:restriction base="B">
   <xsd:sequence/>
  </xsd:restriction></xsd:complexContent>
 </xsd:complexType>
</xsd:schema>`

// A base group that must match once is untouched by any of this: its language
// is not a disjunction, and its members' floors are simply required.
const allRequiredGroupStraddle = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <xsd:element name="doc" type="R"/>
 <xsd:complexType name="B">
  <xsd:all>
   <xsd:element name="a1"/>
  </xsd:all>
 </xsd:complexType>
 <xsd:complexType name="R">
  <xsd:complexContent><xsd:restriction base="B">
   <xsd:sequence>
    <xsd:element name="a1" minOccurs="0" maxOccurs="1"/>
   </xsd:sequence>
  </xsd:restriction></xsd:complexContent>
 </xsd:complexType>
</xsd:schema>`

func TestAllGroupSkippableIsDisjunction(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		valid bool
		// want, when the schema is invalid, is a fragment the
		// diagnostic must carry, so that a case cannot pass by being
		// rejected for some unrelated reason.
		want string
	}{
		{"mgO029/self-restriction", allSelfRestriction, true, ""},
		{"particlesK005/full-match", allSkippableFullMatch, true, ""},
		{"skip-alternative/emptied", allSkippableEmptied, true, ""},
		{"particlesK006/straddle", allSkippableStraddle, false,
			"minOccurs 0 is below the base's 1"},
		{"coupling/split-required-members", allSkippableCoupling, false,
			"minOccurs 0 is below the base's 1"},
		// Decided by particleSubsumes, which runs first and phrases the
		// same violation in terms of the language rather than a floor.
		{"required-group/straddle", allRequiredGroupStraddle, false,
			"the restriction admits content the base does not allow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadSrc(t, tc.src, Version11)
			switch {
			case tc.valid && err != nil:
				t.Fatalf("valid restriction rejected: %v", err)
			case !tc.valid && err == nil:
				t.Fatal("invalid restriction accepted: the base's " +
					"all group is a disjunction, and this " +
					"derivation fits neither alternative")
			case !tc.valid && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("rejected for the wrong reason:\ngot:  %v\nwant substring: %s",
					err, tc.want)
			}
		})
	}
}

// The 1.0 structural table decides these without allSubsumes, and must not
// move: the disjunction reading is 1.1's language inclusion (§3.4.6.4), which
// 1.0 does not have. mgO029 is valid under both only because a type restricting
// itself satisfies the table too.
func TestAllGroupSkippableUnchangedUnder10(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		valid bool
	}{
		{"mgO029/self-restriction", allSelfRestriction, true},
		{"particlesK005/full-match", allSkippableFullMatch, true},
		{"particlesK006/straddle", allSkippableStraddle, false},
		{"coupling/split-required-members", allSkippableCoupling, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadSrc(t, tc.src, Version10)
			if tc.valid != (err == nil) {
				t.Fatalf("valid=%v but err=%v", tc.valid, err)
			}
		})
	}
}
