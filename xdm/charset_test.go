package xdm

import (
	"strings"
	"testing"
)

// A declared encoding is honoured only where this package can decode it
// exactly. Everything else must stay an error rather than being guessed at.
// A case that must parse names the text it has to decode to. Asserting only
// that err is nil let a decoder that mangled every byte pass: "café" coming
// back as "cafi" is a successful parse and a wrong answer.
func TestCharsetReaderAcceptsOnlyExactEncodings(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		text string // the decoded string value, when the document must parse
		want string // substring of the expected error, "" when it must parse
	}{
		{name: "ascii", doc: `<?xml version="1.0" encoding="US-ASCII"?><a>hi</a>`, text: "hi"},
		{name: "ascii lowercase", doc: `<?xml version="1.0" encoding="us-ascii"?><a>hi</a>`, text: "hi"},
		{name: "latin1", doc: "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><a>caf\xe9</a>", text: "café"},
		{name: "latin1 whole high range", doc: "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><a>\xa0\xbf\xe0\xff</a>", text: " ¿àÿ"},
		{name: "latin1 alias", doc: "<?xml version=\"1.0\" encoding=\"latin1\"?><a>\xe9</a>", text: "é"},
		{name: "utf8 still works", doc: `<?xml version="1.0" encoding="UTF-8"?><a>café</a>`, text: "café"},
		{name: "unsupported is refused", doc: `<?xml version="1.0" encoding="Shift_JIS"?><a>x</a>`, want: "unsupported encoding"},
		{name: "utf16 is refused", doc: `<?xml version="1.0" encoding="UTF-16"?><a>x</a>`, want: "unsupported encoding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := ParseString(tc.doc, ParseOptions{})
			if tc.want != "" {
				if err == nil {
					t.Fatalf("%s parsed; it must be refused", tc.name)
				}
				if !contains(err.Error(), tc.want) {
					t.Fatalf("%s: got %v, want mention of %q", tc.name, err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsing %s: %v", tc.name, err)
			}
			if got := tree.Root.StringValue(); got != tc.text {
				t.Fatalf("%s decoded to %q, want %q", tc.name, got, tc.text)
			}
		})
	}
}

// A document declaring US-ASCII while holding a byte above 0x7f is lying, and
// passing the bytes through unchanged would silently accept whatever UTF-8 the
// high bytes happened to form.
func TestASCIIDeclarationWithHighByteIsRefused(t *testing.T) {
	_, err := ParseString("<?xml version=\"1.0\" encoding=\"US-ASCII\"?><a>\xc3\xa9</a>",
		ParseOptions{})
	if err == nil {
		t.Fatal("a US-ASCII document holding a high byte parsed; it must be refused")
	}
	if !contains(err.Error(), "not ASCII") {
		t.Fatalf("got %v, want a complaint that the byte is not ASCII", err)
	}
}

// ISO-8859-1 maps each byte to the code point of the same value, so a byte
// that is not valid UTF-8 on its own still decodes.
func TestLatin1DecodesHighBytes(t *testing.T) {
	tree, err := ParseString("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><a>\xe9</a>",
		ParseOptions{})
	if err != nil {
		t.Fatalf("parsing latin-1: %v", err)
	}
	if got := tree.Root.StringValue(); got != "é" {
		t.Fatalf("got %q, want %q", got, "é")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// The US-ASCII check streams: it used to read the rest of the document into
// a second copy before the tokeniser saw a byte. The refusal keeps its text
// and its offset, counted from just after the XML declaration, also for a
// byte far past the decoder's first read.
func TestASCIICheckStreams(t *testing.T) {
	// Large enough that the decoder's fixed buffers (about 40 KB) sit well
	// inside the bound; the copy the old code made grows with the body.
	body := strings.Repeat("<e>text</e>\n", 200000)
	decl := `<?xml version="1.0" encoding="us-ascii"?>`
	parse := func(doc string) func() {
		return func() {
			if _, err := ParseString(doc, ParseOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	utf8 := allocated(parse(`<?xml version="1.0" encoding="UTF-8"?><a>` + body + `</a>`))
	ascii := allocated(parse(decl + `<a>` + body + `</a>`))
	// The old code copied the whole body first, so it is far above either
	// bound; the race detector's shadow allocations need the wider one.
	bound := int64(len(body)) / 10
	if raceEnabled {
		bound *= 2
	}
	if extra := int64(ascii) - int64(utf8); extra > bound {
		t.Errorf("a US-ASCII document allocated %d bytes more than the same in UTF-8 (body %d bytes)", extra, len(body))
	}

	_, err := ParseString(decl+"<a>"+body+"\xe9</a>", ParseOptions{})
	want := `parse XML: xml: opening charset "us-ascii": declared encoding us-ascii but byte 233 at offset 2400003 is not ASCII`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}
