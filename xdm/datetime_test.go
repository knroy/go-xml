package xdm

import "testing"

func TestParseDateTimeRoundTrip(t *testing.T) {
	cases := []struct {
		in   string
		typ  TypeCode
		want string // canonical form, "" means same as input
	}{
		{"2024-02-29", TypeDate, ""}, // leap year
		{"2024-01-15Z", TypeDate, ""},
		{"2024-01-15+05:30", TypeDate, ""},
		{"-0044-03-15", TypeDate, ""}, // BCE
		{"12:30:00", TypeTime, ""},
		{"12:30:45.5", TypeTime, ""},
		{"2024-01-15T12:30:45Z", TypeDateTime, ""},
		{"2024-01-15T12:30:45.250Z", TypeDateTime, "2024-01-15T12:30:45.25Z"},
	}
	for _, c := range cases {
		dt, err := ParseDateTime(c.in, c.typ)
		if err != nil {
			t.Errorf("ParseDateTime(%q, %v): %v", c.in, c.typ, err)
			continue
		}
		want := c.want
		if want == "" {
			want = c.in
		}
		if got := dt.Lexical(c.typ); got != want {
			t.Errorf("ParseDateTime(%q).Lexical() = %q, want %q", c.in, got, want)
		}
	}
}

func TestParseDateTimeRejectsInvalid(t *testing.T) {
	cases := []struct {
		in  string
		typ TypeCode
	}{
		{"2023-02-29", TypeDate},                    // not a leap year
		{"2024-13-01", TypeDate},                    // month 13
		{"2024-00-01", TypeDate},                    // month 0
		{"0000-01-01", TypeDate},                    // year zero does not exist
		{"2024-01-15", TypeDateTime},                // missing time part
		{"25:00:00", TypeTime},                      // hour out of range
		{"12:60:00", TypeTime},                      // minute out of range
		{"2024-01-15T12:30:45+15:00", TypeDateTime}, // tz beyond +14:00
	}
	for _, c := range cases {
		if _, err := ParseDateTime(c.in, c.typ); err == nil {
			t.Errorf("ParseDateTime(%q, %v) succeeded; want error", c.in, c.typ)
		}
	}
}

func TestUnzonedIsDistinctFromUTC(t *testing.T) {
	// The whole reason DateTime is not time.Time: "no timezone" and "+00:00"
	// are different values.
	a, err := ParseDateTime("2024-01-15", TypeDate)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseDateTime("2024-01-15Z", TypeDate)
	if err != nil {
		t.Fatal(err)
	}
	if a.HasTZ {
		t.Error("unzoned date reported HasTZ")
	}
	if !b.HasTZ {
		t.Error("Z-suffixed date did not report HasTZ")
	}
	if a.Lexical(TypeDate) == b.Lexical(TypeDate) {
		t.Error("unzoned and UTC dates serialise identically")
	}
}

func TestDateTimeOrdering(t *testing.T) {
	// Timezone must be applied before comparison: 12:00Z is earlier than
	// 12:00-05:00 (which is 17:00Z).
	a, _ := ParseDateTime("2024-01-15T12:00:00Z", TypeDateTime)
	b, _ := ParseDateTime("2024-01-15T12:00:00-05:00", TypeDateTime)
	if CompareDT(a, b, 0) >= 0 {
		t.Error("12:00Z should sort before 12:00-05:00")
	}
}

func TestDaysFromCivil(t *testing.T) {
	if got := daysFromCivil(1970, 1, 1); got != 0 {
		t.Errorf("epoch = %d, want 0", got)
	}
	if got := daysFromCivil(1970, 1, 2); got != 1 {
		t.Errorf("day after epoch = %d, want 1", got)
	}
	if got := daysFromCivil(1969, 12, 31); got != -1 {
		t.Errorf("day before epoch = %d, want -1", got)
	}
	// 2000 is a leap year (divisible by 400); 1900 is not (divisible by 100).
	if daysFromCivil(2000, 3, 1)-daysFromCivil(2000, 2, 28) != 2 {
		t.Error("2000 should have a Feb 29")
	}
	if daysFromCivil(1900, 3, 1)-daysFromCivil(1900, 2, 28) != 1 {
		t.Error("1900 should not have a Feb 29")
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		typ  TypeCode
		want string
	}{
		{"P1Y2M", TypeYearMonthDuration, "P1Y2M"},
		{"P14M", TypeYearMonthDuration, "P1Y2M"}, // normalises to years+months
		{"-P1Y", TypeYearMonthDuration, "-P1Y"},
		{"P0M", TypeYearMonthDuration, "P0M"},
		{"P1D", TypeDayTimeDuration, "P1D"},
		{"PT1H30M", TypeDayTimeDuration, "PT1H30M"},
		{"PT90M", TypeDayTimeDuration, "PT1H30M"}, // normalises
		{"P1DT2H3M4S", TypeDayTimeDuration, "P1DT2H3M4S"},
		{"PT0S", TypeDayTimeDuration, "PT0S"},
	}
	for _, c := range cases {
		d, err := ParseDuration(c.in, c.typ)
		if err != nil {
			t.Errorf("ParseDuration(%q): %v", c.in, err)
			continue
		}
		if got := d.Lexical(c.typ); got != c.want {
			t.Errorf("ParseDuration(%q).Lexical() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDurationSubtypeConstraints(t *testing.T) {
	// The subtypes exist precisely because months and seconds are not
	// interconvertible; each must reject the other's components.
	if _, err := ParseDuration("P1Y1D", TypeYearMonthDuration); err == nil {
		t.Error("yearMonthDuration accepted a day component")
	}
	if _, err := ParseDuration("P1Y1D", TypeDayTimeDuration); err == nil {
		t.Error("dayTimeDuration accepted a year component")
	}
	// But plain xs:duration accepts both.
	if _, err := ParseDuration("P1Y1D", TypeDuration); err != nil {
		t.Errorf("xs:duration rejected P1Y1D: %v", err)
	}
}

func TestParseDurationRejectsInvalid(t *testing.T) {
	for _, in := range []string{"1Y", "P", "PT", "P1H", "P-1Y", ""} {
		if _, err := ParseDuration(in, TypeDuration); err == nil {
			t.Errorf("ParseDuration(%q) succeeded; want error", in)
		}
	}
}

// XML Schema admits 24:00:00 as midnight at the end of a day, but defines it
// to denote the same instant as 00:00:00 on the next one. Leaving the hour at
// 24 produced a value that serialised differently from an equal value written
// the ordinary way. Found by the W3C QT3 suite; every expectation here matches
// Saxon-HE 12.4.
func TestHour24Normalizes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1999-12-31T24:00:00", "2000-01-01T00:00:00"},
		// Into a leap day, and past a February that has none.
		{"2000-02-28T24:00:00", "2000-02-29T00:00:00"},
		{"2001-02-28T24:00:00", "2001-03-01T00:00:00"},
		// A timezone is carried through the rollover unchanged.
		{"2000-12-31T24:00:00Z", "2001-01-01T00:00:00Z"},
		// An ordinary value must be untouched.
		{"2000-01-01T00:00:00", "2000-01-01T00:00:00"},
	}
	for _, c := range cases {
		dt, err := ParseDateTime(c.in, TypeDateTime)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got := dt.Lexical(TypeDateTime); got != c.want {
			t.Errorf("%s normalised to %s, want %s", c.in, got, c.want)
		}
	}
}

// An xs:time has no date for the rollover to land on, so only the hour resets.
func TestHour24TimeNormalizes(t *testing.T) {
	dt, err := ParseDateTime("24:00:00", TypeTime)
	if err != nil {
		t.Fatal(err)
	}
	if got := dt.Lexical(TypeTime); got != "00:00:00" {
		t.Errorf("24:00:00 normalised to %s, want 00:00:00", got)
	}
}

// 24:00:00 is legal only at exactly midnight.
func TestHour24RejectsNonMidnight(t *testing.T) {
	for _, s := range []string{"2000-01-01T24:00:01", "2000-01-01T24:01:00"} {
		if _, err := ParseDateTime(s, TypeDateTime); err == nil {
			t.Errorf("%s was accepted; only 24:00:00 is a legal hour 24", s)
		}
	}
}

// The year bounds are asymmetric and were found by bisecting daysFromCivil.
// They are pinned because the negative one is not -maxYear and looks like a
// typo otherwise: the era division rounds toward negative infinity, which
// costs about 1970 years on that side.
//
// One past either bound, the day count wraps — and on the negative side it
// wraps to a *positive* number, so a BCE date compared as later than the year
// 2000 rather than merely being wrong by a fixed amount.
func TestYearBoundIsExact(t *testing.T) {
	for _, c := range []struct {
		name string
		y    int
	}{
		{"maxYear", maxYear},
		{"minYear", minYear},
	} {
		d := daysFromCivil(c.y, 1, 1)
		next := daysFromCivil(c.y+1, 1, 1)
		if d >= next {
			t.Errorf("%s (%d): day count is not monotonic at the bound: %d then %d",
				c.name, c.y, d, next)
		}
		if (c.y < 0) != (d < 0) {
			t.Errorf("%s (%d): day count %d has the wrong sign", c.name, c.y, d)
		}
	}
	// One past the negative bound the count wraps to positive, which is what
	// makes it a wrong *ordering* rather than a wrong magnitude.
	if d := daysFromCivil(minYear-1, 1, 1); d > 0 {
		t.Logf("confirmed: year %d would give day count %d", minYear-1, d)
	} else {
		t.Errorf("minYear no longer marks the wrap point: got %d", d)
	}
	// And the bounds themselves must parse, while one past must not.
	for _, c := range []struct {
		lex string
		ok  bool
	}{
		{"25252734927766554-01-01", true},
		{"25252734927766555-01-01", false},
		{"-25252734927764584-01-01", true},
		{"-25252734927764585-01-01", false},
		{"2024-07-29", true},
	} {
		_, err := ParseDateTime(c.lex, TypeDate)
		if (err == nil) != c.ok {
			t.Errorf("ParseDateTime(%q): err=%v, want ok=%v", c.lex, err, c.ok)
		}
	}
}
