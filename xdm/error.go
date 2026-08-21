package xdm

import (
	"errors"
	"fmt"
	"strings"
)

// Error is an XPath, XQuery or XSLT error carrying its specification error
// code.
//
// The specs define a code for every error condition — XPTY0004 for a type
// error, FORG0001 for a failed cast, FODC0002 for an unretrievable document —
// and those codes are the stable, translatable part of an error. A message is
// prose that may be reworded; a code is what a caller can branch on and what a
// conformance suite compares.
//
// The codes were already present as string prefixes on every error this engine
// produces, which reads correctly but cannot be inspected: a caller wanting to
// distinguish "the document was malformed" from "the stylesheet is wrong" had
// to match on substrings. This type makes the code a field while keeping the
// rendered message byte-identical, so nothing that reads error text changes.
type Error struct {
	// Code is the spec error code, such as "XPTY0004". Codes live in the
	// http://www.w3.org/2005/xqt-errors namespace; the local name alone is
	// carried here because it is unique across the specs and is how the
	// documents themselves refer to them.
	Code string
	// Message is the human-readable detail, without the code prefix.
	Message string
	// Err is an underlying cause, if any.
	Err error
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Errorf builds an Error with the given code.
func Errorf(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrorCode returns the spec error code carried by err, or "" if it has none.
//
// It unwraps, so a code survives being wrapped with fmt.Errorf("%w"). Errors
// produced before this type existed still carry their code as a message
// prefix, so those are recognised too rather than silently reporting "".
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return ""
	}
	// Fall back to the prefix form: "XPTY0004: ...".
	msg := err.Error()
	i := strings.Index(msg, ":")
	if i <= 0 {
		return ""
	}
	if code := msg[:i]; looksLikeErrorCode(code) {
		return code
	}
	// A wrapped error puts the code after its own prefix.
	for _, part := range strings.Split(msg, ": ") {
		if looksLikeErrorCode(part) {
			return part
		}
	}
	return ""
}

// looksLikeErrorCode reports whether s has the shape of a spec error code:
// four letters then four digits, as in XPTY0004 or FORG0001.
func looksLikeErrorCode(s string) bool {
	if len(s) != 8 {
		return false
	}
	for i := 0; i < 4; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	for i := 4; i < 8; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
