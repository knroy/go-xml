package xquery

import (
	"strings"
)

// The XQuery-only expression forms.
//
// XQuery 3.1 [42] ExprSingle is XPath's ExprSingle plus a handful of
// alternatives the expression language does not have: switch, typeswitch,
// try/catch, validate, the extension expression, ordered and unordered, and
// the string constructor. Every one of them is recognised here rather than in
// xpath, for the reason the whole package exists: the expression parser is at
// 100% conformance and is not touched.
//
// They are recognised at the head of a query-body item, which is where the
// grammar puts them and where the tests put them: each is an ExprSingle, so it
// is a whole item of a comma-separated sequence, and the return clauses that
// follow are themselves ExprSingle and are parsed here in turn. What is *not*
// reached is one of these nested inside an ordinary XPath expression —
// "1 + (switch ...)" — because that inner text goes to xpath as a substring.
// A parenthesised sub-expression would have to be re-entered to fix that, and
// the suite does not ask: every case in prod-SwitchExpr, prod-TypeswitchExpr
// and prod-TryCatchExpr writes the construct at the head of an ExprSingle.

// parseXQueryOnly parses one of the XQuery-only expression forms if one starts
// at the current position, reporting whether it did.
//
// Each keyword is checked the way parseComputed checks its own: none of them
// is reserved, so "switch" alone is a legal name and "try(1)" is a legal
// function call. The keyword only commits when what follows it can only be the
// construct — a parenthesis for switch and typeswitch, a brace for try,
// ordered, unordered and validate.
func (p *parser) parseXQueryOnly() (node, bool, error) {
	if p.lookingAt("``[") {
		n, err := p.parseStringConstructor()
		return n, true, err
	}
	if p.lookingAt("(#") {
		n, err := p.parseExtension()
		return n, true, err
	}
	switch p.peekKeyword() {
	case "try":
		return p.parseTryCatch()
	case "switch":
		return p.parseSwitch()
	case "typeswitch":
		return p.parseTypeswitch()
	case "ordered", "unordered":
		return p.parseOrderedUnordered()
	case "validate":
		return p.parseValidate()
	}
	return nil, false, nil
}

// scanExprSingle takes the run of source that forms one ExprSingle and hands
// it to the item parser.
//
// The boundary is what makes this work without a token stream: an ExprSingle
// ends at a top-level comma, and inside one of these constructs it also ends
// at the keyword that begins the next clause — "case", "default" and "catch".
// Those keywords cannot appear at nesting depth zero inside an expression:
// "case" is a reserved function name in XQuery, so nothing else at depth zero
// can be spelled that way. stops names the ones this caller has to stop at.
func (p *parser) scanExprSingle(stops ...string) (node, error) {
	p.skipSpaceAndComments()
	if n, ok, err := p.parseXQueryOnly(); ok || err != nil {
		return n, err
	}
	if n, ok, err := p.parseConstructorItem(); ok || err != nil {
		return n, err
	}
	start := p.pos
	end, err := p.scanToStop(stops)
	if err != nil {
		return nil, err
	}
	src := strings.TrimSpace(p.src[start:end])
	p.pos = end
	if src == "" {
		return nil, p.errorAt(start, "XPST0003: expected an expression")
	}
	if needsXQueryParser(src) {
		c, err := p.parseFromSource(src)
		if err != nil {
			return nil, err
		}
		return &enclosed{items: c.items}, nil
	}
	c, err := p.compileExpr(src)
	if err != nil {
		return nil, err
	}
	return &enclosed{expr: c}, nil
}

// parseConstructorItem parses a constructor if one starts here, so that a
// return clause may be one: "case 1 return <a/>" is ordinary.
func (p *parser) parseConstructorItem() (node, bool, error) {
	switch {
	case p.lookingAt("<!--"):
		n, err := p.parseDirComment()
		return n, true, err
	case p.lookingAt("<?"):
		n, err := p.parseDirPI()
		return n, true, err
	case p.lookingAt("<"):
		n, err := p.parseDirElement()
		return n, true, err
	}
	return p.parseComputed()
}

// scanToStop returns the offset at which the expression starting at p.pos
// ends: a top-level comma, one of the stop keywords at nesting depth zero, or
// the end of the source.
//
// Brackets, braces, parentheses, string literals and comments are all stepped
// over so that a comma or a keyword inside any of them is not a boundary.
// Direct constructors are the one thing this cannot step over with a depth
// count, because "<" is also less-than; they are handled by the caller, which
// reaches a constructor before it ever reaches here.
func (p *parser) scanToStop(stops []string) (int, error) {
	i := p.pos
	depth := 0
	for i < len(p.src) {
		c := p.src[i]
		switch c {
		case '\'', '"':
			end, err := skipString(p.src, i)
			if err != nil {
				return 0, err
			}
			i = end + 1
			continue
		case '(':
			if i+1 < len(p.src) && p.src[i+1] == ':' {
				end, err := skipComment(p.src, i)
				if err != nil {
					return 0, err
				}
				i = end + 1
				continue
			}
			depth++
		case ')', ']', '}':
			depth--
			if depth < 0 {
				return i, nil
			}
		case '[', '{':
			depth++
		case ',':
			if depth == 0 {
				return i, nil
			}
		default:
			if depth == 0 && len(stops) > 0 && isNameByte(c) &&
				(i == p.pos || !isNameByte(p.src[i-1])) {
				for _, kw := range stops {
					if strings.HasPrefix(p.src[i:], kw) &&
						(i+len(kw) == len(p.src) || !isNameByte(p.src[i+len(kw)])) {
						return i, nil
					}
				}
			}
		}
		i++
	}
	return i, nil
}

// consumeKeyword consumes a keyword if it is the next thing, whitespace and
// comments having been skipped first. A keyword is only a keyword when it is
// not the beginning of a longer name.
func (p *parser) consumeKeyword(kw string) bool {
	save := p.pos
	p.skipSpaceAndComments()
	if hasKeywordPrefix(p.src[p.pos:], kw) {
		p.pos += len(kw)
		return true
	}
	p.pos = save
	return false
}

// parseBracedExprSingle parses "{ Expr }" as an expression rather than as
// constructor content.
//
// try, catch, ordered, unordered and validate all take an EnclosedExpr, whose
// body is a whole Expr — a comma-separated sequence — and whose value is the
// value of that expression rather than a constructed tree. The braces are
// found the same way an enclosed expression's are, and the body is parsed as a
// query body so that it may itself hold constructors and further XQuery-only
// forms.
func (p *parser) parseBracedExprSingle() (*enclosed, error) {
	p.skipSpaceAndComments()
	if !p.lookingAt("{") {
		return nil, p.errorf("XPST0003: expected %q", "{")
	}
	end, err := findEnclosed(p.src, p.pos)
	if err != nil {
		return nil, err
	}
	body := p.src[p.pos+1 : end]
	p.pos = end + 1
	if strings.TrimSpace(body) == "" {
		return &enclosed{}, nil
	}
	inner := &parser{src: body, sc: p.sc, version: p.version}
	items, err := inner.parseQueryBody()
	if err != nil {
		return nil, err
	}
	return &enclosed{items: items}, nil
}
