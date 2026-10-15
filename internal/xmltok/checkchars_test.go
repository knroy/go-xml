package xmltok

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// tokenizeAll reads src to the end and returns the first error.
func tokenizeAll(src string) error {
	d := NewDecoder(strings.NewReader(src))
	for {
		if _, err := d.RawToken(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// checkChars scans literal text eight bytes at a time when it is plain ASCII.
// Every byte value, at every offset within and across those words, must get
// the verdict the byte-at-a-time check gives it, in text, attribute values,
// comments and PIs, and next to a character reference.
func TestCheckCharsEveryByteAtEveryOffset(t *testing.T) {
	legal := func(c byte, v11 bool) bool {
		switch {
		case c == '\t' || c == '\n' || c == '\r':
			return true
		case c == 0x7F:
			return !v11 // XML 1.1 admits DEL only through a reference
		default:
			return c >= 0x20 && c < 0x80 // a lone byte >= 0x80 is invalid UTF-8
		}
	}
	shapes := []struct{ name, pre, post string }{
		{"text", "<a>", "</a>"},
		{"text after a reference", "<a>&#65;", "</a>"},
		{"attribute", `<a b="`, `"/>`},
		{"comment", "<a><!--", "--></a>"},
		{"pi", "<a><?p ", "?></a>"},
	}
	for _, v11 := range []bool{false, true} {
		decl := ""
		if v11 {
			decl = `<?xml version="1.1"?>`
		}
		for _, sh := range shapes {
			for c := 0; c < 256; c++ {
				switch byte(c) {
				case '<', '&', '"', '-', '?', '>':
					continue // markup in one shape or another
				}
				for off := range 20 {
					body := bytes.Repeat([]byte("x"), 24)
					body[off] = byte(c)
					err := tokenizeAll(decl + sh.pre + string(body) + sh.post)
					if want := legal(byte(c), v11); (err == nil) != want {
						t.Fatalf("v1.1=%v %s: byte %#02x at offset %d: error %v, want legal=%v",
							v11, sh.name, c, off, err, want)
					}
				}
			}
		}
	}
}

func TestCheckCharsInvalidUTF8InLongASCIIRun(t *testing.T) {
	for _, bad := range []string{"\xff", "\xc3", "\xe2\x82", "\xed\xa0\x80"} {
		src := "<a>" + strings.Repeat("abcdefgh", 3) + bad + strings.Repeat("abcdefgh", 3) + "</a>"
		if err := tokenizeAll(src); err == nil || !strings.Contains(err.Error(), "UTF-8") && !strings.Contains(err.Error(), "illegal character") {
			t.Errorf("%q: error %v, want invalid UTF-8 refused", bad, err)
		}
	}
	if err := tokenizeAll("<a>" + strings.Repeat("abcdefgh", 3) + "é€😀" + strings.Repeat("abcdefgh", 3) + "</a>"); err != nil {
		t.Errorf("valid multi-byte text refused: %v", err)
	}
}

// Under XML 1.0 a character reference's extent is not recorded, so checkChars
// re-reads the expanded bytes; a reference names only a [2] Char, which reads
// the same literally, so the first refusal, its text and its line are those a
// recorded extent gave. The expected errors were taken from the build that
// recorded extents in both versions.
func TestCheckCharsReferenceNextToBadLiteral10(t *testing.T) {
	runCases(t, []tokenCase{
		{name: "after ref", src: "<a>&#65;\x01</a>", wantErr: "XML syntax error on line 1: illegal character code U+0001"},
		{name: "before ref", src: "<a>\x01&#65;</a>", wantErr: "XML syntax error on line 1: illegal character code U+0001"},
		{name: "lines", src: "<a>\n&#x10000;\n\x0B&#xE9;\n</a>", wantErr: "XML syntax error on line 4: illegal character code U+000B"},
		{name: "lead byte then ref", src: "<a>abcdefgh\xe2&#xE9;abcdefgh</a>", wantErr: "XML syntax error on line 1: invalid UTF-8"},
		{name: "attribute", src: "<a b='&#9;&#13;&#x85;x\x7F\x01'/>", wantErr: "XML syntax error on line 1: illegal character code U+0001"},
		{name: "refs only", src: "<a b='&#x2028;&#127;'>&#x85;&#xD;&#x10FFFF;" + strings.Repeat("x", 9) + "</a>",
			want: `<a b="\u2028\x7f">"\u0085\r\U0010ffff` + strings.Repeat("x", 9) + `"</a>`},
	})
}
