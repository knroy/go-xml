package relaxng

import "github.com/knroy/go-xml/xdm"

// Datatypes, values and annotations in the compact syntax.

// parseDatatypeName reads a prefixed datatype name and whatever follows it.
//
// Three constructs begin the same way. "xsd:string" alone is a <data>;
// "xsd:string { minLength = "1" }" is a <data> with parameters; and
// "xsd:string "abc"" is a <value> of that type. The token after the name
// decides, which is why they are read together rather than by three callers
// that would each have to look ahead.
func (p *compactParser) parseDatatypeName() (*xdm.Node, error) {
	prefix, local := p.tok.prefix, p.tok.text
	library, ok := p.datatypeLibraryFor(prefix)
	if !ok {
		return nil, p.errorf("the datatype prefix %q is not bound", prefix)
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	if p.tok.kind == tokLiteral {
		return p.parseValue(library, local)
	}
	n := p.b.el("data")
	p.b.attr(n, "type", local)
	p.b.attr(n, "datatypeLibrary", library)
	if p.at("{") {
		if err := p.parseParams(n); err != nil {
			return nil, err
		}
	}
	// "- pattern" after a data excludes values the pattern matches.
	if p.at("-") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		inner, err := p.parseRepeated()
		if err != nil {
			return nil, err
		}
		ex := p.b.el("except")
		ex.Children = append(ex.Children, inner)
		n.Children = append(n.Children, ex)
	}
	return n, nil
}

// parseParams reads `{ name = "value" ... }`.
func (p *compactParser) parseParams(n *xdm.Node) error {
	if err := p.expect("{"); err != nil {
		return err
	}
	for !p.at("}") {
		if p.tok.kind == tokEOF {
			return p.errorf("unexpected end of input; a parameter list is unclosed")
		}
		// An annotation may appear among parameters.
		if p.at("[") {
			if err := p.skipAnnotation(); err != nil {
				return err
			}
			continue
		}
		if p.tok.kind != tokIdent && p.tok.kind != tokEscapedIdent {
			return p.errorf("expected a parameter name, found %s", p.tok)
		}
		name := p.tok.text
		if err := p.advance(); err != nil {
			return err
		}
		if err := p.expect("="); err != nil {
			return err
		}
		value, err := p.parseLiteralValue()
		if err != nil {
			return err
		}
		param := p.b.el("param")
		p.b.attr(param, "name", name)
		p.b.text(param, value)
		n.Children = append(n.Children, param)
	}
	return p.expect("}")
}

// parseValue reads a literal, producing <value>.
//
// library and typeName are empty for a bare literal, which the spec makes a
// value in the built-in "token" type. Writing type= only when the source named
// one matters: an unqualified <value> takes the token type by the compiler's
// own default, and asserting it here would be asserting a rule twice, in two
// places that could disagree.
func (p *compactParser) parseValue(library, typeName string) (*xdm.Node, error) {
	text, err := p.parseLiteralValue()
	if err != nil {
		return nil, err
	}
	n := p.b.el("value")
	if typeName != "" {
		p.b.attr(n, "type", typeName)
		p.b.attr(n, "datatypeLibrary", library)
	}
	p.b.text(n, text)
	return n, nil
}

// parseLiteralValue reads a literal, joining the "~" concatenation form.
//
// Concatenation exists so that a long value can be broken across lines, and so
// that a character written as an escape can sit beside literal text. The
// segments are joined with nothing between them — "~" is concatenation, not a
// separator with a value of its own.
func (p *compactParser) parseLiteralValue() (string, error) {
	if p.tok.kind != tokLiteral {
		return "", p.errorf("expected a literal, found %s", p.tok)
	}
	text := p.tok.text
	if err := p.advance(); err != nil {
		return "", err
	}
	for p.at("~") {
		if err := p.advance(); err != nil {
			return "", err
		}
		if p.tok.kind != tokLiteral {
			return "", p.errorf(`expected a literal after "~", found %s`, p.tok)
		}
		text += p.tok.text
		if err := p.advance(); err != nil {
			return "", err
		}
	}
	return text, nil
}

// skipAnnotation reads and discards a `[ ... ]` annotation.
//
// Annotations are foreign attributes and elements. They carry no schema
// meaning — the compiler skips any element outside the RELAX NG namespace, and
// any attribute in a foreign namespace — so nothing is lost by not building
// them. They are still parsed rather than skipped by counting brackets,
// because a malformed annotation is a malformed schema and must be reported as
// one; a bracket-counting skip would accept "[ = ]" and any other nonsense
// between the brackets.
func (p *compactParser) skipAnnotation() error {
	if err := p.expect("["); err != nil {
		return err
	}
	p.depth++
	if p.depth > maxCompactDepth {
		p.depth--
		return p.errorf("annotations nest more than %d deep", maxCompactDepth)
	}
	defer func() { p.depth-- }()

	for !p.at("]") {
		if p.tok.kind == tokEOF {
			return p.errorf("unexpected end of input; an annotation is unclosed")
		}
		if err := p.skipAnnotationItem(); err != nil {
			return err
		}
	}
	return p.expect("]")
}

// skipAnnotationItem reads one attribute or element inside an annotation.
//
// The two shapes are `name = "value"` and `name [ ... ]`, the second being a
// nested annotation element which may itself hold a literal as its content.
func (p *compactParser) skipAnnotationItem() error {
	switch p.tok.kind {
	case tokIdent, tokEscapedIdent, tokCName:
		if err := p.advance(); err != nil {
			return err
		}
	case tokLiteral:
		// An element annotation's text content.
		_, err := p.parseLiteralValue()
		return err
	default:
		return p.errorf("expected an annotation attribute or element, found %s", p.tok)
	}

	switch {
	case p.at("="):
		if err := p.advance(); err != nil {
			return err
		}
		_, err := p.parseLiteralValue()
		return err
	case p.at("["):
		return p.skipAnnotation()
	}
	// A bare name is the element form written with no content, which the
	// grammar permits inside a nested annotation.
	return nil
}
