package xdm

import "testing"

// A declared encoding is honoured only where this package can decode it
// exactly. Everything else must stay an error rather than being guessed at.
func TestCharsetReaderAcceptsOnlyExactEncodings(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want string // substring of the expected value, "" when it must parse
	}{
		{"ascii", `<?xml version="1.0" encoding="US-ASCII"?><a>hi</a>`, ""},
		{"ascii lowercase", `<?xml version="1.0" encoding="us-ascii"?><a>hi</a>`, ""},
		{"latin1", "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><a>caf\xe9</a>", ""},
		{"utf8 still works", `<?xml version="1.0" encoding="UTF-8"?><a>café</a>`, ""},
		{"unsupported is refused", `<?xml version="1.0" encoding="Shift_JIS"?><a>x</a>`, "unsupported encoding"},
		{"utf16 is refused", `<?xml version="1.0" encoding="UTF-16"?><a>x</a>`, "unsupported encoding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseString(tc.doc, ParseOptions{})
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("parsing %s: %v", tc.name, err)
			case tc.want != "" && err == nil:
				t.Fatalf("%s parsed; it must be refused", tc.name)
			case tc.want != "" && !contains(err.Error(), tc.want):
				t.Fatalf("%s: got %v, want mention of %q", tc.name, err, tc.want)
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
