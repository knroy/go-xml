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
		return p.oneItem(n)
	}
	if p.lookingAt("(") {
		p.pos++
		items, err := p.parseParenBody()
		if err != nil {
			return nil, err
		}
		p.skipSpaceAndComments()
		if !p.eof() {
			return nil, p.errorf("XPST0003: unexpected %q",
				firstToken(p.src[p.pos:]))
		}
		return items, nil
	}
	if items, ok, err := p.parseNestedCall(); ok || err != nil {
		return items, err
	}
	return nil, p.errorf(
		"XPST0003: a constructor or FLWOR expression cannot appear here")
}

// oneItem returns n as the whole expression, checking that nothing follows it.
func (p *parser) oneItem(n node) ([]node, error) {
	p.skipSpaceAndComments()
	if !p.eof() {
		return nil, p.errorf("XPST0003: unexpected %q",
			firstToken(p.src[p.pos:]))
	}
	return []node{n}, nil
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
