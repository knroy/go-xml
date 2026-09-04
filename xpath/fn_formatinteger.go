package xpath

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/knroy/go-xml/xdm"
)

// fn:format-integer, F&O 3.0 section 4.6.
//
// The picture is a primary format token, optionally followed by a semicolon
// and a format modifier. The token selects a numbering sequence — a decimal
// digit pattern, Roman numerals, letters, or spelled-out words — and the
// modifier asks for ordinal rather than cardinal numbering.
//
// Most of the sequences already existed for fn:format-dateTime's picture
// language, which shares them; this function is largely the picture parsing
// that differs, plus the grouping separators, which a date picture has no use
// for.

// registerFormatInteger adds fn:format-integer.
func registerFormatInteger(l *Library) {
	l.registerFnSince(XPath30, "format-integer", []int{2, 3}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// $value is declared xs:integer? — a singleton — so two items is
		// XPTY0004 rather than a request to format the first.
		a, err := argAtomicOptional(args, 0, "fn:format-integer")
		if err != nil {
			return nil, err
		}
		// An empty $value is the zero-length string, not an error and not
		// "0": the function is defined that way so a missing value formats to
		// nothing.
		if a == nil {
			return strSeq(""), nil
		}
		neg, digits, err := integerValueOf(a)
		if err != nil {
			return nil, err
		}
		pic, err := argStringRequired(args, 1)
		if err != nil {
			return nil, err
		}
		out, err := formatInteger(neg, digits, pic)
		if err != nil {
			return nil, err
		}
		return strSeq(out), nil
	})
}

// integerValueOf reads the xs:integer the first argument is declared to be,
// and returns it as a sign and an exact decimal digit string.
//
// It used to return int64(conv.Float64()), which failed SILENTLY in two ways.
// Float64() on an arbitrary-precision xs:integer overflows the float64 range
// and the int64 conversion then saturates, so format-integer(10^400, '1')
// answered "9223372036854775807"; and below that, a double holds only about 17
// significant digits, so 123456789012345678 came back as 123456789012345680.
// Neither raised an error.
//
// fn:format-integer declares $value as xs:integer?, which is unbounded, and
// F&O gives no error code for a value that is merely large -- so a big value
// is formatted, not refused. The digit string is what every path here actually
// needs: a decimal-digit-pattern pads and groups the digits, and the named
// sequences all fall back to the digit string once past their own range.
func integerValueOf(a *xdm.Atomic) (neg bool, digits string, err error) {
	conv, err := CastAtomic(a, xdm.TypeInteger)
	if err != nil {
		return false, "", xdm.ErrType("fn:format-integer: %v", err)
	}
	s := conv.String()
	if strings.HasPrefix(s, "-") {
		return true, s[1:], nil
	}
	return false, s, nil
}

// formatInteger renders the magnitude digits according to the picture, with a
// minus sign prepended when neg.
//
// The value arrives as sign-and-digits rather than as an int64 so that an
// xs:integer beyond the int64 range is formatted exactly. Taking the sign here
// also matches the spec, which applies every rule to the absolute value -- and
// it removes a second latent defect: the old "if n < 0 { n = -n }" left
// math.MinInt64 negative, so format-integer(-9223372036854775808, '1')
// prepended the sign to a string that already had one and answered
// "--9223372036854775808".
func formatInteger(neg bool, digits string, pic string) (string, error) {
	token, modifier := splitIntegerPicture(pic)
	if token == "" {
		return "", fmt.Errorf(
			"FODF1310: the primary format token of %q is zero-length", pic)
	}
	ordinal, err := parseFormatModifier(modifier)
	if err != nil {
		return "", err
	}

	out, err := formatIntegerToken(digits, token, ordinal)
	if err != nil {
		return "", err
	}
	if neg {
		out = "-" + out
	}
	return out, nil
}

// parseFormatModifier validates the format modifier and reports whether it
// asks for ordinal numbering.
//
// The grammar is a cardinal-or-ordinal letter ("c" or "o"), each optionally
// followed by a parenthesised variation such as "o(-er)", then an optional
// letter-sequence selector ("a" or "t"). Anything else is FODF1310: the suite
// asserts it for "1;o(-er)z", "Ww;o(" and "Ww;o()(" .
func parseFormatModifier(mod string) (ordinal bool, err error) {
	bad := func() (bool, error) {
		return false, fmt.Errorf("FODF1310: invalid format modifier %q", mod)
	}
	runes := []rune(mod)
	i := 0
	if i < len(runes) && (runes[i] == 'c' || runes[i] == 'o') {
		ordinal = runes[i] == 'o'
		i++
		// An optional parenthesised variation, which must be closed and must
		// be the only one.
		if i < len(runes) && runes[i] == '(' {
			depth := 0
			for ; i < len(runes); i++ {
				if runes[i] == '(' {
					depth++
				} else if runes[i] == ')' {
					depth--
					if depth == 0 {
						i++
						break
					}
				}
			}
			if depth != 0 {
				return bad()
			}
		}
	}
	// What remains may only be the letter-sequence selector.
	switch {
	case i == len(runes):
		return ordinal, nil
	case i == len(runes)-1 && (runes[i] == 'a' || runes[i] == 't'):
		return ordinal, nil
	}
	return bad()
}

// splitIntegerPicture divides the picture at its last semicolon.
//
// "Everything that precedes the last semicolon is the primary format token and
// everything that follows is the format modifier." The rule is stated in terms
// of the *last* semicolon so that a semicolon can itself be a grouping
// separator: "#;##0;" is the token "#;##0" with an empty modifier.
func splitIntegerPicture(pic string) (token, modifier string) {
	if i := strings.LastIndexByte(pic, ';'); i >= 0 {
		return pic[:i], pic[i+1:]
	}
	return pic, ""
}

// formatIntegerToken renders n for one primary format token.
//
// The named sequences take an int64 because none of them has a representation
// beyond a few thousand anyway: each already falls back to the decimal digits
// when the value is outside its range. A value too large for an int64 is
// therefore past every one of those ranges, and takes the same fallback --
// with its exact digits, which is the point.
func formatIntegerToken(digits string, token string, ordinal bool) (string, error) {
	n, fits := smallEnough(digits)
	// The sequences that are named by a single letter. A token is a decimal
	// digit pattern only if it contains a Unicode digit — "the primary format
	// token contains at least one Unicode digit" is the spec's test, and it
	// is what decides between FODF1310 and the fallback. A token of "#" or
	// "#a" has no digit, so it is an unrecognised sequence that falls back to
	// the decimal representation rather than a malformed pattern; the suite
	// asserts exactly that for format-integer(1500000, '#').
	if !containsDigit(token) {
		if fits {
			switch token {
			case "I":
				return romanNum(n), nil
			case "i":
				return strings.ToLower(romanNum(n)), nil
			case "A":
				return alphaNum(n, true), nil
			case "a":
				return alphaNum(n, false), nil
			case "w":
				return spellDateNumber(n, "w", ordinal), nil
			case "W":
				return spellDateNumber(n, "W", ordinal), nil
			case "Ww":
				return spellDateNumber(n, "Ww", ordinal), nil
			}
		}
		// A token with no digits and no recognised name is not an error: the
		// spec is explicit that an unrecognised-but-well-formed token falls
		// back to a default representation rather than failing, so that a
		// construct one processor knows and another does not is never an
		// error. Only a malformed digit pattern is FODF1310.
		return decimalString(digits, ordinal), nil
	}
	return formatDigitPattern(digits, token, ordinal)
}

// smallEnough parses the digit string as an int64, reporting whether it fits.
//
// A value that does not fit is far past the range of every named numbering
// sequence there is -- Roman stops at 3999, spelled-out words at 10^15 -- so
// the caller takes the spec's fallback to the decimal digits for it, which is
// what those sequences would have returned anyway had they been able to hold
// the value.
func smallEnough(digits string) (int64, bool) {
	n, err := strconv.ParseInt(digits, 10, 64)
	return n, err == nil
}

// formatDigitPattern renders n for a decimal-digit-pattern token.
func formatDigitPattern(digits string, token string, ordinal bool) (string, error) {
	zero, mandatory, groups, err := parseDigitPattern(token)
	if err != nil {
		return "", err
	}

	s := digits
	// At least as many digits as there are mandatory-digit-signs.
	if len(s) < mandatory {
		s = strings.Repeat("0", mandatory-len(s)) + s
	}
	s = applyIntegerGrouping(s, groups)
	if zero != '0' {
		s = translateDigits(s, zero)
	}
	if ordinal {
		s += ordinalSuffixForDigits(digits)
	}
	return s, nil
}

// groupSpec is one grouping separator: how many digits from the right it sits,
// and which character it is.
type groupSpec struct {
	fromRight int
	sep       rune
	// width is the pattern's total digit count, the same on every entry. A
	// repeating interval is only implied when the digits leading the first
	// separator fit within that interval, which needs the whole width to
	// judge.
	width int
}

// parseDigitPattern validates a decimal-digit-pattern and extracts its digit
// family, its mandatory digit count and its grouping separators.
//
// The rules it enforces are the ones the spec marks "must", each of which is
// FODF1310: every mandatory digit comes from one family, optional digits
// precede mandatory ones, there is at least one mandatory digit, and a
// separator appears neither at either end nor adjacent to another.
func parseDigitPattern(token string) (zero rune, mandatory int, groups []groupSpec, err error) {
	runes := []rune(token)
	zero = 0
	seenMandatory := false
	// Count digits from the right as the scan goes left to right, by first
	// counting the total.
	total := 0
	for _, r := range runes {
		if unicode.IsDigit(r) || r == '#' {
			total++
		}
	}

	digitsSoFar := 0
	lastWasSep := false
	for i, r := range runes {
		switch {
		case r == '#':
			if seenMandatory {
				return 0, 0, nil, fmt.Errorf(
					"FODF1310: optional-digit-sign after a mandatory one in %q", token)
			}
			digitsSoFar++
			lastWasSep = false
		case unicode.IsDigit(r):
			z := r - rune(digitValueOf(r))
			if zero == 0 {
				zero = z
			} else if z != zero {
				return 0, 0, nil, fmt.Errorf(
					"FODF1310: mixed digit families in %q", token)
			}
			seenMandatory = true
			mandatory++
			digitsSoFar++
			lastWasSep = false
		case isGroupingSeparator(r):
			if i == 0 || i == len(runes)-1 {
				return 0, 0, nil, fmt.Errorf(
					"FODF1310: grouping separator at the edge of %q", token)
			}
			if lastWasSep {
				return 0, 0, nil, fmt.Errorf(
					"FODF1310: adjacent grouping separators in %q", token)
			}
			groups = append(groups, groupSpec{fromRight: total - digitsSoFar, sep: r, width: total})
			lastWasSep = true
		default:
			return 0, 0, nil, fmt.Errorf(
				"FODF1310: %q is not a valid character in the format token %q",
				string(r), token)
		}
	}
	if mandatory == 0 {
		return 0, 0, nil, fmt.Errorf(
			"FODF1310: the format token %q has no mandatory-digit-sign", token)
	}
	if zero == 0 {
		zero = '0'
	}
	return zero, mandatory, groups, nil
}

// isGroupingSeparator reports whether r may be a grouping-separator-sign: any
// character that is neither a digit nor a letter.
func isGroupingSeparator(r rune) bool {
	switch {
	case unicode.IsDigit(r), unicode.IsLetter(r), unicode.IsNumber(r):
		return false
	}
	return true
}

// applyIntegerGrouping inserts the separators at their recorded positions.
//
// A pattern whose separators are at a regular interval repeats that interval
// indefinitely leftwards — "#,##0" groups every three digits however long the
// number is — while irregular positions apply only where they fall.
func applyIntegerGrouping(s string, groups []groupSpec) string {
	if len(groups) == 0 {
		return s
	}
	regular := regularInterval(groups)
	runes := []rune(s)
	var sb strings.Builder
	for i, r := range runes {
		fromRight := len(runes) - i
		if i > 0 {
			if regular > 0 {
				if fromRight%regular == 0 {
					sb.WriteRune(groups[0].sep)
				}
			} else {
				for _, g := range groups {
					if g.fromRight == fromRight {
						sb.WriteRune(g.sep)
					}
				}
			}
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// regularInterval returns the repeating interval a grouping pattern implies,
// or 0 when the separators are irregular and apply only where written.
func regularInterval(groups []groupSpec) int {
	if len(groups) == 0 {
		return 0
	}
	sep := groups[0].sep
	for _, g := range groups {
		if g.sep != sep {
			return 0
		}
	}
	// One separator is regular only when nothing wider than its interval
	// leads it: "#,##0" repeats every 3, but "[Y#.0]" places a single
	// separator one from the right in a two-digit pattern and must not
	// repeat it, or 2016 becomes "2.0.1.6".
	if len(groups) == 1 {
		if groups[0].width > 2*groups[0].fromRight {
			return 0
		}
		return groups[0].fromRight
	}
	// Several are regular only if evenly spaced by their own first interval
	// *and* the pattern's leading group is no wider than that interval. In
	// "000,00,00" the separators are two apart but three digits lead them, so
	// the grouping is irregular and applies only where written: 123456789 is
	// "12345,67,89", not the "1,23,45,67,89" a repeat every two would give.
	step := groups[len(groups)-1].fromRight
	for i, g := range groups {
		if g.fromRight != step*(len(groups)-i) {
			return 0
		}
	}
	// The leading group must fit the interval too. In "000,00,00" the
	// separators are two apart but three digits lead them, so the pattern is
	// irregular and its separators apply only where written.
	if groups[0].width > step*(len(groups)+1) {
		return 0
	}
	return step
}

// containsDigit reports whether the token has any Unicode decimal digit, which
// is what makes it a decimal-digit-pattern rather than a named sequence.
func containsDigit(token string) bool {
	for _, r := range token {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// decimalString renders the digits, optionally with an ordinal suffix.
func decimalString(digits string, ordinal bool) string {
	if ordinal {
		return digits + ordinalSuffixForDigits(digits)
	}
	return digits
}

// ordinalSuffixForDigits is ordinalSuffixFor for a value held as digits.
//
// The English suffix depends only on the last two digits, so it is exact for
// a value no int64 can hold: 10^400+21 takes "st" just as 21 does. The digits
// carry no sign -- formatInteger takes that off before any of this -- so the
// negative handling ordinalSuffixFor has does not arise here.
func ordinalSuffixForDigits(digits string) string {
	last2 := digits
	if len(last2) > 2 {
		last2 = last2[len(last2)-2:]
	}
	n, err := strconv.ParseInt(last2, 10, 64)
	if err != nil {
		return "th"
	}
	return ordinalSuffixFor(n)
}
