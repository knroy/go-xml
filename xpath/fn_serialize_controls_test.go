package xpath

import (
	"fmt"
	"testing"
)

// TestSerializeEscapesLineEndingsAndControls pins the characters the XML
// output method must write as character references so that they survive the
// round trip through serialization and parsing.
//
// Serialization 3.1 §5 (XML Output Method), quoted from the local copy at
// testdata/xslt30-test/specs/serialization-31.html: "CR, NEL and LINE
// SEPARATOR characters in text nodes MUST be output respectively as &#xD;,
// &#x85;, and &#x2028;, or their equivalents; while CR, NL, TAB, NEL and LINE
// SEPARATOR characters in attribute nodes MUST be output respectively as
// &#xD;, &#xA;, &#x9;, &#x85;, and &#x2028;, or their equivalents." And: "the
// non-whitespace control characters #x1 through #x1F and #x7F through #x9F in
// text nodes and attribute nodes MUST be output as character references."
//
// The serializer escaped only CR (plus TAB and NL in attributes), so NEL,
// LINE SEPARATOR and the whole of C0 and C1 were written literally. That is
// not merely non-conforming: under XML 1.1 line-end normalisation a literal
// U+0085 reparses as a line feed, so serialize-then-reparse silently
// corrupted the data the MUST exists to protect.
//
// Each case is round-tripped: the character enters as a reference in the
// parsed source, so what the test asserts is that serializing the node it
// produced writes a reference back out rather than the raw character. Both
// positions are asserted from one serialisation, so they cannot disagree
// unnoticed.
func TestSerializeEscapesLineEndingsAndControls(t *testing.T) {
	for _, c := range []struct {
		name string
		cp   int
		want string
	}{
		// NEL and LINE SEPARATOR: named by the spec in both positions, and
		// the two characters that were silently lost on a reparse.
		{"NEL", 0x85, `<e a="&#x85;">&#x85;</e>`},
		{"LINE SEPARATOR", 0x2028, `<e a="&#x2028;">&#x2028;</e>`},
		// DEL and a C1 control, from the "#x7F through #x9F" range.
		{"DEL", 0x7F, `<e a="&#x7F;">&#x7F;</e>`},
		{"C1 CSI", 0x9B, `<e a="&#x9B;">&#x9B;</e>`},
		// A C0 control, from the "#x1 through #x1F" range.
		{"C0 SOH", 0x1, `<e a="&#x1;">&#x1;</e>`},
		// CR is escaped in both positions, and always was.
		{"CR", 0xD, `<e a="&#xD;">&#xD;</e>`},
		// The asymmetry the spec requires, which must not regress: TAB and LF
		// are ordinary characters in content and stay literal there, but in
		// an attribute value a parser would normalise either to a space, so
		// there they are written as references.
		{"TAB", 0x9, "<e a=\"&#x9;\">\t</e>"},
		{"LF", 0xA, "<e a=\"&#xA;\">\n</e>"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// The character is spelled as a reference in the source, so no
			// control character appears literally in this file. XML 1.1 is
			// the version that admits the C0 controls as references at all.
			// The XPath string literal is single-quoted, so the double
			// quotes inside the XML need no escaping.
			src := fmt.Sprintf(
				`<?xml version="1.1"?><e a="&#x%X;">&#x%X;</e>`, c.cp, c.cp)

			got, err := evalSerialize(t,
				fmt.Sprintf(`serialize(parse-xml('%s')/e)`, src))
			if err != nil {
				t.Fatalf("serialize U+%04X: %v", c.cp, err)
			}
			if got != c.want {
				t.Errorf("U+%04X round-tripped wrong:\n got %q\nwant %q",
					c.cp, got, c.want)
			}
		})
	}

	// The guards: the characters just past each escaped range stay literal,
	// so a future widening of the ranges cannot pass unnoticed. U+0020 is the
	// codepoint after C0 and U+00A0 the one after C1.
	for _, g := range []struct {
		name string
		cp   int
	}{
		{"SPACE", 0x20},
		{"NBSP", 0xA0},
	} {
		t.Run(g.name+" stays literal", func(t *testing.T) {
			got, err := evalSerialize(t, fmt.Sprintf(
				`serialize(parse-xml('<e>&#x%X;</e>')/e)`, g.cp))
			if err != nil {
				t.Fatalf("serialize U+%04X: %v", g.cp, err)
			}
			if want := fmt.Sprintf("<e>%c</e>", rune(g.cp)); got != want {
				t.Errorf("U+%04X is outside the escaped range:\n got %q\nwant %q",
					g.cp, got, want)
			}
		})
	}
}
