package xmldecode

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestRewriteEncodingDecl(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<?xml version="1.0" encoding="UTF-16"?><r/>`, `<?xml version="1.0" encoding="UTF-8"?><r/>`},
		{`<?xml version="1.0" encoding='UTF-16'?><r/>`, `<?xml version="1.0" encoding="UTF-8"?><r/>`},
		{`<?xml version="1.0"?><r/>`, `<?xml version="1.0"?><r/>`},
		{`<r/>`, `<r/>`},
	}
	for _, c := range cases {
		if got := rewriteEncodingDecl(c.in); got != c.want {
			t.Errorf("rewriteEncodingDecl(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// utf16Of encodes s as UTF-16 with a byte order mark.
func utf16Of(s string, bigEndian bool) []byte {
	out := []byte{0xFF, 0xFE}
	if bigEndian {
		out = []byte{0xFE, 0xFF}
	}
	for _, u := range utf16.Encode([]rune(s)) {
		if bigEndian {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	return out
}

// countingReader counts the bytes read from it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// TestUTF16StreamsInChunks: a UTF-16 entity is transcoded as it is read, not
// read whole first, so a streamed document costs a buffer rather than about
// ten copies of itself (run-4 utf16Reader.fill whole-source read).
func TestUTF16StreamsInChunks(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-16"?><r>` + strings.Repeat("<a>x</a>", 1<<16) + `</r>`
	src := &countingReader{r: bytes.NewReader(utf16Of(doc, false))}
	r, err := Reader(bufio.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	p := make([]byte, 64)
	if _, err := r.Read(p); err != nil {
		t.Fatal(err)
	}
	if total := 2 * (len(doc) + 1); src.n >= total/2 {
		t.Errorf("first Read consumed %d of %d source bytes", src.n, total)
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?><r>`
	if got := string(p) + string(rest); !strings.HasPrefix(got, want) || len(got) != len(doc)-len("16")+len("8") {
		t.Errorf("decoded %d bytes starting %.50q", len(got), got)
	}
}

// TestUTF16Decoding: surrogate pairs split across chunks, both byte orders,
// an unpaired surrogate, and the errors for an odd byte count and a
// contradicting declaration.
func TestUTF16Decoding(t *testing.T) {
	// "<r>" and the pad fill a chunk but for its last code unit, which is
	// the first pair's high surrogate.
	pad := strings.Repeat("a", utf16Chunk/2-4)
	text := "<r>" + pad + "\U0001F600\U00010000é" + strings.Repeat("\U0001F600", utf16Chunk) + "</r>"
	for _, be := range []bool{false, true} {
		got, err := io.ReadAll(mustReader(t, utf16Of(text, be)))
		if err != nil || string(got) != text {
			t.Errorf("bigEndian=%v: %d bytes, err %v; want the text back", be, len(got), err)
		}
	}
	// A lone high surrogate at the end decodes to U+FFFD, as utf16.Decode does.
	lone := append(utf16Of("<r/>", false), 0x00, 0xD8)
	if got, err := io.ReadAll(mustReader(t, lone)); err != nil || string(got) != "<r/>�" {
		t.Errorf("lone surrogate: %q, %v", got, err)
	}
	odd := append(utf16Of("<r/>", false), 'x')
	if _, err := io.ReadAll(mustReader(t, odd)); err == nil || !strings.Contains(err.Error(), "odd number") {
		t.Errorf("odd byte count: err = %v", err)
	}
	bad := utf16Of(`<?xml version="1.0" encoding="ISO-8859-1"?><r/>`, false)
	if _, err := io.ReadAll(mustReader(t, bad)); err == nil || !strings.Contains(err.Error(), "contradicts") {
		t.Errorf("contradicting declaration: err = %v", err)
	}
	// A declaration longer than a chunk is still rewritten whole.
	long := `<?xml version="1.0"` + strings.Repeat(" ", utf16Chunk) + `encoding="UTF-16"?><r/>`
	got, err := io.ReadAll(mustReader(t, utf16Of(long, false)))
	if want := strings.Replace(long, "UTF-16", "UTF-8", 1); err != nil || string(got) != want {
		t.Errorf("long declaration: %d bytes, err %v", len(got), err)
	}
}

func mustReader(t *testing.T, b []byte) io.Reader {
	t.Helper()
	r, err := Reader(bufio.NewReader(bytes.NewReader(b)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
