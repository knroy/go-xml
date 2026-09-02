package xquery

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// parser reads XQuery's own syntax directly from the source.
//
// There is no token stream. Constructor syntax is XML, and whether a character
// is markup or text depends on which construct is being read, so the state
// that a mode-switching lexer would keep is here held where it belongs: in
// which method is running. Expressions are not read here at all — they are
// handed to xpath as substrings.
type parser struct {
	src string
	pos int
	sc  *staticContext

	// version is the XPath version expressions are compiled at. XQuery 3.1's
	// expression language is XPath 3.1's.
	version xpath.Version

	// lastType is the source of the SequenceType parseOptionalType last read,
	// empty when the declaration was absent. It is a field rather than a
	// return value because "as" is optional in four places and threading an
	// (string, bool, error) through each of them buys nothing.
	lastType string

	// depth bounds how deeply FLWOR expressions may nest, so that a query
	// written to nest them thousands deep cannot exhaust the goroutine stack
	// before the parser can report it.
	depth int
	// declaredNS records the prefixes this module's prolog has bound, so that
	// a second "declare namespace" for one can be reported as XQST0033. The
	// static context's map cannot answer it: the five predeclared prefixes
	// are in there from the start, and rebinding one of those is legal.
	declaredNS map[string]bool

	// vars and funcs accumulate the prolog's declarations. They are on the
	// parser rather than returned from parseProlog because a declaration
	// later in the prolog may name one earlier — a function body calling
	// another function is the ordinary case — and the check for a duplicate
	// name needs everything seen so far.
	vars  []*varDecl
	funcs []*funcDecl

	// contextItem is "declare context item", when the prolog made one.
	contextItem *contextItemDecl

	// formats are the declared decimal formats, keyed by Clark name; the
	// empty key is the default format.
	formats map[string]*xpath.DecimalFormat

	// serialization holds the "declare option output:*" declarations, keyed
	// by the parameter's local name and carrying its value verbatim. It is
	// nil until one is seen, which is how a query that declares none is
	// distinguished from one that declares an empty set — the two mean the
	// same to a serialiser, but the map is only allocated when there is
	// something to put in it.
	serialization map[string]string
}

// compiledExpr is an expression compiled by xpath, kept with the source it
// came from so that an error can quote it.
type compiledExpr struct {
	src      string
	compiled *xpath.Compiled
	// typed marks an expression a declared type was compiled into, so that
	// the "treat as" error it may raise is reported under the code a FLWOR
	// type mismatch has. See retypeError.
	typed bool
	// items is set instead of compiled when the expression is a constructor
	// or a nested FLWOR, neither of which xpath can read.
	items []node
	// check is a declared type applied to the value items produced, for the
	// case where there is no source text to fold a "treat as" into.
	check *compiledExpr
	// sc is the static context in force where the expression was written.
	//
	// It is needed by the one thing that resolves a name *after* parsing: a
	// computed constructor's name expression, whose string result is resolved
	// against the statically known namespaces (§3.9.3.1). Those are the ones
	// in scope at the constructor, which inside a direct constructor includes
	// that element's own xmlns declarations — and by then the evaluation
	// context holds only the module's, the declarations having gone out of
	// scope with the parser that made them.
	sc *staticContext
	// ops are XQuery-only primaries lifted out of src so that xpath could
	// compile the rest. Each one's value is bound to "$local:xq-opN" before
	// compiled is evaluated. See substituteOperands.
	ops []node
}

// bind returns the context compiled must be evaluated in, with every lifted
// operand's value bound to the variable that replaced it.
//
// It is the ordinary context wherever nothing was lifted, which is the common
// case; the loop only runs for an expression compileExpr had to rewrite.
func (e *compiledExpr) bind(ctx *evalContext) (*xpath.Context, error) {
	xp := ctx.xp
	for i, op := range e.ops {
		v, err := (&enclosed{items: []node{op}}).sequence(ctx)
		if err != nil {
			return nil, err
		}
		xp = xp.WithVar(xdm.QName{URI: nsLocal, Local: opVar(i)}, v)
	}
	return xp, nil
}

// eval evaluates the expression, whichever half of it is set.
func (e *compiledExpr) eval(ctx *evalContext) (xdm.Sequence, error) {
	if e.items != nil {
		seq, err := evalItems(e.items, ctx)
		if err != nil {
			return nil, err
		}
		return applyCheck(e.check, seq, ctx)
	}
	xp, err := e.bind(ctx)
	if err != nil {
		return nil, err
	}
	seq, err := e.compiled.Eval(xp)
	if e.typed {
		err = retypeError(err)
	}
	return seq, err
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) lookingAt(s string) bool {
	return strings.HasPrefix(p.src[p.pos:], s)
}

func (p *parser) consume(s string) bool {
	if p.lookingAt(s) {
		p.pos += len(s)
		return true
	}
	return false
}

// skipSpace consumes XML whitespace and reports whether any was there.
func (p *parser) skipSpace() bool {
	start := p.pos
	for !p.eof() {
		switch p.src[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return p.pos > start
		}
	}
	return p.pos > start
}

func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("%s (at offset %d)", fmt.Sprintf(format, args...), p.pos)
}

// scanNCName reads an NCName, returning "" if there is not one here.
func (p *parser) scanNCName() string {
	start := p.pos
	first := true
	for !p.eof() {
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		if first {
			if !xdm.IsNameStartChar(r) || r == ':' {
				break
			}
			first = false
		} else if !xdm.IsNameChar(r) || r == ':' {
			break
		}
		p.pos += size
	}
	return p.src[start:p.pos]
}

// parseQName reads a QName as written, without resolving it. Resolution is a
// separate step because in a start tag it cannot happen until the whole
// attribute list has been seen.
func (p *parser) parseQName() (prefix, local string, err error) {
	first := p.scanNCName()
	if first == "" {
		return "", "", p.errorf("XPST0003: expected a name")
	}
	// "::" is the axis separator and ":=" is the assignment operator, and
	// both are single tokens the lexical rules match before QName's colon.
	// A "let $array:= ..." binds $array and assigns; without the second test
	// the name eats the colon and the "=" is asked to be a local name.
	if !p.lookingAt(":") || p.lookingAt("::") || p.lookingAt(":=") {
		return "", first, nil
	}
	p.pos++
	second := p.scanNCName()
	if second == "" {
		return "", "", p.errorf("XPST0003: expected a local name after %q", ":")
	}
	return first, second, nil
}

// rawPart is one run of an attribute value: either literal text, or the source
// of an enclosed expression.
type rawPart struct {
	text     string
	enclosed bool
}

// rawAttribute is an attribute as written, before its name is resolved.
type rawAttribute struct {
	prefix, local string
	value         []rawPart
}

// scanAttributes reads a start tag's attribute list and its closing ">" or
// "/>", without resolving any name.
//
// Names are left unresolved because a namespace declaration later in the list
// governs how names earlier in it resolve — including the element's own.
func (p *parser) scanAttributes() (attrs []rawAttribute, selfClosing bool, err error) {
	for {
		hadSpace := p.skipSpace()
		switch {
		case p.consume("/>"):
			return attrs, true, nil
		case p.consume(">"):
			return attrs, false, nil
		case p.eof():
			return nil, false, p.errorf("XPST0003: unterminated start tag")
		}
		// XML requires whitespace between attributes, and between the name
		// and the first attribute.
		if !hadSpace {
			return nil, false, p.errorf(
				"XPST0003: expected space before an attribute")
		}
		prefix, local, err := p.parseQName()
		if err != nil {
			return nil, false, err
		}
		p.skipSpace()
		if !p.consume("=") {
			return nil, false, p.errorf("XPST0003: expected %q after %q",
				"=", qnameText(prefix, local))
		}
		p.skipSpace()
		value, err := p.scanAttributeValue()
		if err != nil {
			return nil, false, err
		}
		attrs = append(attrs, rawAttribute{prefix: prefix, local: local,
			value: value})
	}
}

// scanAttributeValue reads a quoted attribute value into its literal and
// enclosed parts.
//
// The escapes are XQuery's, not XML's: a doubled quote of the kind that opened
// the value stands for one, and doubled braces stand for one brace. Character
// and entity references are expanded here, and the result is normalised the
// way XML normalises a CDATA attribute — every tab, carriage return and line
// feed becomes a space, with no trimming, because the value is not of a type
// that would justify it.
func (p *parser) scanAttributeValue() ([]rawPart, error) {
	if p.eof() {
		return nil, p.errorf("XPST0003: expected an attribute value")
	}
	quote := p.src[p.pos]
	if quote != '"' && quote != '\'' {
		return nil, p.errorf("XPST0003: an attribute value must be quoted")
	}
	p.pos++

	var parts []rawPart
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			parts = append(parts, rawPart{text: normalizeAttr(run.String())})
			run.Reset()
		}
	}
	for {
		if p.eof() {
			return nil, p.errorf("XPST0003: unterminated attribute value")
		}
		c := p.src[p.pos]
		switch {
		case c == quote:
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == quote {
				// A doubled quote stands for one.
				run.WriteByte(quote)
				p.pos += 2
				continue
			}
			p.pos++
			flush()
			return parts, nil

		case p.lookingAt("{{"):
			run.WriteByte('{')
			p.pos += 2

		case p.lookingAt("}}"):
			run.WriteByte('}')
			p.pos += 2

		case c == '}':
			return nil, p.errorf(
				"XPST0003: %q must be written %q in an attribute value",
				"}", "}}")

		case c == '{':
			flush()
			end, err := findEnclosed(p.src, p.pos)
			if err != nil {
				return nil, err
			}
			parts = append(parts, rawPart{text: p.src[p.pos+1 : end],
				enclosed: true})
			p.pos = end + 1

		case c == '&':
			text, err := p.parseReference()
			if err != nil {
				return nil, err
			}
			run.WriteString(text)

		case c == '<':
			return nil, p.errorf(
				"XPST0003: %q is not allowed in an attribute value", "<")

		default:
			run.WriteByte(c)
			p.pos++
		}
	}
}

// normalizeAttr applies XML attribute-value normalisation for a CDATA-typed
// attribute: every whitespace character becomes a space, and nothing is
// trimmed or collapsed.
func normalizeAttr(s string) string {
	if !strings.ContainsAny(s, "\t\r\n") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\t', '\n':
			sb.WriteByte(' ')
		case '\r':
			sb.WriteByte(' ')
			// A CRLF pair normalises to one space, not two.
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
		default:
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

// predefined are the five entity references XML defines and XQuery inherits.
// A query has no DTD, so these are the only named ones it may use.
var predefined = map[string]string{
	"lt": "<", "gt": ">", "amp": "&", "quot": "\"", "apos": "'",
}

// parseReference expands a character or predefined entity reference.
func (p *parser) parseReference() (string, error) {
	start := p.pos
	if !p.consume("&") {
		return "", p.errorf("XPST0003: expected %q", "&")
	}
	if p.consume("#") {
		var digits string
		base := 10
		if p.consume("x") {
			base = 16
			digits = p.scanWhile(isHexDigit)
		} else {
			digits = p.scanWhile(isDigit)
		}
		if digits == "" || !p.consume(";") {
			return "", p.errorAt(start, "XPST0003: malformed character reference")
		}
		// The value is read into an int64 and range-checked before the
		// conversion to rune, which is an int32: "&#4294967542;" and its hex
		// spelling are codepoints XML does not have, and truncating them to
		// 32 bits would turn each into a character it is not. Overflowing
		// even the int64 is the same answer, so a parse error joins the
		// range failure rather than being reported apart from it.
		n, err := strconv.ParseInt(digits, base, 64)
		if err != nil || n > 0x10FFFF || !isXMLChar(rune(n)) {
			return "", p.errorAt(start,
				"XQST0090: %q is not a valid XML character", "&#"+digits+";")
		}
		return string(rune(n)), nil
	}
	name := p.scanNCName()
	if name == "" || !p.consume(";") {
		return "", p.errorAt(start, "XPST0003: malformed entity reference")
	}
	text, ok := predefined[name]
	if !ok {
		return "", p.errorAt(start,
			"XPST0003: %q is not a predefined entity reference", "&"+name+";")
	}
	return text, nil
}

func (p *parser) scanWhile(ok func(byte) bool) string {
	start := p.pos
	for !p.eof() && ok(p.src[p.pos]) {
		p.pos++
	}
	return p.src[start:p.pos]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// isXMLChar reports whether a codepoint may appear in an XML 1.0 document.
func isXMLChar(r rune) bool {
	switch {
	case r == 0x9 || r == 0xA || r == 0xD:
		return true
	case r >= 0x20 && r <= 0xD7FF:
		return true
	case r >= 0xE000 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0x10FFFF:
		return true
	}
	return false
}

// compileExpr hands an expression to xpath.
//
// Three things cannot simply be delegated. The namespace axis: XPath has it
// and XQuery does not, so a query using it must be refused even though the
// expression parser beneath would accept it. The annotations XQuery admits
// before an inline function and inside a function test, which XPath has no
// syntax for at all — see stripAnnotations. And the references XQuery's
// StringLiteral admits and XPath's does not, which are expanded into the
// source first — see literal.go. Everything else about the expression language
// is shared, which is why these are the only three.
func (p *parser) compileExpr(src string) (*compiledExpr, error) {
	if err := rejectNamespaceAxis(src); err != nil {
		return nil, err
	}
	// %public and %private have to be refused before the strip removes them.
	// stripAnnotations exists because xpath has no syntax for an annotation
	// and the value of one is implementation-defined; these two are the
	// exception §4.18 makes, and once stripped there is nothing left to
	// object to.
	if err := rejectAnnotatedInlineFunction(src); err != nil {
		return nil, err
	}
	stripped, err := p.stripAnnotations(src)
	if err != nil {
		return nil, err
	}
	expanded, err := p.expandStringLiterals(stripped)
	if err != nil {
		return nil, err
	}
	var opsOut []node
	c, err := xpath.CompileVersion(expanded, p.sc, p.version)
	if err != nil {
		// The source handed here is XQuery this parser has already rewritten
		// around — the trailing half of "(...)/S", a call's rewritten
		// argument list — and the rewriting only lifted out the primary it
		// was aimed at. An XQuery-only primary further in is still there:
		// "<e/>/(a union text {()})" leaves a computed constructor as the
		// operand of "union", which is exactly the shape operand.go lifts.
		// Substituting those and recompiling is the same split applied once
		// more, so it is tried before the error is reported. Failing that,
		// xpath's own error stands, because it names the construct.
		ops, rewritten, serr := p.substituteOperands(expanded)
		if serr != nil || len(ops) == 0 {
			return nil, err
		}
		sub, serr := xpath.CompileVersion(rewritten, p.sc, p.version)
		if serr != nil {
			return nil, err
		}
		c, opsOut = sub, ops
	}
	// The static base URI and the default collation are properties of the
	// expression, not of the evaluation, and xpath models them that way. They
	// are stamped here rather than on the context because the prolog that set
	// them belongs to this module, and a caller who evaluates the query with
	// their own context must not thereby change what "declare default
	// collation" meant.
	if p.sc.baseURI != "" {
		c = c.WithStaticBaseURI(p.sc.baseURI)
	}
	if p.sc.defaultCollation != "" {
		coll, err := xpath.ResolveCollation(p.sc.defaultCollation)
		if err != nil {
			return nil, err
		}
		c = c.WithDefaultCollation(coll)
	}
	return &compiledExpr{src: src, compiled: c, sc: p.sc, ops: opsOut}, nil
}

// rejectNamespaceAxis refuses the namespace axis, which XQuery does not have.
//
// The test is lexical because the expression has not been parsed yet, and it
// deliberately does not fire inside a string literal or a comment, where
// "namespace::" is just text.
func rejectNamespaceAxis(src string) error {
	for i := 0; i+len("namespace::") <= len(src); i++ {
		switch src[i] {
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				// Let the expression parser report it properly.
				return nil
			}
			i = end
			continue
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					return nil
				}
				i = end
				continue
			}
		}
		if !strings.HasPrefix(src[i:], "namespace::") {
			continue
		}
		// A name may end in "namespace" — "xml-namespace::" is not the axis.
		if i > 0 {
			r, _ := utf8.DecodeLastRuneInString(src[:i])
			if xdm.IsNameChar(r) {
				continue
			}
		}
		return fmt.Errorf(
			"XPST0003: XQuery has no %q axis", "namespace")
	}
	return rejectNamespaceNodeStep(src)
}

// rejectNamespaceNodeStep refuses "namespace-node()" written as a step of its
// own, which XQuery 3.0 gives its own error, XQST0134.
//
// The kind test itself is perfectly legal in XQuery and the suite uses it
// freely: "self::namespace-node()", "attribute::namespace-node()", a function
// signature, an "instance of". What XQuery does not have is the namespace
// *axis*, and an unprefixed step takes the child axis, so the only way to read
// a bare "namespace-node()" step is as the axis the language lacks. Every
// legal use names an axis explicitly, which is exactly what distinguishes the
// two, so the test is for the test standing alone after a "/" or at the head
// of the expression.
func rejectNamespaceNodeStep(src string) error {
	const kind = "namespace-node("
	for i := 0; i+len(kind) <= len(src); i++ {
		switch src[i] {
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				return nil
			}
			i = end
			continue
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					return nil
				}
				i = end
				continue
			}
		}
		if !strings.HasPrefix(src[i:], kind) {
			continue
		}
		// What precedes decides it. A name character means the test is the
		// tail of a longer name; a ":" means an axis was named, and an axis
		// makes the use legal. Anything else — a "/", a "(", the start of the
		// expression — leaves the step on the child axis with nothing to say
		// so, which is the case §3.3.2.1 refuses.
		j := len(strings.TrimRight(src[:i], " \t\r\n"))
		if j > 0 {
			if src[j-1] == ':' {
				continue
			}
			if r, _ := utf8.DecodeLastRuneInString(src[:j]); xdm.IsNameChar(r) {
				continue
			}
		}
		return fmt.Errorf(
			"XQST0134: XQuery has no %q axis, so %s) cannot stand as a step",
			"namespace", kind)
	}
	return nil
}

// parseBracedURI reads the "Q{uri}" of an EQName, with p.pos on the "Q".
//
// The URI is a BracedURILiteral [117]: everything up to the closing brace,
// with character and predefined entity references expanded, and then
// whitespace-normalised the way §2.4.1 normalises any URI literal — leading
// and trailing space removed and internal runs collapsed to one. So
// "Q{ }x" and "Q{&#x20;}x" both name x in no namespace, which is what the
// suite's eqname and eqname-entities cases assert.
//
// "{" and "}" may not appear in the URI unescaped: a brace would end the
// literal, so one that is meant literally has to be written as a reference,
// and one that arrives here any other way is a syntax error.
func (p *parser) parseBracedURI() (string, bool, error) {
	if !p.lookingAt("Q{") {
		return "", false, nil
	}
	start := p.pos
	p.pos += 2
	var sb strings.Builder
	for {
		if p.eof() {
			return "", true, p.errorAt(start,
				"XPST0003: unterminated %q", "Q{")
		}
		switch {
		case p.consume("}"):
			return normalizeURILiteral(sb.String()), true, nil
		case p.lookingAt("{"):
			return "", true, p.errorAt(start,
				"XQST0090: %q may not appear in a braced URI literal", "{")
		case p.lookingAt("&"):
			text, err := p.parseReference()
			if err != nil {
				return "", true, err
			}
			sb.WriteString(text)
		default:
			sb.WriteByte(p.src[p.pos])
			p.pos++
		}
	}
}

// normalizeURILiteral applies the whitespace normalisation §2.4.1 gives a URI
// literal: the value is trimmed and every internal run of whitespace becomes a
// single space.
func normalizeURILiteral(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// parseEQNameParts reads an EQName [112]: either "Q{uri}local" or a lexical
// QName.
//
// It returns the URI directly when the name was written in braced form, so a
// caller can skip prefix resolution for a name that has no prefix to resolve.
// The two forms are otherwise interchangeable wherever the grammar admits an
// EQName, which since 3.0 is everywhere a QName was admitted before.
func (p *parser) parseEQNameParts() (prefix, local, uri string, braced bool, err error) {
	if u, ok, err := p.parseBracedURI(); err != nil {
		return "", "", "", true, err
	} else if ok {
		local = p.scanNCName()
		if local == "" {
			return "", "", "", true, p.errorf(
				"XPST0003: expected a local name after %q", "Q{"+u+"}")
		}
		return "", local, u, true, nil
	}
	prefix, local, err = p.parseQName()
	return prefix, local, "", false, err
}

// rejectAnnotatedInlineFunction reports XQST0125 for "%public function(...)"
// and its %private counterpart.
//
// §4.18: "it is a static error if an inline function expression is annotated
// as %public or %private" — the two say where a *declared* function is
// visible, and an inline one is an expression with no declaration to be
// visible from. Every other annotation on an inline function is admitted by
// the grammar and ignored, as it is on a declaration.
//
// The test is lexical because an annotated inline function never reaches a
// parse: xpath's grammar has no annotation, so its lexer refuses the "%" and
// the error has to be recognised before the source is handed over. Only the
// head of the expression is read, which is where an annotation may stand.
func rejectAnnotatedInlineFunction(src string) error {
	rest := strings.TrimLeft(src, " \t\r\n")
	for strings.HasPrefix(rest, "%") {
		name := rest[1:]
		i := 0
		for i < len(name) && (isNameByte(name[i]) || name[i] == ':') {
			i++
		}
		local := name[:i]
		if j := strings.LastIndex(local, ":"); j >= 0 {
			local = local[j+1:]
		}
		rest = strings.TrimLeft(name[i:], " \t\r\n")
		// The error is only about an annotation on an *inline function*, so
		// it needs the "function" that follows the annotation list. A
		// prefixed %eg:public is a vendor annotation that merely ends in the
		// same word, and resolveDeclaredName is what settles that for a
		// declaration; here an unprefixed name is the whole of what §4.18
		// names, since an unprefixed annotation is always in the fn
		// namespace.
		if strings.HasPrefix(rest, "function") &&
			!strings.Contains(name[:i], ":") &&
			(local == "public" || local == "private") {
			return fmt.Errorf(
				"XQST0125: an inline function may not be annotated %%%s", local)
		}
	}
	return nil
}
