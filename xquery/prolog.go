package xquery

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// The prolog is XQuery 3.1 §4: everything between the optional version
// declaration and the query body.
//
// Two properties of it shape the code below and are worth stating before the
// parsing, because neither is obvious from the grammar.
//
// The first is that a declaration takes effect on the static context *while
// the prolog is still being read*, not after it. "declare namespace p =
// 'u'; declare variable $x := <p:e/>;" resolves p:e because the namespace
// declaration was applied before the variable's initialiser was parsed. So
// each declaration is applied to p.sc as it is recognised, in source order,
// rather than collected and replayed.
//
// The second cuts the other way and is why variables and functions cannot be
// handled the same way. §4.14 and §4.15 both say that a declared variable or
// function is in scope throughout the *whole* module, in either textual
// direction: "declare variable $a := $b; declare variable $b := 1; $a" is
// legal and gives 1. A prefix is not in scope before its declaration; a
// variable is. That asymmetry is why namespaces are applied eagerly and
// variables are compiled eagerly but *initialised* lazily, in dependency
// order, at evaluation time.

// seen records which of the once-only declarations the prolog has made, so
// that a second one can be reported as the error the specification names.
//
// Each setter has its own code — XQST0068 for boundary-space, XQST0067 for
// construction and so on — so the code is stored with the flag rather than
// being derived from a single generic message.
type seenDecl struct {
	present bool
	code    string
}

// parseProlog reads the whole prolog, applying each declaration to the static
// context as it goes.
//
// It returns with p.pos on the first character of the query body. A module
// with no prolog at all reaches the loop's first iteration, matches nothing,
// and returns immediately.
func (p *parser) parseProlog() error {
	once := map[string]*seenDecl{
		"boundary-space":     {code: "XQST0068"},
		"construction":       {code: "XQST0067"},
		"ordering":           {code: "XQST0065"},
		"empty-order":        {code: "XQST0069"},
		"copy-namespaces":    {code: "XQST0055"},
		"default-collation":  {code: "XQST0038"},
		"base-uri":           {code: "XQST0032"},
		"default-element-ns": {code: "XQST0066"},
		"default-function-ns": {code: "XQST0066"},
		"context-item":       {code: "XQST0099"},
	}
	// §4 divides the prolog into two parts: the setters, imports and
	// namespace declarations come first, and the variable, function and
	// option declarations come second. A setter written after a variable
	// declaration is XQST0099's neighbour XPST0003 in most processors; we
	// track the boundary so that the ordering rule can be enforced where the
	// suite tests it.
	inSecond := false

	for {
		p.skipSpaceAndComments()
		save := p.pos
		if !p.consume("declare") && !p.consume("import") {
			p.pos = save
			return nil
		}
		kw := p.src[save:p.pos]
		if !p.skipSpaceAndComments() && !p.lookingAtDeclKeyword() {
			// "declarefoo" is a name, not a declaration.
			p.pos = save
			return nil
		}
		p.skipSpaceAndComments()

		if kw == "import" {
			// A module or schema import needs a resolver for the imported
			// module's own text, which this package does not yet have. It is
			// refused by name so that the failure says what is missing
			// rather than pointing at a token.
			what := p.peekKeyword()
			p.pos = save
			return p.errorf("XQST0059: %s import is not implemented yet", what)
		}

		what := p.peekKeyword()
		var err error
		switch what {
		case "namespace":
			err = p.parseNamespaceDecl(once, &inSecond)
		case "default":
			err = p.parseDefaultDecl(once, &inSecond)
		case "boundary-space":
			err = p.parseBoundarySpaceDecl(once, &inSecond)
		case "construction":
			err = p.parseConstructionDecl(once, &inSecond)
		case "ordering":
			err = p.parseOrderingDecl(once, &inSecond)
		case "copy-namespaces":
			err = p.parseCopyNamespacesDecl(once, &inSecond)
		case "base-uri":
			err = p.parseBaseURIDecl(once, &inSecond)
		case "context":
			err = p.parseContextItemDecl(once, &inSecond)
		case "variable":
			inSecond = true
			err = p.parseVarDecl()
		case "function", "%":
			inSecond = true
			err = p.parseFunctionDecl()
		case "option":
			inSecond = true
			err = p.parseOptionDecl()
		case "decimal-format":
			err = p.parseDecimalFormatDecl(false)
		case "":
			if p.lookingAt("%") {
				inSecond = true
				err = p.parseFunctionDecl()
				break
			}
			p.pos = save
			return nil
		default:
			// "declare" is not a reserved word: "declare ne gt" is the value
			// comparison of two path expressions, and K2-ExternalVariables-
			// Without-16 asserts it reaches evaluation as one. So a word that
			// begins no declaration ends the prolog rather than failing it,
			// and the body parser gets the whole thing.
			p.pos = save
			return nil
		}
		if err != nil {
			return err
		}
		p.skipSpaceAndComments()
		if !p.consume(";") {
			return p.errorf("XPST0003: expected %q after a declaration", ";")
		}
	}
}

// lookingAtDeclKeyword reports whether the character at p.pos can begin a
// declaration keyword without intervening space.
//
// Only "%" can: an annotated function declaration may be written
// "declare%private function f() {}", though nothing sane does. Every other
// declaration keyword is a name and needs the space.
func (p *parser) lookingAtDeclKeyword() bool {
	return p.lookingAt("%")
}

// markOnce records a once-only declaration and reports the specification's
// error if it has already been made.
func (p *parser) markOnce(once map[string]*seenDecl, key, what string) error {
	s := once[key]
	if s == nil {
		return nil
	}
	if s.present {
		return p.errorf("%s: %s may only be declared once", s.code, what)
	}
	s.present = true
	return nil
}

// noteSetter enforces §4's two-part prolog: a setter, import or namespace
// declaration may not follow a variable, function or option declaration.
func (p *parser) noteSetter(inSecond *bool, what string) error {
	if *inSecond {
		return p.errorf(
			"XPST0003: %s must be declared before any variable or function", what)
	}
	return nil
}

// parseNamespaceDecl reads "declare namespace p = 'uri'" (§4.2).
//
// The binding goes into the static context immediately, because everything
// later in the prolog and in the body resolves against it. Rebinding a prefix
// the prolog has already bound is XQST0033: unlike a constructor's xmlns,
// which is scoped to its element, a prolog binding is module-wide and there is
// no scope for the second one to belong to.
func (p *parser) parseNamespaceDecl(once map[string]*seenDecl, inSecond *bool) error {
	if err := p.noteSetter(inSecond, "a namespace declaration"); err != nil {
		return err
	}
	p.pos += len("namespace")
	p.skipSpaceAndComments()
	prefix := p.scanNCName()
	if prefix == "" {
		return p.errorf("XPST0003: expected a prefix after %q", "declare namespace")
	}
	p.skipSpaceAndComments()
	if !p.consume("=") {
		return p.errorf("XPST0003: expected %q in a namespace declaration", "=")
	}
	p.skipSpaceAndComments()
	uri, err := p.parseURILiteral()
	if err != nil {
		return err
	}
	if p.declaredNS[prefix] {
		return p.errorf("XQST0033: the prefix %q is already bound in this prolog",
			prefix)
	}
	// §4.2: binding a prefix to the zero-length URI *undeclares* it, so the
	// prefix is no longer available — including a predeclared one.
	// "declare namespace xs = ''" makes xs:integer(1) XPST0081 rather than a
	// call to the constructor. It is a removal rather than a binding to "",
	// because a name resolved against the empty URI would silently be in no
	// namespace instead of being an error.
	if uri == "" {
		if prefix == "xml" {
			return p.errorf("XQST0070: the prefix %q may not be undeclared", "xml")
		}
		delete(p.sc.ns, prefix)
		p.declaredNS[prefix] = true
		return nil
	}
	if err := p.sc.bind(prefix, uri); err != nil {
		return err
	}
	p.declaredNS[prefix] = true
	return nil
}

// parseDefaultDecl reads the three declarations that begin "declare default":
// element namespace, function namespace and collation (§4.3, §4.4).
func (p *parser) parseDefaultDecl(once map[string]*seenDecl, inSecond *bool) error {
	if err := p.noteSetter(inSecond, "a default declaration"); err != nil {
		return err
	}
	p.pos += len("default")
	p.skipSpaceAndComments()
	switch kw := p.peekKeyword(); kw {
	case "element", "function":
		p.pos += len(kw)
		p.skipSpaceAndComments()
		if !p.consumeKeyword("namespace") {
			return p.errorf("XPST0003: expected %q after %q", "namespace", kw)
		}
		p.skipSpaceAndComments()
		uri, err := p.parseURILiteral()
		if err != nil {
			return err
		}
		key := "default-element-ns"
		if kw == "function" {
			key = "default-function-ns"
		}
		if err := p.markOnce(once, key,
			"the default "+kw+" namespace"); err != nil {
			return err
		}
		// The reserved-namespace prohibitions of §4.1 apply here as well: a
		// default namespace bound to the xmlns namespace would put every
		// unprefixed name in it.
		if uri == xdm.NSXMLNS || uri == xdm.NSXML {
			return p.errorf("XQST0070: %q may not be a default namespace", uri)
		}
		if kw == "element" {
			p.sc.defaultElementNS = uri
		} else {
			p.sc.defaultFunctionNS = uri
		}
		return nil

	case "decimal-format":
		// "declare default decimal-format ..." (§4.18) declares the format
		// fn:format-number uses when its third argument is absent.
		p.pos += len(kw)
		p.skipSpaceAndComments()
		return p.parseDecimalFormatDecl(true)

	case "order":
		// "declare default order empty greatest|least" (§4.7). It shares the
		// "declare default" opening with the two namespace declarations and
		// diverges only here.
		p.pos += len(kw)
		p.skipSpaceAndComments()
		return p.parseEmptyOrderDecl(once)

	case "collation":
		p.pos += len(kw)
		p.skipSpaceAndComments()
		uri, err := p.parseURILiteral()
		if err != nil {
			return err
		}
		if err := p.markOnce(once, "default-collation",
			"the default collation"); err != nil {
			return err
		}
		// XQST0038 covers both faults this declaration can have: declaring it
		// twice, and naming a collation the processor does not have. Only the
		// codepoint collation and the two the function library implements are
		// available, so anything else is refused here rather than at the
		// first comparison that would have used it.
		if _, err := xpath.ResolveCollation(uri); err != nil {
			return p.errorf("XQST0038: the collation %q is not available", uri)
		}
		p.sc.defaultCollation = uri
		return nil

	default:
		return p.errorf("XPST0003: %q cannot follow %q", kw, "declare default")
	}
}

// parseBoundarySpaceDecl reads "declare boundary-space preserve|strip" (§4.3).
func (p *parser) parseBoundarySpaceDecl(once map[string]*seenDecl, inSecond *bool) error {
	if err := p.noteSetter(inSecond, "boundary-space"); err != nil {
		return err
	}
	p.pos += len("boundary-space")
	p.skipSpaceAndComments()
	if err := p.markOnce(once, "boundary-space", "boundary-space"); err != nil {
		return err
	}
	switch {
	case p.consumeKeyword("preserve"):
		p.sc.boundarySpace = PreserveSpace
	case p.consumeKeyword("strip"):
		p.sc.boundarySpace = StripSpace
	default:
		return p.errorf("XPST0003: expected %q or %q", "preserve", "strip")
	}
	return nil
}

// parseConstructionDecl reads "declare construction preserve|strip" (§4.10).
func (p *parser) parseConstructionDecl(once map[string]*seenDecl, inSecond *bool) error {
	if err := p.noteSetter(inSecond, "construction"); err != nil {
		return err
	}
	p.pos += len("construction")
	p.skipSpaceAndComments()
	if err := p.markOnce(once, "construction", "construction mode"); err != nil {
		return err
	}
	switch {
	case p.consumeKeyword("preserve"):
		p.sc.construction = PreserveTypes
	case p.consumeKeyword("strip"):
		p.sc.construction = StripTypes
	default:
		return p.errorf("XPST0003: expected %q or %q", "preserve", "strip")
	}
	return nil
}

// parseOrderingDecl reads "declare ordering ordered|unordered" (§4.6).
//
// It is recorded and otherwise ignored, which is conformant: unordered mode
// permits a processor to return a sequence in any order, and returning it in
// document order is one of the orders permitted.
func (p *parser) parseOrderingDecl(once map[string]*seenDecl, inSecond *bool) error {
	if err := p.noteSetter(inSecond, "ordering mode"); err != nil {
		return err
	}
	p.pos += len("ordering")
	p.skipSpaceAndComments()
	if err := p.markOnce(once, "ordering", "ordering mode"); err != nil {
		return err
	}
	switch {
	case p.consumeKeyword("ordered"):
		p.sc.ordering = Ordered
	case p.consumeKeyword("unordered"):
		p.sc.ordering = Unordered
	default:
		return p.errorf("XPST0003: expected %q or %q", "ordered", "unordered")
	}
	return nil
}

// parseEmptyOrderDecl reads "declare default order empty greatest|least"
// (§4.7). It is reached through parseDefaultDecl's "order" arm.
func (p *parser) parseEmptyOrderDecl(once map[string]*seenDecl) error {
	if err := p.markOnce(once, "empty-order", "the default empty order"); err != nil {
		return err
	}
	if !p.consumeKeyword("empty") {
		return p.errorf("XPST0003: expected %q after %q", "empty", "default order")
	}
	p.skipSpaceAndComments()
	switch {
	case p.consumeKeyword("greatest"):
		p.sc.emptyOrder = EmptyGreatest
	case p.consumeKeyword("least"):
		p.sc.emptyOrder = EmptyLeast
	default:
		return p.errorf("XPST0003: expected %q or %q", "greatest", "least")
	}
	return nil
}

// parseCopyNamespacesDecl reads "declare copy-namespaces
// preserve|no-preserve, inherit|no-inherit" (§4.8).
//
// The two halves are independent — one says whether namespaces already on a
// copied element survive the copy, the other whether the namespaces in scope
// where the copy is made are added to it — and xdmbuild's Policy already
// models them that way.
func (p *parser) parseCopyNamespacesDecl(once map[string]*seenDecl, inSecond *bool) error {
	if err := p.noteSetter(inSecond, "copy-namespaces"); err != nil {
		return err
	}
	p.pos += len("copy-namespaces")
	p.skipSpaceAndComments()
	if err := p.markOnce(once, "copy-namespaces", "copy-namespaces"); err != nil {
		return err
	}
	switch {
	case p.consumeKeyword("preserve"):
		p.sc.preserveNS = true
	case p.consumeKeyword("no-preserve"):
		p.sc.preserveNS = false
	default:
		return p.errorf("XPST0003: expected %q or %q", "preserve", "no-preserve")
	}
	p.skipSpaceAndComments()
	if !p.consume(",") {
		return p.errorf("XPST0003: expected %q in a copy-namespaces declaration", ",")
	}
	p.skipSpaceAndComments()
	switch {
	case p.consumeKeyword("inherit"):
		p.sc.inheritNS = true
	case p.consumeKeyword("no-inherit"):
		p.sc.inheritNS = false
	default:
		return p.errorf("XPST0003: expected %q or %q", "inherit", "no-inherit")
	}
	return nil
}

// parseBaseURIDecl reads "declare base-uri 'uri'" (§4.5).
//
// The declared value replaces whatever the caller supplied, because the
// query's own statement about its base URI is more specific than the
// environment's. It is what relative references in the query resolve against
// and what is stamped on constructed elements.
func (p *parser) parseBaseURIDecl(once map[string]*seenDecl, inSecond *bool) error {
	if err := p.noteSetter(inSecond, "the base URI"); err != nil {
		return err
	}
	p.pos += len("base-uri")
	p.skipSpaceAndComments()
	uri, err := p.parseURILiteral()
	if err != nil {
		return err
	}
	if err := p.markOnce(once, "base-uri", "the base URI"); err != nil {
		return err
	}
	// A relative declared base URI resolves against the one already in force,
	// which is what "declare base-uri '../'" in a module loaded from a
	// directory is asking for.
	p.sc.baseURI = resolveBase(p.sc.baseURI, uri)
	return nil
}

// parseContextItemDecl reads "declare context item [as type] (:= expr|external
// [:= expr])" (§4.16).
//
// An external context item declaration with a default is the interesting
// case: the caller's item wins when there is one, and the default is
// evaluated only when there is not.
func (p *parser) parseContextItemDecl(once map[string]*seenDecl, inSecond *bool) error {
	p.pos += len("context")
	p.skipSpaceAndComments()
	if !p.consumeKeyword("item") {
		return p.errorf("XPST0003: expected %q after %q", "item", "declare context")
	}
	p.skipSpaceAndComments()
	if err := p.markOnce(once, "context-item", "the context item"); err != nil {
		return err
	}
	decl := &contextItemDecl{}
	if p.consumeKeyword("as") {
		p.skipSpaceAndComments()
		t, err := p.parseSequenceType()
		if err != nil {
			return err
		}
		decl.typ = t
		p.skipSpaceAndComments()
	}
	switch {
	case p.consume(":="):
		p.skipSpaceAndComments()
		src, err := p.scanDeclExpr()
		if err != nil {
			return err
		}
		decl.init, err = p.compileExpr(src)
		if err != nil {
			return err
		}
	case p.consumeKeyword("external"):
		decl.external = true
		p.skipSpaceAndComments()
		if p.consume(":=") {
			p.skipSpaceAndComments()
			src, err := p.scanDeclExpr()
			if err != nil {
				return err
			}
			decl.init, err = p.compileExpr(src)
			if err != nil {
				return err
			}
		}
	default:
		return p.errorf("XPST0003: expected %q or %q in a context item declaration",
			":=", "external")
	}
	p.contextItem = decl
	return nil
}

// nsSerialization is the namespace whose options are serialization
// parameters. §4.19 leaves every other option namespace to the processor;
// this one is given a meaning by the Serialization specification, which
// XQuery 3.1 §2.3.4 adopts by name.
const nsSerialization = "http://www.w3.org/2010/xslt-xquery-serialization"

// serializationParams is the set of names the serialization namespace
// defines, from Serialization 3.1 §3. build-tree is absent deliberately: it
// is an XSLT parameter, and XQuery has no raw result tree for it to describe.
var serializationParams = map[string]bool{
	"allow-duplicate-names":   true,
	"byte-order-mark":         true,
	"cdata-section-elements":  true,
	"doctype-public":          true,
	"doctype-system":          true,
	"encoding":                true,
	"escape-uri-attributes":   true,
	"html-version":            true,
	"include-content-type":    true,
	"indent":                  true,
	"item-separator":          true,
	"json-node-output-method": true,
	"media-type":              true,
	"method":                  true,
	"normalization-form":      true,
	"omit-xml-declaration":    true,
	"parameter-document":      true,
	"standalone":              true,
	"suppress-indentation":    true,
	"undeclare-prefixes":      true,
	"use-character-maps":      true,
	"version":                 true,
}

// parseOptionDecl reads "declare option p:name 'value'" (§4.19).
//
// An option in a namespace the processor does not recognise must be ignored,
// which is the whole of the required behaviour for those. The name is still
// resolved, because an option with an unbound prefix is XPST0081 whether or
// not the option itself means anything.
//
// An option in the serialization namespace is kept rather than dropped: it is
// how a main module states the serialization parameters its result is to be
// written with, and a host that serialises the result has nowhere else to
// read them from. Nothing here interprets them — the value is the parameter's
// lexical form and stays that way until a serialiser asks for it — so an
// option naming a parameter this engine does not implement costs nothing at
// compile time and is reported by whoever tries to use it.
func (p *parser) parseOptionDecl() error {
	p.pos += len("option")
	p.skipSpaceAndComments()
	prefix, local, err := p.parseEQName()
	if err != nil {
		return err
	}
	// §4.19: the name must be prefixed. An unprefixed option name would take
	// the default element namespace, which is not what an option name means.
	if prefix == "" && !strings.HasPrefix(local, "Q{") {
		return p.errorf("XPST0081: an option name must have a prefix")
	}
	name, err := p.sc.resolveElementName(prefix, local)
	if err != nil && prefix != "" {
		return err
	}
	p.skipSpaceAndComments()
	val, err := p.parseURILiteral()
	if err != nil {
		return err
	}
	if name.URI == nsSerialization {
		// §2.2.4: "It is a static error [err:XQST0109] if the local name of
		// an output declaration in the ... serialization namespace is not
		// one of the serialization parameter names." An unknown name here is
		// not an extension the way an unknown *namespace* is: the namespace
		// is the Serialization specification's own, and every name in it is
		// spoken for.
		if !serializationParams[name.Local] {
			return p.errorf(
				"XQST0109: %q is not a serialization parameter", name.Local)
		}
		if p.serialization == nil {
			p.serialization = map[string]string{}
		}
		// A repeated parameter is XQST0110. The rule is worth enforcing
		// rather than letting the last one win: two "declare option
		// output:method" lines in one prolog are a query that does not know
		// what it wants, and silently honouring the second hides that.
		if _, dup := p.serialization[name.Local]; dup {
			return p.errorf(
				"XQST0110: the serialization parameter %q is declared twice",
				name.Local)
		}
		switch name.Local {
		case "cdata-section-elements", "suppress-indentation":
			// These two carry a list of lexical QNames, and a lexical QName
			// means nothing away from the namespace bindings it was written
			// under. The prolog is the only place those bindings exist — the
			// value is a string literal, not markup, so it carries no
			// namespace nodes of its own and a consumer handed the raw text
			// would have to be handed the prolog's namespaces with it.
			// Expanding here, where the static context is already in hand,
			// is what makes the value self-describing.
			expanded, err := p.expandNameList(val)
			if err != nil {
				return err
			}
			val = expanded
		}
		p.serialization[name.Local] = val
	}
	return nil
}

// expandNameList rewrites a whitespace-separated list of lexical QNames into
// the same list in EQName notation, resolving each prefix against the
// prolog's in-scope namespaces.
//
// An unprefixed name in these two parameters takes the default element
// namespace, as an element name does: both name elements, and
// cdata-section-elements="para" in a query whose default element namespace is
// the DocBook one means that namespace's para. §2.2.4 says the value is
// interpreted "as if it were an attribute of the same name on an xsl:output
// declaration", and that is the xsl:output rule.
func (p *parser) expandNameList(val string) (string, error) {
	names := strings.Fields(val)
	out := make([]string, 0, len(names))
	for _, n := range names {
		// An EQName is already expanded; it is a legal spelling here and
		// resolving it again would try to read "Q{...}local" as a prefix.
		if strings.HasPrefix(n, "Q{") {
			out = append(out, n)
			continue
		}
		prefix, local := "", n
		if i := strings.IndexByte(n, ':'); i >= 0 {
			prefix, local = n[:i], n[i+1:]
		}
		q, err := p.sc.resolveElementName(prefix, local)
		if err != nil {
			return "", err
		}
		out = append(out, "Q{"+q.URI+"}"+q.Local)
	}
	return strings.Join(out, " "), nil
}

// parseURILiteral reads a string literal and applies the escapes a URI
// literal admits.
//
// §4.2's URILiteral is a StringLiteral, which in XQuery — unlike XPath — may
// contain character and predefined entity references. So "&#x2f;" in a
// namespace URI is a solidus, and a query is entitled to write one.
func (p *parser) parseURILiteral() (string, error) {
	if p.eof() {
		return "", p.errorf("XPST0003: expected a string literal")
	}
	quote := p.src[p.pos]
	if quote != '"' && quote != '\'' {
		return "", p.errorf("XPST0003: expected a string literal")
	}
	p.pos++
	var sb strings.Builder
	for {
		if p.eof() {
			return "", p.errorf("XPST0003: unterminated string literal")
		}
		c := p.src[p.pos]
		switch {
		case c == quote:
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == quote {
				sb.WriteByte(quote)
				p.pos += 2
				continue
			}
			p.pos++
			return sb.String(), nil
		case c == '&':
			text, err := p.parseReference()
			if err != nil {
				return "", err
			}
			sb.WriteString(text)
		default:
			sb.WriteByte(c)
			p.pos++
		}
	}
}

// scanDeclExpr takes the source of an ExprSingle up to the ";" that ends the
// declaration.
//
// The terminator cannot be found by searching for the first ";": a semicolon
// may appear inside a string literal, inside an entity reference in a direct
// constructor, and inside a comment. So the scan is the same bracket-,
// string- and comment-aware walk parseExprItem does, stopping at a top-level
// ";" instead of a top-level ",".
//
// A "," at depth zero is *not* a terminator here, because an initialiser is an
// ExprSingle in the grammar but every processor and the suite accept a
// sequence: "declare variable $x := 1, 2;" is not written, but
// "declare variable $x := (1, 2);" is, and the parenthesis puts the comma at
// depth one either way.
func (p *parser) scanDeclExpr() (string, error) {
	start := p.pos
	depth := 0
	for !p.eof() {
		switch p.src[p.pos] {
		case '\'', '"':
			end, err := skipString(p.src, p.pos)
			if err != nil {
				return "", err
			}
			p.pos = end + 1
			continue
		case '(':
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == ':' {
				end, err := skipComment(p.src, p.pos)
				if err != nil {
					return "", err
				}
				p.pos = end + 1
				continue
			}
			depth++
		case ')':
			depth--
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case '<':
			// A direct constructor's content is not expression syntax, and a
			// ";" inside it — in an entity reference, in text, in an
			// attribute — is not a terminator. Skipping the whole
			// constructor is the only reliable way past it, and the
			// constructor parser is the only thing that knows where it ends.
			// skipDirConstructor advances past the constructor itself.
			if err := p.skipDirConstructor(); err == nil {
				continue
			}
		case ';':
			if depth == 0 {
				goto done
			}
		}
		p.pos++
	}
done:
	src := strings.TrimSpace(p.src[start:p.pos])
	if src == "" {
		return "", p.errorf("XPST0003: expected an expression")
	}
	return src, nil
}


// parseEQName reads an EQName: a QName or a braced URI literal with a local
// name, "Q{http://example.com/}local".
//
// The braced form is returned with the prefix empty and the whole "Q{...}"
// spelling in the local part, which resolveDeclaredName unpacks. Keeping it
// as written rather than splitting here means one place decides what a name
// in each position resolves to.
func (p *parser) parseEQName() (prefix, local string, err error) {
	if p.lookingAt("Q{") {
		start := p.pos
		end := strings.IndexByte(p.src[p.pos:], '}')
		if end < 0 {
			return "", "", p.errorf("XPST0003: unterminated %q", "Q{")
		}
		p.pos += end + 1
		name := p.scanNCName()
		if name == "" {
			return "", "", p.errorf("XPST0003: expected a local name after %q", "}")
		}
		return "", p.src[start:p.pos], nil
	}
	return p.parseQName()
}

// resolveDeclaredName resolves the name of a declared variable or function.
//
// The default element namespace does not apply — §4.14 and §4.15 both put an
// unprefixed declared name in no namespace, and a function's in the default
// *function* namespace — so this cannot go through resolveElementName.
func (p *parser) resolveDeclaredName(prefix, local string, isFunction bool) (xdm.QName, error) {
	if strings.HasPrefix(local, "Q{") {
		end := strings.IndexByte(local, '}')
		return xdm.QName{URI: local[2:end], Local: local[end+1:]}, nil
	}
	if prefix == "" {
		if isFunction {
			return xdm.QName{URI: p.sc.defaultFunctionNS, Local: local}, nil
		}
		return xdm.QName{Local: local}, nil
	}
	uri, ok := p.sc.ns[prefix]
	if !ok {
		return xdm.QName{}, p.errorf(
			"XPST0081: the prefix %q is not bound to a namespace", prefix)
	}
	return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
}

// parseVersionDecl reads "xquery version '3.1' [encoding 'utf-8'];" or
// "xquery encoding 'utf-8';" (§4.1).
//
// It must be the first thing in the module, and it is not part of the prolog
// proper. A version this processor does not implement is XQST0031, which is
// the specification's way of saying "this query was written for a language I
// do not have" rather than a syntax error.
func (p *parser) parseVersionDecl() error {
	p.skipSpaceAndComments()
	save := p.pos
	if !p.consumeKeyword("xquery") {
		return nil
	}
	p.skipSpaceAndComments()
	switch {
	case p.consumeKeyword("version"):
		p.skipSpaceAndComments()
		v, err := p.parseURILiteral()
		if err != nil {
			return err
		}
		switch v {
		case "1.0", "3.0", "3.1":
		default:
			return p.errorf("XQST0031: this processor does not implement "+
				"XQuery version %q", v)
		}
		p.skipSpaceAndComments()
		if p.consumeKeyword("encoding") {
			p.skipSpaceAndComments()
			enc, err := p.parseURILiteral()
			if err != nil {
				return err
			}
			if err := checkEncodingName(enc); err != nil {
				return err
			}
		}
	case p.consumeKeyword("encoding"):
		// 3.0 added the encoding-only form. There is nothing to do with the
		// name but check it: the source has already been decoded by the time
		// it reaches this package, and re-decoding it would be wrong.
		p.skipSpaceAndComments()
		enc, err := p.parseURILiteral()
		if err != nil {
			return err
		}
		if err := checkEncodingName(enc); err != nil {
			return err
		}
	default:
		// "xquery" is a legal name, and a body that begins with one is not a
		// version declaration.
		p.pos = save
		return nil
	}
	p.skipSpaceAndComments()
	if !p.consume(";") {
		return p.errorf("XPST0003: expected %q after the version declaration", ";")
	}
	return nil
}

// checkEncodingName applies §4.1's lexical rule for an encoding name, which is
// XML's: a letter followed by letters, digits, periods, hyphens and
// underscores. XQST0087 is the error for anything else.
func checkEncodingName(enc string) error {
	ok := enc != ""
	for i := 0; i < len(enc) && ok; i++ {
		c := enc[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_'):
		default:
			ok = false
		}
	}
	if !ok {
		return fmt.Errorf("XQST0087: %q is not a valid encoding name", enc)
	}
	return nil
}

// resolveBase resolves a possibly-relative declared base URI against the one
// already in force.
func resolveBase(base, decl string) string {
	if base == "" || decl == "" {
		return decl
	}
	b, err := url.Parse(base)
	if err != nil {
		return decl
	}
	r, err := url.Parse(decl)
	if err != nil {
		return decl
	}
	return b.ResolveReference(r).String()
}
