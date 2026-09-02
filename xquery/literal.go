package xquery

import "strings"

// Character and entity references inside string literals.
//
// XQuery's StringLiteral is not XPath's. §3.1.1 admits a PredefinedEntityRef
// and a CharRef inside one — "&amp;", "&#8364;" — where XPath's grammar has
// neither and leaves both as the characters they are spelled with. Expressions
// go to xpath as substrings, so without a pass here "&amp;" evaluates to the
// five characters "&amp;" rather than to "&", and the malformed forms the
// suite requires XPST0003 for evaluate as text instead of being refused.
//
// The expansion is done on the source, before the expression is handed over,
// rather than after: an expanded reference must be indistinguishable from the
// character written directly, and the only way to guarantee that through a
// parser this package does not own is to give it the character. XPath does not
// interpret "&" at all, so an expanded "&" cannot be re-read as a reference;
// the one character that needs care is the quote closing the literal, and the
// doubling that is XQuery's own escape for it is XPath's too.

// expandStringLiterals rewrites every string literal in an expression's source
// with its character and entity references expanded.
//
// Everything outside a literal is copied through untouched — an "&" is not a
// reference anywhere else in an expression, and a comment may hold whatever it
// likes. A literal with no "&" in it is copied through as well, so the common
// expression pays nothing for this.
func (p *parser) expandStringLiterals(src string) (string, error) {
	if !strings.Contains(src, "&") {
		return src, nil
	}
	var out strings.Builder
	copied := 0
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '(':
			// A comment. Its content is not source and holds no literals.
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					// Let the expression parser report it in its own words.
					return src, nil
				}
				i = end
			}
		case 'Q':
			// A braced URI literal. §3.1.1's PredefinedEntityRef and CharRef
			// are admitted inside one exactly as they are inside a string
			// literal — eqname-029 writes Q{http:&#x2F;&#x2F;…}pi() and
			// expects the function it names to be found — and xpath, whose
			// grammar has neither, would otherwise carry the reference into
			// the URI as the characters it is spelled with.
			//
			// The "Q" must open a braced URI and not merely be the first
			// letter of a name: Qname is an NCName, and rewriting inside it
			// would corrupt an ordinary identifier. A preceding name
			// character is the test, since Q{ can only start a braced URI
			// where a name could not already be running.
			if i+1 >= len(src) || src[i+1] != '{' {
				continue
			}
			if i > 0 && isNameByte(src[i-1]) {
				continue
			}
			end := strings.IndexByte(src[i+2:], '}')
			if end < 0 {
				// Unterminated; let the expression parser say so.
				return src, nil
			}
			end += i + 2
			if strings.IndexByte(src[i+2:end], '&') < 0 {
				i = end
				continue
			}
			// No quote character closes a braced URI, so nothing expanded
			// needs re-escaping.
			text, err := p.expandLiteral(src[i+2:end], 0)
			if err != nil {
				return "", err
			}
			// A brace cannot be smuggled in as a reference. eqname-909 writes
			// Q{&#x7D;http://…}pi() and requires an error: the expansion is
			// not a way to put a "}" inside a braced URI, because the literal
			// ends at the first one however it is spelled. Writing the
			// expanded brace through would instead have quietly produced a
			// *different*, well-formed URI and found the function.
			//
			// XQST0046 is the code for a braced URI that is not a valid URI,
			// and the one the case admits alongside the XPST0017 an unknown
			// function would give.
			if strings.ContainsAny(text, "{}") {
				return "", p.errorAt(i,
					"XQST0046: a brace in a braced URI literal cannot be "+
						"written as a character reference")
			}
			out.WriteString(src[copied:i])
			out.WriteString("Q{")
			out.WriteString(text)
			copied = end
			i = end - 1
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				return src, nil
			}
			if strings.IndexByte(src[i:end], '&') < 0 {
				i = end
				continue
			}
			text, err := p.expandLiteral(src[i+1:end], src[i])
			if err != nil {
				return "", err
			}
			out.WriteString(src[copied:i])
			out.WriteByte(src[i])
			out.WriteString(text)
			out.WriteByte(src[i])
			copied = end + 1
			i = end
		}
	}
	if copied == 0 {
		return src, nil
	}
	out.WriteString(src[copied:])
	return out.String(), nil
}

// expandLiteral expands the references in one literal's body, re-escaping
// whatever the expansion produced that the literal's own quoting would
// otherwise read as syntax.
//
// quote is the character that opened the literal. A "&quot;" inside a
// double-quoted one expands to that character, which would close the literal
// where it stands, so it is written doubled — the escape XQuery defines for it
// and the one XPath reads the same way. A doubled quote already in the source
// is left as it is: it is already the escape, and expanding it here would
// change the literal's value.
func (p *parser) expandLiteral(body string, quote byte) (string, error) {
	var sb strings.Builder
	sb.Grow(len(body))
	// A sub-parser over the body gives the reference syntax, the XML
	// character-range check and the error codes — XPST0003 for a malformed
	// reference, XQST0090 for one naming a codepoint XML does not have — from
	// the one implementation that already has them.
	sub := &parser{src: body, sc: p.sc, version: p.version}
	for sub.pos < len(body) {
		c := body[sub.pos]
		if c != '&' {
			sb.WriteByte(c)
			sub.pos++
			continue
		}
		text, err := sub.parseReference()
		if err != nil {
			return "", err
		}
		for i := 0; i < len(text); i++ {
			if text[i] == quote {
				sb.WriteByte(quote)
			}
			sb.WriteByte(text[i])
		}
	}
	return sb.String(), nil
}
