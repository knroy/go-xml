package xpath

import (
	"math/big"
	"strconv"
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
		case "[", "?":
			// A square-bracket array constructor and a unary lookup both begin
			// an expression in 3.1. Neither can be confused with a predicate
			// or an occurrence indicator here, because startsStep is asked
			// where a step is expected rather than where one is continued.
			return p.version.atLeast31()
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
	return p.parsePostfix(base)
}

// parsePostfix applies the postfix chain of production [48]:
// PostfixExpr ::= PrimaryExpr (Predicate | ArgumentList)*.
//
// Predicates and argument lists interleave, so "$f[1](2)" and "$f(2)[1]" are
// both legal and mean different things. They are therefore consumed in one
// loop rather than as two passes; running predicates first would reorder them
// against the calls.
func (p *Parser) parsePostfix(base Expr) (Expr, error) {
	for {
		if p.peekIs(TokOp, "[") {
			pred, err := p.parsePredicates()
			if err != nil {
				return nil, err
			}
			if len(pred) == 0 {
				return base, nil
			}
			base = &FilterExpr{Base: base, Predicates: pred}
			continue
		}
		// An argument list here is a dynamic call on whatever the base
		// produced. It follows the reference floor rather than the parsed
		// version, for the same reason "#N" does and because the two are
		// halves of one feature: a reference the module may write but not
		// call is of no use to it. system-property-023, regex-090 and
		// for-each-group-090 each bind a reference in a global and then
		// invoke it. Under a 2.0 *processor* a "(" after a complete primary
		// expression is still the syntax error it always was.
		if p.refVersion().atLeast30() && p.peekIs(TokOp, "(") {
			args, err := p.parseArgumentList()
			if err != nil {
				return nil, err
			}
			base = &DynamicCall{Target: base, Args: args}
			continue
		}
		// The lookup operator reaches into a map or an array. It is 3.1, and
		// under an earlier version "?" after a primary is still the argument
		// placeholder it was, which parseArgumentList handles.
		if p.version.atLeast31() && p.peekIs(TokOp, "?") {
			look, err := p.parseLookup(base)
			if err != nil {
				return nil, err
			}
			base = look
			continue
		}
		return base, nil
	}
}

// parseArgumentList parses "(" (ExprSingle ("," ExprSingle)*)? ")".
func (p *Parser) parseArgumentList() ([]Expr, error) {
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	var args []Expr
	if !p.peekIs(TokOp, ")") {
		for {
			a, err := p.parseArgument()
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
	return args, nil
}

// parseArgument parses production [60]: an ExprSingle or the "?" placeholder.
//
// The placeholder is only a placeholder in an argument list; "?" elsewhere is
// an occurrence indicator, which is why this is a separate production rather
// than a case inside parseExprSingle.
func (p *Parser) parseArgument() (Expr, error) {
	if p.version.atLeast30() && p.cur().Kind == TokOp && p.cur().Val == "?" {
		// Only when it stands alone as the whole argument: "?" followed by
		// anything else is not a placeholder.
		if next := p.pos + 1; next < len(p.toks) && p.toks[next].Kind == TokOp &&
			(p.toks[next].Val == "," || p.toks[next].Val == ")") {
			p.pos++
			return &ArgumentPlaceholder{}, nil
		}
	}
	return p.parseExprSingle()
}

// tryParseAxisStep parses an axis step if one is present, reporting ok=false
// (without consuming tokens) when the current position starts a primary
// expression instead.
func (p *Parser) tryParseAxisStep() (*Step, bool, error) {
	start := p.pos
	step := &Step{Axis: AxisChild}
	explicitAxis := false

	switch {
	case p.peekIs(TokOp, "."):
		// A bare "." is the context *item*, not the step "self::node()".
		// The distinction matters because the context item may be an atomic
		// value — inside xsl:analyze-string or a for-expression over strings —
		// and a step would reject it as "not a node".
		//
		// Predicates do not change that. ".[...]" is a filter expression over
		// the context item, which parsePostfix builds once parsePrimary has
		// produced the ContextItem; treating it as a step here made "(1, 2) !
		// .[.]" fail as an axis step applied to an integer. Filtering an
		// atomic value is perfectly ordinary — that is what "(1, 2)[.]" is —
		// and the two spellings must agree.
		p.pos = start
		return nil, false, nil

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
			explicitAxis = true
			p.pos += 2
		} else if p.pos+1 < len(p.toks) && p.toks[p.pos+1].Kind == TokOp &&
			p.toks[p.pos+1].Val == "(" && !isKindTestName(p.cur().Val) {
			return nil, false, nil // function call: let parsePrimary handle it
		} else if p.version.atLeast31() && p.pos+1 < len(p.toks) &&
			p.toks[p.pos+1].Kind == TokOp && p.toks[p.pos+1].Val == "{" &&
			(p.cur().Val == "map" || p.cur().Val == "array") {
			// "map {" and "array {" are constructors rather than name tests
			// on elements so called. Only the brace makes them so — "map(1)"
			// is still an ordinary call, and an element named "map" is still
			// selectable as a step.
			return nil, false, nil
		} else if p.refVersion().atLeast30() && p.pos+1 < len(p.toks) &&
			p.toks[p.pos+1].Kind == TokOp && p.toks[p.pos+1].Val == "#" {
			// "name#3" is a named function reference, not a name test on an
			// element called "name". Deferred to parsePrimary for the same
			// reason a function call is: the name belongs to the construct
			// that follows it, not to a step.
			//
			// The reference floor rather than the parsed version, matching
			// the gate in parsePrimary: both have to agree, or the name is
			// eaten as a step here and the "#" is left over.
			//
			// A step is what a 2.0 module would otherwise get, so this also
			// settles which reading wins -- an element named "current-group"
			// followed by a stray "#" was never a valid expression.
			return nil, false, nil
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
	//
	// A *written* axis is left alone: "child::attribute(x)" is legal syntax
	// that selects nothing, and a match pattern spelling it out is testing
	// exactly that. Rewriting the axis there made such a pattern match every
	// attribute named x, so a stylesheet using it to say "this must not
	// match" got the opposite.
	if kt, isKind := test.(*KindTest); isKind && !kt.Any &&
		kt.Kind == xdm.KindAttribute && step.Axis == AxisChild && !explicitAxis {
		step.Axis = AxisAttribute
	}
	preds, err := p.parsePredicates()
	if err != nil {
		return nil, false, err
	}
	step.Predicates = preds
	step.Explicit = explicitAxis
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
		// namespace-node() takes no argument, and the two versions disagree
		// about what an argument means. In 3.0 the name is reserved by the
		// grammar, so "namespace-node(1)" cannot be a function call at all and
		// is simply malformed — XPST0003, which is what
		// function-call-reserved-function-names-037 accepts. In 2.0 there is
		// no namespace-node kind test to be malformed, so the same text reads
		// as a call to a function nobody declared, and -034 asserts XPST0017.
		if !p.peekIs(TokOp, ")") {
			if p.version.atLeast30() {
				return nil, p.errorf(
					"XPST0003: namespace-node() takes no arguments")
			}
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
			// declaration's substitution group. They are resolved now, while
			// the schema is still reachable through the resolver, because the
			// evaluator sees only the test.
			kt.SchemaDeclared = true
			if kt.Kind == xdm.KindElement {
				kt.SubstitutionGroup = schemaSubstitutionGroup(p.cur().Val, p.ns)
			}
			// The declaration's type, for the same reason: a node named E
			// but validated against a *local* declaration of E carries a
			// different annotation, and schema-element(E) must not take it.
			kt.DeclaredType, _ = schemaDeclarationType(
				p.cur().Val, p.ns, kt.Kind == xdm.KindAttribute)
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
			p.readTypeAnnotation(kt)
		} else if p.cur().Kind == TokWildcard {
			p.pos++ // element(*) is the same as element()
			// element(*, type) and attribute(*, type) are as legal as
			// the named forms; only the name may be a wildcard, not
			// the whole second argument, so the annotation is read
			// here too rather than only after a name.
			p.readTypeAnnotation(kt)
		}
	default:
		return nil, p.errorf("unknown kind test %q", name)
	}

	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	return kt, nil
}

// readTypeAnnotation consumes the ", type" of element(name, type) and its
// attribute() counterpart, recording it on the test.
//
// The annotation is a real constraint, not decoration: element(*, xs:NOTATION)
// asks whether the node was validated against that type, and answering "yes"
// for every node made a DTD-declared attribute claim a type it does not have.
// It is kept lexically because the comparison is against the node's own
// annotation, which is likewise a name rather than a resolved component.
func (p *Parser) readTypeAnnotation(kt *KindTest) {
	if _, ok := p.acceptOp(","); !ok {
		return
	}
	if p.cur().Kind == TokName {
		// Resolved to the annotation key here, while the static context that
		// binds the prefix is still reachable. Keeping it lexical meant the
		// comparison against the node's annotation had to fall back to local
		// parts, which conflated types that shared a local name across
		// namespaces.
		kt.TypeName = annotationKeyOf(p.cur().Val, p.ns)
		kt.TypeNameLexical = p.cur().Val
		p.pos++
	}
	if _, ok := p.acceptOp("?"); ok {
		kt.TypeNillable = true
	}
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
	// The 3.1 constructors are spelled with names that are also perfectly
	// ordinary function names, so they are recognised only when the token
	// after them is the brace that makes them constructors: "map" followed by
	// "{" is a map constructor, and "map(...)" is still a call.
	if p.version.atLeast31() && t.Kind == TokName {
		switch t.Val {
		case "map":
			if p.peekAheadIs(1, TokOp, "{") {
				return p.parseMapConstructor()
			}
		case "array":
			if p.peekAheadIs(1, TokOp, "{") {
				return p.parseArrayConstructor(true)
			}
		}
	}
	// The square-bracket array constructor cannot be confused with a
	// predicate: a predicate only ever follows a complete expression, and
	// parsePrimary is called where one is expected rather than continued.
	if p.version.atLeast31() && t.Kind == TokOp && t.Val == "[" {
		return p.parseArrayConstructor(false)
	}
	// A unary lookup applies to the context item: "?name" with nothing
	// before it, which is how a map is read inside a simple-map step.
	if p.version.atLeast31() && t.Kind == TokOp && t.Val == "?" {
		return p.parseLookup(nil)
	}
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
		// An inline function expression: "function" followed by "(". It is
		// checked before the function-call form, which would otherwise treat
		// "function(" as a call to a function named "function".
		if p.version.atLeast30() && t.Val == "function" &&
			p.pos+1 < len(p.toks) && p.toks[p.pos+1].Kind == TokOp &&
			p.toks[p.pos+1].Val == "(" {
			return p.parseInlineFunction()
		}
		// A named function reference: name followed by "#" and an integer.
		// Gated on the reference floor rather than on the parsed version,
		// because resolving a name follows the processor; see refversion.go.
		if p.refVersion().atLeast30() && p.pos+1 < len(p.toks) &&
			p.toks[p.pos+1].Kind == TokOp && p.toks[p.pos+1].Val == "#" {
			return p.parseNamedFunctionRef()
		}
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
	switch t.numType {
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

// parseNamedFunctionRef parses "fn:concat#3", production [63].
//
// The arity is a literal integer, not an expression: a function reference is
// resolved statically, and "concat#$n" is not in the grammar.
func (p *Parser) parseNamedFunctionRef() (Expr, error) {
	nameTok := p.cur()
	// The reserved names are reserved here too: "item#0" is a syntax error
	// rather than a reference to a function nobody declared.
	if p.reservedFor(nameTok.Val) {
		return nil, p.errorf("%q is a reserved name and cannot be referenced",
			nameTok.Val)
	}
	p.pos++
	if err := p.expectOp("#"); err != nil {
		return nil, err
	}
	arityTok := p.cur()
	if arityTok.Kind != TokNumber || arityTok.numType != numInteger {
		return nil, p.errorf("expected an integer arity after '#'")
	}
	// The arity is a bare integer literal with no bound in the grammar, so
	// nothing stops it being larger than any function could have — or than an
	// int can hold. fn-function-arity-017 writes fn:concat#2^128 and accepts
	// either the exact value or FOAR0002; parsing it through the token's
	// float64 quietly saturated to maxint and reported *that* as the arity,
	// which is neither. Re-reading the literal exactly is what catches it.
	arity64, err := strconv.ParseInt(arityTok.Val, 10, 64)
	if err != nil {
		return nil, p.errorf("FOAR0002: the arity %s is out of range", arityTok.Val)
	}
	arity := int(arity64)
	if arity < 0 || int64(arity) != arity64 {
		return nil, p.errorf("FOAR0002: the arity %s is out of range", arityTok.Val)
	}
	p.pos++
	name, err := p.resolveFunctionName(nameTok.Val)
	if err != nil {
		return nil, err
	}
	return &NamedFunctionRef{Name: name, Arity: arity}, nil
}

// parseInlineFunction parses "function($x as T, ...) as T { expr }",
// production [64].
func (p *Parser) parseInlineFunction() (Expr, error) {
	p.pos++ // "function"
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	var params []InlineParam
	if !p.peekIs(TokOp, ")") {
		for {
			if p.cur().Kind != TokVar {
				return nil, p.errorf("expected a parameter name")
			}
			name, err := p.resolveVarName(p.cur().Val)
			if err != nil {
				return nil, err
			}
			p.pos++
			var typ *SequenceType
			if p.peekKeyword("as") {
				p.pos++
				st, err := p.parseSequenceType()
				if err != nil {
					return nil, err
				}
				if err := checkNotListType(st, "a function signature"); err != nil {
					return nil, err
				}
				typ = &st
			}
			// Two parameters of the same name would make the second
			// unreferenceable, so the spec makes it a static error rather
			// than letting one silently shadow the other.
			for _, prior := range params {
				// Compared by namespace and local name, not by the whole
				// QName: two spellings of the same name are the same
				// parameter, and a struct comparison would take
				// $Q{u}foo and $p:foo bound to u for different ones.
				if prior.Name.URI == name.URI && prior.Name.Local == name.Local {
					return nil, xdm.Errorf("XQST0039",
						"the parameter $%s is declared twice", name.Lexical())
				}
			}
			params = append(params, InlineParam{Name: name, Type: typ})
			if _, ok := p.acceptOp(","); !ok {
				break
			}
		}
	}
	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	var result *SequenceType
	if p.peekKeyword("as") {
		p.pos++
		st, err := p.parseSequenceType()
		if err == nil {
			err = checkNotListType(st, "a function signature")
		}
		if err != nil {
			return nil, err
		}
		result = &st
	}
	// The body is an EnclosedExpr: braces, not parentheses.
	if err := p.expectOp("{"); err != nil {
		return nil, err
	}
	// 3.1 made the expression inside an EnclosedExpr optional, so
	// "function($x) {}" is legal and denotes the empty sequence. Parsing the
	// body unconditionally reported XPST0003 on the closing brace. A comment
	// between the braces is whitespace to the lexer, so "{ (: nothing :) }"
	// arrives here as an empty body too and needs no separate case.
	//
	// The empty body is still only a *body*: the declared result type is
	// checked against it as usual, which is why an empty body under
	// "as xs:integer" is XPTY0004 rather than an accepted empty sequence.
	var body Expr
	if p.version.atLeast31() && p.peekIs(TokOp, "}") {
		body = &SequenceExpr{}
	} else {
		var err error
		if body, err = p.parseExpr(); err != nil {
			return nil, err
		}
	}
	if err := p.expectOp("}"); err != nil {
		return nil, err
	}
	return &InlineFunctionExpr{Params: params, Result: result, Body: body}, nil
}

func (p *Parser) parseFunctionCall() (Expr, error) {
	return p.parseFunctionCallWith(nil)
}

// parseFunctionCallWith parses a function call, optionally with an argument
// supplied from outside the argument list.
//
// The arrow operator "$x => f(1)" is defined as the call "f($x, 1)", so it
// needs a call whose first argument comes from the left of the operator
// rather than from between the parentheses. Threading it here rather than
// rewriting the AST afterwards keeps arity right at the point where the
// function is resolved, which is what QName folding and schema-constructor
// folding both key on.
func (p *Parser) parseFunctionCallWith(first Expr) (Expr, error) {
	nameTok := p.cur()
	// A handful of names are reserved by the grammar: they introduce a kind
	// test or a type, so they cannot name a function however the expression
	// continues. Reaching here with one means the expression is malformed —
	// "item()" in a value position is a syntax error, not a call to an unknown
	// function — so it is XPST0003 rather than XPST0017.
	if p.reservedFor(nameTok.Val) {
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
	if first != nil {
		args = append(args, first)
	}
	if !p.peekIs(TokOp, ")") {
		for {
			a, err := p.parseArgument()
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
	// A partial application is a function item, not a call, so the
	// constructor folds below must not claim it.
	if hasPlaceholder(args) {
		return &FuncCall{Name: name, Args: args}, nil
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
	if prim == xdm.TypeQName {
		// A type derived from xs:NOTATION (or from xs:QName) has the QName
		// value space, so its constructor has to resolve the prefix here,
		// while the static context still exists — the same reason xs:QName()
		// is folded rather than called. A cast at run time would build a
		// QName with no namespace URI, which made two notation values with
		// different prefixes for the same namespace compare unequal and two
		// values with the same prefix for different namespaces compare equal.
		if q, ok, err := p.foldQNameLiteral(args[0]); err == nil && ok {
			// The fold resolves the prefix but says nothing about the facets
			// the schema author wrote, so z:notat('z:de') built a NOTATION
			// value for a notation the enumeration does not admit. The check
			// belongs here for the same reason the fold does: it needs the
			// expanded name, and the namespace context that produced it is
			// gone by evaluation time. The expanded spelling is what is
			// handed over, because comparing the raw lexical form matches
			// prefixes rather than namespaces — see ValidateExpandedQNameValue.
			//
			// The failure is deferred to evaluation rather than raised here:
			// FORG0001 is a dynamic error, and a constructor call in a branch
			// that is never taken must not stop the stylesheet compiling.
			expanded := q.Val.QName()
			clark := "{" + expanded.URI + "}" + expanded.Local
			if known, verr := schemaValueValid(lex, p.ns, clark); known && verr != nil {
				return &errorExpr{err: xdm.Errorf("FORG0001", "%v", verr)}, true
			}
			// The ANNOTATION KEY, not the lexical name: the derived name is
			// compared against annotation keys, and a lexical "one:not1-…"
			// matches none of them. Leaving it lexical made the constructor's
			// own result fail "instance of" against the very type it was
			// built for.
			q.Val = q.Val.WithDerived(annotationKeyOf(lex, p.ns))
			return q, true
		}
		// XPath 3.0 admits a computed string here for the same reason it does
		// for xs:QName(): the prefix resolves against the statically known
		// namespaces of the expression rather than of a literal. The facets
		// still have to be checked, but only once the value exists, so both
		// the resolver and the type's name are carried to run time.
		// notation-0001..0004 cast a computed string to a NOTATION subtype.
		if p.version.atLeast30() {
			return &dynamicQName{
				arg:     args[0],
				ns:      p.ns,
				lexType: lex,
				derived: annotationKeyOf(lex, p.ns),
			}, true
		}
		return nil, false
	}
	return &CastExpr{
		Operand: args[0],
		Type: SequenceType{
			AtomicType:    prim,
			HasAtomicType: true,
			SchemaType:    annotationKeyOf(lex, p.ns),
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
	// XPath 3.0 lifts the literal-only restriction, resolving the prefix
	// against the statically known namespaces of the expression instead. The
	// resolver is still in hand here, so a non-literal argument becomes a
	// node that carries it; see dynamicQName. A *literal* still folds, so a
	// malformed one is the static error it has always been.
	if p.version.atLeast30() {
		if lit, ok := args[0].(*Literal); !ok || lit.Val.Type != xdm.TypeString {
			return &dynamicQName{arg: args[0], ns: p.ns}, true, nil
		}
	}
	q, ok, err := p.foldQNameLiteral(args[0])
	if err != nil || !ok {
		return nil, false, err
	}
	return q, true, nil
}

// foldQNameLiteral turns a string-literal argument into a QName literal,
// resolving its prefix in the static context.
//
// It is shared by xs:QName() and by the constructor of a schema type whose
// value space is the QName one, because both have to bind the prefix while the
// namespace declarations of the expression are still reachable.
func (p *Parser) foldQNameLiteral(arg Expr) (*Literal, bool, error) {
	lit, ok := arg.(*Literal)
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

	// ParenthesizedItemType, production [96]: "( ItemType )". The parentheses
	// are there for readability where a function test's own "as" would
	// otherwise be ambiguous about where the signature ends — the one place it
	// really matters is a nested function type in parameter position, as in
	// for-each-011's "$ff as (function(item()) as item())". Without this the
	// parser reported XPST0003 at the opening paren.
	//
	// Only an ItemType may appear inside, so an occurrence indicator or
	// empty-sequence() there is a grammar error; the indicator belongs outside
	// the parentheses, and is picked up by finishOccurrence below.
	if t.Kind == TokOp && t.Val == "(" {
		p.pos++
		inner, err := p.parseSequenceType()
		if err != nil {
			return st, err
		}
		if inner.Empty || inner.Occurrence != "" {
			return st, p.errorf("XPST0003: parenthesized item type may not carry an occurrence indicator")
		}
		if err := p.expectOp(")"); err != nil {
			return st, err
		}
		return p.finishOccurrence(inner)
	}

	// A function test, productions [90]-[92]: "function(*)" matches any
	// function item, and "function(T, ...) as T" a function of that signature.
	// It is checked before the kind tests so that "function(" is never read as
	// a call to a function named "function".
	if p.version.atLeast30() && t.Kind == TokName && t.Val == "function" &&
		p.pos+1 < len(p.toks) && p.toks[p.pos+1].Val == "(" {
		if err := p.parseFunctionTest(&st); err != nil {
			return st, err
		}
		return p.finishOccurrence(st)
	}

	// An array test, production [96] of XPath 3.1. Like the function test it
	// is recognised before the kind tests, and only in sequence-type position:
	// "array(" elsewhere is the array: function library or a name, and the two
	// never meet because a type is only ever parsed where a type is expected.
	if p.version.atLeast31() && t.Kind == TokName && t.Val == "array" &&
		p.pos+1 < len(p.toks) && p.toks[p.pos+1].Val == "(" {
		if err := p.parseArrayTest(&st); err != nil {
			return st, err
		}
		return p.finishOccurrence(st)
	}

	// A map test, production [93] of XPath 3.1, recognised on the same terms
	// as the array test above. "map(" in an expression is the map constructor
	// or the map: function library; the two never collide because a type is
	// only parsed where a type is expected.
	if p.version.atLeast31() && t.Kind == TokName && t.Val == "map" &&
		p.pos+1 < len(p.toks) && p.toks[p.pos+1].Val == "(" {
		if err := p.parseMapTest(&st); err != nil {
			return st, err
		}
		return p.finishOccurrence(st)
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
		// xs:error is the empty type: it has no instances, so nothing is an
		// instance of it and every cast to it fails. It is not an atomic type
		// code because there is no value it could ever hold.
		if isErrorTypeName(t.Val, p.ns) {
			st.IsErrorType = true
			return p.finishOccurrence(st)
		}
		// xs:numeric is a union of the three numeric primitives. Like
		// xs:error it is not in the atomic-type table, because no single type
		// code stands for it.
		if p.version.atLeast31() && isNumericTypeName(t.Val, p.ns) {
			st.IsNumericType = true
			return p.finishOccurrence(st)
		}
		// The built-in list types are not in the atomic-type table either,
		// for the same reason: their value is a sequence of tokens, not one
		// item. castable-007..009 write them in type position.
		if facet, ok := listTypeName(t.Val, p.ns); ok {
			st.ListItemFacet = facet
			return p.finishOccurrence(st)
		}
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
				st.SchemaType = annotationKeyOf(t.Val, p.ns)
				// The facets of an imported simple type are only in the
				// schema, so the check is captured here rather than being
				// reconstructed from the type code at cast time.
				if lex, ns := t.Val, p.ns; true {
					st.SchemaValueValid = func(value string) error {
						known, err := schemaValueValid(lex, ns, value)
						if !known {
							return nil
						}
						return err
					}
				}
				if isAtomic {
					st.AtomicType, st.HasAtomicType = prim, true
				} else if members, pure := schemaUnionMembersOf(t.Val, p.ns); pure {
					// A pure union has no single primitive to erase to, so it
					// arrives here as "known but not atomic". Its members are
					// what makes it matchable at all.
					st.SchemaUnionMembers = members
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

// parseFunctionTest parses productions [90]-[92].
//
// "function(*)" is AnyFunctionTest and matches any function item.
// "function(T, ...) as T" is TypedFunctionTest. Its parameter and return types
// are recorded, and matched against a function item that carries a signature
// of its own; an item without one is judged on arity alone, which is what
// every function item did before signatures existed.
func (p *Parser) parseFunctionTest(st *SequenceType) error {
	p.pos++ // "function"
	if err := p.expectOp("("); err != nil {
		return err
	}
	st.IsFunctionTest = true

	// AnyFunctionTest: the "*" arrives as a wildcard token, since no operand
	// precedes it.
	if p.cur().Kind == TokWildcard && p.cur().Val == "*" {
		p.pos++
		if err := p.expectOp(")"); err != nil {
			return err
		}
		return nil
	}
	if p.cur().Kind == TokOp && p.cur().Val == "*" {
		p.pos++
		if err := p.expectOp(")"); err != nil {
			return err
		}
		return nil
	}

	// TypedFunctionTest: zero or more parameter types, then a required "as".
	arity := 0
	if !p.peekIs(TokOp, ")") {
		for {
			pt, err := p.parseSequenceType()
			if err != nil {
				return err
			}
			st.FunctionParams = append(st.FunctionParams, pt)
			arity++
			if _, ok := p.acceptOp(","); !ok {
				break
			}
		}
	}
	if err := p.expectOp(")"); err != nil {
		return err
	}
	if !p.peekKeyword("as") {
		return p.errorf("expected 'as' after a typed function test")
	}
	p.pos++
	ret, err := p.parseSequenceType()
	if err != nil {
		return err
	}
	st.FunctionReturn = &ret
	st.FunctionArity, st.HasFunctionArity = arity, true
	return nil
}

// parseArrayTest parses productions [95]-[97] of XPath 3.1.
//
// "array(*)" is AnyArrayTest and matches any array. "array(T)" is
// TypedArrayTest, whose T constrains every member sequence.
func (p *Parser) parseArrayTest(st *SequenceType) error {
	p.pos++ // "array"
	if err := p.expectOp("("); err != nil {
		return err
	}
	st.IsArrayTest = true

	// The "*" of AnyArrayTest arrives as a wildcard token rather than an
	// operator, since no operand precedes it — the same disambiguation
	// function(*) needs.
	if (p.cur().Kind == TokWildcard || p.cur().Kind == TokOp) && p.cur().Val == "*" {
		p.pos++
		return p.expectOp(")")
	}
	mt, err := p.parseSequenceType()
	if err != nil {
		return err
	}
	st.ArrayMember = &mt
	return p.expectOp(")")
}

// parseMapTest parses productions [93]-[94] of XPath 3.1.
//
// "map(*)" is AnyMapTest and matches any map. "map(K, V)" is TypedMapTest: K
// is an atomic type, V a sequence type, and both parts are required —
// "map(xs:integer)" is XPST0003 rather than a key type with the value left
// open (MapTest-007).
func (p *Parser) parseMapTest(st *SequenceType) error {
	p.pos++ // "map"
	if err := p.expectOp("("); err != nil {
		return err
	}
	st.IsMapTest = true

	if (p.cur().Kind == TokWildcard || p.cur().Kind == TokOp) && p.cur().Val == "*" {
		p.pos++
		return p.expectOp(")")
	}
	kt, err := p.parseSequenceType()
	if err != nil {
		return err
	}
	// The key type is an AtomicOrUnionType, so it carries no occurrence
	// indicator: "map(xs:string+, ...)" is a grammar error (MapTest-016), not
	// a key that may repeat.
	if kt.Occurrence != "" || !kt.HasAtomicType {
		return p.errorf("XPST0003: a map test's key type must be an atomic type")
	}
	if _, ok := p.acceptOp(","); !ok {
		return p.errorf("XPST0003: a map test names both a key type and a value type")
	}
	vt, err := p.parseSequenceType()
	if err != nil {
		return err
	}
	st.MapKey, st.MapValue = &kt, &vt
	return p.expectOp(")")
}

// finishOccurrence applies a trailing occurrence indicator to a type that was
// parsed by a path returning early, rather than falling through to the shared
// label.
func (p *Parser) finishOccurrence(st SequenceType) (SequenceType, error) {
	if occ, ok := p.acceptOp("?", "*", "+"); ok {
		st.Occurrence = occ
		return st, nil
	}
	if p.cur().Kind == TokWildcard && p.cur().Val == "*" {
		p.pos++
		st.Occurrence = "*"
	}
	return st, nil
}

// atomicTypeByName maps a lexical type name to a TypeCode. Only the xs:
// namespace is recognised; a prefix bound elsewhere is not a built-in type.
// isErrorTypeName reports whether a lexical name is xs:error.
func isErrorTypeName(lex string, ns NamespaceResolver) bool {
	prefix, local := xdm.SplitQName(lex)
	if local != "error" {
		return false
	}
	if prefix == "" {
		return ns != nil && ns.DefaultElementNamespace() == xdm.NSXS
	}
	uri, ok := ns.ResolvePrefix(prefix)
	return ok && uri == xdm.NSXS
}

// isNumericTypeName reports whether lex names xs:numeric, on the same
// prefix-resolution rules as isErrorTypeName.
func isNumericTypeName(lex string, ns NamespaceResolver) bool {
	prefix, local := xdm.SplitQName(lex)
	if local != "numeric" {
		return false
	}
	if prefix == "" {
		return ns != nil && ns.DefaultElementNamespace() == xdm.NSXS
	}
	uri, ok := ns.ResolvePrefix(prefix)
	return ok && uri == xdm.NSXS
}

func atomicTypeByName(lex string, ns NamespaceResolver) (xdm.TypeCode, bool) {
	prefix, local := xdm.SplitQName(lex)
	if prefix != "" {
		uri, ok := ns.ResolvePrefix(prefix)
		if !ok || uri != xdm.NSXS {
			return 0, false
		}
	} else if ns != nil && ns.DefaultElementNamespace() != xdm.NSXS {
		// An unprefixed AtomicType is not in no namespace: it takes the
		// default element/type namespace, so "string" names xs:string only
		// where that namespace is the XSD one. Accepting it unconditionally
		// let "'abc' instance of string" compile in a context binding no such
		// name, where XPST0051 is required.
		//
		// Returning false does not by itself reject the name. The caller
		// falls through to schemaTypeOf, which looks the same name up in
		// whatever namespace *is* in force -- so an imported schema type
		// written unprefixed still resolves, and only a name that denotes
		// nothing anywhere reaches the XPST0051 at the end.
		return 0, false
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
	case "numeric":
		// xs:numeric is not a type in the schema at all: XPath 3.1 defines it
		// as a union of xs:double, xs:float and xs:decimal, used in the
		// signatures of fn:abs, fn:floor and the rest. It resolves to
		// xs:double here only so that the name parses; membership is decided
		// by FacetName in atomicTypeMatchesFacet, which is what keeps
		// "1 instance of xs:numeric" true for an integer rather than only for
		// a double.
		return xdm.TypeDouble, true
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
	// xs:numeric is here for the same reason: its code is xs:double, so the
	// written name is the only thing that still says "any of the numeric
	// types" rather than "a double".
	if hasRangeFacet(name) || hasStringFacet(name) || isAbstractType(name) ||
		name == "dateTimeStamp" || name == "numeric" {
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
// isReservedFunctionName reports whether a name is reserved by the grammar at
// every version.
func isReservedFunctionName(name string) bool {
	switch name {
	case "attribute", "comment", "document-node", "element", "empty-sequence",
		"if", "item", "node", "processing-instruction", "schema-attribute",
		"schema-element", "text", "typeswitch", "namespace-node":
		return true
	}
	return false
}

// reservedFor reports whether a name is reserved at the parser's version.
//
// "function" and "switch" join the list in 3.0, where they introduce an inline
// function expression and a switch expression. Under 2.0 they are ordinary
// names: "function()" there is a call to a function nobody declared, which is
// XPST0017 rather than the XPST0003 a reserved name would give — and the suite
// asserts both readings.
func (p *Parser) reservedFor(name string) bool {
	if isReservedFunctionName(name) {
		return true
	}
	if p.version.atLeast30() {
		switch name {
		case "function", "switch":
			return true
		}
	}
	// "map" and "array" become item-type keywords in 3.1, which reserves them
	// as function names the same way "item" has always been reserved: they
	// introduce map(K,V) and array(T) in type position, so "map()" is a
	// malformed type test rather than a call. Before this they fell through to
	// function lookup and reported XPST0017, "unknown function", where the
	// suite wants the grammar error XPST0003.
	//
	// Only at 3.1: under 2.0 and 3.0 neither name means anything to the
	// grammar, so "map()" there is an ordinary call to an undeclared function
	// and must keep giving XPST0017.
	if p.version.atLeast31() {
		switch name {
		case "map", "array":
			return true
		}
	}
	return false
}

// errorExpr is an expression that always raises one dynamic error.
//
// It exists so that a fold performed at parse time can report a *dynamic*
// failure. The constructor of a schema type is resolved during parsing,
// because it needs the static namespace context, but a value outside the
// type's value space is FORG0001 — a dynamic error, which must not be raised
// until the expression is actually evaluated.
type errorExpr struct{ err error }

func (e *errorExpr) Eval(c *Context) (xdm.Sequence, error) { return nil, e.err }
func (e *errorExpr) String() string                        { return "error()" }

// lookupKeywordName reports whether the current token is a name that the lexer
// classified as an infix operator.
//
// The lexer decides between the two by whether an operand precedes, and a "?"
// leaves that state alone so that a sequence-type occurrence indicator still
// ends its type. The consequence is that "$m?div" lexes "div" as the division
// operator, and Lookup-165 requires it to be the key "div". Reclassifying here
// rather than in the lexer keeps the occurrence-indicator reading intact,
// which is what changing the lexer's state would have broken.
func (p *Parser) lookupKeywordName() bool {
	t := p.cur()
	return t.Kind == TokOp && operatorKeywords[t.Val]
}

// joinLookupName reassembles a hyphenated NCName the lexer split apart.
//
// "$m?div-2" lexes as the operator "div", the operator "-" and the number 2,
// because "div" was taken for an operator and a hyphen after one starts a new
// token. The pieces are rejoined only when they are physically adjacent in the
// source, so "$m?a - 1" is still a subtraction.
func (p *Parser) joinLookupName(name string) string {
	for p.peekIs(TokOp, "-") {
		hyphen := p.cur()
		if hyphen.Pos != p.toks[p.pos-1].Pos+len(p.toks[p.pos-1].Val) {
			return name
		}
		next := p.toks[p.pos+1]
		if next.Pos != hyphen.Pos+1 {
			return name
		}
		switch next.Kind {
		case TokName, TokNumber:
		case TokOp:
			if !operatorKeywords[next.Val] {
				return name
			}
		default:
			return name
		}
		name += "-" + next.Val
		p.pos += 2
	}
	return name
}

// parseLookup parses the postfix lookup operator, production [54]:
// Lookup ::= "?" KeySpecifier, where the specifier is an NCName, an integer,
// "*", or a parenthesised expression.
//
// The base is nil for the unary form, "?name" with nothing before it, which
// applies to the context item.
func (p *Parser) parseLookup(base Expr) (Expr, error) {
	if _, ok := p.acceptOp("?"); !ok {
		return nil, p.errorf("expected '?' to start a lookup")
	}
	e := &LookupExpr{Base: base}
	switch {
	case p.cur().Kind == TokNumber:
		// An integer key selects an array member by position. A
		// non-integer is not a key specifier at all, so it is a syntax
		// error rather than a lookup that fails at evaluation.
		lit := numericLiteral(p.cur())
		if lit.Type != xdm.TypeInteger {
			return nil, p.errorf("a lookup by position needs an integer, got %s", p.cur().Val)
		}
		e.Index, e.HasIndex = int(lit.Float64()), true
		p.pos++
	case p.cur().Kind == TokWildcard && p.cur().Val == "*",
		p.cur().Kind == TokOp && p.cur().Val == "*":
		e.Wildcard = true
		p.pos++
	case p.peekIs(TokOp, "("):
		p.pos++
		// "?()" names no key at all, which is the empty sequence rather than
		// a syntax error: Lookup-146 asserts that "$maps?()" is empty. The
		// parenthesised form is an Expr, and an Expr may be empty here only
		// because the grammar writes the parentheses around it.
		if p.peekIs(TokOp, ")") {
			p.pos++
			e.Expr = &SequenceExpr{}
			break
		}
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(")"); err != nil {
			return nil, err
		}
		e.Expr = inner
	case p.cur().Kind == TokName || p.lookupKeywordName():
		name := p.cur().Val
		// A key specifier is an NCName, so a colon in it is not a prefixed
		// name that happens to be usable — it is a grammar error. Lookup-156
		// writes "?xs:integer" over a map whose keys really are spelled that
		// way and requires XPST0003 rather than the entry.
		if strings.ContainsAny(name, ":{") {
			return nil, p.errorf("XPST0003: a lookup key specifier is an NCName, not %q", name)
		}
		p.pos++
		e.Name, e.HasName = p.joinLookupName(name), true
	default:
		return nil, p.errorf("expected a name, an integer, '*' or '(' after '?'")
	}
	return e, nil
}

// peekAheadIs reports whether the token n places ahead has the given kind and
// value, without consuming anything.
func (p *Parser) peekAheadIs(n int, kind TokenKind, val string) bool {
	if p.pos+n >= len(p.toks) {
		return false
	}
	t := p.toks[p.pos+n]
	return t.Kind == kind && t.Val == val
}

// parseMapConstructor parses "map { k : v, ... }", production [69].
func (p *Parser) parseMapConstructor() (Expr, error) {
	p.pos++ // "map"
	if err := p.expectOp("{"); err != nil {
		return nil, err
	}
	e := &MapConstructor{}
	if p.peekIs(TokOp, "}") {
		p.pos++
		return e, nil
	}
	for {
		k, err := p.parseExprSingle()
		if err != nil {
			return nil, err
		}
		if err := p.expectOp(":"); err != nil {
			return nil, err
		}
		v, err := p.parseExprSingle()
		if err != nil {
			return nil, err
		}
		e.Keys = append(e.Keys, k)
		e.Values = append(e.Values, v)
		if _, ok := p.acceptOp(","); !ok {
			break
		}
	}
	if err := p.expectOp("}"); err != nil {
		return nil, err
	}
	return e, nil
}

// parseArrayConstructor parses both array spellings, productions [70]-[72].
//
// The curly form takes one expression and makes a member of each item it
// yields; the square form takes a comma-separated list and makes a member of
// each expression, so "[(1,2), 3]" has two members where "array { (1,2), 3 }"
// has three.
func (p *Parser) parseArrayConstructor(curly bool) (Expr, error) {
	e := &ArrayConstructor{Curly: curly}
	if curly {
		p.pos++ // "array"
		if err := p.expectOp("{"); err != nil {
			return nil, err
		}
		if p.peekIs(TokOp, "}") {
			p.pos++
			return e, nil
		}
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		e.Members = append(e.Members, inner)
		if err := p.expectOp("}"); err != nil {
			return nil, err
		}
		return e, nil
	}
	if err := p.expectOp("["); err != nil {
		return nil, err
	}
	if p.peekIs(TokOp, "]") {
		p.pos++
		return e, nil
	}
	for {
		m, err := p.parseExprSingle()
		if err != nil {
			return nil, err
		}
		e.Members = append(e.Members, m)
		if _, ok := p.acceptOp(","); !ok {
			break
		}
	}
	if err := p.expectOp("]"); err != nil {
		return nil, err
	}
	return e, nil
}
