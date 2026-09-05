package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
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

// A fragment identifier names a part of a resource, and the unparsed-text
// family retrieves whole resources. F&O 3.0 14.8.5 makes that a dynamic
// error, not something to ignore: "A dynamic error is raised [err:FOUT1170]
// if $href contains a fragment identifier, or if it cannot be used to
// retrieve the string representation of a resource."
//
// The fragment used to be stripped and the whole file returned, which is a
// silent wrong answer: the caller asked for a part of a resource and got all
// of it, with nothing to indicate the difference.
//
// The resolver here answers every URI, so a case that still succeeds proves
// the check reads the URI rather than the resolver's refusal -- which is what
// the suite requires, since unparsed-text-013 and json-doc-error-028 both
// name an http:// host no offline engine will ever fetch.
func TestUnparsedTextRejectsAFragmentIdentifier(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
		// want is a substring of the required error; empty means the call
		// must succeed and return the resource.
		want string
	}{
		// fn:unparsed-text states the rule itself (F&O 3.0 14.8.5).
		{"unparsed-text", `unparsed-text('r.txt#frag')`, "FOUT1170"},
		// fn:unparsed-text-lines is defined as tokenize(unparsed-text(...)),
		// and 14.8.6 says "Error conditions are the same as for the
		// fn:unparsed-text function."
		{"unparsed-text-lines", `unparsed-text-lines('r.txt#frag')`, "FOUT1170"},
		// fn:json-doc is that same read followed by fn:parse-json, and the
		// catalog asserts FOUT1170 for a fragment in json-doc-error-028.
		{"json-doc", `json-doc('r.txt#frag')`, "FOUT1170"},

		// A bare fragment marker still counts: "#" with nothing after it is
		// an empty fragment, not the absence of one.
		{"empty fragment", `unparsed-text('r.txt#')`, "FOUT1170"},

		// "%23" is a percent-encoded number sign, not a delimiter: RFC 3986
		// 3.5 gives that role to a raw "#" alone. Such a URI contains no
		// fragment and must be retrieved normally.
		{"percent-encoded #23 is not a fragment", `unparsed-text('r%23.txt')`, ""},

		// The ordinary path must be untouched.
		{"no fragment at all", `unparsed-text('r.txt')`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewContext(nil, Builtins())
			ctx.Texts = fixedTextResolver{text: "the whole resource"}
			ctx.Version = XPath31

			got, err := Eval(tc.expr, ctx, testNS{})
			switch {
			case tc.want == "":
				if err != nil {
					t.Fatalf("%s: %v, want the resource", tc.expr, err)
				}
			case err == nil:
				t.Fatalf("%s: returned %v, want %s -- the fragment was "+
					"ignored and the whole resource returned",
					tc.expr, got, tc.want)
			case xdm.ErrorCode(err) != tc.want:
				t.Fatalf("%s: %v (code %q), want code %s",
					tc.expr, err, xdm.ErrorCode(err), tc.want)
			}
		})
	}
}

// fn:unparsed-text-available does NOT raise for a fragment. F&O 3.0 14.8.7
// defines it as reporting whether fn:unparsed-text would succeed: it "returns
// true if a call on fn:unparsed-text with the same arguments would succeed,
// and false if a call on fn:unparsed-text with the same arguments would fail
// with a non-recoverable dynamic error". FOUT1170 is such an error, so the
// answer is false -- fn-unparsed-text-available-013 asserts exactly that.
//
// Before the fix this returned true, and it was true for the wrong reason:
// the fragment was dropped and a real file was found behind it.
func TestUnparsedTextAvailableIsFalseForAFragmentRatherThanAnError(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Texts = fixedTextResolver{text: "the whole resource"}
	ctx.Version = XPath31

	for _, expr := range []string{
		`unparsed-text-available('r.txt#frag')`,
		`unparsed-text-available('r.txt#frag', 'utf-8')`,
	} {
		got, err := Eval(expr, ctx, testNS{})
		if err != nil {
			t.Fatalf("%s: %v, want false (this function reports, it does "+
				"not raise)", expr, err)
		}
		if s := renderSeq(got); s != "false" {
			t.Errorf("%s = %s, want false", expr, s)
		}
	}

	// The same call without the fragment must still be true, or the case
	// above would pass for no reason.
	got, err := Eval(`unparsed-text-available('r.txt')`, ctx, testNS{})
	if err != nil {
		t.Fatalf("unparsed-text-available('r.txt'): %v", err)
	}
	if s := renderSeq(got); s != "true" {
		t.Errorf("unparsed-text-available('r.txt') = %s, want true", s)
	}
}
