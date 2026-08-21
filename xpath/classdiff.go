package xpath

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Character-class subtraction, "[a-z-[aeiou]]", selects the characters in the
// first class that are not in the second. RE2 has no such syntax, so the two
// classes are expanded into codepoint ranges here, the difference is computed,
// and the result is emitted as an ordinary class.
//
// Literal characters, ranges, and the multi-character escapes whose definitions
// are fixed and small — \d \D \w \W \s \S \i \I \c \C — are expanded
// exactly. A subtraction involving \p{...} is still refused: expanding a
// Unicode category means embedding the tables that decide it, and a class that
// silently matched the wrong set would be worse than an error.

type cpRange struct{ lo, hi rune }

// parseClassBody expands the body of a character class into sorted, disjoint
// ranges. It reports ok=false for anything it cannot expand exactly.
func parseClassBody(body string) (ranges []cpRange, negated, ok bool) {
	r := []rune(body)
	i := 0
	if i < len(r) && r[i] == '^' {
		negated = true
		i++
	}
	for i < len(r) {
		c := r[i]
		if c == '\\' {
			if i+1 >= len(r) {
				return nil, false, false
			}
			esc := r[i+1]

			// \p{Nd} and \P{Nd} reach here because the translator
			// rewrites \d and \D into them before the class body is
			// read. Recognising the property form keeps subtraction
			// working on a class the rewrite has already touched.
			if esc == 'p' || esc == 'P' {
				name, n, good := readPropertyName(r[i+2:])
				if !good {
					return nil, false, false
				}
				sub, good := propertyRanges(name)
				if !good {
					return nil, false, false
				}
				if esc == 'P' {
					sub = complementRanges(sub)
				}
				ranges = append(ranges, sub...)
				i += 2 + n
				continue
			}

			// A multi-character escape contributes a set of ranges
			// rather than one character, so it cannot take part in a
			// range and is appended whole.
			if sub, good := shorthandRanges(esc); good {
				if i+2 < len(r) && r[i+2] == '-' && i+3 < len(r) && r[i+3] != ']' {
					// "\d-x" is not a range in XML Schema:
					// the left end is a set.
					return nil, false, false
				}
				ranges = append(ranges, sub...)
				i += 2
				continue
			}

			lit, good := classEscapeLiteral(esc)
			if !good {
				// \p{...} and anything else whose expansion needs
				// the Unicode tables.
				return nil, false, false
			}
			c = lit
			i += 2
		} else {
			i++
		}
		// A range is "a-z", but a trailing "-" before "]" is a literal.
		if i < len(r) && r[i] == '-' && i+1 < len(r) {
			hi := r[i+1]
			if hi == '\\' {
				if i+2 >= len(r) {
					return nil, false, false
				}
				lit, good := classEscapeLiteral(r[i+2])
				if !good {
					return nil, false, false
				}
				hi = lit
				i += 3
			} else {
				i += 2
			}
			if hi < c {
				return nil, false, false
			}
			ranges = append(ranges, cpRange{c, hi})
			continue
		}
		ranges = append(ranges, cpRange{c, c})
	}
	return normalizeRanges(ranges), negated, true
}

// classEscapeLiteral maps the escapes that stand for a single character.
func classEscapeLiteral(esc rune) (rune, bool) {
	switch esc {
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	case '\\', ']', '[', '-', '^', '.', '|', '?', '*', '+', '(', ')', '{', '}', '/', '$':
		return esc, true
	}
	return 0, false
}

// shorthandRanges expands a multi-character escape into codepoint ranges.
//
// The definitions come from XML Schema Part 2 Appendix F, and every one of them
// is a fixed, small set — unlike \p{...}, whose expansion would need the
// Unicode category tables. Expanding them is what lets "[\d-[357]]" and
// "[\w-[b-y]]" be computed rather than refused, which is the largest single
// category of pattern this package used to reject.
func shorthandRanges(esc rune) ([]cpRange, bool) {
	switch esc {
	case 'd':
		// Appendix F defines \d as \p{Nd}: every decimal digit in
		// Unicode, not the ASCII ten. The ranges come from Go's own
		// tables rather than a hand-written list, so they cannot drift
		// from the standard.
		return tableRanges(unicode.Nd), true
	case 'D':
		return complementRanges(tableRanges(unicode.Nd)), true

	case 'w':
		// \w is everything outside the punctuation, separator and other
		// categories — defined by subtraction in the spec.
		return complementRanges(nonWordRanges()), true
	case 'W':
		return nonWordRanges(), true

	case 's':
		return []cpRange{{'\t', '\n'}, {'\r', '\r'}, {' ', ' '}}, true
	case 'S':
		return complementRanges([]cpRange{{'\t', '\n'}, {'\r', '\r'}, {' ', ' '}}), true

	case 'i':
		// The characters that may start an XML Name.
		return nameStartRanges(), true
	case 'I':
		return complementRanges(nameStartRanges()), true

	case 'c':
		// The characters that may appear in an XML Name.
		return nameCharRanges(), true
	case 'C':
		return complementRanges(nameCharRanges()), true
	}
	return nil, false
}

// readPropertyName reads "{Name}" following \p or \P, returning the name and
// how many runes were consumed.
func readPropertyName(r []rune) (string, int, bool) {
	if len(r) == 0 || r[0] != '{' {
		return "", 0, false
	}
	for i := 1; i < len(r); i++ {
		if r[i] == '}' {
			return string(r[1:i]), i + 1, true
		}
	}
	return "", 0, false
}

// propertyRanges expands the Unicode properties this package can resolve
// exactly.
//
// Only the ones the translator itself produces are handled. A schema writing
// \p{Lu} inside a subtraction is still refused: the general case needs every
// category table, and a class that silently matched the wrong set would be
// worse than an error.
func propertyRanges(name string) ([]cpRange, bool) {
	switch name {
	case "Nd":
		return tableRanges(unicode.Nd), true
	case "P":
		return tableRanges(unicode.P), true
	case "Z":
		return tableRanges(unicode.Z), true
	case "C":
		return tableRanges(unicode.C), true
	}
	return nil, false
}

// maxRune is the highest codepoint a range may reach.
const maxRune = 0x10FFFF

// complementRanges returns the ranges not covered by the input.
func complementRanges(in []cpRange) []cpRange {
	in = normalizeRanges(append([]cpRange(nil), in...))
	var out []cpRange
	next := rune(0)
	for _, r := range in {
		if r.lo > next {
			out = append(out, cpRange{next, r.lo - 1})
		}
		if r.hi+1 > next {
			next = r.hi + 1
		}
	}
	if next <= maxRune {
		out = append(out, cpRange{next, maxRune})
	}
	return out
}

// nonWordRanges is the set \W names: the punctuation, separator and other
// categories, which Appendix F subtracts from everything to define \w.
func nonWordRanges() []cpRange {
	var out []cpRange
	for _, t := range []*unicode.RangeTable{unicode.P, unicode.Z, unicode.C} {
		out = append(out, tableRanges(t)...)
	}
	return normalizeRanges(out)
}

// tableRanges converts a Unicode range table to codepoint ranges.
//
// Reading Go's tables rather than writing the ranges out is what keeps them
// from drifting: the sets are large, they change between Unicode versions, and
// a hand-copied list would be wrong in a way no test here would catch.
func tableRanges(t *unicode.RangeTable) []cpRange {
	var out []cpRange
	for _, r := range t.R16 {
		if r.Stride == 1 {
			out = append(out, cpRange{rune(r.Lo), rune(r.Hi)})
			continue
		}
		for c := rune(r.Lo); c <= rune(r.Hi); c += rune(r.Stride) {
			out = append(out, cpRange{c, c})
		}
	}
	for _, r := range t.R32 {
		if r.Stride == 1 {
			out = append(out, cpRange{rune(r.Lo), rune(r.Hi)})
			continue
		}
		for c := rune(r.Lo); c <= rune(r.Hi); c += rune(r.Stride) {
			out = append(out, cpRange{c, c})
		}
	}
	return normalizeRanges(out)
}

// nameStartRanges is the set \i names: the characters that may begin an XML
// Name.
func nameStartRanges() []cpRange {
	return []cpRange{
		{':', ':'}, {'A', 'Z'}, {'_', '_'}, {'a', 'z'},
		{0xC0, 0xD6}, {0xD8, 0xF6}, {0xF8, 0x2FF},
		{0x370, 0x37D}, {0x37F, 0x1FFF},
		{0x200C, 0x200D}, {0x2070, 0x218F}, {0x2C00, 0x2FEF},
		{0x3001, 0xD7FF}, {0xF900, 0xFDCF}, {0xFDF0, 0xFFFD},
		{0x10000, 0xEFFFF},
	}
}

// nameCharRanges is the set \c names: the characters that may appear in an XML
// Name, which is \i plus digits, the hyphen, the full stop and the combining
// marks.
func nameCharRanges() []cpRange {
	out := append([]cpRange(nil), nameStartRanges()...)
	return append(out,
		cpRange{'-', '.'}, cpRange{'0', '9'},
		cpRange{0xB7, 0xB7}, cpRange{0x300, 0x36F}, cpRange{0x203F, 0x2040},
	)
}

func normalizeRanges(in []cpRange) []cpRange {
	if len(in) == 0 {
		return in
	}
	sort.Slice(in, func(i, j int) bool { return in[i].lo < in[j].lo })
	out := []cpRange{in[0]}
	for _, r := range in[1:] {
		last := &out[len(out)-1]
		if r.lo <= last.hi+1 {
			if r.hi > last.hi {
				last.hi = r.hi
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// subtractRanges returns the ranges in a that are not in b.
func subtractRanges(a, b []cpRange) []cpRange {
	var out []cpRange
	for _, r := range a {
		cur := []cpRange{r}
		for _, s := range b {
			var next []cpRange
			for _, c := range cur {
				// No overlap: the piece survives whole.
				if s.hi < c.lo || s.lo > c.hi {
					next = append(next, c)
					continue
				}
				if s.lo > c.lo {
					next = append(next, cpRange{c.lo, s.lo - 1})
				}
				if s.hi < c.hi {
					next = append(next, cpRange{s.hi + 1, c.hi})
				}
			}
			cur = next
		}
		out = append(out, cur...)
	}
	return normalizeRanges(out)
}

// formatClass renders ranges as an RE2 character class body.
func formatClass(ranges []cpRange) string {
	var sb strings.Builder
	for _, r := range ranges {
		if r.lo == r.hi {
			sb.WriteString(escapeClassRune(r.lo))
			continue
		}
		sb.WriteString(escapeClassRune(r.lo))
		sb.WriteByte('-')
		sb.WriteString(escapeClassRune(r.hi))
	}
	return sb.String()
}

func escapeClassRune(r rune) string {
	switch r {
	case '\\', ']', '^', '-', '[':
		return "\\" + string(r)
	}
	if r < 0x20 || r == 0x7F {
		return fmt.Sprintf("\\x{%x}", r)
	}
	return string(r)
}

// subtractClasses computes "[left-[right]]" and returns the RE2 class for it.
func subtractClasses(left, right string) (string, bool) {
	lr, lneg, ok := parseClassBody(left)
	if !ok || lneg {
		// A negated left side would need the complement before subtracting,
		// which is expressible but not worth the extra surface until a test
		// asks for it.
		return "", false
	}
	rr, rneg, ok := parseClassBody(right)
	if !ok || rneg {
		return "", false
	}
	diff := subtractRanges(lr, rr)
	if len(diff) == 0 {
		// An empty class matches nothing; RE2 rejects "[]", so a class that
		// cannot match is written as one excluding everything.
		return "^\\x{0}-\\x{10FFFF}", true
	}
	return formatClass(diff), true
}
