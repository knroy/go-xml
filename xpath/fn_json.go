package xpath

import (
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/knroy/go-xml/xdm"
)

// This file implements the four XPath 3.1 JSON functions — fn:parse-json,
// fn:json-doc, fn:json-to-xml and fn:xml-to-json — together with the scanner
// they share.
//
// The scanner is written here rather than delegated to encoding/json because
// the specification asks for things a general-purpose JSON decoder does not
// expose. Duplicate keys must be rejected, kept first or kept last on the
// caller's instruction; the *lexical* form of a number must survive into
// fn:json-to-xml's output, so "23E0" comes back spelled that way rather than
// as 23; a string may be delivered still escaped; a character that no XML
// document may hold has to be routed through a caller-supplied fallback; and
// every malformation has to be distinguishable, since the suite asserts
// FOJS0001 against FOJS0003 against FOJS0007 by code. A decoder that reports
// "invalid character" as one opaque error cannot answer any of that.

// jsonHandler receives the events the scanner produces.
//
// The two consumers build very different things — fn:parse-json builds maps
// and arrays, fn:json-to-xml builds an element tree — but they agree on every
// question of *what is legal*, which is the part with all the detail. Driving
// both from one scanner is what keeps the two functions from disagreeing about
// whether "01" is a number.
type jsonHandler interface {
	// startMap and endMap bracket a JSON object. duplicateKey is called
	// instead of a key when the object already holds that key, so that the
	// handler can apply the duplicates option.
	startMap() error
	key(k string) error
	endMap() error

	startArray() error
	endArray() error

	// The scalars. number is given the lexeme as written, not a parsed value,
	// because fn:json-to-xml must reproduce it verbatim.
	str(s string) error
	number(lexeme string) error
	boolean(v bool) error
	null() error
}

// jsonOptions is the decoded $options map shared by parse-json, json-doc and
// json-to-xml.
type jsonOptions struct {
	liberal    bool
	escape     bool
	escapeSet  bool
	duplicates string
	fallback   *xdm.FunctionItem
	// validate is accepted and ignored: this processor is not schema-aware,
	// and the specification makes validation optional. The flag is still read
	// so that its interaction with duplicates="retain" — an error even for a
	// processor that would not validate — is reported.
	validate bool
}

// errFOJS0001 is the malformed-input error every syntactic failure raises.
func errFOJS0001(format string, args ...any) error {
	return xdm.Errorf("FOJS0001", format, args...)
}

// --- Options ---------------------------------------------------------------

// jsonOptionsFrom decodes the options map.
//
// Two error codes are in play and the difference matters to the suite: a value
// of the wrong *type* for a known option is XPTY0004, because the option map's
// declared entry types are part of the function signature, while a value of
// the right type that names something the function does not offer — such as
// duplicates="retain" outside fn:json-to-xml — is FOJS0005. An option the
// function does not recognise at all is ignored rather than either.
func jsonOptionsFrom(ctx *Context, args []xdm.Sequence, i int, forXML bool) (jsonOptions, error) {
	// The defaults differ between the two families. fn:parse-json unescapes
	// by default and keeps the first of a set of duplicates; fn:json-to-xml
	// retains duplicates, since the XML representation can hold two entries
	// with the same key where a map cannot.
	opts := jsonOptions{escape: false, duplicates: "use-first"}
	if forXML {
		opts.duplicates = "retain"
	}
	// Whether "duplicates" was written matters as well as what it says.
	// fn:json-to-xml defaults it to "retain" -- except with validate=true,
	// where the default is "reject", since a tree that retains duplicates
	// cannot be schema-valid. Defaulting it to "retain" unconditionally made
	// {'validate': true()} on its own a contradiction and raised FOJS0005
	// for the whole of the json-to-xml-typed set, which supplies exactly
	// that option and nothing else.
	duplicatesGiven := false
	if i >= len(args) {
		return opts, nil
	}
	m, err := singleMapArg(args, i)
	if err != nil {
		return opts, err
	}
	if m == nil {
		return opts, nil
	}
	err = m.Entries(func(k *xdm.Atomic, v xdm.Sequence) error {
		if k == nil || k.Type != xdm.TypeString {
			// A non-string key names no option, and the specification says
			// unknown options are ignored.
			return nil
		}
		switch k.String() {
		case "liberal":
			b, err := jsonOptionBool(v, "liberal")
			if err != nil {
				return err
			}
			opts.liberal = b
		case "escape":
			b, err := jsonOptionBool(v, "escape")
			if err != nil {
				return err
			}
			opts.escape, opts.escapeSet = b, true
		case "validate":
			b, err := jsonOptionBool(v, "validate")
			if err != nil {
				return err
			}
			opts.validate = b
		case "duplicates":
			s, err := jsonOptionString(v, "duplicates")
			if err != nil {
				return err
			}
			opts.duplicates = s
			duplicatesGiven = true
		case "fallback":
			fn, err := singleFunctionItem(v)
			if err != nil {
				return xdm.ErrType(
					"the fallback option must be a function of one argument")
			}
			if fn.Arity != 1 {
				return xdm.ErrType(
					"the fallback option must be a function of arity 1, got arity %d", fn.Arity)
			}
			opts.fallback = fn
		}
		return nil
	})
	if err != nil {
		return opts, err
	}

	if forXML && opts.validate && !duplicatesGiven {
		opts.duplicates = "reject"
	}

	// "retain" is the fn:json-to-xml default and is meaningless elsewhere:
	// a map cannot hold the same key twice, so there is nothing to retain.
	switch opts.duplicates {
	case "use-first", "reject":
	case "use-last":
		// fn:json-to-xml offers no use-last: the XML representation keeps
		// duplicates by default, so the ways of collapsing them that a map
		// needs are not all defined for it. json-to-xml-error-040 asserts the
		// refusal rather than a silent reinterpretation.
		if forXML {
			return opts, xdm.Errorf("FOJS0005",
				"duplicates='use-last' is not available for fn:json-to-xml")
		}
	case "retain":
		if !forXML {
			return opts, xdm.Errorf("FOJS0005",
				"duplicates='retain' is not available for this function")
		}
		// Retaining duplicates produces a tree that cannot be schema-valid,
		// so asking for both at once is a contradiction rather than a choice.
		if opts.validate {
			return opts, xdm.Errorf("FOJS0005",
				"duplicates='retain' cannot be combined with validate=true")
		}
	default:
		return opts, xdm.Errorf("FOJS0005",
			"duplicates=%q is not one of reject, use-first, use-last or retain", opts.duplicates)
	}
	// A fallback function exists to replace a character the *unescaped* text
	// cannot represent. With escape=true nothing is unescaped, so there is
	// never anything for it to be handed — asking for both is a mistake the
	// specification names rather than silently ignores.
	if opts.fallback != nil && opts.escape {
		return opts, xdm.Errorf("FOJS0005",
			"the fallback option cannot be combined with escape=true")
	}
	return opts, nil
}

// jsonOptionBool reads an option declared xs:boolean.
//
// The empty sequence is a type error rather than "unset": the option map's
// entry type is xs:boolean, not xs:boolean?, so supplying nothing where a
// boolean is declared is XPTY0004 the same as supplying a string.
func jsonOptionBool(v xdm.Sequence, name string) (bool, error) {
	atoms, err := xdm.AtomizeChecked(v)
	if err != nil {
		return false, err
	}
	if len(atoms) != 1 {
		return false, xdm.ErrType(
			"the %s option must be a single xs:boolean, got %d items", name, len(atoms))
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok || a.Type != xdm.TypeBoolean {
		return false, xdm.ErrType("the %s option must be xs:boolean", name)
	}
	return a.Bool(), nil
}

// jsonOptionString reads an option declared xs:string.
func jsonOptionString(v xdm.Sequence, name string) (string, error) {
	atoms, err := xdm.AtomizeChecked(v)
	if err != nil {
		return "", err
	}
	if len(atoms) != 1 {
		return "", xdm.ErrType(
			"the %s option must be a single xs:string, got %d items", name, len(atoms))
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok || (a.Type != xdm.TypeString && a.Type != xdm.TypeUntypedAtomic) {
		return "", xdm.ErrType("the %s option must be xs:string", name)
	}
	return a.String(), nil
}

// singleMapArg extracts the options map, which is declared map(*) and so may
// be absent but not empty.
func singleMapArg(args []xdm.Sequence, i int) (*xdm.MapItem, error) {
	if i >= len(args) || len(args[i]) == 0 {
		return nil, nil
	}
	if len(args[i]) != 1 {
		return nil, xdm.ErrType("the options argument must be a single map")
	}
	m, ok := args[i][0].(*xdm.MapItem)
	if !ok {
		return nil, xdm.ErrType("the options argument must be a map, got %s",
			args[i][0].TypeName())
	}
	return m, nil
}

// --- Scanner ---------------------------------------------------------------

// jsonScanner walks the input text, calling into a handler.
//
// It works over runes decoded from UTF-8 rather than bytes because the escape
// rules are stated in codepoints and because an unpaired surrogate — which the
// \uXXXX syntax can produce but UTF-8 cannot encode — has to be carried
// through string processing intact.
type jsonScanner struct {
	in   []rune
	pos  int
	opts jsonOptions
	h    jsonHandler
	// unescape is a closure so that the two callers can differ on what to do
	// with a character the result cannot hold: parse-json substitutes U+FFFD
	// or calls the fallback, json-to-xml does the same but must also decide
	// whether to mark the element escaped.
	fallbackFn func(string) (string, error)
}

// scanJSON parses text and drives h, returning the first error.
func scanJSON(text string, opts jsonOptions, h jsonHandler,
	fallbackFn func(string) (string, error)) error {
	s := &jsonScanner{in: []rune(text), opts: opts, h: h, fallbackFn: fallbackFn}
	s.skipSpace()
	if err := s.value(); err != nil {
		return err
	}
	s.skipSpace()
	if s.pos != len(s.in) {
		return errFOJS0001("unexpected content after the JSON value at position %d", s.pos)
	}
	return nil
}

// skipSpace consumes insignificant whitespace.
//
// JSON allows exactly four characters here. A form feed is *not* among them,
// which json-to-xml-error-027 exists to check: it is whitespace in XML but not
// in JSON, and accepting it would let a document through that a conforming
// processor rejects.
func (s *jsonScanner) skipSpace() {
	for s.pos < len(s.in) {
		switch s.in[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

func (s *jsonScanner) peek() (rune, bool) {
	if s.pos >= len(s.in) {
		return 0, false
	}
	return s.in[s.pos], true
}

// value parses one JSON value.
func (s *jsonScanner) value() error {
	c, ok := s.peek()
	if !ok {
		return errFOJS0001("unexpected end of input where a value was expected")
	}
	switch {
	case c == '{':
		return s.object()
	case c == '[':
		return s.array()
	case c == '"':
		lit, err := s.stringLiteral()
		if err != nil {
			return err
		}
		return s.h.str(lit)
	case c == '-' || (c >= '0' && c <= '9'):
		return s.numberValue()
	case c == 't':
		if err := s.keyword("true"); err != nil {
			return err
		}
		return s.h.boolean(true)
	case c == 'f':
		if err := s.keyword("false"); err != nil {
			return err
		}
		return s.h.boolean(false)
	case c == 'n':
		if err := s.keyword("null"); err != nil {
			return err
		}
		return s.h.null()
	}
	return errFOJS0001("unexpected character %q at position %d", string(c), s.pos)
}

// keyword matches one of the three bare words exactly.
//
// It must also check what *follows*: "falsehood" begins with "false", and
// accepting the prefix would silently truncate rather than report the error
// the suite expects.
func (s *jsonScanner) keyword(word string) error {
	w := []rune(word)
	if s.pos+len(w) > len(s.in) || string(s.in[s.pos:s.pos+len(w)]) != word {
		return errFOJS0001("expected %q at position %d", word, s.pos)
	}
	s.pos += len(w)
	if c, ok := s.peek(); ok && (isJSONNameChar(c)) {
		return errFOJS0001("unexpected character after %q at position %d", word, s.pos)
	}
	return nil
}

func isJSONNameChar(c rune) bool {
	return c == '_' || c == '-' || c == '+' || c == '.' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// object parses "{...}".
func (s *jsonScanner) object() error {
	s.pos++ // '{'
	if err := s.h.startMap(); err != nil {
		return err
	}
	s.skipSpace()
	if c, ok := s.peek(); ok && c == '}' {
		s.pos++
		return s.h.endMap()
	}
	for {
		s.skipSpace()
		c, ok := s.peek()
		if !ok {
			return errFOJS0001("unexpected end of input inside an object")
		}
		if c != '"' {
			return errFOJS0001("an object key must be a string, found %q at position %d",
				string(c), s.pos)
		}
		k, err := s.stringLiteral()
		if err != nil {
			return err
		}
		if err := s.h.key(k); err != nil {
			return err
		}
		s.skipSpace()
		if c, ok := s.peek(); !ok || c != ':' {
			return errFOJS0001("expected ':' after an object key at position %d", s.pos)
		}
		s.pos++
		s.skipSpace()
		if err := s.value(); err != nil {
			return err
		}
		s.skipSpace()
		c, ok = s.peek()
		if !ok {
			return errFOJS0001("unexpected end of input inside an object")
		}
		if c == ',' {
			s.pos++
			continue
		}
		if c == '}' {
			s.pos++
			return s.h.endMap()
		}
		return errFOJS0001("expected ',' or '}' at position %d, found %q", s.pos, string(c))
	}
}

// array parses "[...]".
func (s *jsonScanner) array() error {
	s.pos++ // '['
	if err := s.h.startArray(); err != nil {
		return err
	}
	s.skipSpace()
	if c, ok := s.peek(); ok && c == ']' {
		s.pos++
		return s.h.endArray()
	}
	for {
		s.skipSpace()
		if err := s.value(); err != nil {
			return err
		}
		s.skipSpace()
		c, ok := s.peek()
		if !ok {
			return errFOJS0001("unexpected end of input inside an array")
		}
		if c == ',' {
			s.pos++
			continue
		}
		if c == ']' {
			s.pos++
			return s.h.endArray()
		}
		return errFOJS0001("expected ',' or ']' at position %d, found %q", s.pos, string(c))
	}
}

// numberValue scans a number and hands the handler its lexeme.
//
// The grammar is JSON's, which is narrower than XPath's xs:double: no leading
// plus, no leading zero before another digit, no bare leading or trailing
// decimal point. Each of those has a test asserting FOJS0001, so the scan is
// written as the grammar rather than as "take everything numeric-looking and
// let strconv decide" — strconv accepts "+23" and ".3" and would let them
// through.
func (s *jsonScanner) numberValue() error {
	start := s.pos
	if c, ok := s.peek(); ok && c == '-' {
		s.pos++
	}
	// The integer part is either a single "0" or a nonzero digit followed by
	// any number of digits. "01" and "-00" are therefore rejected.
	c, ok := s.peek()
	if !ok || c < '0' || c > '9' {
		return errFOJS0001("expected a digit at position %d", s.pos)
	}
	if c == '0' {
		s.pos++
	} else {
		for {
			c, ok := s.peek()
			if !ok || c < '0' || c > '9' {
				break
			}
			s.pos++
		}
	}
	if c, ok := s.peek(); ok && c == '.' {
		s.pos++
		// At least one digit must follow: "1." is not a JSON number.
		if c, ok := s.peek(); !ok || c < '0' || c > '9' {
			return errFOJS0001("expected a digit after '.' at position %d", s.pos)
		}
		for {
			c, ok := s.peek()
			if !ok || c < '0' || c > '9' {
				break
			}
			s.pos++
		}
	}
	if c, ok := s.peek(); ok && (c == 'e' || c == 'E') {
		s.pos++
		if c, ok := s.peek(); ok && (c == '+' || c == '-') {
			s.pos++
		}
		if c, ok := s.peek(); !ok || c < '0' || c > '9' {
			return errFOJS0001("expected a digit in the exponent at position %d", s.pos)
		}
		for {
			c, ok := s.peek()
			if !ok || c < '0' || c > '9' {
				break
			}
			s.pos++
		}
	}
	// Anything still name-like immediately after is a malformed number rather
	// than the start of the next token: "1.234f0" and "0x1F" both land here.
	if c, ok := s.peek(); ok && isJSONNameChar(c) {
		return errFOJS0001("unexpected character %q after a number at position %d",
			string(c), s.pos)
	}
	return s.h.number(string(s.in[start:s.pos]))
}

// stringLiteral scans a quoted string and returns it in the form the options
// call for: still escaped when escape=true, unescaped otherwise.
//
// Both forms are produced from the same scan because the validity rules are
// the same either way — "\q" is malformed whether or not anyone intends to
// interpret it — and because escape=true is not "copy the source verbatim":
// json-to-xml-049 shows that a literal space stays a space while a "\r" stays
// "\r", so the escaped form is re-derived rather than sliced out of the input.
func (s *jsonScanner) stringLiteral() (string, error) {
	s.pos++ // opening quote
	// Codepoints are accumulated as runes so that an unpaired surrogate from
	// \uD834 survives: Go's string type cannot hold one, and appending it to
	// a strings.Builder would silently become U+FFFD before the fallback ever
	// saw it.
	var out []rune
	for {
		c, ok := s.peek()
		if !ok {
			return "", errFOJS0001("unterminated string literal")
		}
		if c == '"' {
			s.pos++
			break
		}
		if c == '\\' {
			r, err := s.escapeSequence()
			if err != nil {
				return "", err
			}
			out = append(out, r...)
			continue
		}
		// A raw control character is not allowed in a JSON string; it must be
		// written as an escape. parse-json-840 checks that a literal newline
		// inside a key is rejected rather than accepted as data.
		if c < 0x20 {
			return "", errFOJS0001(
				"an unescaped control character U+%04X is not allowed in a string", c)
		}
		s.pos++
		out = append(out, c)
	}
	return s.finishString(out)
}

// escapedRune marks a codepoint that arrived as a \u escape.
//
// The distinction matters only under escape=true, where a character that came
// in escaped stays escaped and one that arrived literally does not: a literal
// space is emitted as a space while " " is too, but a literal U+0001 —
// which cannot appear in XML at all — must come out as "". Rather than
// carry a parallel slice of flags, characters that must survive as escapes are
// encoded in a private area far above Unicode and decoded in finishString.
const escapedBias = 0x200000

// escapeSequence consumes a backslash escape and returns its codepoints.
func (s *jsonScanner) escapeSequence() ([]rune, error) {
	s.pos++ // backslash
	c, ok := s.peek()
	if !ok {
		return nil, errFOJS0001("a string may not end with a backslash")
	}
	switch c {
	case '"', '\\', '/':
		s.pos++
		// These three keep their identity as escapes so that escape=true
		// reproduces "\\" as "\\" rather than as a bare backslash, and so
		// that "\/" stays "\/" — xml-to-json-080 pins that down.
		return []rune{c + escapedBias}, nil
	case 'b':
		s.pos++
		return []rune{0x08 + escapedBias}, nil
	case 'f':
		s.pos++
		return []rune{0x0C + escapedBias}, nil
	case 'n':
		s.pos++
		return []rune{0x0A + escapedBias}, nil
	case 'r':
		s.pos++
		return []rune{0x0D + escapedBias}, nil
	case 't':
		s.pos++
		return []rune{0x09 + escapedBias}, nil
	case 'u':
		s.pos++
		v, err := s.hex4()
		if err != nil {
			return nil, err
		}
		// A high surrogate is joined with the low one that follows, when one
		// does. An unpaired surrogate is not an error here — the escape is
		// well-formed JSON — but it cannot appear in the result either, so it
		// is passed on for finishString to route through the fallback.
		if utf16.IsSurrogate(rune(v)) && v >= 0xD800 && v <= 0xDBFF {
			if s.pos+1 < len(s.in) && s.in[s.pos] == '\\' && s.in[s.pos+1] == 'u' {
				save := s.pos
				s.pos += 2
				lo, err := s.hex4()
				if err != nil {
					return nil, err
				}
				if lo >= 0xDC00 && lo <= 0xDFFF {
					return []rune{utf16.DecodeRune(rune(v), rune(lo)) + escapedBias}, nil
				}
				// Not a low surrogate after all: rewind so the second escape
				// is scanned on its own terms.
				s.pos = save
			}
		}
		return []rune{rune(v) + escapedBias}, nil
	}
	return nil, errFOJS0001("unrecognised escape sequence \\%s at position %d",
		string(c), s.pos)
}

// hex4 reads exactly four hexadecimal digits.
func (s *jsonScanner) hex4() (int, error) {
	if s.pos+4 > len(s.in) {
		return 0, errFOJS0001("a \\u escape needs four hexadecimal digits")
	}
	v := 0
	for i := 0; i < 4; i++ {
		c := s.in[s.pos+i]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return 0, errFOJS0001("%q is not a hexadecimal digit at position %d",
				string(c), s.pos+i)
		}
		v = v*16 + d
	}
	s.pos += 4
	return v, nil
}

// finishString turns the scanned codepoints into the delivered string.
func (s *jsonScanner) finishString(rs []rune) (string, error) {
	if s.opts.escape {
		return escapeJSONRunes(rs)
	}
	var b strings.Builder
	for _, r := range rs {
		c := r
		if c >= escapedBias {
			c -= escapedBias
		}
		if isXMLChar(int64(c)) {
			b.WriteRune(c)
			continue
		}
		// The character cannot appear in an XML document, so it cannot be
		// delivered as itself. Without a fallback the substitute is U+FFFD,
		// which is what the specification names; with one, the fallback is
		// handed the escape *as text* so that it can decide — the tests pass
		// lower-case#1 and expect "￿" back, which is only possible if it
		// receives the six-character escape rather than the character.
		if s.fallbackFn != nil {
			rep, err := s.fallbackFn(escapeOneRune(c))
			if err != nil {
				return "", err
			}
			b.WriteString(rep)
			continue
		}
		b.WriteRune('�')
	}
	return b.String(), nil
}

// escapeOneRune renders a codepoint as the JSON escape a fallback function is
// shown. Surrogate pairs are spelled as two escapes, which is what
// json-to-xml-039 relies on when it takes substring(3) of "\uDEAD".
func escapeOneRune(c rune) string {
	if c > 0xFFFF {
		hi, lo := utf16.EncodeRune(c)
		return escapeOneRune(hi) + escapeOneRune(lo)
	}
	return "\\u" + strings.ToUpper(pad4(strconv.FormatInt(int64(c), 16)))
}

func pad4(s string) string {
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// escapeJSONRunes produces the escape=true form.
//
// Only the characters that *must* be escaped are: a JSON string may hold a
// literal "%" whether or not it arrived as "%", and parse-json-106
// asserts exactly that. What must be escaped is the quote, the backslash, the
// characters with a short escape, and everything XML cannot carry.
func escapeJSONRunes(rs []rune) (string, error) {
	var b strings.Builder
	for _, r := range rs {
		c := r
		if c >= escapedBias {
			c -= escapedBias
		}
		switch c {
		case '"':
			// A quote inside an XML attribute or text node needs no escape,
			// and json-to-xml-049 shows the escaped form dropping it: only the
			// backslash and the characters with no literal spelling keep one.
			b.WriteByte('"')
			continue
		case '\\':
			b.WriteString("\\\\")
			continue
		case 0x08:
			b.WriteString("\\b")
			continue
		case 0x0C:
			b.WriteString("\\f")
			continue
		case 0x0A:
			b.WriteString("\\n")
			continue
		case 0x0D:
			b.WriteString("\\r")
			continue
		case 0x09:
			b.WriteString("\\t")
			continue
		case '/':
			// A solidus needs no escape and does not keep one: parse-json-109
			// rejects '{"/":"x", "\/":"y"}' as a duplicate under escape=true,
			// which only holds if both spellings normalise to the same key.
			b.WriteByte('/')
			continue
		}
		if isXMLChar(int64(c)) {
			b.WriteRune(c)
			continue
		}
		b.WriteString(escapeOneRune(c))
	}
	return b.String(), nil
}

// --- fn:parse-json and fn:json-doc -----------------------------------------

// jsonBuilder is the handler that builds maps and arrays.
type jsonBuilder struct {
	// stack holds the containers being filled. Only the innermost is written
	// to; finishing one pops it and hands the finished item to its parent.
	stack []*jsonFrame
	opts  jsonOptions
	// result is the top-level value, once one has been produced.
	result xdm.Sequence
	done   bool
}

// jsonFrame is one open container.
type jsonFrame struct {
	isMap bool
	// For a map: the entries in insertion order, and an index that answers
	// "have I seen this key" without a scan.
	keys    []string
	values  []xdm.Sequence
	index   map[string]int
	pending string
	// For an array.
	members []xdm.Sequence
}

func (b *jsonBuilder) emit(v xdm.Sequence) error {
	if len(b.stack) == 0 {
		if b.done {
			return errFOJS0001("more than one value at the top level")
		}
		b.result, b.done = v, true
		return nil
	}
	f := b.stack[len(b.stack)-1]
	if f.isMap {
		k := f.pending
		if i, seen := f.index[k]; seen {
			switch b.opts.duplicates {
			case "reject":
				return xdm.Errorf("FOJS0003", "duplicate key %q in a JSON object", k)
			case "use-last":
				f.values[i] = v
			default: // use-first
			}
			return nil
		}
		f.index[k] = len(f.keys)
		f.keys = append(f.keys, k)
		f.values = append(f.values, v)
		return nil
	}
	f.members = append(f.members, v)
	return nil
}

func (b *jsonBuilder) startMap() error {
	b.stack = append(b.stack, &jsonFrame{isMap: true, index: map[string]int{}})
	return nil
}

func (b *jsonBuilder) key(k string) error {
	b.stack[len(b.stack)-1].pending = k
	return nil
}

func (b *jsonBuilder) endMap() error {
	f := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	m := xdm.NewMap()
	for i, k := range f.keys {
		next, err := m.Put(xdm.NewString(k), f.values[i])
		if err != nil {
			return err
		}
		m = next
	}
	return b.emit(xdm.One(m))
}

func (b *jsonBuilder) startArray() error {
	b.stack = append(b.stack, &jsonFrame{})
	return nil
}

func (b *jsonBuilder) endArray() error {
	f := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	return b.emit(xdm.One(xdm.NewArray(f.members...)))
}

func (b *jsonBuilder) str(s string) error { return b.emit(xdm.One(xdm.NewString(s))) }

// number converts the lexeme to xs:double.
//
// The data model has no "JSON number": every one becomes a double, so 1 and
// 1.0 and 1e0 are the same value. The lexeme was carried this far only for
// fn:json-to-xml's benefit.
func (b *jsonBuilder) number(lexeme string) error {
	f, err := strconv.ParseFloat(lexeme, 64)
	if err != nil {
		return errFOJS0001("%q is not a valid JSON number", lexeme)
	}
	return b.emit(xdm.One(xdm.NewDouble(f)))
}

func (b *jsonBuilder) boolean(v bool) error { return b.emit(xdm.One(xdm.NewBoolean(v))) }

// null becomes the empty sequence, not a distinguished null item: the data
// model has no null, and the specification maps it this way.
func (b *jsonBuilder) null() error { return b.emit(xdm.Empty()) }

// parseJSONText is the shared body of fn:parse-json and fn:json-doc.
func parseJSONText(ctx *Context, text string, opts jsonOptions) (xdm.Sequence, error) {
	b := &jsonBuilder{opts: opts}
	fb := jsonFallback(ctx, opts)
	if err := scanJSON(text, opts, b, fb); err != nil {
		return nil, err
	}
	if !b.done {
		return nil, errFOJS0001("the input contains no JSON value")
	}
	return b.result, nil
}

// jsonFallback wraps the caller's fallback function, or returns nil when there
// is none.
func jsonFallback(ctx *Context, opts jsonOptions) func(string) (string, error) {
	if opts.fallback == nil {
		return nil
	}
	return func(esc string) (string, error) {
		out, err := opts.fallback.Invoke(ctx, []xdm.Sequence{xdm.One(xdm.NewString(esc))})
		if err != nil {
			return "", err
		}
		atoms, err := xdm.AtomizeChecked(out)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, it := range atoms {
			b.WriteString(it.(*xdm.Atomic).String())
		}
		return b.String(), nil
	}
}

// --- fn:json-to-xml --------------------------------------------------------

// jsonXMLBuilder is the handler that builds the XML representation.
//
// It differs from jsonBuilder in more than its output: duplicates="retain" is
// the default here, so a map element may legitimately carry two children with
// the same key, and the escaped/escaped-key attributes have to record whether
// a value was delivered in escaped form.
type jsonXMLBuilder struct {
	opts    jsonOptions
	stack   []*xdm.Node
	root    *xdm.Node
	pending string
	// pendingRaw records whether the pending key needed escaping, which
	// decides the escaped-key attribute.
	pendingEscaped bool
	// seen tracks the keys of the open map element, for the duplicates
	// options that are not "retain".
	seen []map[string]int
}

const nsJSON = "http://www.w3.org/2005/xpath-functions"

func jsonElement(local string) *xdm.Node {
	return &xdm.Node{Kind: xdm.KindElement,
		Name: xdm.QName{URI: nsJSON, Local: local}}
}

func setAttr(el *xdm.Node, local, value string) {
	a := &xdm.Node{Kind: xdm.KindAttribute,
		Name: xdm.QName{Local: local}, Value: value, Parent: el}
	el.Attrs = append(el.Attrs, a)
}

// attach places a finished element under the open container, applying the key
// attribute and the duplicates option.
func (b *jsonXMLBuilder) attach(el *xdm.Node) error {
	if len(b.stack) == 0 {
		if b.root != nil {
			return errFOJS0001("more than one value at the top level")
		}
		b.root = el
		return nil
	}
	parent := b.stack[len(b.stack)-1]
	if parent.Name.Local == "map" {
		k := b.pending
		// The key attribute is written before the duplicate check so that
		// use-last can replace the whole element.
		setAttr(el, "key", k)
		if b.pendingEscaped {
			setAttr(el, "escaped-key", "true")
		}
		if b.opts.duplicates != "retain" {
			idx := b.seen[len(b.seen)-1]
			if i, dup := idx[k]; dup {
				switch b.opts.duplicates {
				case "reject":
					return xdm.Errorf("FOJS0003", "duplicate key %q in a JSON object", k)
				case "use-last":
					el.Parent = parent
					parent.Children[i] = el
				default: // use-first
				}
				return nil
			}
			idx[k] = len(parent.Children)
		}
	}
	el.Parent = parent
	parent.Children = append(parent.Children, el)
	return nil
}

func (b *jsonXMLBuilder) startMap() error {
	el := jsonElement("map")
	b.stack = append(b.stack, el)
	b.seen = append(b.seen, map[string]int{})
	// The key of the map *itself* is consumed here, before its children
	// overwrite the pending slot with their own keys.
	return b.deferAttach(el)
}

func (b *jsonXMLBuilder) startArray() error {
	el := jsonElement("array")
	b.stack = append(b.stack, el)
	return b.deferAttach(el)
}

// deferAttach records where a container belongs at the moment it opens.
//
// A container has to be attached on open rather than on close, because the
// pending key belongs to it and would otherwise be overwritten by the keys of
// its own entries. Attaching early is safe: the tree is not read until the
// whole parse succeeds.
func (b *jsonXMLBuilder) deferAttach(el *xdm.Node) error {
	// The element being opened is on the stack already, so the parent is the
	// one below it.
	saved := b.stack
	b.stack = b.stack[:len(b.stack)-1]
	err := b.attach(el)
	b.stack = saved
	return err
}

func (b *jsonXMLBuilder) key(k string) error {
	// json-to-xml delivers keys unescaped unless escape=true, exactly as it
	// does values, and marks the escaped ones.
	b.pending, b.pendingEscaped = k, b.opts.escape && needsEscapeMark(k)
	return nil
}

func (b *jsonXMLBuilder) endMap() error {
	b.stack = b.stack[:len(b.stack)-1]
	b.seen = b.seen[:len(b.seen)-1]
	return nil
}

func (b *jsonXMLBuilder) endArray() error {
	b.stack = b.stack[:len(b.stack)-1]
	return nil
}

func (b *jsonXMLBuilder) str(s string) error {
	el := jsonElement("string")
	if b.opts.escape && needsEscapeMark(s) {
		setAttr(el, "escaped", "true")
	}
	if s != "" {
		el.Children = []*xdm.Node{{Kind: xdm.KindText, Value: s, Parent: el}}
	}
	return b.attach(el)
}

// needsEscapeMark reports whether a string delivered in escaped form actually
// contains an escape.
//
// Marking every string escaped=true would be harmless for round-tripping but
// wrong for comparison: json-to-xml-024 expects the attribute only on the
// entry that carries "\uDA00", and json-to-xml-049 only on the one that
// carries a real escape.
func needsEscapeMark(s string) bool { return strings.Contains(s, "\\") }

func (b *jsonXMLBuilder) number(lexeme string) error {
	el := jsonElement("number")
	el.Children = []*xdm.Node{{Kind: xdm.KindText, Value: lexeme, Parent: el}}
	return b.attach(el)
}

func (b *jsonXMLBuilder) boolean(v bool) error {
	el := jsonElement("boolean")
	s := "false"
	if v {
		s = "true"
	}
	el.Children = []*xdm.Node{{Kind: xdm.KindText, Value: s, Parent: el}}
	return b.attach(el)
}

func (b *jsonXMLBuilder) null() error { return b.attach(jsonElement("null")) }

// jsonToXML builds the document node fn:json-to-xml returns.
func jsonToXML(ctx *Context, text string, opts jsonOptions) (xdm.Sequence, error) {
	b := &jsonXMLBuilder{opts: opts}
	if err := scanJSON(text, opts, b, jsonFallback(ctx, opts)); err != nil {
		return nil, err
	}
	if b.root == nil {
		return nil, errFOJS0001("the input contains no JSON value")
	}
	// The root element declares the namespace it is in. Without the
	// declaration the tree serialises with none, so a comparison against the
	// expected XML sees a differently-named element.
	b.root.Namespaces = []*xdm.Node{{
		Kind: xdm.KindNamespace, Value: nsJSON, Parent: b.root,
	}}
	doc := &xdm.Node{Kind: xdm.KindDocument, BaseURI: ctx.StaticBaseURI}
	b.root.Parent = doc
	doc.Children = []*xdm.Node{b.root}
	setBaseURI(b.root, ctx.StaticBaseURI)
	tree := &xdm.Tree{Root: doc}
	tree.Finalize()
	return xdm.One(doc), nil
}

// setBaseURI propagates the base URI down the constructed tree, since
// fn:base-uri walks to the nearest ancestor that has one and the document node
// is not consulted for an element built this way.
func setBaseURI(n *xdm.Node, base string) {
	n.BaseURI = base
	for _, c := range n.Children {
		setBaseURI(c, base)
	}
}

// --- fn:xml-to-json --------------------------------------------------------

// xmlToJSON serialises the XML representation back to JSON text.
func xmlToJSON(n *xdm.Node, indent bool) (string, error) {
	el := n
	if el.Kind == xdm.KindDocument {
		// A document node stands for its single element child; anything else
		// under it is not the representation of a JSON value.
		el = nil
		for _, c := range n.Children {
			switch c.Kind {
			case xdm.KindElement:
				if el != nil {
					return "", xdm.Errorf("FOJS0006",
						"a document node holding JSON XML must have one element child")
				}
				el = c
			case xdm.KindComment, xdm.KindPI:
			case xdm.KindText:
				if strings.TrimSpace(c.Value) != "" {
					return "", xdm.Errorf("FOJS0006",
						"unexpected text under a document node holding JSON XML")
				}
			}
		}
		if el == nil {
			return "", xdm.Errorf("FOJS0006",
				"a document node holding JSON XML must have one element child")
		}
	}
	if el.Kind != xdm.KindElement {
		return "", xdm.ErrType(
			"fn:xml-to-json expects an element or document node, got %s", el.Kind)
	}
	var b strings.Builder
	if err := writeJSONFrom(&b, el, indent, 0, true); err != nil {
		return "", err
	}
	return b.String(), nil
}

// jsonElementChild is one significant child of a map or array element.
type jsonElementChild struct {
	el  *xdm.Node
	key string
}

// jsonChildren returns the element children, rejecting anything else.
//
// Comments and processing instructions are ignored — xml-to-json-028 puts a
// comment between two entries and still expects two — and so is whitespace,
// but non-whitespace text under a map or array is not the representation of
// anything and is FOJS0006.
func jsonChildren(el *xdm.Node) ([]*xdm.Node, error) {
	var out []*xdm.Node
	for _, c := range el.Children {
		switch c.Kind {
		case xdm.KindElement:
			out = append(out, c)
		case xdm.KindComment, xdm.KindPI:
		case xdm.KindText:
			if strings.TrimSpace(c.Value) != "" {
				return nil, xdm.Errorf("FOJS0006",
					"element %s may not contain text", el.Name.Local)
			}
		}
	}
	return out, nil
}

// checkJSONAttrs validates the attributes on an element of the representation.
//
// Attributes in another namespace are permitted and ignored — xml:base is the
// case xml-to-json-068 covers — but one in the JSON namespace itself is not,
// and neither is a no-namespace attribute the element does not define.
func checkJSONAttrs(el *xdm.Node, allowed ...string) error {
	for _, a := range el.Attrs {
		if a.Name.URI != "" {
			if a.Name.URI == nsJSON {
				return xdm.Errorf("FOJS0006",
					"attribute %s is not allowed in the JSON namespace", a.Name.Local)
			}
			continue
		}
		ok := false
		for _, name := range allowed {
			if a.Name.Local == name {
				ok = true
				break
			}
		}
		if !ok {
			return xdm.Errorf("FOJS0006",
				"attribute %q is not allowed on element %s", a.Name.Local, el.Name.Local)
		}
	}
	return nil
}

// attrValue returns a no-namespace attribute's value.
func attrValue(el *xdm.Node, local string) (string, bool) {
	for _, a := range el.Attrs {
		if a.Name.URI == "" && a.Name.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// jsonBooleanAttr reads escaped or escaped-key, which are xs:boolean and so
// accept "1" and "0" as well as the words.
func jsonBooleanAttr(el *xdm.Node, local string) (bool, error) {
	v, ok := attrValue(el, local)
	if !ok {
		return false, nil
	}
	switch strings.TrimSpace(v) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	}
	return false, xdm.Errorf("FOJS0006",
		"%q is not a valid xs:boolean for the %s attribute", v, local)
}

// elementText is the element's string value, ignoring comments and PIs.
//
// A comment splitting a number is invisible to the string value, which is why
// xml-to-json-038 expects "27" from "2<!--c-->7"; but a *child element* is not
// invisible and is FOJS0006.
func elementText(el *xdm.Node) (string, error) {
	var b strings.Builder
	for _, c := range el.Children {
		switch c.Kind {
		case xdm.KindText:
			b.WriteString(c.Value)
		case xdm.KindComment, xdm.KindPI:
		case xdm.KindElement:
			return "", xdm.Errorf("FOJS0006",
				"element %s may not have element children", el.Name.Local)
		}
	}
	return b.String(), nil
}

// writeJSONFrom serialises one element of the representation.
//
// isRoot distinguishes the outermost element, which may carry a key attribute
// left over from being selected out of a larger tree: xml-to-json-070 selects
// a keyed array and expects the key to be dropped rather than to be an error.
func writeJSONFrom(b *strings.Builder, el *xdm.Node, indent bool, depth int, isRoot bool) error {
	if el.Name.URI != nsJSON {
		return xdm.Errorf("FOJS0006",
			"element %s is not in the XPath functions namespace", el.Name.Local)
	}
	// escaped and escaped-key are allowed on any element of the
	// representation, including ones where they have no effect: xml-to-json-064
	// puts escaped-key on a map and expects it ignored rather than rejected.
	allowed := []string{"key", "escaped", "escaped-key"}
	if err := checkJSONAttrs(el, allowed...); err != nil {
		return err
	}
	switch el.Name.Local {
	case "null":
		kids, err := jsonChildren(el)
		if err != nil {
			return err
		}
		if len(kids) > 0 {
			return xdm.Errorf("FOJS0006", "element null must be empty")
		}
		if txt, err := elementText(el); err != nil {
			return err
		} else if strings.TrimSpace(txt) != "" {
			return xdm.Errorf("FOJS0006", "element null must be empty")
		}
		b.WriteString("null")
	case "boolean":
		txt, err := elementText(el)
		if err != nil {
			return err
		}
		switch strings.TrimSpace(txt) {
		case "true", "1":
			b.WriteString("true")
		case "false", "0":
			b.WriteString("false")
		default:
			return xdm.Errorf("FOJS0006", "%q is not a valid boolean", txt)
		}
	case "number":
		txt, err := elementText(el)
		if err != nil {
			return err
		}
		s, err := canonicalJSONNumber(strings.TrimSpace(txt))
		if err != nil {
			return err
		}
		b.WriteString(s)
	case "string":
		txt, err := elementText(el)
		if err != nil {
			return err
		}
		esc, err := jsonBooleanAttr(el, "escaped")
		if err != nil {
			return err
		}
		lit, err := jsonStringLiteral(txt, esc)
		if err != nil {
			return err
		}
		b.WriteString(lit)
	case "array":
		kids, err := jsonChildren(el)
		if err != nil {
			return err
		}
		b.WriteByte('[')
		for i, c := range kids {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONIndent(b, indent, depth+1)
			if err := writeJSONFrom(b, c, indent, depth+1, false); err != nil {
				return err
			}
		}
		if len(kids) > 0 {
			writeJSONIndent(b, indent, depth)
		}
		b.WriteByte(']')
	case "map":
		kids, err := jsonChildren(el)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		b.WriteByte('{')
		for i, c := range kids {
			// Every entry of a map must name itself. The check is here rather
			// than in the recursive call because it is the *parent* that
			// makes the key mandatory: the same element at the top level has
			// no key and is still valid.
			k, ok := attrValue(c, "key")
			if !ok {
				return xdm.Errorf("FOJS0006",
					"every child of a map element must have a key attribute")
			}
			escKey, err := jsonBooleanAttr(c, "escaped-key")
			if err != nil {
				return err
			}
			// The key is compared after unescaping, so "\n" and a literal
			// newline are the same key and collide.
			plain := k
			if escKey {
				plain, err = unescapeJSONString(k)
				if err != nil {
					return err
				}
			}
			if seen[plain] {
				return xdm.Errorf("FOJS0006", "duplicate key %q in a map element", plain)
			}
			seen[plain] = true
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONIndent(b, indent, depth+1)
			lit, err := jsonStringLiteral(k, escKey)
			if err != nil {
				return err
			}
			b.WriteString(lit)
			b.WriteByte(':')
			if indent {
				b.WriteByte(' ')
			}
			if err := writeJSONFrom(b, c, indent, depth+1, false); err != nil {
				return err
			}
		}
		if len(kids) > 0 {
			writeJSONIndent(b, indent, depth)
		}
		b.WriteByte('}')
	default:
		return xdm.Errorf("FOJS0006",
			"element %s is not part of the JSON XML representation", el.Name.Local)
	}
	// A key on the outermost element is dropped rather than rejected; one on
	// an element that is not a map's child anywhere else has already been
	// used or is spurious, and the map branch above is what enforces its
	// presence where it is required.
	_ = isRoot
	return nil
}

func writeJSONIndent(b *strings.Builder, indent bool, depth int) {
	if !indent {
		return
	}
	b.WriteByte('\n')
	for i := 0; i < depth; i++ {
		b.WriteString("  ")
	}
}

// canonicalJSONNumber renders the text of a number element as JSON.
//
// The element's content is xs:double in the schema, so it accepts more than
// JSON does — leading whitespace, a leading plus, leading zeroes — and
// xml-to-json-026 expects " +005 " to come out as "5". The value is therefore
// parsed and re-rendered rather than copied, except that an integral value
// keeps an integer spelling so that "23" does not become "23.0".
func canonicalJSONNumber(s string) (string, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return "", xdm.Errorf("FOJS0006", "%q is not a valid number", s)
	}
	// A double that JSON cannot express at all is not serialisable: JSON has
	// no infinity and no NaN.
	if f != f || f > 1.7976931348623157e308 || f < -1.7976931348623157e308 {
		return "", xdm.Errorf("FOJS0006", "%q has no JSON representation", s)
	}
	a := xdm.NewDouble(f)
	return a.String(), nil
}

// jsonStringLiteral renders a string value as a quoted JSON literal.
//
// When escaped is true the text already holds JSON escapes, which are checked
// and passed through; the characters that must be escaped and are not get
// escaped now. When it is false every backslash is data and is doubled.
func jsonStringLiteral(s string, escaped bool) (string, error) {
	var b strings.Builder
	b.WriteByte('"')
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if escaped && c == '\\' {
			n, err := copyJSONEscape(&b, rs, i)
			if err != nil {
				return "", err
			}
			i += n - 1
			continue
		}
		writeJSONChar(&b, c)
	}
	b.WriteByte('"')
	return b.String(), nil
}

// copyJSONEscape validates one escape sequence and copies it verbatim,
// returning how many runes it consumed.
//
// Verbatim matters: xml-to-json-071 feeds in "  " and expects both
// spellings preserved, so the sequence is not decoded and re-encoded.
func copyJSONEscape(b *strings.Builder, rs []rune, i int) (int, error) {
	if i+1 >= len(rs) {
		return 0, xdm.Errorf("FOJS0007", "a string may not end with a backslash")
	}
	switch rs[i+1] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		b.WriteRune(rs[i])
		b.WriteRune(rs[i+1])
		return 2, nil
	case 'u':
		if i+5 >= len(rs) {
			return 0, xdm.Errorf("FOJS0007", "a \\u escape needs four hexadecimal digits")
		}
		for j := i + 2; j < i+6; j++ {
			c := rs[j]
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return 0, xdm.Errorf("FOJS0007",
					"%q is not a hexadecimal digit in a \\u escape", string(c))
			}
		}
		for j := i; j < i+6; j++ {
			b.WriteRune(rs[j])
		}
		return 6, nil
	}
	return 0, xdm.Errorf("FOJS0007", "invalid escape sequence \\%s", string(rs[i+1]))
}

// writeJSONChar emits one character of a string value, escaping what must be.
func writeJSONChar(b *strings.Builder, c rune) {
	switch c {
	case '"':
		b.WriteString("\\\"")
	case '\\':
		b.WriteString("\\\\")
	case '/':
		// The solidus needs no escape to be valid JSON, but the serialisation
		// rules escape it anyway (bug 29665), and xml-to-json-017 round-trips
		// a string through json-to-xml expecting the escape back.
		b.WriteString("\\/")
	case 0x08:
		b.WriteString("\\b")
	case 0x09:
		b.WriteString("\\t")
	case 0x0A:
		b.WriteString("\\n")
	case 0x0C:
		b.WriteString("\\f")
	case 0x0D:
		b.WriteString("\\r")
	default:
		// The control ranges have no literal spelling in JSON, and U+007F to
		// U+009F are escaped too: xml-to-json-073 pins down exactly which
		// codepoints come out as \uHHHH.
		if c < 0x20 || (c >= 0x7F && c <= 0x9F) {
			b.WriteString("\\u" + strings.ToUpper(pad4(strconv.FormatInt(int64(c), 16))))
			return
		}
		b.WriteRune(c)
	}
}

// unescapeJSONString decodes JSON escapes in a key, for duplicate comparison.
func unescapeJSONString(s string) (string, error) {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '\\' {
			b.WriteRune(rs[i])
			continue
		}
		if i+1 >= len(rs) {
			return "", xdm.Errorf("FOJS0007", "a string may not end with a backslash")
		}
		switch rs[i+1] {
		case '"', '\\', '/':
			b.WriteRune(rs[i+1])
			i++
		case 'b':
			b.WriteRune(0x08)
			i++
		case 'f':
			b.WriteRune(0x0C)
			i++
		case 'n':
			b.WriteRune(0x0A)
			i++
		case 'r':
			b.WriteRune(0x0D)
			i++
		case 't':
			b.WriteRune(0x09)
			i++
		case 'u':
			if i+5 >= len(rs) {
				return "", xdm.Errorf("FOJS0007", "a \\u escape needs four hexadecimal digits")
			}
			v, err := strconv.ParseInt(string(rs[i+2:i+6]), 16, 32)
			if err != nil {
				return "", xdm.Errorf("FOJS0007", "invalid \\u escape")
			}
			b.WriteRune(rune(v))
			i += 5
		default:
			return "", xdm.Errorf("FOJS0007", "invalid escape sequence \\%s", string(rs[i+1]))
		}
	}
	return b.String(), nil
}

// --- Registration ----------------------------------------------------------

// registerJSONFuncs adds the four JSON functions to the builtin library.
func registerJSONFuncs(l *Library) {
	l.registerFnSince(XPath31, "parse-json", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// $json-text is xs:string?, so an empty argument is the empty
		// sequence rather than a parse of "". The options are still decoded,
		// because their errors are raised whatever the input.
		opts, err := jsonOptionsFrom(ctx, args, 1, false)
		if err != nil {
			return nil, err
		}
		if len(args) > 0 && len(args[0]) == 0 {
			return xdm.Empty(), nil
		}
		text, err := argStringRequired(args, 0)
		if err != nil {
			return nil, err
		}
		return parseJSONText(ctx, text, opts)
	})

	l.registerFnSince(XPath31, "json-doc", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		opts, err := jsonOptionsFrom(ctx, args, 1, false)
		if err != nil {
			return nil, err
		}
		if len(args) > 0 && len(args[0]) == 0 {
			return xdm.Empty(), nil
		}
		// The read is fn:unparsed-text's, and so are its errors: json-doc is
		// specified as that read followed by fn:parse-json, and the suite
		// asserts FOUT1170 for a URI that cannot be retrieved.
		text, err := unparsedText(ctx, args[:1])
		if err != nil {
			return nil, err
		}
		return parseJSONText(ctx, text, opts)
	})

	l.registerFnSince(XPath31, "json-to-xml", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		opts, err := jsonOptionsFrom(ctx, args, 1, true)
		if err != nil {
			return nil, err
		}
		if len(args) > 0 && len(args[0]) == 0 {
			return xdm.Empty(), nil
		}
		text, err := argStringRequired(args, 0)
		if err != nil {
			return nil, err
		}
		return jsonToXML(ctx, text, opts)
	})

	l.registerFnSince(XPath31, "xml-to-json", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		indent := false
		if len(args) > 1 {
			m, err := singleMapArg(args, 1)
			if err != nil {
				return nil, err
			}
			if m != nil {
				v, _, err := m.Get(xdm.NewString("indent"))
				if err != nil {
					return nil, err
				}
				if v != nil {
					// The entry exists; its type is checked even when the
					// value is the empty sequence, which xml-to-json-059
					// expects to be XPTY0004 rather than "unset".
					if _, present, _ := m.Get(xdm.NewString("indent")); present {
						indent, err = jsonOptionBool(v, "indent")
						if err != nil {
							return nil, err
						}
					}
				}
			}
		}
		if len(args) == 0 || len(args[0]) == 0 {
			return xdm.Empty(), nil
		}
		if len(args[0]) != 1 {
			return nil, xdm.ErrType(
				"fn:xml-to-json expects at most one node, got %d items", len(args[0]))
		}
		n, ok := args[0][0].(*xdm.Node)
		if !ok {
			return nil, xdm.ErrType("fn:xml-to-json expects a node, got %s",
				args[0][0].TypeName())
		}
		s, err := xmlToJSON(n, indent)
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewString(s)), nil
	})
}
