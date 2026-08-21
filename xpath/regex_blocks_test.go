package xpath

import (
	"regexp"
	"testing"
)

// Unicode block names and class subtraction over shorthand classes are the two
// pattern constructs XML Schema defines that RE2 has no syntax for. Both are
// translated rather than refused; these pin the translation, since a class that
// silently matched the wrong set would be far worse than one that failed to
// compile.
func TestUnicodeBlockPatterns(t *testing.T) {
	cases := []struct {
		pat   string
		in    string
		match bool
	}{
		{`^\p{IsBasicLatin}+$`, "hello", true},
		{`^\p{IsBasicLatin}+$`, "héllo", false},
		{`^\p{IsTibetan}+$`, "ༀཀ", true},
		{`^\p{IsTibetan}+$`, "a", false},
		{`^\p{IsGreek}+$`, "α", true},
		{`^\p{IsPrivateUse}+$`, "\uE000\uF8FF", true},
		{`^\p{IsPrivateUse}+$`, "a", false},

		// A block inside a class contributes its range bare. Emitting a
		// bracketed range here would nest a "[", which RE2 reads as a
		// literal rather than as a class.
		{`^[\p{IsBasicLatin}\p{IsGreek}]+$`, "aα", true},
		{`^[\p{IsBasicLatin}]+$`, "α", false},
	}
	for _, c := range cases {
		tr, err := translatePattern(c.pat, false)
		if err != nil {
			t.Errorf("%q: translate: %v", c.pat, err)
			continue
		}
		re, err := regexp.Compile(tr)
		if err != nil {
			t.Errorf("%q -> %q: compile: %v", c.pat, tr, err)
			continue
		}
		if got := re.MatchString(c.in); got != c.match {
			t.Errorf("%q on %q = %v, want %v (translated %q)",
				c.pat, c.in, got, c.match, tr)
		}
	}
}

// An unknown block name is an error rather than something passed through to
// RE2. Emitting \p{IsFoo} verbatim made the whole pattern fail to compile, so
// the schema carrying it failed to load — a schema-level error a long way from
// its cause.
func TestUnknownUnicodeBlockIsRejected(t *testing.T) {
	if _, err := translatePattern(`\p{IsNoSuchBlock}`, false); err == nil {
		t.Fatal("unknown block translated; want FORX0002")
	}
}

// "[\i-[:]][\c-[:]]*" is how a schema spells an XML NCName: a Name character
// minus the colon. It needs the left operand of a subtraction to be a shorthand
// class, which means recovering it from the source — by this point \i and \c
// have been rewritten into RE2 bodies that cannot be read back.
func TestSubtractionOverShorthandClasses(t *testing.T) {
	const ncname = `^[\i-[:]][\c-[:]]*$`
	tr, err := translatePattern(ncname, false)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	re, err := regexp.Compile(tr)
	if err != nil {
		t.Fatalf("compile %q: %v", tr, err)
	}
	for _, c := range []struct {
		in    string
		match bool
	}{
		{"abc", true},
		{"a-b.9", true},
		{"a:b", false}, // the colon is what the subtraction removes
		{":ab", false},
		{"9ab", false}, // a digit may not start a Name
	} {
		if got := re.MatchString(c.in); got != c.match {
			t.Errorf("NCName pattern on %q = %v, want %v", c.in, got, c.match)
		}
	}
}

// RE2 has no way to complement a class from inside one, and no class-level
// complement at all, so \I, \C and \P{...} used within a class are computed
// here and their ranges contributed bare.
//
// Every one of these spans the surrogate block, which is why they are also the
// cases that pin the hex escaping: string(r) on a surrogate yields U+FFFD,
// which silently changes the range and can make RE2 reject the class.
func TestComplementedClassEscapes(t *testing.T) {
	for _, c := range []struct{ pat, in string; want bool }{
		{`^[\C]+$`, "!", true},
		{`^[\C]+$`, "a", false},
		{`^[\C]+$`, ":", false},
		{`^[\C\?a-c\?]+$`, "abc", true},
		{`^[\C\?a-c\?]+$`, "!", true},
		{`^[\I]+$`, "1", true},
		{`^[\I]+$`, "a", false},
		{`^[^\P{IsBasicLatin}]$`, "a", true},
		{`^[^\P{IsBasicLatin}]$`, "é", false},
		{`^[\P{IsBasicLatin}]$`, "é", true},
		{`^[\P{IsBasicLatin}]$`, "a", false},
	} {
		tr, err := translatePattern(c.pat, false)
		if err != nil { t.Errorf("%q: %v", c.pat, err); continue }
		re, err := regexp.Compile(tr)
		if err != nil { t.Errorf("%q->%q: %v", c.pat, tr, err); continue }
		if got := re.MatchString(c.in); got != c.want {
			t.Errorf("%q on %q = %v want %v (%q)", c.pat, c.in, got, c.want, tr)
		}
	}
}

// A Unicode block is not always one contiguous range. Specials, in the
// Unicode 3.1 definition Appendix G pins, is FEFF together with FFF0-FFFD —
// FEFF sits on its own, separated from the rest by the Arabic Presentation
// Forms-B block that ends at FEFE.
//
// The table originally held one range per block and recorded only the FEFF
// half, so \p{IsSpecials} matched a single character and rejected every
// codepoint the block is actually about.
func TestSpecialsIsTwoRanges(t *testing.T) {
	tr, err := translatePattern(`^\p{IsSpecials}+$`, false)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	re, err := regexp.Compile(tr)
	if err != nil {
		t.Fatalf("compile %q: %v", tr, err)
	}
	for _, c := range []struct {
		r     rune
		match bool
	}{
		{0xFEFF, true},  // the lone low half
		{0xFFF0, true},  // the start of the high half
		{0xFFFD, true},  // its end
		{0xFEFE, false}, // Arabic Presentation Forms-B, the block below
		{0xFFFE, false}, // a noncharacter, outside the 3.1 block
		{0xFFFF, false},
	} {
		if got := re.MatchString(string(c.r)); got != c.match {
			t.Errorf("%U = %v, want %v", c.r, got, c.match)
		}
	}
}
