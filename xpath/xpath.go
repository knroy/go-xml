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
	// staticBase is the base URI of the element this expression was written
	// on, when the host language tracks one per expression rather than per
	// module. XSLT does: xml:base on any element changes the static base URI
	// for the expressions within it, so the module's own is only a default.
	// Empty means the context's applies.
	staticBase string
	// staticCollation is the default collation for this expression, which
	// [xsl:]default-collation sets and which is static for the same reason
	// the base URI is: it is written in the stylesheet.
	staticCollation Collation
	// compat is XPath 1.0 compatibility mode, which XSLT 3.8 turns on for the
	// expressions written within an element whose effective [xsl:]version is
	// below 2.0. It is static, so it belongs here rather than on the Context.
	compat bool
	// ns is the namespace resolver src was parsed with, kept so that
	// WithCompatMode can re-parse. See there for why re-parsing is necessary.
	ns NamespaceResolver
	// version is the language version src was parsed in. It is static for the
	// same reason the base URI is — it is a property of where the expression
	// was written — and it is applied to the context at evaluation so that
	// the function library sees the same version the parser did.
	version Version
}

// WithDefaultCollation returns a copy of c whose functions use coll when no
// collation argument is given.
func (c *Compiled) WithDefaultCollation(coll Collation) *Compiled {
	if c == nil || coll == nil {
		return c
	}
	n := *c
	n.staticCollation = coll
	return &n
}

// WithStaticBaseURI returns a copy of c whose expressions resolve relative
// references against base.
//
// It exists because the static base URI really is static: xml:base is written
// in the stylesheet and cannot change between evaluations, so binding it to
// the compiled expression is both correct and cheaper than threading it
// through the dynamic context.
func (c *Compiled) WithStaticBaseURI(base string) *Compiled {
	if c == nil || base == "" {
		return c
	}
	n := *c
	n.staticBase = base
	return &n
}

// Compile parses src, resolving namespace prefixes with ns.
func Compile(src string, ns NamespaceResolver) (*Compiled, error) {
	return CompileVersion(src, ns, XPath20)
}

// CompileVersion is Compile for a given version of the language.
//
// Compile remains the 2.0 spelling so that an existing caller keeps the
// behaviour it had; a 3.0 host calls this instead. The version is recorded on
// the result, so evaluating it does not require the caller to set it on the
// context as well.
func CompileVersion(src string, ns NamespaceResolver, v Version) (*Compiled, error) {
	e, err := ParseVersion(src, ns, v)
	if err != nil {
		return nil, err
	}
	// Optimisation happens once per compiled expression, and a compiled
	// stylesheet is reused across every node and every document, so anything
	// folded here is work removed from the inner loop rather than deferred.
	return &Compiled{expr: optimize(e), src: src, ns: ns, version: v}, nil
}

// CompileVersionRefFloor is CompileVersion with the named-function-reference
// floor raised; see ParseVersionRefFloor and refversion.go.
func CompileVersionRefFloor(src string, ns NamespaceResolver, v, refFloor Version) (*Compiled, error) {
	e, err := ParseVersionRefFloor(src, ns, v, refFloor)
	if err != nil {
		return nil, err
	}
	return &Compiled{expr: optimize(e), src: src, ns: ns, version: v}, nil
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
	if (c.staticBase != "" && c.staticBase != ctx.StaticBaseURI) ||
		c.staticCollation != nil || c.compat != ctx.Compat ||
		c.version != ctx.Version {
		sub := *ctx
		if c.staticBase != "" {
			sub.StaticBaseURI = c.staticBase
		}
		// The version the expression was compiled in is what its function
		// calls resolve against, whatever the caller's context says: an
		// expression that parsed as 3.0 must not then be evaluated against a
		// 2.0 function library.
		sub.Version = c.version
		if c.staticCollation != nil {
			sub.collation = c.staticCollation
		}
		// The compiled expression's mode is authoritative in both
		// directions. A 2.0 expression evaluated from inside a 1.0 scope --
		// an xsl:function called from a 1.0 template, say -- is a 2.0
		// expression, so the flag has to be cleared as well as set.
		sub.Compat = c.compat
		return c.expr.Eval(&sub)
	}
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
	// The context's version decides how the expression is compiled as well as
	// how it evaluates: a caller that set ctx.Version = XPath30 means the
	// whole language, not just the function library.
	v := XPath20
	if ctx != nil {
		v = ctx.Version
	}
	c, err := CompileVersion(src, ns, v)
	if err != nil {
		return nil, err
	}
	return c.Eval(ctx)
}

// WithCompatMode returns a copy of c evaluated under XPath 1.0
// compatibility mode.
//
// The mode is static, exactly as the base URI and the default collation are:
// XSLT 3.8 fixes it from the [xsl:]version attribute of the nearest
// ancestor-or-self of the element the expression is written on, which cannot
// change between evaluations. Binding it to the compiled expression rather
// than threading it through the dynamic context is therefore both correct and
// what keeps an ordinary 2.0 expression byte-identical to what it was: a
// Compiled that was never given the flag never sets it on the context, so no
// evaluation outside a 1.0 scope can observe it.
func (c *Compiled) WithCompatMode(on bool) *Compiled {
	if c == nil || !on {
		return c
	}
	n := *c
	n.compat = true

	// The optimiser folds a closed sub-expression by evaluating it against a
	// bare context, and a bare context is not in compatibility mode: "1 + 1"
	// folds to the xs:integer 2, where under 1.0 it is the xs:double 2, and
	// backwards-027 asks the question directly with "instance of xs:double".
	//
	// Re-parsing rather than re-optimising is deliberate. optimizeChildren
	// rewrites the tree in place, so the folded AST no longer holds the
	// operands to fold differently; the source is the only thing left that
	// does. It costs one parse per expression at stylesheet-compile time,
	// which is once per stylesheet rather than once per node, and only for
	// expressions actually written in a 1.0 scope.
	if c.ns != nil {
		if e, err := Parse(c.src, c.ns); err == nil {
			n.expr = optimizeCompat(e)
		}
	}
	return &n
}

// CompatMode reports whether c evaluates under XPath 1.0 compatibility mode.
func (c *Compiled) CompatMode() bool { return c != nil && c.compat }
