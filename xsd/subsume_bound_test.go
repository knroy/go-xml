package xsd

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The language-inclusion procedure unrolls a repetition into one state per
// copy, and used to decline outright above maxOccurs=64. That was a second
// cliff in front of subsumeMaxStates, which the unroll loop already checks on
// every iteration — so a legal restriction with a bound in the hundreds fell
// back to the structural rules for no reason.
//
// Removing it must not make a large bound expensive: the state budget has to
// stop the unroll instead. These load in milliseconds rather than declining
// early, and a bound no budget can hold declines through the budget.
//
// The cost is asserted as bytes allocated, which a loaded CI runner cannot
// move, rather than as wall time. The budget caps the unroll at 4096 states,
// so maxOccurs=100000 and 1000000 allocate what 1000 does (measured under
// 600KB each); an unroll the budget does not stop allocates per copy.
func TestSubsumeLargeOccursTerminates(t *testing.T) {
	for _, max := range []string{"64", "65", "100", "1000", "100000", "1000000", "unbounded"} {
		src := fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:t" targetNamespace="urn:t">
  <xs:complexType name="B"><xs:sequence>
    <xs:element name="a" type="xs:string" minOccurs="0" maxOccurs="%s"/>
  </xs:sequence></xs:complexType>
  <xs:complexType name="D"><xs:complexContent><xs:restriction base="t:B">
    <xs:sequence><xs:element name="a" type="xs:string" minOccurs="0" maxOccurs="1"/></xs:sequence>
  </xs:restriction></xs:complexContent></xs:complexType>
  <xs:element name="root" type="t:D"/>
</xs:schema>`, max)
		st, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("maxOccurs=%s: parse: %v", max, err)
		}
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		if _, err := Load(st.Root, "", Options{Version: Version11}); err != nil {
			t.Errorf("maxOccurs=%s: a restriction to maxOccurs=1 is legal, got %v", max, err)
		}
		runtime.ReadMemStats(&m1)
		const ceiling = 4 << 20
		if a := m1.TotalAlloc - m0.TotalAlloc; a > ceiling {
			t.Errorf("maxOccurs=%s: load allocated %dKB, ceiling %dKB; the state "+
				"budget did not bound the unroll", max, a/1024, ceiling/1024)
		}
	}
}
