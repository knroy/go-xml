package xdm

import "testing"

// XML 1.0 Fifth Edition redefined NameStartChar [4] and NameChar [4a] from the
// First Edition's enumerated Letter/Digit/Extender lists into a broad range
// formulation with explicit exclusions. Go's encoding/xml ships the First
// Edition tables, which is why this package tokenises through
// internal/xmlfork; see internal/xmlfork/NOTICE.
//
// The acceptances below are all 5e-legal and 1e-illegal, so every one of them
// fails against an unpatched encoding/xml.
func TestNameStartChar5eAccepted(t *testing.T) {
	for _, tc := range []struct {
		name string
		elem string
	}{
		// The W3C's own case: XSD suite saxonData/XmlVersions xv001 and xv004
		// are titled "Use newly-allowed name characters ...".
		{"U+0132 LATIN CAPITAL LIGATURE IJ", "DĲkstra"},
		{"U+0133 LATIN SMALL LIGATURE IJ", "Dĳkstra"},
		// 1e stopped Latin Extended-A at U+0131 and resumed at U+0134; 5e
		// admits the whole block via C0-2FF.
		{"U+013F LATIN CAPITAL LIGATURE OE", "aĿ"},
		{"U+0140 LATIN SMALL LIGATURE OE", "aŀ"},
		{"U+0149 LATIN SMALL N PRECEDED BY APOSTROPHE", "aŉ"},
		{"U+017F LATIN SMALL LETTER LONG S", "aſ"},
		// 5e admits the modifier-letter block, which 1e classed as Extender
		// and so forbade at the start of a name.
		{"U+02B0 MODIFIER LETTER SMALL H", "ʰx"},
		{"U+02FF MODIFIER LETTER LOW LEFT ARROW", "˿x"},
		// Non-BMP: xv004's case. 1e had no astral letters at all.
		{"U+10000 LINEAR B SYLLABLE B008 A", "\U00010000x"},
		{"U+20000 CJK EXT B", "\U00020000x"},
		{"U+EFFFF top of the 5e astral range", "\U000EFFFFx"},
		// Currency and letterlike symbols admitted by 2070-218F.
		{"U+2071 SUPERSCRIPT LATIN SMALL LETTER I", "ⁱx"},
		// Glagolitic, admitted by 2C00-2FEF.
		{"U+2C00 GLAGOLITIC CAPITAL LETTER AZU", "Ⰰx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseString("<"+tc.elem+"/>", ParseOptions{}); err != nil {
				t.Errorf("element <%s>: %v", tc.elem, err)
			}
			// An attribute name runs the same production through a different
			// call site in the tokeniser, so check both.
			if _, err := ParseString(`<e `+tc.elem+`="v"/>`, ParseOptions{}); err != nil {
				t.Errorf("attribute %s=: %v", tc.elem, err)
			}
		})
	}
}

// TestNameChar5eAccepted covers characters legal only after the first
// character. NameChar is a superset of NameStartChar, and the difference is
// load-bearing: a combining mark or a digit may continue a name and may not
// begin one.
func TestNameChar5eAccepted(t *testing.T) {
	for _, tc := range []struct{ name, elem string }{
		{"U+0300 COMBINING GRAVE ACCENT", "à"},
		{"U+036F COMBINING LATIN SMALL LETTER X", "aͯ"},
		{"U+00B7 MIDDLE DOT", "a·"},
		{"U+203F UNDERTIE", "a‿"},
		{"U+2040 CHARACTER TIE", "a⁀"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseString("<"+tc.elem+"/>", ParseOptions{}); err != nil {
				t.Errorf("element <%s>: %v", tc.elem, err)
			}
		})
	}
}

// TestNameStillRejected is the half that matters. Widening a name table is
// only correct if it widens to exactly the 5e production and no further: a
// tokeniser that accepts everything would also pass every test above.
func TestNameStillRejected(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		// NameStartChar excludes the ASCII digits — and, as it happens, only
		// those. 5e's ranges are broad enough that U+0660 ARABIC-INDIC DIGIT
		// ZERO falls inside [#x37F-#x1FFF] and IS a legal NameStartChar; the
		// exclusions are by codepoint range, not by Unicode category, which
		// is precisely the change 5e made.
		{"leading ASCII digit", "<1abc/>"},
		{"leading ASCII digit 9", "<9abc/>"},
		// ... and '-' and '.', which are NameChar only.
		{"leading hyphen", "<-abc/>"},
		{"leading full stop", "<.abc/>"},
		// ... and the combining marks. This is the exclusion the xdm comment
		// calls out: U+0300 continues a name but cannot begin one.
		{"leading combining mark U+0300", "<̀abc/>"},
		{"leading combining mark U+036F", "<ͯabc/>"},
		{"leading U+00B7 MIDDLE DOT", "<·abc/>"},
		{"leading U+203F UNDERTIE", "<‿abc/>"},
		// 5e's explicit exclusions, the gaps between its ranges.
		{"U+00D7 MULTIPLICATION SIGN", "<a×b/>"},
		{"U+00F7 DIVISION SIGN", "<a÷b/>"},
		{"U+037E GREEK QUESTION MARK", "<a;b/>"},
		{"U+2000 EN QUAD", "<a b/>"},
		{"U+3000 IDEOGRAPHIC SPACE", "<a　b/>"},
		{"U+FDD0 noncharacter", "<a﷐b/>"},
		{"U+FFFE noncharacter", "<a￾b/>"},
		{"above U+EFFFF", "<a\U000F0000b/>"},
		// Structural characters, which must remain delimiters rather than
		// become name characters.
		{"space inside a name", "<ab cd=\"1\"><x/>"},
		{"'<' inside a name", "<ab<cd/>"},
		{"'&' inside a name", "<ab&cd/>"},
		{"'\"' inside a name", "<ab\"cd/>"},
		{"'/' inside a name", "<ab/cd/>"},
		{"empty name", "</>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseString(tc.doc, ParseOptions{}); err == nil {
				t.Errorf("ParseString(%q) = nil error, want a rejection", tc.doc)
			}
		})
	}
}
