package xpath

import (
	"github.com/knroy/go-xml/xdm"
)

// Compiled is a parsed XPath expression, ready to evaluate against any
// context.
//
// Compiling once and evaluating many times is the intended usage: parsing
// dominates the cost of a short expression, and a stylesheet evaluates the
// same expression once per node. A Compiled value is immutable and safe for
// concurrent use.
type Compiled struct {
	expr Expr
	src  string
}

// Compile parses src, resolving namespace prefixes with ns.
func Compile(src string, ns NamespaceResolver) (*Compiled, error) {
	e, err := Parse(src, ns)
	if err != nil {
		return nil, err
	}
	// Optimisation happens once per compiled expression, and a compiled
	// stylesheet is reused across every node and every document, so anything
	// folded here is work removed from the inner loop rather than deferred.
	return &Compiled{expr: optimize(e), src: src}, nil
}

// MustCompile is Compile, panicking on error. For tests and for expressions
// that are literals in this package's own source.
func MustCompile(src string, ns NamespaceResolver) *Compiled {
	c, err := Compile(src, ns)
	if err != nil {
		panic(err)
	}
	return c
}

// Source returns the original expression text.
func (c *Compiled) Source() string { return c.src }

// Expr returns the root of the AST, for callers that need to inspect or
// rewrite it (the XSLT layer analyses patterns this way).
func (c *Compiled) Expr() Expr { return c.expr }

// Eval evaluates the expression in ctx.
func (c *Compiled) Eval(ctx *Context) (xdm.Sequence, error) {
	// The item budget bounds one expression evaluation, not the transform.
	// A stylesheet that evaluates a legitimate 500-item range once per node of
	// a 20,000-node document is doing nothing wrong, and a budget carried
	// across all of them would refuse it after the fortieth node.
	//
	// Resetting here rather than in the accumulating constructs keeps the
	// meaning simple: the limit is on how large a single expression's
	// intermediate sequences may grow, which is exactly the thing that has to
	// fit in memory at once.
	ctx.resetItems()
	return c.expr.Eval(ctx)
}

// EvalString evaluates and returns the string value of the result, which is
// the concatenation rule of fn:string applied to the first item, or "" for the
// empty sequence.
func (c *Compiled) EvalString(ctx *Context) (string, error) {
	seq, err := c.Eval(ctx)
	if err != nil {
		return "", err
	}
	if len(seq) == 0 {
		return "", nil
	}
	switch v := seq[0].(type) {
	case *xdm.Node:
		return v.StringValue(), nil
	case *xdm.Atomic:
		return v.String(), nil
	}
	return "", nil
}

// EvalBool evaluates and returns the effective boolean value.
func (c *Compiled) EvalBool(ctx *Context) (bool, error) {
	seq, err := c.Eval(ctx)
	if err != nil {
		return false, err
	}
	return EffectiveBooleanValue(seq)
}

// Eval is a one-shot compile-and-evaluate, for callers that will not reuse the
// expression.
func Eval(src string, ctx *Context, ns NamespaceResolver) (xdm.Sequence, error) {
	c, err := Compile(src, ns)
	if err != nil {
		return nil, err
	}
	return c.Eval(ctx)
}
