package xquery

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// parser reads XQuery's own syntax directly from the source.
//
// There is no token stream. Constructor syntax is XML, and whether a character
// is markup or text depends on which construct is being read, so the state
// that a mode-switching lexer would keep is here held where it belongs: in
// which method is running. Expressions are not read here at all — they are
// handed to xpath as substrings.
type parser struct {
	src string
	pos int
	sc  *staticContext

	// version is the XPath version expressions are compiled at. XQuery 3.1's
	// expression language is XPath 3.1's.
	version xpath.Version
}

// compiledExpr is an expression compiled by xpath, kept with the source it
// came from so that an error can quote it.
type compiledExpr struct {
	src      string
	compiled *xpath.Compiled
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) lookingAt(s string) bool {
	return strings.HasPrefix(p.src[p.pos:], s)
}

func (p *parser) consume(s string) bool {
	if p.lookingAt(s) {
		p.pos += len(s)
		return true
	}
	return false
}

// skipSpace consumes XML whitespace and reports whether any was there.
func (p *parser) skipSpace() bool {
	start := p.pos
	for !p.eof() {
		switch p.src[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return p.pos > start
		}
	}
	return p.pos > start
}

func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("%s (at offset %d)", fmt.Sprintf(format, args...), p.pos)
}

// scanNCName reads an NCName, returning "" if there is not one here.
func (p *parser) scanNCName() string {
	start := p.pos
	first := true
	for !p.eof() {
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		if first {
			if !xdm.IsNameStartChar(r) || r == ':' {
				break
			}
			first = false
		} else if !xdm.IsNameChar(r) || r == ':' {
			break
		}
		p.pos += size
	}
	return p.src[start:p.pos]
}

// parseQName reads a QName as written, without resolving it. Resolution is a
// separate step because in a start tag it cannot happen until the whole
// attribute list has been seen.
func (p *parser) parseQName() (prefix, local string, err error) {
	first := p.scanNCName()
	if first == "" {
		return "", "", p.errorf("XPST0003: expected a name")
	}
	if !p.lookingAt(":") || p.lookingAt("::") {
		return "", first, nil
	}
	p.pos++
	second := p.scanNCName()
	if second == "" {
		return "", "", p.errorf("XPST0003: expected a local name after %q", ":")
	}
	return first, second, nil
}

// rawPart is one run of an attribute value: either literal text, or the source
// of an enclosed expression.
type rawPart struct {
	text     string
	enclosed bool
}

// rawAttribute is an attribute as written, before its name is resolved.
type rawAttribute struct {
	prefix, local string
	value         []rawPart
}

// scanAttributes reads a start tag's attribute list and its closing ">" or
// "/>", without resolving any name.
//
// Names are left unresolved because a namespace declaration later in the list
// governs how names earlier in it resolve — including the element's own.
func (p *parser) scanAttributes() (attrs []rawAttribute, selfClosing bool, err error) {
	for {
		hadSpace := p.skipSpace()
		switch {
		case p.consume("/>"):
			return attrs, true, nil
		case p.consume(">"):
			return attrs, false, nil
		case p.eof():
			return nil, false, p.errorf("XPST0003: unterminated start tag")
		}
		// XML requires whitespace between attributes, and between the name
		// and the first attribute.
		if !hadSpace {
			return nil, false, p.errorf(
				"XPST0003: expected space before an attribute")
		}
		prefix, local, err := p.parseQName()
		if err != nil {
			return nil, false, err
		}
		p.skipSpace()
		if !p.consume("=") {
			return nil, false, p.errorf("XPST0003: expected %q after %q",
				"=", qnameText(prefix, local))
		}
		p.skipSpace()
		value, err := p.scanAttributeValue()
		if err != nil {
			return nil, false, err
		}
		attrs = append(attrs, rawAttribute{prefix: prefix, local: local,
			value: value})
	}
}

// scanAttributeValue reads a quoted attribute value into its literal and
// enclosed parts.
//
// The escapes are XQuery's, not XML's: a doubled quote of the kind that opened
// the value stands for one, and doubled braces stand for one brace. Character
// and entity references are expanded here, and the result is normalised the
// way XML normalises a CDATA attribute — every tab, carriage return and line
// feed becomes a space, with no trimming, because the value is not of a type
// that would justify it.
func (p *parser) scanAttributeValue() ([]rawPart, error) {
	if p.eof() {
		return nil, p.errorf("XPST0003: expected an attribute value")
	}
	quote := p.src[p.pos]
	if quote != '"' && quote != '\'' {
		return nil, p.errorf("XPST0003: an attribute value must be quoted")
	}
	p.pos++

	var parts []rawPart
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			parts = append(parts, rawPart{text: normalizeAttr(run.String())})
			run.Reset()
		}
	}
	for {
		if p.eof() {
			return nil, p.errorf("XPST0003: unterminated attribute value")
		}
		c := p.src[p.pos]
		switch {
		case c == quote:
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == quote {
				// A doubled quote stands for one.
				run.WriteByte(quote)
				p.pos += 2
				continue
			}
			p.pos++
			flush()
			return parts, nil

		case p.lookingAt("{{"):
			run.WriteByte('{')
			p.pos += 2

		case p.lookingAt("}}"):
			run.WriteByte('}')
			p.pos += 2

		case c == '}':
			return nil, p.errorf(
				"XPST0003: %q must be written %q in an attribute value",
				"}", "}}")

		case c == '{':
			flush()
			end, err := findEnclosed(p.src, p.pos)
			if err != nil {
				return nil, err
			}
			parts = append(parts, rawPart{text: p.src[p.pos+1 : end],
				enclosed: true})
			p.pos = end + 1

		case c == '&':
			text, err := p.parseReference()
			if err != nil {
				return nil, err
			}
			run.WriteString(text)

		case c == '<':
			return nil, p.errorf(
				"XPST0003: %q is not allowed in an attribute value", "<")

		default:
			run.WriteByte(c)
			p.pos++
		}
	}
}

// normalizeAttr applies XML attribute-value normalisation for a CDATA-typed
// attribute: every whitespace character becomes a space, and nothing is
// trimmed or collapsed.
func normalizeAttr(s string) string {
	if !strings.ContainsAny(s, "\t\r\n") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\t', '\n':
			sb.WriteByte(' ')
		case '\r':
			sb.WriteByte(' ')
			// A CRLF pair normalises to one space, not two.
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
		default:
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

// predefined are the five entity references XML defines and XQuery inherits.
// A query has no DTD, so these are the only named ones it may use.
var predefined = map[string]string{
	"lt": "<", "gt": ">", "amp": "&", "quot": "\"", "apos": "'",
}

// parseReference expands a character or predefined entity reference.
func (p *parser) parseReference() (string, error) {
	start := p.pos
	if !p.consume("&") {
		return "", p.errorf("XPST0003: expected %q", "&")
	}
	if p.consume("#") {
		var digits string
		base := 10
		if p.consume("x") {
			base = 16
			digits = p.scanWhile(isHexDigit)
		} else {
			digits = p.scanWhile(isDigit)
		}
		if digits == "" || !p.consume(";") {
			return "", p.errorAt(start, "XPST0003: malformed character reference")
		}
		n, err := strconv.ParseInt(digits, base, 64)
		if err != nil || !isXMLChar(rune(n)) {
			return "", p.errorAt(start,
				"XQST0090: %q is not a valid XML character", "&#"+digits+";")
		}
		return string(rune(n)), nil
	}
	name := p.scanNCName()
	if name == "" || !p.consume(";") {
		return "", p.errorAt(start, "XPST0003: malformed entity reference")
	}
	text, ok := predefined[name]
	if !ok {
		return "", p.errorAt(start,
			"XPST0003: %q is not a predefined entity reference", "&"+name+";")
	}
	return text, nil
}

func (p *parser) scanWhile(ok func(byte) bool) string {
	start := p.pos
	for !p.eof() && ok(p.src[p.pos]) {
		p.pos++
	}
	return p.src[start:p.pos]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// isXMLChar reports whether a codepoint may appear in an XML 1.0 document.
func isXMLChar(r rune) bool {
	switch {
	case r == 0x9 || r == 0xA || r == 0xD:
		return true
	case r >= 0x20 && r <= 0xD7FF:
		return true
	case r >= 0xE000 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0x10FFFF:
		return true
	}
	return false
}

// compileExpr hands an expression to xpath.
//
// The one thing that cannot simply be delegated is the namespace axis: XPath
// has it and XQuery does not, so a query using it must be refused even though
// the expression parser beneath would accept it. Everything else about the
// expression language is shared, which is why this is the only such check.
func (p *parser) compileExpr(src string) (*compiledExpr, error) {
	if err := rejectNamespaceAxis(src); err != nil {
		return nil, err
	}
	c, err := xpath.CompileVersion(src, p.sc, p.version)
	if err != nil {
		return nil, err
	}
	return &compiledExpr{src: src, compiled: c}, nil
}

// rejectNamespaceAxis refuses the namespace axis, which XQuery does not have.
//
// The test is lexical because the expression has not been parsed yet, and it
// deliberately does not fire inside a string literal or a comment, where
// "namespace::" is just text.
func rejectNamespaceAxis(src string) error {
	for i := 0; i+len("namespace::") <= len(src); i++ {
		switch src[i] {
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				// Let the expression parser report it properly.
				return nil
			}
			i = end
			continue
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					return nil
				}
				i = end
				continue
			}
		}
		if !strings.HasPrefix(src[i:], "namespace::") {
			continue
		}
		// A name may end in "namespace" — "xml-namespace::" is not the axis.
		if i > 0 {
			r, _ := utf8.DecodeLastRuneInString(src[:i])
			if xdm.IsNameChar(r) {
				continue
			}
		}
		return fmt.Errorf(
			"XPST0003: XQuery has no %q axis", "namespace")
	}
	return nil
}
