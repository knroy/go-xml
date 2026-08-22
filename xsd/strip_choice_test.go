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
