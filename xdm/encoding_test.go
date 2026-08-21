package xdm

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf16"
)

// encodeUTF16 renders a string as UTF-16 with a byte order mark.
func encodeUTF16(s string, bigEndian bool) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2+2*len(units))
	if bigEndian {
		out = append(out, 0xFE, 0xFF)
	} else {
		out = append(out, 0xFF, 0xFE)
	}
	for _, u := range units {
		if bigEndian {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	return out
}

// TestParseUTF16 covers the encodings XML 1.0 §4.3.3 makes mandatory.
//
// encoding/xml reads only UTF-8 and hands anything else to a CharsetReader,
// which is nil by default — so a UTF-16 document used to fail with "invalid
// UTF-8" rather than being read. 65 schemas in the W3C suite are UTF-16.
func TestParseUTF16(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="UTF-16"?><r a="v">text</r>`

	for _, big := range []bool{false, true} {
		name := "little-endian"
		if big {
			name = "big-endian"
		}
		tree, err := Parse(bytes.NewReader(encodeUTF16(doc, big)), ParseOptions{})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		root := tree.Root.ChildElements()[0]
		if root.Name.Local != "r" {
			t.Errorf("%s: root is %q", name, root.Name.Local)
		}
		if root.AttrValue("a") != "v" {
			t.Errorf("%s: attribute lost", name)
		}
		if root.StringValue() != "text" {
			t.Errorf("%s: content is %q", name, root.StringValue())
		}
	}
}

// TestParseUTF16NonASCII confirms the decoding is not byte-wise: characters
// outside ASCII, and outside the basic plane, must survive.
func TestParseUTF16NonASCII(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="UTF-16"?><r>héllo 𝄞</r>`
	tree, err := Parse(bytes.NewReader(encodeUTF16(doc, false)), ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.Root.ChildElements()[0].StringValue(); got != "héllo 𝄞" {
		t.Errorf("got %q, want the original text", got)
	}
}

// TestParseUTF8BOM covers a UTF-8 byte order mark, which is legal and carries
// no information — but encoding/xml treats it as character data before the root
// element, which is not well formed.
func TestParseUTF8BOM(t *testing.T) {
	src := "\xEF\xBB\xBF<r>x</r>"
	tree, err := Parse(strings.NewReader(src), ParseOptions{})
	if err != nil {
		t.Fatalf("a UTF-8 BOM should be accepted: %v", err)
	}
	if tree.Root.ChildElements()[0].Name.Local != "r" {
		t.Error("the root element was not found")
	}
}

// TestParseUTF16OddLength records that a truncated UTF-16 document is an error
// rather than being decoded up to the last whole unit, which would lose the end
// of the document silently.
func TestParseUTF16OddLength(t *testing.T) {
	b := encodeUTF16(`<?xml version="1.0"?><r/>`, false)
	if _, err := Parse(bytes.NewReader(b[:len(b)-1]), ParseOptions{}); err == nil {
		t.Error("an odd byte count should be refused")
	}
}

func TestRewriteEncodingDecl(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<?xml version="1.0" encoding="UTF-16"?><r/>`, `<?xml version="1.0"?><r/>`},
		{`<?xml version="1.0" encoding='UTF-16'?><r/>`, `<?xml version="1.0"?><r/>`},
		{`<?xml version="1.0"?><r/>`, `<?xml version="1.0"?><r/>`},
		{`<r/>`, `<r/>`},
	}
	for _, c := range cases {
		if got := rewriteEncodingDecl(c.in); got != c.want {
			t.Errorf("rewriteEncodingDecl(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
