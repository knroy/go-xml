package relaxng

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Lexical analysis of the RELAX NG compact syntax.
//
// The compact syntax is a second surface for the same language: every construct
// in it has an XML-syntax equivalent, and the specification defines it by that
// correspondence rather than by its own semantics. So this package parses RNC
// into the XML syntax and hands the result to the existing compiler — the
// derivative engine, the section 7 restrictions and the datatype library are
// all reached through that one tree, and none of them needs to learn a second
// notation.
//
// The compact syntax specification is NOT vendored in this repository (the
// suite under testdata/relaxng is James Clark's spectest.xml, which is XML
// syntax only and carries no .rnc cases). The grammar implemented here is the
// one from the OASIS committee specification of 2002; where a rule was not
// verifiable against a vendored text it is called out in a comment at the
// point it is applied, so that a reader can see exactly which claims rest on
// the spec being remembered rather than read.

// tokenKind classifies one lexeme.
//
// Keywords are deliberately NOT a separate kind. The compact syntax has no
// reserved words in the usual sense: "element" is a keyword where a pattern is
// expected and an ordinary identifier where a name is, and which one it is
// depends on the parser's position, not on the lexer's. Folding them into
// tokIdent and letting the parser ask "is this the keyword X here?" is what
// makes that possible; a lexer that decided for itself would have to guess.
type tokenKind int

const (
	tokEOF tokenKind = iota
	// tokIdent is an identifier or a keyword — see the note above on why
	// they are not distinguished here.
	tokIdent
	// tokEscapedIdent is an identifier that was written with a leading
	// backslash. It is kept apart from tokIdent for exactly one reason: the
	// backslash means "this is a name, not a keyword", so `\element` must be
	// usable as a definition name where bare `element` may not. Losing that
	// distinction in the lexer would make the escape a no-op.
	tokEscapedIdent
	// tokCName is a prefixed name, prefix:local.
	tokCName
	// tokNsName is prefix:* — a name class matching any name in a namespace.
	tokNsName
	tokLiteral
	// tokDocumentation is a "##" comment, which unlike a "#" comment is part
	// of the grammar: it becomes an annotation on the construct that follows.
	tokDocumentation
	tokPunct
)

func (k tokenKind) String() string {
	switch k {
	case tokEOF:
		return "end of input"
	case tokIdent, tokEscapedIdent:
		return "identifier"
	case tokCName:
		return "prefixed name"
	case tokNsName:
		return "namespace name"
	case tokLiteral:
		return "literal"
	case tokDocumentation:
		return "documentation"
	case tokPunct:
		return "punctuation"
	}
	return "token"
}

// token is one lexeme with the source position it began at.
type token struct {
	kind tokenKind
	// text is the lexeme's value, already unescaped: a literal's quotes are
	// removed and its \x{...} escapes resolved, an escaped identifier has
	// lost its backslash.
	text string
	// prefix is the part before the colon, for tokCName and tokNsName.
	prefix string
	line   int
	col    int
}

func (t token) String() string {
	switch t.kind {
	case tokEOF:
		return "end of input"
	case tokLiteral:
		return fmt.Sprintf("literal %q", t.text)
	case tokCName, tokNsName:
		return fmt.Sprintf("%q", t.prefix+":"+t.text)
	}
	return fmt.Sprintf("%q", t.text)
}

// lexer turns RNC source into tokens.
type lexer struct {
	src  string
	pos  int
	line int
	col  int
}

func newLexer(src string) *lexer {
	// A leading byte order mark is not part of the grammar and is not an
	// error either: a schema saved by an editor that writes one must still
	// parse, and every other reader of these files strips it silently.
	src = strings.TrimPrefix(src, "\uFEFF")
	return &lexer{src: src, line: 1, col: 1}
}

// errorf reports a lexical error carrying the position it was found at.
func (l *lexer) errorf(line, col int, format string, args ...any) error {
	return fmt.Errorf("relaxng: compact syntax, line %d column %d: %s",
		line, col, fmt.Sprintf(format, args...))
}

// next returns the next token.
func (l *lexer) next() (token, error) {
	if err := l.skipSpaceAndComments(); err != nil {
		return token{}, err
	}
	line, col := l.line, l.col
	if l.pos >= len(l.src) {
		return token{kind: tokEOF, line: line, col: col}, nil
	}

	// A documentation comment survives skipSpaceAndComments, because it is a
	// token rather than whitespace.
	if strings.HasPrefix(l.src[l.pos:], "##") {
		return l.lexDocumentation()
	}

	c := l.src[l.pos]
	switch {
	case c == '"' || c == '\'':
		return l.lexLiteral()
	case c == '\\':
		return l.lexEscapedIdent()
	}

	if r, _ := utf8.DecodeRuneInString(l.src[l.pos:]); isNameStart4(r) {
		return l.lexName()
	}
	return l.lexPunct()
}

// skipSpaceAndComments advances past whitespace and "#" comments.
//
// A "##" comment is left in place: it is a documentation annotation and the
// caller must see it. Distinguishing the two here rather than in next() keeps
// the "is this whitespace" question in one place.
func (l *lexer) skipSpaceAndComments() error {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			l.advance(1)
			continue
		}
		if c == '#' {
			if strings.HasPrefix(l.src[l.pos:], "##") {
				return nil
			}
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.advance(1)
			}
			continue
		}
		return nil
	}
	return nil
}

// advance consumes n bytes, tracking line and column.
func (l *lexer) advance(n int) {
	for i := 0; i < n && l.pos < len(l.src); i++ {
		if l.src[l.pos] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
}

// lexDocumentation reads one or more consecutive "##" lines.
//
// Consecutive lines are joined rather than returned separately because they
// document one construct: the spec's mapping makes a run of them a single
// annotation element whose content is the lines' text, and a parser that saw
// them one at a time would have to rejoin them anyway.
func (l *lexer) lexDocumentation() (token, error) {
	line, col := l.line, l.col
	var lines []string
	for {
		if !strings.HasPrefix(l.src[l.pos:], "##") {
			break
		}
		l.advance(2)
		start := l.pos
		for l.pos < len(l.src) && l.src[l.pos] != '\n' {
			l.advance(1)
		}
		// A single leading space is conventional and is not content; any
		// further indentation is the author's and is kept.
		lines = append(lines, strings.TrimPrefix(l.src[start:l.pos], " "))
		// Only a run broken by nothing but whitespace-to-end-of-line
		// continues the same annotation.
		save, saveLine, saveCol := l.pos, l.line, l.col
		if err := l.skipSpaceAndComments(); err != nil {
			return token{}, err
		}
		if !strings.HasPrefix(l.src[l.pos:], "##") {
			l.pos, l.line, l.col = save, saveLine, saveCol
			break
		}
	}
	return token{
		kind: tokDocumentation,
		text: strings.Join(lines, "\n"),
		line: line, col: col,
	}, nil
}

// lexEscapedIdent reads a backslash-escaped identifier.
//
// The escape exists so that a name colliding with a keyword can still be
// written — `\text` names a definition called "text". It escapes an identifier
// and nothing else, so a backslash followed by anything that is not a name
// start is an error rather than a literal backslash.
func (l *lexer) lexEscapedIdent() (token, error) {
	line, col := l.line, l.col
	l.advance(1) // the backslash
	if l.pos >= len(l.src) {
		return token{}, l.errorf(line, col, `"\" at end of input; it must be followed by an identifier`)
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.pos:])
	if !isNameStart4(r) {
		return token{}, l.errorf(line, col, `"\" must be followed by an identifier, not %q`, r)
	}
	tok, err := l.lexName()
	if err != nil {
		return token{}, err
	}
	// An escaped name is a name, so prefix:local after a backslash escapes
	// only the prefix and is not a construct the grammar offers.
	if tok.kind != tokIdent {
		return token{}, l.errorf(line, col, `"\" may escape only an identifier, not a %s`, tok.kind)
	}
	tok.kind = tokEscapedIdent
	tok.line, tok.col = line, col
	return tok, nil
}

// lexName reads an NCName, and the ":" forms built on one.
//
// Names follow XML 1.0 fourth edition here for the same reason they do in the
// XML syntax — see the note in ncname.go. Using a different rule for the two
// surfaces would make a name legal in one spelling of a schema and not the
// other, which is precisely what the compact syntax is defined not to do.
func (l *lexer) lexName() (token, error) {
	line, col := l.line, l.col
	start := l.pos
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if !isNameChar4(r) || r == ':' {
			break
		}
		l.advance(size)
	}
	name := l.src[start:l.pos]

	// A colon makes this prefix:local or prefix:*. It is only a name
	// separator when what follows can begin one; "a:b" in a context where ":"
	// were punctuation does not arise, since the grammar has no bare colon.
	if l.pos < len(l.src) && l.src[l.pos] == ':' {
		save, saveLine, saveCol := l.pos, l.line, l.col
		l.advance(1)
		if l.pos < len(l.src) && l.src[l.pos] == '*' {
			l.advance(1)
			return token{kind: tokNsName, prefix: name, line: line, col: col}, nil
		}
		if l.pos < len(l.src) {
			if r, _ := utf8.DecodeRuneInString(l.src[l.pos:]); isNameStart4(r) {
				lstart := l.pos
				for l.pos < len(l.src) {
					r, size := utf8.DecodeRuneInString(l.src[l.pos:])
					if !isNameChar4(r) || r == ':' {
						break
					}
					l.advance(size)
				}
				return token{
					kind: tokCName, prefix: name, text: l.src[lstart:l.pos],
					line: line, col: col,
				}, nil
			}
		}
		// Not a name after all — put the colon back and let the caller see a
		// bare identifier followed by whatever this is.
		l.pos, l.line, l.col = save, saveLine, saveCol
	}
	return token{kind: tokIdent, text: name, line: line, col: col}, nil
}

// lexLiteral reads a quoted string, single or triple quoted.
//
// Triple quoting exists so a literal may contain its own quote character and
// span lines, which a schema embedding a regular expression regularly needs.
func (l *lexer) lexLiteral() (token, error) {
	line, col := l.line, l.col
	quote := l.src[l.pos]
	triple := strings.HasPrefix(l.src[l.pos:], strings.Repeat(string(quote), 3))
	delim := string(quote)
	if triple {
		delim = strings.Repeat(string(quote), 3)
	}
	l.advance(len(delim))
	start := l.pos
	for {
		if l.pos >= len(l.src) {
			return token{}, l.errorf(line, col, "unterminated literal")
		}
		if strings.HasPrefix(l.src[l.pos:], delim) {
			break
		}
		// A single-quoted literal may not cross a line. Allowing it would let
		// a missing close quote swallow the rest of the schema and report the
		// error somewhere unrelated.
		if !triple && l.src[l.pos] == '\n' {
			return token{}, l.errorf(line, col, "unterminated literal")
		}
		l.advance(1)
	}
	raw := l.src[start:l.pos]
	l.advance(len(delim))

	text, err := unescapeLiteral(raw)
	if err != nil {
		return token{}, l.errorf(line, col, "%s", err)
	}
	return token{kind: tokLiteral, text: text, line: line, col: col}, nil
}

// unescapeLiteral resolves the \x{...} escapes a literal may contain.
//
// This is the ONLY escape in a compact-syntax literal: a backslash not
// beginning "\x{" stands for itself. That matters because these literals
// routinely hold XSD regular expressions, which are full of backslashes that
// mean something to the regex engine and nothing here — "\i" and "\c" in
// `pattern = "[\i-[:]][\c-[:]]*"` must reach the datatype library untouched.
func unescapeLiteral(s string) (string, error) {
	if !strings.Contains(s, `\x{`) {
		return s, nil
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		if !strings.HasPrefix(s[i:], `\x{`) {
			sb.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			return "", fmt.Errorf(`unterminated "\x{" escape in literal`)
		}
		digits := s[i+3 : i+end]
		if digits == "" {
			return "", fmt.Errorf(`empty "\x{}" escape in literal`)
		}
		var r rune
		for _, d := range digits {
			var v rune
			switch {
			case d >= '0' && d <= '9':
				v = d - '0'
			case d >= 'a' && d <= 'f':
				v = d - 'a' + 10
			case d >= 'A' && d <= 'F':
				v = d - 'A' + 10
			default:
				return "", fmt.Errorf(`%q is not a hexadecimal digit in an "\x{}" escape`, d)
			}
			r = r*16 + v
			if r > 0x10FFFF {
				return "", fmt.Errorf(`"\x{%s}" is beyond the Unicode range`, digits)
			}
		}
		// The escape names a character, so it must name one that XML can
		// hold. Letting a surrogate or a forbidden control through here would
		// build a tree that cannot be serialised, and the failure would
		// surface far from the literal that caused it.
		if !isXMLChar(r) {
			return "", fmt.Errorf(`"\x{%s}" is not a character XML permits`, digits)
		}
		sb.WriteRune(r)
		i += end + 1
	}
	return sb.String(), nil
}

// isXMLChar reports whether r is legal in an XML 1.0 document.
func isXMLChar(r rune) bool {
	return r == 0x9 || r == 0xA || r == 0xD ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}

// punctuation is the operator set, longest first so that ">>" is not read as
// two ">" and "|=" is not read as "|" followed by "=".
var punctuation = []string{
	"|=", "&=", "-=", "~", "|", "&", ",", "?", "*", "+", "-",
	"(", ")", "{", "}", "[", "]", "=", ">>",
}

// lexPunct reads one operator.
func (l *lexer) lexPunct() (token, error) {
	line, col := l.line, l.col
	for _, p := range punctuation {
		if strings.HasPrefix(l.src[l.pos:], p) {
			l.advance(len(p))
			return token{kind: tokPunct, text: p, line: line, col: col}, nil
		}
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.pos:])
	return token{}, l.errorf(line, col, "unexpected character %q", r)
}
