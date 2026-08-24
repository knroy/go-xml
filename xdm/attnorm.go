package xdm

import "io"

// Attribute-value normalization, XML 1.0 section 3.3.3.
//
// A literal TAB, LF or CR inside an attribute value is replaced by a single
// space before the value is reported. A CHARACTER REFERENCE to the same
// character is not: "&#xA;" denotes a newline the author asked for, and
// section 3.3.3 says a reference is appended "as is" while a literal
// whitespace character is normalized. The document
//
//	<td style="color: #336699; font-weight:
//	bold">
//
// therefore has the value "color: #336699; font-weight: bold", with a space
// where the line break was.
//
// WHY THIS IS A READER AND NOT A PASS OVER a.Value.
//
// encoding/xml decodes character references while tokenising, so by the time
// an attribute arrives as a Go string the two spellings are the same string:
// `<a s="x&#10;b"/>` and a literal newline both produce "x\nb" — verified.
// Replacing whitespace there would normalize the reference too, which is
// exactly what section 3.3.3 forbids, and would silently corrupt every
// document that spells a newline as &#10; — including the ones the XSD and
// RELAX NG suites parse.
//
// So the rewrite happens on the RAW BYTES, upstream of the decoder, where
// "&#10;" is still five characters and a newline is still one. That is the
// same decode-once discipline the entity rewrite in dtd_entities.go follows.
//
// Offsets are preserved exactly: one byte in, one byte out, always. Position
// tracking and the entity base spans both index into this stream, so a filter
// that changed lengths would move every node's recorded position.
//
// The scanner is byte-oriented, which is safe for UTF-8: every byte it acts on
// is ASCII, and no UTF-8 continuation byte is ASCII, so a multi-byte character
// can never be mistaken for one of the delimiters.

// attNormState is where the scanner is in the document.
type attNormState uint8

const (
	// attOutside is character data, between tags.
	attOutside attNormState = iota
	// attInTag is inside a start or end tag but not inside a value.
	attInTag
	// attInValue is inside a quoted attribute value, the one place the
	// rewrite applies. attQuote says which quote closes it.
	attInValue
	// attInComment, attInPI and attInCData are regions where markup is not
	// recognised and nothing is rewritten. A comment may contain a quote; a
	// PI's pseudo-attributes are not attributes; CDATA is character data.
	attInComment
	attInPI
	attInCData
	// attInDecl is a markup declaration — DOCTYPE, ELEMENT, ATTLIST,
	// ENTITY. Quoted strings there are literals whose whitespace belongs to
	// the DTD, not attribute values, so they are left alone. An internal
	// subset is scanned through, since it contains further declarations
	// rather than tags.
	attInDecl
	// attInDeclLiteral is a quoted literal inside a markup declaration.
	attInDeclLiteral
)

// attNormReader rewrites literal TAB, LF and CR inside attribute values to a
// single space as it reads. See the file comment for why it operates here.
type attNormReader struct {
	src   io.Reader
	state attNormState
	quote byte
	// buf holds input read but not yet emitted. A read boundary may fall in
	// the middle of a delimiter — "<!--" and "<![CDATA[" are up to nine
	// bytes — and the scan must not depend on where it fell.
	buf []byte
	// declDepth counts "[" in a markup declaration, so that "]" closing an
	// internal subset is told apart from one inside it.
	declDepth int
	// run counts the trailing "-", "]" or "?" seen inside an unscanned
	// region, so its closing delimiter is recognised on its last byte
	// without needing lookahead.
	run int
	err error
}

// newAttNormReader wraps r so that attribute values are normalized.
func newAttNormReader(r io.Reader) *attNormReader {
	return &attNormReader{src: r}
}

// maxAttNormPending is the longest delimiter the scanner must hold: len("<![CDATA[").
const maxAttNormPending = 9

func (a *attNormReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		// Held-back bytes are scanned first. They are only held while more
		// input might extend a delimiter, so once EOF is known — or once
		// enough follows them — they can be resolved like any other.
		if n := a.drain(p); n > 0 {
			return n, nil
		}
		if a.err != nil {
			return 0, a.err
		}
		// Read into a scratch buffer no larger than the caller's, so one
		// Read of the wrapped reader yields at most one Read out of this
		// one: every byte maps to exactly one byte.
		size := len(p)
		if size > 4096 {
			size = 4096
		}
		buf := make([]byte, size)
		n, err := a.src.Read(buf)
		if err != nil {
			a.err = err
		}
		if n > 0 {
			a.buf = append(a.buf, buf[:n]...)
		}
		if n == 0 && a.err != nil && len(a.buf) == 0 {
			return 0, a.err
		}
	}
}

// drain scans buffered input into p, stopping at a delimiter that might be
// continued by bytes not yet read. It returns how many bytes it wrote.
func (a *attNormReader) drain(p []byte) int {
	out := 0
	i := 0
	for i < len(a.buf) && out < len(p) {
		// A "<" too close to the end of the buffer might begin "<!--" or
		// "<![CDATA[" once more input arrives. Guessing would put the
		// scanner in the wrong state for the rest of the document, so it
		// waits — unless EOF says no more is coming.
		if a.err == nil && a.buf[i] == '<' &&
			len(a.buf)-i < maxAttNormPending && a.wantsLookahead(a.buf[i:]) {
			break
		}
		consumed, emit := a.step(a.buf, i)
		p[out] = emit
		out++
		i += consumed
	}
	a.buf = a.buf[i:]
	if len(a.buf) == 0 {
		a.buf = a.buf[:0]
	}
	return out
}

// wantsLookahead reports whether a "<" beginning s might start a delimiter
// longer than what is available, so that resolving it needs more bytes.
func (a *attNormReader) wantsLookahead(s []byte) bool {
	if a.state != attOutside && a.state != attInTag && a.state != attInValue {
		return false
	}
	for _, d := range []string{"<!--", "<![CDATA[", "<!"} {
		if len(s) < len(d) && hasPrefixBytes([]byte(d), s) {
			return true
		}
	}
	return false
}

func hasPrefixBytes(s, prefix []byte) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i := range prefix {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

// step advances the scanner one byte and returns how many input bytes it
// consumed and the single byte to emit. It always consumes and emits exactly
// one byte, so offsets are preserved; the multi-byte delimiters are recognised
// by lookahead and change only the state.
func (a *attNormReader) step(in []byte, i int) (int, byte) {
	c := in[i]
	rest := in[i:]
	switch a.state {
	case attOutside:
		if c == '<' {
			switch {
			case hasPrefixBytes(rest, []byte("<!--")):
				// -3 rather than 0: the "!--" that follows is consumed in
				// the comment state and its two dashes would otherwise count
				// towards a closing "-->", so "<!--->" would close on its
				// own opening.
				a.state, a.run = attInComment, -3
			case hasPrefixBytes(rest, []byte("<![CDATA[")):
				// The "![CDATA[" that follows is consumed in the CDATA state
				// and its two "[" do not count, but its bracket run must not
				// leave a stale count behind either.
				a.state, a.run = attInCData, -8
			case hasPrefixBytes(rest, []byte("<?")):
				// "?" is the second byte of "<?", consumed in the PI state;
				// it must not be read as the "?" of a closing "?>", or
				// "<?>" would close a PI that never opened one.
				a.state, a.run = attInPI, -1
			case hasPrefixBytes(rest, []byte("<!")):
				a.state, a.declDepth = attInDecl, 0
			default:
				a.state = attInTag
			}
		}

	case attInTag:
		switch c {
		case '"', '\'':
			a.state, a.quote = attInValue, c
		case '>':
			a.state = attOutside
		}

	case attInValue:
		if c == a.quote {
			a.state = attInTag
			break
		}
		// The rewrite itself, and the only place in this file that changes a
		// byte. A carriage return is handled here rather than left to the
		// decoder's line-ending normalization: section 3.3.3 normalizes every
		// literal whitespace character in a value to a space, and CR-LF in a
		// value must become TWO spaces, not one.
		if c == '\t' || c == '\n' || c == '\r' {
			return 1, ' '
		}

	// The three unscanned regions close on a multi-byte delimiter, and the
	// state must change on its LAST byte, not its first. Matching the whole
	// delimiter forward would need lookahead the caller's read may not have
	// supplied — with a reader handing over one byte at a time, "rest" is
	// never long enough and the region never closes. So the closers are
	// recognised backwards, from the ">" that ends all three, using the run
	// of "-" or "]" this scanner has already passed.
	case attInComment:
		if a.run < 0 {
			a.run++
		} else if c == '-' {
			a.run++
		} else if c == '>' && a.run >= 2 {
			a.state, a.run = attOutside, 0
		} else {
			a.run = 0
		}

	case attInPI:
		if a.run < 0 {
			a.run++
		} else if c == '>' && a.run >= 1 {
			a.state, a.run = attOutside, 0
		} else if c == '?' {
			a.run = 1
		} else {
			a.run = 0
		}

	case attInCData:
		if a.run < 0 {
			a.run++
		} else if c == ']' {
			a.run++
		} else if c == '>' && a.run >= 2 {
			a.state, a.run = attOutside, 0
		} else {
			a.run = 0
		}

	case attInDecl:
		switch c {
		case '[':
			a.declDepth++
		case ']':
			if a.declDepth > 0 {
				a.declDepth--
			}
		case '"', '\'':
			// A literal in a declaration is skipped wholesale rather than
			// entered as a value: a system identifier or a default value may
			// contain ">" and "[", and treating those as structure ends the
			// declaration in the wrong place.
			a.state, a.quote = attInDeclLiteral, c
		case '>':
			if a.declDepth == 0 {
				a.state = attOutside
			}
		}

	case attInDeclLiteral:
		if c == a.quote {
			a.state = attInDecl
		}
	}
	return 1, c
}
