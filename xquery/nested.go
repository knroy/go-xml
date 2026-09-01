package xquery

import (
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// maxNestDepth bounds how deeply expressions this parser reads itself may
// nest, so that a query written to nest them without limit is refused rather
// than exhausting the goroutine stack.
const maxNestDepth = 200

// parseNestedExpr parses an expression this parser has to read itself,
// returning the items its value is the concatenation of.
//
// Only three shapes are unwrapped, and between them they cover every way a
// constructor or a FLWOR reaches an expression that is otherwise ordinary
// XPath: the construct itself; a parenthesised expression, "(<a/>, <b/>)";
// and a function call's arguments, "count(for $x in E group by $k return
// $x)". The last two put the syntax xpath cannot read inside brackets this
// parser can find, and the items inside are parsed by the ordinary item
// parser, which recurses back here for anything nested further.
//
// Anything else is a construct this package does not take apart — an operator
// with a constructor as an operand, say — and is refused by name rather than
// mis-parsed. Refusing rather than guessing is also what makes the recursion
// terminate: every path that recurses does so on a strictly shorter
// substring.
func (p *parser) parseNestedExpr() ([]node, error) {
	if p.depth++; p.depth > maxNestDepth {
		return nil, p.errorf(
			"XPST0003: expressions nested more than %d deep", maxNestDepth)
	}
	defer func() { p.depth-- }()

	p.skipSpaceAndComments()
	if p.startsConstructor() || p.looksLikeFLWOR() || p.looksLikeQuantified() {
		n, err := p.parseItem()
		if err != nil {
			return nil, err
		}
		p.skipSpaceAndComments()
		if p.eof() {
			return []node{n}, nil
		}
		// A path or a predicate over the construct: "<e/>/(for $i in
		// self::node() return $i)" is the mirror of the parenthesised case,
		// with the syntax xpath cannot read on the left of the "/" rather
		// than inside brackets, and it is rewritten the same way.
		return p.pathOver([]node{n})
	}
	if p.lookingAt("(") {
		p.pos++
		items, err := p.parseParenBody()
		if err != nil {
			return nil, err
		}
		p.skipSpaceAndComments()
		if p.eof() {
			return items, nil
		}
		// Something follows the parentheses — "/name()", "[1]", "instance of
		// ...". The value of the parenthesised part is computed here and the
		// rest of the expression compiled by xpath over a variable bound to
		// it, which keeps the path semantics, the predicates and the operator
		// precedence where they belong. "(for $x in E return F)/g" is the
		// idiom the suite leans on hardest, and it cannot be read any other
		// way: the parenthesised half needs this parser and the half after it
		// needs the other one.
		return p.pathOver(items)
	}
	if items, ok, err := p.parseNestedCall(); ok || err != nil {
		return items, err
	}
	return nil, p.errorf(
		"XPST0003: a constructor or FLWOR expression cannot appear here")
}

// parseParenBody reads the comma-separated items of a parenthesised
// expression, up to and including its ")".
func (p *parser) parseParenBody() ([]node, error) {
	var out []node
	p.skipSpaceAndComments()
	if p.consume(")") {
		return out, nil
	}
	for {
		n, err := p.parseItem()
		if err != nil {
			return nil, err
		}
		out = append(out, n)
		p.skipSpaceAndComments()
		if p.consume(",") {
			continue
		}
		if p.consume(")") {
			return out, nil
		}
		return nil, p.errorf("XPST0003: expected %q or %q", ",", ")")
	}
}

// parseNestedCall parses a function call whose arguments this parser has to
// read, rewriting it as a call over values it has already computed.
//
// The call is not evaluated here: the arguments are parsed into nodes, and
// the call itself is compiled by xpath over variables the evaluation binds
// those values to. So the function library, the argument conversion rules and
// the arity resolution all stay where they are, and the only thing this does
// is get the arguments past a parser that could not have read them.
func (p *parser) parseNestedCall() ([]node, bool, error) {
	save := p.pos
	prefix, local, err := p.parseQName()
	if err != nil {
		p.pos = save
		return nil, false, nil
	}
	p.skipSpaceAndComments()
	if !p.consume("(") {
		p.pos = save
		return nil, false, nil
	}
	args, err := p.parseParenBody()
	if err != nil {
		return nil, false, err
	}
	p.skipSpaceAndComments()
	if !p.eof() {
		// Something follows the call — a predicate, an operator — which this
		// package does not take apart.
		p.pos = save
		return nil, false, nil
	}
	c, err := p.compileCallOverArgs(prefix, local, len(args))
	if err != nil {
		return nil, false, err
	}
	return []node{&nestedCall{fn: c, args: args}}, true, nil
}

// compileCallOverArgs compiles "f($a0, ..., $an)", so that a call whose
// arguments this parser had to read is still resolved and applied by xpath.
func (p *parser) compileCallOverArgs(prefix, local string, n int) (*compiledExpr, error) {
	var sb strings.Builder
	if prefix != "" {
		sb.WriteString(prefix)
		sb.WriteByte(':')
	}
	sb.WriteString(local)
	sb.WriteByte('(')
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("$local:" + callArgLocal(i))
	}
	sb.WriteByte(')')
	return p.compileExpr(sb.String())
}

// callArgLocal names the variable one parsed argument is bound to. The
// reserved local-function namespace keeps it from colliding with anything the
// query itself binds.
func callArgLocal(i int) string { return "xq-arg" + strconv.Itoa(i) }

// nestedCall is a function call whose arguments this package had to parse
// itself, because one of them held a constructor or a FLWOR expression.
//
// The call is still xpath's: fn is "f($a0, ..., $an)" compiled by that
// parser, and evaluating it binds each parsed argument's value to the
// matching variable first. Nothing about how the function is resolved or how
// its arguments are converted changes — only where the argument values came
// from.
type nestedCall struct {
	fn   *compiledExpr
	args []node
}

func (n *nestedCall) sequence(ctx *evalContext) (xdm.Sequence, error) {
	xp := ctx.xp
	for i, a := range n.args {
		v, err := (&enclosed{items: []node{a}}).sequence(ctx)
		if err != nil {
			return nil, err
		}
		xp = xp.WithVar(xdm.QName{URI: nsLocal, Local: callArgLocal(i)}, v)
	}
	return n.fn.compiled.Eval(xp)
}

func (n *nestedCall) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.sequence(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq)
}

// pathOver rewrites "E S" — a value this parser had to read, followed by a
// path step or a predicate — so that xpath evaluates the step.
//
// The value is computed here and the rest of the expression compiled over a
// variable bound to it, which keeps the path semantics, the predicates and
// the document ordering where they belong. "(for $x in E return F)/g" is the
// idiom the suite leans on hardest, and it cannot be read any other way: the
// left half needs this parser and the right half needs the other one.
func (p *parser) pathOver(value []node) ([]node, error) {
	rest := p.src[p.pos:]
	if !strings.HasPrefix(rest, "/") && !strings.HasPrefix(rest, "[") {
		// Only a step or a predicate binds tightly enough for the rewrite to
		// preserve precedence. An operator would not: in "(...) + 1 * 2" the
		// substitution is still correct, and in "-(...)" and wherever the
		// left operand is not the whole of what was parsed it is not
		// obviously so. Guessing wrong there changes an answer silently,
		// which is worse than refusing.
		return nil, p.errorf("XPST0003: unexpected %q", firstToken(rest))
	}
	c, err := p.compileExpr("$local:" + parenVar + rest)
	if err != nil {
		return nil, err
	}
	p.pos = len(p.src)
	return []node{&parenPath{value: value, rest: c}}, nil
}

// withTrailingPath wraps n in the path step or predicate that follows it,
// when one does.
//
// It is a no-op wherever nothing follows, which is the ordinary case: an item
// of a query body, an argument of a call, an element of a parenthesised
// sequence. Where something does follow, only a step or a predicate is taken
// — see pathOver for why an operator is not — and anything else is left for
// the caller to report against its own expectations.
func (p *parser) withTrailingPath(n node) (node, error) {
	save := p.pos
	p.skipSpaceAndComments()
	if p.eof() || (p.src[p.pos] != '/' && p.src[p.pos] != '[') {
		p.pos = save
		return n, nil
	}
	src, err := p.scanTrailingPath()
	if err != nil {
		return nil, err
	}
	c, err := p.compileExpr("$local:" + parenVar + src)
	if err != nil {
		return nil, err
	}
	return &parenPath{value: []node{n}, rest: c}, nil
}

// scanTrailingPath returns the source of the steps and predicates following
// the cursor, stopping where the expression they are part of does.
//
// It is the same scan an ExprSingle gets, restricted to what may follow a
// primary: it stops at a comma or a closing bracket it did not open, and at
// a clause keyword, because a path may be the last thing in a clause.
func (p *parser) scanTrailingPath() (string, error) {
	return p.scanExprSingle()
}

// parenVar names the variable a parenthesised expression's value is bound to
// when a step or a predicate follows it. The reserved local-function
// namespace keeps it from colliding with anything the query binds.
const parenVar = "xq-paren"

// parenPath is a parenthesised expression this package had to read, followed
// by a path step or a predicate that xpath reads.
//
// The two halves are evaluated in that order: the parenthesised part produces
// a value, and the rest of the expression is applied to it through a variable.
// Splitting it this way is what lets "(for $x in E return F)/g" work without
// either parser having to understand the other's half.
type parenPath struct {
	value []node
	rest  *compiledExpr
}

func (n *parenPath) sequence(ctx *evalContext) (xdm.Sequence, error) {
	v, err := (&enclosed{items: n.value}).sequence(ctx)
	if err != nil {
		return nil, err
	}
	return n.rest.compiled.Eval(
		ctx.xp.WithVar(xdm.QName{URI: nsLocal, Local: parenVar}, v))
}

func (n *parenPath) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.sequence(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq)
}
