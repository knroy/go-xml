package xpath

import (
	"fmt"
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
		atoms := xdm.Atomize(seqArg(args, 0))
		// An empty $value is the zero-length string, not an error and not
		// "0": the function is defined that way so a missing value formats to
		// nothing.
		if len(atoms) == 0 {
			return strSeq(""), nil
		}
		a, ok := atoms[0].(*xdm.Atomic)
		if !ok {
			return nil, xdm.ErrType("fn:format-integer: expected an xs:integer")
		}
		n, err := integerValueOf(a)
		if err != nil {
			return nil, err
		}
		pic, err := argStringRequired(args, 1)
		if err != nil {
			return nil, err
		}
		out, err := formatInteger(n, pic)
		if err != nil {
			return nil, err
		}
		return strSeq(out), nil
	})
}

// integerValueOf reads the xs:integer the first argument is declared to be.
func integerValueOf(a *xdm.Atomic) (int64, error) {
	conv, err := CastAtomic(a, xdm.TypeInteger)
	if err != nil {
		return 0, xdm.ErrType("fn:format-integer: %v", err)
	}
	return int64(conv.Float64()), nil
}

// formatInteger renders n according to the picture.
func formatInteger(n int64, pic string) (string, error) {
	token, modifier := splitIntegerPicture(pic)
	if token == "" {
		return "", fmt.Errorf(
			"FODF1310: the primary format token of %q is zero-length", pic)
	}
	ordinal, err := parseFormatModifier(modifier)
	if err != nil {
		return "", err
	}

	// A negative value is formatted from its absolute value with a minus sign
	// prepended, whatever the numbering sequence.
	neg := n < 0
	if neg {
		n = -n
	}

	out, err := formatIntegerToken(n, token, ordinal)
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
func formatIntegerToken(n int64, token string, ordinal bool) (string, error) {
	// The sequences that are named by a single letter. A token is a decimal
	// digit pattern only if it contains a Unicode digit — "the primary format
	// token contains at least one Unicode digit" is the spec's test, and it
	// is what decides between FODF1310 and the fallback. A token of "#" or
	// "#a" has no digit, so it is an unrecognised sequence that falls back to
	// the decimal representation rather than a malformed pattern; the suite
	// asserts exactly that for format-integer(1500000, '#').
	if !containsDigit(token) {
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
		// A token with no digits and no recognised name is not an error: the
		// spec is explicit that an unrecognised-but-well-formed token falls
		// back to a default representation rather than failing, so that a
		// construct one processor knows and another does not is never an
		// error. Only a malformed digit pattern is FODF1310.
		return decimalString(n, ordinal), nil
	}
	return formatDigitPattern(n, token, ordinal)
}

// formatDigitPattern renders n for a decimal-digit-pattern token.
func formatDigitPattern(n int64, token string, ordinal bool) (string, error) {
	zero, mandatory, groups, err := parseDigitPattern(token)
	if err != nil {
		return "", err
	}

	s := decimalString(n, false)
	// At least as many digits as there are mandatory-digit-signs.
	if len(s) < mandatory {
		s = strings.Repeat("0", mandatory-len(s)) + s
	}
	s = applyIntegerGrouping(s, groups)
	if zero != '0' {
		s = translateDigits(s, zero)
	}
	if ordinal {
		s += ordinalSuffixFor(n)
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

// decimalString renders n in ASCII digits, optionally with an ordinal suffix.
func decimalString(n int64, ordinal bool) string {
	s := fmt.Sprintf("%d", n)
	if ordinal {
		s += ordinalSuffixFor(n)
	}
	return s
}
