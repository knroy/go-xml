// Package xmltok is go-xml's XML tokeniser: it turns a byte stream into a flat
// sequence of markup and character-data tokens, and nothing more.
//
// It is original work for go-xml, written against the XML 1.0 Fifth Edition
// and XML 1.1 Second Edition Recommendations, and is covered by the module's
// MIT licence like the rest of the repository. It replaces internal/xmlfork, a
// hand-maintained fork of Go's encoding/xml, and it reproduces that fork's
// observable behaviour — token boundaries, error text and line numbers,
// InputOffset — so that the switch is invisible to xdm. Where that behaviour
// departs from the Recommendations the departure is kept on purpose and is
// marked "parity:" below. Six have since been corrected, each measured
// against the W3C XML Conformance Test Suite: white space between attributes,
// white space after a PI target, [2] Char in comments and PI bodies, version
// 1.x read as 1.0, and Literal, which lets xdm refuse a reference or CDATA
// section outside the document element.
//
// What each part implements:
//
//   - text, checkChars: §2.2 Characters, [2] Char in both versions and XML 1.1
//     [2a] RestrictedChar, which 1.1 admits only as a character reference;
//     §2.4 Character Data, including the "]]>" prohibition; §2.11 End-of-Line
//     Handling, with 1.1's NEL and U+2028; §4.1 [66] CharRef and [68]
//     EntityRef, and §4.6 Predefined Entities.
//   - name, nsname: §2.3 [4] NameStartChar, [4a] NameChar and [5] Name, via
//     internal/xmlname, which holds the one transcription of the 5e
//     productions that xdm shares; the prefix:local split is Namespaces in
//     XML 1.0 §3 [7] QName, as written, with no binding.
//   - comment: §2.5 Comments. procInst: §2.6 Processing Instructions.
//   - cdata: §2.7 CDATA Sections.
//   - xmlDecl: §2.8 [23] XMLDecl, [24] VersionInfo (1.1, or any other 1.x
//     read as 1.0), and §4.3.3 [80] EncodingDecl, which hands a non-UTF-8
//     stream to CharsetReader.
//   - directive: §2.8 [28] doctypedecl, returned unparsed for xdm to read.
//   - startTag, endTag: §3.1 [40] STag, [41] Attribute, [42] ETag, [44]
//     EmptyElemTag. Tags are not paired here; xdm does that.
//
// The Decoder reads its input through its own window rather than a byte at a
// time, so the common runs — text between markup, names, comment and PI bodies
// — are scanned and copied in bulk. Nothing recurses on the input's structure,
// and the memory held per token is that token's own bytes.
package xmltok

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/knroy/go-xml/internal/xmlname"
)

// A SyntaxError is a well-formedness error, reported against the line the
// tokeniser had reached when it found it.
type SyntaxError struct {
	Msg  string
	Line int
}

func (e *SyntaxError) Error() string {
	return "XML syntax error on line " + strconv.Itoa(e.Line) + ": " + e.Msg
}

// A Name is a name as written: Space holds the prefix, not a namespace URI,
// and Local the part after the colon. An unprefixed name has an empty Space.
type Name struct {
	Space, Local string
}

// An Attr is one attribute specification, its value with references expanded
// and line ends normalised.
type Attr struct {
	Name  Name
	Value string
}

// A Token is one of *StartElement, *EndElement, *CharData, *Comment,
// *ProcInst or *Directive. It points into the Decoder, which reuses the same
// value for the next token of its kind, so a token -- like the byte slices
// and attribute slice it holds -- is valid only until the next call to
// RawToken. Handing out pointers rather than values is what keeps a token
// from costing an allocation: a struct or slice stored in an interface is
// copied to the heap, a pointer is not.
type Token any

// A StartElement is a start tag. An empty-element tag is returned as a
// StartElement followed by a matching EndElement on the next call.
type StartElement struct {
	Name Name
	Attr []Attr
}

// An EndElement is an end tag.
type EndElement struct {
	Name Name
}

// CharData is character data with references expanded; a CDATA section is
// returned as CharData too.
type CharData []byte

// A Comment is the text between <!-- and -->.
type Comment []byte

// A ProcInst is a processing instruction. The XML declaration is returned as
// one, with Target "xml".
type ProcInst struct {
	Target string
	Inst   []byte
}

// A Directive is the text between <! and > of a markup declaration that is
// neither a comment nor a CDATA section — in practice the DOCTYPE.
type Directive []byte

// bufSize is the read window. It matches bufio's default, which is what the
// stream was read through before.
const bufSize = 4096

// A Decoder reads tokens from an XML byte stream.
//
// A returned token, and the byte and attribute slices in it, alias the
// Decoder's own storage and are valid only until the next call to RawToken.
type Decoder struct {
	// Strict is kept for the caller's sake, which sets it; the Decoder is
	// strict whatever its value. xdm has never read a document any other way.
	Strict bool

	// Entity maps general entity names to replacement text. It is consulted
	// at each reference, not captured up front, so a caller may fill it
	// between calls — xdm does, from the internal subset. The replacement is
	// substituted as text and is not itself scanned for markup or references.
	// The five predefined entities are recognised whatever it holds.
	Entity map[string]string

	// CharsetReader converts a stream whose XML declaration names an
	// encoding other than UTF-8. It receives the bytes after the declaration
	// and returns UTF-8. Without one, such a declaration is an error.
	CharsetReader func(charset string, input io.Reader) (io.Reader, error)

	src    io.Reader
	srcErr error // first error from src; surfaced once buf is drained

	// buf[pos:] is unread input. Bytes before buf[0] have been read and
	// discarded: consumed counts them, and lines counts the newlines among
	// them, which is all the line number needs.
	buf      []byte
	pos      int
	consumed int64
	lines    int

	err error // sticky: once set, every call returns it

	scratch []byte    // the bytes of the token being built
	spans   []refSpan // in scratch, the extents produced by character references
	carry   []byte    // a name that straddled a refill
	attrs   []Attr    // backs StartElement.Attr, reused tag to tag

	// names interns the names this Decoder has returned, so that a document
	// repeating a few element and attribute names thousands of times
	// allocates each once. Only valid names are entered.
	names map[string]string

	endPending bool // the last tag was empty; its EndElement is owed
	endName    Name

	v11      bool // the XML declaration said version="1.1"
	declSeen bool // the first <?xml?> has been read; the version is fixed

	literal bool // the last CharData had no reference and was not CDATA

	// The values RawToken returns pointers to, one per kind, reused.
	tokStart   StartElement
	tokEnd     EndElement
	tokText    CharData
	tokComment Comment
	tokPI      ProcInst
	tokDir     Directive
}

// NewDecoder returns a Decoder reading from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{Strict: true, src: r, buf: make([]byte, 0, bufSize)}
}

// IsVersion11 reports whether the document declared version="1.1". The
// version comes from the XML declaration and nowhere else, so there is no
// setter: a caller able to assert a version could give a 1.0 document 1.1's
// relaxations. It is meaningful once the declaration has been read.
func (d *Decoder) IsVersion11() bool { return d.v11 }

// Literal reports whether the CharData last returned was written out as it
// stands: no reference was expanded in it and it is not a CDATA section.
// Outside the document element [27] Misc admits only literal S, so a caller
// needs this as well as the text to tell "&#32;" or "<![CDATA[]]>" there from
// a space.
func (d *Decoder) Literal() bool { return d.literal }

// InputOffset returns the stream offset just past the last token returned,
// counted in the bytes the tokeniser read — after CharsetReader conversion,
// where there was one.
func (d *Decoder) InputOffset() int64 { return d.consumed + int64(d.pos) }

// line is the line the read position is on, counting from 1. Only \n counts:
// a bare \r ends a line once normalised, but not for this purpose.
func (d *Decoder) line() int {
	return 1 + d.lines + bytes.Count(d.buf[:d.pos], newline)
}

var newline = []byte{'\n'}

func (d *Decoder) syntaxError(msg string) error {
	d.err = &SyntaxError{Msg: msg, Line: d.line()}
	return d.err
}

// fill replaces the drained window with the next read from src. It is called
// only when pos == len(buf), so the whole window is accounted for first.
func (d *Decoder) fill() bool {
	if d.srcErr != nil {
		return false
	}
	d.lines += bytes.Count(d.buf, newline)
	d.consumed += int64(len(d.buf))
	d.buf, d.pos = d.buf[:cap(d.buf)], 0
	// An io.Reader may return 0, nil; bufio gives up after 100 such reads,
	// and so does this.
	for range 100 {
		n, err := d.src.Read(d.buf)
		d.srcErr = err
		if n > 0 || err != nil {
			d.buf = d.buf[:n]
			return n > 0
		}
	}
	d.buf, d.srcErr = d.buf[:0], io.ErrNoProgress
	return false
}

// getc consumes one byte. At the end of input it records why in d.err —
// io.EOF, or the reader's error — and reports false.
func (d *Decoder) getc() (byte, bool) {
	if d.pos < len(d.buf) {
		b := d.buf[d.pos]
		d.pos++
		return b, true
	}
	if d.err == nil && d.fill() {
		d.pos = 1
		return d.buf[0], true
	}
	if d.err == nil {
		d.err = d.srcErr
	}
	return 0, false
}

// mustgetc is getc where the construct being read is not finished, so that
// running out of input is a syntax error rather than the end of the document.
func (d *Decoder) mustgetc() (byte, bool) {
	b, ok := d.getc()
	if !ok && d.err == io.EOF {
		d.syntaxError("unexpected EOF")
	}
	return b, ok
}

// ungetc steps back over the byte just consumed. The byte is always still in
// the window: a refill happens before a byte is read, never after.
func (d *Decoder) ungetc() { d.pos-- }

// peek returns the next byte without consuming it.
func (d *Decoder) peek() (byte, bool) {
	if d.pos == len(d.buf) && (d.err != nil || !d.fill()) {
		return 0, false
	}
	return d.buf[d.pos], true
}

// space skips XML white space, [3] S, and reports whether there was any.
// Running out of input is not an error here; the caller's next mustgetc
// reports it.
func (d *Decoder) space() bool {
	from := d.InputOffset()
	for {
		for d.pos < len(d.buf) {
			switch d.buf[d.pos] {
			case ' ', '\t', '\n', '\r':
				d.pos++
			default:
				return d.InputOffset() > from
			}
		}
		if _, ok := d.peek(); !ok {
			if d.err == nil {
				d.err = d.srcErr
			}
			return d.InputOffset() > from
		}
	}
}

// RawToken returns the next token, or io.EOF at the end of the input. Names
// are returned as written, with no namespace processing, and end tags are not
// checked against start tags. After an error every call returns that error.
func (d *Decoder) RawToken() (Token, error) {
	if d.err != nil {
		return nil, d.err
	}
	if d.endPending {
		d.endPending = false
		d.tokEnd = EndElement{d.endName}
		return &d.tokEnd, nil
	}
	b, ok := d.getc()
	if !ok {
		return nil, d.err
	}
	if b != '<' {
		d.ungetc()
		d.literal = true // until reference says otherwise
		data, ok := d.text(0, false)
		if !ok {
			return nil, d.err
		}
		d.tokText = CharData(data)
		return &d.tokText, nil
	}
	if b, ok = d.mustgetc(); !ok {
		return nil, d.err
	}
	switch b {
	case '/':
		return d.endTag()
	case '?':
		return d.procInst()
	case '!':
		return d.bang()
	}
	d.ungetc()
	return d.startTag()
}

// endTag reads [42] ETag after "</".
func (d *Decoder) endTag() (Token, error) {
	name, ok := d.nsname()
	if !ok {
		return nil, d.orSyntaxError("expected element name after </")
	}
	d.space()
	b, ok := d.mustgetc()
	if !ok {
		return nil, d.err
	}
	if b != '>' {
		return nil, d.syntaxError("invalid characters between </" + name.Local + " and >")
	}
	d.tokEnd = EndElement{name}
	return &d.tokEnd, nil
}

// orSyntaxError reports msg unless a more specific error is already recorded.
func (d *Decoder) orSyntaxError(msg string) error {
	if d.err == nil {
		d.syntaxError(msg)
	}
	return d.err
}

// startTag reads [40] STag or [44] EmptyElemTag after "<". Each attribute
// follows white space, so <a b="1"c="2"> is refused. Duplicate attributes are
// xdm's to reject.
func (d *Decoder) startTag() (Token, error) {
	name, ok := d.nsname()
	if !ok {
		return nil, d.orSyntaxError("expected element name after <")
	}
	attrs := d.attrs[:0]
	for {
		spaced := d.space()
		b, ok := d.mustgetc()
		if !ok {
			return nil, d.err
		}
		if b == '>' {
			break
		}
		if b == '/' {
			if b, ok = d.mustgetc(); !ok {
				return nil, d.err
			}
			if b != '>' {
				return nil, d.syntaxError("expected /> in element")
			}
			d.endPending, d.endName = true, name
			break
		}
		if !spaced && len(attrs) > 0 {
			return nil, d.syntaxError("expected white space between attributes")
		}
		d.ungetc()
		an, ok := d.nsname()
		if !ok {
			return nil, d.orSyntaxError("expected attribute name in element")
		}
		d.space()
		if b, ok = d.mustgetc(); !ok {
			return nil, d.err
		}
		if b != '=' {
			return nil, d.syntaxError("attribute name without = in element")
		}
		d.space()
		if b, ok = d.mustgetc(); !ok {
			return nil, d.err
		}
		if b != '"' && b != '\'' {
			return nil, d.syntaxError("unquoted or missing attribute value in element")
		}
		v, ok := d.text(b, false)
		if !ok {
			return nil, d.err
		}
		attrs = append(attrs, Attr{an, string(v)})
	}
	d.attrs = attrs
	d.tokStart = StartElement{name, attrs}
	return &d.tokStart, nil
}

// procInst reads [16] PI after "<?", and treats target "xml" as [23] XMLDecl.
// The target is followed by white space or "?>", and the body is checked
// against [2] Char.
//
// parity: the body is not newline-normalised, and a target of "xml" is the
// declaration wherever it appears, not only at the start of the document.
func (d *Decoder) procInst() (Token, error) {
	target, ok := d.name()
	if !ok {
		return nil, d.orSyntaxError("expected target name after <?")
	}
	spaced := d.space()
	data, ok := d.until("?>", d.scratch[:0])
	if !ok {
		return nil, d.err
	}
	d.scratch = data
	if !spaced && len(data) > 0 {
		return nil, d.syntaxError("expected white space after processing instruction target " + target)
	}
	if !d.checkChars(data, nil) {
		return nil, d.err
	}
	if target == "xml" {
		if err := d.xmlDecl(string(data)); err != nil {
			d.err = err
			return nil, err
		}
	}
	d.tokPI = ProcInst{target, data}
	return &d.tokPI, nil
}

// xmlDecl applies the version and encoding of an XML declaration whose
// pseudo-attributes are decl.
//
// A version of 1.x other than 1.1 is read as 1.0, as §2.8 asks of a 1.0
// processor since the Fifth Edition.
//
// parity: an unrecognised version or encoding is a plain error, not a
// SyntaxError, and the pseudo-attributes are found by declValue's substring
// search rather than parsed by [24] and [80]. The version is taken from the
// first <?xml?> only; the encoding from every one.
func (d *Decoder) xmlDecl(decl string) error {
	ver := declValue(decl, "version")
	if ver != "" && !isVersionNum(ver) {
		return fmt.Errorf("xml: unsupported version %q; only versions 1.x are supported", ver)
	}
	if !d.declSeen {
		d.declSeen, d.v11 = true, ver == "1.1"
	}
	enc := declValue(decl, "encoding")
	if enc == "" || strings.EqualFold(enc, "utf-8") {
		return nil
	}
	if d.CharsetReader == nil {
		return fmt.Errorf("xml: encoding %q declared but Decoder.CharsetReader is nil", enc)
	}
	r, err := d.CharsetReader(enc, d.unread())
	if err != nil {
		return fmt.Errorf("xml: opening charset %q: %w", enc, err)
	}
	if r == nil {
		// A broken CharsetReader, not a broken document.
		panic("CharsetReader returned a nil Reader for charset " + enc)
	}
	d.lines += bytes.Count(d.buf[:d.pos], newline)
	d.consumed += int64(d.pos)
	d.buf, d.pos = d.buf[:0], 0
	d.src, d.srcErr = r, nil
	return nil
}

// isVersionNum reports whether v is [26] VersionNum, '1.' [0-9]+.
func isVersionNum(v string) bool {
	digits, ok := strings.CutPrefix(v, "1.")
	if !ok || digits == "" {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// unread returns the rest of the stream: what is left in the window, then the
// source. The window is copied because it is about to be reused.
func (d *Decoder) unread() io.Reader {
	rest := bytes.NewReader(bytes.Clone(d.buf[d.pos:]))
	if d.srcErr != nil {
		return io.MultiReader(rest, errReader{d.srcErr})
	}
	return io.MultiReader(rest, d.src)
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// declValue returns the value of pseudo-attribute key in an XML declaration's
// body: the text between the quotes after the first `key=` that is directly
// followed by a quote. A `key=` followed by anything else is passed over,
// along with the byte after it, and the search resumes. White space around
// "=", which [25] Eq allows, is not recognised, so such a key reads as
// absent.
func declValue(decl, key string) string {
	key += "="
	for from := 0; ; {
		i := strings.Index(decl[from:], key)
		if i < 0 {
			return ""
		}
		q := from + i + len(key)
		if q >= len(decl) {
			return ""
		}
		if c := decl[q]; c == '"' || c == '\'' {
			n := strings.IndexByte(decl[q+1:], c)
			if n < 0 {
				return ""
			}
			return decl[q+1 : q+1+n]
		}
		from = q + 1
	}
}

// bang reads whatever follows "<!": a comment, a CDATA section or a
// directive.
func (d *Decoder) bang() (Token, error) {
	b, ok := d.mustgetc()
	if !ok {
		return nil, d.err
	}
	switch b {
	case '-':
		return d.comment()
	case '[':
		for i := 0; i < len("CDATA["); i++ {
			if b, ok = d.mustgetc(); !ok {
				return nil, d.err
			}
			if b != "CDATA["[i] {
				return nil, d.syntaxError("invalid <![ sequence")
			}
		}
		d.literal = false
		data, ok := d.text(0, true)
		if !ok {
			return nil, d.err
		}
		d.tokText = CharData(data)
		return &d.tokText, nil
	}
	return d.directive(b)
}

// comment reads [15] Comment after "<!-", checking the body against [2] Char.
//
// parity: the body is not newline-normalised, as for a PI.
func (d *Decoder) comment() (Token, error) {
	b, ok := d.mustgetc()
	if !ok {
		return nil, d.err
	}
	if b != '-' {
		return nil, d.syntaxError("invalid sequence <!- not part of <!--")
	}
	data, ok := d.until("--", d.scratch[:0])
	if !ok {
		return nil, d.err
	}
	d.scratch = data
	if b, ok = d.mustgetc(); !ok {
		return nil, d.err
	}
	if b != '>' {
		return nil, d.syntaxError(`invalid sequence "--" not allowed in comments`)
	}
	if !d.checkChars(data, nil) {
		return nil, d.err
	}
	d.tokComment = Comment(data)
	return &d.tokComment, nil
}

// directive reads a markup declaration after "<!", whose first byte b has
// already been read, up to the ">" that closes it.
//
// Quoted text is opaque. Outside quotes each "<" opens a level that a ">"
// closes, except that "<!--" begins a comment, which is dropped up to its
// "-->" and replaced by one space, so that the text either side of it is not
// joined into something new. The first byte is taken as it stands: it neither
// opens a quote nor a level, and cannot close the directive.
//
// parity: the text is returned raw, and a comment inside it is not checked
// for "--".
func (d *Decoder) directive(b byte) (Token, error) {
	out := append(d.scratch[:0], b)
	var quote byte
	depth := 0
	for {
		b, ok := d.mustgetc()
		if !ok {
			return nil, d.err
		}
		if quote == 0 && depth == 0 && b == '>' {
			break
		}
		// One pass handles b; a failed "<!--" match hands its mismatching
		// byte back round for a second.
		for again := true; again; {
			again = false
			out = append(out, b)
			switch {
			case quote != 0:
				if b == quote {
					quote = 0
				}
			case b == '"' || b == '\'':
				quote = b
			case b == '>':
				depth--
			case b == '<':
				n := 0
				for n < len("!--") {
					if b, ok = d.mustgetc(); !ok {
						return nil, d.err
					}
					if b != "!--"[n] {
						break
					}
					n++
				}
				if n < len("!--") {
					out = append(out, "!--"[:n]...)
					depth++
					again = true
					continue
				}
				mark := len(out) - 1
				if out, ok = d.until("-->", out); !ok {
					return nil, d.err
				}
				out = append(out[:mark], ' ')
			}
		}
	}
	d.scratch = out
	d.tokDir = Directive(out)
	return &d.tokDir, nil
}

// until consumes input up to and including the first occurrence of term,
// appends what preceded it to dst, and returns dst.
func (d *Decoder) until(term string, dst []byte) ([]byte, bool) {
	base := len(dst)
	for {
		// term may straddle the previous window and this one, so the search
		// starts len(term)-1 bytes back into what was already copied.
		from := max(base, len(dst)-len(term)+1)
		had := len(dst)
		dst = append(dst, d.buf[d.pos:]...)
		if i := bytes.Index(dst[from:], []byte(term)); i >= 0 {
			end := from + i + len(term)
			d.pos += end - had
			return dst[:from+i], true
		}
		d.pos = len(d.buf)
		if _, ok := d.mustgetc(); !ok {
			return dst, false
		}
		d.ungetc()
	}
}

// nsname reads a name and splits it at its colon into prefix and local part.
// A name with more than one colon is refused without an error of its own, so
// that the caller reports what it was expecting. A leading or trailing colon
// leaves the whole name in Local.
func (d *Decoder) nsname() (Name, bool) {
	s, ok := d.name()
	if !ok {
		return Name{}, false
	}
	i := strings.IndexByte(s, ':')
	switch {
	case i < 0:
		return Name{Local: s}, true
	case strings.IndexByte(s[i+1:], ':') >= 0:
		return Name{}, false
	case i == 0 || i == len(s)-1:
		return Name{Local: s}, true
	}
	return Name{Space: s[:i], Local: s[i+1:]}, true
}

// name reads [5] Name. If the input does not start with a name character it
// consumes nothing and reports false without an error, leaving the message to
// the caller.
func (d *Decoder) name() (string, bool) {
	b, ok := d.nameBytes()
	if !ok {
		return "", false
	}
	if s, ok := d.names[string(b)]; ok {
		return s, true
	}
	if !isName(b) {
		d.syntaxError("invalid XML name: " + string(b))
		return "", false
	}
	s := string(b)
	// ponytail: entries stop at maxInternedNames, so a document of unique
	// names costs a bounded map; later names are allocated as before.
	if len(d.names) < maxInternedNames {
		if d.names == nil {
			d.names = make(map[string]string)
		}
		d.names[s] = s
	}
	return s, true
}

// maxInternedNames bounds the name table of one Decoder.
const maxInternedNames = 4096

// nameBytes consumes a run of bytes that may belong to a name: the ASCII name
// characters and every non-ASCII byte, whose validity isName decides once the
// run is decoded. The result is valid until the next read.
func (d *Decoder) nameBytes() ([]byte, bool) {
	start := d.pos
	for d.pos < len(d.buf) && nameByte[d.buf[d.pos]] {
		d.pos++
	}
	if d.pos < len(d.buf) {
		return d.buf[start:d.pos], d.pos > start
	}
	// The run reaches the end of the window: carry it over the refill.
	d.carry = append(d.carry[:0], d.buf[start:]...)
	for {
		b, ok := d.mustgetc()
		if !ok {
			return nil, false
		}
		if !nameByte[b] {
			d.ungetc()
			return d.carry, len(d.carry) > 0
		}
		d.carry = append(d.carry, b)
	}
}

// nameByte marks the bytes nameBytes accepts.
var nameByte = func() (t [256]bool) {
	for c := range t {
		t[c] = c >= utf8.RuneSelf || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' ||
			'0' <= c && c <= '9' || c == '_' || c == ':' || c == '.' || c == '-'
	}
	return
}()

// isName reports whether b, a run from nameBytes, is a [5] Name.
func isName(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	ascii := true
	for _, c := range b {
		if c >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		// Every ASCII byte nameBytes accepts is a NameChar; only digits,
		// '-' and '.' cannot start a name.
		c := b[0]
		return !('0' <= c && c <= '9' || c == '-' || c == '.')
	}
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n == 1 {
			return false
		}
		if i == 0 && !xmlname.IsNameStartRune(r) || !xmlname.IsNameRune(r) {
			return false
		}
		i += n
	}
	return true
}

// predefined are the entities of §4.6, recognised whether declared or not.
var predefined = map[string]string{
	"lt": "<", "gt": ">", "amp": "&", "apos": "'", "quot": `"`,
}

// refSpan is a half-open extent of scratch that a character reference
// produced. XML 1.1 admits a RestrictedChar only as a reference, and once
// expanded it is the same bytes as the literal character, so which bytes came
// from a reference has to be recorded as they are written.
type refSpan struct{ start, end int }

// Byte classes that end a bulk run in text. Which of them matter depends on
// what is being read; see text.
const (
	clLT    = 1 << iota // '<'
	clAmp               // '&'
	clGT                // '>', which ends "]]>"
	clCR                // '\r'
	clDQ                // '"'
	clSQ                // '\''
	clNEL11             // last byte of NEL (C2 85) or U+2028 (E2 80 A8)
	clWS                // '\t' or '\n', which §3.3.3 maps to a space in a value
)

var class = func() (t [256]uint8) {
	t['<'], t['&'], t['>'], t['\r'], t['"'], t['\''] = clLT, clAmp, clGT, clCR, clDQ, clSQ
	t[0x85], t[0xA8] = clNEL11, clNEL11
	t['\t'], t['\n'] = clWS, clWS
	return
}()

// text reads character data up to the markup that ends it: [14] CharData in
// content, [20] CData inside a CDATA section (cdata), or [10] AttValue's
// characters when quote is the opening quote. References are expanded except
// in a CDATA section, and line ends are normalised (§2.11). The characters
// are checked against [2] once the run is complete.
//
// The ending markup is consumed for a CDATA section ("]]>") and an attribute
// value (the quote), and left unread for content ("<"). Content and attribute
// values may also end at the end of input; xdm or the caller's next read
// reports that.
//
// In an attribute value a literal TAB, LF or CR (CR-LF counting as one) is
// replaced by a space, as §3.3.3 asks, while a character reference to one of
// them is kept as the character: the rewrite sees only literal input bytes.
//
// parity: a 1.1 NEL or U+2028 in an attribute value becomes a newline, not a
// space, and an entity's replacement text is not normalised.
func (d *Decoder) text(quote byte, cdata bool) ([]byte, bool) {
	var stop uint8
	switch {
	case cdata:
		stop = clGT | clCR
	case quote == '"':
		stop = clDQ | clLT | clAmp | clCR | clWS
	case quote == '\'':
		stop = clSQ | clLT | clAmp | clCR | clWS
	default:
		stop = clLT | clAmp | clGT | clCR
	}
	if d.v11 {
		stop |= clNEL11
	}
	out := d.scratch[:0]
	spans := d.spans[:0]
	// p1 and p2 are the last two input bytes read as literal text, p1 the
	// later; a reference resets both. "]]>", \r\n and the 1.1 line ends are
	// recognised from them at their final byte.
	var p1, p2 byte
	for {
		if run := d.buf[d.pos:]; len(run) > 0 {
			n := 0
			for n < len(run) && class[run[n]]&stop == 0 {
				n++
			}
			if n > 0 {
				out = append(out, run[:n]...)
				if n == 1 {
					p2, p1 = p1, run[0]
				} else {
					p2, p1 = run[n-2], run[n-1]
				}
				d.pos += n
				if n == len(run) {
					continue
				}
			}
		}
		b, ok := d.getc()
		if !ok {
			if cdata {
				if d.err == io.EOF {
					d.syntaxError("unexpected EOF in CDATA section")
				}
				return nil, false
			}
			break
		}
		switch {
		case class[b]&stop == 0:
			// A plain byte that arrived through a refill.
			out = append(out, b)
		case b == '>' && quote == 0 && p2 == ']' && p1 == ']':
			if !cdata {
				d.syntaxError("unescaped ]]> not in CDATA section")
				return nil, false
			}
			return d.finishText(out[:len(out)-2], spans)
		case b == '<':
			if quote != 0 {
				d.syntaxError("unescaped < inside quoted string")
				return nil, false
			}
			d.ungetc()
			return d.finishText(out, spans)
		case b == quote && quote != 0:
			return d.finishText(out, spans)
		case b == '&':
			if out, spans, ok = d.reference(out, spans); !ok {
				return nil, false
			}
			p1, p2 = 0, 0
			continue
		case quote != 0 && (b == '\t' || b == '\n' || b == '\r'):
			// §3.3.3. b becomes the space, so a NEL after a CR is read as a
			// line end of its own, as it was when this rewrite ran upstream.
			if c, ok := d.peek(); ok && b == '\r' && c == '\n' {
				d.pos++
			}
			b = ' '
			out = append(out, b)
		case b == '\r':
			out = append(out, '\n')
			if c, ok := d.peek(); ok && c == '\n' {
				d.pos++
				b, p1 = '\n', '\r'
			}
		case b == 0x85 && p1 == 0xC2:
			// NEL. Its lead byte is already written; \r NEL is one line
			// end, and the \r has already been written as \n.
			out = out[:len(out)-1]
			if p2 != '\r' {
				out = append(out, '\n')
			}
		case b == 0xA8 && p1 == 0x80 && p2 == 0xE2:
			// U+2028, likewise.
			out = append(out[:len(out)-2], '\n')
		default:
			out = append(out, b)
		}
		p2, p1 = p1, b
	}
	return d.finishText(out, spans)
}

// finishText checks the run against [2] and keeps its buffers for reuse.
func (d *Decoder) finishText(out []byte, spans []refSpan) ([]byte, bool) {
	d.scratch, d.spans = out, spans
	if !d.checkChars(out, spans) {
		return nil, false
	}
	return out, true
}

// reference reads a reference after its "&" and appends its expansion to
// out: §4.1 [66] CharRef, or [68] EntityRef resolved against the predefined
// entities and then d.Entity.
//
// A character reference is checked against [2] here, while it is still known
// to be one, and its extent recorded in spans for checkChars to skip.
//
// parity: an entity's replacement text is not newline-normalised, but
// checkChars does see it, as if it had been literal.
func (d *Decoder) reference(out []byte, spans []refSpan) ([]byte, []refSpan, bool) {
	d.literal = false
	start := len(out)
	out = append(out, '&')
	b, ok := d.mustgetc()
	if !ok {
		return out, spans, false
	}
	var repl string
	var found, charRef bool
	if b == '#' {
		out = append(out, '#')
		if b, ok = d.mustgetc(); !ok {
			return out, spans, false
		}
		base := 10
		if b == 'x' {
			base = 16
			out = append(out, 'x')
			if b, ok = d.mustgetc(); !ok {
				return out, spans, false
			}
		}
		digits := len(out)
		for '0' <= b && b <= '9' || base == 16 && ('a' <= b && b <= 'f' || 'A' <= b && b <= 'F') {
			out = append(out, b)
			if b, ok = d.mustgetc(); !ok {
				return out, spans, false
			}
		}
		if b != ';' {
			d.ungetc()
		} else {
			// Parsed by hand: anything past MaxRune is refused whatever its
			// length, so overflow cannot arise.
			r, inRange := rune(0), len(out) > digits
			for _, c := range out[digits:] {
				v := rune(c - '0')
				if c >= 'a' {
					v = rune(c-'a') + 10
				} else if c >= 'A' {
					v = rune(c-'A') + 10
				}
				if r = r*rune(base) + v; r > utf8.MaxRune {
					inRange = false
					break
				}
			}
			out = append(out, ';')
			if inRange {
				if !isChar(r, d.v11, false) {
					d.syntaxError(fmt.Sprintf("illegal character code %U", r))
					return out, spans, false
				}
				repl, found, charRef = string(r), true, true
			}
		}
	} else {
		d.ungetc()
		name, ok := d.nameBytes()
		if !ok && d.err != nil {
			return out, spans, false
		}
		out = append(out, name...)
		if b, ok = d.mustgetc(); !ok {
			return out, spans, false
		}
		if b != ';' {
			d.ungetc()
		} else {
			name := out[start+1:]
			out = append(out, ';')
			if isName(name) {
				if repl, found = predefined[string(name)]; !found && d.Entity != nil {
					repl, found = d.Entity[string(name)]
				}
			}
		}
	}
	if !found {
		ref := string(out[start:])
		if ref[len(ref)-1] != ';' {
			ref += " (no semicolon)"
		}
		d.syntaxError("invalid character entity " + ref)
		return out, spans, false
	}
	out = append(out[:start], repl...)
	if charRef {
		spans = append(spans, refSpan{start, len(out)})
	}
	return out, spans, true
}

// checkChars checks literal text against [2] Char, and under XML 1.1 against
// [2a] as well. The extents in spans came from character references, which
// were checked as they were read, and are skipped.
//
// parity: an error is reported against the line where the run ended, not the
// line of the offending character.
func (d *Decoder) checkChars(b []byte, spans []refSpan) bool {
	ok := &asciiChar10
	if d.v11 {
		ok = &asciiChar11
	}
	for i := 0; i < len(b); {
		if len(spans) > 0 && i >= spans[0].start {
			i = max(i, spans[0].end)
			spans = spans[1:]
			continue
		}
		if c := b[i]; c < utf8.RuneSelf {
			if !ok[c] {
				d.syntaxError(fmt.Sprintf("illegal character code %U", rune(c)))
				return false
			}
			i++
			continue
		}
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n == 1 {
			d.syntaxError("invalid UTF-8")
			return false
		}
		if !isChar(r, d.v11, true) {
			d.syntaxError(fmt.Sprintf("illegal character code %U", r))
			return false
		}
		i += n
	}
	return true
}

// asciiChar10 and asciiChar11 tabulate isChar for literal ASCII.
var asciiChar10, asciiChar11 = func() (t10, t11 [utf8.RuneSelf]bool) {
	for c := range t10 {
		t10[c] = isChar(rune(c), false, true)
		t11[c] = isChar(rune(c), true, true)
	}
	return
}()

// isChar reports whether r is a character of the document's version: XML 1.0
// [2] Char, or XML 1.1 [2] Char less, when literal, the [2a] RestrictedChar
// that 1.1 admits only through a character reference. 1.0 has no
// RestrictedChar: the characters it would name are not characters at all.
func isChar(r rune, v11, literal bool) bool {
	switch {
	case r >= 0x10000:
		return r <= utf8.MaxRune
	case r >= 0xE000:
		return r <= 0xFFFD
	case r > 0xD7FF:
		return false
	case !v11:
		return r >= 0x20 || r == '\t' || r == '\n' || r == '\r'
	case r == 0:
		return false
	case !literal:
		return true
	}
	restricted := r <= 0x8 || r == 0xB || r == 0xC || 0xE <= r && r <= 0x1F ||
		0x7F <= r && r <= 0x84 || 0x86 <= r && r <= 0x9F
	return !restricted
}
