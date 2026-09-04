package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A CR must survive parse -> serialize -> parse.
//
// XML 1.0 §2.11 normalises a literal CR to LF on input, so a CR that has to be
// preserved can only be written as a character reference. security.md recorded
// this as an open defect on the grounds that escapeText handles & < > but not
// \r; that reading missed the branch above the switch, which writes a numeric
// reference for CR, U+2028 and the C1 range before the named escapes are
// reached. K2-Serialization-5, -6, -10 and -11 assert those cases.
func TestCRSurvivesRoundTrip(t *testing.T) {
	for _, src := range []string{
		"<r>a&#13;b</r>",
		`<r a="x&#13;y"/>`,
		"<r>line&#13;&#10;end</r>",
		"<r>&#13;</r>",
		"<r>a&#13;&#13;b</r>",
	} {
		tr, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		before := tr.Root.StringValue()

		var sb strings.Builder
		if err := Serialize(&sb, xdm.Sequence{tr.Root}, OutputSettings{Method: "xml"}, nil); err != nil {
			t.Fatalf("serialize %q: %v", src, err)
		}

		tr2, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("reparse %q: %v", sb.String(), err)
		}
		if after := tr2.Root.StringValue(); before != after {
			t.Errorf("%q: value changed across the round trip: %q -> %q\n  serialized: %q",
				src, before, after, sb.String())
		}
	}
}
