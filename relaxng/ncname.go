package relaxng

import "unicode"

// Names in a RELAX NG schema follow XML 1.0 *fourth* edition, which is the
// edition the language was specified against.
//
// This differs from what xdm.IsNCName implements, and the difference is not an
// oversight in either place. The fifth edition (2008) replaced the fourth's
// explicit Unicode 2.0 character classes with broad ranges, and in doing so
// made legal a great many names that were not — among them any name beginning
// with a combining mark. U+0E35 THAI CHARACTER SARA II is the conformance
// suite's example: legal *within* a name in both editions, legal to *begin*
// one only in the fifth.
//
// An XML parser should implement the fifth edition, because that is what XML
// is now and refusing a name a conforming parser produces would be wrong. A
// RELAX NG schema is a different question: the language says its names are
// NCNames as the fourth edition defined them, and a schema written to that
// rule is what the suite tests. So the two live side by side, and this is the
// one the schema language uses.
//
// The classes below follow the fourth edition's Appendix B: a name starts with
// a Letter or an underscore, and continues with those plus digits, combining
// marks, extenders, and the three punctuation characters.

// isNCName4 reports whether s is an NCName under XML 1.0 fourth edition.
func isNCName4(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == ':' {
			return false // non-colonised
		}
		if i == 0 {
			if !isNameStart4(r) {
				return false
			}
			continue
		}
		if !isNameChar4(r) {
			return false
		}
	}
	return true
}

// isNameStart4 is the fourth edition's (Letter | '_').
//
// Letter is BaseChar | Ideographic, which between them are the Unicode
// categories Lu, Ll, Lo, Lt and Lm — letters proper. What they exclude, and
// what the fifth edition's ranges swept back in, is everything else: the
// combining marks (Mn, Mc), the digits (Nd), and the modifier symbols.
func isNameStart4(r rune) bool {
	if r == '_' {
		return true
	}
	return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lo, unicode.Lt,
		unicode.Lm)
}

// isNameChar4 is the fourth edition's NameChar, less the colon.
func isNameChar4(r rune) bool {
	if isNameStart4(r) {
		return true
	}
	switch r {
	case '-', '.', 0xB7: // the last is MIDDLE DOT, an Extender
		return true
	}
	// Digits, combining marks and extenders may appear after the first
	// character though not before it.
	return unicode.In(r, unicode.Nd, unicode.Mn, unicode.Mc)
}
