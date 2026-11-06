package xsd

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

// Comparison of the date, time and duration types.
//
// These need their own ordering because none of them is a number and two of
// them are only *partially* ordered. A validator that compared their lexical
// forms would get "2024-01-01Z" and "2023-12-31-05:00" backwards, and one that
// converted to a machine timestamp would lose the arbitrary-precision year and
// the distinction between an absent timezone and UTC.

// temporal is a date or time value reduced to a comparable form.
//
// The seconds are a big.Rat because the fractional part of a time is arbitrary
// precision in the lexical space — "00:00:00.000000000000001" is a legal
// xs:time — and because the year is unbounded, so the whole value cannot be
// held in an int64 of nanoseconds.
type temporal struct {
	// seconds is the value as seconds from an arbitrary epoch, with the
	// timezone already applied.
	seconds *big.Rat

	// hasTZ records whether the literal carried a timezone. Two values
	// that differ in this are not necessarily comparable, which is what
	// makes the order partial.
	hasTZ bool
}

// compareTemporal orders two date or time values of the same primitive.
//
// The second return reports whether they are comparable at all. A value with a
// timezone and one without are only comparable when the answer is the same for
// every timezone the absent one could stand for — the spec models this as the
// ±14 hour interval, and the comparison is indeterminate when the intervals
// overlap. Returning "not comparable" rather than a guess is what keeps a bound
// from rejecting a value the spec leaves undetermined.
func compareTemporal(a, b temporal) (int, bool) {
	if a.seconds == nil || b.seconds == nil {
		return 0, false
	}
	if a.hasTZ == b.hasTZ {
		return a.seconds.Cmp(b.seconds), true
	}

	// One has a timezone and one does not. The one without could be any
	// offset in ±14 hours, so compare against both extremes: the answer
	// stands only if it is the same either way.
	const tzRange = 14 * 60 * 60
	span := new(big.Rat).SetInt64(tzRange)

	var lo, hi *big.Rat
	var fixed *big.Rat
	var floatingIsA bool
	if a.hasTZ {
		fixed = a.seconds
		lo = new(big.Rat).Sub(b.seconds, span)
		hi = new(big.Rat).Add(b.seconds, span)
	} else {
		fixed = b.seconds
		lo = new(big.Rat).Sub(a.seconds, span)
		hi = new(big.Rat).Add(a.seconds, span)
		floatingIsA = true
	}

	cLo := fixed.Cmp(lo)
	cHi := fixed.Cmp(hi)
	if cLo != cHi {
		// The fixed value falls inside the interval the floating one
		// could occupy, so the order is indeterminate.
		return 0, false
	}
	if floatingIsA {
		// cLo is fixed vs a; invert to report a vs fixed.
		return -cLo, true
	}
	return cLo, true
}

// parseTemporal reduces a date or time literal to a comparable value.
//
// The primitive decides which fields are present; the missing ones take a
// canonical value, which is what makes two gMonth values comparable to each
// other without making them comparable to a date.
func parseTemporal(v, primitive string) (temporal, bool) {
	body, tzOffset, hasTZ, ok := splitTZ(v)
	if !ok {
		return temporal{}, false
	}

	year := big.NewInt(1972) // a leap year, so that --02-29 is representable
	var month int64 = 1
	var day int64 = 1
	var secs = new(big.Rat)

	switch primitive {
	case "dateTime":
		i := strings.IndexByte(body, 'T')
		if i < 0 {
			return temporal{}, false
		}
		if !parseDate(body[:i], year, &month, &day) {
			return temporal{}, false
		}
		if !parseTime(body[i+1:], secs) {
			return temporal{}, false
		}
	case "date":
		if !parseDate(body, year, &month, &day) {
			return temporal{}, false
		}
	case "time":
		if !parseTime(body, secs) {
			return temporal{}, false
		}
	case "gYear":
		if !parseYear(body, year) {
			return temporal{}, false
		}
	case "gYearMonth":
		i := strings.LastIndexByte(body, '-')
		if i <= 0 || !parseYear(body[:i], year) || !parseInt(body[i+1:], &month) {
			return temporal{}, false
		}
	case "gMonth":
		if !strings.HasPrefix(body, "--") || !parseInt(body[2:], &month) {
			return temporal{}, false
		}
	case "gDay":
		if !strings.HasPrefix(body, "---") || !parseInt(body[3:], &day) {
			return temporal{}, false
		}
	case "gMonthDay":
		if !strings.HasPrefix(body, "--") || len(body) != 7 ||
			!parseInt(body[2:4], &month) || body[4] != '-' ||
			!parseInt(body[5:], &day) {
			return temporal{}, false
		}
	default:
		return temporal{}, false
	}

	// Days from a civil date, using the era algorithm so that the year is
	// unbounded and negative years work. The Gregorian calendar repeats
	// every 400 years, which are exactly 146097 days, so the year is split
	// into whole eras and a year of era in big arithmetic and only the
	// small remainder goes through daysFromCivil. Doing it all in int64
	// wrapped days*86400 above a year of about 2.9e11, and a date in the
	// year 394573983248376 then compared equal to one in 2024.
	era, yoe := new(big.Int).DivMod(year, big.NewInt(400), new(big.Int))
	days := era.Mul(era, big.NewInt(146097))
	days.Add(days, big.NewInt(daysFromCivil(yoe.Int64(), month, day)))
	total := new(big.Rat).SetInt(days.Mul(days, big.NewInt(86400)))
	total.Add(total, secs)
	if primitive == "time" {
		// xs:time has no date, so 24:00:00 is the same time of day as
		// 00:00:00 rather than a point a day later. Leaving it at 86400
		// makes two spellings of one time compare unequal, which a key
		// sequence and an enumeration both notice.
		//
		// This is not done for dateTime, where 24:00:00 genuinely means
		// midnight starting the *next* day and the date carries it.
		total = wrapDay(total)
	}
	if hasTZ {
		// A timezone of +05:00 means the local time is ahead of UTC, so
		// the instant is *earlier*.
		total.Sub(total, new(big.Rat).SetInt64(tzOffset))
	}
	return temporal{seconds: total, hasTZ: hasTZ}, true
}

// splitTZ removes a trailing timezone and returns its offset in seconds.
func splitTZ(v string) (body string, offset int64, hasTZ, ok bool) {
	if strings.HasSuffix(v, "Z") {
		return v[:len(v)-1], 0, true, true
	}
	if len(v) >= 6 {
		tz := v[len(v)-6:]
		if (tz[0] == '+' || tz[0] == '-') && tz[3] == ':' {
			var h, m int64
			if parseInt(tz[1:3], &h) && parseInt(tz[4:], &m) {
				off := h*3600 + m*60
				if tz[0] == '-' {
					off = -off
				}
				return v[:len(v)-6], off, true, true
			}
		}
	}
	return v, 0, false, true
}

func parseDate(v string, year *big.Int, month, day *int64) bool {
	last := strings.LastIndexByte(v, '-')
	if last <= 0 {
		return false
	}
	mid := strings.LastIndexByte(v[:last], '-')
	if mid <= 0 {
		return false
	}
	return parseYear(v[:mid], year) && parseInt(v[mid+1:last], month) &&
		parseInt(v[last+1:], day)
}

// parseTime converts hh:mm:ss[.frac] to seconds.
func parseTime(v string, out *big.Rat) bool {
	if len(v) < 8 || v[2] != ':' || v[5] != ':' {
		return false
	}
	var h, m, s int64
	if !parseInt(v[:2], &h) || !parseInt(v[3:5], &m) || !parseInt(v[6:8], &s) {
		return false
	}
	out.SetInt64(h*3600 + m*60 + s)
	if len(v) > 8 {
		if v[8] != '.' {
			return false
		}
		frac, ok := new(big.Rat).SetString("0" + v[8:])
		if !ok {
			return false
		}
		out.Add(out, frac)
	}
	return true
}

// maxYearDigits is the most year digits a date or time value may have.
//
// The year is unbounded in the lexical space, but converting it is not free:
// big.Int.SetString is superlinear in the digit count, so a megabyte-long year
// cost seconds every time a facet, enumeration or key compared it. XSD 1.1
// Part 2 §5.4 (Partial Implementation of Infinite Datatypes) lets a processor
// limit the year digits it supports, provided it supports at least four. A
// thousand is far past any year a document means and keeps one conversion in
// microseconds.
const maxYearDigits = 1000

// yearDigits returns the length of the year of a date or time literal of a
// primitive with a year (0 for the others): the run of digits after an
// optional leading '-'. The literal is already in the lexical space, so that
// run is the whole year.
func yearDigits(v, primitive string) int {
	switch primitive {
	case "dateTime", "date", "gYear", "gYearMonth":
	default:
		return 0
	}
	v = strings.TrimPrefix(v, "-")
	n := 0
	for n < len(v) && v[n] >= '0' && v[n] <= '9' {
		n++
	}
	return n
}

// checkYearDigits refuses a value whose year runs past maxYearDigits. The
// refusal wraps xdm.ErrResourceLimit: the value is not invalid, this
// processor declines to decide it, and a caller has to be able to tell the two
// apart.
func checkYearDigits(normalized, primitive string) error {
	if n := yearDigits(normalized, primitive); n > maxYearDigits {
		return fmt.Errorf(
			"the year of %s has %d digits, more than the %d this processor "+
				"supports (XSD 1.1 Part 2 §5.4): %w",
			truncate(normalized), n, maxYearDigits, xdm.ErrResourceLimit)
	}
	return nil
}

// parseYear reads an optionally negative decimal year of up to maxYearDigits
// digits.
//
// The year is unbounded in the lexical space, and refusing one too long for
// an int64 left every facet, enumeration and key that compares it
// unconstrained: a caller that cannot compare a value lets it pass. A longer
// year is refused before it reaches here, by checkYearDigits, as a resource
// limit. The digits are checked first because SetString also takes a '+',
// which the lexical space does not.
func parseYear(v string, out *big.Int) bool {
	digits := strings.TrimPrefix(v, "-")
	if digits == "" || len(digits) > maxYearDigits {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	_, ok := out.SetString(v, 10)
	return ok
}

// parseInt reads an optionally signed decimal integer.
func parseInt(v string, out *int64) bool {
	if v == "" {
		return false
	}
	neg := false
	if v[0] == '-' {
		neg, v = true, v[1:]
	} else if v[0] == '+' {
		v = v[1:]
	}
	if v == "" {
		return false
	}
	var n int64
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
		// A year of more than eighteen digits is legal in the lexical
		// space but cannot be held here. Refusing to compare is better
		// than wrapping into a wrong answer.
		if n > (1<<62)/10 {
			return false
		}
		n = n*10 + int64(v[i]-'0')
	}
	if neg {
		n = -n
	}
	*out = n
	return true
}

// daysFromCivil converts a proleptic Gregorian date to days from 1970-01-01.
//
// This is Howard Hinnant's era algorithm. It is used rather than time.Date
// because the year is not bounded to what a time.Time can hold, and because the
// division must round toward negative infinity for years before the epoch.
func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	var era int64
	if y >= 0 {
		era = y / 400
	} else {
		era = (y - 399) / 400
	}
	yoe := y - era*400
	var mp int64
	if m > 2 {
		mp = m - 3
	} else {
		mp = m + 9
	}
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// duration is an xs:duration reduced to its two independent components.
//
// Months and seconds cannot be reconciled: a month is between 28 and 31 days,
// so P1M and P30D have no fixed order. The spec makes duration only partially
// ordered for exactly this reason, and comparing through a single "average
// month" would invent an order the spec does not have.
type duration struct {
	months  int64
	seconds *big.Rat
}

// compareDuration orders two durations, reporting whether they are comparable.
//
// The spec (Part 2 §3.2.6.2) defines the order by adding both durations to four
// reference dateTimes and requiring the same answer from all four. That is not
// the same as "the month and second components agree": P1973Y12M29DT05H47M26S
// has fewer months but more seconds than P1979Y05M22DT21H16M00S, yet adding
// each to any of the four references orders them the same way, because 65
// months outweighs the six-day difference whichever month lengths apply.
//
// The four references are the ones the spec names, chosen so that every
// combination of leap year and month length is covered.
func compareDuration(a, b duration) (int, bool) {
	if a.months == b.months {
		return a.seconds.Cmp(b.seconds), true
	}

	var first int
	for i, ref := range durationReferences {
		ra := addDurationTo(ref, a)
		rb := addDurationTo(ref, b)
		c := ra.Cmp(rb)
		if i == 0 {
			first = c
			continue
		}
		if c != first {
			// The references disagree, so the order is genuinely
			// indeterminate — P1M against P30D is the canonical case.
			return 0, false
		}
	}
	return first, true
}

// durationReferences are the four dateTimes Part 2 §3.2.6.2 names for ordering
// durations, as (year, month, day, seconds-into-day).
//
// They are chosen to cover every combination of leap year and month length, so
// that two durations ordered the same way against all four are ordered the same
// way against every dateTime.
var durationReferences = [4]struct {
	y, m, d int64
	secs    int64
}{
	{1696, 9, 1, 0},
	{1697, 2, 1, 0},
	{1903, 3, 1, 0},
	{1903, 7, 1, 0},
}

// addDurationTo adds a duration to a reference dateTime and returns the result
// as seconds from the epoch.
//
// The months are added first and the day clamped to the length of the resulting
// month, which is what makes "one month after 31 January" mean 28 or 29
// February rather than an invalid date. Only then are the seconds added.
func addDurationTo(ref struct {
	y, m, d int64
	secs    int64
}, d duration) *big.Rat {
	totalMonths := ref.y*12 + (ref.m - 1) + d.months
	year := floorDiv(totalMonths, 12)
	month := totalMonths - year*12 + 1

	day := ref.d
	if max := daysInMonth(year, month); day > max {
		day = max
	}

	out := new(big.Rat).SetInt64(daysFromCivil(year, month, day)*86400 + ref.secs)
	return out.Add(out, d.seconds)
}

// floorDiv divides rounding toward negative infinity, which is what a calendar
// needs for years before the epoch.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// daysInMonth returns the length of a month in the proleptic Gregorian
// calendar.
func daysInMonth(year, month int64) int64 {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	}
	if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
		return 29
	}
	return 28
}

// parseDuration reduces an xs:duration literal to its components.
func parseDuration(v string) (duration, bool) {
	neg := false
	if strings.HasPrefix(v, "-") {
		neg, v = true, v[1:]
	}
	if !strings.HasPrefix(v, "P") {
		return duration{}, false
	}
	v = v[1:]

	datePart, timePart, hasT := strings.Cut(v, "T")
	var months int64
	seconds := new(big.Rat)

	if !scanDurationFields(datePart, "YMD", func(field byte, n *big.Rat) bool {
		switch field {
		case 'Y':
			i, ok := ratToInt(n)
			if !ok {
				return false
			}
			months += i * 12
		case 'M':
			i, ok := ratToInt(n)
			if !ok {
				return false
			}
			months += i
		case 'D':
			seconds.Add(seconds, new(big.Rat).Mul(n, big.NewRat(86400, 1)))
		}
		return true
	}) {
		return duration{}, false
	}

	if hasT && !scanDurationFields(timePart, "HMS", func(field byte, n *big.Rat) bool {
		switch field {
		case 'H':
			seconds.Add(seconds, new(big.Rat).Mul(n, big.NewRat(3600, 1)))
		case 'M':
			seconds.Add(seconds, new(big.Rat).Mul(n, big.NewRat(60, 1)))
		case 'S':
			seconds.Add(seconds, n)
		}
		return true
	}) {
		return duration{}, false
	}

	if neg {
		months = -months
		seconds.Neg(seconds)
	}
	return duration{months: months, seconds: seconds}, true
}

// scanDurationFields walks "12Y3M" style fields in designator order.
func scanDurationFields(v, designators string, fn func(byte, *big.Rat) bool) bool {
	pos := 0
	for len(v) > 0 {
		n := 0
		for n < len(v) && (v[n] >= '0' && v[n] <= '9' || v[n] == '.') {
			n++
		}
		if n == 0 || n == len(v) {
			return false
		}
		d := strings.IndexByte(designators[pos:], v[n])
		if d < 0 {
			return false
		}
		pos += d + 1

		val, ok := new(big.Rat).SetString(v[:n])
		if !ok || !fn(v[n], val) {
			return false
		}
		v = v[n+1:]
	}
	return true
}

// ratToInt returns the integer value of an exact whole rational.
func ratToInt(r *big.Rat) (int64, bool) {
	if !r.IsInt() {
		return 0, false
	}
	n := r.Num()
	if !n.IsInt64() {
		return 0, false
	}
	return n.Int64(), true
}

// wrapDay reduces a seconds-of-day value into [0, 86400).
//
// It is used for xs:time, where the value space is a time of day rather than a
// point on a timeline: 24:00:00 and 00:00:00 name the same time, and a
// timezone may carry a value outside the day in either direction.
func wrapDay(t *big.Rat) *big.Rat {
	day := new(big.Rat).SetInt64(86400)
	for t.Cmp(day) >= 0 {
		t = new(big.Rat).Sub(t, day)
	}
	zero := new(big.Rat)
	for t.Cmp(zero) < 0 {
		t = new(big.Rat).Add(t, day)
	}
	return t
}
