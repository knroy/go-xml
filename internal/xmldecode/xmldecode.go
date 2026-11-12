// Package xmldecode detects and decodes the character encoding of an XML
// entity: a document entity, an external parsed entity or an external DTD
// subset. xdm and dtd both read entities, and both go through Reader, so the
// two cannot disagree about which bytes are UTF-16.
package xmldecode

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"unicode/utf16"
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

// Reader wraps br so that a UTF-16 entity is decoded to UTF-8.
//
// The BOM is consumed when present. A UTF-8 BOM is also removed: it is legal
// and carries no information, but encoding/xml treats it as content and a
// document beginning with one would fail to parse.
func Reader(br *bufio.Reader) (io.Reader, error) {
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
		decl, _ := br.Peek(declPeek)
		if enc := declaredEncoding(string(decl)); enc != "" && !strings.EqualFold(enc, "UTF-8") {
			return nil, fmt.Errorf("UTF-8 byte order mark contradicts encoding=%q", enc)
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
	// A UTF-8 stream is handed to the tokeniser as it stands. The version
	// declaration used to be rewritten here; it is now the tokeniser's to
	// read, which is the whole point — see internal/xmltok's IsVersion11.
	return br, nil
}

// declPeek bounds how far Reader looks for the XML declaration. An
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
//
// Only the declaration needs looking ahead at, so only it is held: decoded
// text is kept back until it is known not to begin "<?xml", or up to the
// first ">" that ends the declaration, and everything after streams through
// a chunk at a time. Memory is the declaration plus one chunk, not the
// document.
type utf16Reader struct {
	src       *bufio.Reader
	bigEndian bool
	raw       []byte       // the chunk being read; a carried odd byte or high surrogate first
	units     []uint16     // raw's complete code units, reused
	out       bytes.Buffer // decoded UTF-8 not yet returned
	declDone  bool         // the declaration has been checked and rewritten
	err       error        // returned once out is drained
}

// utf16Chunk is how many bytes of UTF-16 one fill reads: large enough that
// the per-call overhead vanishes, small enough that a streamed document's
// decoding costs a fixed buffer rather than a copy of itself.
const utf16Chunk = 32 << 10

func newUTF16Reader(src *bufio.Reader, bigEndian bool) *utf16Reader {
	return &utf16Reader{src: src, bigEndian: bigEndian}
}

// Read implements io.Reader.
func (u *utf16Reader) Read(p []byte) (int, error) {
	for !u.declDone {
		if u.err == nil && declOpen(u.out.Bytes()) {
			u.fill()
			continue
		}
		u.declDone = true
		if u.err != nil && u.err != io.EOF {
			u.out.Reset()
			return 0, u.err
		}
		text := u.out.String()
		u.out.Reset()
		if enc := declaredEncoding(text); enc != "" && !strings.HasPrefix(strings.ToUpper(enc), "UTF-16") {
			u.err = fmt.Errorf("UTF-16 input contradicts encoding=%q", enc)
			return 0, u.err
		}
		// Only the ENCODING declaration is rewritten: the text really is
		// UTF-8 now, so a declaration naming UTF-16 has become false. The
		// VERSION is left exactly as written — the tokeniser reads it, and a
		// 1.1 document that arrived as UTF-16 is still a 1.1 document.
		u.out.WriteString(rewriteEncodingDecl(text))
	}
	for u.out.Len() == 0 {
		if u.err != nil {
			return 0, u.err
		}
		u.fill()
	}
	return u.out.Read(p)
}

// declOpen reports whether the decoded text so far may still be the start of
// an XML declaration that has not ended: it begins "<?xml", or could, and
// holds no ">". A ">" before the "?>" leaves the declaration malformed, which
// the tokeniser reports, so it ends the look-ahead either way.
func declOpen(text []byte) bool {
	const open = "<?xml"
	if len(text) < len(open) {
		return strings.HasPrefix(open, string(text))
	}
	return bytes.HasPrefix(text, []byte(open)) && bytes.IndexByte(text, '>') < 0
}

// fill decodes the next chunk onto out, carrying an odd trailing byte or a
// high surrogate whose pair may be in the next chunk. At the end of input it
// sets err: io.EOF, or an error for an odd byte count, which cannot be UTF-16
// and whose truncation would lose the end of the document silently.
func (u *utf16Reader) fill() {
	carry := len(u.raw)
	if cap(u.raw) < carry+utf16Chunk {
		u.raw = append(make([]byte, 0, carry+utf16Chunk), u.raw...)
	}
	n, err := io.ReadFull(u.src, u.raw[carry:carry+utf16Chunk])
	u.raw = u.raw[:carry+n]
	eof := err == io.EOF || err == io.ErrUnexpectedEOF
	if err != nil && !eof {
		u.err = err
		return
	}
	u.units = u.units[:0]
	for i := 0; i+1 < len(u.raw); i += 2 {
		hi, lo := u.raw[i], u.raw[i+1]
		if u.bigEndian {
			u.units = append(u.units, uint16(hi)<<8|uint16(lo))
		} else {
			u.units = append(u.units, uint16(lo)<<8|uint16(hi))
		}
	}
	keep := len(u.raw) % 2
	if !eof && len(u.units) > 0 && utf16.IsSurrogate(rune(u.units[len(u.units)-1])) &&
		u.units[len(u.units)-1] < 0xDC00 {
		u.units = u.units[:len(u.units)-1]
		keep += 2
	}
	for _, r := range utf16.Decode(u.units) {
		u.out.WriteRune(r)
	}
	u.raw = append(u.raw[:0], u.raw[len(u.raw)-keep:]...)
	if eof {
		if keep != 0 {
			u.err = fmt.Errorf("UTF-16 input has an odd number of bytes")
			return
		}
		u.err = io.EOF
	}
}

// encodingDecl finds [80] EncodingDecl in an XML declaration at the start of s.
var encodingDecl = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`^<\?xml[ \t\r\n][^?>]*?encoding[ \t\r\n]*=[ \t\r\n]*["']([A-Za-z][A-Za-z0-9._-]*)["']`)
})

// declaredEncoding returns the encoding an XML declaration names, or "".
// XML 1.0 §4.3.3 makes it a fatal error for an entity to be presented in an
// encoding other than the one its declaration names, and a byte order mark
// is that presentation (Appendix F): a UTF-8 BOM before encoding="ISO-8859-1"
// leaves two readings of the same bytes, so the document is refused.
func declaredEncoding(s string) string {
	if m := encodingDecl().FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// rewriteEncodingDecl rewrites the encoding an XML declaration names to UTF-8.
//
// The text has already been decoded to UTF-8, so a declaration naming UTF-16 is
// now false, and the tokeniser would act on it. It is rewritten rather than
// removed because the same decoding serves an external entity, whose text
// declaration must name an encoding (XML 1.0 §4.3.1 [77] TextDecl): removing
// it left "<?xml version='1.0'?>", which is not one.
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
	return decl[:i] + `encoding="UTF-8"` + decl[cut:] + s[end:]
}
