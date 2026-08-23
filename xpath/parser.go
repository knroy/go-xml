package xpath

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// NamespaceResolver resolves a namespace prefix to a URI at parse time.
//
// Prefixes must be resolved when the expression is compiled, not when it runs:
// an XPath expression in a stylesheet is bound to the namespace declarations
// in scope at the point it appears, and by evaluation time the relevant
// element is long out of view.
type NamespaceResolver interface {
	// ResolvePrefix returns the URI bound to prefix, or false if unbound.
	ResolvePrefix(prefix string) (string, bool)
	// DefaultElementNamespace returns the namespace applied to unprefixed
	// element name tests. XSLT sets this from xpath-default-namespace; it is
	// empty by default, and it never applies to attribute names or function
	// names.
	DefaultElementNamespace() string
	// DefaultFunctionNamespace returns the namespace for unprefixed function
	// names, which is the fn: namespace in XPath and XSLT.
	DefaultFunctionNamespace() string
}

// Parser builds an AST from tokens.
type Parser struct {
	toks []Token
	pos  int
	src  string
	ns   NamespaceResolver
}

// Parse compiles an XPath 2.0 expression.
func Parse(src string, ns NamespaceResolver) (Expr, error) {
	if ns == nil {
		ns = defaultResolver{}
	}
	toks, err := NewLexer(src).Tokens()
	if err != nil {
		return nil, fmt.Errorf("%w in %q", err, src)
	}
	p := &Parser{toks: toks, src: src, ns: ns}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if !p.atEOF() {
		return nil, p.errorf("unexpected %q after expression", p.cur().Val)
	}
	return e, nil
}

// defaultResolver binds no prefixes and uses the standard function namespace.
type defaultResolver struct{}

func (defaultResolver) ResolvePrefix(p string) (string, bool) {
	switch p {
	case "xml":
		return xdm.NSXML, true
	case "xs":
		return xdm.NSXS, true
	case "fn":
		return xdm.NSFN, true
	}
	return "", false
}
func (defaultResolver) DefaultElementNamespace() string  { return "" }
func (defaultResolver) DefaultFunctionNamespace() string { return xdm.NSFN }

// --- Token helpers ----------------------------------------------------------

// cur returns the current token, or the EOF sentinel once the input is
// exhausted.
//
// The token slice always ends in a TokEOF, and a correct parse stops there.
// But a malformed expression can advance past it — "3 treat as item(" runs the
// item() branch off the end looking for its closing paren — and an unguarded
// index turns that into a panic rather than a syntax error. Since the parser
// is reachable from any stylesheet or query, that is a denial of service for
// an embedding server, so the bound is enforced here once rather than at each
// of the many call sites.
func (p *Parser) cur() Token {
	if p.pos >= len(p.toks) {
		return p.toks[len(p.toks)-1] // the EOF sentinel
	}
	return p.toks[p.pos]
}
func (p *Parser) atEOF() bool { return p.cur().Kind == TokEOF }

func (p *Parser) peekIs(kind TokenKind, val string) bool {
	t := p.cur()
	return t.Kind == kind && t.Val == val
}

// peekKeyword reports whether the current token is the given keyword.
//
// It accepts both TokOp and TokName because the lexer's operator/name
// disambiguation is driven by whether an operand preceded the token, and the
// second word of a two-word operator ("cast as", "instance of", "treat as")
// follows an operator rather than an operand. The keyword is unambiguous at
// these parse positions, so the token kind carries no extra information.
func (p *Parser) peekKeyword(kw string) bool {
	t := p.cur()
	return (t.Kind == TokOp || t.Kind == TokName) && t.Val == kw
}

// expectKeyword consumes the given keyword or reports an error.
func (p *Parser) expectKeyword(kw string) error {
	if !p.peekKeyword(kw) {
		return p.errorf("expected %q, got %q", kw, p.cur().Val)
	}
	p.pos++
	return nil
}

// acceptOp consumes an operator token if it matches any of vals.
func (p *Parser) acceptOp(vals ...string) (string, bool) {
	t := p.cur()
	if t.Kind != TokOp {
		return "", false
	}
	for _, v := range vals {
		if t.Val == v {
			p.pos++
			return v, true
		}
	}
	return "", false
}

func (p *Parser) expectOp(val string) error {
	if _, ok := p.acceptOp(val); !ok {
		return p.errorf("expected %q, got %q", val, p.cur().Val)
	}
	return nil
}

func (p *Parser) errorf(format string, args ...any) error {
	// XPST0003 is the generic "this is not a valid expression" code, and it is
	// the right answer for most parse failures. But the spec defines narrower
	// codes for particular static errors — XPST0081 for an unbound prefix,
	// XPST0051 for an unknown type, XPST0017 for an unresolvable function —
	// and callers pass those in the message. Prefixing XPST0003 in front of
	// them buried the specific code where nothing could read it.
	msg := fmt.Sprintf(format, args...)
	code := "XPST0003"
	if c, rest, ok := splitLeadingCode(msg); ok {
		code, msg = c, rest
	}
	return xdm.Errorf(code, "%s (at offset %d in %q)", msg, p.cur().Pos, p.src)
}

// splitLeadingCode separates a "CODE: message" prefix, if the message has one.
func splitLeadingCode(msg string) (code, rest string, ok bool) {
	i := strings.Index(msg, ": ")
	if i <= 0 {
		return "", msg, false
	}
	if !looksLikeStaticCode(msg[:i]) {
		return "", msg, false
	}
	return msg[:i], msg[i+2:], true
}

// looksLikeStaticCode reports whether s has the shape of a spec error code:
// four letters then four digits.
func looksLikeStaticCode(s string) bool {
	if len(s) != 8 {
		return false
	}
	for i := 0; i < 4; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	for i := 4; i < 8; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// --- Precedence ladder ------------------------------------------------------
//
// Lowest to highest: ",", "or", "and", comparison, "to", "+"/"-", "*"/"div"/
// "idiv"/"mod", union, intersect/except, instance of, treat as, castable as,
// cast as, unary, path. Each level is one function that parses the next level
// up and then loops on its own operators, which is the standard shape and
// makes the precedence table directly readable from the call chain.

// parseExpr handles the comma operator, which builds a sequence.
func (p *Parser) parseExpr() (Expr, error) {
	first, err := p.parseExprSingle()
	if err != nil {
		return nil, err
	}
	if !p.peekIs(TokOp, ",") {
		return first, nil
	}
	items := []Expr{first}
	for {
		if _, ok := p.acceptOp(","); !ok {
			break
		}
		e, err := p.parseExprSingle()
		if err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return &SequenceExpr{Items: items}, nil
}

// parseExprSingle is an expression without a top-level comma: the operand of
// a function argument, a predicate, or a sequence member.
func (p *Parser) parseExprSingle() (Expr, error) {
	t := p.cur()
	if t.Kind == TokName {
		switch t.Val {
		case "for":
			return p.parseFor()
		case "some", "every":
			return p.parseQuantified()
		case "if":
			// "if" is only the conditional when followed by "("; otherwise it
			// is an element name, as in the path "if/then".
			if p.pos+1 < len(p.toks) && p.toks[p.pos+1].Kind == TokOp &&
				p.toks[p.pos+1].Val == "(" {
				return p.parseIf()
			}
		}
	}
	return p.parseOr()
}

func (p *Parser) parseFor() (Expr, error) {
	p.pos++ // "for"
	bindings, err := p.parseBindings()
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("return"); err != nil {
		return nil, err
	}
	body, err := p.parseExprSingle()
	if err != nil {
		return nil, err
	}
	return &ForExpr{Bindings: bindings, Return: body}, nil
}

func (p *Parser) parseQuantified() (Expr, error) {
	every := p.cur().Val == "every"
	p.pos++
	bindings, err := p.parseBindings()
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("satisfies"); err != nil {
		return nil, err
	}
	test, err := p.parseExprSingle()
	if err != nil {
		return nil, err
	}
	return &QuantifiedExpr{Every: every, Bindings: bindings, Test: test}, nil
}

// parseBindings parses "$v in expr" clauses separated by commas.
func (p *Parser) parseBindings() ([]Binding, error) {
	var out []Binding
	for {
		if p.cur().Kind != TokVar {
			return nil, p.errorf("expected a variable")
		}
		name, err := p.resolveVarName(p.cur().Val)
		if err != nil {
			return nil, err
		}
		p.pos++
		if !p.peekKeyword("in") {
			return nil, p.errorf("expected 'in' after $%s", name.Local)
		}
		p.pos++
		seq, err := p.parseExprSingle()
		if err != nil {
			return nil, err
		}
		out = append(out, Binding{Var: name, Seq: seq})
		if _, ok := p.acceptOp(","); !ok {
			return out, nil
		}
	}
}

func (p *Parser) parseIf() (Expr, error) {
	p.pos++ // "if"
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	if err := p.expectKeyword("then"); err != nil {
		return nil, err
	}
	thenE, err := p.parseExprSingle()
	if err != nil {
		return nil, err
	}
	// The else branch is mandatory: without it, the type of the expression
	// would depend on the condition at runtime.
	if !p.peekKeyword("else") {
		return nil, p.errorf("expected 'else' (it is not optional in XPath)")
	}
	p.pos++
	elseE, err := p.parseExprSingle()
	if err != nil {
		return nil, err
	}
	return &IfExpr{Cond: cond, Then: thenE, Else: elseE}, nil
}

func (p *Parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peekIs(TokOp, "or") {
		p.pos++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: "or", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseAnd() (Expr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.peekIs(TokOp, "and") {
		p.pos++
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: "and", Left: left, Right: right}
	}
	return left, nil
}

// comparisonOps covers all three families: general (=, !=, <, ...), value
// (eq, ne, lt, ...) and node (is, <<, >>). They share a precedence level and
// are non-associative, so this parses at most one.
var comparisonOps = []string{
	"=", "!=", "<", "<=", ">", ">=",
	"eq", "ne", "lt", "le", "gt", "ge",
	"is", "<<", ">>",
}

func (p *Parser) parseComparison() (Expr, error) {
	left, err := p.parseRange()
	if err != nil {
		return nil, err
	}
	if op, ok := p.acceptOp(comparisonOps...); ok {
		right, err := p.parseRange()
		if err != nil {
			return nil, err
		}
		return &BinaryOp{Op: op, Left: left, Right: right}, nil
	}
	return left, nil
}

func (p *Parser) parseRange() (Expr, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	if p.peekIs(TokOp, "to") {
		p.pos++
		right, err := p.parseAdditive()
		if err != nil {
			return nil, err
		}
		return &BinaryOp{Op: "to", Left: left, Right: right}, nil
	}
	return left, nil
}

func (p *Parser) parseAdditive() (Expr, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for {
		op, ok := p.acceptOp("+", "-")
		if !ok {
			return left, nil
		}
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: op, Left: left, Right: right}
	}
}

func (p *Parser) parseMultiplicative() (Expr, error) {
	left, err := p.parseUnion()
	if err != nil {
		return nil, err
	}
	for {
		op, ok := p.acceptOp("*", "div", "idiv", "mod")
		if !ok {
			// A "*" reaching operator position as a wildcard token is
			// multiplication. The lexer clears its operand flag on a "*" it
			// has already classified as an operator — it must, or "*******"
			// would lex as one wildcard followed by six operators instead of
			// four wildcards separated by three multiplications — and a
			// sequence-type occurrence indicator is a "*" the parser
			// consumed as an operator, so the star after it arrives spelled
			// as a wildcard. "3 treat as xs:integer * * 3" is that case: the
			// first star is the indicator, the second multiplies.
			//
			// This is the exact mirror of parseSequenceType accepting a
			// wildcard-spelled "*" as the occurrence indicator. Neither
			// spelling is ambiguous in its own position; only the lexer,
			// which sees no positions, cannot tell them apart.
			if p.cur().Kind != TokWildcard || p.cur().Val != "*" {
				return left, nil
			}
			p.pos++
			op = "*"
		}
		right, err := p.parseUnion()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: op, Left: left, Right: right}
	}
}

func (p *Parser) parseUnion() (Expr, error) {
	left, err := p.parseIntersectExcept()
	if err != nil {
		return nil, err
	}
	for {
		// "union" and "|" are synonyms, so both normalise to one Op.
		if _, ok := p.acceptOp("union", "|"); !ok {
			return left, nil
		}
		right, err := p.parseIntersectExcept()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: "union", Left: left, Right: right}
	}
}

func (p *Parser) parseIntersectExcept() (Expr, error) {
	left, err := p.parseInstanceOf()
	if err != nil {
		return nil, err
	}
	for {
		op, ok := p.acceptOp("intersect", "except")
		if !ok {
			return left, nil
		}
		right, err := p.parseInstanceOf()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: op, Left: left, Right: right}
	}
}

func (p *Parser) parseInstanceOf() (Expr, error) {
	left, err := p.parseTreat()
	if err != nil {
		return nil, err
	}
	if p.peekKeyword("instance") {
		p.pos++
		if err := p.expectKeyword("of"); err != nil {
			return nil, err
		}
		st, err := p.parseSequenceType()
		if err != nil {
			return nil, err
		}
		return &InstanceOfExpr{Operand: left, Type: st}, nil
	}
	return left, nil
}

func (p *Parser) parseTreat() (Expr, error) {
	left, err := p.parseCastable()
	if err != nil {
		return nil, err
	}
	if p.peekKeyword("treat") {
		p.pos++
		if err := p.expectKeyword("as"); err != nil {
			return nil, err
		}
		st, err := p.parseSequenceType()
		if err != nil {
			return nil, err
		}
		return &TreatExpr{Operand: left, Type: st}, nil
	}
	return left, nil
}

func (p *Parser) parseCastable() (Expr, error) {
	left, err := p.parseCast()
	if err != nil {
		return nil, err
	}
	if p.peekKeyword("castable") {
		p.pos++
		if err := p.expectKeyword("as"); err != nil {
			return nil, err
		}
		st, err := p.parseSequenceType()
		if err != nil {
			return nil, err
		}
		if err := checkCastTarget(st); err != nil {
			return nil, err
		}
		return &CastExpr{Operand: left, Type: st, Castable: true}, nil
	}
	return left, nil
}

func (p *Parser) parseCast() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	if p.peekKeyword("cast") {
		p.pos++
		if err := p.expectKeyword("as"); err != nil {
			return nil, err
		}
		st, err := p.parseSequenceType()
		if err != nil {
			return nil, err
		}
		if err := checkCastTarget(st); err != nil {
			return nil, err
		}
		// A cast to xs:QName has to resolve its prefix, and only the parser
		// has the namespace bindings to do it — CastAtomic sees a string and
		// a target type and nothing else, so a prefixed name reaching it
		// became a QName with no URI. xs:QName() is folded here for the same
		// reason; "cast as" was left out and quietly produced the wrong
		// namespace.
		if q, ok, err := p.foldQNameCast(left, st); err != nil {
			return nil, err
		} else if ok {
			return q, nil
		}
		return &CastExpr{Operand: left, Type: st}, nil
	}
	return left, nil
}

func (p *Parser) parseUnary() (Expr, error) {
	if op, ok := p.acceptOp("-", "+"); ok {
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryOp{Op: op, Operand: operand}, nil
	}
	return p.parsePath()
}

// checkCastTarget rejects a cast whose target cannot be a target.
//
// An abstract type names a position in the hierarchy rather than a type a
// value can have, so "cast as xs:NOTATION" has no meaning — the spec gives it
// its own static error rather than letting it fail at run time.
func checkCastTarget(st SequenceType) error {
	// The grammar for a cast target is SingleType: an atomic type with an
	// optional "?". "*" and "+" are not part of it, so they are a syntax
	// error rather than a cast that happens to fail.
	if st.Occurrence == "*" || st.Occurrence == "+" {
		return xdm.Errorf("XPST0003",
			"a cast target may not have an occurrence indicator %q", st.Occurrence)
	}
	// A node kind is not an atomic type, so it cannot be a cast target
	// either — "castable as node()" is asking whether a value can be
	// converted into a node, which no cast does.
	//
	// This is XPST0003 rather than XPST0080: the grammar's cast production
	// admits only an atomic type name, so a kind test there does not parse at
	// all. XPST0080 is for a name that *is* a type but is not allowed as a
	// target, which is the abstract-type case below.
	if !st.HasAtomicType {
		return xdm.Errorf("XPST0003",
			"a cast target must be an atomic type, got %s", st)
	}
	if isAbstractType(st.FacetName) {
		return xdm.Errorf("XPST0080",
			"xs:%s cannot be a cast target: it is an abstract type", st.FacetName)
	}
	return nil
}

// foldQNameCast folds "literal cast as xs:QName" to the QName it denotes.
//
// It applies only to a string literal. A computed operand has no static
// context to resolve a prefix against — which is exactly why
// "for $var in "ABC" return $var castable as xs:QName" is false where the
// same expression with a literal is true — so anything else is left for the
// ordinary path, where an unprefixed name still casts correctly.
func (p *Parser) foldQNameCast(operand Expr, st SequenceType) (Expr, bool, error) {
	if !st.HasAtomicType || st.AtomicType != xdm.TypeQName || st.FacetName != "" {
		return nil, false, nil
	}
	if st.Occurrence != "" && st.Occurrence != "?" {
		return nil, false, nil
	}
	lit, ok := operand.(*Literal)
	if !ok || !isStringLike(lit.Val.Type) {
		return nil, false, nil
	}
	q, ok, err := p.foldQNameConstructor(
		xdm.QName{URI: xdm.NSXS, Local: "QName"}, []Expr{lit})
	if err != nil {
		return nil, false, err
	}
	return q, ok, nil
}
