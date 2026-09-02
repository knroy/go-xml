package xpath

import (
	"strings"
	"testing"
)

// fixedTextResolver hands back one text for any URI, so a test can put exact
// bytes in front of fn:unparsed-text and fn:json-doc without touching disk.
type fixedTextResolver struct{ text string }

func (r fixedTextResolver) ResolveText(uri, base, encoding string) (string, error) {
	return r.text, nil
}

// fn:unparsed-text may not return a character XML does not permit, but
// fn:json-doc reads through the same resolver and is not subject to that
// rule: a JSON text is allowed to hold U+FFFF, and an unescaped control
// character in one is FOJS0001 raised by the JSON parser rather than a
// text-decoding error raised before the parser ever sees it.
//
// The two used to share the restriction because it was applied inside the
// resolver, which made json-doc reject JSON the suite requires it to accept
// (y_string_nonCharacterInUTF-8_U+FFFF) and report a decoding error where the
// suite requires FOJS0001 (n_string_unescaped_crtl_char and its neighbours).
func TestJSONDocIsNotBoundByUnparsedTextsCharacterRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		// unparsedWant is a substring of the error fn:unparsed-text must
		// raise; empty means it must succeed.
		unparsedWant string
		// jsonWant is a substring of the error fn:json-doc must raise;
		// empty means it must parse.
		jsonWant string
	}{
		{
			name: "U+FFFF is legal JSON and illegal XML",
			text: "[\"￿\"]",
			// U+FFFF is outside the XML Char production, so unparsed-text
			// refuses it; the same bytes are a perfectly good JSON array.
			unparsedWant: "FOUT1200",
			jsonWant:     "",
		},
		{
			name: "an unescaped control character is FOJS0001, not a decode error",
			text: "[\"a\x00a\"]",
			// The NUL is illegal in XML, so unparsed-text refuses it. JSON
			// forbids it too, but as a JSON error from the parser -- the
			// distinction the catalog encodes by asking for FOJS0001.
			unparsedWant: "FOUT1200",
			jsonWant:     "FOJS0001",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewContext(nil, Builtins())
			ctx.Texts = fixedTextResolver{text: tc.text}
			ctx.Version = XPath31

			check := func(expr, want string) {
				t.Helper()
				_, err := Eval(expr, ctx, testNS{})
				switch {
				case want == "" && err != nil:
					t.Errorf("%s: %v, want success", expr, err)
				case want != "" && err == nil:
					t.Errorf("%s: succeeded, want %s", expr, want)
				case want != "" && err != nil && !strings.Contains(err.Error(), want):
					t.Errorf("%s: %v, want %s", expr, err, want)
				}
			}
			check(`unparsed-text('x')`, tc.unparsedWant)
			check(`json-doc('x')`, tc.jsonWant)
		})
	}
}
