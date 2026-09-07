package xmlfork

import (
	"io"
	"strings"
	"testing"
)

// The declarations under test. Every case below is the same document twice,
// differing only in this line, because the point of XML 1.1 support is that
// the declaration — and nothing else — decides what the language is.
const (
	decl10 = `<?xml version="1.0"?>`
	decl11 = `<?xml version="1.1"?>`
)

// The two characters §2.11 adds to the line ends, written as escapes so that
// no raw control byte sits in this source file.
const (
	nel     = "\u0085" // NEL
	lineSep = "\u2028" // LINE SEPARATOR
	bel     = "\u0007" // a RestrictedChar, [2a]
)

// readAll drains the tokeniser and returns the concatenated character data, or
// the error that stopped it.
func readAll(t *testing.T, doc string) (string, error) {
	t.Helper()
	d := NewDecoder(strings.NewReader(doc))
	var sb strings.Builder
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return sb.String(), nil
		}
		if err != nil {
			return sb.String(), err
		}
		if c, ok := tok.(CharData); ok {
			sb.Write(c)
		}
	}
}

// TestXML11RestrictedCharAsReference is the W3C XmlVersions cases xv003, xv006,
// xv008 and xv009 in miniature: a C0 control written as a character reference
// is a character in 1.1 and is not one in 1.0.
//
// The refusal is the half that matters. A 1.0 document must not acquire 1.1's
// relaxations because a 1.1 document elsewhere earned them.
func TestXML11RestrictedCharAsReference(t *testing.T) {
	const body = `<doc>a&#x7;b</doc>`

	got, err := readAll(t, decl11+body)
	if err != nil {
		t.Fatalf("XML 1.1 [2a] admits &#x7; as a reference, but it was refused: %v", err)
	}
	if want := "a" + bel + "b"; got != want {
		t.Errorf("character data = %q, want %q", got, want)
	}

	if _, err := readAll(t, decl10+body); err == nil {
		t.Error("XML 1.0 [2] Char excludes U+0007; &#x7; must be refused, and was accepted")
	}

	// No declaration at all is XML 1.0, not "unknown, so be lenient".
	if _, err := readAll(t, body); err == nil {
		t.Error("a document with no declaration is XML 1.0; &#x7; must be refused")
	}
}

// TestXML11RestrictedCharLiteralRefused pins the distinction the whole
// implementation turns on. XML 1.1 §2.2 permits a RestrictedChar "only as
// character references", so the very character that
// TestXML11RestrictedCharAsReference accepts through &#x7; is a fatal error
// written out — under BOTH versions.
func TestXML11RestrictedCharLiteralRefused(t *testing.T) {
	for _, v := range []struct{ name, decl string }{
		{"1.0", decl10},
		{"1.1", decl11},
	} {
		t.Run(v.name, func(t *testing.T) {
			if _, err := readAll(t, v.decl+"<doc>a"+bel+"b</doc>"); err == nil {
				t.Error("a literal U+0007 is never permitted, and was accepted")
			}
			// CDATA is not an escape hatch: §2.2 is about the character,
			// not about the markup it sits in.
			if _, err := readAll(t, v.decl+"<doc><![CDATA[a"+bel+"b]]></doc>"); err == nil {
				t.Error("a literal U+0007 in CDATA is never permitted, and was accepted")
			}
			// Nor is an attribute value.
			if _, err := readAll(t, v.decl+`<doc a="x`+bel+`y"/>`); err == nil {
				t.Error("a literal U+0007 in an attribute value is never permitted, and was accepted")
			}
		})
	}
}

// TestXML11NULRefused records the one character that stays illegal in 1.1.
// [2] Char begins at #x1, so NUL is not a character however it is written —
// this is why the 1.1 predicate is not simply "anything below #x20".
func TestXML11NULRefused(t *testing.T) {
	if _, err := readAll(t, decl11+`<doc>a&#0;b</doc>`); err == nil {
		t.Error("U+0000 is outside XML 1.1 [2] Char and must be refused even as a reference")
	}
}

// TestXML11LineEndNormalisation covers §2.11, which adds NEL (#x85) and
// #x2028 to the line ends normalised to #xA. Under 1.0 neither is a line end,
// so both must survive as the characters they are.
func TestXML11LineEndNormalisation(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		want11 string
		want10 string
	}{
		{"NEL", "<doc>a" + nel + "b</doc>", "a\nb", "a" + nel + "b"},
		{"LINE SEPARATOR", "<doc>a" + lineSep + "b</doc>", "a\nb", "a" + lineSep + "b"},
		// CR NEL is a two-character line end in 1.1, so it yields ONE #xA
		// rather than two. Under 1.0 the CR normalises on its own and the
		// NEL survives beside it.
		{"CR NEL", "<doc>a\r" + nel + "b</doc>", "a\nb", "a\n" + nel + "b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := readAll(t, decl11+c.body)
			if err != nil {
				t.Fatalf("XML 1.1: %v", err)
			}
			if got != c.want11 {
				t.Errorf("XML 1.1 gave %q, want %q — §2.11 normalises this to #xA", got, c.want11)
			}

			got, err = readAll(t, decl10+c.body)
			if err != nil {
				t.Fatalf("XML 1.0: %v", err)
			}
			if got != c.want10 {
				t.Errorf("XML 1.0 gave %q, want %q — 1.0 has no such line end, so the character stands", got, c.want10)
			}
		})
	}
}

// TestXML11ReferencedLineEndNotNormalised separates the two rules that both
// mention #x85. §2.11 normalises a line end found in the INPUT; a character
// reference denotes the character itself and is not touched. Conflating them
// would silently rewrite &#x85; to a newline.
func TestXML11ReferencedLineEndNotNormalised(t *testing.T) {
	got, err := readAll(t, decl11+`<doc>a&#x85;b</doc>`)
	if err != nil {
		t.Fatalf("&#x85; is a character reference and must be accepted: %v", err)
	}
	if want := "a" + nel + "b"; got != want {
		t.Errorf("character data = %q, want %q — a reference is not a line end", got, want)
	}
}

// TestUnsupportedVersionRefused records that widening to 1.1 did not widen to
// anything else: a version this tokeniser does not implement is still refused
// outright rather than read under whichever rules happen to be nearest.
func TestUnsupportedVersionRefused(t *testing.T) {
	for _, ver := range []string{"1.2", "2.0", "0.9"} {
		if _, err := readAll(t, `<?xml version="`+ver+`"?><doc/>`); err == nil {
			t.Errorf("version %q is not supported and must be refused", ver)
		}
	}
}
