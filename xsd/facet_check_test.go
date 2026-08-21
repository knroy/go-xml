package xsd

import "testing"

// TestBoundFacetMayRestateBaseBound covers "maxExclusive valid restriction"
// clause 1 and its three siblings: it is an error only if the declared bound is
// *greater than* the base's, so re-stating a bound with the same value is
// legal.
//
// The value-space check that runs alongside those clauses used to validate the
// facet value against the base outright, bounding facets included. A bound
// equal to its parent's then failed the parent's own strict comparison — the
// facet contradicted itself — and d3_4_28v09 was rejected. The value space is
// the datatype's; how a bound relates to the base's bounds is the separate
// clause with the separate comparison.
func TestBoundFacetMayRestateBaseBound(t *testing.T) {
	// Re-stating maxExclusive and minExclusive identically: legal.
	mustLoadOK(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="a">
	    <xs:restriction base="xs:int">
	      <xs:maxExclusive value="100"/>
	      <xs:minExclusive value="0"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="b">
	    <xs:restriction base="a">
	      <xs:maxExclusive value="100"/>
	      <xs:minExclusive value="0"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`)

	// The same for the inclusive pair, whose endpoints are in the value
	// space of the base as restricted.
	mustLoadOK(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="a">
	    <xs:restriction base="xs:int">
	      <xs:maxInclusive value="100"/>
	      <xs:minInclusive value="0"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="b">
	    <xs:restriction base="a">
	      <xs:maxInclusive value="100"/>
	      <xs:minInclusive value="0"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`)

	// Widening is still an error: the clause the value-space check must not
	// duplicate is the one that catches this.
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="a">
	    <xs:restriction base="xs:int">
	      <xs:maxExclusive value="100"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="b">
	    <xs:restriction base="a">
	      <xs:maxExclusive value="101"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`, "maxExclusive-valid-restriction")

	// A value outside the base's value space is still an error: that is
	// what the check is for.
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="b">
	    <xs:restriction base="xs:int">
	      <xs:maxExclusive value="notanint"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`, "maxExclusive-valid-restriction")
}
