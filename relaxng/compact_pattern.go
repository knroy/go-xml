package relaxng

import "github.com/knroy/go-xml/xdm"

// The pattern and name-class level of the compact syntax.

// parsePattern reads a pattern with all its infix operators.
//
// The three binary operators do not associate with one another, so this is one
// function rather than a tower of three: the first operator seen fixes which
// one the whole sequence uses, and a different one after it is an error naming
// both. A tower would have had to re-implement that refusal at each level, or
// silently impose a precedence the language does not define.
func (p *compactParser) parsePattern() (*xdm.Node, error) {
	p.depth++
	if p.depth > maxCompactDepth {
		p.depth--
		return nil, p.errorf("patterns nest more than %d deep", maxCompactDepth)
	}
	defer func() { p.depth-- }()

	first, err := p.parseRepeated()
	if err != nil {
		return nil, err
	}
	if !p.at("|") && !p.at(",") && !p.at("&") {
		return first, nil
	}

	op := p.tok.text
	operands := []*xdm.Node{first}
	for {
		if p.tok.kind != tokPunct {
			break
		}
		cur := p.tok.text
		if cur != "|" && cur != "," && cur != "&" {
			break
		}
		if cur != op {
			return nil, p.errorf(
				"%q and %q are combined without parentheses; the infix operators "+
					"do not associate with one another, so the grouping must be written",
				op, cur)
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		operand, err := p.parseRepeated()
		if err != nil {
			return nil, err
		}
		operands = append(operands, operand)
	}

	var local string
	switch op {
	case "|":
		local = "choice"
	case ",":
		local = "group"
	case "&":
		local = "interleave"
	}
	n := p.b.el(local)
	n.Children = append(n.Children, operands...)
	return n, nil
}

// parseRepeated reads a primary pattern and any postfix repetition operators.
//
// They stack: "a?*" is legal, if pointless, and each wraps the last. Reading
// them in a loop rather than allowing one keeps that from being a special
// case the grammar does not actually make.
func (p *compactParser) parseRepeated() (*xdm.Node, error) {
	n, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		var local string
		switch {
		case p.at("?"):
			local = "optional"
		case p.at("*"):
			local = "zeroOrMore"
		case p.at("+"):
			local = "oneOrMore"
		default:
			// A follow annotation binds to the whole repeated pattern, so it
			// is read once the operators are exhausted rather than between
			// them.
			return n, p.parseFollowAnnotations()
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		wrap := p.b.el(local)
		wrap.Children = append(wrap.Children, n)
		n = wrap
	}
}

// parseFollowAnnotations reads any ">> annotation" following a pattern.
//
// ">>" attaches an annotation to the pattern *before* it, where "[ ... ]"
// attaches to the pattern after. Both forms exist because an annotation on a
// branch of a choice has nowhere else to go: in
//
//	xsd:integer >> a:documentation [ "An explicit offset." ]
//	| xsd:string { pattern = "[0-9]+%" } >> a:documentation [ "A percentage." ]
//
// — from the DocBook 5.1 assembly schema in testdata, which is where this rule
// was found to be needed — a leading "[ ... ]" would bind to the whole choice
// rather than to the branch. Like the bracket form the annotation carries no
// schema meaning and is discarded once parsed; it is parsed rather than
// skipped so that a malformed one is still an error.
func (p *compactParser) parseFollowAnnotations() error {
	for p.at(">>") {
		if err := p.advance(); err != nil {
			return err
		}
		switch p.tok.kind {
		case tokIdent, tokEscapedIdent, tokCName:
			if err := p.advance(); err != nil {
				return err
			}
		default:
			return p.errorf(`expected an annotation element name after ">>", found %s`, p.tok)
		}
		if !p.at("[") {
			return p.errorf(`expected "[" after an annotation element name, found %s`, p.tok)
		}
		if err := p.skipAnnotation(); err != nil {
			return err
		}
	}
	return nil
}

// parsePrimary reads a pattern with no infix or postfix operator.
func (p *compactParser) parsePrimary() (*xdm.Node, error) {
	if err := p.readDoc(); err != nil {
		return nil, err
	}
	// An annotation in square brackets precedes the pattern it annotates.
	// Annotations carry no schema meaning — they are foreign elements the
	// compiler skips — so they are parsed to be refused if malformed, and
	// otherwise discarded. Keeping them would mean inventing element names in
	// a namespace the source only names by prefix.
	if p.at("[") {
		if err := p.skipAnnotation(); err != nil {
			return nil, err
		}
		if err := p.readDoc(); err != nil {
			return nil, err
		}
	}
	doc := p.takeDoc()

	n, err := p.parsePrimaryInner()
	if err != nil {
		return nil, err
	}
	p.attachDoc(n, doc)
	return n, nil
}

func (p *compactParser) parsePrimaryInner() (*xdm.Node, error) {
	switch {
	case p.at("("):
		if err := p.advance(); err != nil {
			return nil, err
		}
		p.depth++
		if p.depth > maxCompactDepth {
			return nil, p.errorf("patterns nest more than %d deep", maxCompactDepth)
		}
		n, err := p.parsePattern()
		p.depth--
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return n, nil

	case p.atKeyword("element"):
		return p.parseElementOrAttribute("element")
	case p.atKeyword("attribute"):
		return p.parseElementOrAttribute("attribute")
	case p.atKeyword("text"):
		return p.keywordPattern("text")
	case p.atKeyword("empty"):
		return p.keywordPattern("empty")
	case p.atKeyword("notAllowed"):
		return p.keywordPattern("notAllowed")
	case p.atKeyword("list"):
		return p.parseBracedPattern("list")
	case p.atKeyword("mixed"):
		return p.parseBracedPattern("mixed")
	case p.atKeyword("grammar"):
		return p.parseInlineGrammar()
	case p.atKeyword("parent"):
		return p.parseParentRef()
	case p.atKeyword("external"):
		return p.parseExternalRef()
	}

	// A literal with no datatype is a value in the built-in token type.
	if p.tok.kind == tokLiteral {
		return p.parseValue("", "")
	}
	// A prefixed name here is a datatype: "xsd:string", optionally with
	// parameters or a value.
	if p.tok.kind == tokCName {
		return p.parseDatatypeName()
	}
	if p.tok.kind == tokIdent || p.tok.kind == tokEscapedIdent {
		return p.parseRef()
	}
	return nil, p.errorf("expected a pattern, found %s", p.tok)
}

// keywordPattern reads one of the patterns that is just a keyword.
func (p *compactParser) keywordPattern(local string) (*xdm.Node, error) {
	if err := p.advance(); err != nil {
		return nil, err
	}
	return p.b.el(local), nil
}

// parseBracedPattern reads `list { p }` and `mixed { p }`.
func (p *compactParser) parseBracedPattern(local string) (*xdm.Node, error) {
	if err := p.advance(); err != nil {
		return nil, err
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	inner, err := p.parsePattern()
	if err != nil {
		return nil, err
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	n := p.b.el(local)
	n.Children = append(n.Children, inner)
	return n, nil
}

// parseRef reads a reference to a definition.
func (p *compactParser) parseRef() (*xdm.Node, error) {
	n := p.b.el("ref")
	p.b.attr(n, "name", p.tok.text)
	return n, p.advance()
}

// parseParentRef reads `parent name`.
//
// It names a definition in the grammar one level out, which is how a nested
// grammar reaches the one that contains it. The compiler already refuses one
// written outside any enclosing grammar, so that is not rechecked here.
func (p *compactParser) parseParentRef() (*xdm.Node, error) {
	if err := p.advance(); err != nil { // "parent"
		return nil, err
	}
	if p.tok.kind != tokIdent && p.tok.kind != tokEscapedIdent {
		return nil, p.errorf(`expected a definition name after "parent", found %s`, p.tok)
	}
	n := p.b.el("parentRef")
	p.b.attr(n, "name", p.tok.text)
	return n, p.advance()
}

// parseExternalRef reads `external "uri" [inherit = prefix]`.
func (p *compactParser) parseExternalRef() (*xdm.Node, error) {
	if err := p.advance(); err != nil { // "external"
		return nil, err
	}
	if p.tok.kind != tokLiteral {
		return nil, p.errorf(`expected a URI in quotes after "external", found %s`, p.tok)
	}
	n := p.b.el("externalRef")
	p.b.attr(n, "href", p.tok.text)
	if err := p.advance(); err != nil {
		return nil, err
	}
	if err := p.parseInheritClause(n); err != nil {
		return nil, err
	}
	return n, nil
}

// parseInlineGrammar reads `grammar { ... }`.
func (p *compactParser) parseInlineGrammar() (*xdm.Node, error) {
	if err := p.advance(); err != nil { // "grammar"
		return nil, err
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	g := p.b.el("grammar")
	if err := p.parseGrammarBody(g, tokPunct); err != nil {
		return nil, err
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	return g, nil
}

// parseElementOrAttribute reads `element nameclass { p }` or the attribute
// form.
func (p *compactParser) parseElementOrAttribute(local string) (*xdm.Node, error) {
	if err := p.advance(); err != nil {
		return nil, err
	}
	n := p.b.el(local)
	nc, simple, err := p.parseNameClass(n)
	if err != nil {
		return nil, err
	}
	// A name class that is one name becomes the name= attribute, which is
	// what an author writing the XML syntax by hand would do. Anything richer
	// needs a name-class child, and syntax.go refuses both at once — so this
	// chooses exactly one.
	if simple != "" {
		p.b.attr(n, "name", simple)
	} else {
		n.Children = append(n.Children, nc)
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	inner, err := p.parsePattern()
	if err != nil {
		return nil, err
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	n.Children = append(n.Children, inner)
	return n, nil
}

// parseNameClass reads a name class.
//
// It returns the name-class element, and separately the lexical name when the
// class is exactly one name, since that case is written as an attribute
// instead. owner is the element the namespace bindings for any prefix must be
// written onto.
func (p *compactParser) parseNameClass(owner *xdm.Node) (nc *xdm.Node, simple string, err error) {
	p.depth++
	if p.depth > maxCompactDepth {
		p.depth--
		return nil, "", p.errorf("name classes nest more than %d deep", maxCompactDepth)
	}
	defer func() { p.depth-- }()

	first, firstSimple, err := p.parseNameClassPrimary(owner)
	if err != nil {
		return nil, "", err
	}
	if !p.at("|") {
		return first, firstSimple, nil
	}
	// A choice of names is a name-class child, so a simple name that turns
	// out to be one branch of a choice has to become a <name> element after
	// all.
	choice := p.b.el("choice")
	choice.Children = append(choice.Children, p.nameClassNode(first, firstSimple))
	for p.at("|") {
		if err := p.advance(); err != nil {
			return nil, "", err
		}
		next, nextSimple, err := p.parseNameClassPrimary(owner)
		if err != nil {
			return nil, "", err
		}
		choice.Children = append(choice.Children, p.nameClassNode(next, nextSimple))
	}
	return choice, "", nil
}

// nameClassNode returns the element form of a name class, making a <name> when
// the class was a bare name.
func (p *compactParser) nameClassNode(n *xdm.Node, simple string) *xdm.Node {
	if simple == "" {
		return n
	}
	name := p.b.el("name")
	p.b.text(name, simple)
	return name
}

// parseNameClassPrimary reads one name class with no "|".
func (p *compactParser) parseNameClassPrimary(owner *xdm.Node) (*xdm.Node, string, error) {
	// An annotation may precede a name class as it may precede a pattern.
	if p.at("[") {
		if err := p.skipAnnotation(); err != nil {
			return nil, "", err
		}
	}
	switch {
	case p.at("("):
		if err := p.advance(); err != nil {
			return nil, "", err
		}
		nc, simple, err := p.parseNameClass(owner)
		if err != nil {
			return nil, "", err
		}
		if err := p.expect(")"); err != nil {
			return nil, "", err
		}
		// Parentheses do not change what a class means, but a parenthesised
		// single name can still be written as name=, so the simple form
		// survives them.
		return nc, simple, nil

	case p.at("*"):
		// "*" is any name, and "* - nc" excludes a class from it.
		if err := p.advance(); err != nil {
			return nil, "", err
		}
		n := p.b.el("anyName")
		if err := p.parseNameClassExcept(n, owner); err != nil {
			return nil, "", err
		}
		return n, "", nil
	}

	if p.tok.kind == tokNsName {
		// "prefix:*" is any name in that namespace.
		prefix := p.tok.prefix
		uri, ok := p.resolvePrefix(prefix)
		if !ok {
			return nil, "", p.errorf("the prefix %q is not bound", prefix)
		}
		if err := p.advance(); err != nil {
			return nil, "", err
		}
		n := p.b.el("nsName")
		p.b.attr(n, "ns", uri)
		if err := p.parseNameClassExcept(n, owner); err != nil {
			return nil, "", err
		}
		return n, "", nil
	}

	if p.tok.kind == tokCName {
		lexical, err := p.qnameFor(owner, p.tok.prefix, p.tok.text)
		if err != nil {
			return nil, "", err
		}
		if err := p.advance(); err != nil {
			return nil, "", err
		}
		return nil, lexical, nil
	}

	if p.tok.kind == tokIdent || p.tok.kind == tokEscapedIdent {
		name := p.tok.text
		if err := p.advance(); err != nil {
			return nil, "", err
		}
		return nil, name, nil
	}
	return nil, "", p.errorf("expected a name class, found %s", p.tok)
}

// parseNameClassExcept reads an optional `- nameclass` following anyName or
// nsName.
func (p *compactParser) parseNameClassExcept(n *xdm.Node, owner *xdm.Node) error {
	if !p.at("-") {
		return nil
	}
	if err := p.advance(); err != nil {
		return err
	}
	inner, simple, err := p.parseNameClassPrimary(owner)
	if err != nil {
		return err
	}
	ex := p.b.el("except")
	ex.Children = append(ex.Children, p.nameClassNode(inner, simple))
	// A further "|" after an except belongs to the excepted class, not to the
	// class being excepted from: "* - (a | b)" is written with parentheses,
	// but "* - a | b" excludes both. Reading the rest of the choice here is
	// what makes the second spelling mean that.
	for p.at("|") {
		if err := p.advance(); err != nil {
			return err
		}
		next, nextSimple, err := p.parseNameClassPrimary(owner)
		if err != nil {
			return err
		}
		ex.Children = append(ex.Children, p.nameClassNode(next, nextSimple))
	}
	if len(ex.Children) > 1 {
		choice := p.b.el("choice")
		choice.Children = ex.Children
		ex.Children = []*xdm.Node{choice}
	}
	n.Children = append(n.Children, ex)
	return nil
}
