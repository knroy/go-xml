package xdm

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Character encoding detection and decoding.
//
// XML 1.0 §4.3.3 requires every processor to accept UTF-8 and UTF-16, and a
// document in UTF-16 must begin with a byte order mark. Go's encoding/xml reads
// UTF-8 and hands anything else to a CharsetReader, which is nil by default —
// so without this a UTF-16 document fails with "invalid UTF-8" rather than
// being read.
//
// Only the two encodings the spec makes mandatory are handled. A document in
// some other encoding gets a clear error naming it, rather than being decoded
// wrongly: guessing at an unknown encoding produces text that parses and means
// something different, which is worse than a refusal.

// decodeReader wraps r so that a UTF-16 document is decoded to UTF-8.
//
// The BOM is consumed when present. A UTF-8 BOM is also removed: it is legal
// and carries no information, but encoding/xml treats it as content and a
// document beginning with one would fail to parse.
func decodeReader(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, 4096)
	prefix, err := br.Peek(4)
	if err != nil && err != io.EOF && !errIsShort(err) {
		return nil, err
	}

	switch {
	case len(prefix) >= 2 && prefix[0] == 0xFE && prefix[1] == 0xFF:
		if _, err := br.Discard(2); err != nil {
			return nil, err
		}
		return newUTF16Reader(br, true), nil

	case len(prefix) >= 2 && prefix[0] == 0xFF && prefix[1] == 0xFE:
		if _, err := br.Discard(2); err != nil {
			return nil, err
		}
		return newUTF16Reader(br, false), nil

	case len(prefix) >= 3 && prefix[0] == 0xEF && prefix[1] == 0xBB && prefix[2] == 0xBF:
		// A UTF-8 BOM. encoding/xml would treat it as character data
		// before the root element, which is not well formed.
		if _, err := br.Discard(3); err != nil {
			return nil, err
		}
		return br, nil

	case len(prefix) >= 4 && prefix[0] == 0x3C && prefix[1] == 0x00 &&
		prefix[2] == 0x3F && prefix[3] == 0x00:
		// "<?" in UTF-16LE with no BOM. The spec requires the mark, but
		// the pattern is unambiguous and refusing a readable document
		// on that technicality helps nobody.
		return newUTF16Reader(br, false), nil

	case len(prefix) >= 4 && prefix[0] == 0x00 && prefix[1] == 0x3C &&
		prefix[2] == 0x00 && prefix[3] == 0x3F:
		// The same in big-endian.
		return newUTF16Reader(br, true), nil
	}
	return utf8VersionReader(br)
}

// utf8VersionReader restates an XML 1.1 declaration as 1.0 on a UTF-8 stream.
//
// Only the declaration is buffered — it is bounded and sits at the very start —
// so the rest of the document still streams. A document with no declaration, or
// one already naming 1.0, is handed back unchanged and unbuffered.
func utf8VersionReader(br *bufio.Reader) (io.Reader, error) {
	head, err := br.Peek(declPeek)
	if err != nil && err != io.EOF && !errIsShort(err) {
		return nil, err
	}
	if declaredVersion(string(head)) != "1.1" {
		return br, nil
	}
	end := strings.Index(string(head), "?>") + len("?>")
	if _, err := br.Discard(end); err != nil {
		return nil, err
	}
	return io.MultiReader(strings.NewReader(rewriteVersionDecl(string(head[:end]))), br), nil
}

// declPeek bounds how far decodeReader looks for the XML declaration. An
// declaration cannot reasonably exceed this, and Peek must not exceed the
// reader's buffer.
const declPeek = 256

func errIsShort(err error) bool {
	return err != nil && strings.Contains(err.Error(), "buffer")
}

// utf16Reader decodes UTF-16 to UTF-8 as it reads.
//
// It also rewrites the encoding declaration, because encoding/xml reads the
// bytes it is given and would otherwise see encoding="UTF-16" on text that is
// now UTF-8 and hand it to a CharsetReader that does not exist. Rewriting the
// declaration is what lets the decoder proceed without one.
type utf16Reader struct {
	src       *bufio.Reader
	bigEndian bool
	buf       bytes.Buffer
	started   bool
	err       error
}

func newUTF16Reader(src *bufio.Reader, bigEndian bool) *utf16Reader {
	return &utf16Reader{src: src, bigEndian: bigEndian}
}

// Read implements io.Reader.
func (u *utf16Reader) Read(p []byte) (int, error) {
	if !u.started {
		u.started = true
		if err := u.fill(); err != nil && err != io.EOF {
			return 0, err
		}
	}
	if u.buf.Len() == 0 {
		if u.err != nil {
			return 0, u.err
		}
		return 0, io.EOF
	}
	return u.buf.Read(p)
}

// fill decodes the whole input.
//
// A document is decoded in one pass rather than streamed because the encoding
// declaration has to be rewritten, and finding it means looking at the start of
// the text — which a streaming decoder would have already handed on. Schema and
// instance documents are small enough that this is not the cost it would be for
// arbitrary data.
func (u *utf16Reader) fill() error {
	raw, err := io.ReadAll(u.src)
	if err != nil {
		u.err = err
		return err
	}
	if len(raw)%2 != 0 {
		// An odd byte count cannot be UTF-16. Truncating the last byte
		// would decode most of the document and lose the end silently.
		u.err = fmt.Errorf("UTF-16 input has an odd number of bytes")
		return u.err
	}

	units := make([]uint16, len(raw)/2)
	for i := range units {
		hi, lo := raw[2*i], raw[2*i+1]
		if u.bigEndian {
			units[i] = uint16(hi)<<8 | uint16(lo)
		} else {
			units[i] = uint16(lo)<<8 | uint16(hi)
		}
	}

	text := string(utf16.Decode(units))
	u.buf.WriteString(rewriteVersionDecl(rewriteEncodingDecl(text)))
	u.err = io.EOF
	return nil
}

// rewriteEncodingDecl removes an encoding declaration from an XML declaration.
//
// The text has already been decoded to UTF-8, so a declaration naming UTF-16 is
// now false, and encoding/xml would act on it. Removing it rather than
// rewriting it to UTF-8 keeps the change minimal: with no declaration the
// decoder assumes UTF-8, which is what the text now is.
func rewriteEncodingDecl(s string) string {
	if !strings.HasPrefix(s, "<?xml") {
		return s
	}
	end := strings.Index(s, "?>")
	if end < 0 {
		return s
	}
	decl := s[:end]
	i := strings.Index(decl, "encoding")
	if i < 0 {
		return s
	}
	// Find the quoted value and cut from "encoding" through it.
	rest := decl[i+len("encoding"):]
	q := strings.IndexAny(rest, `"'`)
	if q < 0 {
		return s
	}
	quote := rest[q]
	closeAt := strings.IndexByte(rest[q+1:], quote)
	if closeAt < 0 {
		return s
	}
	cut := i + len("encoding") + q + 1 + closeAt + 1
	return strings.TrimRight(decl[:i], " \t") + decl[cut:] + s[end:]
}

// declaredVersion returns the version named by an XML declaration, or "" if the
// document has no declaration or no version pseudo-attribute.
func declaredVersion(s string) string {
	if !strings.HasPrefix(s, "<?xml") {
		return ""
	}
	end := strings.Index(s, "?>")
	if end < 0 {
		return ""
	}
	decl := s[:end]
	i := strings.Index(decl, "version")
	if i < 0 {
		return ""
	}
	rest := decl[i+len("version"):]
	q := strings.IndexAny(rest, `"'`)
	if q < 0 {
		return ""
	}
	quote := rest[q]
	closeAt := strings.IndexByte(rest[q+1:], quote)
	if closeAt < 0 {
		return ""
	}
	return rest[q+1 : q+1+closeAt]
}

// rewriteVersionDecl restates an XML 1.1 declaration as 1.0.
//
// encoding/xml is used as a tokeniser, and it refuses any version but 1.0
// outright — so a schema document written in XML 1.1 never reaches the parser
// at all. The saxonData XmlVersions tests (xv001..xv009) are exactly that: each
// declares version="1.1" and is otherwise an ordinary schema, with every
// 1.1-only character appearing inside an attribute value as a character
// reference, which the tokeniser accepts either way.
//
// Rewriting the declaration is deliberately all this does. The genuine 1.1
// differences — the wider NameStartChar and NameChar ranges, NEL and LINE
// SEPARATOR as line ends, and the stricter treatment of C0 controls — live
// inside the tokeniser, which is not ours to change. A document that actually
// depends on one of them still fails, and fails in the tokeniser as before;
// this only stops the version string alone from being the obstacle. Nothing
// about XML 1.0 parsing changes, because a 1.0 declaration is left untouched.
func rewriteVersionDecl(s string) string {
	if declaredVersion(s) != "1.1" {
		return s
	}
	end := strings.Index(s, "?>")
	decl := s[:end]
	i := strings.Index(decl, "version")
	rest := decl[i+len("version"):]
	q := strings.IndexAny(rest, `"'`)
	quote := rest[q]
	closeAt := strings.IndexByte(rest[q+1:], quote)
	valAt := i + len("version") + q + 1
	return s[:valAt] + "1.0" + s[valAt+closeAt:]
}

// validUTF8Prefix reports whether the first bytes of a document are valid
// UTF-8, used to give a clearer error than the decoder's.
func validUTF8Prefix(b []byte) bool {
	if len(b) > 64 {
		b = b[:64]
	}
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			return false
		}
		b = b[size:]
	}
	return true
}
