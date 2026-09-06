package relaxng

import (
	"github.com/knroy/go-xml/xdm"
)

// The declarations and grammar level of the compact syntax.
//
// A compact schema is a sequence of declarations followed by either one
// pattern or a sequence of grammar contents. Both forms produce the same XML
// syntax shape — the bare pattern becomes the document element directly, the
// grammar form becomes <grammar> — and the declarations become attributes and
// namespace bindings on whichever it is.

// parseTopLevel reads the declarations and then the body.
func (p *compactParser) parseTopLevel() (*xdm.Node, error) {
	if err := p.parseDecls(); err != nil {
		return nil, err
	}
	if err := p.readDoc(); err != nil {
		return nil, err
	}

	// A schema is either one pattern or a grammar written without the
	// "grammar" keyword — "start = foo" at the top level is a grammar. The
	// two are told apart by what the first construct is: "start", "div",
	// "include" or an identifier followed by an assignment operator begins a
	// grammar, anything else begins a pattern. The identifier case is the
	// only one needing more than the current token, and it is settled by
	// looking at the next one over a copy of the lexer, which costs one token
	// of scanning and no backtracking of the parse.
	if p.startsGrammarContent() {
		g := p.b.el("grammar")
		if err := p.parseGrammarBody(g, tokEOF); err != nil {
			return nil, err
		}
		p.decorate(g)
		return g, nil
	}

	pat, err := p.parsePattern()
	if err != nil {
		return nil, err
	}
	if p.tok.kind != tokEOF {
		return nil, p.errorf("unexpected %s after the schema's pattern", p.tok)
	}
	p.decorate(pat)
	return pat, nil
}

// decorate writes the schema-level declarations onto the document element.
//
// The default namespace becomes ns=, which the compiler inherits down the
// whole tree — that is the XML syntax's own rule for the attribute, and it is
// what makes "default namespace" mean what it says. The prefix bindings become
// namespace nodes, because a prefixed name in the compact syntax was resolved
// against them and the compiler resolves the name= it becomes by looking them
// up again.
func (p *compactParser) decorate(root *xdm.Node) {
	if p.defaultNS != "" {
		p.b.attr(root, "ns", p.defaultNS)
	}
	for prefix, uri := range p.namespaces {
		root.Namespaces = append(root.Namespaces, &xdm.Node{
			Kind: xdm.KindNamespace, Name: xdm.QName{Local: prefix}, Value: uri,
		})
	}
	// datatypeLibrary is inherited the same way ns= is, so the one written
	// without a prefix — "datatypes xsd = ..." names a prefix, but a schema
	// with exactly one library conventionally uses it everywhere — is set
	// here and overridden per <data> where a different prefix was used.
	if uri, ok := p.datatypes["xsd"]; ok {
		p.b.attr(root, "datatypeLibrary", uri)
	}
}

// startsGrammarContent reports whether the coming construct belongs to a
// grammar rather than being a pattern.
func (p *compactParser) startsGrammarContent() bool {
	if p.atKeyword("start") || p.atKeyword("div") || p.atKeyword("include") {
		return true
	}
	// An identifier begins a grammar only when it is being defined. "element"
	// and the other pattern keywords are excluded because a bare pattern may
	// begin with one and they can never be a definition's name unescaped.
	if p.tok.kind != tokIdent && p.tok.kind != tokEscapedIdent {
		return false
	}
	if p.tok.kind == tokIdent && patternKeywords[p.tok.text] {
		return false
	}
	scan := *p.lex
	next, err := scan.next()
	if err != nil {
		return false
	}
	return next.kind == tokPunct &&
		(next.text == "=" || next.text == "|=" || next.text == "&=")
}

// patternKeywords are the words that begin a pattern.
//
// They are listed so that startsGrammarContent can tell "element" beginning a
// bare-pattern schema from "foo" beginning a definition. A name written with a
// backslash is not in this set by construction, which is the escape's purpose.
var patternKeywords = map[string]bool{
	"element": true, "attribute": true, "text": true, "empty": true,
	"notAllowed": true, "list": true, "mixed": true, "parent": true,
	"external": true, "grammar": true,
}

// parseDecls reads the namespace and datatypes declarations that open a schema.
func (p *compactParser) parseDecls() error {
	for {
		if err := p.readDoc(); err != nil {
			return err
		}
		switch {
		case p.atKeyword("default"):
			if err := p.parseDefaultNamespace(); err != nil {
				return err
			}
		case p.atKeyword("namespace"):
			if err := p.parseNamespaceDecl(); err != nil {
				return err
			}
		case p.atKeyword("datatypes"):
			if err := p.parseDatatypesDecl(); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

// parseDefaultNamespace reads `default namespace [prefix] = "uri"`.
//
// The optional prefix binds that prefix to the same URI as well, which is a
// convenience the syntax offers so that a schema need not write the URI twice.
func (p *compactParser) parseDefaultNamespace() error {
	if err := p.advance(); err != nil { // "default"
		return err
	}
	if !p.atKeyword("namespace") {
		return p.errorf(`expected "namespace" after "default", found %s`, p.tok)
	}
	if err := p.advance(); err != nil {
		return err
	}
	prefix := ""
	if p.tok.kind == tokIdent || p.tok.kind == tokEscapedIdent {
		prefix = p.tok.text
		if err := p.advance(); err != nil {
			return err
		}
	}
	if err := p.expect("="); err != nil {
		return err
	}
	uri, err := p.parseNamespaceURI()
	if err != nil {
		return err
	}
	p.defaultNS = uri
	if prefix != "" {
		p.namespaces[prefix] = uri
	}
	return nil
}

// parseNamespaceDecl reads `namespace prefix = "uri"`.
func (p *compactParser) parseNamespaceDecl() error {
	if err := p.advance(); err != nil { // "namespace"
		return err
	}
	if p.tok.kind != tokIdent && p.tok.kind != tokEscapedIdent {
		return p.errorf("expected a prefix after \"namespace\", found %s", p.tok)
	}
	prefix := p.tok.text
	if err := p.advance(); err != nil {
		return err
	}
	if err := p.expect("="); err != nil {
		return err
	}
	uri, err := p.parseNamespaceURI()
	if err != nil {
		return err
	}
	// Binding "xmlns" is refused for the same reason the XML syntax refuses
	// it: the prefix is reserved, and a schema that appears to rebind it is
	// making a claim no processor will honour.
	if prefix == "xmlns" {
		return p.errorf(`the prefix "xmlns" may not be bound`)
	}
	p.namespaces[prefix] = uri
	return nil
}

// parseNamespaceURI reads the right-hand side of a namespace declaration,
// which is a literal or the keyword "inherit".
//
// "inherit" means the namespace inherited from the including schema. At the
// top level of a schema being compiled on its own there is nothing to inherit,
// and it is the empty namespace — which is what leaving ns= unwritten already
// means, so it is recorded as such rather than specially.
func (p *compactParser) parseNamespaceURI() (string, error) {
	if p.atKeyword("inherit") {
		if err := p.advance(); err != nil {
			return "", err
		}
		return "", nil
	}
	if p.tok.kind != tokLiteral {
		return "", p.errorf("expected a namespace URI in quotes, found %s", p.tok)
	}
	uri := p.tok.text
	if err := p.advance(); err != nil {
		return "", err
	}
	return uri, nil
}

// parseDatatypesDecl reads `datatypes prefix = "uri"`.
func (p *compactParser) parseDatatypesDecl() error {
	if err := p.advance(); err != nil { // "datatypes"
		return err
	}
	if p.tok.kind != tokIdent && p.tok.kind != tokEscapedIdent {
		return p.errorf(`expected a prefix after "datatypes", found %s`, p.tok)
	}
	prefix := p.tok.text
	if err := p.advance(); err != nil {
		return err
	}
	if err := p.expect("="); err != nil {
		return err
	}
	if p.tok.kind != tokLiteral {
		return p.errorf("expected a datatype library URI in quotes, found %s", p.tok)
	}
	p.datatypes[prefix] = p.tok.text
	return p.advance()
}

// parseGrammarBody reads grammar contents until the closing token.
//
// end is tokEOF for a schema that is a grammar without the keyword, and
// tokPunct "}" for one written with braces. Passing it in rather than having
// two loops keeps the set of things a grammar may hold in one place.
func (p *compactParser) parseGrammarBody(g *xdm.Node, end tokenKind) error {
	for {
		if err := p.readDoc(); err != nil {
			return err
		}
		if end == tokEOF && p.tok.kind == tokEOF {
			return nil
		}
		if end == tokPunct && p.at("}") {
			return nil
		}
		if p.tok.kind == tokEOF {
			return p.errorf("unexpected end of input; a grammar is unclosed")
		}
		if err := p.parseGrammarContent(g); err != nil {
			return err
		}
	}
}

// parseGrammarContent reads one start, define, div or include.
func (p *compactParser) parseGrammarContent(g *xdm.Node) error {
	doc := p.takeDoc()
	switch {
	case p.atKeyword("start"):
		return p.parseStart(g, doc)
	case p.atKeyword("div"):
		return p.parseDiv(g, doc)
	case p.atKeyword("include"):
		return p.parseInclude(g, doc)
	}
	if p.tok.kind != tokIdent && p.tok.kind != tokEscapedIdent {
		return p.errorf("expected a definition, found %s", p.tok)
	}
	return p.parseDefine(g, doc)
}

// combineFor maps an assignment operator to the XML syntax's combine=.
//
// "=" is a plain definition with no combine, and two of those for one name is
// an error the compiler already reports. "|=" and "&=" say how a definition
// spread over several places is to be joined.
func combineFor(op string) (string, bool) {
	switch op {
	case "=":
		return "", true
	case "|=":
		return "choice", true
	case "&=":
		return "interleave", true
	}
	return "", false
}

// parseAssignOp reads "=", "|=" or "&=" and returns the combine= it means.
func (p *compactParser) parseAssignOp() (string, error) {
	if p.tok.kind != tokPunct {
		return "", p.errorf(`expected "=", "|=" or "&=", found %s`, p.tok)
	}
	combine, ok := combineFor(p.tok.text)
	if !ok {
		return "", p.errorf(`expected "=", "|=" or "&=", found %s`, p.tok)
	}
	return combine, p.advance()
}

// parseStart reads `start = pattern`.
func (p *compactParser) parseStart(g *xdm.Node, doc *annotation) error {
	if err := p.advance(); err != nil { // "start"
		return err
	}
	combine, err := p.parseAssignOp()
	if err != nil {
		return err
	}
	pat, err := p.parsePattern()
	if err != nil {
		return err
	}
	s := p.b.el("start")
	if combine != "" {
		p.b.attr(s, "combine", combine)
	}
	s.Children = append(s.Children, pat)
	p.attachDoc(s, doc)
	g.Children = append(g.Children, s)
	return nil
}

// parseDefine reads `name = pattern`.
func (p *compactParser) parseDefine(g *xdm.Node, doc *annotation) error {
	name := p.tok.text
	if err := p.advance(); err != nil {
		return err
	}
	combine, err := p.parseAssignOp()
	if err != nil {
		return err
	}
	pat, err := p.parsePattern()
	if err != nil {
		return err
	}
	d := p.b.el("define")
	p.b.attr(d, "name", name)
	if combine != "" {
		p.b.attr(d, "combine", combine)
	}
	d.Children = append(d.Children, pat)
	p.attachDoc(d, doc)
	g.Children = append(g.Children, d)
	return nil
}

// parseDiv reads `div { ... }`.
//
// A div groups definitions without affecting their scope. It exists in the
// compact syntax for the same reason as in the XML syntax: so that an include
// can override a whole block, and so that annotations can apply to a group.
func (p *compactParser) parseDiv(g *xdm.Node, doc *annotation) error {
	if err := p.advance(); err != nil { // "div"
		return err
	}
	if err := p.expect("{"); err != nil {
		return err
	}
	d := p.b.el("div")
	if err := p.parseGrammarBody(d, tokPunct); err != nil {
		return err
	}
	if err := p.expect("}"); err != nil {
		return err
	}
	p.attachDoc(d, doc)
	g.Children = append(g.Children, d)
	return nil
}

// parseInclude reads `include "uri" [inherit = prefix] [{ overrides }]`.
//
// The overrides are definitions that replace the included schema's own, which
// is how a schema is specialised without editing it. They are the same
// grammar contents as anywhere else, so the same loop reads them.
func (p *compactParser) parseInclude(g *xdm.Node, doc *annotation) error {
	if err := p.advance(); err != nil { // "include"
		return err
	}
	if p.tok.kind != tokLiteral {
		return p.errorf("expected a URI in quotes after \"include\", found %s", p.tok)
	}
	href := p.tok.text
	if err := p.advance(); err != nil {
		return err
	}
	inc := p.b.el("include")
	p.b.attr(inc, "href", href)

	if err := p.parseInheritClause(inc); err != nil {
		return err
	}
	if p.at("{") {
		if err := p.advance(); err != nil {
			return err
		}
		if err := p.parseGrammarBody(inc, tokPunct); err != nil {
			return err
		}
		if err := p.expect("}"); err != nil {
			return err
		}
	}
	p.attachDoc(inc, doc)
	g.Children = append(g.Children, inc)
	return nil
}

// parseInheritClause reads an optional `inherit = prefix`.
//
// It says which namespace the included schema's unprefixed names take. In the
// XML syntax that is ns= on the <include>, so the prefix is resolved here and
// the URI written on, which is also why an unbound prefix is an error at this
// point rather than a dangling reference later.
func (p *compactParser) parseInheritClause(n *xdm.Node) error {
	if !p.atKeyword("inherit") {
		return nil
	}
	if err := p.advance(); err != nil {
		return err
	}
	if err := p.expect("="); err != nil {
		return err
	}
	if p.tok.kind != tokIdent && p.tok.kind != tokEscapedIdent {
		return p.errorf(`expected a prefix after "inherit =", found %s`, p.tok)
	}
	prefix := p.tok.text
	uri, ok := p.namespaces[prefix]
	if !ok {
		return p.errorf("the prefix %q in an inherit clause is not bound", prefix)
	}
	p.b.attr(n, "ns", uri)
	return p.advance()
}

// resolvePrefix returns the URI a prefix is bound to.
//
// The "xml" prefix is bound by the XML specification itself and needs no
// declaration, which matches what resolveName in the compiler does for the
// XML syntax.
func (p *compactParser) resolvePrefix(prefix string) (string, bool) {
	if prefix == "xml" {
		return xdm.NSXML, true
	}
	uri, ok := p.namespaces[prefix]
	return uri, ok
}

// datatypeLibraryFor returns the library URI a datatype prefix names.
//
// The "xsd" prefix is conventionally the W3C library and the spec makes it
// available without a datatypes declaration, which is why every schema in
// this repository's testdata writes "xsd:string" without declaring it.
func (p *compactParser) datatypeLibraryFor(prefix string) (string, bool) {
	if uri, ok := p.datatypes[prefix]; ok {
		return uri, true
	}
	if prefix == "xsd" {
		return xsdLibrary, true
	}
	return "", false
}

// qnameFor turns a prefixed name into the lexical form the XML syntax uses,
// reporting an unbound prefix rather than letting it through.
//
// The name written onto the tree keeps the prefix, and the binding for it is
// written as a namespace node on the element, so the compiler resolves it by
// its own rule. Resolving it to a URI here instead would mean inventing an
// ns= that could collide with an inherited one.
func (p *compactParser) qnameFor(n *xdm.Node, prefix, local string) (string, error) {
	uri, ok := p.resolvePrefix(prefix)
	if !ok {
		return "", p.errorf("the prefix %q is not bound", prefix)
	}
	n.Namespaces = append(n.Namespaces, &xdm.Node{
		Kind: xdm.KindNamespace, Name: xdm.QName{Local: prefix}, Value: uri,
	})
	return prefix + ":" + local, nil
}
