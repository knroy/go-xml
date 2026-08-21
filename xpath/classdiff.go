package xpath

import (
	"fmt"
	"sort"
	"strings"
)

// Character-class subtraction, "[a-z-[aeiou]]", selects the characters in the
// first class that are not in the second. RE2 has no such syntax, so the two
// classes are expanded into codepoint ranges here, the difference is computed,
// and the result is emitted as an ordinary class.
//
// Only classes made of literal characters and ranges can be handled this way.
// A subtraction involving \d, \p{...} or another shorthand is still refused:
// expanding those means embedding the Unicode tables that decide them, and a
// class that silently matched the wrong set would be worse than an error.

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
			lit, good := classEscapeLiteral(esc)
			if !good {
				// A shorthand class such as \d or \p{L}: not expandable here.
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
