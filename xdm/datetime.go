package xdm

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// DateTime represents xs:date, xs:time and xs:dateTime.
//
// It is not time.Time. XML Schema dates carry three properties that
// time.Time cannot express: an optional timezone (distinct from UTC — an
// unzoned date is a different value from a UTC one), a year range that exceeds
// int64 nanoseconds, and second values with arbitrary fractional precision.
// Comparison of unzoned values against zoned ones is defined against an
// implicit timezone supplied by the dynamic context, which only works if
// "absent" is representable.
type DateTime struct {
	Year   int      // proleptic Gregorian; negative for BCE. No year zero.
	Month  int      // 1-12
	Day    int      // 1-31
	Hour   int      // 0-24 (24 only as the lexical form 24:00:00)
	Minute int      // 0-59
	Second *big.Rat // seconds including fraction, [0,60)

	// TZOffset is the timezone offset in minutes east of UTC.
	// HasTZ distinguishes "no timezone" from "+00:00", which are different
	// values under XML Schema equality.
	TZOffset int
	HasTZ    bool
}

// ParseDateTime parses the lexical form of xs:date, xs:time or xs:dateTime
// according to the requested type.
func ParseDateTime(s string, t TypeCode) (*DateTime, error) {
	s = strings.TrimSpace(s)
	dt := &DateTime{Second: new(big.Rat)}
	var rest string
	var err error

	switch t {
	case TypeDate:
		rest, err = dt.parseDatePart(s)
		if err != nil {
			return nil, err
		}
	case TypeTime:
		dt.Year, dt.Month, dt.Day = 1972, 12, 31 // reference date for xs:time
		rest, err = dt.parseTimePart(s)
		if err != nil {
			return nil, err
		}
	case TypeDateTime:
		var after string
		after, err = dt.parseDatePart(s)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(after, "T") {
			return nil, ErrCast("invalid xs:dateTime %q: expected 'T' separator", s)
		}
		rest, err = dt.parseTimePart(after[1:])
		if err != nil {
			return nil, err
		}
	default:
		return nil, ErrCast("not a date/time type: %s", t)
	}

	if err := dt.parseTZ(rest); err != nil {
		return nil, err
	}
	if !dt.valid() {
		return nil, ErrCast("invalid date/time value %q", s)
	}
	dt.normalizeHour24(t)
	return dt, nil
}

// normalizeHour24 rewrites the lexical form 24:00:00 to 00:00:00 on the
// following day.
//
// XML Schema admits 24:00:00 as a way of writing midnight at the *end* of a
// day, but defines it to denote the same instant as 00:00:00 on the next one.
// Keeping the hour as 24 leaves a value that compares and serialises
// differently from an equal value written the ordinary way, so the two forms
// are collapsed at the point of parsing rather than everywhere they are used.
//
// For xs:time there is no date to carry, so only the hour is reset — which is
// correct, since a time has no day for the rollover to land on.
func (dt *DateTime) normalizeHour24(t TypeCode) {
	if dt.Hour != 24 {
		return
	}
	dt.Hour = 0
	// Decided by the type, not by whether the date happens to be zero:
	// parsing an xs:time fills in the reference date 1972-12-31, so the zero
	// test never fired for one and the day rolled forward. The hour came out
	// right and the instant was a day late, which made
	// xs:time('24:00:00') - xs:time('23:59:59') report PT1S instead of
	// -PT23H59M59S.
	if t == TypeTime {
		return
	}
	if dt.Year == 0 && dt.Month == 0 && dt.Day == 0 {
		return
	}
	dt.Day++
	if dt.Day > daysInMonth(dt.Year, dt.Month) {
		dt.Day = 1
		dt.Month++
		if dt.Month > 12 {
			dt.Month = 1
			dt.Year++
			// There is no year 0 in the proleptic Gregorian calendar used
			// here, so -0001-12-31T24:00:00 becomes 0001-01-01T00:00:00.
			if dt.Year == 0 {
				dt.Year = 1
			}
		}
	}
}

// maxYear bounds the year so that daysFromCivil's arithmetic stays inside
// int64.
//
// It is deliberately far wider than any year a document will carry. An earlier
// version set it at 1e9 to keep the *second* count representable, but that
// made xs:date("25252734927766555-07-28") a parse error where the suite
// expects the value to parse and the later arithmetic to overflow — a
// different error, at a different stage. ToSeconds now multiplies in big.Int,
// so the second count no longer constrains this at all.
// It is the exact point at which daysFromCivil's day count leaves int64:
// y*146097/400 must stay representable. The suite's overflow cases sit on
// either side of it deliberately.
const maxYear = 25252734927766554

// minYear is the corresponding bound below zero. It is not -maxYear: the era
// division in daysFromCivil rounds toward negative infinity, which costs about
// 1970 years on that side.
const minYear = -25252734927764584

// parseDatePart consumes [-]YYYY-MM-DD and returns the remainder.
func (dt *DateTime) parseDatePart(s string) (string, error) {
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	// Year is at least 4 digits and may be longer.
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < 4 {
		return "", ErrCast("invalid year: need at least 4 digits")
	}
	// A year longer than four digits is legal — 12004 is a year — but it must
	// not be zero-padded to get there: "02004" is not a valid lexical form,
	// because the grammar allows extra digits only for years that need them.
	if i > 4 && s[0] == '0' {
		return "", ErrCast("invalid year %q: leading zeros are not permitted", s[:i])
	}
	y, err := strconv.Atoi(s[:i])
	if err != nil {
		// A year too large for an int is an overflow, not a malformed
		// lexical form: the digits are well written and there are simply too
		// many of them. FODT0001 is the code for a date value out of range,
		// where FORG0001 would claim the string was not a date at all.
		if errors.Is(err, strconv.ErrRange) {
			return "", Errorf("FODT0001", "year %q is out of range", s[:i])
		}
		return "", ErrCast("invalid year: %v", err)
	}
	if y == 0 {
		return "", ErrCast("year 0000 is not a valid xs:date year")
	}
	// The year has to stay in the range daysFromCivil can convert without its
	// own int64 arithmetic wrapping. ToSeconds multiplies in big.Int, so the
	// second count is no longer the binding constraint; this bound is what is
	// left, and it is far wider than any year a document will carry.
	if neg {
		y = -y
	}
	// FODT0001 is the overflow code. This is reported at parse rather than at
	// the arithmetic that would overflow, because every operation on such a
	// value — comparison, subtraction, timezone normalisation — goes through
	// the same day count, so there is nothing useful the value could still do.
	//
	// The check is applied after the sign, because the two directions do not
	// have the same limit: the era division rounds toward negative infinity,
	// so the negative side runs out about 1970 years sooner. An earlier
	// version of this comment said "one year sooner", which was a guess; the
	// two bounds below were found by bisecting daysFromCivil itself, and
	// TestYearBoundIsExact pins them.
	if y > maxYear || y < minYear {
		return "", Errorf("FODT0001", "year %d overflows the date range", y)
	}
	dt.Year = y
	s = s[i:]
	if len(s) < 6 || s[0] != '-' || s[3] != '-' {
		return "", ErrCast("invalid date: expected -MM-DD")
	}
	if dt.Month, err = atoiDigits(s[1:3]); err != nil {
		return "", ErrCast("invalid month: %v", err)
	}
	if dt.Day, err = atoiDigits(s[4:6]); err != nil {
		return "", ErrCast("invalid day: %v", err)
	}
	return s[6:], nil
}

// parseTimePart consumes HH:MM:SS[.sss] and returns the remainder.
func (dt *DateTime) parseTimePart(s string) (string, error) {
	if len(s) < 8 || s[2] != ':' || s[5] != ':' {
		return "", ErrCast("invalid time: expected HH:MM:SS")
	}
	var err error
	if dt.Hour, err = atoiDigits(s[0:2]); err != nil {
		return "", ErrCast("invalid hour: %v", err)
	}
	if dt.Minute, err = atoiDigits(s[3:5]); err != nil {
		return "", ErrCast("invalid minute: %v", err)
	}
	i := 6
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < 8 {
		return "", ErrCast("invalid seconds")
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if _, ok := dt.Second.SetString(s[6:i]); !ok {
		return "", ErrCast("invalid seconds %q", s[6:i])
	}
	return s[i:], nil
}

// parseTZ consumes Z, +HH:MM or -HH:MM, or nothing.
func (dt *DateTime) parseTZ(s string) error {
	if s == "" {
		dt.HasTZ = false
		return nil
	}
	if s == "Z" {
		dt.HasTZ, dt.TZOffset = true, 0
		return nil
	}
	if len(s) != 6 || (s[0] != '+' && s[0] != '-') || s[3] != ':' {
		return ErrCast("invalid timezone %q", s)
	}
	h, err := atoiDigits(s[1:3])
	if err != nil {
		return ErrCast("invalid timezone hour: %v", err)
	}
	m, err := atoiDigits(s[4:6])
	if err != nil {
		return ErrCast("invalid timezone minute: %v", err)
	}
	if h > 14 || m > 59 || (h == 14 && m != 0) {
		return ErrCast("timezone offset out of range: %q", s)
	}
	off := h*60 + m
	if s[0] == '-' {
		off = -off
	}
	dt.HasTZ, dt.TZOffset = true, off
	return nil
}

func (dt *DateTime) valid() bool {
	if dt.Month < 1 || dt.Month > 12 {
		return false
	}
	if dt.Day < 1 || dt.Day > daysInMonth(dt.Year, dt.Month) {
		return false
	}
	if dt.Hour == 24 {
		// 24:00:00 is legal and means midnight the next day.
		if dt.Minute != 0 || dt.Second.Sign() != 0 {
			return false
		}
	} else if dt.Hour < 0 || dt.Hour > 23 {
		return false
	}
	if dt.Minute < 0 || dt.Minute > 59 {
		return false
	}
	sixty := big.NewRat(60, 1)
	if dt.Second.Sign() < 0 || dt.Second.Cmp(sixty) >= 0 {
		return false
	}
	return true
}

func daysInMonth(y, m int) int {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(y) {
			return 29
		}
		return 28
	}
	return 0
}

func isLeap(y int) bool {
	if y < 0 {
		y++ // no year zero: 1 BCE is a leap year
	}
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

// Lexical returns the canonical lexical form for the given type.
func (dt *DateTime) Lexical(t TypeCode) string {
	var sb strings.Builder
	writeDate := func() {
		if dt.Year < 0 {
			sb.WriteByte('-')
			fmt.Fprintf(&sb, "%04d", -dt.Year)
		} else {
			fmt.Fprintf(&sb, "%04d", dt.Year)
		}
		fmt.Fprintf(&sb, "-%02d-%02d", dt.Month, dt.Day)
	}
	writeTime := func() {
		fmt.Fprintf(&sb, "%02d:%02d:", dt.Hour, dt.Minute)
		sb.WriteString(formatSeconds(dt.Second))
	}
	switch t {
	case TypeDate:
		writeDate()
	case TypeTime:
		writeTime()
	case TypeDateTime:
		writeDate()
		sb.WriteByte('T')
		writeTime()
	}
	if dt.HasTZ {
		sb.WriteString(formatTZ(dt.TZOffset))
	}
	return sb.String()
}

// formatSeconds renders seconds with a leading zero and no trailing fraction
// zeros, which is the canonical form.
func formatSeconds(sec *big.Rat) string {
	if sec.IsInt() {
		return fmt.Sprintf("%02d", sec.Num().Int64())
	}
	s := sec.FloatString(decimalScale(sec))
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if i := strings.IndexByte(s, '.'); i == 1 {
		s = "0" + s
	} else if i < 0 && len(s) == 1 {
		s = "0" + s
	}
	return s
}

func formatTZ(off int) string {
	if off == 0 {
		return "Z"
	}
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	return fmt.Sprintf("%s%02d:%02d", sign, off/60, off%60)
}

// ToSeconds returns the value as seconds since 1972-12-31T00:00:00Z, adjusted
// to UTC using implicitTZ (in minutes) when the value carries no timezone.
//
// Comparison and subtraction are defined on this normalised timeline, so
// having one conversion point means the timezone rules are applied uniformly
// rather than re-derived at each comparison site.
func (dt *DateTime) ToSeconds(implicitTZ int) *big.Rat {
	days := daysFromCivil(dt.Year, dt.Month, dt.Day)
	// days*86400 overflows int64 for a year past about 2.9e11, so the
	// multiplication is done in big.Int. Doing it in int64 wrapped, and a
	// value that parsed then compared and subtracted as garbage.
	total := new(big.Rat).SetInt(
		new(big.Int).Mul(big.NewInt(int64(days)), big.NewInt(86400)))
	total.Add(total, new(big.Rat).SetInt64(int64(dt.Hour)*3600+int64(dt.Minute)*60))
	total.Add(total, dt.Second)
	tz := implicitTZ
	if dt.HasTZ {
		tz = dt.TZOffset
	}
	total.Sub(total, new(big.Rat).SetInt64(int64(tz)*60))
	return total
}

// daysFromCivil converts a proleptic Gregorian date to a day number relative to
// 1970-01-01. Howard Hinnant's civil-from-days algorithm: branch-free and
// correct for the full year range, unlike repeated leap-year loops.
func daysFromCivil(y, m, d int) int {
	if m <= 2 {
		y--
	}
	era := y
	if y < 0 {
		era = y - 399
	}
	era /= 400
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// CompareDT orders two date/time values on the normalised timeline.
func CompareDT(a, b *DateTime, implicitTZ int) int {
	return a.ToSeconds(implicitTZ).Cmp(b.ToSeconds(implicitTZ))
}

// atoiDigits parses a fixed-width field that must be digits and nothing else.
//
// strconv.Atoi accepts a leading sign, which every one of these fields
// forbids: "11:+1:11" parsed as 11:01:11 and "1111-+1-11" as 1111-01-11,
// silently reading a malformed value as a valid one.
func atoiDigits(s string) (int, error) {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrCast("invalid value %q: expected digits", s)
		}
	}
	return strconv.Atoi(s)
}
