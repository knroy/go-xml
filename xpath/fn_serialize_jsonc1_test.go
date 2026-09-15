package xpath

import (
	"fmt"
	"strings"
	"testing"
)

// TestSerializeJSONEscapesC1Controls pins the codepoints the JSON output
// method must write as \uHHHH.
//
// Serialization 3.1 §9 (JSON Output Method): "JSON escaping replaces the
// characters quotation mark, backspace, form-feed, newline, carriage return,
// tab, reverse solidus, or solidus by the corresponding JSON escape sequences
// \", \b, \f, \n, \r, \t, \\, or \/ respectively, and any other codepoint in
// the range 1-31 or 127-159 by an escape in the form \uHHHH where HHHH is the
// hexadecimal representation of the codepoint value."
//
// The serializer escaped only codepoints below 0x20, so DEL and the C1 range
// U+007F to U+009F were written literally. fn:xml-to-json, which is governed
// by the same JSON escaping rule, already had the full range (it is pinned by
// QT3 xml-to-json-073, whose expected result spells out every escape from
//  to ). The two spellings of the same rule had drifted apart.
func TestSerializeJSONEscapesC1Controls(t *testing.T) {
	got, err := evalSerialize(t,
		`serialize(codepoints-to-string((127 to 159)), map{'method':'json'})`)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	var want strings.Builder
	want.WriteByte('"')
	for c := 0x7F; c <= 0x9F; c++ {
		fmt.Fprintf(&want, `\u%04X`, c)
	}
	want.WriteByte('"')
	if got != want.String() {
		t.Errorf("C1 controls not escaped by the JSON method:\n got %q\nwant %q",
			got, want.String())
	}

	// The guard: U+00A0, the first codepoint past the range, stays literal.
	got, err = evalSerialize(t,
		`serialize(codepoints-to-string(160), map{'method':'json'})`)
	if err != nil {
		t.Fatalf("serialize A0: %v", err)
	}
	if w := "\" \""; got != w {
		t.Errorf("U+00A0 is outside the escaped range: got %q want %q", got, w)
	}
}
