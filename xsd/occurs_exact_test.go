package xsd

import (
	"fmt"
	"math"
	"testing"
)

// Saturating occurrence arithmetic fixed the wrap. It left a second defect
// behind, and this file is about that one: two bounds that both exceed the
// saturation point compare *equal*, because both clamp to occursHuge.
//
// The derivation checks are a pair of inequalities, so collapsing distinct
// magnitudes onto one value is not conservative in either direction. A base of
// 1e30 restricted by three members of 1e30 each has a true effective total of
// 3e30 against a base of 1e30 — invalid — and saturation makes both sides read
// 4611686018427387903, so the restriction is accepted.
//
// The probes below come in pairs, one where the restriction genuinely fits and
// one where it genuinely does not, because a fix that simply rejected
// everything large would pass a one-sided test while breaking every schema
// that legitimately writes a huge bound.

// aboveThird is a decimal literal above MaxInt/3: three of them saturate, one
// of them does not. This is the *near* case, and it is where the collapse does
// not yet bite — the derived side saturates while the base stays exact, so the
// inequality still comes out right. It is here to pin that the fix does not
// regress the cases the saturating version already got right.
var aboveThird = fmt.Sprintf("%d", math.MaxInt/3+1)

// past is a bound far beyond occursHuge, so that *both* sides of every
// comparison below saturate. This is where the collapse actually happens.
const past = "1000000000000000000000000000000" // 1e30

func timesLiteral(t *testing.T, n string, k int) string {
	t.Helper()
	// Decimal multiplication by a small k, done on the string so the test
	// does not depend on the very arithmetic it is testing.
	digits := []byte(n)
	carry := 0
	for i := len(digits) - 1; i >= 0; i-- {
		v := int(digits[i]-'0')*k + carry
		digits[i] = byte('0' + v%10)
		carry = v / 10
	}
	for carry > 0 {
		digits = append([]byte{byte('0' + carry%10)}, digits...)
		carry /= 10
	}
	return string(digits)
}

// seqRestrictsWildcard builds a base wildcard admitting baseMax children and a
// derived sequence of members children, each written derivedEach times. The
// derived effective total range is members*derivedEach, and the check is
// whether that fits the base.
//
// The wildcard path is chosen because it goes through effectiveTotalRange,
// which is the addition site, and rangeOK, which is the comparison.
func seqRestrictsWildcard(t *testing.T, baseMax string, members int, derivedEach string) error {
	t.Helper()
	body := ""
	for i := 0; i < members; i++ {
		body += fmt.Sprintf(
			"      <xs:element name=%q type=\"xs:string\" minOccurs=%q maxOccurs=%q/>\n",
			fmt.Sprintf("e%d", i), derivedEach, derivedEach)
	}
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="base">
    <xs:sequence>
      <xs:any minOccurs="0" maxOccurs="` + baseMax + `" processContents="lax"/>
    </xs:sequence>
  </xs:complexType>
  <xs:complexType name="derived">
    <xs:complexContent>
      <xs:restriction base="base">
        <xs:sequence>
` + body + `        </xs:sequence>
      </xs:restriction>
    </xs:complexContent>
  </xs:complexType>
</xs:schema>`
	_, err := parseSchemaString(t, src)
	return err
}

// TestOccursExactSummation is the table the review asked for: a base of kN
// against a derived total of mN, for N past the saturation point and for N just
// above MaxInt/3.
func TestOccursExactSummation(t *testing.T) {
	for _, n := range []struct {
		label string
		value string
	}{
		{"past occursHuge", past},
		{"above MaxInt/3", aboveThird},
	} {
		t.Run(n.label, func(t *testing.T) {
			cases := []struct {
				name       string
				baseTimes  int
				members    int
				wantAccept bool
			}{
				{"base N, derived N", 1, 1, true},
				{"base 3N, derived N", 3, 1, true},
				{"base N, derived 3N", 1, 3, false},
				{"base 3N, derived 2N", 3, 2, true},
				{"base 2N, derived 3N", 2, 3, false},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					baseMax := timesLiteral(t, n.value, c.baseTimes)
					err := seqRestrictsWildcard(t, baseMax, c.members, n.value)
					if c.wantAccept && err != nil {
						t.Errorf("a derived total of %d×N against a base of %d×N was rejected: %v",
							c.members, c.baseTimes, err)
					}
					if !c.wantAccept && err == nil {
						t.Errorf("a derived total of %d×N exceeds a base of %d×N but was accepted",
							c.members, c.baseTimes)
					}
				})
			}
		})
	}
}

// TestOccursExactNestedProduct reaches the multiplication site rather than the
// addition one. Two members of N each inside a sequence repeated twice has an
// effective total of 4N — 2 members × 2 repetitions — which is far beyond an
// int64 when N is 1e30, and must still be recognised as exceeding a base of 3N.
func TestOccursExactNestedProduct(t *testing.T) {
	base3N := timesLiteral(t, past, 3)
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="base">
    <xs:sequence>
      <xs:any minOccurs="0" maxOccurs="` + base3N + `" processContents="lax"/>
    </xs:sequence>
  </xs:complexType>
  <xs:complexType name="derived">
    <xs:complexContent>
      <xs:restriction base="base">
        <xs:sequence minOccurs="2" maxOccurs="2">
          <xs:element name="a" type="xs:string" minOccurs="` + past + `" maxOccurs="` + past + `"/>
          <xs:element name="b" type="xs:string" minOccurs="` + past + `" maxOccurs="` + past + `"/>
        </xs:sequence>
      </xs:restriction>
    </xs:complexContent>
  </xs:complexType>
</xs:schema>`
	if _, err := parseSchemaString(t, src); err == nil {
		t.Error("an effective total of 4N was accepted against a base of 3N")
	}
	// The mirror: 4N against a base of 4N fits exactly.
	base4N := timesLiteral(t, past, 4)
	ok := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="base">
    <xs:sequence>
      <xs:any minOccurs="0" maxOccurs="` + base4N + `" processContents="lax"/>
    </xs:sequence>
  </xs:complexType>
  <xs:complexType name="derived">
    <xs:complexContent>
      <xs:restriction base="base">
        <xs:sequence minOccurs="0" maxOccurs="2">
          <xs:element name="a" type="xs:string" minOccurs="0" maxOccurs="` + past + `"/>
          <xs:element name="b" type="xs:string" minOccurs="0" maxOccurs="` + past + `"/>
        </xs:sequence>
      </xs:restriction>
    </xs:complexContent>
  </xs:complexType>
</xs:schema>`
	if _, err := parseSchemaString(t, ok); err != nil {
		t.Errorf("an effective total of 4N against a base of 4N was rejected: %v", err)
	}
}

// TestOccursExactPureAddition isolates the sum. N + N with N above MaxInt/2
// saturates on the derived side while the base 2N also saturates, so the two
// compare equal and a base of only N accepts a derived total of 2N.
func TestOccursExactPureAddition(t *testing.T) {
	halfPlus := fmt.Sprintf("%d", math.MaxInt/2+1)
	for _, n := range []string{past, halfPlus} {
		if err := seqRestrictsWildcard(t, n, 2, n); err == nil {
			t.Errorf("N=%s: a derived total of 2N was accepted against a base of N", n)
		}
		if err := seqRestrictsWildcard(t, timesLiteral(t, n, 2), 2, n); err != nil {
			t.Errorf("N=%s: a derived total of 2N against a base of 2N was rejected: %v", n, err)
		}
	}
}

// TestOccursExactSequenceRestrictingChoice drives mapAndSum, which multiplies
// the derived particle's own bounds by the group's length rather than by an
// inner particle's. Three members at N each against a choice of 2N is 3N and
// must be refused; against 3N it fits.
func TestOccursExactSequenceRestrictingChoice(t *testing.T) {
	build := func(baseMax string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="base">
    <xs:choice minOccurs="0" maxOccurs="` + baseMax + `">
      <xs:element name="a" type="xs:string"/>
      <xs:element name="b" type="xs:string"/>
      <xs:element name="c" type="xs:string"/>
    </xs:choice>
  </xs:complexType>
  <xs:complexType name="derived">
    <xs:complexContent>
      <xs:restriction base="base">
        <xs:sequence minOccurs="0" maxOccurs="` + past + `">
          <xs:element name="a" type="xs:string"/>
          <xs:element name="b" type="xs:string"/>
          <xs:element name="c" type="xs:string"/>
        </xs:sequence>
      </xs:restriction>
    </xs:complexContent>
  </xs:complexType>
</xs:schema>`
	}
	if _, err := parseSchemaString(t, build(timesLiteral(t, past, 2))); err == nil {
		t.Error("a sequence of 3 members repeated N times was accepted against a choice of 2N")
	}
	if _, err := parseSchemaString(t, build(timesLiteral(t, past, 3))); err != nil {
		t.Errorf("3N against a base of 3N was rejected: %v", err)
	}
}
