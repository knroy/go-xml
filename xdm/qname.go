package xdm

import "strings"

// QName is an expanded name: namespace URI plus local part, with the prefix
// retained only for serialisation.
//
// Equality in XPath is defined on (URI, Local) alone — the prefix is not part
// of the value — so Equal deliberately ignores Prefix. Keeping the prefix
// around anyway matters because a literal result element must be serialised
// with the prefix the stylesheet author wrote, not one we invent.
type QName struct {
	Prefix string
	URI    string
	Local  string
}

// Equal reports QName equality per XPath: namespace URI and local name, prefix
// ignored.
func (q QName) Equal(o QName) bool {
	return q.URI == o.URI && q.Local == o.Local
}

// Lexical returns the prefix:local form used for serialisation, or just the
// local name when there is no prefix.
func (q QName) Lexical() string {
	if q.Prefix == "" {
		return q.Local
	}
	return q.Prefix + ":" + q.Local
}

// Clark returns the {uri}local form, which is unambiguous without a namespace
// context and is therefore what error messages and map keys use.
func (q QName) Clark() string {
	if q.URI == "" {
		return q.Local
	}
	return "{" + q.URI + "}" + q.Local
}

// SplitQName splits a lexical QName into prefix and local part. It does not
// resolve the prefix; resolution needs a namespace context and is done by the
// caller that has one.
func SplitQName(s string) (prefix, local string) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

// IsNCName reports whether s is an XML non-colonised name: a name with no
// prefix, which is what an element, attribute or processing-instruction name
// must be once the prefix has been split off.
//
// It lives here rather than in a consumer because more than one caller needs
// it, and because the cost of *not* checking is that a computed name reaches
// the serialiser unvalidated. A name is written to output as-is, so a name
// holding "><script>" produces markup rather than a name — output that is
// either malformed or, in HTML, an injected element.
func IsNCName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		// The colon is in XML's NameStartChar and NameChar productions, and
		// excluded here: that exclusion is the whole of what "non-colonised"
		// means.
		if r == ':' {
			return false
		}
		if i == 0 {
			if !isNameStartRune(r) {
				return false
			}
			continue
		}
		if !isNameRune(r) {
			return false
		}
	}
	return true
}

// isNameStartRune and isNameRune are the XML NameStartChar and NameChar
// productions of XML 1.0 fifth edition, transcribed.
//
// The ranges are written out rather than approximated by "anything above
// Latin-1". The difference matters: NameStartChar deliberately excludes the
// combining marks and the digits, so U+0E35 THAI CHARACTER SARA II is a legal
// character *within* a name and an illegal one to begin it. A schema language
// that gets this wrong accepts names no conforming parser will produce.
func isNameStartRune(r rune) bool {
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

func isNameRune(r rune) bool {
	if isNameStartRune(r) {
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

// Well-known namespace URIs used throughout the engine.
const (
	NSXSL    = "http://www.w3.org/1999/XSL/Transform"
	NSXML    = "http://www.w3.org/XML/1998/namespace"
	NSXMLNS  = "http://www.w3.org/2000/xmlns/"
	NSXS     = "http://www.w3.org/2001/XMLSchema"
	NSXSI    = "http://www.w3.org/2001/XMLSchema-instance"
	NSFN     = "http://www.w3.org/2005/xpath-functions"
	NSMath   = "http://www.w3.org/2005/xpath-functions/math"
	NSArray  = "http://www.w3.org/2005/xpath-functions/array"
	NSMap    = "http://www.w3.org/2005/xpath-functions/map"
	NSErr    = "http://www.w3.org/2005/xqt-errors"
	NSSVRL   = "http://purl.oclc.org/dsdl/svrl"
	NSSchema = "http://purl.oclc.org/dsdl/schematron"

	// NSGoxslt is this engine's extension namespace. Extensions live outside
	// the fn: namespace so that a stylesheet written for another processor
	// cannot silently pick one up in place of a standard function, and so
	// that a stylesheet using them is visibly engine-specific.
	NSGoxslt = "https://github.com/knroy/go-xml"
)
