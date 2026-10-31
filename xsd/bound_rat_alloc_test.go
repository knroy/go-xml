package xsd

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The bound facets are parsed into rationals once, with the memoised chain.
// checkBounds parsed every bound again for every value validated: 18% of
// BenchmarkValidateInstance, whose one bound is minInclusive="0".
func TestBoundFacetsParsedOnce(t *testing.T) {
	s := mustParseSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="T"><xs:restriction base="xs:decimal">
	    <xs:minInclusive value="-1000.5"/><xs:maxExclusive value="1000.5"/>
	  </xs:restriction></xs:simpleType>
	</xs:schema>`)
	typ := xdm.QName{Local: "T"}
	for v, ok := range map[string]bool{"12.5": true, "-1000.5": true, "1000.5": false, "-1000.6": false} {
		if err := s.ValidateValue(v, typ); (err == nil) != ok {
			t.Errorf("ValidateValue(%q) = %v, want ok=%v", v, err, ok)
		}
	}
	// 25 allocations per value with the bounds re-parsed, 11 without.
	if n := testing.AllocsPerRun(20, func() { _ = s.ValidateValue("12.5", typ) }); n > 15 {
		t.Errorf("ValidateValue made %v allocations, want at most 15; are the bounds re-parsed?", n)
	}
}
