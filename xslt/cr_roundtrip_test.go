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
// Each case names the value that must survive, because StringValue() on the
// root element does not reach an attribute: the element-only comparison this
// test used to make read "" == "" for the attribute case, so an attribute CR
// could be written as a literal LF -- which the next parse normalises away --
// and the round trip still looked lossless.
func TestCRSurvivesRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		src  string
		attr string // when set, the attribute whose value must round trip
		want string
	}{
		{src: "<r>a&#13;b</r>", want: "a\rb"},
		{src: `<r a="x&#13;y"/>`, attr: "a", want: "x\ry"},
		{src: "<r>line&#13;&#10;end</r>", want: "line\r\nend"},
		{src: "<r>&#13;</r>", want: "\r"},
		{src: "<r>a&#13;&#13;b</r>", want: "a\r\rb"},
		{src: `<r a="p&#13;&#13;q"/>`, attr: "a", want: "p\r\rq"},
		{src: `<r a="&#13;"/>`, attr: "a", want: "\r"},
		{src: `<r a="s&#13;&#10;t"/>`, attr: "a", want: "s\r\nt"},
	} {
		tr, err := xdm.ParseString(tc.src, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse %q: %v", tc.src, err)
		}
		// The parse itself must already hold the CR; if it does not, the
		// round trip below would compare two equally wrong values.
		if before := crValue(t, tr.Root, tc.attr); before != tc.want {
			t.Errorf("%q: parsed as %q, want %q", tc.src, before, tc.want)
			continue
		}

		var sb strings.Builder
		if err := Serialize(&sb, xdm.Sequence{tr.Root}, OutputSettings{Method: "xml"}, nil); err != nil {
			t.Fatalf("serialize %q: %v", tc.src, err)
		}
		// A literal CR in the output is already lost: XML 1.0 §2.11
		// normalises it on the next parse, so only a character reference
		// preserves it.
		if strings.ContainsRune(sb.String(), '\r') {
			t.Errorf("%q: serialized with a literal CR, which the next parse destroys: %q",
				tc.src, sb.String())
		}

		tr2, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("reparse %q: %v", sb.String(), err)
		}
		if after := crValue(t, tr2.Root, tc.attr); after != tc.want {
			t.Errorf("%q: value changed across the round trip: got %q, want %q\n  serialized: %q",
				tc.src, after, tc.want, sb.String())
		}
	}
}

// crValue returns the element's string value, or the named attribute's when
// attr is set. An absent attribute is a failure rather than an empty string,
// which is the hole the old comparison fell through.
func crValue(t *testing.T, root *xdm.Node, attr string) string {
	t.Helper()
	if attr == "" {
		return root.StringValue()
	}
	el := elementOf(t, root)
	for _, a := range el.Attrs {
		if a.Name.Local == attr {
			return a.Value
		}
	}
	t.Fatalf("no attribute %q on the serialized element", attr)
	return ""
}

// elementOf digs the document element out of a parsed tree root.
func elementOf(t *testing.T, n *xdm.Node) *xdm.Node {
	t.Helper()
	if n.Kind == xdm.KindElement {
		return n
	}
	for _, c := range n.Children {
		if c.Kind == xdm.KindElement {
			return c
		}
	}
	t.Fatalf("no element in the tree")
	return nil
}
