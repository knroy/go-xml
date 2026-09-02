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
	// The operand is an EnclosedExpr, not a braced Expr:
	//
	//	[136] OrderedExpr   ::= "ordered" EnclosedExpr
	//	[137] UnorderedExpr ::= "unordered" EnclosedExpr
	//	[5]   EnclosedExpr  ::= "{" Expr? "}"
	//
	// XQuery 1.0 wrote both operands as "{" Expr "}" with Expr required; 3.1
	// routes them through EnclosedExpr, whose Expr is optional. So "ordered{}"
	// is well-formed and its value is the empty sequence — the operand is
	// absent, and both keywords return their operand's value unchanged.
	// K-OrderExpr-1a and -2a assert exactly that, under an XQ31+ dependency
	// and citing the bug that made the change.
	//
	// Left at 3.1's reading whatever the module declares, even though the
	// version is now recorded on the static context. A 1.0 module writing
	// "ordered{}" is accepted here where a conforming 1.0 processor would
	// raise XPST0003, which is a permissive divergence: nothing is given a
	// wrong answer, only a query is accepted that need not have been. Routing
	// it would mean sc.xqVersion.atLeast31() around the empty case here.
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
	// §3.17 requires a *fallback expression* where no pragma is recognised,
	// and "{}" supplies none: [104]'s EnclosedExpr has an optional Expr, and
	// with it absent there is nothing for the processor to evaluate instead
	// of the pragmas. The suite tests all four spellings of it — prefixed,
	// unprefixed and in no namespace — and expects XQST0079 for each.
	if len(body.items) == 0 {
		return nil, p.errorAt(start,
			"XQST0079: an extension expression whose pragmas are all "+
				"unrecognised must have a fallback expression")
	}
	return body, nil
}

// parsePragma parses [105] "(# S? EQName (S PragmaContents)? #)".
func (p *parser) parsePragma() error {
	start := p.pos
	if !p.consume("(#") {
		return p.errorf("XPST0003: expected %q", "(#")
	}
	p.skipSpace()
	// "Q{uri}local" names the namespace outright. It is read before the
	// QName because parseQName would take the "Q" for a local name and stop
	// at the brace.
	var prefix, local string
	if _, ok, err := p.parseBracedURI(); err != nil {
		return err
	} else if ok {
		// A braced URI may be empty — "Q{}name" is a name in no namespace,
		// which 3.1 admits; see below.
		if local = p.scanNCName(); local == "" {
			return p.errorAt(start, "XPST0003: expected a local name after %q", "Q{...}")
		}
	} else {
		var err error
		if prefix, local, err = p.parseQName(); err != nil {
			return err
		}
		if prefix != "" {
			if _, ok := p.sc.ResolvePrefix(prefix); !ok {
				return p.errorAt(start,
					"XPST0081: the prefix %q is not bound to a namespace", prefix)
			}
		}
	}
	// An unprefixed name, or one written with an empty braced URI, is a name
	// in no namespace. XQuery 3.0 did not admit that; 3.1 does, and says the
	// pragma is then simply one nothing recognises — which is the state every
	// pragma is in here anyway. So there is nothing to report: the name is
	// well-formed, and whether the extension expression is an error is
	// decided by parseExtension, on whether an enclosed expression follows.
	//
	// Left at 3.1's reading whatever the module declares, on the same
	// permissive-divergence reasoning as parseOrderedUnordered above: a 1.0 or
	// 3.0 module writing an unprefixed pragma name is accepted rather than
	// refused. Routing it would mean sc.xqVersion.atLeast31() here.
	//
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
// Schema import is not implemented, so the in-scope schema definitions are
// always empty. What that means for the expression depends on the mode, and
// the two halves are not the same answer:
//
// Strict validation demands a declaration. §3.21 makes it XQDY0084 when "the
// element does not have a top-level element declaration in the in-scope schema
// definitions", which is true of every element here, so "validate strict" gets
// the error a conformant processor with no imports would also give it.
//
// Lax validation does not demand one. XSD 1.0 §3.3.4 clause 1.2 says lax
// assessment of an element with no matching declaration is *skipped* — the
// outcome is "notKnown", which is not an error — and the node comes through
// unannotated. So "validate lax" over a constructed tree with nothing to
// validate against succeeds and yields its operand. Refusing it was reading
// the strict rule onto the lax keyword.
//
// The mode is therefore kept rather than parsed and dropped. Type annotations
// still cannot come from a schema, but they can come from xsi:type when it
// names a built-in, which validateExpr handles; that much needs no import,
// since the XSD built-ins are always available.
func (p *parser) parseValidate() (node, bool, error) {
	save := p.pos
	lax := false
	p.pos += len("validate")
	p.skipSpaceAndComments()
	switch {
	case p.consumeKeyword("lax"):
		lax = true
		p.skipSpaceAndComments()
	case p.consumeKeyword("strict"):
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
	// as one rather than hidden behind an unsupported mode.
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
	return &validateExpr{body: body, lax: lax}, true, nil
}

// validateExpr is a parsed validate expression. Lax validation runs; every
// other mode fails, because it needs schema definitions that were never
// imported.
type validateExpr struct {
	body *enclosed
	lax  bool
}

func (n *validateExpr) eval(out *builderRef, ctx *evalContext) error {
	_, err := n.sequence(ctx)
	return err
}

func (n *validateExpr) sequence(ctx *evalContext) (xdm.Sequence, error) {
	// §3.21 requires the operand to be exactly one element or document node.
	// That is a property of the operand alone, so it is answered here even
	// though the validation it precedes cannot be: "validate { 1 }" is
	// XQTY0030 in a processor with every schema in the world imported.
	seq, err := n.body.sequence(ctx)
	if err != nil {
		return nil, err
	}
	if len(seq) != 1 {
		return nil, xdm.Errorf("XQTY0030",
			"validate requires exactly one element or document node, "+
				"and its operand is a sequence of %d items", len(seq))
	}
	root, ok := seq[0].(*xdm.Node)
	if !ok || (root.Kind != xdm.KindElement && root.Kind != xdm.KindDocument) {
		return nil, xdm.Errorf("XQTY0030",
			"validate requires an element or document node")
	}
	if !n.lax {
		return nil, xdm.Errorf("XQDY0084",
			"validate has no in-scope schema definitions to validate "+
				"against: schema import is not implemented")
	}
	// Lax assessment with empty in-scope schema definitions finds no
	// declaration for any element, and XSD 1.0 §3.3.4 clause 1.2 makes that
	// a skipped assessment rather than a failure. The tree therefore comes
	// through as it was built, with one exception below.
	annotateBuiltinXSIType(root)
	return seq, nil
}

// annotateBuiltinXSIType stamps the type annotation an xsi:type attribute
// names, for the built-in types alone, over a tree being laxly validated.
//
// Lax assessment of an element carrying xsi:type is not skipped the way an
// undeclared element is: XSD 1.0 §3.3.4 clause 1.2.1.2 resolves the type from
// the attribute and assesses against it. The XSD built-ins need no import to
// be available — they are always in scope — so this much of lax validation is
// answerable here even though schema import is not implemented, and a type
// from any other namespace is left alone because that one really would need
// the import.
//
// The annotation matters beyond its own name: SetTypeAnnotation turns on the
// is-id and is-idrefs properties for the ID family, and those are what
// fn:id, fn:element-with-id and fn:idref look the node up by. An element
// whose content is <empnr xsi:type="xs:ID">E21256</empnr> is an ID in the
// data model, and without this it was an ordinary untyped element that
// fn:id could not find.
//
// Nothing here validates the content against the type it claims. A lax
// assessment that found the value invalid would report it, and that check
// belongs with a real schema processor; what this provides is the annotation,
// which is the part the data model's ID properties are derived from.
func annotateBuiltinXSIType(n *xdm.Node) {
	if n.Kind == xdm.KindElement {
		if a := n.Attr(xdm.NSXSI, "type"); a != nil {
			// The value is a QName in the element's namespace scope, so the
			// prefix is resolved rather than assumed to be "xs": a query is
			// free to bind the XSD namespace to any prefix it likes.
			prefix, local := "", strings.TrimSpace(a.Value)
			if i := strings.IndexByte(local, ':'); i >= 0 {
				prefix, local = local[:i], local[i+1:]
			}
			if uri, ok := n.LookupPrefix(prefix); ok && uri == xdm.NSXS {
				n.SetTypeAnnotation(local)
			}
		}
	}
	for _, c := range n.Children {
		annotateBuiltinXSIType(c)
	}
}
