package xquery

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// parseComputed parses a computed constructor if one starts here.
//
// The keywords are not reserved — "element" is a legal function name and a
// legal element name — so each is recognised only when what follows it could
// not be anything else: a name and a brace, or a brace on its own. "element(",
// which is a kind test, and "element" alone, which is a name, both fall
// through to the expression parser.
func (p *parser) parseComputed() (node, bool, error) {
	start := p.pos
	kw := p.peekKeyword()
	switch kw {
	case "element", "attribute", "processing-instruction", "namespace",
		"document", "text", "comment":
	default:
		return nil, false, nil
	}

	save := p.pos
	p.pos += len(kw)
	p.skipSpaceAndComments()

	// "document {", "text {" and "comment {" take no name.
	switch kw {
	case "document", "text", "comment":
		if !p.lookingAt("{") {
			p.pos = save
			return nil, false, nil
		}
		content, err := p.parseBracedContent()
		if err != nil {
			return nil, true, err
		}
		switch kw {
		case "document":
			return &document{content: content, baseURI: p.sc.baseURI}, true, nil
		case "text":
			return &textNode{content: content}, true, nil
		default:
			return &comment{content: content}, true, nil
		}
	}

	// The rest take a name, written either as a QName or as an expression in
	// braces, and then their content in braces.
	var namePrefix, nameLocal, nameURI string
	var nameBraced bool
	var nameExpr *compiledExpr
	if p.lookingAt("{") {
		end, err := findEnclosed(p.src, p.pos)
		if err != nil {
			return nil, true, err
		}
		body := strings.TrimSpace(p.src[p.pos+1 : end])
		p.pos = end + 1
		if body == "" {
			return nil, true, p.errorAt(start,
				"XPST0003: a computed constructor needs a name")
		}
		nameExpr, err = p.compileExpr(body)
		if err != nil {
			return nil, true, err
		}
	} else {
		var err error
		namePrefix, nameLocal, nameURI, nameBraced, err = p.parseEQNameParts()
		if err != nil {
			// Not a computed constructor after all — "element" used as a
			// name or a function call.
			p.pos = save
			return nil, false, nil
		}
	}

	p.skipSpaceAndComments()
	if !p.lookingAt("{") {
		p.pos = save
		return nil, false, nil
	}
	content, err := p.parseBracedContent()
	if err != nil {
		return nil, true, err
	}

	switch kw {
	case "element":
		el := &element{nameExpr: nameExpr, baseURI: p.sc.baseURI,
			inherited: p.sc.ctorNS, content: content}
		if nameExpr == nil {
			// A braced URI names the namespace outright, so there is no
			// prefix to look up and the default element namespace does not
			// apply: "Q{}x" is x in no namespace even under a default
			// declaration.
			if nameBraced {
				el.name = xdm.QName{URI: nameURI, Local: nameLocal}
			} else {
				q, err := p.sc.resolveElementName(namePrefix, nameLocal)
				if err != nil {
					return nil, true, p.errorAt(start, "%v", err)
				}
				el.name = q
			}
		}
		return el, true, nil

	case "attribute":
		at := &attribute{nameExpr: nameExpr, value: content, computed: true}
		if nameExpr == nil {
			if nameBraced {
				at.name = xdm.QName{URI: nameURI, Local: nameLocal}
			} else {
				q, err := p.sc.resolveAttributeName(namePrefix, nameLocal)
				if err != nil {
					return nil, true, p.errorAt(start, "%v", err)
				}
				at.name = q
			}
		}
		return &computedAttr{attr: at}, true, nil

	case "processing-instruction":
		// A target written out rather than computed is an NCName:
		//
		//   CompPIConstructor ::= "processing-instruction"
		//                         (NCName | ("{" Expr "}")) EnclosedExpr
		//
		// NCName is the non-colonised name, so neither a prefix nor a braced
		// URI literal belongs here — the same rule the namespace constructor
		// below applies to its prefix. Only nameLocal was read, so
		// "processing-instruction foo:pi {...}" quietly dropped the prefix
		// and built <?pi text?>: a wrong answer rather than the rejection the
		// grammar calls for, which is err:XPST0003. (Constr-comppi-name-2)
		if nameExpr == nil && (namePrefix != "" || nameBraced) {
			written := qnameText(namePrefix, nameLocal)
			if nameBraced {
				written = "Q{" + nameURI + "}" + nameLocal
			}
			return nil, true, p.errorAt(start,
				"XPST0003: a processing-instruction constructor takes an "+
					"NCName target, not %q", written)
		}
		// §3.9.3.5 refuses "xml" in any combination of case as a target.
		// XQDY0064 is a *dynamic* error, so it is raised by pi.eval even for
		// a target written as a name and knowable here: the suite catches it
		// with "try { processing-instruction XML {} } catch err:XQDY0064",
		// which only a dynamic error reaches.
		return &pi{target: nameLocal, targetExpr: nameExpr,
			content: content}, true, nil

	case "namespace":
		// The prefix is written as an NCName or computed in braces, and the
		// content is the URI. Unlike an element or an attribute the name here
		// is a prefix rather than a QName, so it does not go through
		// parseEQNameParts: "namespace Q{...}x" is not a spelling the grammar
		// has.
		ns := &namespaceNode{prefixExpr: nameExpr, content: content}
		if nameExpr == nil {
			if namePrefix != "" {
				return nil, true, p.errorAt(start,
					"XPST0003: a namespace constructor takes a prefix, "+
						"not the QName %q", qnameText(namePrefix, nameLocal))
			}
			ns.prefix = nameLocal
		}
		return ns, true, nil
	}
	return nil, false, nil
}

// computedAttr wraps an attribute so it can stand as a node in a sequence.
//
// A direct constructor's attributes are held by the element that owns them,
// but "attribute a {1}" is an item in its own right, and may be the whole of a
// query. The builder decides whether that is legal where it lands: an
// attribute with no element to attach to is a legal item at the top level and
// an error inside element content that already has children.
type computedAttr struct{ attr *attribute }

func (n *computedAttr) eval(out *builderRef, ctx *evalContext) error {
	return n.attr.eval(out, ctx)
}

// parseBracedContent parses "{ ... }" as constructor content.
//
// The content is a sequence of items, so it is parsed the same way a query
// body is: constructors are read here, and everything else is handed to xpath.
func (p *parser) parseBracedContent() ([]node, error) {
	end, err := findEnclosed(p.src, p.pos)
	if err != nil {
		return nil, err
	}
	inner := &parser{src: p.src[p.pos+1 : end], sc: p.sc, version: p.version}
	p.pos = end + 1
	body, err := inner.parseQueryBody()
	if err != nil {
		return nil, err
	}
	return body, nil
}

// peekKeyword returns the identifier at the current position without
// consuming it.
func (p *parser) peekKeyword() string {
	i := p.pos
	for i < len(p.src) && isNameByte(p.src[i]) {
		i++
	}
	return p.src[p.pos:i]
}
