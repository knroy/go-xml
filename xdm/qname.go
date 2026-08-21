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

// Well-known namespace URIs used throughout the engine.
const (
	NSXSL    = "http://www.w3.org/1999/XSL/Transform"
	NSXML    = "http://www.w3.org/XML/1998/namespace"
	NSXMLNS  = "http://www.w3.org/2000/xmlns/"
	NSXS     = "http://www.w3.org/2001/XMLSchema"
	NSXSI    = "http://www.w3.org/2001/XMLSchema-instance"
	NSFN     = "http://www.w3.org/2005/xpath-functions"
	NSErr    = "http://www.w3.org/2005/xqt-errors"
	NSSVRL   = "http://purl.oclc.org/dsdl/svrl"
	NSSchema = "http://purl.oclc.org/dsdl/schematron"

	// NSGoxslt is this engine's extension namespace. Extensions live outside
	// the fn: namespace so that a stylesheet written for another processor
	// cannot silently pick one up in place of a standard function, and so
	// that a stylesheet using them is visibly engine-specific.
	NSGoxslt = "https://github.com/knroy/go-xml"
)
