package xdm

import "io"

// End-of-line handling, XML 1.0 section 2.11.
//
// "the XML processor MUST behave as if it normalized all line breaks in
// external parsed entities (including the document entity) on input, before
// parsing, by translating both the two-character sequence #xD #xA and any #xD
// that is not followed by #xA to a single #xA."
//
// So it is done here, on input, before parsing, rather than left to the
// tokeniser. encoding/xml folds line ends only in character data and attribute
// values — a comment or processing instruction kept its carriage returns, so
// "<!--a\r\nb-->" had the value "a\r\nb" — and in an attribute value it folds
// them too late: attNormReader, which must see a literal line end to turn it
// into a space, would have seen CR-LF as two characters and written two
// spaces where §3.3.3 applied to the normalized text gives one.
//
// Unlike attNormReader this changes lengths. That is safe because it sits
// upstream of everything that records an offset: the retained source,
// position tracking and the entity base spans all see the normalized text,
// and only characters at line ends are touched, so line numbers are those of
// the original.
//
// One sequence is passed through: a CR followed by NEL (U+0085). XML 1.1
// section 2.11 makes that pair a single line end, and whether the document is
// 1.1 is known only to the tokeniser, which reads the declaration and folds
// the pair itself. Under 1.0 it folds the CR alone and NEL stays a character,
// which is the 1.0 reading.

// lineEndReader translates CR-LF and a lone CR to LF as it reads.
type lineEndReader struct {
	src     io.Reader
	scratch []byte
	out     []byte // normalized, not yet returned
	carry   []byte // a trailing CR whose successor has not been read yet
	err     error
}

func newLineEndReader(r io.Reader) *lineEndReader {
	return &lineEndReader{src: r, scratch: make([]byte, 0, 4096)}
}

func (l *lineEndReader) Read(p []byte) (int, error) {
	for len(l.out) == 0 {
		if l.err != nil {
			return 0, l.err
		}
		// The carry is copied to the front of the scratch buffer it already
		// sits at the end of; copy handles the overlap.
		in := append(l.scratch[:0], l.carry...)
		n, err := l.src.Read(in[len(in):cap(in)])
		in = in[:len(in)+n]
		l.err = err
		l.out, l.carry = foldLineEnds(in, err != nil)
	}
	n := copy(p, l.out)
	l.out = l.out[n:]
	return n, nil
}

// foldLineEnds normalizes in place and returns the normalized prefix and the
// unprocessed tail. The tail is a CR (or CR plus the first byte of a NEL)
// whose meaning depends on bytes not yet read; final says none will come.
func foldLineEnds(in []byte, final bool) (out, carry []byte) {
	w := 0
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c != '\r' {
			in[w] = c
			w++
			continue
		}
		switch {
		case i+1 < len(in) && in[i+1] == '\n':
			in[w] = '\n'
			i++ // the LF is the same line end
		case i+1 < len(in) && in[i+1] == 0xC2 && i+2 >= len(in) && !final:
			return in[:w], in[i:] // CR, 0xC2, and the next byte unknown
		case i+2 < len(in) && in[i+1] == 0xC2 && in[i+2] == 0x85:
			in[w] = '\r' // CR NEL: the tokeniser's to fold, by version
		case i+1 >= len(in) && !final:
			return in[:w], in[i:]
		default:
			in[w] = '\n'
		}
		w++
	}
	return in[:w], nil
}
