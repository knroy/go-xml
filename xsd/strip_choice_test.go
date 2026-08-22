package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

func loadSrc(t *testing.T, src string, v Version) error {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Load(tree.Root, "t.xsd", Options{Version: v})
	return err
}

// A one-member choice restricting a choice keeps its identity under 1.1: the
// table dispatches on the compositor, so stripping the wrapper turns a
// choice-restricting-choice derivation into a sequence restricting a choice,
// which is a different cell with a different rule.
//
// The 1.0 table has no such relaxation and the derivation really is invalid
// there. particlesZ023 asserts exactly that split — invalid under 1.0, valid
// under 1.1 — so a fix that is not version-gated breaks one to fix the other.
const choiceRestrictsChoice = `<xsd:schema targetNamespace="http://myuri" xmlns="http://myuri"
    xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <xsd:complexType name="base">
  <xsd:sequence>
   <xsd:choice>
    <xsd:sequence>
      <xsd:element name="AAA" minOccurs="0" maxOccurs="unbounded"/>
      <xsd:element name="BBB" minOccurs="0" maxOccurs="unbounded"/>
      <xsd:element name="CCC" minOccurs="0" maxOccurs="unbounded"/>
    </xsd:sequence>
    <xsd:sequence>
      <xsd:element name="AAAA" minOccurs="0" maxOccurs="unbounded"/>
      <xsd:element name="BBBB" minOccurs="0" maxOccurs="unbounded"/>
      <xsd:element name="CCCC" minOccurs="0" maxOccurs="unbounded"/>
    </xsd:sequence>
   </xsd:choice>
  </xsd:sequence>
 </xsd:complexType>
 <xsd:complexType name="derived">
  <xsd:complexContent><xsd:restriction base="base">
   <xsd:sequence>
    <xsd:choice>
     <xsd:sequence>
       <xsd:element name="AAA" minOccurs="0" maxOccurs="unbounded"/>
       <xsd:element name="BBB" minOccurs="0" maxOccurs="unbounded"/>
       <xsd:element name="CCC" minOccurs="0" maxOccurs="unbounded"/>
     </xsd:sequence>
    </xsd:choice>
   </xsd:sequence>
  </xsd:restriction></xsd:complexContent>
 </xsd:complexType>
</xsd:schema>`

func TestOneMemberChoiceIsVersionDependent(t *testing.T) {
	if err := loadSrc(t, choiceRestrictsChoice, Version11); err != nil {
		t.Errorf("1.1 should accept dropping one alternative: %v", err)
	}
	err := loadSrc(t, choiceRestrictsChoice, Version10)
	if err == nil {
		t.Error("1.0's table forbids this; accepting it is a false accept")
	} else if !strings.Contains(err.Error(), "restriction") {
		t.Errorf("1.0 error = %v, want a restriction failure", err)
	}
}

// Against a base that is *not* a choice there is nothing for the wrapper to
// preserve, and stripping it is what lets the derivation reach a cell at all.
//
// particlesR001 is this shape. A first version of the fix kept every
// one-member choice under 1.1 and turned R001 into a false reject — "a choice
// group may not restrict a sequence group" — which is why the condition is
// that *both* sides are choices rather than just the derived one.
func TestOneMemberChoiceAgainstSequenceStillStrips(t *testing.T) {
	const src = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"
    targetNamespace="http://xsdtesting" xmlns:x="http://xsdtesting">
 <xsd:complexType name="B">
  <xsd:sequence minOccurs="0" maxOccurs="1">
   <xsd:any namespace="##any" minOccurs="1" maxOccurs="1"/>
  </xsd:sequence>
 </xsd:complexType>
 <xsd:complexType name="R">
  <xsd:complexContent><xsd:restriction base="x:B">
   <xsd:sequence minOccurs="1" maxOccurs="1">
    <xsd:choice minOccurs="1" maxOccurs="1">
     <xsd:element name="e1"/>
    </xsd:choice>
   </xsd:sequence>
  </xsd:restriction></xsd:complexContent>
 </xsd:complexType>
</xsd:schema>`
	for _, v := range []Version{Version10, Version11} {
		if err := loadSrc(t, src, v); err != nil {
			t.Errorf("v=%v: a one-member choice restricting a sequence is valid: %v", v, err)
		}
	}
}

// A group reference inside an all group is flattened before the occurrence
// budget is built.
//
// XSD 1.1 requires such a reference to name a group whose model is itself an
// all group, and an all group of all groups admits exactly the interleaving of
// their members — so the nesting carries no information the flat list does
// not. Without flattening, allSubsumes finds a group particle where it expects
// an element declaration, gives up, and falls back to the 1.0 table, which
// calls the derivation Forbidden. all206 is that shape.
func TestNestedAllGroupIsFlattened(t *testing.T) {
	const src = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
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
	if err := loadSrc(t, src, Version11); err != nil {
		t.Errorf("1.1 should accept a narrowed nested all group: %v", err)
	}
}

// Flattening must not lose the occurrence range of a *repeating* nested group:
// its members' ranges would be multiplied, and folding them into the parent
// would compare the wrong budgets. Such a group is left in place, which makes
// allSubsumes fall back rather than guess.
func TestRepeatingNestedAllGroupIsNotFlattened(t *testing.T) {
	ps := []*Particle{
		{MinOccurs: 1, MaxOccurs: 1, Term: &ModelGroup{
			Compositor: CompositorAll,
			Particles:  []*Particle{{MinOccurs: 1, MaxOccurs: 1, Term: &ElementDecl{}}},
		}},
		{MinOccurs: 0, MaxOccurs: 3, Term: &ModelGroup{
			Compositor: CompositorAll,
			Particles:  []*Particle{{MinOccurs: 1, MaxOccurs: 1, Term: &ElementDecl{}}},
		}},
	}
	got := flattenAllGroups(ps)
	if len(got) != 2 {
		t.Fatalf("got %d particles, want 2", len(got))
	}
	if _, isElem := got[0].Term.(*ElementDecl); !isElem {
		t.Error("the once-occurring group should have been flattened")
	}
	if _, isGroup := got[1].Term.(*ModelGroup); !isGroup {
		t.Error("the repeating group should have been left in place")
	}
}

// Under 1.1 a derived choice may offer the base's alternatives in a different
// order: a choice imposes no order on what it admits, so the language is the
// same. 1.0's RecurseLax is an order-preserving walk and forbids it, which is
// why particlesT002 is marked invalid under 1.0 and valid under 1.1.
func TestReorderedChoiceAlternatives(t *testing.T) {
	const src = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:complexType name="b"><xs:sequence>
  <xs:choice>
   <xs:element name="c1"/>
   <xs:element name="c2"/>
  </xs:choice>
 </xs:sequence></xs:complexType>
 <xs:complexType name="r"><xs:complexContent><xs:restriction base="b">
  <xs:sequence>
   <xs:choice>
    <xs:element name="c2"/>
    <xs:element name="c1"/>
   </xs:choice>
  </xs:sequence>
 </xs:restriction></xs:complexContent></xs:complexType>
</xs:schema>`
	if err := loadSrc(t, src, Version11); err != nil {
		t.Errorf("1.1 should accept reordered alternatives: %v", err)
	}
	if err := loadSrc(t, src, Version10); err == nil {
		t.Error("1.0's ordered RecurseLax forbids this")
	}
}

// Each base alternative may back at most one derived alternative: merging two
// onto one would let the restriction admit a sequence twice where the base
// admits it once.
func TestChoiceAlternativesAreNotReused(t *testing.T) {
	const src = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:complexType name="b"><xs:sequence>
  <xs:choice><xs:element name="c1"/></xs:choice>
 </xs:sequence></xs:complexType>
 <xs:complexType name="r"><xs:complexContent><xs:restriction base="b">
  <xs:sequence>
   <xs:choice>
    <xs:element name="c1"/>
    <xs:element name="c1"/>
   </xs:choice>
  </xs:sequence>
 </xs:restriction></xs:complexContent></xs:complexType>
</xs:schema>`
	if err := loadSrc(t, src, Version11); err == nil {
		t.Error("two derived alternatives must not share one base alternative")
	}
}

// An optional element restricting a non-repeating optional choice puts its own
// range on the wrapper: the optionality belongs to the choice, not to the
// alternative inside it. particlesHa161 is that shape.
func TestOptionalElementRestrictsOptionalChoice(t *testing.T) {
	const src = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:complexType name="base"><xs:sequence>
  <xs:choice minOccurs="0">
   <xs:element name="a" type="xs:string"/>
   <xs:element name="b" type="xs:string"/>
  </xs:choice>
 </xs:sequence></xs:complexType>
 <xs:complexType name="derived"><xs:complexContent><xs:restriction base="base">
  <xs:sequence><xs:element name="a" type="xs:string" minOccurs="0"/></xs:sequence>
 </xs:restriction></xs:complexContent></xs:complexType>
</xs:schema>`
	if err := loadSrc(t, src, Version11); err != nil {
		t.Errorf("1.1 should accept an optional element here: %v", err)
	}
}

// The derived minimum must already satisfy the base's. Without that guard,
// moving a minOccurs of 0 onto the wrapper made it violate a base requiring
// one occurrence — ctF007 became a false reject for exactly one case gained.
func TestOptionalElementCannotWeakenARequiredChoice(t *testing.T) {
	const src = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:complexType name="base"><xs:sequence>
  <xs:choice minOccurs="1">
   <xs:element name="a" type="xs:string"/>
   <xs:element name="b" type="xs:string"/>
  </xs:choice>
 </xs:sequence></xs:complexType>
 <xs:complexType name="derived"><xs:complexContent><xs:restriction base="base">
  <xs:sequence><xs:element name="a" type="xs:string" minOccurs="0"/></xs:sequence>
 </xs:restriction></xs:complexContent></xs:complexType>
</xs:schema>`
	if err := loadSrc(t, src, Version11); err == nil {
		t.Error("an optional element must not restrict a required choice")
	}
}
