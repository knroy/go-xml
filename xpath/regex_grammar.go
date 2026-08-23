package xpath

import (
	"fmt"
	"strings"
)

// Constructs that are legal in Perl and RE2 but not in the XML Schema regular
// expression language, which is what XPath's fn:matches, fn:replace,
// fn:tokenize and fn:analyze-string use.
//
// Appendix F's grammar is much smaller than Perl's, and the difference is not
// a matter of taste: a pattern using a construct it does not define is
// FORX0002, and a processor that quietly accepts one gives a different answer
// from every conforming processor rather than a worse one. RE2 accepts most of
// them, so they have to be refused here before the pattern reaches it.
//
// The grammar is checked rather than the translation, because the translation
// deliberately rewrites what it can; a construct that survives untouched is
// exactly the one that would be silently misread.

// checkRegexGrammar reports FORX0002 for a pattern using a construct the XML
// Schema regular expression language does not define.
func checkRegexGrammar(p string) error {
	inClass := false
	// classDepth tracks nesting inside a class, so that a subtraction's inner
	// class does not read as the outer one closing.
	classDepth := 0
	// groupStart is the offset of the first member of the innermost group
	// now open, past any leading "^". A "]" seen there closes an empty group.
	groupStart := -1

	for i := 0; i < len(p); i++ {
		c := p[i]

		if c == '\\' {
			if i+1 >= len(p) {
				return fmt.Errorf(
					"FORX0002: invalid regular expression %q: trailing backslash", p)
			}
			i++
			// \p and \P take a braced property name, and that brace is not a
			// quantifier. Skipping only the "p" left "{L}" to be read as one,
			// which refused every category escape in the language.
			if p[i] == 'p' || p[i] == 'P' {
				if i+1 < len(p) && p[i+1] == '{' {
					end := strings.IndexByte(p[i+1:], '}')
					if end < 0 {
						return fmt.Errorf(
							"FORX0002: invalid regular expression %q: "+
								"unterminated property name", p)
					}
					i += 1 + end
				}
			}
			continue
		}

		switch c {
		case '[':
			if inClass {
				classDepth++
			} else {
				inClass = true
				classDepth = 1
			}
			// Appendix F's posCharGroup needs at least one member, so a
			// group that closes with nothing in it is not in the grammar.
			// The position after any "^" is remembered so that "[^]" is
			// caught as well as "[]"; RE2 rejects those two on its own, but
			// "[a-f-[]]" and "[^-[bc]]" are the same emptiness inside a
			// subtraction, and there the rewrite dropped the empty half and
			// produced a pattern that matched rather than an error.
			groupStart = i + 1
			if i+1 < len(p) && p[i+1] == '^' {
				groupStart = i + 2
			}
			// The same emptiness on the left of a subtraction: charClassSub
			// is "(posCharGroup | negCharGroup) '-' charClassExpr", so the
			// group before the "-" must have a member. A leading "-" is
			// otherwise a literal dash, which is why only "-[" is refused.
			if groupStart+1 < len(p) && p[groupStart] == '-' && p[groupStart+1] == '[' {
				return fmt.Errorf(
					"FORX0002: invalid regular expression %q: character "+
						"class subtraction with an empty left-hand group", p)
			}
			// POSIX collating elements and equivalence classes — "[[:alpha:]]",
			// "[[=a=]]", "[[.a.]]" — are not in Appendix F's grammar. RE2
			// accepts the first, which made a pattern using it match
			// something rather than be refused.
			//
			// Only a class opening *inside* another one can be POSIX syntax,
			// and only when it is not the inner class of a subtraction:
			// "[\i-[:]]" is the subtraction every XML Name pattern uses, and
			// its inner class legitimately begins with a colon. The
			// distinguishing mark is the "-" that must precede a
			// subtraction's inner class.
			if classDepth > 1 && i > 0 && p[i-1] != '-' && i+1 < len(p) {
				switch p[i+1] {
				case ':', '=', '.':
					return fmt.Errorf(
						"FORX0002: invalid regular expression %q: POSIX "+
							"class syntax %q is not in the XML Schema grammar",
						p, "[["+string(p[i+1]))
				}
			}
		case ']':
			if inClass {
				if i == groupStart {
					return fmt.Errorf(
						"FORX0002: invalid regular expression %q: empty "+
							"character class", p)
				}
				classDepth--
				if classDepth <= 0 {
					inClass = false
				}
			} else {
				// An unescaped "]" outside a class has no meaning in the
				// grammar: Appendix F lists it among the characters that must
				// be escaped. Perl treats a stray one as a literal.
				return fmt.Errorf(
					"FORX0002: invalid regular expression %q: %q must be "+
						"escaped outside a character class", p, "]")
			}
		case '(':
			if inClass {
				break
			}
			// Appendix F has exactly one parenthesised form, the capturing
			// group. Every "(?" construct — non-capturing groups, inline flag
			// settings, named groups, lookaround, atomic groups — belongs to
			// Perl and is absent from the grammar.
			if i+1 < len(p) && p[i+1] == '?' {
				return fmt.Errorf(
					"FORX0002: invalid regular expression %q: %q is not in "+
						"the XML Schema grammar (only capturing groups exist)",
					p, groupPrefix(p[i:]))
			}
		case '{':
			if inClass {
				break
			}
			if err := checkQuantifier(p, i); err != nil {
				return err
			}
		}
	}
	if inClass {
		return fmt.Errorf(
			"FORX0002: invalid regular expression %q: unterminated character class", p)
	}
	return nil
}

// groupPrefix returns a short readable form of a "(?..." construct, for the
// error message.
func groupPrefix(s string) string {
	if len(s) > 4 {
		s = s[:4]
	}
	return s
}

// checkQuantifier verifies a "{...}" quantifier against the grammar.
//
// Appendix F's quantity is "{" n ("," m?)? "}", with n required. Perl accepts
// "{,2}" as a synonym for "{0,2}" and treats an unclosed "{" as a literal;
// neither is in the grammar, and both were reaching RE2, which agrees with
// Perl.
func checkQuantifier(p string, start int) error {
	end := strings.IndexByte(p[start:], '}')
	if end < 0 {
		// An unclosed "{" is a literal brace in Perl and an error here.
		return fmt.Errorf(
			"FORX0002: invalid regular expression %q: unterminated quantifier", p)
	}
	body := p[start+1 : start+end]

	// The lower bound is required, so an empty body or one starting with the
	// comma is malformed.
	if body == "" || body[0] == ',' {
		return fmt.Errorf(
			"FORX0002: invalid regular expression %q: quantifier {%s} has no "+
				"lower bound", p, body)
	}

	lo, hi, hasHi := body, "", false
	if j := strings.IndexByte(body, ','); j >= 0 {
		lo, hi, hasHi = body[:j], body[j+1:], true
	}
	n, ok := parseDigits(lo)
	if !ok {
		return fmt.Errorf(
			"FORX0002: invalid regular expression %q: quantifier {%s} is not "+
				"a number", p, body)
	}
	if hasHi && hi != "" {
		m, ok := parseDigits(hi)
		if !ok {
			return fmt.Errorf(
				"FORX0002: invalid regular expression %q: quantifier {%s} is "+
					"not a number", p, body)
		}
		// "{2,0}" cannot match anything, and the grammar requires the upper
		// bound to be at least the lower.
		if m < n {
			return fmt.Errorf(
				"FORX0002: invalid regular expression %q: quantifier {%s} has "+
					"an upper bound below its lower bound", p, body)
		}
	}
	return nil
}

// parseDigits reads a decimal bound, rejecting anything else.
func parseDigits(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
		// A bound far past any real use is capped rather than overflowed;
		// its exact value does not matter to the comparisons above.
		if n > 1<<20 {
			n = 1 << 20
		}
	}
	return n, true
}
