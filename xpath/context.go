package xpath

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// Context is the XPath dynamic context: everything an expression can observe
// beyond its own AST.
//
// The focus (item, position, size) changes on every step and predicate, while
// the rest (variables, functions, the implicit timezone) changes rarely. They
// are kept in one struct anyway, copied cheaply by value in the hot paths,
// because splitting them means every evaluator function takes two parameters
// and the copy is a handful of words either way.
type Context struct {
	// Item is the context item. It is nil where there is no context item,
	// which is an error to reference rather than an empty sequence.
	Item xdm.Item
	// Position is the context position, 1-based. Zero means "no focus".
	Position int
	// Size is the context size.
	Size int

	// Vars holds in-scope variable bindings, keyed by expanded name.
	// Lookups walk to Parent, so a nested scope does not copy the map.
	Vars   map[string]xdm.Sequence
	Parent *Context

	// Funcs resolves function calls. Supplied by the caller so that XSLT can
	// add xsl:function declarations and extension functions without this
	// package knowing about them.
	Funcs FunctionLibrary

	// StaticBaseURI is the base URI of the expression itself — the stylesheet
	// or query it was written in — which is what fn:static-base-uri returns
	// and what fn:resolve-uri resolves against by default.
	//
	// It is distinct from a *node's* base URI, which comes from the document
	// the node was parsed from. Returning the context node's was the nearest
	// thing available before this existed, and it is a different value: a
	// stylesheet in one place can perfectly well be applied to a document
	// from another.
	StaticBaseURI string

	// collation is the collation in force for string comparison, when a
	// function has been given one. Nil means the codepoint collation, which
	// is the default everywhere.
	//
	// It lives here rather than being threaded through every comparison
	// because fn:deep-equal applies its collation to every string it reaches,
	// however deep in the two sequences that is.
	collation Collation

	// ImplicitTimezone is the offset in minutes applied to date/time values
	// that carry no timezone. The spec requires the dynamic context to supply
	// one; defaulting to UTC keeps results reproducible across machines,
	// which matters more for a validator than matching local time.
	ImplicitTimezone int

	// Ctx carries cancellation. A stylesheet can loop for a long time on
	// pathological input, and the caller needs a way out that does not
	// involve killing the process.
	Ctx context.Context

	// Docs resolves fn:doc and fn:document URIs. Nil disables them, which is
	// the safe default: a stylesheet that can open arbitrary URIs is an SSRF
	// and file-disclosure vector.
	Docs DocumentResolver

	// Collections resolves fn:collection URIs. Nil disables it, for the same
	// reason nil disables Docs, and setting Docs does not set this: see
	// CollectionResolver.
	Collections CollectionResolver

	// Depth guards against unbounded recursion in user-defined functions and
	// named templates, which the spec does not bound.
	Depth int

	// items counts the items materialised into intermediate sequences during
	// this evaluation, bounding memory the way Depth bounds stack.
	//
	// It is a pointer because the Context is copied by value on every scope
	// change — Descend, WithVar, WithFocus — and a plain counter would let
	// each copy accumulate its own, so a nested "for" would never reach any
	// limit. The same reasoning as xsl:message output on the XSLT runtime.
	//
	// Nil means unbounded, which is what a caller building a Context by hand
	// gets; NewContext installs a budget.
	items *int64

	// Now is the value fn:current-dateTime and its siblings return.
	//
	// The spec requires these to be stable for the whole of one evaluation:
	// calling current-dateTime() twice must give the same answer, or a
	// stylesheet that stamps a document and then checks the stamp against
	// "now" can disagree with itself. Reading the clock here once, rather
	// than per call, is what guarantees that. A zero value means the caller
	// did not set one and the functions are unavailable.
	Now time.Time
	// HasNow distinguishes an unset clock from a legitimately zero time.
	HasNow bool
}

// WithNow returns a copy of ctx with the transform clock set.
func (c *Context) WithNow(t time.Time) *Context {
	n := *c
	n.Now, n.HasNow = t, true
	return &n
}

// MaxDepth bounds recursive evaluation.
const MaxDepth = 500

// MaxItems bounds the number of items an evaluation may materialise.
//
// Depth bounds the stack and Ctx bounds the wall clock, but neither bounds
// memory: "count(1 to 9999999)" is one shallow, fast expression that allocates
// nine million *Atomic values and peaked at 1.8 GB of resident memory. The
// range operator had its own limit, but "for $a in 1 to 3000, $b in 1 to 3000"
// walked straight past it, because the sequence is built by the for-expression
// rather than by the range.
//
// The bound is deliberately generous: a real stylesheet over a large document
// works in thousands of nodes, not tens of millions, so this only fires on
// input designed to exhaust memory or on a genuine runaway.
const MaxItems = 5_000_000

// DocumentResolver loads a document by URI for fn:doc and fn:document.
type DocumentResolver interface {
	// ResolveDocument returns the tree for uri, resolved against base.
	ResolveDocument(uri, base string) (*xdm.Tree, error)
}

// CollectionResolver loads a named set of documents for fn:collection.
//
// It is deliberately separate from DocumentResolver rather than an extra
// method on it. A caller who wants fn:doc for the code lists shipped beside a
// stylesheet does not thereby want fn:collection to enumerate a directory, and
// folding the two together would make enabling one enable the other.
//
// The empty uri is the default collection — fn:collection() with no argument.
// A resolver that has no default should return an error for it rather than an
// empty sequence, for the reason given on fnCollection.
type CollectionResolver interface {
	// ResolveCollection returns the documents in uri, resolved against base.
	//
	// The result is a sequence rather than a []*xdm.Tree because a collection
	// is permitted to contain items that are not document nodes.
	ResolveCollection(uri, base string) (xdm.Sequence, error)
}

// FunctionLibrary resolves and calls functions.
type FunctionLibrary interface {
	// Lookup returns the function with the given name and arity.
	Lookup(name xdm.QName, arity int) (Function, bool)
}

// Function is a callable XPath function.
type Function struct {
	Name  xdm.QName
	Arity int
	// Call receives the already-evaluated arguments. Functions that need the
	// context item (fn:string with no argument, fn:position) read it from ctx.
	Call func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error)
}

// NewContext returns a context with the given focus and library.
func NewContext(item xdm.Item, funcs FunctionLibrary) *Context {
	c := &Context{
		Item:     item,
		Funcs:    funcs,
		Vars:     map[string]xdm.Sequence{},
		Ctx:      context.Background(),
		Position: 1,
		Size:     1,
		items:    new(int64),
	}
	if item == nil {
		c.Position, c.Size = 0, 0
	}
	return c
}

// WithFocus returns a copy of ctx with a new context item, position and size,
// sharing the variable scope.
//
// This is the operation performed once per node per step. It copies the struct
// rather than allocating a child scope, so variable lookups still resolve
// through the same maps without a new one being built.
//
// The copy itself does allocate — it is the largest single allocation site in
// the engine, around a quarter of what a stylesheet render allocates. Reusing
// one context across a step loop was measured and made no difference at all
// (4,963,596 vs 4,964,187 bytes per render), so it was reverted: WithVar
// builds children holding a pointer back to this context, and the aliasing
// risk that reuse introduces buys nothing. Anyone tempted to try it again
// should measure first.
func (c *Context) WithFocus(item xdm.Item, pos, size int) *Context {
	n := *c
	n.Item, n.Position, n.Size = item, pos, size
	return &n
}

// WithVar returns a child context binding name to val.
//
// A child scope with its own one-entry map is used rather than mutating the
// parent's, because a for-expression binds a fresh value per iteration while
// the body may capture it; mutation would make all iterations observe the last
// value.
func (c *Context) WithVar(name xdm.QName, val xdm.Sequence) *Context {
	n := *c
	n.Vars = map[string]xdm.Sequence{name.Clark(): val}
	n.Parent = c
	return &n
}

// LookupVar resolves a variable by expanded name, walking enclosing scopes.
func (c *Context) LookupVar(name xdm.QName) (xdm.Sequence, bool) {
	key := name.Clark()
	for s := c; s != nil; s = s.Parent {
		if v, ok := s.Vars[key]; ok {
			return v, true
		}
	}
	return nil, false
}

// ContextNode returns the context item as a node, or an error when there is no
// context item or it is an atomic value.
//
// Steps require a node context; the distinct error codes matter because
// XPDY0002 (absent) and XPTY0020 (present but not a node) mean different
// things to a stylesheet author.
func (c *Context) ContextNode() (*xdm.Node, error) {
	if c.Item == nil {
		return nil, fmt.Errorf("XPDY0002: no context item")
	}
	n, ok := c.Item.(*xdm.Node)
	if !ok {
		return nil, xdm.Errorf("XPTY0020",
			"context item is %s, not a node", c.Item.TypeName())
	}
	return n, nil
}

// withCollation returns a copy of ctx with the collation in force.
func withCollation(ctx *Context, c Collation) *Context {
	if c == nil {
		return ctx
	}
	out := *ctx
	out.collation = c
	return &out
}

// Err reports cancellation, checked at loop boundaries during evaluation.
func (c *Context) Err() error {
	if c.Ctx == nil {
		return nil
	}
	return c.Ctx.Err()
}

// Descend returns a copy with the recursion depth incremented, erroring past
// the limit.
func (c *Context) Descend() (*Context, error) {
	if c.Depth >= MaxDepth {
		return nil, fmt.Errorf("XPDY0001: recursion exceeded %d levels", MaxDepth)
	}
	n := *c
	n.Depth++
	return &n, nil
}

// countItems charges n items against the evaluation budget.
//
// Accumulating constructs call it as they build, so a runaway is stopped while
// it is running rather than after it has already allocated. A Context with no
// budget — one assembled by hand rather than through NewContext — is
// unbounded, which keeps the type usable as a plain value.
func (c *Context) countItems(n int) error {
	if c == nil || c.items == nil || n <= 0 {
		return nil
	}
	if atomic.AddInt64(c.items, int64(n)) > MaxItems {
		return fmt.Errorf(
			"XPDY0130: evaluation materialised more than %d items; "+
				"the expression is building a sequence too large to hold",
			MaxItems)
	}
	return nil
}

// resetItems starts a fresh item budget for one expression evaluation.
func (c *Context) resetItems() {
	if c != nil && c.items != nil {
		atomic.StoreInt64(c.items, 0)
	}
}
