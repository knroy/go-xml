package xpath

import (
	"errors"
	"fmt"
	"strconv"
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
	// depth counts nested expressions, so that a deeply nested one is a
	// static error rather than a crash. See maxParseDepth.
	depth int
	// version is the language version being parsed. It gates the constructs
	// 3.0 adds to the grammar, which a 2.0 processor must not accept.
	version Version
	// refFloor raises the version at which a named function reference is
	// accepted, without admitting any other 3.0 construct. See refversion.go.
	refFloor Version
	// xquery records that the expression came from an XQuery module rather
	// than from a standalone XPath expression.
	//
	// The two languages share this grammar but not the set of tokens that can
	// begin a step: XQuery's RelativePathExpr may start with a direct
	// constructor and XPath's may not. Exactly one rule turns on the
	// difference — xgc:leading-lone-slash, in startsStep — so this is a flag
	// rather than a second parser.
	xquery bool
}

// maxParseDepth bounds expression nesting.
//
// The parser is recursive descent through a fifteen-function precedence
// chain, so one level of nesting costs roughly 5 kB of stack. Without a bound
// a sufficiently nested expression exhausts the goroutine stack, and Go makes
// that "fatal error: stack overflow" — which recover() cannot catch, so it
// kills the process rather than failing the request. Measured before this
// bound existed: 130,000 nested parentheses crashed the process at Go's
// default 1 GB stack ceiling, and 7,000 crashed it under a 32 MB one, which
// is a plausible server setting. A 14 kB expression should not be able to
// take a service down.
//
// The limit matches xdm.DefaultMaxDepth. Both bound the same thing — how far
// a recursive descent may go before the stack is at risk — and the deepest
// expression in either conformance suite is nowhere near it, so a legal
// expression is not refused. An expression this nested is machine-generated
// whatever its intent.
const maxParseDepth = 1000

// Parse compiles an XPath 2.0 expression.
func Parse(src string, ns NamespaceResolver) (Expr, error) {
	return parse(src, ns, false, XPath20)
}

// ParseVersion compiles an expression in the given version of the language.
//
// Parse remains the 2.0 spelling, so an existing caller is unaffected.
func ParseVersion(src string, ns NamespaceResolver, v Version) (Expr, error) {
	return parse(src, ns, false, v)
}

// ParseExtended compiles an expression in which the XPath 3.0 braced URI
// literal Q{uri}local is also accepted.
//
// A stylesheet compiled with Parse still rejects it, which is what a 2.0
// processor must do. It exists for a caller that is itself writing XPath
// rather than running someone else's — specifically the conformance harness,
// whose assertion expressions are written in the 3.0 language even for tests
// whose stylesheets are 2.0.
//
// The simple map operator "!" used to be gated here too. It is now accepted
// unconditionally, along with "||" and "=>": see Lexer.extended.
func ParseExtended(src string, ns NamespaceResolver) (Expr, error) {
	return parse(src, ns, true, XPath20)
}

// ParseVersionRefFloor is ParseVersion with the named-function-reference
// floor raised: "#N" is accepted even when v is below 3.0.
//
// The floor exists because which functions exist -- and so whether a name can
// be referenced at all -- follows the processor rather than the module, in the
// same way Context.LibraryVersion and Context.RegexVersion already do. See
// refversion.go.
func ParseVersionRefFloor(src string, ns NamespaceResolver, v, refFloor Version) (Expr, error) {
	return parseWith(src, ns, false, v, refFloor)
}

// ParseXQuery is ParseVersion for an expression taken from an XQuery module.
//
// It differs from ParseVersion in one rule. XQuery's RelativePathExpr may
// begin with a direct constructor and XPath's may not, so the two languages
// disagree about whether "<" can be the first token of a step — which is what
// xgc:leading-lone-slash consults to decide whether a "/" is a whole
// expression or the head of a path. See Parser.xquery and startsStep.
func ParseXQuery(src string, ns NamespaceResolver, v Version) (Expr, error) {
	return parseHost(src, ns, false, v, 0, true)
}

func parse(src string, ns NamespaceResolver, extended bool, v Version) (Expr, error) {
	return parseWith(src, ns, extended, v, 0)
}

func parseWith(src string, ns NamespaceResolver, extended bool, v, refFloor Version) (Expr, error) {
	return parseHost(src, ns, extended, v, refFloor, false)
}

func parseHost(src string, ns NamespaceResolver, extended bool, v, refFloor Version, xquery bool) (Expr, error) {
	if ns == nil {
		ns = defaultResolver{}
	}
	lex := NewLexer(src)
	if extended {
		lex = newExtendedLexer(src)
	}
	lex.version = v
	toks, err := lex.Tokens()
	if err != nil {
		return nil, fmt.Errorf("%w in %q", err, src)
	}
	if len(lex.bracedURIs) > 0 {
		ns = wrapBraced(ns, lex.bracedURIs)
	}
	p := &Parser{toks: toks, src: src, ns: ns, version: v, refFloor: refFloor,
		xquery: xquery}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if !p.atEOF() {
		return nil, p.errorf("unexpected %q after expression", p.cur().Val)
	}
	return e, nil
}

// bracedResolver answers the synthetic prefixes the lexer substitutes for
// braced URI literals, and delegates every other prefix to the resolver the
// caller supplied.
//
// Rewriting Q{uri}local to a prefixed name keeps the whole feature inside the
// lexer and this one lookup: every place that resolves a name — element test,
// attribute test, variable, function, type — goes through ResolvePrefix, so
// none of them needs to learn a second spelling.
type bracedResolver struct {
	NamespaceResolver
	uris []string
}

func (b bracedResolver) ResolvePrefix(prefix string) (string, bool) {
	if idx, ok := strings.CutPrefix(prefix, bracedURIPrefix); ok {
		n, err := strconv.Atoi(idx)
		if err != nil || n < 0 || n >= len(b.uris) {
			return "", false
		}
		return b.uris[n], true
	}
	return b.NamespaceResolver.ResolvePrefix(prefix)
}

// bracedSchemaResolver is bracedResolver for a resolver that also carries a
// schema.
//
// Embedding only NamespaceResolver promotes only that interface's methods, so
// the wrapper answered ResolvePrefix and nothing else: a resolver that
// implemented SchemaTypes stopped implementing it the moment an expression
// contained a braced URI literal, and every schema lookup in schema_types.go
// took its "no schema in the static context" branch. That made
// schema-element(Q{uri}local) report XPST0008 — "no schema is imported" —
// against a static context that had imported one, while the same test written
// with an ordinary prefix resolved fine. Carrying the inner SchemaTypes
// through restores the one property the wrapper was never meant to change.
type bracedSchemaResolver struct {
	bracedResolver
	SchemaTypes
}

// wrapBraced wraps ns so the synthetic prefixes resolve, preserving the inner
// resolver's schema when it has one.
func wrapBraced(ns NamespaceResolver, uris []string) NamespaceResolver {
	b := bracedResolver{NamespaceResolver: ns, uris: uris}
	if st, ok := ns.(SchemaTypes); ok {
		return bracedSchemaResolver{bracedResolver: b, SchemaTypes: st}
	}
	return b
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
	// Every nesting construct in the grammar — a parenthesised expression, a
	// predicate, a function argument, the branches of "if", the body of "for"
	// and of a quantifier — reaches its operand through here, so counting at
	// this one point bounds them all.
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseDepth {
		// XPST0003 is kept because callers and the conformance suites match
		// on it, but the condition is a resource refusal rather than a
		// syntax fault: the expression is well-formed, merely deeper than
		// this processor will parse. The sentinel is added alongside the
		// code so an embedding caller can tell the two apart.
		return nil, fmt.Errorf("XPST0003: expression nesting exceeds %d levels: %w",
			maxParseDepth, xdm.ErrResourceLimit)
	}
	t := p.cur()
	if t.Kind == TokName {
		switch t.Val {
		case "for":
			// "for" is only the for expression when a variable follows;
			// otherwise it is an element name, as in the path "for/x" or the
			// step "for" standing alone. This is the same test "let" and
			// "if" already carry, and for the same reason: XPath has no
			// reserved words, so every keyword needs it. Without it
			// "for $x in for return 1" -- where the "in" clause selects a
			// child named "for" -- was a syntax error complaining that a
			// variable was expected, rather than a path with no context
			// item. K2-NameTest-5 is built out of exactly this trick.
			//
			// A window clause ("for sliding"/"for tumbling") is XQuery's, not
			// XPath's, and is parsed by the xquery package before the
			// expression reaches here, so a name is the only other thing
			// "for" can be at this point.
			if p.pos+1 < len(p.toks) && p.toks[p.pos+1].Kind == TokVar {
				return p.parseFor()
			}
		case "let":
			// "let" is only the let expression under 3.0 and when followed by
			// a variable; otherwise it is an element name, as in "let/x".
			// XPath has no reserved words, so every keyword needs this test.
			if p.version.atLeast30() && p.pos+1 < len(p.toks) &&
				p.toks[p.pos+1].Kind == TokVar {
				return p.parseLet()
			}
		case "some", "every":
			// Likewise: a quantified expression names its variable straight
			// away, so "some" or "every" not followed by one is an element
			// name and the path "some/x" must parse as such.
			if p.pos+1 < len(p.toks) && p.toks[p.pos+1].Kind == TokVar {
				return p.parseQuantified()
			}
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

// parseLet parses "let $x := expr, $y := expr return expr".
//
// The bindings are sequential: a later one sees the variables bound by the
// earlier ones, which is why evaluation nests the scopes rather than binding
// them all into one.
func (p *Parser) parseLet() (Expr, error) {
	p.pos++ // "let"
	bindings, err := p.parseLetBindings()
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
	return &LetExpr{Bindings: bindings, Return: body}, nil
}

// parseLetBindings parses "$v := expr" clauses separated by commas.
//
// Separate from parseBindings because the separator differs: "in" for a for
// or quantified expression, ":=" here.
func (p *Parser) parseLetBindings() ([]Binding, error) {
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
		if p.cur().Kind != TokOp || p.cur().Val != ":=" {
			return nil, p.errorf("expected ':=' after $%s", name.Local)
		}
		p.pos++
		seq, err := p.parseExprSingle()
		if err != nil {
			return nil, err
		}
		out = append(out, Binding{Var: name, Seq: seq})
		if p.cur().Kind == TokOp && p.cur().Val == "," {
			p.pos++
			continue
		}
		return out, nil
	}
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
	left, err := p.parseStringConcat()
	if err != nil {
		return nil, err
	}
	if op, ok := p.acceptOp(comparisonOps...); ok {
		right, err := p.parseStringConcat()
		if err != nil {
			return nil, err
		}
		// The namespace bindings in scope *here* are the ones an
		// untypedAtomic-to-xs:QName conversion must use. choose-0106 puts the
		// same comparison under three different xmlns declarations and
		// expects three different answers, which is only possible if each
		// operator keeps its own bindings.
		return &BinaryOp{Op: op, Left: left, Right: right,
			ResolveQName: p.qnameResolver()}, nil
	}
	return left, nil
}

// qnameResolver captures the parser's prefix bindings for later use at
// evaluation time. It returns nil when there is no resolver, so that a node
// built without a static context is indistinguishable from one built before
// this existed.
func (p *Parser) qnameResolver() func(string) (string, bool) {
	if p.ns == nil {
		return nil
	}
	ns := p.ns
	return func(prefix string) (string, bool) {
		if prefix == "" {
			return "", true
		}
		return ns.ResolvePrefix(prefix)
	}
}

// parseStringConcat parses the string concatenation operator:
// StringConcatExpr ::= RangeExpr ("||" RangeExpr)*.
//
// XPath 3.0 introduced it, and it sits between comparison and range so that
// "$a || $b eq $c" concatenates before it compares. It is exactly fn:concat
// on two arguments, including concat's treatment of the empty sequence as the
// zero-length string, so it compiles to a call rather than to an operator of
// its own.
func (p *Parser) parseStringConcat() (Expr, error) {
	left, err := p.parseRange()
	if err != nil {
		return nil, err
	}
	for {
		if _, ok := p.acceptOp("||"); !ok {
			return left, nil
		}
		right, err := p.parseRange()
		if err != nil {
			return nil, err
		}
		left = &StringConcat{Left: left, Right: right}
	}
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
		if err := checkNotListType(st, "instance of"); err != nil {
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
		if err := checkNotListType(st, "treat as"); err != nil {
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
			return nil, p.castTargetTypeError(err)
		}
		if err := checkCastTarget(st); err != nil {
			return nil, err
		}
		return &CastExpr{Operand: left, Type: st, Castable: true}, nil
	}
	return left, nil
}

func (p *Parser) parseCast() (Expr, error) {
	left, err := p.parseArrow()
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
			return nil, p.castTargetTypeError(err)
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

// parseUnary parses UnaryExpr ::= ("-" | "+")* ValueExpr.
//
// The grammar puts ArrowExpr *above* UnaryExpr, so the sign binds tighter than
// "=>" and "-1=>abs()" is "abs(-1)", which is 1. Calling parseArrow from here
// had it the other way round — the arrow consumed the bare literal and the
// negation was applied to the call's result, so that expression answered -1.
func (p *Parser) parseUnary() (Expr, error) {
	if op, ok := p.acceptOp("-", "+"); ok {
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryOp{Op: op, Operand: operand}, nil
	}
	return p.parseSimpleMap()
}

// parseArrow parses the arrow operator:
// ArrowExpr ::= UnaryExpr ("=>" ArrowFunctionSpecifier ArgumentList)*
// ArrowFunctionSpecifier ::= EQName | VarRef | ParenthesizedExpr.
//
// "$x => f(1)" is "f($x, 1)": the left operand becomes the call's first
// argument and the written arguments follow it. When the specifier is a name
// this lowers to an ordinary call, so everything a call already does —
// reserved names, QName and schema-constructor folding, arity-keyed lookup —
// applies without change.
//
// The other two specifier forms name no function at compile time: the callee
// is whatever the variable or the parenthesized expression evaluates to, which
// may be a function item but equally a map or an array, both of which are
// callable. Those become a DynamicCall with the left operand prepended to the
// arguments, which is the same lowering one level later. Only the name form
// was accepted before, so every "$x => $f()" in the suite was rejected as a
// syntax error when the spec makes it a type error at worst.
func (p *Parser) parseArrow() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		if _, ok := p.acceptOp("=>"); !ok {
			return left, nil
		}
		switch {
		case p.cur().Kind == TokName:
			left, err = p.parseFunctionCallWith(left)
			if err != nil {
				return nil, err
			}
		case p.cur().Kind == TokVar, p.peekIs(TokOp, "("):
			target, err := p.parseArrowTarget()
			if err != nil {
				return nil, err
			}
			args, err := p.parseArgumentList()
			if err != nil {
				return nil, err
			}
			// The left operand is the first argument, exactly as in the name
			// form; the written arguments follow it.
			left = &DynamicCall{Target: target, Args: append([]Expr{left}, args...)}
		default:
			return nil, p.errorf(
				"expected a function name, variable or parenthesized expression after \"=>\"")
		}
	}
}

// parseArrowTarget parses the VarRef and ParenthesizedExpr forms of
// ArrowFunctionSpecifier.
//
// It deliberately does not go through the postfix parser: "$f(...)" there
// would swallow the argument list as a dynamic call of its own, leaving the
// arrow with nothing to apply and losing the left operand. The specifier is
// just the callee, so only the variable or the parenthesized expression is
// taken and the argument list is left for the caller.
func (p *Parser) parseArrowTarget() (Expr, error) {
	if t := p.cur(); t.Kind == TokVar {
		p.pos++
		name, err := p.resolveVarName(t.Val)
		if err != nil {
			return nil, err
		}
		return &VarRef{Name: name}, nil
	}
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	// "()" is a legal parenthesized expression, and an empty sequence is not
	// callable — but that is FOTY0013 at evaluation, not a syntax error, so
	// it is admitted here and left to DynamicCall to reject.
	if _, ok := p.acceptOp(")"); ok {
		return &SequenceExpr{}, nil
	}
	inner, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectOp(")"); err != nil {
		return nil, err
	}
	return inner, nil
}

// parseSimpleMap parses the XPath 3.0 simple map operator, which sits between
// arrow and path in the grammar: SimpleMapExpr ::= PathExpr ("!" PathExpr)*.
func (p *Parser) parseSimpleMap() (Expr, error) {
	left, err := p.parsePath()
	if err != nil {
		return nil, err
	}
	for {
		if _, ok := p.acceptOp("!"); !ok {
			return left, nil
		}
		right, err := p.parsePath()
		if err != nil {
			return nil, err
		}
		left = &SimpleMap{Left: left, Right: right}
	}
}

// SimpleMap is the XPath 3.0 "!" operator: the right operand is evaluated once
// per item of the left, with that item as the context item, and the results
// are concatenated.
//
// Unlike "/" it neither requires nodes nor sorts, which is the whole reason
// the suite's assertions use it — "string-to-codepoints(...)!string()" maps
// over integers, where "/" would raise XPTY0019.
type SimpleMap struct {
	Left  Expr
	Right Expr
}

func (e *SimpleMap) Eval(ctx *Context) (xdm.Sequence, error) {
	in, err := e.Left.Eval(ctx)
	if err != nil {
		return nil, err
	}
	var out xdm.Sequence
	for i, it := range in {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v, err := e.Right.Eval(ctx.WithFocus(it, i+1, len(in)))
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
	}
	return out, nil
}

func (e *SimpleMap) String() string { return e.Left.String() + "!" + e.Right.String() }

// StringConcat is the XPath 3.0 "||" operator.
//
// It is defined as fn:concat($a, $b), which means it atomizes each operand,
// requires at most one item from each, and treats the empty sequence as the
// zero-length string rather than propagating it — so "() || 'x'" is "x" and
// not the empty sequence.
type StringConcat struct {
	Left  Expr
	Right Expr
}

func (e *StringConcat) Eval(ctx *Context) (xdm.Sequence, error) {
	l, err := e.Left.Eval(ctx)
	if err != nil {
		return nil, err
	}
	r, err := e.Right.Eval(ctx)
	if err != nil {
		return nil, err
	}
	args := []xdm.Sequence{l, r}
	ls, err := argAnyAtomicString(args, 0)
	if err != nil {
		return nil, err
	}
	rs, err := argAnyAtomicString(args, 1)
	if err != nil {
		return nil, err
	}
	return strSeq(ls + rs), nil
}

func (e *StringConcat) String() string {
	return e.Left.String() + " || " + e.Right.String()
}

// castTargetTypeError renumbers a cast target's type error for XQuery 3.0.
//
// The type named in "cast as" and "castable as" is parsed by the same code
// that parses the type in "instance of", so a name that is in scope nowhere
// arrives here as XPST0051 — the correct code for XPath at every version, and
// for XQuery 1.0. XQuery 3.0 gave the cast case an error of its own: a
// SingleType naming a type that is not among the in-scope schema types is
// XQST0052.
//
// The suite states both halves of the rule directly, in one file, over
// character-for-character identical queries that differ only in their spec
// dependency. "'string' cast as xs:untyped" is XPST0051 under XQ10
// (K-SeqExprCast-5) and XQST0052 under XQ30+ (K-SeqExprCast-5a). The same
// pairing holds for xs:anyType (7/7a), for a name that denotes nothing at all
// (9/9a), and for "castable as" (K-SeqExprCastable-6/6a).
//
// Both conditions are load-bearing. The renumbering is XQuery-only, so an
// XPath 3.1 expression still reports XPST0051 — which is why this hangs off
// p.xquery and not off the version alone. And it is 3.0-and-later, so an
// XQuery 1.0 module keeps XPST0051.
//
// The version arm was dead until the xquery package began recording what a
// module's version declaration said and compiling its expressions at the
// matching XPath version: every XQuery expression arrived here at XPath 3.1
// regardless, so the XPST0051 branch could not be taken and the sentence
// above described behaviour the engine could not produce. It now can — see
// xquery.XQVersion.xpathVersion.
//
// Only the *type* error is renumbered. A target that names a well-known type
// which is merely not permitted keeps the code it already had — XPST0080 for
// an abstract type, XPST0003 for a grammar violation such as a trailing "*" —
// because neither of those is "the type is not in scope".
func (p *Parser) castTargetTypeError(err error) error {
	if err == nil || !p.xquery || !p.version.atLeast30() {
		return err
	}
	var e *xdm.Error
	if !errors.As(err, &e) || e.Code != "XPST0051" {
		return err
	}
	// Rebuilt rather than mutated: the error value may be shared, and only
	// the code differs between the two languages.
	return xdm.Errorf("XQST0052", "%s", e.Message)
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
	// xs:error is a legal cast target and a legal castable target: it names a
	// type, so the grammar is satisfied, and the cast then fails at run time
	// with FORG0001 because the type has no instances. Refusing it statically
	// reported a syntax error for an expression that is well-formed.
	if st.IsErrorType {
		return nil
	}
	// xs:numeric is a legal cast target even though it is a union: the spec
	// gives it cast semantics of its own, so the general "unions cannot be
	// cast to" rule does not reach it.
	if st.IsNumericType {
		return nil
	}
	// The built-in list types are legal cast targets too. They have no atomic
	// type code because their value is a sequence of tokens rather than one
	// item; see listtype.go.
	if st.ListItemFacet != "" {
		return nil
	}
	// A named *pure union type* from an imported schema is a legal cast
	// target. XPath 3.1 3.14.2 admits any simple type in the in-scope schema
	// types as a SingleType, and F&O defines the cast by trying the union's
	// member types in order and taking the first that accepts the value —
	// which is why the result is an instance of a member, not of the union
	// itself. import-schema-192 is "'2008-11-14' cast as dateUnion" over a
	// union of xs:date, xs:time and xs:dateTime, and expects an xs:date.
	//
	// Purity is what SchemaUnionMembers already stands for: it is nil unless
	// the union carries no facets and has no list type anywhere in its
	// transitive membership. An impure union therefore still lands on the
	// error below rather than being cast to permissively, which is the strict
	// direction XSD 1.1 3.16.6.3 requires.
	if len(st.SchemaUnionMembers) > 0 {
		return nil
	}
	// A schema-defined list type is a legal cast target for the same reason
	// the built-in ones above are: F&O 3.0 18.3 defines casting "to types
	// derived by restriction, to union types, and to list types". Its value
	// is a sequence, so it has no atomic type code and would otherwise fall
	// into the error below -- which made "castable as s:intListType1" a
	// static error where the spec asks for false.
	if st.SchemaListType {
		return nil
	}
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

// checkNotListType refuses a built-in list type where a sequence type is
// required.
//
// Casting to one is legal -- "xs:untypedAtomic('a b') cast as xs:NMTOKENS"
// yields two items -- but a list type is not an ItemType, so it cannot stand
// in an "instance of", a "treat as", or a function signature. XPath has no
// way to say "a sequence of exactly the tokens this list admits", which is
// why the specification refuses the question rather than answering it.
//
// instanceof111 asks "xs:NMTOKEN('abc') instance of xs:NMTOKENS" and
// FunctionCall-027 declares a parameter "as xs:NMTOKENS"; both require
// XPST0051, the code for a type name that is not in scope as an item type.
func checkNotListType(st SequenceType, where string) error {
	if st.ListItemFacet == "" {
		return nil
	}
	return xdm.Errorf("XPST0051",
		"a list type cannot be used in %s: xs:%s is not an item type",
		where, st.ListItemFacet)
}
