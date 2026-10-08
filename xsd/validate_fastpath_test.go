package xsd

import "testing"

// TestBoundsSkippedWithoutBoundFacets pins checkBounds' early return: a chain
// with no min/max facet is never compared, so validating an xs:integer does
// not parse it into a big.Rat. The allocation bound is what fails if the parse
// comes back; the table is what fails if the early return swallows a bound.
func TestBoundsSkippedWithoutBoundFacets(t *testing.T) {
	integer := BuiltinType("integer")
	if hasBoundFacet(facetChain(integer)) {
		t.Fatal("xs:integer carries no bound facet")
	}
	allocs := testing.AllocsPerRun(100, func() {
		if _, err := validateAtomicValueVersion("123456789012", integer, Version10); err != nil {
			t.Fatal(err)
		}
	})
	// 23 with both rational parses (bounds, then the fractionDigits="0"
	// every xs:integer carries); what remains is the rest of the check.
	if allocs > 1 {
		t.Errorf("validating an xs:integer allocated %v times, want at most 1", allocs)
	}

	for _, c := range []struct {
		typ, value string
		valid      bool
	}{
		{"byte", "127", true},
		{"byte", "128", false},
		{"unsignedLong", "18446744073709551615", true},
		{"unsignedLong", "18446744073709551616", false},
		{"nonNegativeInteger", "-1", false},
		{"integer", "-99999999999999999999", true},
	} {
		_, err := validateAtomicValueVersion(c.value, BuiltinType(c.typ), Version10)
		if (err == nil) != c.valid {
			t.Errorf("%s %q: err=%v, want valid=%v", c.typ, c.value, err, c.valid)
		}
	}

	// User-declared bounds on the other primitives checkBounds dispatches to.
	schema := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="r"><xs:complexType><xs:sequence>
	    <xs:element name="d" minOccurs="0"><xs:simpleType><xs:restriction base="xs:double">
	      <xs:maxInclusive value="100"/></xs:restriction></xs:simpleType></xs:element>
	    <xs:element name="t" minOccurs="0"><xs:simpleType><xs:restriction base="xs:date">
	      <xs:minInclusive value="2000-01-01"/></xs:restriction></xs:simpleType></xs:element>
	    <xs:element name="p" minOccurs="0"><xs:simpleType><xs:restriction base="xs:duration">
	      <xs:maxExclusive value="P1D"/></xs:restriction></xs:simpleType></xs:element>
	  </xs:sequence></xs:complexType></xs:element>
	</xs:schema>`
	assertValid(t, schema, `<r><d>100</d><t>2000-01-01</t><p>PT23H</p></r>`)
	assertInvalid(t, schema, `<r><d>INF</d></r>`, "maxInclusive")
	assertInvalid(t, schema, `<r><d>NaN</d></r>`, "maxInclusive")
	assertInvalid(t, schema, `<r><t>1999-12-31</t></r>`, "minInclusive")
	assertInvalid(t, schema, `<r><p>PT24H</p></r>`, "maxExclusive")
}

// TestCollapseFastPath pins Normalize's early return for a value that is
// already collapsed. It must return exactly what building the copy returns —
// including U+FFFD for invalid UTF-8, which the copy produces — and it must not
// allocate when it applies.
func TestCollapseFastPath(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"a", "a"},
		{"a b c", "a b c"},
		{"é ü", "é ü"},
		{" ", ""},
		{" a", "a"},
		{"a ", "a"},
		{"a  b", "a b"},
		{"a\tb", "a b"},
		{"a\nb", "a b"},
		{"a\rb", "a b"},
		{" a ", " a "},
		{"a\xffb", "a�b"},
		{"\xff", "�"},
	} {
		if got := WhiteCollapse.Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		_ = WhiteCollapse.Normalize("already collapsed value")
	})
	if allocs != 0 {
		t.Errorf("collapsing a collapsed value allocated %v times, want 0", allocs)
	}
}
