package xslt

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// The package-version attribute's syntax, section 3.6.1.
//
// A package's version number is not free text. 3.6.1 gives it a grammar:
//
//	PackageVersion ::= NumericPart ( "-" NamePart )?
//	NumericPart    ::= IntegerLiteral ( "." IntegerLiteral )*
//	NamePart       ::= NCName
//
// "Leading and trailing whitespace is ignored; no other whitespace is
// allowed." The components are ordered, and the ordering is what decides
// which package a use-package resolves to, so a version that does not parse
// has no place in that ordering — which is why a malformed one is rejected
// rather than compared as a string.
//
// The attribute table cannot express this: its values field is a closed set
// of literal alternatives, and a version number is an open-ended grammar.

// validPackageVersion reports whether v matches the section 3.6.1 grammar.
func validPackageVersion(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	// The NamePart is separated by the first "-", not the last: an
	// IntegerLiteral has no sign, so a "-" can only ever begin the name.
	numeric := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
		numeric = v[:i]
		if !xdm.IsNCName(v[i+1:]) {
			return false
		}
	}
	if numeric == "" {
		return false
	}
	for _, part := range strings.Split(numeric, ".") {
		if !isIntegerLiteral(part) {
			return false
		}
	}
	return true
}

// isIntegerLiteral reports whether s is an XPath IntegerLiteral: one or more
// decimal digits, with no sign and no exponent. "34E9" and "-5" are rejected
// here rather than by the caller, because both are ordinary XPath numeric
// literals and only this production excludes them.
func isIntegerLiteral(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
