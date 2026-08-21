package xdm

import (
	"errors"
	"fmt"
	"testing"
)

// The spec error code is the stable part of an error: a message is prose that
// may be reworded, a code is what a caller branches on and what a conformance
// suite compares. It must survive wrapping.
func TestErrorCodeIsInspectable(t *testing.T) {
	err := ErrType("cannot compare %s with %s", "xs:date", "xs:integer")

	if got := ErrorCode(err); got != "XPTY0004" {
		t.Errorf("ErrorCode = %q, want XPTY0004", got)
	}
	// The rendered message keeps the prefix form, so anything reading error
	// text is unaffected by the code becoming a field.
	const want = "XPTY0004: cannot compare xs:date with xs:integer"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	// errors.As must find it through a wrap, which is how errors travel up
	// through the evaluator.
	wrapped := fmt.Errorf("evaluating select: %w", err)
	if got := ErrorCode(wrapped); got != "XPTY0004" {
		t.Errorf("ErrorCode through a wrap = %q, want XPTY0004", got)
	}
	var e *Error
	if !errors.As(wrapped, &e) {
		t.Error("errors.As did not find the Error through a wrap")
	}
}

// Errors produced as plain formatted strings still carry their code as a
// prefix. Recognising those keeps ErrorCode useful across the whole engine
// rather than only where the constructors were used.
func TestErrorCodeFromPrefixForm(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("FORG0001: invalid xs:hexBinary %q", "zz"), "FORG0001"},
		{fmt.Errorf("XPST0003: unexpected token"), "XPST0003"},
		{fmt.Errorf("parsing stylesheet: %w",
			fmt.Errorf("XTSE0010: unknown element")), "XTSE0010"},
		{ErrCast("bad value"), "FORG0001"},
		// Not error codes.
		{fmt.Errorf("no such file"), ""},
		{fmt.Errorf("parse XML: nesting exceeds 1000 levels"), ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := ErrorCode(c.err); got != c.want {
			t.Errorf("ErrorCode(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// The shape test must not accept things that merely look similar, or a
// message beginning with an ordinary word would be reported as a code.
func TestLooksLikeErrorCodeIsStrict(t *testing.T) {
	valid := []string{"XPTY0004", "FORG0001", "FODC0002", "XTSE0010", "FOCH0001"}
	for _, s := range valid {
		if !looksLikeErrorCode(s) {
			t.Errorf("%q was not recognised as an error code", s)
		}
	}
	invalid := []string{
		"", "XPTY", "0004", "XPTY004", "XPTY00004",
		"xpty0004", // lower case
		"XPT10004", // digit among the letters
		"XPTYA004", // letter among the digits
		"Warning1",
	}
	for _, s := range invalid {
		if looksLikeErrorCode(s) {
			t.Errorf("%q was wrongly recognised as an error code", s)
		}
	}
}
