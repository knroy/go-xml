package xquery

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// A node is one piece of parsed constructor content.
//
// The tree a constructor produces is not built while parsing: a constructor
// may sit inside a FLWOR that runs it many times, each run producing fresh
// nodes with their own identity, so parsing produces a plan and evaluation
// produces nodes. This is that plan.
type node interface {
	// eval appends what this piece contributes to out.
	eval(out *builderRef, ctx *evalContext) error
}

// literalText is content that was written literally, after entity expansion
// and boundary-whitespace stripping.
type literalText struct{ text string }

// enclosed is an expression in braces, whose value becomes content.
//
// Its body is a query body rather than a bare expression, because a
// constructor may appear inside one: <a>{<b/>}</a> is legal, and the inner
// constructor is not something the expression parser can read.
type enclosed struct {
	expr  *compiledExpr
	items []node

	// braced records that this node is a whole "{ ... }" written in element
	// content, rather than one comma-separated item of one.
	//
	// The two are the same type because "{1,2,3}" parses to a wrapper holding
	// three item nodes, each of which is also an enclosed. Only the wrapper
	// ends an atomic run: the three items are one value and are separated
	// from each other, while the wrapper's value is separated from whatever
	// the next pair of braces yields by nothing at all.
	braced bool
}

// element is a direct or computed element constructor.
type element struct {
	// name is fixed for a direct constructor; nameExpr is set instead for a
	// computed one, whose name is not known until it runs.
	name     xdm.QName
	nameExpr *compiledExpr

	// namespaces are the declarations written on this element. They are
	// applied to the constructed node as namespace nodes, having already
	// been applied to the static context while parsing.
	namespaces []nsBinding

	// inherited are the bindings that namespace declaration attributes on
	// enclosing direct element constructors put in scope, including this
	// element's own. §3.9.1.3 makes them part of the in-scope namespaces of
	// every element constructed within their reach, whether or not the
	// element's own name or attributes use them, so they are carried here and
	// applied at evaluation to whichever of them an ancestor does not already
	// supply.
	inherited map[string]string

	attrs   []attribute
	content []node

	// baseURI is the static base URI in force where the constructor was
	// written, stamped on the constructed element.
	baseURI string
}

// nsBinding is one namespace declaration written on a constructor.
type nsBinding struct{ prefix, uri string }

// attribute is one attribute of a direct or computed constructor. Its value
// is a list because an attribute value alternates literal runs and enclosed
// expressions: id="a{$x}b" is three pieces.
type attribute struct {
	name     xdm.QName
	nameExpr *compiledExpr
	value    []node

	// computed marks an attribute written as "attribute a { ... }", whose
	// value is one enclosed expression, rather than a direct constructor's
	// a="..." whose value is a run of alternating parts. The two join their
	// pieces differently, and only the spelling says which rule applies.
	computed bool
}

// comment, pi and text are the remaining node kinds a constructor can make.
type comment struct{ content []node }

type pi struct {
	target     string
	targetExpr *compiledExpr
	content    []node
}

type textNode struct{ content []node }

// document is "document { ... }".
// document is "document { ... }". Its baseURI is the static base URI in
// force where the constructor was written: §3.9.3.1 gives a constructed
// document node the constructor's base URI, unlike a comment, PI or text
// node, which get none at all.
type document struct {
	content []node
	baseURI string
}

// namespaceNode is "namespace prefix { uri }", §3.9.3.7.
//
// Its name is a prefix rather than a QName — the empty one binds the default
// namespace — so it holds a bare string where the other constructors hold an
// xdm.QName.
type namespaceNode struct {
	prefix     string
	prefixExpr *compiledExpr
	content    []node
}

// parseDirElement parses a direct element constructor, with p.pos on the "<".
//
// The two passes over the attribute list are not an optimisation gone wrong.
// A namespace declaration written on this element governs how the element's
// *own* name resolves, and how its sibling attributes' names resolve — and the
// element name appears before any of them in the source. So the attributes are
// scanned once for xmlns declarations, which are applied to a child static
// context, and only then is anything resolved. BaseX does the same thing by
// rewinding and re-parsing; the shape here is the same discipline written out.
func (p *parser) parseDirElement() (node, error) {
	if !p.consume("<") {
		return nil, p.errorf("XPST0003: expected %q", "<")
	}
	startPos := p.pos
	prefix, local, err := p.parseQName()
	if err != nil {
		return nil, err
	}

	// Pass one: collect the raw attributes without resolving any name.
	raw, selfClosing, err := p.scanAttributes()
	if err != nil {
		return nil, err
	}

	// Apply the namespace declarations among them to a child context. Only
	// now can any QName on this element be resolved.
	inner := p.sc.child()
	var bindings []nsBinding
	seenNS := map[string]bool{}
	for _, a := range raw {
		isDecl, pfx := namespaceDecl(a.prefix, a.local)
		if !isDecl {
			continue
		}
		// The value has to be a compile-time constant: §3.9.1.2 makes an
		// enclosed expression here XQST0022, because the URI is needed
		// before anything that depends on it can be parsed.
		uri, ok := literalValue(a.value)
		if !ok {
			return nil, p.errorAt(startPos,
				"XQST0022: the value of a namespace declaration attribute "+
					"must be a literal")
		}
		if seenNS[pfx] {
			return nil, p.errorAt(startPos,
				"XQST0071: the prefix %q is declared twice on the same element",
				pfx)
		}
		seenNS[pfx] = true
		if uri == "" && pfx != "" {
			// Undeclaring a prefix needs XML Names 1.1; without it this is
			// XQST0085. Undeclaring the *default* namespace is always legal.
			return nil, p.errorAt(startPos,
				"XQST0085: the namespace URI in a namespace declaration "+
					"attribute for prefix %q may not be empty", pfx)
		}
		if pfx == "" {
			// A default namespace declaration binds *no* prefix, and
			// §3.9.1.2 reserves the XML and xmlns namespaces against exactly
			// that: "the following namespace URIs must not be used ... in a
			// namespace declaration attribute: the XML namespace ... and the
			// XMLNS namespace", each being err:XQST0070. The check in bind is
			// phrased per prefix, so the default declaration — which is not a
			// prefix binding and so must not enter sc.ns — is checked here
			// against the same two reserved URIs.
			switch uri {
			case xdm.NSXML:
				return nil, p.errorAt(startPos,
					"XQST0070: only the prefix %q may be bound to %q",
					"xml", xdm.NSXML)
			case xdm.NSXMLNS:
				return nil, p.errorAt(startPos,
					"XQST0070: no prefix may be bound to %q", xdm.NSXMLNS)
			}
			inner.defaultElementNS = uri
		} else if err := inner.bind(pfx, uri); err != nil {
			return nil, p.errorAt(startPos, "%v", err)
		}
		if uri == "" {
			delete(inner.ctorNS, pfx)
		} else {
			inner.ctorNS[pfx] = uri
			// Also recorded module-wide, for the one name resolved after the
			// constructor's context has gone out of scope. See
			// parser.ctorPrefixes.
			if p.ctorPrefixes == nil {
				p.ctorPrefixes = map[string]string{}
			}
			p.ctorPrefixes[pfx] = uri
		}
		bindings = append(bindings, nsBinding{prefix: pfx, uri: uri})
	}

	saved := p.sc
	p.sc = inner
	defer func() { p.sc = saved }()

	name, err := inner.resolveElementName(prefix, local)
	if err != nil {
		return nil, p.errorAt(startPos, "%v", err)
	}

	el := &element{
		name:       name,
		namespaces: bindings,
		inherited:  inner.ctorNS,
		baseURI:    inner.baseURI,
	}

	// Pass two: resolve and compile the attributes that are not namespace
	// declarations.
	seen := map[xdm.QName]bool{}
	for _, a := range raw {
		if isDecl, _ := namespaceDecl(a.prefix, a.local); isDecl {
			continue
		}
		an, err := inner.resolveAttributeName(a.prefix, a.local)
		if err != nil {
			return nil, p.errorAt(startPos, "%v", err)
		}
		key := xdm.QName{URI: an.URI, Local: an.Local}
		if seen[key] {
			return nil, p.errorAt(startPos,
				"XQST0040: the attribute %q appears more than once",
				an.Lexical())
		}
		seen[key] = true
		val, err := p.compileContent(a.value)
		if err != nil {
			return nil, err
		}
		el.attrs = append(el.attrs, attribute{name: an, value: val})
	}

	if selfClosing {
		return el, nil
	}

	content, err := p.parseElementContent(prefix, local)
	if err != nil {
		return nil, err
	}
	el.content = content
	return el, nil
}

// namespaceDecl reports whether an attribute is a namespace declaration, and
// which prefix it declares. xmlns declares the default namespace, written here
// as the empty prefix; xmlns:p declares p.
func namespaceDecl(prefix, local string) (bool, string) {
	switch {
	case prefix == "" && local == "xmlns":
		return true, ""
	case prefix == "xmlns":
		return true, local
	}
	return false, ""
}

// literalValue returns the text of an attribute value that has no enclosed
// expression in it, which is what a namespace declaration requires.
func literalValue(parts []rawPart) (string, bool) {
	var sb strings.Builder
	for _, part := range parts {
		if part.enclosed {
			return "", false
		}
		sb.WriteString(part.text)
	}
	return sb.String(), true
}

// parseElementContent reads to the matching end tag, with p.pos just past the
// start tag's ">".
//
// Boundary whitespace is decided here rather than later. A run of literal text
// that is entirely whitespace, and is bounded on each side by the start or end
// of the content, another constructor, or an enclosed expression, is boundary
// whitespace and is dropped unless the policy is preserve. Text that came from
// a character reference or a CDATA section does not count as whitespace for
// this purpose, so a run containing one is never boundary whitespace however
// it looks — which is why each run carries that flag rather than being
// re-examined afterwards.
func (p *parser) parseElementContent(prefix, local string) ([]node, error) {
	var out []node
	var run strings.Builder
	// literal records that the run so far is only literal whitespace, with
	// nothing in it that came from a reference or a CDATA section.
	literal := true

	flush := func(boundary bool) {
		s := run.String()
		run.Reset()
		if s == "" {
			return
		}
		if boundary && literal && p.sc.boundarySpace == StripSpace &&
			strings.TrimLeft(s, " \t\r\n") == "" {
			literal = true
			return
		}
		out = append(out, &literalText{text: s})
		literal = true
	}

	for {
		if p.eof() {
			return nil, p.errorf("XPST0003: unterminated element %q",
				qnameText(prefix, local))
		}
		switch {
		case p.lookingAt("</"):
			flush(true)
			if err := p.parseEndTag(prefix, local); err != nil {
				return nil, err
			}
			return out, nil

		case p.lookingAt("<!--"):
			flush(true)
			c, err := p.parseDirComment()
			if err != nil {
				return nil, err
			}
			out = append(out, c)

		case p.lookingAt("<?"):
			flush(true)
			n, err := p.parseDirPI()
			if err != nil {
				return nil, err
			}
			out = append(out, n)

		case p.lookingAt("<![CDATA["):
			// CDATA content is literal text, but it is not boundary
			// whitespace however blank it is, so the run it joins is marked
			// as no longer purely literal.
			text, err := p.parseCDATA()
			if err != nil {
				return nil, err
			}
			run.WriteString(text)
			literal = false

		case p.lookingAt("<"):
			flush(true)
			child, err := p.parseDirElement()
			if err != nil {
				return nil, err
			}
			out = append(out, child)

		case p.lookingAt("{{"):
			// A doubled brace is an escaped one, and an escape is not
			// boundary whitespace.
			p.pos += 2
			run.WriteByte('{')
			literal = false

		case p.lookingAt("}}"):
			p.pos += 2
			run.WriteByte('}')
			literal = false

		case p.lookingAt("}"):
			return nil, p.errorf("XPST0003: %q must be written %q in element content",
				"}", "}}")

		case p.lookingAt("{"):
			flush(true)
			e, err := p.parseEnclosed()
			if err != nil {
				return nil, err
			}
			out = append(out, e)

		case p.lookingAt("&"):
			text, err := p.parseReference()
			if err != nil {
				return nil, err
			}
			run.WriteString(text)
			literal = false

		default:
			run.WriteByte(p.src[p.pos])
			p.pos++
		}
	}
}

// parseEndTag consumes "</name S? >" and checks that the name matches.
//
// The comparison is on the name as written, not on the resolved QName:
// XQST0118 is about the lexical form, so a start tag written with one prefix
// and an end tag with another that happens to be bound to the same URI is
// still an error.
func (p *parser) parseEndTag(prefix, local string) error {
	if !p.consume("</") {
		return p.errorf("XPST0003: expected %q", "</")
	}
	ep, el, err := p.parseQName()
	if err != nil {
		return err
	}
	if ep != prefix || el != local {
		return p.errorf(
			"XQST0118: end tag %q does not match start tag %q",
			qnameText(ep, el), qnameText(prefix, local))
	}
	p.skipSpace()
	if !p.consume(">") {
		return p.errorf("XPST0003: expected %q to close the end tag", ">")
	}
	return nil
}

func qnameText(prefix, local string) string {
	if prefix == "" {
		return local
	}
	return prefix + ":" + local
}

// parseDirComment parses "<!-- ... -->".
//
// The content may not contain "--", which XML forbids and XQuery inherits.
func (p *parser) parseDirComment() (node, error) {
	if !p.consume("<!--") {
		return nil, p.errorf("XPST0003: expected %q", "<!--")
	}
	start := p.pos
	for {
		if p.eof() {
			return nil, p.errorf("XPST0003: unterminated comment")
		}
		if p.lookingAt("-->") {
			text := p.src[start:p.pos]
			p.pos += 3
			// [179] DirCommentConstructor is spelled out of
			// DirCommentContents, which admits neither "--" nor a trailing
			// "-", so a direct comment whose content breaks the rule is
			// refused by the grammar and the error is the static XPST0003.
			// XQDY0072 is the dynamic code, and it belongs to the computed
			// constructor, whose content is an expression and is not known
			// until the query runs.
			if strings.Contains(text, "--") || strings.HasSuffix(text, "-") {
				return nil, p.errorAt(start,
					"XPST0003: a comment may not contain %q or end with %q",
					"--", "-")
			}
			return &comment{content: []node{&literalText{text: text}}}, nil
		}
		p.pos++
	}
}

// parseDirPI parses "<?target content?>".
func (p *parser) parseDirPI() (node, error) {
	if !p.consume("<?") {
		return nil, p.errorf("XPST0003: expected %q", "<?")
	}
	start := p.pos
	target := p.scanNCName()
	if target == "" {
		return nil, p.errorAt(start,
			"XPST0003: a processing instruction needs a target")
	}
	if strings.EqualFold(target, "xml") {
		// §3.9.3 forbids "xml" in any case as a PI target, and where the
		// target is written out as a literal name the prohibition is part of
		// the grammar — [180] PITarget excludes it — so the error is the
		// static XPST0003. It is only the *computed* constructor, whose
		// target is not known until the expression is evaluated, that raises
		// the dynamic XQDY0064; Constr-pi-target-1..4 write "<?xml?>" and
		// friends directly and require the static code.
		return nil, p.errorAt(start,
			"XPST0003: %q is not a legal processing-instruction target", target)
	}
	// A target not followed by space runs straight into "?>".
	if !p.lookingAt("?>") {
		if !p.skipSpace() {
			return nil, p.errorf("XPST0003: expected space after the target")
		}
	}
	cstart := p.pos
	for {
		if p.eof() {
			return nil, p.errorf("XPST0003: unterminated processing instruction")
		}
		if p.lookingAt("?>") {
			text := p.src[cstart:p.pos]
			p.pos += 2
			return &pi{target: target,
				content: []node{&literalText{text: text}}}, nil
		}
		p.pos++
	}
}

// parseCDATA parses "<![CDATA[ ... ]]>" and returns its literal text.
func (p *parser) parseCDATA() (string, error) {
	if !p.consume("<![CDATA[") {
		return "", p.errorf("XPST0003: expected %q", "<![CDATA[")
	}
	start := p.pos
	for {
		if p.eof() {
			return "", p.errorf("XPST0003: unterminated CDATA section")
		}
		if p.lookingAt("]]>") {
			text := p.src[start:p.pos]
			p.pos += 3
			return text, nil
		}
		p.pos++
	}
}

// blankEnclosedBody reports whether an enclosed expression's body holds no
// expression: nothing but whitespace and comments.
//
// A comment counts as nothing, and it has to, because "{(:comment:)}" is the
// empty enclosed expression written the long way and Constr-attr-enclexpr-12
// and K2-DirectConElemContent-26b both write it. Trimming whitespace alone
// left the comment behind and sent it to xpath as though it were an
// expression.
func blankEnclosedBody(body string) bool {
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case ' ', '\t', '\r', '\n':
		case '(':
			// Only a comment may stand here. A pragma is not "nothing": an
			// ExtensionExpr is an expression, so "{(#p:x#) {1}}" has a body
			// and this must answer false for it, which the fallthrough to
			// the return below already does.
			if i+1 < len(body) && body[i+1] == ':' {
				end, err := skipComment(body, i)
				if err != nil {
					// An unterminated comment is not a comment; let the
					// parser report it in context.
					return false
				}
				i = end
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}

// parseEnclosed parses "{ Expr? }", handing the expression to xpath.
//
// This is the boundary the whole design turns on: everything between the
// braces is the expression language and is compiled by a parser that is
// already conformant, and nothing of XML syntax reaches it.
func (p *parser) parseEnclosed() (node, error) {
	end, err := findEnclosed(p.src, p.pos)
	if err != nil {
		return nil, err
	}
	body := p.src[p.pos+1 : end]
	p.pos = end + 1
	if blankEnclosedBody(body) {
		// "{}" is an empty sequence, which contributes nothing.
		return &enclosed{braced: true}, nil
	}
	// The body is a query body rather than a bare expression: it may itself
	// hold constructors, as in <a>{<b/>}</a>.
	inner := &parser{src: body, sc: p.sc, version: p.version}
	items, err := inner.parseQueryBody()
	if err != nil {
		return nil, err
	}
	return &enclosed{items: items, braced: true}, nil
}

// compileContent compiles the parts of an attribute value into nodes.
func (p *parser) compileContent(parts []rawPart) ([]node, error) {
	var out []node
	for _, part := range parts {
		if !part.enclosed {
			out = append(out, &literalText{text: part.text})
			continue
		}
		if blankEnclosedBody(part.text) {
			out = append(out, &enclosed{})
			continue
		}
		c, err := p.compileExpr(part.text)
		if err != nil {
			return nil, err
		}
		out = append(out, &enclosed{expr: c})
	}
	return out, nil
}

// errorAt reports an error against a recorded position rather than the
// current one, for a fault discovered after the parser has moved on.
func (p *parser) errorAt(pos int, format string, args ...any) error {
	return fmt.Errorf("%s (at offset %d)", fmt.Sprintf(format, args...), pos)
}
