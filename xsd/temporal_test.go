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

// TestDurationOrderUsesReferenceDateTimes covers the rule the spec actually
// gives, which is not the one the components suggest.
//
// Part 2 §3.2.6.2 orders two durations by adding both to four reference
// dateTimes and requiring the same answer from all four. Comparing the month
// and second components and demanding they agree is a different and stricter
// test: P1973Y12M29DT05H47M26S has fewer months but more seconds than
// P1979Y05M22DT21H16M00S, so the components disagree — yet 65 months outweighs
// the six-day difference against every reference, so the durations *are*
// ordered, and treating them as incomparable let a bound accept a value it
// should have rejected.
func TestDurationOrderUsesReferenceDateTimes(t *testing.T) {
	a, ok := parseDuration("P1973Y12M29DT05H47M26S")
	if !ok {
		t.Fatal("the first duration did not parse")
	}
	b, ok := parseDuration("P1979Y05M22DT21H16M00S")
	if !ok {
		t.Fatal("the second duration did not parse")
	}
	got, comparable := compareDuration(a, b)
	if !comparable {
		t.Fatal("the components disagree but the references do not; " +
			"these durations are ordered")
	}
	if got != -1 {
		t.Errorf("compare = %d, want -1", got)
	}

	// The canonical incomparable pair still is: a month is between 28 and
	// 31 days, so the references genuinely disagree.
	m, _ := parseDuration("P1M")
	d30, _ := parseDuration("P30D")
	if _, ok := compareDuration(m, d30); ok {
		t.Error("P1M and P30D should remain incomparable")
	}

	// Leap years are covered by the reference set: P1Y against P365D is
	// indeterminate for the same reason.
	y, _ := parseDuration("P1Y")
	d365, _ := parseDuration("P365D")
	if _, ok := compareDuration(y, d365); ok {
		t.Error("P1Y and P365D should be incomparable across a leap year")
	}
}
