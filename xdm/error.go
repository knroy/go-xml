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
	// CodeName is the full QName of the code, when the error came from
	// fn:error with a QName naming a namespace other than the standard error
	// one. Code alone cannot carry it, and XSLT 3.0's $err:code is a QName.
	CodeName *QName
	// Raised marks an error that fn:error produced rather than one the
	// engine detected.
	//
	// It matters to try/catch. §3.16 catches dynamic errors only, and this
	// engine tells a static error from a dynamic one by its code, because it
	// resolves names at evaluation time and a static fault therefore arrives
	// looking dynamic. fn:error breaks that inference: it raises a *dynamic*
	// error whatever QName it is handed, so fn:error(xs:QName("err:XPST0008"))
	// is catchable even though XPST names the static family. Without this
	// flag the code heuristic refused to catch it.
	Raised bool
	// Value is the error object fn:error was given as its third argument.
	//
	// It exists for XSLT 3.0's xsl:catch, which exposes it as $err:value. Only
	// fn:error can supply one, so it is nil on every error the engine raises
	// itself, which is exactly the empty sequence the spec requires there.
	Value Sequence
	// Line and Module say where in a stylesheet the error was raised: the
	// line number within the module, and the module's URI. XSLT 3.0 section
	// 8.3 publishes them to an xsl:catch clause as $err:line-number and
	// $err:module, and both are optional there — a processor that does not
	// record the position reports the empty sequence. Line is 0 and Module
	// is "" when nothing stamped them, which is how "not recorded" is
	// spelled.
	//
	// They are stamped by the XSLT engine as the error passes out of the
	// instruction that raised it, not at the point of construction: an error
	// value is built in dozens of places across xdm, xpath and xsd, none of
	// which knows anything about a stylesheet. See xslt/execSequence.
	Line   int
	Module string
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

// ErrResourceLimit marks an error raised because the processor refused to do
// the work, not because the input was wrong.
//
// "This XPath is malformed" and "this document would have cost more than the
// processor is willing to spend" are different conditions with different
// remedies, but they arrive looking alike: a nesting guard reports XPST0003
// because that is the code the specs give for a syntactically unacceptable
// expression, and an embedding caller reading the code alone concludes the
// user's expression was invalid when in fact it was merely deep. The specs
// give no code for "I gave up", so the code cannot say this; a sentinel can.
//
// Wrap with %w to add it, never to replace the code:
//
//	fmt.Errorf("XPST0003: expression nesting exceeds %d levels: %w",
//		maxParseDepth, ErrResourceLimit)
//
// The rendered message still begins with the code, so ErrorCode and the
// conformance suites, which read the code out of the message, are unaffected,
// while a caller can now ask errors.Is(err, ErrResourceLimit) and back off
// rather than reporting a syntax error to its user.
var ErrResourceLimit = errors.New("processor resource limit exceeded")
