package xpath

import (
	"fmt"
	"strings"
)

// validateSchemaRegexp checks a pattern facet against the grammar of XML Schema
// Part 2 Appendix F, productions [1] through [27].
//
// The translator that follows this exists to reach RE2, and RE2 is a much more
// permissive language than Appendix F: it accepts Perl's "(?i:...)" groups, its
// lazy "a+?" quantifiers, its "{,2}" quantity and its backreferences, none of
// which the schema grammar has. Translating without checking therefore let a
// malformed pattern facet through as a working regexp, which is a silent
// widening of the schema — msData/regex has 244 schemas that are invalid for
// exactly this reason and that used to load.
//
// The check is deliberately a separate pass over the original source rather
// than a set of guards inside translatePattern. The translator is shared with
// fn:matches, whose language *is* the Perl-ish superset: XPath 2.0 keeps the
// anchors, the lazy quantifiers and the non-capturing groups that Appendix F
// omits. Enforcing the grammar inside the shared translator would break every
// one of those; enforcing it here keeps the restriction on the schema side
// where it belongs.
//
// The parser mirrors the numbered productions closely enough that each function
// below names the one it implements, so a future reading of the spec can be
// checked against it directly.
func validateSchemaRegexp(pattern string, xsd11 bool) error {
	p := &schemaRegexpParser{
		src:                 []rune(pattern),
		allowUnknownBlocks:  xsd11,
		allowDashAfterRange: xsd11,
	}
	if err := p.regExp(); err != nil {
		return err
	}
	if p.pos < len(p.src) {
		// The only way to stop early is an unbalanced ")": regExp consumes
		// branches until it sees one, leaving the caller to reject it.
		return p.errorf("unmatched %q", string(p.src[p.pos]))
	}
	return nil
}

type schemaRegexpParser struct {
	src []rune
	pos int

	// XSD 1.1 relaxed the block names: an unrecognised one is no longer an
	// error but a class matching every character. reK88 pins both readings,
	// expecting "invalid" under 1.0 and "valid" under 1.1 for
	// "\p{IsaA0-a9}", so the check has to know which version it is running
	// under rather than picking one.
	allowUnknownBlocks bool

	// 1.1 also relaxed where a literal "-" may sit inside a character
	// group. The two relaxations always travel together, since both are
	// simply "this is XSD 1.1", but they are named apart so each reads as
	// the rule it is.
	allowDashAfterRange bool
}

func (p *schemaRegexpParser) errorf(format string, args ...any) error {
	return fmt.Errorf("FORX0002: invalid XML Schema regular expression: "+format, args...)
}

func (p *schemaRegexpParser) eof() bool { return p.pos >= len(p.src) }

func (p *schemaRegexpParser) peek() rune {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

// regExp implements [1] regExp ::= branch ( '|' branch )*.
//
// A branch may be empty — "(a|)" and even "" are valid — so no branch is
// required to consume anything.
func (p *schemaRegexpParser) regExp() error {
	for {
		if err := p.branch(); err != nil {
			return err
		}
		if p.peek() != '|' {
			return nil
		}
		p.pos++
	}
}

// branch implements [2] branch ::= piece*.
//
// It stops at "|" and ")", the two characters that end a branch without being
// part of one; anything else is a piece.
func (p *schemaRegexpParser) branch() error {
	for !p.eof() && p.peek() != '|' && p.peek() != ')' {
		if err := p.piece(); err != nil {
			return err
		}
	}
	return nil
}

// piece implements [3] piece ::= atom quantifier?.
func (p *schemaRegexpParser) piece() error {
	if err := p.atom(); err != nil {
		return err
	}
	return p.quantifier()
}

// quantifier implements [4] quantifier ::= [?*+] | ( '{' quantity '}' ).
//
// Appendix F has no lazy form. Perl's "a+?" is a second quantifier applied to a
// piece that already has one, which [3] does not allow — a piece takes at most
// one quantifier — so the trailing "?" is rejected here rather than passed to
// RE2, which would read it as "match as few as possible" and compile happily.
func (p *schemaRegexpParser) quantifier() error {
	switch p.peek() {
	case '?', '*', '+':
		p.pos++
	case '{':
		p.pos++
		if err := p.quantity(); err != nil {
			return err
		}
		if p.peek() != '}' {
			return p.errorf("unterminated quantifier: expected %q", "}")
		}
		p.pos++
	default:
		return nil
	}
	// A quantifier may not itself be quantified. "a**" and "a+?" are both this
	// error; the second is the one the suite exercises, forty times over.
	switch p.peek() {
	case '?', '*', '+':
		return p.errorf("quantifier %q may not follow another quantifier",
			string(p.peek()))
	}
	return nil
}

// quantity implements [5] quantity ::= quantRange | quantMin | QuantExact,
// where [6] quantRange ::= QuantExact ',' QuantExact, [7] quantMin ::=
// QuantExact ',' and [8] QuantExact ::= [0-9]+.
//
// The leading QuantExact is mandatory in all three, which is what makes "a{,2}"
// invalid. The spec says so in as many words: Perl allows S{,m} and "we have,
// therefore, left this logical possibility out of the regular expression
// language defined by this specification". reC65 pins it.
func (p *schemaRegexpParser) quantity() error {
	lo, ok := p.quantExact()
	if !ok {
		return p.errorf("quantifier needs a lower bound; " +
			"Appendix F has no {,m} form")
	}
	if p.peek() != ',' {
		return nil
	}
	p.pos++
	if p.peek() == '}' {
		// quantMin: "{n,}" has no upper bound.
		return nil
	}
	hi, ok := p.quantExact()
	if !ok {
		return p.errorf("quantifier upper bound is not a number")
	}
	// The table in [3] defines S{n,m} only "for all atoms S and non-negative
	// integers n, m such that n <= m", so an inverted range is not a pattern
	// that matches nothing — it is not a pattern at all.
	if !boundsOrdered(lo, hi) {
		return p.errorf("quantifier range {%s,%s} has its bounds inverted", lo, hi)
	}
	return nil
}

// quantExact reads [8] QuantExact ::= [0-9]+, returning the digits as written.
//
// The digits are kept as a string rather than parsed to an int because a count
// may be far larger than the language needs — "a{2147483647}" is a valid
// pattern — and comparing bounds does not require converting them.
func (p *schemaRegexpParser) quantExact() (string, bool) {
	start := p.pos
	for !p.eof() && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	if p.pos == start {
		return "", false
	}
	return string(p.src[start:p.pos]), true
}

// boundsOrdered reports whether lo <= hi for two digit strings.
//
// Comparing as text avoids overflowing on counts that exceed int64: after
// leading zeros are dropped the shorter number is the smaller one, and equal
// lengths compare lexicographically.
func boundsOrdered(lo, hi string) bool {
	lo = strings.TrimLeft(lo, "0")
	hi = strings.TrimLeft(hi, "0")
	if len(lo) != len(hi) {
		return len(lo) < len(hi)
	}
	return lo <= hi
}

// atom implements [9] atom ::= Char | charClass | ( '(' regExp ')' ).
func (p *schemaRegexpParser) atom() error {
	switch c := p.peek(); c {
	case '(':
		p.pos++
		// Appendix F's only parenthesised form is a plain group. Perl's
		// extensions — "(?:...)", "(?i)", "(?<name>...)" — all begin with a
		// "?" here, and "?" is a metacharacter that must be escaped to appear
		// as itself, so there is no valid pattern this rejects. RE2 accepts
		// most of them, which is how 134 of the suite's malformed schemas
		// used to load.
		if p.peek() == '?' {
			return p.errorf("%q is not an XML Schema construct; "+
				"Appendix F has only plain %q groups", "(?", "(...)")
		}
		if err := p.regExp(); err != nil {
			return err
		}
		if p.peek() != ')' {
			return p.errorf("unterminated group: expected %q", ")")
		}
		p.pos++
		return nil

	case '[':
		return p.charClassExpr()

	case '\\':
		return p.charClassEsc()

	case '.':
		// The wildcard, [11]'s WildcardEsc.
		p.pos++
		return nil

	case ')':
		// Reached only from the top level, where no group is open.
		return p.errorf("unmatched %q", ")")

	case ']', '}':
		// [10] Char excludes "[" and "]"; a bare "]" is therefore not an atom.
		// "}" is a metacharacter by the definition in F, and although the
		// grammar's Char production does not list it, the suite treats "a]"
		// as invalid (reH19 and friends), so both are refused unescaped.
		return p.errorf("%q must be escaped to appear as itself", string(c))

	case '*', '+', '?':
		// A quantifier with no atom in front of it.
		return p.errorf("quantifier %q has nothing to repeat", string(c))

	case '{':
		// "{5" and "{5,6" are quantifiers with no atom; the suite has all
		// three truncated forms.
		return p.errorf("%q must be escaped to appear as itself", "{")

	default:
		p.pos++
		return nil
	}
}

// charClassEsc implements [23] charClassEsc ::= SingleCharEsc | MultiCharEsc |
// catEsc | complEsc.
//
// The set is closed. Perl's "\1" backreference, "\0", "\a", "\e", "\b" and an
// escaped space are all outside it, and passing them to RE2 turned several of
// them into something that compiled.
func (p *schemaRegexpParser) charClassEsc() error {
	p.pos++ // the backslash
	if p.eof() {
		return p.errorf("pattern ends with a backslash")
	}
	esc := p.src[p.pos]
	p.pos++

	switch {
	case esc == 'p' || esc == 'P':
		return p.categoryEsc(esc)

	// [24] SingleCharEsc, and the MultiCharEsc set \s \S \d \D \w \W \i \I
	// \c \C that F.1.1 adds.
	case strings.ContainsRune(`nrt\|.?*+(){}-[]^`, esc),
		strings.ContainsRune("sSdDwWiIcC", esc):
		return nil

	case esc >= '1' && esc <= '9':
		// Backreferences exist in Perl and in XPath 3, but not in Appendix F,
		// whose atom production has no form for them.
		return p.errorf("backreference \\%s is not part of the XML Schema "+
			"regular expression language", string(esc))

	default:
		return p.errorf("%q is not a valid escape", `\`+string(esc))
	}
}

// categoryEsc implements [25] catEsc ::= '\p{' charProp '}' and [26] complEsc,
// where [27] charProp ::= IsCategory | IsBlock.
//
// The name must be one the spec lists: the General Category values in F.1.1 or
// a block name from Appendix G. An unknown one is a malformed pattern, not a
// class that matches nothing.
func (p *schemaRegexpParser) categoryEsc(esc rune) error {
	if p.peek() != '{' {
		return p.errorf("\\%s must be followed by %q", string(esc), "{")
	}
	p.pos++
	start := p.pos
	for !p.eof() && p.src[p.pos] != '}' {
		p.pos++
	}
	if p.eof() {
		return p.errorf("unterminated \\%s{...}", string(esc))
	}
	name := string(p.src[start:p.pos])
	p.pos++ // the closing brace

	if strings.HasPrefix(name, "Is") {
		if _, ok := unicodeBlocks[name]; ok {
			return nil
		}
		// 1.1 relaxed *unrecognised* block names, not absent ones: "\p{Is}"
		// names no block at all and stays invalid in both versions, which is
		// what reK86 and reK87 assert alongside reK88's relaxation.
		if p.allowUnknownBlocks && name != "Is" {
			return nil
		}
		return p.errorf("unknown Unicode block %q", name)
	}
	if !validCategories[name] {
		return p.errorf("unknown Unicode category %q", name)
	}
	return nil
}

// validCategories is the "General Category" table in F.1.1.
//
// It is written out rather than taken from unicode.Categories because the two
// sets are not the same: Go's map carries entries the spec's table does not
// list, and a schema naming one of those is malformed even though Go could
// resolve it.
var validCategories = map[string]bool{
	"L": true, "Lu": true, "Ll": true, "Lt": true, "Lm": true, "Lo": true,
	"M": true, "Mn": true, "Mc": true, "Me": true,
	"N": true, "Nd": true, "Nl": true, "No": true,
	"P": true, "Pc": true, "Pd": true, "Ps": true, "Pe": true,
	"Pi": true, "Pf": true, "Po": true,
	"Z": true, "Zs": true, "Zl": true, "Zp": true,
	"S": true, "Sm": true, "Sc": true, "Sk": true, "So": true,
	"C": true, "Cc": true, "Cf": true, "Co": true, "Cn": true,
}

// charClassExpr implements [12] charClassExpr ::= '[' charGroup ']' and, below
// it, [13] charGroup ::= posCharGroup | negCharGroup | charClassSub.
func (p *schemaRegexpParser) charClassExpr() error {
	if p.peek() != '[' {
		return p.errorf("expected %q", "[")
	}
	p.pos++
	if p.peek() == '^' {
		// [15] negCharGroup ::= '^' posCharGroup.
		p.pos++
	}
	if err := p.charGroupBody(); err != nil {
		return err
	}
	if p.peek() != ']' {
		return p.errorf("unterminated character class: expected %q", "]")
	}
	p.pos++
	return nil
}

// charGroupBody reads [14] posCharGroup and the optional subtraction [16]
// charClassSub ::= ( posCharGroup | negCharGroup ) '-' charClassExpr.
//
// The group must be non-empty — posCharGroup is "( charRange | charClassEsc )+"
// — so "[]" and "[a-f-[]]" are both malformed, the latter because the class
// being subtracted is empty too.
func (p *schemaRegexpParser) charGroupBody() error {
	members := 0
	prevWasRange := false
	for {
		if p.eof() {
			return p.errorf("unterminated character class")
		}
		c := p.peek()

		if c == ']' {
			if members == 0 {
				return p.errorf("empty character class")
			}
			return nil
		}

		// A "-" immediately followed by "[" is the subtraction operator; the
		// class it introduces must be the last thing in the group.
		if c == '-' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '[' {
			if members == 0 {
				return p.errorf("character class subtraction needs a group " +
					"to subtract from")
			}
			p.pos++
			if err := p.charClassExpr(); err != nil {
				return err
			}
			if p.peek() != ']' {
				// [16] puts the subtraction last: "[a-c-[b]x]" has trailing
				// members the production does not allow.
				return p.errorf("character class subtraction must be the " +
					"last part of the group")
			}
			return nil
		}

		if c == '[' {
			// [22] XmlCharIncDash excludes "[", so a bare one inside a class
			// is malformed. RE2 reads it as a literal, which is how "[^[a-b]]"
			// used to load.
			return p.errorf("%q must be escaped inside a character class", "[")
		}

		// F.1: "the - character is a valid character range only at the
		// beginning or end of a positive character group". After a completed
		// range there is neither, so "[a-c-1-4]" is malformed — RE2 reads the
		// stray hyphen as a literal, which is how reG26 and its seven
		// siblings used to load. A hyphen that opens the group or closes it
		// is still a literal member, and both are reached through charRange
		// rather than here.
		// F.1 in 1.0: "the - character is a valid character range only at
		// the beginning or end of a positive character group". After a
		// completed range there is neither, so "[a-c-1-4]" is malformed —
		// RE2 reads the stray hyphen as a literal, which is how reG26 and
		// its siblings used to load.
		//
		// 1.1 dropped the restriction, and the suite asserts the split
		// directly: reF20, reG26 and reH19 all carry expected validity
		// "invalid" for 1.0 and "valid" for 1.1.
		//
		// A hyphen that *ends* the group is still a literal member in both
		// versions — "[a-z-]" and reF55's "[a-\}-]" — and so is the one that
		// introduces a subtraction, "[a-z--[b-z]]"; both are excluded here
		// because the next character decides, not the previous one.
		if prevWasRange && c == '-' && !p.allowDashAfterRange &&
			p.pos+1 < len(p.src) &&
			p.src[p.pos+1] != ']' && p.src[p.pos+1] != '[' &&
			!p.startsSubtraction(p.pos+1) {
			return p.errorf("%q may not follow a character range; it is a "+
				"valid member only at the start or end of the group", "-")
		}
		wasRange, err := p.charRange()
		if err != nil {
			return err
		}
		prevWasRange = wasRange
		members++
	}
}

// startsSubtraction reports whether a subtraction operator begins at i.
//
// reF56's "[a-z--[b-z]]" is valid: the range "a-z" is followed by a literal
// "-", and only the second hyphen introduces the subtraction of "[b-z]". The
// literal one is therefore at the end of its positive character group, which
// F.1 allows, and looking one character further is what tells it apart from
// reG26's stray hyphen.
func (p *schemaRegexpParser) startsSubtraction(i int) bool {
	return i+1 < len(p.src) && p.src[i] == '-' && p.src[i+1] == '['
}

// charRange implements [17] charRange ::= seRange | XmlCharIncDash, with [18]
// seRange ::= charOrEsc '-' charOrEsc.
func (p *schemaRegexpParser) charRange() (wasRange bool, err error) {
	lo, isEsc, err := p.charOrEsc()
	if err != nil {
		return false, err
	}
	// A multi-character escape is a set, not a character, so it cannot be one
	// end of a range: "[\d-x]" is malformed. A single-character escape can.
	loIsSet := isEsc && !isSingleCharEsc(lo)

	// "-" starts a range unless it is the subtraction operator or the last
	// member of the group, both of which leave it a literal.
	if p.peek() != '-' || p.pos+1 >= len(p.src) {
		return false, nil
	}
	if p.src[p.pos+1] == '[' || p.src[p.pos+1] == ']' {
		return false, nil
	}
	if loIsSet {
		return false, p.errorf("a multi-character escape may not begin a range")
	}
	p.pos++ // the hyphen

	hi, hiIsEsc, err := p.charOrEsc()
	if err != nil {
		return false, err
	}
	if hiIsEsc && !isSingleCharEsc(hi) {
		return false, p.errorf("a multi-character escape may not end a range")
	}
	// F.1's fifth bullet for s-e: "the code point of e is greater than or
	// equal to the code point of s". "[z-a]" is therefore malformed rather
	// than empty.
	if hi < lo {
		return false, p.errorf("character range %q-%q is inverted",
			string(lo), string(hi))
	}
	return true, nil
}

// charOrEsc implements [20] charOrEsc ::= XmlChar | SingleCharEsc, returning
// the character it stands for and whether it came from an escape.
//
// For a multi-character escape the rune returned is the escape letter itself,
// which isSingleCharEsc uses to tell the two apart; the caller never treats it
// as a code point in that case.
func (p *schemaRegexpParser) charOrEsc() (rune, bool, error) {
	if p.eof() {
		return 0, false, p.errorf("unterminated character class")
	}
	if p.peek() == '\\' {
		start := p.pos
		if err := p.charClassEsc(); err != nil {
			return 0, false, err
		}
		// A category escape spans several runes; only the letter matters.
		return p.src[start+1], true, nil
	}
	c := p.peek()
	p.pos++
	return c, false, nil
}

// isSingleCharEsc reports whether an escape letter denotes one character, as
// [24] SingleCharEsc does, rather than a set.
func isSingleCharEsc(esc rune) bool {
	return strings.ContainsRune(`nrt\|.?*+(){}-[]^`, esc)
}
