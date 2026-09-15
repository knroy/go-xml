package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestJSONFallbackResultIsChecked covers the fallback function's ANSWER.
//
// The fallback is handed the escape for a character the result cannot hold and
// is expected to name a replacement that it can. Nothing obliges it to: it is
// arbitrary user code and may hand back a C0 control of its own. F&O 3.1
// §17.4.1 makes that err:FOJS0007 rather than a value.
//
// Accepting the answer unchecked was worse than a missing error code. The
// control reached the map fn:parse-json returns, so string-to-codepoints saw
// U+0008; and it reached the tree fn:json-to-xml builds, where serialisation
// silently discarded it and the <string> element came back empty. The bad
// character was neither reported nor preserved -- it vanished.
func TestJSONFallbackResultIsChecked(t *testing.T) {
	for _, q := range []string{
		`parse-json('"\uFFFF"', map{'fallback':function($s){codepoints-to-string(8)}})`,
		`parse-json('"\uFFFF"', map{'fallback':function($s){codepoints-to-string(1)}})`,
		`parse-json('"\uDEAD"', map{'fallback':function($s){codepoints-to-string(8)}})`,
		`json-to-xml('"\uFFFF"', map{'fallback':function($s){codepoints-to-string(8)}})`,
		// The replacement is checked wherever it sits in the answer, not just
		// at its head: a fallback that decorates a bad character still fails.
		`parse-json('"\uFFFF"', map{'fallback':function($s){'ok' || codepoints-to-string(8)}})`,
	} {
		ctx := NewContext(nil, Builtins())
		ctx.Version, ctx.LibraryVersion = XPath31, XPath31
		_, err := Eval(q, ctx, nil)
		if err == nil || !strings.Contains(err.Error(), "FOJS0007") {
			t.Errorf("a fallback returning an invalid XML character should be FOJS0007\n  %s\n  got %v", q, err)
		}
	}

	// A fallback that answers legally is untouched: the check must not turn
	// the working cases -- json-to-xml-039 among them -- into errors.
	for _, tc := range []struct{ query, want string }{
		{`parse-json('"\uFFFF"', map{'fallback':function($s){'??'}})`, "??"},
		{`parse-json('"\uFFFF"', map{'fallback':function($s){'[' || $s || ']'}})`, `[\uFFFF]`},
		{`serialize(json-to-xml('"oh dear \uDEAD"', map{'fallback':function($s){upper-case($s) => substring(3)}}))`,
			`<string xmlns="http://www.w3.org/2005/xpath-functions">oh dear DEAD</string>`},
		// Tab is a legal XML character and so is a legal replacement.
		{`parse-json('"\uFFFF"', map{'fallback':function($s){codepoints-to-string(9)}})`, "\t"},
	} {
		ctx := NewContext(nil, Builtins())
		ctx.Version, ctx.LibraryVersion = XPath31, XPath31
		seq, err := Eval(tc.query, ctx, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		if got := seq[0].(*xdm.Atomic).String(); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.query, got, tc.want)
		}
	}
}
