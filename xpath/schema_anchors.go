package xpath

import "strings"

// escapeSchemaAnchors makes "^" and "$" the ordinary characters they are in a
// schema pattern.
//
// Appendix F's grammar has no anchors at all — a pattern facet is anchored as a
// whole, so there is nothing for them to express — and lists "^" and "$" among
// the characters an atom may be. XPath's fn:matches does have them, and the two
// flavours share one translator, so they are escaped here rather than in it.
//
// Passing them through let RE2 read them as anchors, which silently widened
// every schema pattern that used one: reZ001 writes "^...$" around an address
// pattern, and RE2 read those as the anchors the wrapping already supplies
// instead of as the two characters the value has to carry.
func escapeSchemaAnchors(pattern string) string {
	var sb strings.Builder
	sb.Grow(len(pattern) + 8)

	// Classes nest: character class subtraction writes "[a-z-[^a]]", and
	// the inner class negates just as an outer one does. Tracking a single
	// level escaped the "^" in "[^a]" and turned the subtraction into a
	// subtraction of the literal characters "^" and "a".
	depth := 0
	classBody := -1 // where the innermost class's body starts in sb

	for i := 0; i < len(pattern); i++ {
		c := pattern[i]

		// An escaped character is already literal, whatever it is.
		if c == '\\' && i+1 < len(pattern) {
			sb.WriteByte(c)
			i++
			sb.WriteByte(pattern[i])
			continue
		}

		switch {
		case c == '[':
			depth++
			classBody = sb.Len() + 1
		case c == ']' && depth > 0:
			depth--
			classBody = -1
		case c == '^' && depth > 0 && sb.Len() == classBody:
			// First position in a class is negation in both
			// flavours; leave it to mean that.
		case c == '^' || c == '$':
			sb.WriteByte('\\')
		}
		sb.WriteByte(c)
	}
	return sb.String()
}
