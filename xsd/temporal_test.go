package xsd

import "testing"

func TestTemporalOrdering(t *testing.T) {
	cases := []struct {
		a, b, prim string
		want       int
		comparable bool
	}{
		{"2024-01-01", "2024-01-02", "date", -1, true},
		{"2024-01-02", "2024-01-01", "date", 1, true},
		{"2024-01-01", "2024-01-01", "date", 0, true},

		// A timezone shifts the instant: +05:00 is ahead of UTC, so the
		// same wall clock is an earlier moment.
		{"2024-01-01T00:00:00Z", "2024-01-01T00:00:00+05:00", "dateTime", 1, true},
		{"2024-01-01T00:00:00-05:00", "2024-01-01T00:00:00Z", "dateTime", 1, true},

		// Years before the epoch, where the era division must round
		// toward negative infinity.
		{"-0001-01-01", "0001-01-01", "date", -1, true},
	}
	for _, c := range cases {
		a, ok := parseTemporal(c.a, c.prim)
		if !ok {
			t.Errorf("parseTemporal(%q) failed", c.a)
			continue
		}
		b, ok := parseTemporal(c.b, c.prim)
		if !ok {
			t.Errorf("parseTemporal(%q) failed", c.b)
			continue
		}
		got, cmp := compareTemporal(a, b)
		if cmp != c.comparable {
			t.Errorf("compare(%q, %q) comparable=%v, want %v",
				c.a, c.b, cmp, c.comparable)
			continue
		}
		if cmp && got != c.want {
			t.Errorf("compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestTemporalPartialOrder covers the rule that a value with a timezone and one
// without are only ordered when every timezone the absent one could stand for
// gives the same answer.
//
// Treating "indeterminate" as a failure would reject values the spec leaves
// undetermined, so the comparison has to report it rather than guess.
func TestTemporalPartialOrder(t *testing.T) {
	// These are far enough apart that the ±14 hour interval cannot span
	// the difference, so they are ordered despite one lacking a timezone.
	a, _ := parseTemporal("2024-01-01T00:00:00Z", "dateTime")
	b, _ := parseTemporal("2024-06-01T00:00:00", "dateTime")
	if got, ok := compareTemporal(a, b); !ok || got != -1 {
		t.Errorf("distant values should be ordered, got %d ok=%v", got, ok)
	}

	// These are within the interval, so the order is indeterminate.
	c, _ := parseTemporal("2024-01-01T00:00:00Z", "dateTime")
	d, _ := parseTemporal("2024-01-01T06:00:00", "dateTime")
	if _, ok := compareTemporal(c, d); ok {
		t.Error("values within the timezone interval should be incomparable")
	}
}

// TestDurationPartialOrder covers the fact that a month has no fixed length, so
// P1M and P30D have no order at all. Comparing through an "average month" would
// invent an order the spec does not have.
func TestDurationPartialOrder(t *testing.T) {
	m, ok := parseDuration("P1M")
	if !ok {
		t.Fatal("P1M did not parse")
	}
	d30, ok := parseDuration("P30D")
	if !ok {
		t.Fatal("P30D did not parse")
	}
	if _, ok := compareDuration(m, d30); ok {
		t.Error("P1M and P30D should be incomparable")
	}

	// Two durations differing in one component only are ordered.
	d1, _ := parseDuration("P1D")
	d2, _ := parseDuration("P2D")
	if got, ok := compareDuration(d1, d2); !ok || got != -1 {
		t.Errorf("P1D < P2D: got %d ok=%v", got, ok)
	}
	y1, _ := parseDuration("P1Y")
	y2, _ := parseDuration("P2Y")
	if got, ok := compareDuration(y1, y2); !ok || got != -1 {
		t.Errorf("P1Y < P2Y: got %d ok=%v", got, ok)
	}
}

func TestValidateTemporalBounds(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="d">
	    <xs:restriction base="xs:date">
	      <xs:minInclusive value="2024-01-01"/>
	      <xs:maxInclusive value="2024-12-31"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:element name="root" type="d"/>
	</xs:schema>`

	assertValid(t, schema, `<root>2024-06-15</root>`)
	assertValid(t, schema, `<root>2024-01-01</root>`)
	assertInvalid(t, schema, `<root>2023-12-31</root>`, "minInclusive")
	assertInvalid(t, schema, `<root>2025-01-01</root>`, "maxInclusive")
}

func TestValidateDurationBounds(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="d">
	    <xs:restriction base="xs:duration">
	      <xs:minInclusive value="P1D"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:element name="root" type="d"/>
	</xs:schema>`

	assertValid(t, schema, `<root>P2D</root>`)
	assertInvalid(t, schema, `<root>PT1H</root>`, "minInclusive")
}
