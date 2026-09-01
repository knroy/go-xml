package xquery

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// parseOrderedUnordered parses [136] "ordered { Expr }" and [137]
// "unordered { Expr }", XQuery 3.1 §3.12.
//
// Both evaluate their operand and return its value unchanged. That is not a
// shortcut: §3.12 says the ordering mode governs the order in which certain
// expressions return *nodes*, and it explicitly permits a processor whose
// path expressions and FLWOR always deliver document order to treat unordered
// as a no-op — "the processor is free to ignore" the relaxation, because the
// mode grants a licence to reorder rather than an obligation to. This engine
// returns document order everywhere by construction, so it declines the
// licence, and both keywords reduce to their operand.
//
// Neither is reserved, so each commits only on a following brace.
func (p *parser) parseOrderedUnordered() (node, bool, error) {
	kw := p.peekKeyword()
	save := p.pos
	p.pos += len(kw)
	p.skipSpaceAndComments()
	if !p.lookingAt("{") {
		p.pos = save
		return nil, false, nil
	}
	body, err := p.parseBracedExprSingle()
	if err != nil {
		return nil, true, err
	}
	// [136] and [137] write the operand as "{ Expr }" with Expr required, so
	// "ordered {}" is a syntax error rather than the empty sequence.
	if body.expr == nil && body.items == nil {
		return nil, true, p.errorAt(save,
			"XPST0003: %q needs an expression", kw)
	}
	return body, true, nil
}

// extension is [104] "Pragma+ EnclosedExpr", XQuery 3.1 §3.17.
//
// §3.17 is unusually direct about what a processor that recognises none of the
// pragmas must do: if the extension expression has an enclosed expression, it
// evaluates that and ignores the pragmas entirely; if it does not, the whole
// thing is the static error XQST0079. This engine defines no pragma, so every
// extension expression takes one of those two paths.
//
// The pragmas are still parsed rather than skipped over, because their syntax
// is checked even when their content is not: a pragma's name must be a QName
// with a prefix bound to a namespace, and an unbound prefix is XPST0081
// whether or not anything would have recognised the pragma.
func (p *parser) parseExtension() (node, error) {
	start := p.pos
	for {
		p.skipSpaceAndComments()
		if !p.lookingAt("(#") {
			break
		}
		if err := p.parsePragma(); err != nil {
			return nil, err
		}
	}
	p.skipSpaceAndComments()
	if !p.lookingAt("{") {
		return nil, p.errorAt(start,
			"XQST0079: an extension expression whose pragmas are all "+
				"unrecognised must have an enclosed expression")
	}
	body, err := p.parseBracedExprSingle()
	if err != nil {
		return nil, err
	}
	// An empty enclosed expression is still an enclosed expression, so this
	// is the empty sequence rather than XQST0079.
	return body, nil
}

// parsePragma parses [105] "(# S? EQName (S PragmaContents)? #)".
func (p *parser) parsePragma() error {
	start := p.pos
	if !p.consume("(#") {
		return p.errorf("XPST0003: expected %q", "(#")
	}
	p.skipSpace()
	prefix, local, err := p.parseQName()
	if err != nil {
		return err
	}
	if prefix != "" {
		if _, ok := p.sc.ResolvePrefix(prefix); !ok {
			return p.errorAt(start,
				"XPST0081: the prefix %q is not bound to a namespace", prefix)
		}
	} else if p.sc.defaultElementNS == "" {
		// [105] requires the name to be in a namespace, and an unprefixed
		// pragma name takes the default element namespace. With none, the
		// name is in no namespace, which the grammar does not admit.
		return p.errorAt(start,
			"XPST0081: the pragma name %q is in no namespace", local)
	}
	// The content runs to the first "#)". It is not parsed: a pragma's
	// contents are whatever the implementation that recognises it says they
	// are, and this one recognises none.
	i := strings.Index(p.src[p.pos:], "#)")
	if i < 0 {
		return p.errorAt(start, "XPST0003: unterminated pragma")
	}
	// A pragma with contents must have whitespace between the name and them,
	// which is what separates "(#p:x#)" from "(#p:xy#)".
	if i > 0 && !isSpaceByte(p.src[p.pos]) {
		return p.errorAt(start,
			"XPST0003: expected space after the pragma name %q",
			qnameText(prefix, local))
	}
	p.pos += i + 2
	return nil
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// stringConstructor is [177] StringConstructor, XQuery 3.1 §3.11.6: content
// between a doubled-backtick-bracket opener and its mirror.
//
// It is a string, not a tree: the literal runs and the interpolations are
// concatenated, each interpolation being atomised and its values joined with
// single spaces the way fn:string-join with a single space would.
type stringConstructor struct{ parts []node }

// parseStringConstructor parses the backtick-bracket form.
//
// The delimiters are two backticks and a bracket, and the escape rules are
// deliberately minimal: §3.11.6 exists precisely so that a query can carry
// text with braces, quotes and ampersands in it without escaping any of them.
// So there are no entity references here, no doubled braces, and no character
// references — the only thing that ends a literal run is the closing
// bracket-backtick-backtick, or the interpolation opener.
func (p *parser) parseStringConstructor() (node, error) {
	start := p.pos
	if !p.consume("``[") {
		return nil, p.errorf("XPST0003: expected %q", "``[")
	}
	sc := &stringConstructor{}
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			sc.parts = append(sc.parts, &literalText{text: run.String()})
			run.Reset()
		}
	}
	for {
		switch {
		case p.eof():
			return nil, p.errorAt(start,
				"XPST0003: unterminated string constructor")

		case p.lookingAt("]``"):
			p.pos += 3
			flush()
			return sc, nil

		case p.lookingAt("`{"):
			flush()
			p.pos++ // step onto the "{"
			end, err := findEnclosed(p.src, p.pos)
			if err != nil {
				return nil, err
			}
			body := p.src[p.pos+1 : end]
			p.pos = end + 1
			// [180] StringConstructorInterpolation closes with "}`", so the
			// backtick after the brace is part of the delimiter.
			if !p.consume("`") {
				return nil, p.errorf("XPST0003: expected %q to close an "+
					"interpolation", "}`")
			}
			if strings.TrimSpace(body) == "" {
				sc.parts = append(sc.parts, &enclosed{})
				continue
			}
			inner := &parser{src: body, sc: p.sc, version: p.version}
			items, err := inner.parseQueryBody()
			if err != nil {
				return nil, err
			}
			sc.parts = append(sc.parts, &enclosed{items: items})

		default:
			run.WriteByte(p.src[p.pos])
			p.pos++
		}
	}
}

func (n *stringConstructor) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.sequence(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}

func (n *stringConstructor) sequence(ctx *evalContext) (xdm.Sequence, error) {
	var sb strings.Builder
	for _, part := range n.parts {
		switch v := part.(type) {
		case *literalText:
			sb.WriteString(v.text)
		case *enclosed:
			seq, err := v.sequence(ctx)
			if err != nil {
				return nil, err
			}
			s, err := joinAtomized(seq)
			if err != nil {
				return nil, err
			}
			sb.WriteString(s)
		}
	}
	return xdm.One(xdm.NewString(sb.String())), nil
}

// parseValidate parses [102] "validate (ValidationMode | type TypeName)?
// EnclosedExpr", XQuery 3.1 §3.21.
//
// The expression is parsed and refused rather than mis-parsed. Validating
// means running the schema processor over a freshly constructed tree and
// stamping the resulting PSVI type annotations onto it, and this package has
// nowhere to put those annotations: the data model here carries a type
// annotation on a node, but nothing in xdmbuild threads a validated one back
// through construction, and the in-scope schema definitions the mode needs are
// part of the prolog's schema import, which is not implemented either.
//
// So this reports XQDY0084 — "the element does not have a top-level element
// declaration in the in-scope schema definitions" — which is exactly true of
// every element here, since the in-scope schema definitions are empty. A query
// that validates against a schema it never imported gets the same answer from
// a conformant processor.
func (p *parser) parseValidate() (node, bool, error) {
	save := p.pos
	p.pos += len("validate")
	p.skipSpaceAndComments()
	switch {
	case p.consumeKeyword("lax"), p.consumeKeyword("strict"):
		p.skipSpaceAndComments()
	case p.consumeKeyword("type"):
		p.skipSpaceAndComments()
		if _, _, err := p.parseQName(); err != nil {
			return nil, true, err
		}
		p.skipSpaceAndComments()
	}
	if !p.lookingAt("{") {
		// "validate" used as a name or a function call.
		p.pos = save
		return nil, false, nil
	}
	// The body is still parsed, so that a syntax error inside it is reported
	// as one rather than hidden behind the unsupported feature.
	body, err := p.parseBracedExprSingle()
	if err != nil {
		return nil, true, err
	}
	// [102] writes the operand as an EnclosedExpr whose Expr is required, not
	// optional: "validate { }" has nothing to validate and is a syntax error
	// rather than a validation of the empty sequence.
	if body.expr == nil && body.items == nil {
		return nil, true, p.errorAt(save,
			"XPST0003: a %q expression needs an expression to validate",
			"validate")
	}
	return &validateExpr{}, true, nil
}

// validateExpr is a parsed validate expression, which fails when it runs.
type validateExpr struct{}

func (n *validateExpr) eval(out *builderRef, ctx *evalContext) error {
	_, err := n.sequence(ctx)
	return err
}

func (n *validateExpr) sequence(ctx *evalContext) (xdm.Sequence, error) {
	return nil, xdm.Errorf("XQDY0084",
		"validate has no in-scope schema definitions to validate against: "+
			"schema import is not implemented")
}
