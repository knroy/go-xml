// Package xmlname holds the XML Name productions.
//
// They live in their own leaf package because two packages need them and
// neither may import the other: xdm exports them to the sibling parsers, and
// internal/xmlfork — the forked encoding/xml tokeniser — resolves its name
// check through them. A tokeniser that imported xdm would close a cycle,
// since xdm is what drives the tokeniser.
package xmlname

// isNameStartRune and isNameRune are the XML NameStartChar and NameChar
// productions of XML 1.0 fifth edition, transcribed.
//
// The ranges are written out rather than approximated by "anything above
// Latin-1". The difference matters: NameStartChar deliberately excludes the
// combining marks and the digits, so U+0E35 THAI CHARACTER SARA II is a legal
// character *within* a name and an illegal one to begin it. A schema language
// that gets this wrong accepts names no conforming parser will produce.
func IsNameStartRune(r rune) bool {
	switch {
	case r == ':' || r == '_':
		// The colon is in the production; callers that forbid it — NCName —
		// reject it before reaching here.
		return true
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		return true
	case r >= 0xC0 && r <= 0xD6:
		return true
	case r >= 0xD8 && r <= 0xF6:
		return true
	case r >= 0xF8 && r <= 0x2FF:
		return true
	case r >= 0x370 && r <= 0x37D:
		return true
	case r >= 0x37F && r <= 0x1FFF:
		return true
	case r >= 0x200C && r <= 0x200D:
		return true
	case r >= 0x2070 && r <= 0x218F:
		return true
	case r >= 0x2C00 && r <= 0x2FEF:
		return true
	case r >= 0x3001 && r <= 0xD7FF:
		return true
	case r >= 0xF900 && r <= 0xFDCF:
		return true
	case r >= 0xFDF0 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0xEFFFF:
		return true
	}
	return false
}

func IsNameRune(r rune) bool {
	if IsNameStartRune(r) {
		return true
	}
	switch {
	case r == '-', r == '.', r == 0xB7:
		return true
	case r >= '0' && r <= '9':
		return true
	case r >= 0x300 && r <= 0x36F:
		return true
	case r >= 0x203F && r <= 0x2040:
		return true
	}
	return false
}
