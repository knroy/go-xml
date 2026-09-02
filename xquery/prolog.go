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
		"boundary-space":      {code: "XQST0068"},
		"construction":        {code: "XQST0067"},
		"ordering":            {code: "XQST0065"},
		"empty-order":         {code: "XQST0069"},
		"copy-namespaces":     {code: "XQST0055"},
		"default-collation":   {code: "XQST0038"},
		"base-uri":            {code: "XQST0032"},
		"default-element-ns":  {code: "XQST0066"},
		"default-function-ns": {code: "XQST0066"},
		"context-item":        {code: "XQST0099"},
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
			what := p.peekKeyword()
			if what != "module" && what != "schema" {
				// §4.11 admits only "import schema" and "import module".
				// Anything else means this "import" was never a declaration:
				// "import ne import" is a comparison of two element name
				// tests, and belongs to the query body. Refusing it here as a
				// malformed import robbed it of the XPDY0002 it owes for an
				// absent context item.
				p.pos = save
				return nil
			}
			// The faults an import's own syntax settles are reported before
			// the resolver is missed. They are decided by the declaration as
			// written, so a processor that cannot fetch the target still owes
			// the right error: XQST0059 says "no such module", which is a
			// different complaint from a prefix that may not be bound at all.
			if err := p.checkImportSyntax(what); err != nil {
				return err
			}
			// A module or schema import needs a resolver for the imported
			// module's own text, which this package does not yet have. It is
			// refused by name so that the failure says what is missing
			// rather than pointing at a token.
			p.pos = save
			return p.errorf("XQST0059: %s import is not implemented yet", what)
		}

		what := p.peekKeyword()
		var err error
		if what == "%" || (what == "" && p.lookingAt("%")) {
			// §4 AnnotatedDecl ::= "declare" Annotation* (VarDecl |
			// FunctionDecl): the annotations bind to *either* declaration, so
			// what follows them decides which one this is. Routing every
			// annotated declaration to parseFunctionDecl made
			// "declare %eg:sequential variable $foo := 'bar'" fail with
			// "expected function", though the grammar admits it and the
			// annotation is one nothing here interprets.
			var private, conflict bool
			if private, conflict, err = p.parseAnnotationList(); err == nil {
				p.skipSpaceAndComments()
				inSecond = true
				isVar := p.peekKeyword() == "variable"
				// §4.15 admits at most one of %public and %private on a
				// declaration, so %public %public breaks the rule as surely
				// as %public %private. The code names the declaration rather
				// than the annotation — XQST0106 for a function, XQST0116 for
				// a variable — which is why the conflict is reported here and
				// not where it is detected: the keyword that settles which
				// one this is has only just been read.
				switch {
				case conflict && isVar:
					err = p.errorf("XQST0116: a variable declaration may "+
						"carry at most one of %s and %s", "%public", "%private")
				case conflict:
					err = p.errorf("XQST0106: a function declaration may "+
						"carry at most one of %s and %s", "%public", "%private")
				case isVar:
					err = p.parseVarDecl()
				default:
					err = p.parseFunctionDeclBody(private)
				}
			}
			if err != nil {
				return err
			}
			p.skipSpaceAndComments()
			if !p.consume(";") {
				return p.errorf("XPST0003: expected %q after a declaration", ";")
			}
			continue
		}
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
		case "function":
			inSecond = true
			err = p.parseFunctionDecl()
		case "option":
			inSecond = true
			err = p.parseOptionDecl()
		case "decimal-format":
			err = p.parseDecimalFormatDecl(false)
		case "":
			// An annotation is handled above, so nothing that begins no
			// keyword can start a declaration here.
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
	// §4.2 forbids a prolog from saying anything at all about xml or xmlns:
	// "it is a static error if the prefix is xml or xmlns". That is stricter
	// than the rule bind applies, which allows xml to be bound to its own
	// namespace because a constructor's xmlns:xml="…/XML/1998/namespace" is
	// legal — an XML parser would have accepted it. A prolog has no such
	// excuse, so both prefixes are refused here whatever the URI, including
	// the zero-length one that would otherwise be read as an undeclaration.
	// namespaceDecl-3 declares xml to its own correct URI and K2-Namespace-
	// Prolog-7 undeclares xmlns; both are XQST0070.
	if prefix == "xml" || prefix == "xmlns" {
		return p.errorf("XQST0070: the prefix %q may not be declared in a "+
			"prolog", prefix)
	}
	if uri == "" {
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
		// The grammar gives ContextItemDecl an ItemType, not a SequenceType:
		// the context item is one item, so an occurrence indicator has nothing
		// to quantify and "declare context item as xs:integer+" is a syntax
		// error rather than a type error. contextDecl-023 asserts XPST0003 for
		// exactly that. parseSequenceType is the only parser available here
		// and it accepts the indicator, so it is rejected after the fact.
		if src := strings.TrimSpace(t.src); src != "" {
			switch src[len(src)-1] {
			case '+', '*', '?':
				return p.errorf(
					"XPST0003: the context item type %q may not carry an "+
						"occurrence indicator", src)
			}
		}
		decl.typ = t
		p.skipSpaceAndComments()
	}
	switch {
	case p.consume(":="):
		p.skipSpaceAndComments()
		var err error
		// parseDeclBody rather than compileExpr, for the reason a variable
		// declaration uses it: "declare context item := <a>bananas</a>" has a
		// constructor for an initialiser, and a constructor cannot be handed
		// to xpath. compileExpr alone lifts it to a "local:xq-stepN()" it then
		// never binds, so the query fails with XPST0008 naming a variable the
		// parser invented. §4.16 puts no restriction on the initialiser that
		// §4.14 does not put on a variable's, so the two read it the same way.
		if decl.init, decl.body, err = p.parseDeclBody(); err != nil {
			return err
		}
	case p.consumeKeyword("external"):
		decl.external = true
		p.skipSpaceAndComments()
		if p.consume(":=") {
			p.skipSpaceAndComments()
			var err error
			if decl.init, decl.body, err = p.parseDeclBody(); err != nil {
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
	// An unprefixed option name is legal and is simply ignored.
	//
	// XQuery 1.0 §4.16 required the name to be prefixed and made an
	// unprefixed one XPST0081. 3.0 lifted the restriction: the name is in no
	// namespace, matches none of the options this processor acts on, and the
	// effect of an option a processor does not recognise is
	// implementation-defined -- so ignoring it is conforming.
	// K-OptionDeclarationProlog-1b is "declare option myopt 'option value';
	// true()" under XQ30+ and accepts either XQST0123 or true.
	//
	// This parser is unconditionally 3.1 -- parseVersionDecl reads the
	// version declaration and discards it, and nothing records a version --
	// so the 1.0 rule cannot be the one applied. The XQ10 sibling of this
	// case, which does want XPST0081, is out of scope for the same reason.
	//
	// The name falls through to resolveElementName below, whose error is
	// already swallowed for an empty prefix, and lands in no namespace; the
	// declaration then matches no option this processor knows and is
	// discarded.
	// A braced name carries its own namespace and must not be sent through
	// resolveElementName, which would take the default element namespace and
	// leave the whole "Q{uri}local" spelling as the local part. The check
	// above already knows an option name may be written that way; resolving
	// it the same way made every "declare option Q{...}method" land in the
	// wrong namespace, so the serialization options of the method-text set --
	// which spell the name that way throughout -- were silently dropped and
	// their results written as XML.
	var name xdm.QName
	if strings.HasPrefix(local, "Q{") {
		end := strings.IndexByte(local, '}')
		name = xdm.QName{URI: local[2:end], Local: local[end+1:]}
	} else {
		name, err = p.sc.resolveElementName(prefix, local)
		if err != nil && prefix != "" {
			return err
		}
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
		// The same error covers a second case: §2.2.4 continues "... or if
		// the local name of the option is use-character-maps". It is a
		// serialization parameter, so the set above rightly holds it, but it
		// is not one an output declaration may set. The reason is structural
		// rather than arbitrary -- its value is a map from character to
		// replacement string, and an option declaration's value is a single
		// URILiteral, which has no way to spell one. A query that writes it
		// here has said something it cannot mean, so accepting the string and
		// discarding it, as this did, is the wrong answer twice over: the
		// declaration had no effect and nothing said so. A character map
		// still reaches serialization through output:parameter-document,
		// which is why that name is *not* excluded (Serialization-006/007
		// require it to be accepted). (Serialization-023)
		if name.Local == "use-character-maps" {
			return p.errorf(
				"XQST0109: %q may not be set by an option declaration; its "+
					"value is a map, which an option's URILiteral cannot "+
					"express", name.Local)
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
// A "," at depth zero is a *syntax error*, not a terminator. The grammar is
// exact about what an initialiser may be:
//
//	VarDecl ::= "declare" Annotation* "variable" "$" VarName TypeDeclaration?
//	            ((":=" VarValue) | ("external" (":=" VarDefaultValue)?))
//	VarValue ::= ExprSingle
//	VarDefaultValue ::= ExprSingle
//
// ExprSingle is the branch of Expr *below* the comma, so "declare variable
// $x := 1, 2;" has no parse: the comma can start neither a continuation of
// VarValue nor the ";" the declaration needs. Writing the sequence as
// "declare variable $x := (1, 2);" is how the grammar admits it, and the
// parenthesis puts the comma at depth one, where this scan ignores it.
// Accepting the bare comma silently swallowed a second expression and bound
// the wrong value, which is err:XPST0003 — "it is a static error if an
// expression is not a valid instance of the grammar".
func (p *parser) scanDeclExpr() (string, error) {
	start := p.pos
	depth := 0
	for !p.eof() {
		// A literal, a comment, a pragma or a string constructor holds no
		// ";" and no bracket this scan should count: a semicolon written
		// inside any of them does not end the declaration.
		if end, ok, err := skipNonSyntax(p.src, p.pos); ok {
			if err != nil {
				return "", err
			}
			p.pos = end + 1
			continue
		}
		switch p.src[p.pos] {
		case '(':
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
		case ',':
			if depth == 0 {
				return "", p.errorf(
					"XPST0003: the initialiser of a declaration is an "+
						"ExprSingle, so a %q may not appear here; write the "+
						"sequence in parentheses", ",")
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
	prefix, local, uri, braced, err := p.parseEQNameParts()
	if err != nil || !braced {
		return prefix, local, err
	}
	// Re-spell the scanned name in the "Q{uri}local" form resolveDeclaredName
	// unpacks. The URI it carries is the scanned one — references expanded
	// and whitespace normalised — rather than the source text, so a declared
	// name written with an entity in its URI resolves to the same namespace a
	// constructor's would.
	return "", "Q{" + uri + "}" + local, nil
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

// checkImportSyntax reports the faults of an import that its own text settles,
// leaving the cursor wherever it stopped — the caller resets it.
//
// Only the "namespace Prefix = URI" form is examined. "import schema" may also
// be written with "default element namespace" or with no prefix at all, and
// neither has a prefix or a target namespace to complain about; a module
// import's URI is not a target namespace in the same sense, so the empty-URI
// rule below is a schema rule only.
func (p *parser) checkImportSyntax(what string) error {
	p.pos += len(what)
	p.skipSpaceAndComments()
	if !p.consume("namespace") {
		// "import module URI" binds no prefix. The URI is still the target
		// namespace, and §4.11's zero-length rule still applies to it.
		if what == "module" {
			if uri, err := p.parseStringLiteral(); err == nil && uri == "" {
				return p.errorf("XQST0088: the target namespace of a " +
					"module import may not be a zero-length string")
			}
		}
		return nil
	}
	p.skipSpaceAndComments()
	prefix := p.scanNCName()
	if prefix == "" {
		return nil
	}
	p.skipSpaceAndComments()
	// §4.11 spells the binding with "=". ":=" is the variable declaration's
	// operator and the grammar does not admit it here, which is XPST0003
	// rather than anything about the import.
	if p.lookingAt(":=") {
		return p.errorf("XPST0003: expected %q, not %q in an import", "=", ":=")
	}
	if !p.consume("=") {
		return nil
	}
	// §4.11: neither a schema nor a module import may bind "xml" or "xmlns".
	// The prefixes are reserved by the Namespaces recommendation, so no
	// target could make the binding legal.
	if prefix == "xml" || prefix == "xmlns" {
		return p.errorf(
			"XQST0070: the prefix %q may not be bound by an import", prefix)
	}
	p.skipSpaceAndComments()
	uri, err := p.parseStringLiteral()
	if err != nil {
		return nil
	}
	if uri != "" {
		return nil
	}
	// A zero-length target namespace is refused by both imports, under
	// different codes. §4.11 gives the module import XQST0088 outright: a
	// library module's namespace may not be empty, so no module could ever
	// satisfy the import. The schema import's complaint is narrower — it is
	// about the prefix, which the empty URI would leave bound to no
	// namespace — and is XQST0057.
	if what == "module" {
		return p.errorf("XQST0088: the target namespace of a module import " +
			"may not be a zero-length string")
	}
	return p.errorf("XQST0057: a schema import that binds the prefix %q "+
		"must name a target namespace", prefix)
}
