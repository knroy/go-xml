package xdm

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// The five Gregorian types denote a recurring or partial point on the
// calendar: xs:gYear is a whole year, xs:gMonthDay is a date whose year is
// unspecified (a birthday), and so on.
//
// They reuse the DateTime representation, with the unspecified components left
// at their zero values and the type code recording which parts are meaningful.
// Giving each its own struct would duplicate the timezone handling, which is
// the only genuinely fiddly part.

// ParseGregorian parses the lexical form of one of the five Gregorian types.
//
// Each has its own leading-hyphen convention — "--01" is a month, "---15" a
// day — which exists so that the forms cannot be confused with a truncated
// date. Getting the hyphen count wrong silently reinterprets the value, so
// each form is matched exactly rather than by a permissive scan.
func ParseGregorian(s string, t TypeCode) (*DateTime, error) {
	s = strings.TrimSpace(s)
	dt := &DateTime{Second: new(big.Rat), Month: 1, Day: 1}

	var rest string
	var err error

	switch t {
	case TypeGYear:
		rest, err = parseGYear(s, dt)
	case TypeGYearMonth:
		rest, err = parseGYearMonth(s, dt)
	case TypeGMonth:
		rest, err = parseFixed(s, "--", 2, dt, func(v int) { dt.Month = v })
	case TypeGMonthDay:
		rest, err = parseGMonthDay(s, dt)
	case TypeGDay:
		rest, err = parseFixed(s, "---", 2, dt, func(v int) { dt.Day = v })
	default:
		return nil, ErrCast("not a Gregorian type: %s", t)
	}
	if err != nil {
		return nil, err
	}
	if err := dt.parseTZ(rest); err != nil {
		return nil, err
	}
	if err := validateGregorian(dt, t); err != nil {
		return nil, err
	}
	return dt, nil
}

func parseGYear(s string, dt *DateTime) (string, error) {
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < 4 {
		return "", ErrCast("invalid xs:gYear: the year needs at least 4 digits")
	}
	// Extra digits are allowed only for years that need them; "02004" is not
	// a valid lexical form. See parseDatePart for the same rule.
	if i > 4 && s[0] == '0' {
		return "", ErrCast("invalid xs:gYear %q: leading zeros are not permitted", s[:i])
	}
	y, err := strconv.Atoi(s[:i])
	if err != nil {
		return "", ErrCast("invalid xs:gYear: %v", err)
	}
	// Year 0000 is legal in XSD 1.1 — it denotes 1 BCE — where 1.0 excluded
	// it. The suite tests against 1.1.

	if neg {
		y = -y
	}
	dt.Year = y
	return s[i:], nil
}

func parseGYearMonth(s string, dt *DateTime) (string, error) {
	rest, err := parseGYear(s, dt)
	if err != nil {
		return "", ErrCast("invalid xs:gYearMonth: %v", err)
	}
	if len(rest) < 3 || rest[0] != '-' {
		return "", ErrCast("invalid xs:gYearMonth: expected -MM after the year")
	}
	m, err := strconv.Atoi(rest[1:3])
	if err != nil {
		return "", ErrCast("invalid xs:gYearMonth month: %v", err)
	}
	dt.Month = m
	return rest[3:], nil
}

func parseGMonthDay(s string, dt *DateTime) (string, error) {
	if !strings.HasPrefix(s, "--") || len(s) < 7 || s[4] != '-' {
		return "", ErrCast("invalid xs:gMonthDay: expected --MM-DD")
	}
	m, err := strconv.Atoi(s[2:4])
	if err != nil {
		return "", ErrCast("invalid xs:gMonthDay month: %v", err)
	}
	d, err := strconv.Atoi(s[5:7])
	if err != nil {
		return "", ErrCast("invalid xs:gMonthDay day: %v", err)
	}
	dt.Month, dt.Day = m, d
	return s[7:], nil
}

// parseFixed reads a form that is a fixed prefix followed by n digits.
func parseFixed(s, prefix string, n int, dt *DateTime, set func(int)) (string, error) {
	if !strings.HasPrefix(s, prefix) || len(s) < len(prefix)+n {
		return "", ErrCast("invalid value %q: expected %s followed by %d digits",
			s, prefix, n)
	}
	body := s[len(prefix) : len(prefix)+n]
	v, err := strconv.Atoi(body)
	if err != nil {
		return "", ErrCast("invalid value %q: %v", s, err)
	}
	set(v)
	return s[len(prefix)+n:], nil
}

func validateGregorian(dt *DateTime, t TypeCode) error {
	if t == TypeGYearMonth || t == TypeGMonth || t == TypeGMonthDay {
		if dt.Month < 1 || dt.Month > 12 {
			return ErrCast("month %d is out of range", dt.Month)
		}
	}
	if t == TypeGDay {
		if dt.Day < 1 || dt.Day > 31 {
			return ErrCast("day %d is out of range", dt.Day)
		}
	}
	if t == TypeGMonthDay {
		// The year is unspecified, so February is validated against a leap
		// year: --02-29 is a legal gMonthDay even though 2023-02-29 is not.
		if dt.Day < 1 || dt.Day > daysInMonth(2000, dt.Month) {
			return ErrCast("day %d is out of range for month %d", dt.Day, dt.Month)
		}
	}
	return nil
}

// LexicalGregorian returns the canonical lexical form.
func LexicalGregorian(dt *DateTime, t TypeCode) string {
	var sb strings.Builder
	writeYear := func() {
		if dt.Year < 0 {
			fmt.Fprintf(&sb, "-%04d", -dt.Year)
			return
		}
		fmt.Fprintf(&sb, "%04d", dt.Year)
	}
	switch t {
	case TypeGYear:
		writeYear()
	case TypeGYearMonth:
		writeYear()
		fmt.Fprintf(&sb, "-%02d", dt.Month)
	case TypeGMonth:
		fmt.Fprintf(&sb, "--%02d", dt.Month)
	case TypeGMonthDay:
		fmt.Fprintf(&sb, "--%02d-%02d", dt.Month, dt.Day)
	case TypeGDay:
		fmt.Fprintf(&sb, "---%02d", dt.Day)
	}
	if dt.HasTZ {
		sb.WriteString(formatTZ(dt.TZOffset))
	}
	return sb.String()
}

// IsGregorian reports whether t is one of the five Gregorian types.
func IsGregorian(t TypeCode) bool {
	switch t {
	case TypeGYear, TypeGYearMonth, TypeGMonth, TypeGMonthDay, TypeGDay:
		return true
	}
	return false
}
