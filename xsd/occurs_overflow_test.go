package xsd

import (
	"regexp"
	"strings"
	"testing"
)

// Occurrence bounds are combined by the derivation checks — multiplied by a
// model group's length, and summed across a group's members — and until this
// test those combinations wrapped.
//
// occursValue saturates a bound too large for an int at occursHuge, which is
// int(^uint(0) >> 2): roughly a quarter of the int range. That leaves room to
// double the value but not to triple it. Particle Derivation OK
// (Sequence:Choice) computes (min occurs × length, max occurs × length), so a
// saturated bound on a sequence of three members produced
// occursHuge*3 == -4611686018427387907 and reported
//
//	sequence restricting a choice: minOccurs -4611686018427387907 is below the base's 0
//
// A negative bound in a diagnostic is the visible half of the problem. The
// invisible half is worse: rangeOK is a pair of inequalities, and a bound that
// has wrapped negative compares *below* every base minimum and *below* every
// base maximum, so the same wrap that produced this spurious rejection would,
// with the operands the other way round, silently accept a restriction that
// widens its base.
//
// The fix is to saturate the products and sums at occursHuge rather than let
// them wrap, on the same reasoning the parser already uses: no document can
// supply that many children, so a bound clamped there behaves exactly as the
// true value would.
//
// negativeNumber finds a wrapped bound in a diagnostic. A minus sign followed
// by digits cannot appear in an occurrence diagnostic otherwise: minOccurs and
// maxOccurs are xs:nonNegativeInteger, and a negative literal is refused at
// parse time long before it could reach a comparison.
var negativeNumber = regexp.MustCompile(`-\d+`)

// hugeOccurs is a bound past what an int can hold, which the parser saturates
// to occursHuge. It is msData's own value, from particlesZ033_a.
const hugeOccurs = "79228162514244337593543950335"

// checkNoWrap asserts that whatever the loader decided, it did not decide it
// on the strength of a wrapped bound.
func checkNoWrap(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if m := negativeNumber.FindString(err.Error()); m != "" {
		t.Errorf("diagnostic contains a wrapped occurrence bound %s: %v", m, err)
	}
}

// TestOccursOverflowSequenceRestrictingChoice is the reported case: a sequence
// of three members, carrying a saturated minOccurs, restricting a choice.
// mapAndSum multiplies the bound by three, which is the product that wraps.
func TestOccursOverflowSequenceRestrictingChoice(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="base">
    <xs:choice minOccurs="0" maxOccurs="unbounded">
      <xs:element name="a" type="xs:string"/>
      <xs:element name="b" type="xs:string"/>
      <xs:element name="c" type="xs:string"/>
    </xs:choice>
  </xs:complexType>
  <xs:complexType name="derived">
    <xs:complexContent>
      <xs:restriction base="base">
        <xs:sequence minOccurs="` + hugeOccurs + `" maxOccurs="` + hugeOccurs + `">
          <xs:element name="a" type="xs:string"/>
          <xs:element name="b" type="xs:string"/>
          <xs:element name="c" type="xs:string"/>
        </xs:sequence>
      </xs:restriction>
    </xs:complexContent>
  </xs:complexType>
</xs:schema>`
	_, err := parseSchemaString(t, src)
	checkNoWrap(t, err)
}

// TestOccursOverflowEffectiveTotalRange reaches the second multiplication
// site. effectiveTotalRange sums its members' contributions and then scales
// the sum by the enclosing particle's own bounds, so three saturated members
// inside a repeated sequence overflow on the sum before the product is even
// reached.
func TestOccursOverflowEffectiveTotalRange(t *testing.T) {
	group := &ModelGroup{
		Compositor: CompositorSequence,
		Particles: []*Particle{
			{MinOccurs: occursHuge, MaxOccurs: occursHuge, Term: &ElementDecl{}},
			{MinOccurs: occursHuge, MaxOccurs: occursHuge, Term: &ElementDecl{}},
			{MinOccurs: occursHuge, MaxOccurs: occursHuge, Term: &ElementDecl{}},
		},
	}
	p := &Particle{MinOccurs: occursHuge, MaxOccurs: occursHuge, Term: group}
	min, max := effectiveTotalRange(p)
	if min != occursHuge {
		t.Errorf("effective total minimum = %d, want %d", min, occursHuge)
	}
	if max != occursHuge {
		t.Errorf("effective total maximum = %d, want %d", max, occursHuge)
	}
}

// TestOccursOverflowAgainstWildcard drives the same overflow through a schema
// rather than through the component API: a saturated sequence restricting a
// wildcard is compared by its effective total range, which is where the
// wrapped sum would have landed in the diagnostic.
func TestOccursOverflowAgainstWildcard(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="base">
    <xs:sequence>
      <xs:any minOccurs="0" maxOccurs="1" processContents="lax"/>
    </xs:sequence>
  </xs:complexType>
  <xs:complexType name="derived">
    <xs:complexContent>
      <xs:restriction base="base">
        <xs:sequence minOccurs="` + hugeOccurs + `" maxOccurs="` + hugeOccurs + `">
          <xs:element name="a" type="xs:string" minOccurs="` + hugeOccurs + `" maxOccurs="` + hugeOccurs + `"/>
          <xs:element name="b" type="xs:string" minOccurs="` + hugeOccurs + `" maxOccurs="` + hugeOccurs + `"/>
          <xs:element name="c" type="xs:string" minOccurs="` + hugeOccurs + `" maxOccurs="` + hugeOccurs + `"/>
        </xs:sequence>
      </xs:restriction>
    </xs:complexContent>
  </xs:complexType>
</xs:schema>`
	_, err := parseSchemaString(t, src)
	if err == nil {
		t.Fatal("a sequence of that size cannot restrict a 0..1 wildcard")
	}
	checkNoWrap(t, err)
	// The saturated bound must survive into the diagnostic intact, which
	// is the positive form of the same claim.
	if !strings.Contains(err.Error(), "4611686018427387903") {
		t.Errorf("diagnostic does not report the saturated bound: %v", err)
	}
}
