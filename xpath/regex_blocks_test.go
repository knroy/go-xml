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
		tr, err := translatePattern(c.pat, false, XPath20)
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
	if _, err := translatePattern(`\p{IsNoSuchBlock}`, false, XPath20); err == nil {
		t.Fatal("unknown block translated; want FORX0002")
	}
}

// "[\i-[:]][\c-[:]]*" is how a schema spells an XML NCName: a Name character
// minus the colon. It needs the left operand of a subtraction to be a shorthand
// class, which means recovering it from the source — by this point \i and \c
// have been rewritten into RE2 bodies that cannot be read back.
func TestSubtractionOverShorthandClasses(t *testing.T) {
	const ncname = `^[\i-[:]][\c-[:]]*$`
	tr, err := translatePattern(ncname, false, XPath20)
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
	for _, c := range []struct {
		pat, in string
		want    bool
	}{
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
		tr, err := translatePattern(c.pat, false, XPath20)
		if err != nil {
			t.Errorf("%q: %v", c.pat, err)
			continue
		}
		re, err := regexp.Compile(tr)
		if err != nil {
			t.Errorf("%q->%q: %v", c.pat, tr, err)
			continue
		}
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
	tr, err := translatePattern(`^\p{IsSpecials}+$`, false, XPath20)
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

// Appendix F defines \w by subtraction — everything outside \p{P}, \p{Z} and
// \p{C} — which has no single property to name it, so inside a character class
// the escape used to fall back to RE2's own ASCII \w.
//
// That made the two spellings disagree with each other, which is how it was
// found: "`" is Sk, a symbol, so it matches \w but did not match [\w]; "_" is
// Pc, punctuation, so it does the opposite. Neither of those is the ASCII
// intuition, which is exactly why the fallback looked reasonable.
func TestWordClassAgreesWithItself(t *testing.T) {
	for _, c := range []struct {
		r     rune
		match bool
		why   string
	}{
		{'a', true, "Ll"},
		{'0', true, "Nd"},
		{'`', true, "Sk — a symbol, not punctuation"},
		{'_', false, "Pc — punctuation, despite ASCII intuition"},
		{'-', false, "Pd"},
		{'.', false, "Po"},
		{' ', false, "Zs"},
	} {
		for _, pat := range []string{`^\w$`, `^[\w]$`} {
			tr, err := translatePattern(pat, false, XPath20)
			if err != nil {
				t.Errorf("%s: translate: %v", pat, err)
				continue
			}
			re, err := regexp.Compile(tr)
			if err != nil {
				t.Errorf("%s: compile: %v", pat, err)
				continue
			}
			if got := re.MatchString(string(c.r)); got != c.match {
				t.Errorf("%s on %q = %v, want %v (%s)",
					pat, c.r, got, c.match, c.why)
			}
		}
	}
}

// A Name character on a supplementary plane matches \i and \c. The class
// bodies used inside another class were written out by hand and omitted
// \x{10000}-\x{EFFFF}, while the ranges used elsewhere included it — so the
// two forms disagreed above the BMP, which is what saxonData's xv100 catches.
//
// Both are now derived from the same ranges, so there is one source of truth.
func TestNameCharsAboveTheBMP(t *testing.T) {
	// No "^...$" here: TranslateSchemaRegexp makes those literal, and the
	// \A...\z wrapping below is what anchors a schema pattern.
	for _, pat := range []string{`[\i]+`, `\i+`, `[\c]+`, `\c+`} {
		tr, err := TranslateSchemaRegexp(pat)
		if err != nil {
			t.Errorf("%s: %v", pat, err)
			continue
		}
		re, err := regexp.Compile(`\A(?:` + tr + `)\z`)
		if err != nil {
			t.Errorf("%s: compile: %v", pat, err)
			continue
		}
		for _, r := range []rune{0x10000, 0x10064, 0xEFFFF} {
			if !re.MatchString(string(r)) {
				t.Errorf("%s does not match %U", pat, r)
			}
		}
		// Still bounded: EFFFF is the top of the production.
		if re.MatchString(string(rune(0xF0000))) {
			t.Errorf("%s matches %U, past the end of NameStartChar",
				pat, rune(0xF0000))
		}
	}
}

// The "i" flag must not reach inside a category escape, and a character class
// is not an exception. The spec is explicit: "All other constructs are
// unaffected by the 'i' flag. For example, '\p{Lu}' continues to match
// upper-case letters only."
//
// Outside a class that is a "(?-i:...)" group. Inside one it could not be:
// RE2 reads "(?-i:...)" between brackets as the literal characters that spell
// it, so "[(?-i:\p{Lu})]" matches "?" and ":", and "[(?-i:\P{L})*]" matches
// every character. The escape was contributed bare instead, which avoided that
// misparse but dropped the case pinning with it — a documented trade-off, but
// one that made matches('a','[\p{Lu}]','i') return true. A wrong boolean, not
// a wrong error, and silent.
//
// The category is now expanded into codepoint ranges, so there is no property
// reference left for the flag to reach into.
func TestCategoryEscapeIgnoresCaseFlagInsideClass(t *testing.T) {
	for _, c := range []struct{ expr, want string }{
		// The class form and the bare form must now agree.
		{`matches('a', '^[\p{Lu}]$', 'i')`, "false"},
		{`matches('A', '^[\p{Lu}]$', 'i')`, "true"},
		{`matches('A', '^[\p{Ll}]$', 'i')`, "false"},
		{`matches('a', '^[\p{Ll}]$', 'i')`, "true"},
		{`matches('a', '^\p{Lu}$', 'i')`, "false"},
		{`matches('A', '^\p{Lu}$', 'i')`, "true"},

		// Without the flag nothing changed: the common path still works.
		{`matches('A', '^[\p{Lu}]$')`, "true"},
		{`matches('a', '^[\p{Lu}]$')`, "false"},
		{`matches('a', '^[\p{Ll}]$')`, "true"},
		{`matches('5', '^[\p{Nd}]$')`, "true"},
		{`matches('a', '^[\p{Nd}]$')`, "false"},

		// A category alongside other members of the class, which is what
		// makes the bare contribution necessary in the first place.
		{`matches('*', '^[\p{Lu}*]$', 'i')`, "true"},
		{`matches('A', '^[\p{Lu}*]$', 'i')`, "true"},
		{`matches('a', '^[\p{Lu}*]$', 'i')`, "false"},

		// A negated category inside a class. "[\P{L}]" is the complement of
		// the letters, computed here rather than left as a flag-sensitive \P.
		{`matches('5', '^[\P{L}]$', 'i')`, "true"},
		{`matches('a', '^[\P{L}]$', 'i')`, "false"},
		{`matches('A', '^[\P{L}]$', 'i')`, "false"},
		{`matches('5', '^[\P{L}]$')`, "true"},
		{`matches('a', '^[\P{L}]$')`, "false"},
		{`matches('a', '^[\P{Lu}]$', 'i')`, "true"},
		{`matches('A', '^[\P{Lu}]$', 'i')`, "false"},

		// The misparse the old comment warned about. Under a naive
		// "(?-i:...)" inside the brackets this class became "any of
		// ( ? - i : \ P { L } * )", which matched "A" and every other
		// character. It must match the non-letters and "*", nothing else.
		{`matches('A', '^[\P{L}*]$', 'i')`, "false"},
		{`matches('a', '^[\P{L}*]$', 'i')`, "false"},
		{`matches('*', '^[\P{L}*]$', 'i')`, "true"},
		{`matches('5', '^[\P{L}*]$', 'i')`, "true"},
		// "?" and ":" do belong to this class — they are not letters, so
		// \P{L} covers them — but "i" and "P" are letters and must not,
		// which is exactly what the misparse got wrong: it admitted every
		// character that spells "(?-i:\P{L})".
		{`matches('i', '^[\P{L}*]$', 'i')`, "false"},
		{`matches('P', '^[\P{L}*]$', 'i')`, "false"},
		{`matches('L', '^[\P{L}*]$', 'i')`, "false"},

		// A negated class holding a category. RE2 applies "[^...]" to the
		// whole body, so the bare ranges compose correctly.
		{`matches('a', '^[^\p{Lu}]$', 'i')`, "true"},
		{`matches('A', '^[^\p{Lu}]$', 'i')`, "false"},
		{`matches('5', '^[^\p{Lu}]$', 'i')`, "true"},

		// Subtraction reads the original source text rather than what the
		// translator emitted, so it is unaffected — with the flag and
		// without it.
		{`matches('b', '^[\p{Ll}-[aeiou]]$')`, "true"},
		{`matches('a', '^[\p{Ll}-[aeiou]]$')`, "false"},
		{`matches('b', '^[\p{Ll}-[aeiou]]$', 'i')`, "true"},
		{`matches('a', '^[\p{Ll}-[aeiou]]$', 'i')`, "false"},
		// The subtracted class stays case-pinned too: "B" is not Ll.
		{`matches('B', '^[\p{Ll}-[aeiou]]$', 'i')`, "false"},
		{`matches('c', '^[a-z-[\p{Lu}]]$', 'i')`, "true"},
	} {
		if got := evalStrXSLT(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// Two things the case-pinning of a category inside a class had to not break,
// both caught by the QT3 suite rather than by reasoning.
//
// A category is a set, so it cannot be the upper end of a range. RE2 used to
// refuse "[f-\p{Lu}]" on our behalf, because the escape was still a property
// reference when it saw it; expanding the category to ranges removed that
// accident and left a stray "f-" that would have compiled as a literal hyphen.
// These are QT3's re00589 and re00590.
//
// And the subtraction path splices "(?-i:" in front of the class it emits. The
// offset for that has to come from the code that built the class: the expanded
// body contains escaped "\[" literals, so searching backwards for a bracket
// finds one of those and cuts the class in half. That produced patterns RE2
// rejected outright, which is how the suite caught it.
func TestCategoryPinningKeepsGrammarAndBrackets(t *testing.T) {
	for _, pat := range []string{
		`([f-\p{Lu}]\w*)\s([\p{Lu}]\w*)`,
		`([1-\P{Ll}][\p{Ll}]*)\s([\P{Ll}][\p{Ll}]*)`,
	} {
		if _, err := translatePattern(pat, false, XPath31); err == nil {
			t.Errorf("%s: want FORX0002, got no error", pat)
		}
	}
	// A subtraction whose left side is a negated category: the emitted class
	// must still be a class RE2 can parse.
	for _, pat := range []string{
		`^[\P{Lu}-[ae-z]]+$`,
		`^[\P{Lu}-[A-Z]]+$`,
		`^[\P{Lu}-[\p{Lu}]]+$`,
		`^[\P{Lu}-[ae-zA-Z]]+$`,
	} {
		tr, err := translatePattern(pat, false, XPath31)
		if err != nil {
			t.Errorf("%s: translate: %v", pat, err)
			continue
		}
		if _, err := regexp.Compile("(?i)" + tr); err != nil {
			t.Errorf("%s: compile: %v", pat, err)
		}
	}
}
