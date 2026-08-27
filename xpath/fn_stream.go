package xpath

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// registerStreamFuncs adds fn:stream-available, XSLT 3.0 section 18.1.3.
//
// It exists here rather than in the xslt package because it is an ordinary
// function of a URI: nothing about it needs a transform, and the XSLT-only
// library is for the functions that do.
//
// Streaming itself is not implemented, and the function does not require it.
// Section 18.1.3 asks only whether a document is *available* for streamed
// processing -- whether the URI resolves, and whether what comes back is
// well formed "at least to the extent that some initial sequence of octets
// can be decoded into characters and matched against the production
// prolog (EmptyElemTag | STag)". A processor that cannot stream still has to
// answer that question honestly, and answering it wrongly would be worse
// than answering it: a stylesheet uses the result to choose between a
// streamed path and a fallback, and a false negative sends it down the
// fallback for a document that is perfectly good.
func registerStreamFuncs(l *Library) {
	l.registerFnSince(XPath30, "stream-available", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The empty sequence is false rather than an error: asking whether
		// nothing is streamable is a question with an answer.
		if len(args) == 0 || len(args[0]) == 0 {
			return boolSeq(false), nil
		}
		// A type error in the argument is still an error, on the same
		// reasoning as fn:doc-available: the function answers whether the
		// document is available, not whether the call was well formed.
		if _, err := argStringRequired(args, 0); err != nil {
			return nil, err
		}
		// Only as far as the first start tag, which is what the spec asks and
		// is not the same as parsing the document. The suite's unfinished.xml
		// is a 95KB schema with its end tag deliberately removed: it is
		// available for streamed processing -- a streaming processor would
		// happily consume the prefix -- and reading it whole to answer the
		// question would both say false and defeat the purpose, since the
		// caller is asking precisely so it can avoid holding the document in
		// memory.
		//
		// The answer is allowed to be optimistic: section 18.1.3 says
		// explicitly that "there is no guarantee that because
		// stream-available returns true, xsl:stream will necessarily
		// succeed".
		// A document that parses whole is certainly available: it reached its
		// start tag on the way. That path is tried first because it needs no
		// text resolver, which is nil by default.
		if seq, err := fnDoc(ctx, args); err == nil && len(seq) > 0 {
			return boolSeq(true), nil
		}
		// A document that does not parse whole may still be streamable --
		// the suite's unfinished.xml is a 95KB schema with its end tag
		// deliberately removed -- so the prefix is examined directly. This
		// needs the text resolver, and without one the answer is the
		// conservative false rather than an error: refusing to read the
		// resource is not the same as knowing it is unavailable, but a
		// caller who has disabled reads is better served by the fallback
		// path than by an exception.
		text, err := unparsedText(ctx, args)
		if err != nil || text == "" {
			return boolSeq(false), nil
		}
		return boolSeq(reachesStartTag(text)), nil
	})
}

// reachesStartTag reports whether s matches "prolog (EmptyElemTag | STag)" --
// whether a document begins well enough to start streaming it.
//
// Only the shape is checked, not the whole grammar: the question is whether
// an initial sequence of octets decodes and gets as far as the document
// element, so anything past that first tag is irrelevant by construction.
func reachesStartTag(s string) bool {
	for i := 0; i < len(s); {
		switch {
		case s[i] == '<' && strings.HasPrefix(s[i:], "<?"):
			// An XML or processing-instruction declaration.
			end := strings.Index(s[i:], "?>")
			if end < 0 {
				return false
			}
			i += end + 2
		case strings.HasPrefix(s[i:], "<!--"):
			end := strings.Index(s[i:], "-->")
			if end < 0 {
				return false
			}
			i += end + 3
		case strings.HasPrefix(s[i:], "<!DOCTYPE"):
			// The document type declaration may carry an internal subset,
			// whose "]" must be found before the closing ">" -- a ">" inside
			// the subset does not end it. An unterminated subset never
			// reaches a start tag, which is the suite's dtd-only.xml.
			j := i + len("<!DOCTYPE")
			depth := 0
			for ; j < len(s); j++ {
				switch s[j] {
				case '[':
					depth++
				case ']':
					depth--
				case '>':
					if depth <= 0 {
						goto doctypeDone
					}
				}
			}
			return false
		doctypeDone:
			i = j + 1
		case s[i] == '<':
			// The document element. Anything that is not a declaration, a
			// comment or a PI is it.
			return i+1 < len(s) && (isNameStartByte(s[i+1]))
		case s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n':
			i++
		default:
			// Character data before the document element is not well formed.
			return false
		}
	}
	return false
}

// isNameStartByte reports whether b can begin an XML element name.
//
// A byte test rather than a rune one: any multi-byte UTF-8 sequence has its
// high bit set, and every such character that may not start a name is
// excluded by the XML grammar for reasons this check does not need to
// reproduce -- getting as far as a plausible tag is the whole question.
func isNameStartByte(b byte) bool {
	return b == '_' || b == ':' || b >= 0x80 ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
