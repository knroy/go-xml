package xdm

import (
	"bytes"
	"strings"
	"testing"
	"testing/iotest"
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

// TestParseXML11Declaration records that a document declaring XML 1.1 is
// parsed rather than refused. encoding/xml, which this package uses as its
// tokeniser, rejects any version but 1.0 outright; the saxonData XmlVersions
// schemas (xv001..xv009) are ordinary schema documents whose only 1.1 feature
// is that declaration, so refusing it kept nine valid schemas from loading.
func TestParseXML11Declaration(t *testing.T) {
	// The 1.1-only character arrives as a character reference in an
	// attribute value, exactly as xv001 writes it.
	const doc = `<?xml version="1.1" encoding="UTF-8"?><r name="D&#x133;kstra"/>`
	tree, err := Parse(strings.NewReader(doc), ParseOptions{})
	if err != nil {
		t.Fatalf("an XML 1.1 declaration should be accepted: %v", err)
	}
	root := tree.Root.ChildElements()[0]
	if got := root.AttrValue("name"); got != "Dĳkstra" {
		t.Errorf("attribute value = %q, want the character reference expanded", got)
	}
}

// TestParseXML11UTF16 covers the other decode path: a UTF-16 document rewrites
// its encoding declaration, and the version rewrite has to survive that.
func TestParseXML11UTF16(t *testing.T) {
	b := encodeUTF16(`<?xml version="1.1" encoding="UTF-16"?><r/>`, false)
	if _, err := Parse(bytes.NewReader(b), ParseOptions{}); err != nil {
		t.Fatalf("an XML 1.1 UTF-16 document should be accepted: %v", err)
	}
}

func TestRewriteVersionDecl(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<?xml version="1.1"?><r/>`, `<?xml version="1.0"?><r/>`},
		{`<?xml version='1.1' encoding="UTF-8"?><r/>`, `<?xml version='1.0' encoding="UTF-8"?><r/>`},
		// A 1.0 declaration must come through byte for byte.
		{`<?xml version="1.0"?><r/>`, `<?xml version="1.0"?><r/>`},
		{`<r/>`, `<r/>`},
	}
	for _, c := range cases {
		if got := rewriteVersionDecl(c.in); got != c.want {
			t.Errorf("rewriteVersionDecl(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A document shorter than the four bytes decodeReader peeks at must still be
// read rather than being reported as a read error.
//
// bufio returns a "buffer full"-family error for a short Peek, which is not a
// failure of the underlying reader — it means the document ended. Treating it
// as an error would make every document under four bytes unparseable, and the
// shortest legal one is well under that.
func TestShortDocumentIsNotAReadError(t *testing.T) {
	cases := []struct {
		name, src string
		wantErr   bool
	}{
		{"a minimal document", `<a/>`, false},
		{"shorter than the peek", `<a/`, true}, // malformed, but not a read error
		{"empty", ``, true},
		{"one byte", `<`, true},
	}
	for _, c := range cases {
		_, err := ParseString(c.src, ParseOptions{})
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, want error = %v", c.name, err, c.wantErr)
			continue
		}
		// Whatever the outcome, it must be an XML complaint rather than a
		// read failure: the reader did its job.
		if err != nil && strings.Contains(err.Error(), "buffer") {
			t.Errorf("%s: reported a buffer error: %v", c.name, err)
		}
	}
}

// A reader that returns fewer bytes per call than the peek wants exercises the
// same path as a short document, and must not change the result. An io.Reader
// is permitted to return one byte at a time.
func TestDripFedReaderParses(t *testing.T) {
	const src = `<a><b>text</b></a>`
	tree, err := Parse(iotest.OneByteReader(strings.NewReader(src)),
		ParseOptions{})
	if err != nil {
		t.Fatalf("a one-byte-at-a-time reader failed: %v", err)
	}
	if got := tree.Root.StringValue(); got != "text" {
		t.Errorf("parsed to %q, want %q", got, "text")
	}
}
