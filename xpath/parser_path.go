package xpath

import (
	"math/big"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// parsePath parses a path expression.
//
// The three leading forms are distinct: "/" alone selects the document root,
// "/step..." is an absolute path, and "//step..." abbreviates
// "/descendant-or-self::node()/step...".
func (p *Parser) parsePath() (Expr, error) {
	switch {
	case p.peekIs(TokOp, "//"):
		p.pos++
		rest, err := p.parseRelativePath()
		if err != nil {
			return nil, err
		}
		steps := append([]Expr{descendantOrSelfStep()}, rest...)
		return &PathExpr{Root: true, Steps: steps}, nil

	case p.peekIs(TokOp, "/"):
		p.pos++
		// A lone "/" is the whole expression when nothing that can start a
		// step follows. "/ * 2" is therefore division of the root by 2 —
		// which is a type error at runtime, but a parse this way per the spec.
		if !p.startsStep() {
			return &PathExpr{Root: true}, nil
		}
		steps, err := p.parseRelativePath()
		if err != nil {
			return nil, err
		}
		return &PathExpr{Root: true, Steps: steps}, nil
	}

	steps, err := p.parseRelativePath()
	if err != nil {
		return nil, err
	}
	if len(steps) == 1 {
		// A single step with no "/" is just that expression; wrapping it in a
		// PathExpr would force document-order sorting that the spec does not
		// apply to a bare primary expression.
		if _, isStep := steps[0].(*Step); !isStep {
			return steps[0], nil
		}
	}
	return &PathExpr{Steps: steps}, nil
}

// startsStep reports whether the current token can begin a step. Used to
// distinguish "/" as a complete expression from "/" as a path separator.
func (p *Parser) startsStep() bool {
	t := p.cur()
	switch t.Kind {
	case TokName, TokWildcard, TokVar, TokNumber, TokString:
		return true
	case TokOp:
		switch t.Val {
		case "@", "(", ".", "..":
			return true
		}
	}
	return false
}

func (p *Parser) parseRelativePath() ([]Expr, error) {
	first, err := p.parseStepExpr()
	if err != nil {
		return nil, err
	}
	steps := []Expr{first}
	for {
		switch {
		case p.peekIs(TokOp, "//"):
			p.pos++
			steps = append(steps, descendantOrSelfStep())
		case p.peekIs(TokOp, "/"):
			p.pos++
		default:
			return steps, nil
		}
		next, err := p.parseStepExpr()
		if err != nil {
			return nil, err
		}
		steps = append(steps, next)
	}
}

// descendantOrSelfStep builds the step that "//" abbreviates.
func descendantOrSelfStep() *Step {
	return &Step{Axis: AxisDescendantOrSelf, Test: &KindTest{Any: true}}
}

// parseStepExpr parses either an axis step or a filtered primary expression.
func (p *Parser) parseStepExpr() (Expr, error) {
	if step, ok, err := p.tryParseAxisStep(); err != nil {
		return nil, err
	} else if ok {
		return step, nil
	}
	base, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	preds, err := p.parsePredicates()
	if err != nil {
		return nil, err
	}
	if len(preds) == 0 {
		return base, nil
	}
	return &FilterExpr{Base: base, Predicates: preds}, nil
}

// tryParseAxisStep parses an axis step if one is present, reporting ok=false
// (without consuming tokens) when the current position starts a primary
// expression instead.
func (p *Parser) tryParseAxisStep() (*Step, bool, error) {
	start := p.pos
	step := &Step{Axis: AxisChild}

	switch {
	case p.peekIs(TokOp, "."):
		// A bare "." is the context *item*, not the step "self::node()".
		// The distinction matters because the context item may be an atomic
		// value — inside xsl:analyze-string or a for-expression over strings —
		// and a step would reject it as "not a node". Only when predicates
		// follow does it become a step, since predicates imply node selection.
		p.pos++
		preds, err := p.parsePredicates()
		if err != nil {
			return nil, false, err
		}
		if len(preds) == 0 {
			// Rewind and let parsePrimary produce a ContextItem.
			p.pos = start
			return nil, false, nil
		}
		step.Axis, step.Test = AxisSelf, &KindTest{Any: true}
		step.Predicates = preds
		return step, true, nil

	case p.peekIs(TokOp, ".."):
		p.pos++
		step.Axis, step.Test = AxisParent, &KindTest{Any: true}
		preds, err := p.parsePredicates()
		if err != nil {
			return nil, false, err
		}
		step.Predicates = preds
		return step, true, nil

	case p.peekIs(TokOp, "@"):
		p.pos++
		step.Axis = AxisAttribute

	case p.cur().Kind == TokName:
		// "name::" is an axis; "name(" is a function call; bare "name" is a
		// name test on the child axis.
		if p.pos+1 < len(p.toks) && p.toks[p.pos+1].Kind == TokOp &&
			p.toks[p.pos+1].Val == "::" {
			ax, ok := axisNames[p.cur().Val]
			if !ok {
				return nil, false, p.errorf("unknown axis %q", p.cur().Val)
			}
			step.Axis = ax
			p.pos += 2
		} else if p.pos+1 < len(p.toks) && p.toks[p.pos+1].Kind == TokOp &&
			p.toks[p.pos+1].Val == "(" && !isKindTestName(p.cur().Val) {
			return nil, false, nil // function call: let parsePrimary handle it
		}
	}

	test, ok, err := p.tryParseNodeTest(step.Axis)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		p.pos = start
		return nil, false, nil
	}
	step.Test = test

	// An unabbreviated "attribute()" or "schema-attribute()" kind test
	// implies the attribute axis. Without this, "attribute()" would be a
	// child-axis step and select nothing at all, since attributes are never
	// children.
	if kt, isKind := test.(*KindTest); isKind && !kt.Any &&
		kt.Kind == xdm.KindAttribute && step.Axis == AxisChild {
		step.Axis = AxisAttribute
	}
	preds, err := p.parsePredicates()
	if err != nil {
		return nil, false, err
	}
	step.Predicates = preds
	return step, true, nil
}

// kindTestNames are the node tests that look like function calls but are not.
func isKindTestName(s string) bool {
	switch s {
	case "node", "text", "comment", "processing-instruction",
		"element", "attribute", "document-node", "schema-element",
		"schema-attribute", "namespace-node", "item":
		return true
	}
	return false
}

func (p *Parser) tryParseNodeTest(axis Axis) (NodeTest, bool, error) {
	t := p.cur()
	switch t.Kind {
	case TokWildcard:
		p.pos++
		return p.wildcardTest(t.Val)

	case TokName:
		if isKindTestName(t.Val) && p.pos+1 < len(p.toks) &&
			p.toks[p.pos+1].Kind == TokOp && p.toks[p.pos+1].Val == "(" {
			kt, err := p.parseKindTest()
			return kt, err == nil, err
		}
		p.pos++
		name, err := p.resolveElementName(t.Val, axis)
		if err != nil {
			return nil, false, err
		}
		return &NameTest{Name: name}, true, nil
	}
	return nil, false, nil
}

// wildcardTest builds the node test for "*", "prefix:*" and "*:local".
func (p *Parser) wildcardTest(val string) (NodeTest, bool, error) {
	switch {
	case val == "*":
		return &NameTest{AnyURI: true, AnyLocal: true}, true, nil

	case strings.HasPrefix(val, "*:"):
		return &NameTest{AnyURI: true, Name: xdm.QName{Local: val[2:]}}, true, nil

	default: // "prefix:*"
		prefix := strings.TrimSuffix(val, ":*")
		uri, ok := p.ns.ResolvePrefix(prefix)
		if !ok {
			return nil, false, p.errorf("XPST0081: unbound namespace prefix %q", prefix)
		}
		return &NameTest{AnyLocal: true, Name: xdm.QName{Prefix: prefix, URI: uri}}, true, nil
	}
}

func (p *Parser) parseKindTest() (NodeTest, error) {
	name := p.cur().Val
	p.pos++
	if err := p.expectOp("("); err != nil {
		return nil, err
	}

	kt := &KindTest{}
	switch name {
	case "node":
		kt.Any = true
	case "text":
		kt.Kind = xdm.KindText
	case "comment":
		kt.Kind = xdm.KindComment
	case "namespace-node":
		kt.Kind = xdm.KindNamespace
		// namespace-node() takes no argument. With one it is not a kind test
		// at all but a call to a function by that name, and no such function
		// exists: XPST0017 rather than the generic syntax error.
		if !p.peekIs(TokOp, ")") {
			return nil, p.errorf(
				"XPST0017: namespace-node() takes no arguments")
		}
	case "document-node":
		kt.Kind = xdm.KindDocument
		// document-node() may name the kind of its root element:
		// document-node(element(invoice)) matches only a document whose
		// element child is an <invoice>. The inner test was not parsed at
		// all, so the whole form was a syntax error.
		if t := p.cur(); t.Kind == TokName &&
			(t.Val == "element" || t.Val == "schema-element") {
			inner, err := p.parseKindTest()
			if err != nil {
				return nil, err
			}
			kt.Content = inner
		}
	case "item":
		// item() is a sequence type, not a node test: it matches atomic
		// values, which no axis yields. It is legal in "instance of" and
		// "treat as", where parseSequenceType handles it directly, but as a
		// path step it is a syntax error rather than a step that happens to
		// select nothing.
		return nil, p.errorf("item() is not a node test")
	case "processing-instruction":
		kt.Kind = xdm.KindPI
		// The target may be given as a name or a string literal.
		if t := p.cur(); t.Kind == TokName || t.Kind == TokString {
			kt.Name = &xdm.QName{Local: t.Val}
			kt.HasName = true
			p.pos++
		}
	case "element", "attribute", "schema-element", "schema-attribute":
		kt.Kind = xdm.KindElement
		if name == "attribute" || name == "schema-attribute" {
			kt.Kind = xdm.KindAttribute
		}
		// The schema- forms name a globally-declared element or attribute, so
		// the name is required and a wildcard will not do: neither
		// schema-attribute() nor schema-attribute(*) names a declaration.
		if strings.HasPrefix(name, "schema-") {
			if p.cur().Kind != TokName {
				return nil, p.errorf("%s() requires a declared name", name)
			}
			// Resolving the name comes first: an unbound prefix is XPST0081
			// and is reported whatever the name would have meant.
			axis := AxisChild
			if kt.Kind == xdm.KindAttribute {
				axis = AxisAttribute
			}
			if _, err := p.resolveElementName(p.cur().Val, axis); err != nil {
				return nil, err
			}
			// The name must then resolve to a global declaration in the
			// in-scope schema. Without one — no xsl:import-schema, or a
			// schema that does not declare this name — there is nothing for
			// the name to mean, and XPST0008 says exactly that.
			if !schemaDeclared(p.cur().Val, p.ns, kt.Kind == xdm.KindAttribute) {
				return nil, p.errorf(
					"XPST0008: %s(%s) refers to a schema declaration, and no schema is imported",
					name, p.cur().Val)
			}
			qn, err := p.resolveElementName(p.cur().Val, axis)
			if err != nil {
				return nil, err
			}
			kt.Name = &qn
			kt.HasName = true
			// A schema-element test also admits the members of the named
			// declaration's substitution group, which this records so that
			// matching can consult the schema rather than the name alone.
			kt.SchemaDeclared = true
			p.pos++
		}
		if t := p.cur(); t.Kind == TokName {
			axis := AxisChild
			if kt.Kind == xdm.KindAttribute {
				axis = AxisAttribute
			}
			qn, err := p.resolveElementName(t.Val, axis)
			if err != nil {
				return nil, err
			}
			kt.Name = &qn
			kt.HasName = true
			p.pos++
			p.skipTypeAnnotation()
		} else if p.cur().Kind == TokWildcard {
			p.pos++ // element(*) is the same as element()
			// element(*, type) and attribute(*, type) are as legal as
			// the named forms; only the name may be a wildcard, not
			// the whole second argument, so the annotation is skipped
			// here too rather than only after a name.
			p.skipTypeAnnotation()
		}
	default:
		return nil, p.errorf("unknown kind test %q", name)
	}

	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	return kt, nil
}

// skipTypeAnnotation consumes the ", type" of element(name, type) and its
// attribute() counterpart.
//
// The annotation is accepted and ignored: this engine carries no in-scope
// schema, so there is no type hierarchy to check a node's annotation against.
// Ignoring it makes the test the name test alone, which is weaker than the
// spec's but never wrong about the name — whereas failing to parse it rejects
// a legal expression outright.
func (p *Parser) skipTypeAnnotation() {
	if _, ok := p.acceptOp(","); !ok {
		return
	}
	if p.cur().Kind == TokName {
		p.pos++
	}
	p.acceptOp("?")
}

func (p *Parser) parsePredicates() ([]Expr, error) {
	var out []Expr
	for p.peekIs(TokOp, "[") {
		p.pos++
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp("]"); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// parsePrimary parses a literal, variable reference, parenthesised
// expression, context item, or function call.
func (p *Parser) parsePrimary() (Expr, error) {
	t := p.cur()
	switch t.Kind {
	case TokNumber:
		p.pos++
		return &Literal{Val: numericLiteral(t)}, nil

	case TokString:
		p.pos++
		return &Literal{Val: xdm.NewString(t.Val)}, nil

	case TokVar:
		p.pos++
		name, err := p.resolveVarName(t.Val)
		if err != nil {
			return nil, err
		}
		return &VarRef{Name: name}, nil

	case TokOp:
		switch t.Val {
		case "(":
			p.pos++
			// "()" is the empty sequence.
			if p.peekIs(TokOp, ")") {
				p.pos++
				return &SequenceExpr{}, nil
			}
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return e, nil
		case ".":
			p.pos++
			return &ContextItem{}, nil
		}

	case TokName:
		// A function call: name followed by "(".
		if p.pos+1 < len(p.toks) && p.toks[p.pos+1].Kind == TokOp &&
			p.toks[p.pos+1].Val == "(" {
			return p.parseFunctionCall()
		}
	}
	return nil, p.errorf("unexpected %q", t.Val)
}

// numericLiteral converts a numeric token to a typed atomic value. The
// lexical form determines the type, which is why the lexer recorded it.
func numericLiteral(t Token) *xdm.Atomic {
	switch t.NumType {
	case numInteger:
		r := new(big.Rat)
		if _, ok := r.SetString(t.Val); ok {
			return xdm.NewIntegerFromRat(r)
		}
		return xdm.NewInteger(int64(t.Num))
	case numDecimal:
		r := new(big.Rat)
		if _, ok := r.SetString(t.Val); ok {
			return xdm.NewDecimal(r)
		}
		return xdm.NewDouble(t.Num)
	default:
		return xdm.NewDouble(t.Num)
	}
}

func (p *Parser) parseFunctionCall() (Expr, error) {
	nameTok := p.cur()
	// A handful of names are reserved by the grammar: they introduce a kind
	// test or a type, so they cannot name a function however the expression
	// continues. Reaching here with one means the expression is malformed —
	// "item()" in a value position is a syntax error, not a call to an unknown
	// function — so it is XPST0003 rather than XPST0017.
	if isReservedFunctionName(nameTok.Val) {
		return nil, p.errorf("%q is a reserved name and cannot be called",
			nameTok.Val)
	}
	p.pos++
	if err := p.expectOp("("); err != nil {
		return nil, err
	}

	name, err := p.resolveFunctionName(nameTok.Val)
	if err != nil {
		return nil, err
	}

	var args []Expr
	if !p.peekIs(TokOp, ")") {
		for {
			a, err := p.parseExprSingle()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if _, ok := p.acceptOp(","); !ok {
				break
			}
		}
	}
	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	if q, ok, err := p.foldQNameConstructor(name, args); err != nil {
		return nil, err
	} else if ok {
		return q, nil
	}
	if c, ok := p.foldSchemaConstructor(name, args); ok {
		return c, nil
	}
	return &FuncCall{Name: name, Args: args}, nil
}

// foldSchemaConstructor rewrites a call on an imported schema type into a cast.
//
// Importing a schema makes a constructor function available for each atomic
// type it defines, and that constructor is defined as a cast: foo:size("3") is
// "3" cast as foo:size?. Nothing registers such a function in the library,
// because the set of them is not known until a schema is imported, so the call
// is turned into the cast it is defined to be while the schema hook is still
// reachable. Without this, importing a schema gave the type to "treat as" but
// left every constructor call reported as an unknown function.
//
// Only a one-argument call qualifies. A built-in xs: name never reaches here:
// those are registered in the library and resolved before this point.
func (p *Parser) foldSchemaConstructor(name xdm.QName, args []Expr) (Expr, bool) {
	if len(args) != 1 || name.URI == xdm.NSXS {
		return nil, false
	}
	lex := name.Local
	if name.Prefix != "" {
		lex = name.Prefix + ":" + name.Local
	}
	prim, isAtomic, found := schemaTypeOf(lex, p.ns)
	if !found || !isAtomic {
		return nil, false
	}
	return &CastExpr{
		Operand: args[0],
		Type: SequenceType{
			AtomicType:    prim,
			HasAtomicType: true,
			SchemaType:    lex,
			Occurrence:    "?",
		},
	}, true
}

// foldQNameConstructor rewrites xs:QName("prefix:local") into a QName literal.
//
// This constructor is resolved here rather than at run time because a QName
// value carries the namespace URI, not the prefix, and the binding for that
// prefix comes from the static context of the expression — which exists during
// parsing and is gone by the time the function would be called. That is also
// why the spec restricts the argument to a string literal: there is no answer
// for xs:QName($computed), so a non-literal argument is rejected rather than
// resolved against whatever namespaces happen to be in scope at the call site.
func (p *Parser) foldQNameConstructor(name xdm.QName, args []Expr) (Expr, bool, error) {
	if name.URI != xdm.NSXS || name.Local != "QName" || len(args) != 1 {
		return nil, false, nil
	}
	lit, ok := args[0].(*Literal)
	if !ok || lit.Val.Type != xdm.TypeString {
		return nil, false, xdm.Errorf("XPTY0004",
			"xs:QName() requires a string literal: the prefix must be resolvable "+
				"in the static context of the expression")
	}
	lex := strings.TrimSpace(lit.Val.Str())
	prefix, local := "", lex
	if i := strings.Index(lex, ":"); i >= 0 {
		prefix, local = lex[:i], lex[i+1:]
	}
	// FORG0001 rather than FOCA0002: the argument is a string being converted
	// to a QName, so an unusable lexical form is a bad *value* for the target
	// type rather than an argument of the wrong type.
	//
	// Both halves must be NCNames. Checking only for an empty local part let
	// xs:QName(":x") through — the prefix is empty, the local part is not, and
	// the colon simply vanished — which is the same gap the runtime
	// constructor had.
	if local == "" || !isNCName(local) ||
		(strings.Contains(lex, ":") && !isNCName(prefix)) {
		return nil, false, xdm.Errorf("FORG0001",
			"xs:QName(%q) is not a valid lexical QName", lex)
	}
	uri := ""
	if prefix != "" {
		u, found := p.ns.ResolvePrefix(prefix)
		if !found {
			// FONS0004 is the namespace-function code for a prefix with no
			// binding. XPST0081 is the *static* form, used where an
			// expression names an unbound prefix directly; here the prefix
			// arrives as a string argument, so it is the function's error.
			return nil, false, xdm.Errorf("FONS0004",
				"xs:QName(%q): prefix %q is not bound", lex, prefix)
		}
		uri = u
	}
	// An unprefixed name takes the default *element* namespace only in some
	// contexts; for xs:QName the spec says it takes the default namespace for
	// elements and types, which is what DefaultElementNamespace reports.
	if prefix == "" {
		uri = p.ns.DefaultElementNamespace()
	}
	return &Literal{Val: xdm.NewQNameValue(
		xdm.QName{URI: uri, Prefix: prefix, Local: local})}, true, nil
}

// --- Name resolution --------------------------------------------------------

// resolveElementName resolves a lexical QName used as a name test.
//
// An unprefixed name takes the default element namespace on element-bearing
// axes, but never on the attribute axis: unprefixed attributes are always in
// no namespace, which is the single most commonly mis-implemented rule here.
func (p *Parser) resolveElementName(lex string, axis Axis) (xdm.QName, error) {
	prefix, local := xdm.SplitQName(lex)
	if prefix == "" {
		uri := ""
		if axis != AxisAttribute && axis != AxisNamespace {
			uri = p.ns.DefaultElementNamespace()
		}
		return xdm.QName{Local: local, URI: uri}, nil
	}
	uri, ok := p.ns.ResolvePrefix(prefix)
	if !ok {
		return xdm.QName{}, p.errorf("XPST0081: unbound namespace prefix %q", prefix)
	}
	return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
}

// resolveVarName resolves a variable's QName. Unprefixed variables are in no
// namespace; the default element namespace does not apply.
func (p *Parser) resolveVarName(lex string) (xdm.QName, error) {
	prefix, local := xdm.SplitQName(lex)
	if prefix == "" {
		return xdm.QName{Local: local}, nil
	}
	uri, ok := p.ns.ResolvePrefix(prefix)
	if !ok {
		return xdm.QName{}, p.errorf("XPST0081: unbound namespace prefix %q", prefix)
	}
	return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
}

// resolveFunctionName resolves a function name, applying the default function
// namespace to unprefixed names.
func (p *Parser) resolveFunctionName(lex string) (xdm.QName, error) {
	prefix, local := xdm.SplitQName(lex)
	if prefix == "" {
		return xdm.QName{URI: p.ns.DefaultFunctionNamespace(), Local: local}, nil
	}
	uri, ok := p.ns.ResolvePrefix(prefix)
	if !ok {
		return xdm.QName{}, p.errorf("XPST0081: unbound namespace prefix %q", prefix)
	}
	return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
}

// parseSequenceType parses a type annotation used by instance of, cast,
// castable and treat.
func (p *Parser) parseSequenceType() (SequenceType, error) {
	var st SequenceType
	t := p.cur()

	if t.Kind == TokName && t.Val == "empty-sequence" {
		p.pos++
		if err := p.expectOp("("); err != nil {
			return st, err
		}
		if err := p.expectOp(")"); err != nil {
			return st, err
		}
		st.Empty = true
		return st, nil
	}

	if t.Kind == TokName && isKindTestName(t.Val) &&
		p.pos+1 < len(p.toks) && p.toks[p.pos+1].Val == "(" {
		if t.Val == "item" {
			// item() is the one kind test with no body, but the closing paren
			// still has to be there. Skipping a fixed three tokens assumed it
			// was, so "3 treat as item(" moved the position past the end of
			// the input instead of reporting a syntax error.
			p.pos++ // item
			if err := p.expectOp("("); err != nil {
				return st, err
			}
			if err := p.expectOp(")"); err != nil {
				return st, err
			}
			st.ItemType = nil
		} else {
			kt, err := p.parseKindTest()
			if err != nil {
				return st, err
			}
			st.ItemType = kt
		}
	} else if t.Kind == TokName {
		p.pos++
		code, ok := atomicTypeByName(t.Val, p.ns)
		if !ok {
			// An unbound prefix and an unknown local name are different
			// errors. XPST0051 says the type does not exist; XPST0081 says
			// the name could not be resolved at all, which is what an unbound
			// prefix means and which has to be reported first.
			if prefix, _, found := strings.Cut(t.Val, ":"); found {
				if _, found := p.ns.ResolvePrefix(prefix); !found {
					return st, p.errorf(
						"XPST0081: unbound namespace prefix %q", prefix)
				}
			}
			// An unprefixed name followed by "(" is a kind test that does not
			// exist — "document()" rather than "document-node()" — which is a
			// grammar error rather than an unknown *type*. XPST0051 is for a
			// name in type position that names no type; this name is not in
			// type position at all.
			if p.cur().Kind == TokOp && p.cur().Val == "(" {
				return st, p.errorf("%q is not a kind test", t.Val)
			}
			// A name the built-in table does not know may still be a type,
			// if a schema was imported. Asking only here is what keeps a
			// schema from redefining xs:integer, and keeps the built-in
			// path free of a map lookup.
			// Falling through to the occurrence indicator below is the whole
			// point of not returning here. Returning early gave an imported
			// schema type no occurrence indicator at all, so "foo:testType*"
			// reported a syntax error at the "*" while "xs:integer*" parsed.
			if prim, isAtomic, found := schemaTypeOf(t.Val, p.ns); found {
				st.SchemaType = t.Val
				if isAtomic {
					st.AtomicType, st.HasAtomicType = prim, true
				}
				goto occurrence
			}
			return st, p.errorf("XPST0051: unknown type %q", t.Val)
		}
		st.AtomicType, st.HasAtomicType = code, true
		// Keep the written name when it is a derived type, so that a cast can
		// still apply the facet the code alone cannot express.
		if local := localTypeName(t.Val); local != "" {
			st.FacetName = local
		}
	} else {
		return st, p.errorf("expected a type, got %q", t.Val)
	}

occurrence:
	// At most one occurrence indicator, and only immediately after the type.
	// "xs:integer ? * 3" is an optional integer multiplied by 3, not an
	// occurrence indicator followed by a second one: taking both left the "3"
	// with no operator in front of it.
	if occ, ok := p.acceptOp("?", "*", "+"); ok {
		st.Occurrence = occ
		return st, nil
	}
	// "*" after a type name is the occurrence indicator, but the lexer cannot
	// know that: it disambiguates "*" by whether an operand preceded it, and a
	// type name is not one. It therefore arrives as a wildcard token, and
	// "xs:integer*" would not parse without accepting that spelling here.
	if p.cur().Kind == TokWildcard && p.cur().Val == "*" {
		p.pos++
		st.Occurrence = "*"
	}
	return st, nil
}

// atomicTypeByName maps a lexical type name to a TypeCode. Only the xs:
// namespace is recognised; a prefix bound elsewhere is not a built-in type.
func atomicTypeByName(lex string, ns NamespaceResolver) (xdm.TypeCode, bool) {
	prefix, local := xdm.SplitQName(lex)
	if prefix != "" {
		uri, ok := ns.ResolvePrefix(prefix)
		if !ok || uri != xdm.NSXS {
			return 0, false
		}
	}
	switch local {
	case "string":
		return xdm.TypeString, true
	case "boolean":
		return xdm.TypeBoolean, true
	case "decimal":
		return xdm.TypeDecimal, true
	case "integer", "int", "long", "short", "byte",
		"nonNegativeInteger", "positiveInteger",
		"nonPositiveInteger", "negativeInteger",
		"unsignedInt", "unsignedLong", "unsignedShort", "unsignedByte":
		// The integer subtypes are xs:integer values with a range facet. The
		// code carries the arithmetic; the caller records the name so that
		// the bound can still be enforced on a cast.
		return xdm.TypeInteger, true
	case "double":
		return xdm.TypeDouble, true
	case "float":
		return xdm.TypeFloat, true
	case "anyURI":
		return xdm.TypeAnyURI, true
	case "QName":
		return xdm.TypeQName, true
	case "date":
		return xdm.TypeDate, true
	case "time":
		return xdm.TypeTime, true
	case "dateTimeStamp":
		// xs:dateTime with explicitTimezone="required". The code carries
		// the arithmetic; the written name is kept so that a cast can
		// still enforce the facet, exactly as for the integer subtypes.
		return xdm.TypeDateTime, true
	case "dateTime":
		return xdm.TypeDateTime, true
	case "duration":
		return xdm.TypeDuration, true
	case "yearMonthDuration":
		return xdm.TypeYearMonthDuration, true
	case "dayTimeDuration":
		return xdm.TypeDayTimeDuration, true
	case "untypedAtomic":
		return xdm.TypeUntypedAtomic, true
	case "gYear":
		return xdm.TypeGYear, true
	case "gYearMonth":
		return xdm.TypeGYearMonth, true
	case "gMonth":
		return xdm.TypeGMonth, true
	case "gMonthDay":
		return xdm.TypeGMonthDay, true
	case "gDay":
		return xdm.TypeGDay, true
	case "hexBinary":
		return xdm.TypeHexBinary, true
	case "base64Binary":
		return xdm.TypeBase64Binary, true
	case "normalizedString", "token", "language", "Name", "NCName",
		"ID", "IDREF", "ENTITY", "NMTOKEN":
		return xdm.TypeString, true
	case "anySimpleType", "anyAtomicType", "NOTATION":
		// These are abstract: they name a position in the type hierarchy
		// rather than a type anything can be. They resolve — "instance of
		// xs:anyAtomicType" is a legal question — but casting to one is
		// XPST0080, which is checked where the target is used rather than
		// here, so that "instance of" keeps working.
		return xdm.TypeString, true
	}
	return 0, false
}

// localTypeName returns the local part of a type name written with a prefix,
// or "" when the name is not a facet-bearing derived type.
func localTypeName(name string) string {
	if i := strings.Index(name, ":"); i >= 0 {
		name = name[i+1:]
	}
	// dateTimeStamp is named here for the same reason as the range- and
	// string-facet types: its code is xs:dateTime, so the written name is
	// the only thing that still carries explicitTimezone="required".
	if hasRangeFacet(name) || hasStringFacet(name) || isAbstractType(name) ||
		name == "dateTimeStamp" {
		return name
	}
	return ""
}

// isAbstractType reports whether a type name is abstract: it names a position
// in the hierarchy rather than a type a value can have. Casting to one is
// XPST0080; asking "instance of" is legal.
func isAbstractType(local string) bool {
	switch local {
	case "anyAtomicType", "anySimpleType", "NOTATION", "anyType":
		return true
	}
	return false
}

// isReservedFunctionName reports whether name is reserved by the grammar.
//
// These are the kind tests and the sequence-type keywords. They appear in
// type position, where the parser reaches them through parseSequenceType or
// parseKindTest; seeing one where a function call is expected means the
// expression is malformed.
func isReservedFunctionName(name string) bool {
	switch name {
	case "attribute", "comment", "document-node", "element", "empty-sequence",
		"if", "item", "node", "processing-instruction", "schema-attribute",
		"schema-element", "text", "typeswitch", "namespace-node":
		return true
	}
	return false
}
