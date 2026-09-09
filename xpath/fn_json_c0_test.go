package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestJSONC0ControlIsNotDeliverable covers the predicate that decides whether a
// codepoint can be carried literally by the text node fn:json-to-xml builds.
//
// A C0 control other than tab, newline and carriage return is not an XML 1.0
// Char, so it is a character the result "cannot represent": under escape=true
// it keeps its \uXXXX spelling, and under escape=false it is replaced by U+FFFD
// (or handed to the fallback function).
//
// The predicate used to be isXMLChar, which is the XML 1.1 Char production —
// there every C0 control but NUL is legal, because 1.1 lets one be written as a
// character reference. JSON delivery has no such escape hatch, so U+0001 and
// U+0008 were emitted raw on both settings. json-to-xml-045 is the case:
// "-\b-\t-\u0001-" must come back as itself under escape=true and as
// "-<FFFD>-<tab>-<FFFD>-" under escape=false.
func TestJSONC0ControlIsNotDeliverable(t *testing.T) {
	// Every backslash below is doubled in the Go literal, so the JSON source
	// really holds the escape sequences \b, \t and \u0001 rather than the
	// characters they stand for.
	const in = "\"-\\b-\\t-\\u0001-\""
	for _, tc := range []struct {
		escape bool
		want   string
	}{
		// escape=true: nothing is unescaped, and the two characters XML
		// cannot carry keep an escaped spelling. The tab can be carried, but
		// it has a short escape and so keeps it.
		{true, `-\b-\t-\u0001-`},
		// escape=false: the tab is a legal XML character and is delivered as
		// itself; U+0008 and U+0001 are not, and become U+FFFD.
		{false, "-�-\t-�-"},
	} {
		ctx := NewContext(nil, Builtins())
		ctx.Version, ctx.LibraryVersion = XPath31, XPath31
		seq, err := Eval(
			`json-to-xml($j, map{'escape': $e})/*:string/string()`,
			ctx.WithVar(xdm.QName{Local: "j"}, xdm.One(xdm.NewString(in))).
				WithVar(xdm.QName{Local: "e"}, xdm.One(xdm.NewBoolean(tc.escape))), nil)
		if err != nil {
			t.Fatalf("escape=%v: %v", tc.escape, err)
		}
		got := seq[0].(*xdm.Atomic).String()
		if got != tc.want {
			t.Errorf("escape=%v: got %q, want %q", tc.escape, got, tc.want)
		}
	}
}

// TestDeliverableInXMLBoundaries pins the predicate at each edge of the XML 1.0
// Char production, so a future edit cannot quietly widen it back to 1.1.
func TestDeliverableInXMLBoundaries(t *testing.T) {
	for _, tc := range []struct {
		c    rune
		want bool
	}{
		{0x00, false}, // NUL
		{0x01, false}, // the case's control
		{0x08, false}, // backspace, what \b unescapes to
		{0x09, true},  // tab
		{0x0A, true},  // newline
		{0x0B, false}, // vertical tab: legal in 1.1, not in 1.0
		{0x0C, false}, // form feed, what \f unescapes to
		{0x0D, true},  // carriage return
		{0x1F, false}, // the last C0 control
		{0x20, true},  // space, the first unconditionally legal character
		{0xD7FF, true},
		{0xD800, false}, // a lone surrogate
		{0xDFFF, false},
		{0xE000, true},
		{0xFFFD, true},
		{0xFFFE, false}, // not a character
		{0xFFFF, false},
		{0x10000, true},
		{0x10FFFF, true},
		{0x110000, false}, // past the last codepoint
	} {
		if got := deliverableInXML(tc.c); got != tc.want {
			t.Errorf("deliverableInXML(U+%04X) = %v, want %v", tc.c, got, tc.want)
		}
	}
}
