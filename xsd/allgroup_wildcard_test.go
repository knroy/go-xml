package xsd

import (
	"strings"
	"testing"
)

// XSD 1.1 permits derivations of an <all> group that the 1.0 structural table
// calls Forbidden: a sequence or a wildcard restricting an all group, and a
// named model group merged into one. Deciding them means working out how a
// wildcard's occurrences split between the names it spans, which is not a
// simple count — a wildcard at 2..2 over {two, three} is not the same budget
// as one wildcard at 2..2 over {two} beside another at 2..2 over {three}.
//
// The cases below are transcribed from the W3C suite. They are guarded here
// rather than left to the conformance run because the aggregate agreement
// count says only that the total moved, never which shape regressed: the five
// valid schemas and the invalid one that discriminates them are one property,
// and it is two-sided.

// all206: a named model group merged into an all group on both sides. R's
// group narrows b to 3..4 and c to 2..4, both inside the base's ranges, and
// drops a — whose floor is 0, so dropping it is allowed.
const allWildcardNamedGroup = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:complexType name="b">
  <xs:all>
   <xs:group ref="abc"/>
   <xs:element name="d" minOccurs="1" maxOccurs="1"/>
  </xs:all>
 </xs:complexType>
 <xs:group name="abc">
  <xs:all>
   <xs:element name="a" minOccurs="0" maxOccurs="5"/>
   <xs:element name="b" minOccurs="1" maxOccurs="5"/>
   <xs:element name="c" minOccurs="2" maxOccurs="unbounded"/>
  </xs:all>
 </xs:group>
 <xs:complexType name="r">
  <xs:complexContent><xs:restriction base="b">
   <xs:all>
    <xs:element name="d" minOccurs="1" maxOccurs="1"/>
    <xs:group ref="bc"/>
   </xs:all>
  </xs:restriction></xs:complexContent>
 </xs:complexType>
 <xs:group name="bc">
  <xs:all>
   <xs:element name="b" minOccurs="3" maxOccurs="4"/>
   <xs:element name="c" minOccurs="2" maxOccurs="4"/>
  </xs:all>
 </xs:group>
</xs:schema>`

// all218: a singleton sequence restricting an all group of two wildcards. The
// derived wildcard spans only two.uri, so it draws entirely on the base's
// second branch, and 2..4 sits inside that branch's 0..5. The base's first
// branch is dropped, which its floor of 0 permits.
const allWildcardSequenceOfOne = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:complexType name="b">
  <xs:all>
   <xs:any namespace="http://one.uri/" minOccurs="0" maxOccurs="5"/>
   <xs:any namespace="http://two.uri/" minOccurs="0" maxOccurs="5"/>
  </xs:all>
 </xs:complexType>
 <xs:complexType name="r">
  <xs:complexContent><xs:restriction base="b">
   <xs:sequence>
    <xs:any namespace="http://two.uri/" minOccurs="2" maxOccurs="4"/>
   </xs:sequence>
  </xs:restriction></xs:complexContent>
 </xs:complexType>
</xs:schema>`

// all237: overlapping wildcards that still subsume. R splits the base's
// {one,two} branch into a one-only wildcard at 3..unbounded and a two-only one
// at 2..2, so every document R admits draws at least 5 from that branch —
// meeting its floor — while three stays inside its own branch's 0..2.
const allWildcardOverlapValid = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:complexType name="b">
  <xs:all>
   <xs:any namespace="http://one.uri/ http://two.uri/" minOccurs="5" maxOccurs="unbounded"/>
   <xs:any namespace="http://three.uri/" minOccurs="0" maxOccurs="2"/>
  </xs:all>
 </xs:complexType>
 <xs:complexType name="r">
  <xs:complexContent><xs:restriction base="b">
   <xs:all>
    <xs:any namespace="http://one.uri/" minOccurs="3" maxOccurs="unbounded"/>
    <xs:any namespace="http://two.uri/" minOccurs="2" maxOccurs="2"/>
    <xs:any namespace="http://three.uri/" minOccurs="2" maxOccurs="2"/>
   </xs:all>
  </xs:restriction></xs:complexContent>
 </xs:complexType>
</xs:schema>`

// all244.n, and the whole reason this file exists. It differs from all237 by
// one edit: the two-only and three-only wildcards are merged into a single
// wildcard at 2..2 spanning {two, three}. That merge lets R admit
// (one, one, one, three, three) — three drawn twice from a budget the base
// caps at 2 for three while its {one,two} branch goes unmet at 3 of 5 — which
// the base forbids.
//
// A per-name count cannot tell all237 from all244.n: both spell one at
// 3..unbounded and both spell a 2..2 alongside it. Only tracking which names a
// wildcard's occurrences may be spent on separates them. Any relaxation that
// accepts the five valid schemas by loosening that rule accepts this one too,
// which is what makes it the discriminating half of the pair rather than a
// sixth case of the same kind.
const allWildcardOverlapInvalid = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:complexType name="b">
  <xs:all>
   <xs:any namespace="http://one.uri/ http://two.uri/" minOccurs="5" maxOccurs="unbounded"/>
   <xs:any namespace="http://three.uri/" minOccurs="0" maxOccurs="2"/>
  </xs:all>
 </xs:complexType>
 <xs:complexType name="r">
  <xs:complexContent><xs:restriction base="b">
   <xs:all>
    <xs:any namespace="http://one.uri/" minOccurs="3" maxOccurs="unbounded"/>
    <xs:any namespace="http://two.uri/ http://three.uri/" minOccurs="2" maxOccurs="2"/>
   </xs:all>
  </xs:restriction></xs:complexContent>
 </xs:complexType>
</xs:schema>`

// wild049: one derived wildcard restricting two base ones. Its notQName list
// excludes every name the base's two branches disallow, so the names it still
// spans are covered whichever branch each one falls to.
const allWildcardMergedBranches = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    xmlns:x="http://extra.com/">
 <xs:complexType name="computer">
  <xs:all>
   <xs:element name="name" type="xs:string"/>
   <xs:any namespace="##local" notQName="a b c" minOccurs="0" maxOccurs="2" processContents="skip"/>
   <xs:any notNamespace="##local" notQName="x:c x:d x:e" minOccurs="0" maxOccurs="2" processContents="skip"/>
  </xs:all>
 </xs:complexType>
 <xs:complexType name="restrictedComputer">
  <xs:complexContent><xs:restriction base="computer">
   <xs:sequence>
    <xs:element name="name" type="xs:string"/>
    <xs:any namespace="##any" notQName="a b c d x:c x:d x:e x:f" minOccurs="1" maxOccurs="2" processContents="skip"/>
   </xs:sequence>
  </xs:restriction></xs:complexContent>
 </xs:complexType>
 <xs:element name="computer" type="restrictedComputer"/>
</xs:schema>`

// wild050: the converse split — two derived wildcards against one base
// wildcard. Their floors sum to 2 and their ceilings to 6, matching the base's
// 2..6 exactly, and neither spans a name the base's notQName excludes.
const allWildcardSplitBranches = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    xmlns:x="http://extra.com/">
 <xs:complexType name="computer">
  <xs:all>
   <xs:element name="name" type="xs:string"/>
   <xs:any namespace="##any" notQName="a b x:e x:d" minOccurs="2" maxOccurs="6" processContents="skip"/>
  </xs:all>
 </xs:complexType>
 <xs:complexType name="restrictedComputer">
  <xs:complexContent><xs:restriction base="computer">
   <xs:sequence>
    <xs:element name="name" type="xs:string"/>
    <xs:any namespace="##local" notQName="a b c" minOccurs="1" maxOccurs="3" processContents="skip"/>
    <xs:any notNamespace="##local" notQName="x:c x:d x:e" minOccurs="1" maxOccurs="3" processContents="skip"/>
   </xs:sequence>
  </xs:restriction></xs:complexContent>
 </xs:complexType>
 <xs:element name="computer" type="restrictedComputer"/>
</xs:schema>`

func TestAllGroupWildcardSubsumption(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		valid bool
		// want, when the schema is invalid, is a fragment the
		// diagnostic must carry, so that a case cannot pass by being
		// rejected for some unrelated reason.
		want string
	}{
		{"all206/named-model-group", allWildcardNamedGroup, true, ""},
		{"all218/sequence-of-one-wildcard", allWildcardSequenceOfOne, true, ""},
		{"all237/overlapping-wildcards", allWildcardOverlapValid, true, ""},
		{"wild049/merged-branches", allWildcardMergedBranches, true, ""},
		{"wild050/split-branches", allWildcardSplitBranches, true, ""},
		{"all244.n/merged-budget-widens", allWildcardOverlapInvalid, false,
			"the base requires a wildcard, which the restriction omits"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadSrc(t, tc.src, Version11)
			switch {
			case tc.valid && err != nil:
				t.Fatalf("valid restriction rejected: %v", err)
			case !tc.valid && err == nil:
				t.Fatal("invalid restriction accepted: merging two " +
					"of the base's branches into one wildcard lets " +
					"the restriction spend that budget on names the " +
					"base caps separately")
			case !tc.valid && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("rejected for the wrong reason:\ngot:  %v\nwant substring: %s",
					err, tc.want)
			}
		})
	}
}

// The 1.0 structural table calls every one of these Forbidden, and must not
// move: a non-element particle in an all group violates cos-all-limited.1
// whatever the derivation does, and wild049/wild050 additionally spell
// notQName, which 1.1 introduced. A relaxation aimed at 1.1 that leaked into
// the 1.0 lane would show up here rather than as a drop in the suite total.
func TestAllGroupWildcardForbiddenUnder10(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"all206/named-model-group", allWildcardNamedGroup},
		{"all218/sequence-of-one-wildcard", allWildcardSequenceOfOne},
		{"all237/overlapping-wildcards", allWildcardOverlapValid},
		{"all244.n/merged-budget-widens", allWildcardOverlapInvalid},
		{"wild049/merged-branches", allWildcardMergedBranches},
		{"wild050/split-branches", allWildcardSplitBranches},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := loadSrc(t, tc.src, Version10); err == nil {
				t.Fatal("XSD 1.0 accepted an all group holding a " +
					"non-element particle, which cos-all-limited.1 forbids")
			}
		})
	}
}
