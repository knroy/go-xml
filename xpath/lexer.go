// Package xpath implements XPath 2.0: lexing, parsing to an AST, static
// analysis, and evaluation against an XDM tree.
package xpath

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TokenKind classifies a lexical token.
type TokenKind int

const (
	TokEOF  TokenKind = iota
	TokName           // NCName or QName, including keywords: disambiguated by the parser
	TokNumber
	TokString
	TokVar      // $name
	TokOp       // operators and punctuation
	TokWildcard // *, prefix:*, *:local
)

// Token is a lexical token with its source offset, which error messages use to
// point at the offending construct.
type Token struct {
	Kind TokenKind
	Val  string
	Pos  int

	// Num holds the parsed numeric value and NumType its XPath type, so the
	// parser does not re-parse the literal. A numeric literal's type is fixed
	// by its lexical form: no dot or E means integer, a dot means decimal, an
	// E means double.
	Num     float64
	NumType numLiteralKind
}

type numLiteralKind int

const (
	numInteger numLiteralKind = iota
	numDecimal
	numDouble
)

// Lexer turns XPath source into tokens.
//
// XPath 2.0's grammar is not context-free at the lexical level: whether `*`
// means multiplication or "any element", and whether `div`, `and`, `is` and
// friends are operators or element names, depends on what preceded them. The
// spec resolves this with a rule stated in terms of the previous token, and
// that is what prevOperand tracks. Trying to decide these in the parser
// instead means the lexer must emit ambiguous tokens and the parser must
// re-lex, which is worse.
type Lexer struct {
	src string
	pos int

	// prevOperand reports whether the previous token could end an operand. If
	// it could, then `*` is multiplication and `div` is an operator; if it
	// could not, `*` is a wildcard and `div` is a name test.
	prevOperand bool

	// extended enables the two XPath 3.0 constructs the XSLT test suite uses
	// in its own assertion expressions: the braced URI literal Q{uri}local
	// and the simple map operator "!". It is off by default, so an XSLT 2.0
	// stylesheet still gets XPST0003 for either — which is correct, and which
	// several tests in the suite assert. Only ParseExtended turns it on.
	extended bool

	// bracedURIs collects the URIs seen in braced URI literals, in the order
	// they were lexed. A literal is rewritten to a synthetic prefix naming
	// its index here, so that the ordinary QName resolution path can bind it
	// without the parser needing a second spelling of every name test. The
	// prefix begins with a character no NCName may contain, so it cannot
	// collide with a prefix written in the source.
	bracedURIs []string
}

// bracedURIPrefix marks a synthetic prefix standing for a braced URI literal.
// U+0001 is not a name character, so no source-written prefix can begin with
// it and no lookup of a real prefix can be captured by one of these.
const bracedURIPrefix = "\x01Q"

// NewLexer returns a lexer over src.
func NewLexer(src string) *Lexer { return &Lexer{src: src} }

// newExtendedLexer returns a lexer that also accepts the XPath 3.0 constructs
// the test suite's assertion language uses. See Lexer.extended.
func newExtendedLexer(src string) *Lexer { return &Lexer{src: src, extended: true} }

// operatorKeywords are the names that act as infix operators when they follow
// an operand.
var operatorKeywords = map[string]bool{
	"and": true, "or": true, "div": true, "idiv": true, "mod": true,
	"is": true, "to": true, "eq": true, "ne": true, "lt": true,
	"le": true, "gt": true, "ge": true, "union": true, "intersect": true,
	"except": true, "instance": true, "treat": true, "castable": true,
	"cast": true, "satisfies": true, "return": true, "in": true, "as": true,
	"then": true, "else": true,
}

// Tokens lexes the entire input.
func (l *Lexer) Tokens() ([]Token, error) {
	var out []Token
	for {
		t, err := l.next()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
		if t.Kind == TokEOF {
			return out, nil
		}
	}
}

func (l *Lexer) next() (Token, error) {
	if err := l.skipSpace(); err != nil {
		return Token{}, err
	}
	start := l.pos
	if l.pos >= len(l.src) {
		return Token{Kind: TokEOF, Pos: start}, nil
	}

	c := l.src[l.pos]
	switch {
	case c == '$':
		l.pos++
		name, err := l.lexQName()
		if err != nil {
			return Token{}, err
		}
		l.prevOperand = true
		return Token{Kind: TokVar, Val: name, Pos: start}, nil

	case c == '"' || c == '\'':
		s, err := l.lexString(c)
		if err != nil {
			return Token{}, err
		}
		l.prevOperand = true
		return Token{Kind: TokString, Val: s, Pos: start}, nil

	case c >= '0' && c <= '9':
		return l.lexNumber(start)

	case c == '.':
		// A leading dot may start a decimal literal (.5) or be the context
		// item (.) or the descendant step (..).
		if l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '9' {
			return l.lexNumber(start)
		}
		if strings.HasPrefix(l.src[l.pos:], "..") {
			l.pos += 2
			l.prevOperand = true
			return Token{Kind: TokOp, Val: "..", Pos: start}, nil
		}
		l.pos++
		l.prevOperand = true
		return Token{Kind: TokOp, Val: ".", Pos: start}, nil

	case c == '*':
		// The context-dependence in its purest form.
		if l.prevOperand {
			l.pos++
			// A "*" that follows an operand is either multiplication or a
			// sequence-type occurrence indicator, and the lexer cannot tell
			// which. The flag is cleared, which is what multiplication does:
			// an operator does not end an operand, so a "*" immediately
			// after it is a wildcard. That is the case the spec's own
			// tokenization rule pins and the suite tests with "*******" —
			// four wildcards separated by three multiplications — where
			// leaving the flag set lexed every star after the first as an
			// operator and the expression did not parse.
			//
			// The occurrence indicator is not lost by this. It occurs only
			// in a sequence-type position, which the parser reaches through
			// parseSequenceType, and that function already accepts the "*"
			// under either spelling — TokOp when an operand preceded it,
			// TokWildcard when a type name did.
			l.prevOperand = false
			return Token{Kind: TokOp, Val: "*", Pos: start}, nil
		}
		l.pos++
		// *:local is a wildcard with a fixed local name.
		if l.pos < len(l.src) && l.src[l.pos] == ':' {
			l.pos++
			local, err := l.lexNCName()
			if err != nil {
				return Token{}, err
			}
			l.prevOperand = true
			return Token{Kind: TokWildcard, Val: "*:" + local, Pos: start}, nil
		}
		l.prevOperand = true
		return Token{Kind: TokWildcard, Val: "*", Pos: start}, nil

	case isNameStart(rune(c)) || c >= utf8.RuneSelf:
		name, err := l.lexQName()
		if err != nil {
			return Token{}, err
		}
		// prefix:* is a wildcard over one namespace.
		if strings.HasSuffix(name, ":") && l.pos < len(l.src) && l.src[l.pos] == '*' {
			l.pos++
			l.prevOperand = true
			return Token{Kind: TokWildcard, Val: name + "*", Pos: start}, nil
		}
		if operatorKeywords[name] && l.prevOperand {
			l.prevOperand = false
			return Token{Kind: TokOp, Val: name, Pos: start}, nil
		}
		// Otherwise it is a name: an element name test, a function name, an
		// axis name, or a keyword in a position where it is not an operator
		// (the `if` in `if (...)`, the `for` in `for $x in ...`).
		l.prevOperand = true
		return Token{Kind: TokName, Val: name, Pos: start}, nil
	}

	// The simple map operator. It must be tested before the multi-character
	// list below, which would otherwise never see it, and after nothing —
	// "!=" is still matched first because the prefix check there runs on the
	// two-character string.
	if l.extended && c == '!' && !strings.HasPrefix(l.src[l.pos:], "!=") {
		l.pos++
		l.prevOperand = false
		return Token{Kind: TokOp, Val: "!", Pos: start}, nil
	}

	// Multi-character operators must be matched before their prefixes, or
	// "!=" lexes as "!" followed by "=".
	for _, op := range []string{"!=", "<=", ">=", "<<", ">>", "//", "::"} {
		if strings.HasPrefix(l.src[l.pos:], op) {
			l.pos += len(op)
			l.prevOperand = false
			return Token{Kind: TokOp, Val: op, Pos: start}, nil
		}
	}

	switch c {
	case '(', ')', '[', ']', ',', '/', '+', '-', '=', '<', '>', '|', '@', '?', '{', '}':
		l.pos++
		op := string(c)
		// A closing bracket ends an operand. So does "?" when one precedes
		// it, because there it is a sequence-type occurrence indicator
		// rather than the start of something — and an indicator ends the
		// type it applies to. That matters for the "*" which may come next:
		// "3 treat as xs:integer ? * 3" multiplies, and without this the
		// "*" lexed as a wildcard and the expression did not parse.
		//
		// "+" is deliberately NOT in that set even though it too can be an
		// occurrence indicator. Binary "+" is far commoner, and after a
		// binary operator a "*" is a wildcard: "5.+*" is the decimal 5 plus
		// the wildcard step, which the suite pins as select-3502 and which
		// treating "+" as operand-ending made a syntax error. The
		// occurrence-indicator reading survives the change because
		// parseMultiplicative accepts a wildcard-spelled "*" in operator
		// position, so "xs:integer + * 3" still multiplies.
		l.prevOperand = op == ")" || op == "]" ||
			(op == "?" && l.prevOperand)
		return Token{Kind: TokOp, Val: op, Pos: start}, nil
	}

	return Token{}, fmt.Errorf("XPST0003: unexpected character %q at offset %d", string(c), l.pos)
}

func (l *Lexer) skipSpace() error {
	for l.pos < len(l.src) {
		switch l.src[l.pos] {
		case ' ', '\t', '\n', '\r':
			l.pos++
		case '(':
			// (: comment :) — nestable, per the spec.
			if strings.HasPrefix(l.src[l.pos:], "(:") {
				if err := l.skipComment(); err != nil {
					return err
				}
				continue
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}

func (l *Lexer) skipComment() error {
	start := l.pos
	depth := 0
	for l.pos < len(l.src) {
		switch {
		case strings.HasPrefix(l.src[l.pos:], "(:"):
			depth++
			l.pos += 2
		case strings.HasPrefix(l.src[l.pos:], ":)"):
			depth--
			l.pos += 2
			if depth == 0 {
				return nil
			}
		default:
			l.pos++
		}
	}
	// Running off the end means the comment was never closed. Ignoring that
	// silently discarded the rest of the expression, so "1(: unterminated"
	// evaluated to 1 instead of reporting the syntax error.
	return fmt.Errorf(
		"XPST0003: unterminated comment starting at offset %d", start)
}

func (l *Lexer) lexString(quote byte) (string, error) {
	l.pos++ // opening quote
	var sb strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == quote {
			// A doubled quote is an escaped quote, not the end of the literal.
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == quote {
				sb.WriteByte(quote)
				l.pos += 2
				continue
			}
			l.pos++
			return sb.String(), nil
		}
		sb.WriteByte(c)
		l.pos++
	}
	return "", fmt.Errorf("XPST0003: unterminated string literal")
}

func (l *Lexer) lexNumber(start int) (Token, error) {
	kind := numInteger
	for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
		l.pos++
	}
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		kind = numDecimal
		l.pos++
		for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
			l.pos++
		}
	}
	if l.pos < len(l.src) && (l.src[l.pos] == 'e' || l.src[l.pos] == 'E') {
		save := l.pos
		l.pos++
		if l.pos < len(l.src) && (l.src[l.pos] == '+' || l.src[l.pos] == '-') {
			l.pos++
		}
		if l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
			kind = numDouble
			for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
				l.pos++
			}
		} else {
			// "1e" with no exponent digits: the 'e' belongs to a following
			// name, as in "1 eq 2" written without spaces.
			l.pos = save
		}
	}
	text := l.src[start:l.pos]
	// A numeric literal must be followed by something that is not a name
	// character. "10idiv 3" is not "10 idiv 3": the grammar requires the
	// separation, and without this check the number and the operator ran
	// together and evaluated as though the space were there.
	if l.pos < len(l.src) && isNameStart(rune(l.src[l.pos])) {
		return Token{}, fmt.Errorf(
			"XPST0003: numeric literal %q must be separated from what follows",
			text)
	}
	// The float value is a convenience for the double case; integer and
	// decimal literals are re-parsed exactly, in big.Rat, by the parser.
	// Sscanf overflows to +Inf and reports an error for a literal with more
	// digits than a double can hold, which made a 400-digit xs:integer — a
	// perfectly ordinary arbitrary-precision value — a syntax error. An
	// overflow is only meaningful for a double, where it yields INF.
	// The float value is a convenience for the double case; integer and
	// decimal literals are re-parsed exactly, in big.Rat, by the parser.
	//
	// A literal too large for a double is not a syntax error. For an
	// xs:integer it is an ordinary arbitrary-precision value, and a 400-digit
	// one was being refused outright; for an xs:double it is INF, which is
	// what IEEE 754 overflow produces and what the suite asserts for
	// "999...9E1000...0". Only a genuinely malformed literal is XPST0003, and
	// the scanner above has already established the shape.
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		var numErr *strconv.NumError
		if !errors.As(err, &numErr) || numErr.Err != strconv.ErrRange {
			return Token{}, fmt.Errorf(
				"XPST0003: invalid numeric literal %q", text)
		}
		// ErrRange: ParseFloat still returns ±Inf (or ±0 for underflow),
		// which is the right value.
	}
	l.prevOperand = true
	return Token{Kind: TokNumber, Val: text, Pos: start, Num: f, NumType: kind}, nil
}

// lexQName reads an NCName, optionally followed by ':' and a second NCName.
// A trailing ':' is returned as part of the value so the caller can detect the
// prefix:* wildcard form.
func (l *Lexer) lexQName() (string, error) {
	// A braced URI literal, Q{uri}local. Recognised only in extended mode;
	// otherwise "Q" is an ordinary name and the "{" that follows is the
	// syntax error XPath 2.0 says it is.
	if l.extended && strings.HasPrefix(l.src[l.pos:], "Q{") {
		l.pos += 2
		end := strings.IndexByte(l.src[l.pos:], '}')
		if end < 0 {
			return "", fmt.Errorf("XPST0003: unterminated braced URI literal at offset %d", l.pos)
		}
		uri := strings.TrimSpace(l.src[l.pos : l.pos+end])
		l.pos += end + 1
		local, err := l.lexNCName()
		if err != nil {
			return "", err
		}
		l.bracedURIs = append(l.bracedURIs, uri)
		return fmt.Sprintf("%s%d:%s", bracedURIPrefix, len(l.bracedURIs)-1, local), nil
	}
	first, err := l.lexNCName()
	if err != nil {
		return "", err
	}
	// A single ':' means a QName; '::' is the axis separator and must not be
	// consumed here.
	if l.pos < len(l.src) && l.src[l.pos] == ':' &&
		!strings.HasPrefix(l.src[l.pos:], "::") {
		l.pos++
		if l.pos < len(l.src) && l.src[l.pos] == '*' {
			return first + ":", nil // caller consumes the '*'
		}
		second, err := l.lexNCName()
		if err != nil {
			return "", err
		}
		return first + ":" + second, nil
	}
	return first, nil
}

func (l *Lexer) lexNCName() (string, error) {
	start := l.pos
	// End of input decodes as (RuneError, 0), and RuneError is U+FFFD, which
	// is INSIDE NameStartChar's [#xFDF0-#xFFFD] range. Without this guard a
	// trailing colon — "prefix:" or "*:" — consumed zero bytes and returned
	// an empty name instead of the syntax error the grammar requires.
	if l.pos >= len(l.src) {
		return "", fmt.Errorf("XPST0003: expected a name at offset %d", l.pos)
	}
	r, size := utf8.DecodeRuneInString(l.src[l.pos:])
	if !isNameStart(r) {
		return "", fmt.Errorf("XPST0003: expected a name at offset %d", l.pos)
	}
	l.pos += size
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if !isNameChar(r) {
			break
		}
		l.pos += size
	}
	return l.src[start:l.pos], nil
}

// isNameStart reports whether r may begin an NCName. Note that ':' is excluded:
// it separates the parts of a QName rather than being part of one.
//
// This is XML 1.0 fifth edition's NameStartChar production minus the colon,
// transcribed, rather than the "_ or unicode.IsLetter" approximation it
// replaced. The two differ, and the difference is exactly what the suite
// tests: U+00B5 MICRO SIGN is category Ll, so IsLetter accepts it, but the
// production's Latin-1 range starts at U+00C0 and deliberately leaves it out.
// error-XPST0003o writes it as an element name and requires XPST0003 — a name
// no conforming XML parser would ever hand back cannot be a valid name test.
// xdm/qname.go carries the same transcription for the same reason.
func isNameStart(r rune) bool {
	switch {
	case r == '_':
		return true
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		return true
	case r >= 0xC0 && r <= 0xD6:
		return true
	case r >= 0xD8 && r <= 0xF6:
		return true
	case r >= 0xF8 && r <= 0x2FF:
		return true
	case r >= 0x370 && r <= 0x37D:
		return true
	case r >= 0x37F && r <= 0x1FFF:
		return true
	case r >= 0x200C && r <= 0x200D:
		return true
	case r >= 0x2070 && r <= 0x218F:
		return true
	case r >= 0x2C00 && r <= 0x2FEF:
		return true
	case r >= 0x3001 && r <= 0xD7FF:
		return true
	case r >= 0xF900 && r <= 0xFDCF:
		return true
	case r >= 0xFDF0 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0xEFFFF:
		return true
	}
	return false
}

// isNameChar is NameChar minus the colon: NameStartChar plus the characters
// that may appear only after the first.
func isNameChar(r rune) bool {
	if isNameStart(r) {
		return true
	}
	switch {
	case r == '-', r == '.', r == 0xB7:
		return true
	case r >= '0' && r <= '9':
		return true
	case r >= 0x300 && r <= 0x36F:
		return true
	case r >= 0x203F && r <= 0x2040:
		return true
	}
	return false
}
