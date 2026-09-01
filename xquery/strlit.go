package xquery

import (
	"strings"
)

// resolveStringLiterals rewrites the character and predefined entity
// references inside an expression's string literals.
//
// XQuery §3.1.1 admits them there and XPath does not: "&amp;" is one
// character in a query and five in an XPath expression. Every expression this
// package does not read itself is handed to the XPath parser, which would
// therefore see the reference as literal text and hand back a value the query
// never wrote -- attribute a { "x&lt;y" } produced the six characters "x&lt;y"
// rather than the three "x<y", and serialising that escaped the ampersand a
// second time.
//
// The rewrite happens on the source rather than after parsing because a string
// literal's value is settled by then and nothing downstream knows it came from
// a query. Only the insides of literals are touched: an "&" anywhere else is
// not a reference in either language, and rewriting one would change an
// expression rather than a value.
//
// A resolved character is re-escaped so that the result is still one literal:
// "&quot;" becomes a doubled quote when the literal is quote-delimited, and
// the character itself when it is not. That is what keeps the rewrite
// invisible -- the XPath parser reads the same value the XQuery rules define,
// through a spelling it can read.
func resolveStringLiterals(src string) (string, error) {
	if !strings.ContainsRune(src, '&') {
		// The overwhelmingly common case, and the scan below is not free:
		// no ampersand anywhere means no reference in any literal.
		return src, nil
	}
	var sb strings.Builder
	sb.Grow(len(src))
	for i := 0; i < len(src); {
		switch c := src[i]; {
		case c == '(' && i+1 < len(src) && src[i+1] == ':':
			// A comment. Its contents are not source and an "&" in one is
			// not a reference, so it is copied across untouched.
			end, err := skipComment(src, i)
			if err != nil {
				return "", err
			}
			sb.WriteString(src[i : end+1])
			i = end + 1
		case c == '\'' || c == '"':
			end, err := skipString(src, i)
			if err != nil {
				return "", err
			}
			body, err := resolveInLiteral(src[i+1:end], c)
			if err != nil {
				return "", err
			}
			sb.WriteByte(c)
			sb.WriteString(body)
			sb.WriteByte(c)
			i = end + 1
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return sb.String(), nil
}

// resolveInLiteral expands the references in one literal's body, quote being
// the delimiter the body will be written back inside.
func resolveInLiteral(body string, quote byte) (string, error) {
	if !strings.ContainsRune(body, '&') {
		return body, nil
	}
	var sb strings.Builder
	sb.Grow(len(body))
	for i := 0; i < len(body); {
		if body[i] != '&' {
			sb.WriteByte(body[i])
			i++
			continue
		}
		p := &parser{src: body, pos: i}
		text, err := p.parseReference()
		if err != nil {
			return "", err
		}
		// The delimiter has to go back in doubled, which is the only escape
		// an XPath string literal has. Every other character stands for
		// itself: XPath gives them no meaning inside a literal, so there is
		// nothing to escape them from.
		for _, r := range text {
			if byte(r) == quote && r < 0x80 {
				sb.WriteByte(quote)
			}
			sb.WriteRune(r)
		}
		i = p.pos
	}
	return sb.String(), nil
}
