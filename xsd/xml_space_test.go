package xsd

import (
	"strings"
	"testing"
)

// A bound facet's value is a lexical form of the type it constrains, and every
// type carrying minInclusive/maxInclusive has whiteSpace="collapse" fixed.
// Collapse trims XML S only, so a no-break space in a facet value is part of
// the lexical form and makes it unparsable -- an unparsable bound constrains
// nothing. strings.TrimSpace stripped the NBSP instead, so a bound the schema
// wrote invalidly silently acted as a real bound.
//
// The three bound paths are checked through the parse functions they call,
// because the facet comparison treats an unparsable bound as "no opinion":
// going through Validate, a stripped and an unstripped NBSP can produce the
// same verdict for different reasons, which would not distinguish them.
func TestBoundLexicalsUseXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"

	t.Run("numeric", func(t *testing.T) {
		// The numeric bound path trims and then parses as a big.Rat.
		if got := trimXMLSpace(" \t10\n"); got != "10" {
			t.Errorf("XML S must be trimmed from a numeric bound: %q", got)
		}
		if got := trimXMLSpace(nbsp + "10"); got != nbsp+"10" {
			t.Errorf("an NBSP must survive in a numeric bound: %q", got)
		}
	})

	t.Run("temporal", func(t *testing.T) {
		if _, ok := parseTemporal(trimXMLSpace(" 2026-06-15\t"), "date"); !ok {
			t.Error("XML S around a date bound must be trimmed")
		}
		if _, ok := parseTemporal(trimXMLSpace(nbsp+"2026-06-15"), "date"); ok {
			t.Error("an NBSP must make a date bound unparsable")
		}
	})

	t.Run("duration", func(t *testing.T) {
		if _, ok := parseDuration(trimXMLSpace(" P10D\n")); !ok {
			t.Error("XML S around a duration bound must be trimmed")
		}
		if _, ok := parseDuration(trimXMLSpace(nbsp + "P10D")); ok {
			t.Error("an NBSP must make a duration bound unparsable")
		}
	})
}

// expandFacetQName and resolveInstanceQName trim their value before splitting
// off the prefix. That trim is the whiteSpace="collapse" edge trim, so it must
// take XML S only: strings.TrimSpace also stripped an NBSP, making
// "<NBSP>a" resolve to the name "a".
//
// NOTE: this pins the trim, not the whole QName lexical rule. isNCName in
// validate_attr.go accepts any rune >= 0x80 as a name character, so an NBSP
// that survives the trim is still accepted as an NCName. That is a separate
// defect in the NCName grammar and is deliberately not addressed here.
func TestFacetQNameTrimUsesXMLWhitespaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	if got := trimXMLSpace(" \t a \n"); got != "a" {
		t.Errorf("XML S must be trimmed: got %q", got)
	}
	if got := trimXMLSpace(nbsp + "a" + nbsp); got != nbsp+"a"+nbsp {
		t.Errorf("an NBSP must not be trimmed: got %q", got)
	}
}

// splitFields is the XSD package's list tokenizer and already recognizes only
// XML S. This pins that, so a later "simplification" to strings.Fields is
// caught: an NBSP inside a list item is data and must not split it.
func TestSplitFieldsIsNotUnicodeFields(t *testing.T) {
	const nbsp = "\u00a0"
	if got := splitFields("a" + nbsp + "b c"); len(got) != 2 {
		t.Errorf("splitFields = %q, want 2 tokens", got)
	}
	if got := splitFields(" a\tb\nc\r"); len(got) != 3 {
		t.Errorf("splitFields on XML S = %q, want 3 tokens", got)
	}
	if got := strings.Join(splitFields("a"+nbsp+"b"), "|"); got != "a"+nbsp+"b" {
		t.Errorf("splitFields split on an NBSP: %q", got)
	}
}
