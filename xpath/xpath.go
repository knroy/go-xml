package xpath

import (
	"context"
	"fmt"

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
	// staticHost is an opaque value the host language attaches to the
	// expression and reads back from Context.StaticHost when it evaluates.
	//
	// It exists so a host-language static property can travel with the
	// expression without this package having to know what it means. XSLT uses
	// it for the package a call is written in, which section 4.4 of that
	// specification makes decide which xsl:strip-space declarations apply to
	// a document the call loads -- a question about where the call appears
	// lexically, so a property of the compiled expression rather than of the
	// runtime that reaches it.
	staticHost any
	// staticCollation is the default collation for this expression, which
	// [xsl:]default-collation sets and which is static for the same reason
	// the base URI is: it is written in the stylesheet.
	staticCollation Collation
	// staticCollationURI is the URI that named staticCollation, kept because
	// fn:default-collation must return the URI and a Collation value cannot
	// be turned back into one: a host-registered collation is the embedder's
	// own type. F&O 3.0 15.7 (functions-and-operators-rec30.xml:26642):
	// "Returns the value of the default collation property from the static
	// context."
	staticCollationURI string
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
	return c.WithDefaultCollationURI(coll, "")
}

// WithDefaultCollationURI is WithDefaultCollation that also records the URI
// that named coll, which is what fn:default-collation reports. A caller that
// resolved a URI to get coll should use this, because the URI cannot be
// recovered from the Collation value afterwards.
func (c *Compiled) WithDefaultCollationURI(coll Collation, uri string) *Compiled {
	if c == nil || coll == nil {
		return c
	}
	n := *c
	n.staticCollation = coll
	n.staticCollationURI = uri
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

// WithStaticHost attaches an opaque host-language value to the expression,
// which Eval puts in Context.StaticHost. See Compiled.staticHost.
func (c *Compiled) WithStaticHost(v any) *Compiled {
	if c == nil || v == nil {
		return c
	}
	n := *c
	n.staticHost = v
	return &n
}

// CompileOptions configures CompileWith.
//
// The zero value compiles XPath 2.0 with no namespace resolver, no deadline
// and no size bound, which is what Compile(src, nil) does. Version in
// particular is XPath20 when unset, matching the zero value of Version itself
// and of Context.Version, so an unset version means the same thing everywhere
// in this package.
//
// An unset Version is safe to get wrong in one direction only: 3.0 and 3.1
// syntax is REFUSED by the 2.0 grammar rather than silently mis-parsed, so a
// host that forgets to set it sees a parse error at the call site rather than
// a wrong answer later. A 3.1 host must still set it.
//
// Fields added here in future are additive: a zero value must always mean the
// behaviour this package had before that field existed.
type CompileOptions struct {
	// Namespaces resolves the prefixes in src. Nil admits none, which is
	// what passing a nil NamespaceResolver does.
	Namespaces NamespaceResolver

	// Version is the language version. The zero value is XPath20.
	Version Version

	// RefFloor raises the version at which a named function reference is
	// admitted, for a host whose own version outruns the expression's. Zero
	// means Version; see refversion.go.
	RefFloor Version

	// XQuery parses src by the rule an expression embedded in an XQuery
	// module follows; see ParseXQuery for the one difference.
	XQuery bool

	// Context bounds the compile. Nil means no deadline.
	//
	// Cancellation is observed before parsing, between parsing and
	// optimisation, and periodically inside the optimiser -- so a deadline
	// is honoured at a granularity rather than instantly, and an expression
	// compiling in microseconds (the ordinary case: the median is ~3µs)
	// never observes it at all.
	//
	// Parsing has no cancellation of its own and is roughly two thirds of
	// the cost on a large expression, so a deadline set to expire mid-compile
	// is observed when parsing finishes rather than during it. MaxBytes is
	// the bound that covers parsing.
	Context context.Context

	// MaxBytes bounds len(src). Zero means unbounded. It is checked before
	// parsing, so an over-large expression costs the comparison and nothing
	// else.
	//
	// This is the deterministic half of the protection Context gives: it
	// refuses the same input every time, where a deadline depends on how
	// loaded the machine is. A host compiling expressions taken from
	// document data -- xsl:evaluate does exactly that, at transform time --
	// should set it.
	MaxBytes int
}

// CompileWith parses src according to opts and optimises the result.
//
// It is the entry point the other Compile functions delegate to; they remain
// the spellings for a caller that needs neither bound, and this one is where
// options added later appear without another positional spelling.
//
// Optimisation happens once per compiled expression, and a compiled
// stylesheet is reused across every node and every document, so anything
// folded here is work removed from the inner loop rather than deferred.
func CompileWith(src string, opts CompileOptions) (*Compiled, error) {
	if opts.MaxBytes > 0 && len(src) > opts.MaxBytes {
		// XPDY0130, not XPST0003. XPath 3.1 2.3.1: "limitations may exist on
		// the maximum numbers or sizes of various objects. An error must be
		// raised if such a limitation is exceeded [err:XPDY0130]", and F.2
		// glosses the code as "An implementation-dependent limit has been
		// exceeded". XPST0003 is a PARSE error, and this expression may parse
		// perfectly well -- it is merely larger than this caller admits.
		//
		// The parser's own depth and chain limits report XPST0003 instead,
		// and say why at parser.go: the conformance suites match on it there.
		// Nothing matches on these two, so they carry the code the
		// specification names, which is also the one this engine already uses
		// for its evaluation-time budgets (context.go).
		return nil, fmt.Errorf("XPDY0130: expression is %d bytes, over the "+
			"%d-byte limit: %w", len(src), opts.MaxBytes, xdm.ErrResourceLimit)
	}
	ctx := opts.Context
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, compileCancelled(err)
		}
	}

	var (
		e   Expr
		err error
	)
	switch {
	case opts.XQuery:
		e, err = ParseXQuery(src, opts.Namespaces, opts.Version)
	case opts.RefFloor != 0 && opts.RefFloor != opts.Version:
		e, err = ParseVersionRefFloor(src, opts.Namespaces, opts.Version,
			opts.RefFloor)
	default:
		e, err = ParseVersion(src, opts.Namespaces, opts.Version)
	}
	if err != nil {
		return nil, err
	}
	// Between the two phases: parsing is bounded by the grammar's own depth
	// and chain limits, optimisation is where the remaining work is.
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, compileCancelled(err)
		}
	}
	opt, err := optimizeContext(ctx, e)
	if err != nil {
		return nil, err
	}
	return &Compiled{
		expr: opt, src: src, ns: opts.Namespaces, version: opts.Version,
	}, nil
}

// compileCancelled reports a cancelled or timed-out compile.
//
// It carries XPDY0130 -- the code XPath 3.1 2.3.1 requires when "an
// implementation-dependent limit has been exceeded" -- rather than returning
// ctx.Err() bare, so a caller that classifies failures by error code sees the
// same code this engine already uses for its evaluation budgets. Both
// xdm.ErrResourceLimit and the context cause stay reachable through
// errors.Is, so a caller can still tell a deadline from a cancellation.
func compileCancelled(err error) error {
	return fmt.Errorf("XPDY0130: compiling the expression was interrupted: "+
		"%w: %w", xdm.ErrResourceLimit, err)
}

// Compile parses src, resolving namespace prefixes with ns.
//
// It is CompileWith with only Namespaces set, and so compiles XPath 2.0. A
// caller that needs a deadline or a size bound calls CompileWith.
func Compile(src string, ns NamespaceResolver) (*Compiled, error) {
	return CompileWith(src, CompileOptions{Namespaces: ns})
}

// CompileVersion is Compile for a given version of the language.
//
// Compile remains the 2.0 spelling so that an existing caller keeps the
// behaviour it had; a 3.0 host calls this instead. The version is recorded on
// the result, so evaluating it does not require the caller to set it on the
// context as well.
//
// Deprecated: use CompileWith with CompileOptions.Version set.
func CompileVersion(src string, ns NamespaceResolver, v Version) (*Compiled, error) {
	return CompileWith(src, CompileOptions{Namespaces: ns, Version: v})
}

// CompileXQuery is CompileVersion for an expression taken from an XQuery
// module; see ParseXQuery for the one rule that differs.
//
// Deprecated: use CompileWith with CompileOptions.XQuery set.
func CompileXQuery(src string, ns NamespaceResolver, v Version) (*Compiled, error) {
	return CompileWith(src, CompileOptions{
		Namespaces: ns, Version: v, XQuery: true,
	})
}

// CompileVersionRefFloor is CompileVersion with the named-function-reference
// floor raised; see ParseVersionRefFloor and refversion.go.
//
// Deprecated: use CompileWith with CompileOptions.RefFloor set.
func CompileVersionRefFloor(src string, ns NamespaceResolver, v, refFloor Version) (*Compiled, error) {
	return CompileWith(src, CompileOptions{
		Namespaces: ns, Version: v, RefFloor: refFloor,
	})
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
	//
	// Unless a host language has taken the boundary over: a FLWOR reaches
	// this once per tuple, and resetting there would clear the counter under
	// the loop that is the thing worth bounding. See Context.HoldItemBudget.
	if ctx == nil || !ctx.heldItems {
		ctx.resetItems()
	}
	// The byte budget takes the same boundary for the same reason, and is held
	// separately: a stylesheet that builds a legitimate string once per node
	// of a large document is doing nothing wrong, and a budget carried across
	// all of them would refuse it. The two holds are independent because the
	// natural unit differs -- a FLWOR for items, one constructed value for
	// bytes -- and a host that holds one does not thereby hold the other.
	if ctx == nil || !ctx.heldBytes {
		ctx.resetBytes()
	}
	if (c.staticBase != "" && c.staticBase != ctx.StaticBaseURI) ||
		c.staticCollation != nil || c.compat != ctx.Compat ||
		c.version != ctx.Version ||
		(c.staticHost != nil && c.staticHost != ctx.StaticHost) ||
		(c.ns != nil && ctx.StaticNamespaces == nil) {
		sub := *ctx
		if c.staticBase != "" {
			sub.StaticBaseURI = c.staticBase
		}
		if c.staticHost != nil {
			sub.StaticHost = c.staticHost
		}
		// The namespaces the expression was compiled against are the ones a
		// prefixed $calendar expands with; see Context.StaticNamespaces.
		if c.ns != nil {
			sub.StaticNamespaces = c.ns
		}
		// The version the expression was compiled in is what its function
		// calls resolve against, whatever the caller's context says: an
		// expression that parsed as 3.0 must not then be evaluated against a
		// 2.0 function library.
		sub.Version = c.version
		if c.staticCollation != nil {
			sub.collation = c.staticCollation
			sub.collationURI = c.staticCollationURI
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
	c, err := CompileWith(src, CompileOptions{Namespaces: ns, Version: v})
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
