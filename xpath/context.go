package xpath

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
)

// Context is the XPath dynamic context: everything an expression can observe
// beyond its own AST.
//
// It is three parts, by how often each changes. The Context itself is the
// per-scope part -- the focus, the variable bindings, the function library,
// the recursion depth and the host's dynamic state -- and is copied on every
// step, predicate and binding, so it is kept to a few words. The expression's
// static properties (its version, static base URI, namespaces, default
// collation, XPath 1.0 compatibility and host value) change once per compiled
// expression and sit behind one pointer that Compiled.Eval swaps. Everything
// set up once per evaluation -- cancellation, resolvers, limits, the clock and
// the budget counters -- is the Env, shared by every scope of the evaluation
// and never written once installed.
//
// Build one with NewContext, configure its environment with WithEnv, and
// derive scopes with WithFocus and WithVar. A Context is never modified once
// it has been handed to an evaluation; every method that changes something
// returns a copy.
type Context struct {
	// Item is the context item. It is nil where there is no context item,
	// which is an error to reference rather than an empty sequence.
	Item xdm.Item
	// Position is the context position, 1-based. Zero means "no focus".
	// It and Size are int32, as is Depth, so that the Context, which every
	// step, predicate and binding copies, is 96 B rather than 112 B.
	Position int32
	// Size is the context size.
	Size int32
	// Vars holds in-scope variable bindings, keyed by expanded name.
	// Lookups walk to Parent, so a nested scope does not copy the map.
	Vars   map[string]xdm.Sequence
	Parent *Context
	// bind is the one binding WithVar adds, held here rather than in a
	// one-entry Vars map: that map was the largest allocation site in every
	// stylesheet profiled. It is consulted before Vars at the same level. It
	// is a pointer, allocated with the scope WithVar makes, so the copies
	// every focus change and evaluation make do not carry its 56 B.
	bind *varBinding
	// Funcs resolves function calls. Supplied by the caller so that XSLT can
	// add xsl:function declarations and extension functions without this
	// package knowing about them.
	Funcs FunctionLibrary
	// Depth guards against unbounded recursion in user-defined functions and
	// named templates, which the spec does not bound.
	Depth int32
	// heldItems suppresses Compiled.Eval's per-expression reset of items,
	// because a host language is measuring a larger evaluation against the
	// same counter. Set by HoldItemBudget, and copied along with the rest of
	// the Context by every scope change, which is what carries the hold into
	// the nested evaluations it has to cover.
	heldItems bool
	// heldBytes suppresses Compiled.Eval's per-expression reset of bytes,
	// because a host language is measuring a larger evaluation against the
	// same counter. Set by HoldByteBudget; the same mechanism as heldItems,
	// and separate from it because the two budgets have different natural
	// boundaries -- a FLWOR for items, one constructed value for bytes.
	heldBytes bool
	// foreign marks a context handed to host code (see hostCall), which may
	// use it from any goroutine: a call or an evaluation made with it takes
	// the host runtime's EvalLock instead of assuming its caller holds it.
	foreign bool
	// host is the host language's dynamic state (XSLT: the transform runtime
	// and fn:current()), opaque here and copied with the context as the focus
	// is. A dynamic function call clears its current item and marks it
	// absent, as it does the variables in ClearedOnDynamicCall and
	// MarkedOnDynamicCall. See internal/xpathleaf.Host and leafhook.go.
	host *xpathleaf.Host

	// static holds the static properties of the expression being evaluated.
	// Nil means the zero values: XPath 2.0, no base URI, no namespaces, the
	// codepoint collation. Shared and never written; see staticContext.
	static *staticContext

	// env is the evaluation's environment. Nil means the zero Env, which is
	// what a hand-built Context gets. Shared and never written; see Env.
	env *Env
}

// Env is the part of the dynamic context set up once per evaluation and shared
// by every scope within it: cancellation, the resolvers that grant access to
// the outside world, the limits, the clock, and the budget counters.
//
// A Context holds a pointer to one, so deriving a scope never copies it. It is
// therefore never modified once installed: Context.WithEnv changes a copy.
//
// The budget counters are not part of what WithEnv changes. They stay with
// the Context they were minted for (NewContext) or adopted into
// (Context.AdoptBudget), so configuring an environment can never reset or
// remove a budget a caller is being charged against.
type Env struct {
	// Ctx carries cancellation. A stylesheet can loop for a long time on
	// pathological input, and the caller needs a way out that does not
	// involve killing the process.
	Ctx context.Context
	// RegexVersion raises the version of the *regular expression* dialect
	// above Version, without admitting any other 3.0 construct.
	//
	// The two are separable because the regex language is not part of the
	// XPath grammar: a pattern is a string, read by fn:matches and its
	// siblings at the point of call rather than by the parser. So a host may
	// legitimately want a 2.0 expression to accept a 3.0 pattern, which is
	// exactly what XSLT needs -- the XSLT suite runs version="2.0"
	// stylesheets under a 3.0 processor and expects "(?:...)" to compile,
	// because the dialect follows the processor while the syntax follows the
	// module.
	//
	// The zero value adds nothing: the effective dialect is the larger of
	// this and Version, so an existing caller is unaffected.
	RegexVersion Version
	// LibraryVersion raises the version of the *function library* above
	// Version, without admitting any new syntax.
	//
	// Which functions exist is a property of the processor rather than of the
	// module, in the same way the regular-expression dialect is: calling a
	// function is ordinary syntax at every version, and only the name has to
	// resolve. The XSLT suite requires the separation — accessor-050 and
	// fifteen others are version="2.0" stylesheets scoped XSLT30+ that call
	// fn:path, and a 3.0 processor must find it for them.
	//
	// Raising Version instead would also hand those modules inline functions
	// and map constructors, which the XSLT 2.0 grammar must refuse.
	//
	// The zero value adds nothing: the effective floor is the larger of this
	// and Version, so an existing caller is unaffected.
	LibraryVersion Version
	// QualifyVar lets the host language redirect a variable reference to a
	// different name; see VarQualifier. Nil for the flat scoping XPath's own
	// grammar implies.
	QualifyVar VarQualifier
	// MissingVar lets the host language say what an unresolved reference
	// means. Returning nil leaves the ordinary XPST0008. It exists because a
	// host may decline to bind a variable whose evaluation failed, and owe
	// the failure to whoever refers to it -- an XSLT 3.0 abstract variable
	// is the case.
	MissingVar func(ctx *Context, name xdm.QName) error
	// ImplicitTimezone is the offset in minutes applied to date/time values
	// that carry no timezone. The spec requires the dynamic context to supply
	// one; defaulting to UTC keeps results reproducible across machines,
	// which matters more for a validator than matching local time.
	ImplicitTimezone int
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
	// Docs resolves fn:doc and fn:document URIs. Nil disables them, which is
	// the safe default: a stylesheet that can open arbitrary URIs is an SSRF
	// and file-disclosure vector.
	Docs DocumentResolver
	// Collections resolves fn:collection URIs. Nil disables it, for the same
	// reason nil disables Docs, and setting Docs does not set this: see
	// CollectionResolver.
	Collections CollectionResolver
	// Texts resolves fn:unparsed-text URIs. Nil disables it, and setting
	// Docs does not set this: reading a file as raw text is a wider grant
	// than reading it as a parsed document. See TextResolver.
	Texts TextResolver
	// Entities resolves the external entities and external DTD subset that a
	// document handed to fn:parse-xml declares. Nil refuses every one of
	// them, which is the safe default and the one nearly every caller wants:
	// parse-xml is handed a string that came from somewhere, and honouring
	// <!ENTITY e SYSTEM "..."> inside it is XXE by definition. Setting Docs
	// or Texts does not set this — those grant reads of URIs the expression
	// itself named, while this grants reads of URIs the *parsed data* names,
	// which is a different and wider trust decision.
	//
	// Confinement is entirely the resolver's; see xdm.EntityResolver.
	Entities xdm.EntityResolver
	// Environment answers fn:environment-variable and
	// fn:available-environment-variables. Nil withholds the process
	// environment from both, which is the default and the safe one: the
	// environment of a server process routinely holds credentials, and
	// nothing about evaluating an expression implies consent to read them.
	//
	// Setting Docs or Texts does not set this, and this does not set those:
	// those grant reads of a URI space the caller has confined, while this
	// grants reads of the process's own state, which no resolver root
	// bounds. Withholding costs no conformance — see EnvironmentResolver.
	Environment EnvironmentResolver
	// Modules locates the XQuery library modules fn:load-xquery-module
	// loads. Nil reads nothing, so every module is FOQM0002, which is the
	// default for the reason Docs is nil by default: the module URI and its
	// location hints are strings the expression chose. An XQuery query
	// fills this from its own Options.Modules and Options.ModuleResolver
	// when the caller left it nil. See ModuleResolver.
	Modules ModuleResolver
	// Validator validates a tree fn:json-to-xml has just built, when the
	// call asked for validate=true. Nil means the processor cannot do it,
	// which is FOJS0004 rather than a silent untyped result; see
	// TreeValidator.
	Validator TreeValidator
	// MapDuplicateCode overrides the error code raised when a map constructor
	// names the same key twice.
	//
	// The construct is one expression with two spellings of the same failure,
	// because the code is the host language's rather than XPath's. XQuery 3.1
	// section 3.11.1 calls it XQDY0137, which is the default and what the QT3
	// suite requires. XSLT 3.0 section 17.4 says of the very same MapExpr that
	// "if two or more entries have the same key then a dynamic error occurs
	// [see ERR XTDE3365]", so an XSLT host sets this to XTDE3365 -- matching
	// the code xsl:map already raises for a duplicate, which is the point: in
	// XSLT the two ways of writing a map agree on how they fail.
	//
	// The zero value keeps XQDY0137, so a host that does not set it behaves
	// exactly as it did.
	MapDuplicateCode string
	// MaxDepth is the bound Depth is checked against. Zero means the package
	// default, MaxDepth.
	//
	// It is settable because the default is a guard against untrusted input
	// rather than a limit the language imposes: a query that recurses five
	// thousand deep is perfectly legal, and fn-format-number's numberformat121
	// and 122 do exactly that on purpose. A caller evaluating an expression it
	// trusts can raise the bound; one evaluating an expression from outside
	// should leave it alone.
	MaxDepth int
	// MaxItems is the bound the item budget is checked against. Zero means
	// the package default, MaxItems; a negative value means no bound; a
	// positive value is the bound. The range operator's cap follows it too.
	//
	// It is settable for the reason MaxDepth is: the default guards against
	// untrusted input, and a trusted query over a large document can need
	// more: XMark q11 and q12 at factor 1 exceed it.
	//
	// Note the convention differs from MaxDepth's, where a negative value
	// falls back to the default rather than removing the bound.
	//
	// AdoptBudget copies it along with the shared counter, so a nested
	// evaluation cannot raise the bound it is charged against.
	MaxItems int
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
	// bytes counts the bytes of string content this evaluation has built,
	// bounding the one dimension items cannot: a string is a single item
	// however long it is, so a chain of concatenations that doubles its
	// result each step passes the item budget untouched. See MaxBytes.
	//
	// A pointer for the same reason items is one -- the Context is copied by
	// value on every scope change, and a plain counter would let each copy
	// accumulate its own. Nil means unbounded, which is what a hand-built
	// Context gets.
	bytes *int64
	// entities is the entity-expansion allowance shared by every parse this
	// evaluation performs, bounding the one dimension neither items nor bytes
	// can: fn:parse-xml builds a TREE out of a string, so an expansion is
	// neither an intermediate sequence nor built string content, and both
	// counters walk straight past it.
	//
	// It exists because a parse is not a document when an expression is the
	// thing doing the parsing. fn:parse-xml is an ordinary function in the
	// default library, so an expression calls it once per node, and each call
	// minted a fresh xdm entity allowance -- so the 1 MB ceiling bounded each
	// of sixty calls separately rather than together, and 1,328 bytes of XPath
	// expanded 47,185,920 bytes and allocated 180 MB, accepted. Sharing one
	// allowance across the evaluation is what makes the ceiling bound the
	// evaluation rather than the call.
	//
	// A pointer for the same reason items and bytes are pointers: the Context
	// is copied by value on every scope change, and a plain value would let
	// each copy accumulate its own. Nil means unbounded, which is what a
	// hand-built Context gets; NewContext installs one.
	entities *xdm.EntityBudget
	// nodes counts the nodes constructed into result trees during this
	// evaluation, bounding the one dimension neither items nor bytes sees.
	//
	// A result tree is not an intermediate sequence and not string content, so
	// it passed both budgets untouched: two nested xsl:for-each over //i is a
	// three-line stylesheet whose result is the SQUARE of the input's node
	// count, and 24 kB of input built nine million nodes and allocated 44 GB
	// before returning. Each loop is individually far under MaxItems, and the
	// item budget measures sequences rather than tree construction, so nothing
	// counted the product.
	//
	// Like entities and unlike items and bytes, it is NOT reset per expression
	// by Compiled.Eval. The tree under construction outlives the expression
	// that contributed to it -- that is what makes it a result tree -- so a
	// budget an expression could restart by being a new expression would never
	// see the growth. See MaxNodes.
	//
	// A pointer for the same reason the others are: the Context is copied by
	// value on every scope change, and a plain counter would let each copy
	// accumulate its own. Nil means unbounded, which is what a hand-built
	// Context gets; NewContext installs a budget.
	nodes *int64
}

// staticContext is the static part of the context: the properties of the
// expression being evaluated rather than of the evaluation. Compiled.Eval
// installs the compiled expression's own, so one pointer swap replaces the
// copy of every field. Never written once a Context points at it.
type staticContext struct {
	// Version is the language version the expression was compiled under.
	//
	// It reaches the function library because a few functions differ between
	// versions in ways the parser cannot settle: fn:matches and its siblings
	// accept the "q" flag and the 3.0 regular expression constructs only
	// under 3.0, and must raise the same errors as any other processor when
	// asked to be 2.0. The zero value is XPath20, so a Context built by an
	// existing caller behaves exactly as it did before.
	version Version
	// StaticBaseURI is the base URI of the expression itself — the stylesheet
	// or query it was written in — which is what fn:static-base-uri returns
	// and what fn:resolve-uri resolves against by default.
	//
	// It is distinct from a *node's* base URI, which comes from the document
	// the node was parsed from. Returning the context node's was the nearest
	// thing available before this existed, and it is a different value: a
	// stylesheet in one place can perfectly well be applied to a document
	// from another.
	baseURI string
	// StaticHost is an opaque value the host language attached to the
	// expression being evaluated; see Compiled.WithStaticHost. This package
	// never interprets it, only carries it.
	host any
	// StaticNamespaces is the statically known namespaces of the expression,
	// carried from compile time so that a function can expand a prefix that
	// reaches it as a *string* rather than as syntax.
	//
	// Almost every prefix in an expression is resolved by the parser, which is
	// why NamespaceResolver is a compile-time interface. The exception is a
	// prefixed name that arrives as an argument value: F&O 9.8.4.3 says the
	// $calendar argument of the date formatting functions "must be a valid
	// EQName ... if it is a lexical QName then it is expanded into an expanded
	// QName using the statically known namespaces", and the functions' own
	// Properties section lists them as depending on "namespaces" for exactly
	// this reason. The argument need not be a literal, so the expansion cannot
	// be done at parse time; the resolver has to survive into evaluation.
	//
	// Nil means the caller compiled without one, and a prefixed calendar is
	// then unresolvable — which is the same answer an empty resolver gives.
	ns NamespaceResolver
	// collation is the collation in force for string comparison, when a
	// function has been given one. Nil means the codepoint collation, which
	// is the default everywhere.
	//
	// It lives here rather than being threaded through every comparison
	// because fn:deep-equal applies its collation to every string it reaches,
	// however deep in the two sequences that is.
	collation Collation
	// collationURI is the URI that named collation, when the default came
	// from the static context. fn:default-collation returns it; every other
	// collation-taking function needs only the Collation value. Empty means
	// the codepoint collation, which is the default the spec states.
	collationURI string
	// Compat is XPath 1.0 compatibility mode, which XSLT 3.8 puts in force for
	// expressions written on an element whose effective [xsl:]version is below
	// 2.0. Under it the coercion rules of XPath 2.0 appendix B.1 apply: a
	// multi-item argument to a parameter expecting a string, a number or a
	// node is truncated to its first item instead of raising XPTY0004,
	// arithmetic on a non-numeric operand yields NaN rather than a type error,
	// and a general comparison converts its operands the way XPath 1.0 did.
	//
	// It defaults to false and is set only by a Compiled that was given it, so
	// ordinary 2.0 evaluation never sees it.
	compat bool
}

// zeroEnv and zeroStatic are what a Context with no environment or static
// part reads. Never written.
var (
	zeroEnv    Env
	zeroStatic staticContext
)

// ev returns c's environment, or the zero one. Internal: callers outside the
// package go through Env, which never hands out the shared zero value.
func (c *Context) ev() *Env {
	if c == nil || c.env == nil {
		return &zeroEnv
	}
	return c.env
}

// st returns c's static part, or the zero one.
func (c *Context) st() *staticContext {
	if c == nil || c.static == nil {
		return &zeroStatic
	}
	return c.static
}

// Env returns the environment c evaluates in. It is shared with every scope
// derived from c and must not be modified; WithEnv changes a copy. A Context with no environment reports a fresh zero Env.
func (c *Context) Env() *Env {
	if c == nil || c.env == nil {
		return new(Env)
	}
	return c.env
}

// WithEnv returns a copy of c whose environment is c's with set applied to a
// copy of it:
//
//	ctx = ctx.WithEnv(func(e *xpath.Env) {
//		e.Docs = resolver
//		e.MaxItems = -1
//	})
//
// The budget counters are c's own whatever set does, so configuring an
// environment never resets, replaces or removes a budget; sharing another
// evaluation's is AdoptBudget's job. set must not keep e.
func (c *Context) WithEnv(set func(e *Env)) *Context {
	own := c.ev()
	env := *own
	set(&env)
	env.items, env.bytes, env.entities, env.nodes =
		own.items, own.bytes, own.entities, own.nodes
	n := *c
	n.env = &env
	return &n
}

// Version is the language version of the expression being evaluated.
func (c *Context) Version() Version { return c.st().version }

// StaticBaseURI is the static base URI of the expression being evaluated.
func (c *Context) StaticBaseURI() string { return c.st().baseURI }

// StaticHost is the opaque value the host language attached to the expression
// being evaluated; see Compiled.WithStaticHost.
func (c *Context) StaticHost() any { return c.st().host }

// StaticNamespaces is the statically known namespaces of the expression being
// evaluated, or nil where it was compiled without them.
//
// They are installed only for an expression that reads them at run time: one
// that calls or references fn:format-date, fn:format-dateTime,
// fn:format-time or fn:function-lookup. Any other expression sees whatever
// its caller's context held, which is nil at the top level, so a host
// function must not rely on this to expand a prefix it was given as a
// string; it resolves the prefix against namespaces it captured itself.
func (c *Context) StaticNamespaces() NamespaceResolver { return c.st().ns }

// Compat reports XPath 1.0 compatibility mode; see Compiled.WithCompatMode.
func (c *Context) Compat() bool { return c.st().compat }

// WithVersion returns a copy of c whose expressions evaluate as version v
// until a compiled expression installs its own. It is for a caller evaluating
// an expression it did not compile through Compiled -- the version of a
// Compiled is always the one it was compiled under.
func (c *Context) WithVersion(v Version) *Context {
	if c.st().version == v {
		return c
	}
	s := *c.st()
	s.version = v
	n := *c
	n.static = &s
	return &n
}

// WithStaticHost returns a copy of c whose StaticHost is v until a compiled
// expression installs its own. A host language whose expressions all carry
// one value sets it here once, so that evaluating them does not copy the
// context to install it.
func (c *Context) WithStaticHost(v any) *Context {
	if comparableValue(v) && c.st().host == v {
		return c
	}
	s := *c.st()
	s.host = v
	n := *c
	n.static = &s
	return &n
}

// WithStaticBaseURI returns a copy of c with the static base URI set, for the
// expressions that were compiled without one of their own.
func (c *Context) WithStaticBaseURI(base string) *Context {
	if c.st().baseURI == base {
		return c
	}
	s := *c.st()
	s.baseURI = base
	n := *c
	n.static = &s
	return &n
}

// MaxDepth bounds recursive evaluation by default.
//
// It is a denial-of-service guard for a caller evaluating an expression it did
// not write, not a conformance limit: nothing in the specification caps
// recursion, and a query is entitled to recurse as deeply as it likes. A
// caller that trusts its input can raise the bound through Env.MaxDepth,
// which is what the conformance harnesses do — xslt.TransformOptions has
// carried the same escape hatch for template recursion all along.
const MaxDepth = 500

// depthLimit is the bound in force, which is Env.MaxDepth where the caller
// set one and MaxDepth otherwise.
func (c *Context) depthLimit() int {
	if c.ev().MaxDepth > 0 {
		return c.ev().MaxDepth
	}
	return MaxDepth
}

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

// MaxBytes bounds the string content one evaluation may build.
//
// MaxItems bounds how many items an evaluation materialises, and a string is
// one item however long it is, so nothing bounded the bytes: twenty-six
// nested "let"s, each concatenating the previous string with itself, is a
// 1,009-byte expression that returned 671,088,640 bytes without complaint.
// Four more lines is ten gigabytes. The limits in docs/security.md all bound
// bytes at ingress -- what a parse, a module or an external entity may read --
// and none of them sees a string produced during evaluation.
//
// The bound is set from measurement rather than taste. Instrumenting the
// charge points and running the suites and the real-world corpora, the
// largest legitimate accumulation was 14,516,346 bytes, in the XSLT 3.0
// suite. The XQuery suite peaked at 4,382,554 -- Constr-cont-document-3,
// codepoints-to-string over every valid XML codepoint -- the XPath suite at
// 4,194,304, XSLT 2.0 at 753,560, and the DocBook xslTNG and XSpec corpora,
// 877 real documents through two large real stylesheets, at 1,031,269.
// A gibibyte is 74 times the largest of those, so a transform that serialises
// a big document into one text node or joins a whole corpus is unaffected,
// which matters more than the bound being tight: a false rejection is a
// conformance bug, and refusing legitimate work would be worse than the
// runaway this guards.
//
// The charge is cumulative over the evaluation, not per string, so the
// doubling chain is refused while it is still doubling -- the step that would
// cross the bound never allocates.
const MaxBytes = 1 << 30

// MaxNodes bounds the number of nodes one evaluation may construct into
// result trees.
//
// MaxItems bounds the items an expression materialises and MaxBytes the string
// content it builds, and a result tree is neither. It is not an intermediate
// sequence -- it outlives the expression that contributed to it, which is what
// makes it a result -- and its size is in nodes rather than characters. So a
// stylesheet whose result is the CROSS PRODUCT of its input passed both
// budgets without being charged anything: two nested xsl:for-each over //i, a
// three-line stylesheet, squares the input's node count. Measured, 811 bytes
// of input built 10,000 nodes, and 24 kB built 9,000,000 nodes and allocated
// 44 GB over 49 seconds before returning a result. Nothing refused it, because
// each loop is individually a few thousand items and the limit that could have
// seen the product does not measure trees.
//
// This memory differs from the other two in being RETAINED rather than
// transient: the tree is the caller's result and is live until the caller
// discards it, so no amount of garbage collection reclaims it while it is
// being built. That is what the bound is drawn from. A constructed node costs
// on the order of a few hundred bytes of live heap, measured by building trees
// at two sizes and reading HeapAlloc across a forced collection: 328 bytes per
// constructed element on darwin/arm64, stable to a tenth of a byte between
// 160,000 and 640,000 nodes. So two million nodes caps a result tree near
// 0.6 GB. An earlier note here recorded 672 bytes and a 1.3 GB cap; that
// figure did not reproduce and no test pinned it. The discrepancy is in the
// safe direction -- the real ceiling is half what was claimed, so the bound
// binds sooner than advertised rather than later -- but the number is stated
// here as an order of magnitude for future reasoning, not as a constant to
// compute against. Per-node cost varies with node kind, name length and
// platform, so re-measure before drawing a new bound from it.
//
// Setting it from a margin over legitimate work instead is the instructive
// failure. Fifty million is a much larger multiple of anything real, and it
// let the measured case allocate 128 GB over seven minutes before refusing,
// which is not a memory bound at all; ten million still reached 6.7 GB
// retained. The bound has to come from the memory it caps.
//
// It is nonetheless far above real work, which is the constraint that matters
// on the other side. Instrumenting this charge point and running the suites
// and the real-world corpora, the largest legitimate result tree was 274,719
// nodes, in the XSLT suites; the XSpec corpus peaked at 52,607, the XQuery QT3
// lane at 35,328, and the DocBook xslTNG corpus at 35,104 across 577 real
// documents through a large real stylesheet. Two million is seven times the
// largest of those and thirty-eight times the largest real-world one. A false
// rejection is a conformance bug, and refusing legitimate work would be worse
// than the runaway this guards, so the margin is deliberate.
//
// The charge is cumulative over the whole evaluation, not per instruction or
// per expression, and is never reset -- entities is the precedent. A budget an
// expression could restart by being a new expression would never see a loop.
const MaxNodes = 2_000_000

// DocumentResolver loads a document by URI for fn:doc and fn:document.
type DocumentResolver interface {
	// ResolveDocument returns the tree for uri, resolved against base.
	ResolveDocument(uri, base string) (*xdm.Tree, error)
}

// ContextDocumentResolver is a DocumentResolver that also wants the
// evaluation context of the call.
//
// It is a separate optional interface rather than an extra parameter on
// ResolveDocument so that existing implementations keep working: a resolver
// that does not implement it is called through ResolveDocument exactly as
// before. fn:doc and fn:document prefer this method where it is offered.
//
// XSLT needs it because whitespace stripping is scoped to the package the
// CALL is written in -- section 4.4 of that specification -- so which
// declarations apply is a property of the expression rather than of the
// transform, and only the context carries it.
type ContextDocumentResolver interface {
	DocumentResolver
	// ResolveDocumentIn is ResolveDocument for a call made from ctx.
	ResolveDocumentIn(ctx *Context, uri, base string) (*xdm.Tree, error)
}

// resolveDocument loads a document through the richer interface when the
// resolver offers one, and through the plain one otherwise.
func resolveDocument(ctx *Context, uri, base string) (*xdm.Tree, error) {
	if cr, ok := ctx.ev().Docs.(ContextDocumentResolver); ok {
		return resolveDocumentIn(cr, ctx, uri, base)
	}
	return ctx.ev().Docs.ResolveDocument(uri, base)
}

// resolveDocumentIn calls r.ResolveDocumentIn. A resolver not of this
// module is host code handed the context: see hostCall.
func resolveDocumentIn(r ContextDocumentResolver, ctx *Context, uri, base string) (*xdm.Tree, error) {
	if _, ok := r.(xpathleaf.NativeResolver); ok {
		return r.ResolveDocumentIn(ctx, uri, base)
	}
	return hostCall(ctx, func(c *Context) (*xdm.Tree, error) { return r.ResolveDocumentIn(c, uri, base) })
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

// ContextCollectionResolver is a CollectionResolver that also sees the
// evaluation context of the fn:collection call.
//
// It mirrors ContextDocumentResolver and exists for the same reason: XSLT 4.4
// scopes xsl:strip-space to the package the call appears in LEXICALLY --
// "Declarations within a library package only affect the handling of documents
// loaded using a call on the document, doc, or collection functions ...
// appearing lexically within the same package" -- and the context is what
// carries the package. A resolver that does not implement this is called
// through ResolveCollection as before.
type ContextCollectionResolver interface {
	CollectionResolver
	ResolveCollectionIn(ctx *Context, uri, base string) (xdm.Sequence, error)
}

// resolveCollectionIn loads a collection through ContextCollectionResolver
// where the resolver offers it, and through the plain interface otherwise.
func resolveCollectionIn(ctx *Context, uri, base string) (xdm.Sequence, error) {
	if cr, ok := ctx.ev().Collections.(ContextCollectionResolver); ok {
		if _, ok := cr.(xpathleaf.NativeResolver); ok {
			return cr.ResolveCollectionIn(ctx, uri, base)
		}
		return hostCall(ctx, func(c *Context) (xdm.Sequence, error) { return cr.ResolveCollectionIn(c, uri, base) })
	}
	return ctx.ev().Collections.ResolveCollection(uri, base)
}

// TextResolver reads a resource as text for fn:unparsed-text.
//
// It is deliberately separate from DocumentResolver rather than reusing it.
// fn:doc parses what it reads as XML, so a resolver granting it hands out
// well-formed documents; fn:unparsed-text hands the stylesheet the raw bytes
// of any file the resolver will open, which is a strictly larger disclosure
// and a different decision for the caller to make. Nil disables the function,
// which is the default and the safe one.
type TextResolver interface {
	// ResolveText returns the text of uri, resolved against base, decoded
	// using encoding when one is named and as UTF-8 when it is empty.
	ResolveText(uri, base, encoding string) (string, error)
}

// ModuleResolver locates the source of an XQuery library module for
// fn:load-xquery-module. It has the shape of xquery.ModuleResolver, so any
// value of that type serves here; it is declared in this package because
// xpath cannot import xquery.
type ModuleResolver interface {
	// Resolve returns the source of the library module whose target
	// namespace is namespace; hints are the caller's location hints and base
	// is what they resolve against. A nil reader and a nil error mean "no
	// such module".
	Resolve(namespace string, hints []string, base string) (io.ReadCloser, string, error)
}

// FunctionLibrary resolves and calls functions.
type FunctionLibrary interface {
	// Lookup returns the function with the given name and arity.
	Lookup(name xdm.QName, arity int) (Function, bool)
}

// DynamicFunctionLibrary is a FunctionLibrary that answers a DYNAMIC function
// reference -- fn:function-lookup and fn:function-available, where the name is
// a value rather than a literal -- differently from a call written out in the
// source.
//
// A host language may scope the two differently. XSLT 3.0 3.6.3.5 does: a
// dynamic reference sees only the functions declared in the package the call
// is written in, where an ordinary call resolves against the whole assembled
// stylesheet. The context is passed because the package is a property of the
// expression, carried on Context.StaticHost.
//
// A library that does not implement this is scoped identically either way,
// which is what XPath on its own means.
type DynamicFunctionLibrary interface {
	FunctionLibrary
	// LookupDynamic returns the function a dynamic reference to this name and
	// arity resolves to.
	LookupDynamic(ctx *Context, name xdm.QName, arity int) (Function, bool)
}

// ScopedFunctionLibrary is a FunctionLibrary whose answer to an ORDINARY call
// depends on where the call is written.
//
// Plain XPath has no such notion: a function is either in the static context
// or it is not, and one library answers for the whole expression. XSLT 3.0
// packages break that. 3.6.3.4 puts in a package's static context only "the
// components of the packages it uses that are visible to it" -- public, final
// or abstract -- so a PRIVATE function of a used package is not callable from
// the using package even though it is perfectly callable from inside the
// package that declares it. One name, two answers, decided by the caller.
//
// The context is passed for the same reason DynamicFunctionLibrary passes it:
// the package a call was written in is a property of the expression, carried
// on Context.StaticHost.
//
// A library that does not implement this is scoped the same wherever it is
// called from, which is what XPath on its own means.
type ScopedFunctionLibrary interface {
	FunctionLibrary
	// LookupFrom returns the function a call written in ctx's package resolves
	// to, or false where that package may not see it.
	LookupFrom(ctx *Context, name xdm.QName, arity int) (Function, bool)
}

// Function is a callable XPath function.
type Function struct {
	Name  xdm.QName
	Arity int
	// Call receives the already-evaluated arguments. Functions that need the
	// context item (fn:string with no argument, fn:position) read it from ctx.
	Call func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error)
	// Since is the first language version in which this function exists. The
	// zero value is XPath20, so every function that predates versioning is
	// available everywhere, and a 3.0 addition is invisible to a 2.0
	// expression rather than being quietly callable from one.
	//
	// The check belongs at lookup rather than in each function body: a 2.0
	// expression calling fn:head must get "unknown function", the same static
	// error every other processor raises, not a working answer or a distinct
	// complaint from inside a function it should not have found.
	//
	// It applies only to the fn: namespace in practice. The math: functions
	// are gated by their namespace instead — see registerMathFuncs.
	Since Version

	// Signature is the declared return type followed by the parameter types,
	// in source spelling. It is what a typed function test — "f#1 instance of
	// function(element(A)) as xs:string" — is judged against.
	//
	// applyBuiltinSignatures fills this from specSignatures for every
	// function in the four namespaces the F&O manifest covers, so it is nil
	// only for a function with no declared type to read: a host or EXSLT
	// extension, or a stylesheet's own. Such a function is matched on arity
	// alone, which is the right answer there rather than merely the
	// permissive one -- its declared type is whatever declared it, not
	// "nothing".
	Signature []string

	// VariadicSignature carries a variadic function's declared type without
	// repeating its one parameter, and mirrors the field of the same name on
	// xdm.FunctionItem -- see the commentary there for why the materialised
	// form is a liability rather than a convenience. Nil for every
	// fixed-arity function, which leaves Signature the ordinary path.
	VariadicSignature *xdm.VariadicSignature

	// leaf marks a builtin that never calls back into user code, never
	// keeps the context past its return and never writes to it, which is
	// what lets a call be answered without calling it (see nameOf). Set
	// only by markLeafBuiltins and xpathleaf.Mark.
	leaf bool
}

// NewContext returns a context with the given focus and library, in a fresh
// environment carrying its own budgets. Each configure function, if any, is
// applied to the environment first, which is WithEnv without the copy:
//
//	ctx := xpath.NewContext(doc, xpath.Builtins(), func(e *xpath.Env) {
//		e.Docs = resolver
//	})
//
// The budgets are installed after them, so configure cannot remove them.
func NewContext(item xdm.Item, funcs FunctionLibrary, configure ...func(e *Env)) *Context {
	// The root scope and its environment in one allocation.
	r := &struct {
		c Context
		e Env
	}{}
	r.e.Ctx = context.Background()
	for _, set := range configure {
		set(&r.e)
	}
	r.e.items, r.e.bytes, r.e.nodes = new(int64), new(int64), new(int64)
	// One entity-expansion allowance for the whole evaluation. Unlike items
	// and bytes it is NOT reset per expression by Compiled.Eval: the reset is
	// what the per-call mint already amounted to, and a budget an expression
	// can restart by being a new expression is not a budget. See
	// Env.entities.
	r.e.entities = xdm.NewEntityBudget()
	r.c = Context{
		Item:     item,
		Funcs:    funcs,
		Vars:     map[string]xdm.Sequence{},
		Position: 1,
		Size:     1,
		env:      &r.e,
	}
	if item == nil {
		r.c.Position, r.c.Size = 0, 0
	}
	return &r.c
}

// WithFocus returns a copy of ctx with a new context item, position and size,
// sharing the variable scope.
//
// This is the operation performed once per node per step. It copies the struct
// rather than allocating a child scope, so variable lookups still resolve
// through the same maps without a new one being built.
//
// The copy itself does allocate — it is the largest single allocation site in
// the engine, around a quarter of what a stylesheet render allocates (counted
// when the copy was 512 bytes; it is 112 since the environment, the static
// part and the WithVar binding moved behind pointers). Reusing
// one context across a step loop was measured and made no difference at all
// (4,963,596 vs 4,964,187 bytes per render), so it was reverted: WithVar
// builds children holding a pointer back to this context, and the aliasing
// risk that reuse introduces buys nothing. Anyone tempted to try it again
// should measure first.
func (c *Context) WithFocus(item xdm.Item, pos, size int) *Context {
	n := *c
	n.Item, n.Position, n.Size = item, int32(pos), int32(size)
	return &n
}

// WithVar returns a child context binding name to val.
//
// A child scope with its own one-entry map is used rather than mutating the
// parent's, because a for-expression binds a fresh value per iteration while
// the body may capture it; mutation would make all iterations observe the last
// value.
func (c *Context) WithVar(name xdm.QName, val xdm.Sequence) *Context {
	// The scope and its binding in one allocation.
	s := &struct {
		n Context
		b varBinding
	}{n: *c}
	n := &s.n
	n.Vars = nil
	if name.Local == "" { // not a variable name, but keep it resolvable
		n.Vars = map[string]xdm.Sequence{name.Clark(): val}
		n.bind = nil
	} else {
		s.b = varBinding{uri: name.URI, local: name.Local, val: val}
		n.bind = &s.b
	}
	n.Parent = c
	return n
}

// varBinding is the binding a scope made by WithVar adds. Never written once
// the scope is published. A scope made by WithLazyVars holds lazy instead,
// and binds every name in it; one made by xpathleaf.WithLocals holds locals,
// a host's stack of bindings, which is written in place.
type varBinding struct {
	uri, local string
	val        xdm.Sequence
	lazy       map[varKey]*LazyVar
	locals     *xpathleaf.Locals
}

// frozenLocals is the copy of c a closure captures: every host stack of
// local bindings on its chain replaced by a copy of what is visible now, so
// that the stack moving on does not change what the closure sees.
func frozenLocals(c *Context) *Context {
	if c == nil {
		return nil
	}
	if b := c.bind; b != nil && b.locals != nil {
		n := *c
		n.bind = &varBinding{locals: b.locals.Frozen()}
		return &n
	}
	p := frozenLocals(c.Parent)
	if p == c.Parent {
		return c
	}
	n := *c
	n.Parent = p
	return &n
}

// varKey is a variable's expanded name as a map key that needs no Clark
// string built for a lookup.
type varKey struct{ uri, local string }

// LazyVar is a variable whose value is computed when a reference first needs
// it. XSLT's global variables are the case: a stylesheet may declare hundreds
// and read a few, and evaluating each only on first use is what section 2.14
// permits ("an implementation will signal the error only if it actually
// executes the instructions and expressions").
//
// Force computes the value. A successful result is kept and Force is not
// called again; a failure is not kept, so the host decides what a second
// reference sees. While Force runs the variable is not in scope for the
// goroutine running it: a reference reached from inside its own evaluation
// resolves as if the name were not bound here, which is how a host sees a
// circularity. Unbind takes the variable out of scope for good.
//
// A LazyVar is safe for concurrent use once its scope's EvalLock has been
// shared. A function item can outlive the evaluation that made it and be
// called from several goroutines, each reaching a variable nothing has
// evaluated yet. The variables bound by one WithLazyVars call share one
// lock, held while any of them is forced, so each is evaluated once and the
// host's Force never runs concurrently with another of the same scope; one
// lock per variable would let two goroutines forcing two variables that read
// each other wait on each other forever.
//
// Until the lock is shared, the scope is used by the one goroutine
// evaluating with it and no lock is taken: finding the goroutine costs a
// stack trace, and taking it on every first force cost DocBook 44% CPU. A
// forced value is read with one atomic load either way.
type LazyVar struct {
	Force func() (xdm.Sequence, error)
	val   xdm.Sequence
	state atomic.Uint32
	group *EvalLock
}

const (
	lazyPending uint32 = iota
	lazyRunning
	lazyDone
	lazyUnbound
)

// EvalLock serializes evaluation of one host's state once a value that can
// reach it, such as a function item, has left the goroutine that made it:
// when the evaluation ends (Share), or earlier, when it calls host code that
// may hand such a value to goroutines of its own (hostCall). From that call
// on, the goroutine evaluating holds the lock for its own evaluation too,
// letting it go only while it is in host code, until Share.
// The lazy variables of a WithLazyVars scope take it while one is forced,
// and a function item whose body runs under a context carrying a host
// runtime that implements EvalLocker takes it for the call. One lock for
// both, since forcing a variable can build the host's state and building
// the state can read a variable: two locks would let two goroutines each
// hold one and wait for the other. It is re-entrant for the goroutine
// holding it, since one function calls another and forcing one variable
// usually reads others.
//
// Until it is shared it is never taken and costs one atomic load.
type EvalLock struct {
	shared atomic.Bool // set by Share or the first hostCall
	mu     sync.Mutex
	owner  atomic.Int64 // goroutine holding mu, or 0
	// evaluating is set while the goroutine evaluating with the state holds
	// mu for that evaluation, between a host call's return and the next host
	// call or Share. Read and written under mu.
	evaluating bool
}

// EvalLocker is implemented by a host's xpathleaf.Host.Runtime whose state
// an escaped function item must not reach from two goroutines at once.
type EvalLocker interface{ EvalLock() *EvalLock }

// Share marks the state g guards as reachable from other goroutines: from
// then on Lock takes it. A host calls it on the goroutine evaluating with the
// state when that evaluation ends and a value that can reach the state may
// outlive it. If a host call shared g earlier, that goroutine has held g
// since, and Share lets it go.
func (g *EvalLock) Share() {
	if !g.shared.Load() {
		g.shared.Store(true)
		return
	}
	if g.owner.Load() == goroutineID() && g.evaluating {
		g.evaluating = false
		g.owner.Store(0)
		g.mu.Unlock()
	}
}

// yield lets other goroutines take g while the calling goroutine runs host
// code, and returns what takes it back; locals, the host's stack of local
// bindings, is hidden meanwhile from whoever holds g (see
// xpathleaf.Locals.Suspend). The first call shares g: until then only the
// goroutine evaluating has used the state, and it holds g from the host's
// return on, as any other goroutine does for a call, so that what the host
// started cannot run beside it. A goroutine not holding g has nothing to let
// go.
func (g *EvalLock) yield(locals *xpathleaf.Locals) func() {
	if !g.shared.Load() {
		resume := locals.Suspend()
		g.shared.Store(true)
		return func() {
			g.mu.Lock()
			g.owner.Store(goroutineID())
			g.evaluating = true
			resume()
		}
	}
	id := goroutineID()
	if g.owner.Load() != id {
		return func() {}
	}
	evaluating := g.evaluating
	resume := func() {}
	if evaluating {
		resume = locals.Suspend()
	}
	g.evaluating = false
	g.owner.Store(0)
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		g.owner.Store(id)
		g.evaluating = evaluating
		resume()
	}
}

// Lock takes g for the calling goroutine and returns the release, which does
// nothing when the goroutine already held it or g is not shared.
func (g *EvalLock) Lock() func() {
	if !g.shared.Load() {
		return func() {}
	}
	id := goroutineID()
	if g.owner.Load() == id {
		return func() {}
	}
	g.mu.Lock()
	g.owner.Store(id)
	return func() {
		g.owner.Store(0)
		g.mu.Unlock()
	}
}

// goroutineID is the calling goroutine's number, read from the first line
// of its stack trace ("goroutine 18 [running]:"). Go offers no other way to
// tell a re-entrant call from another goroutine's. Only a Lock after Share
// pays for it.
//
// ponytail: parses runtime.Stack; a context-carried lock token would avoid
// it, but every evaluation path would have to carry the token.
func goroutineID() int64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = b[len("goroutine "):]
	var id int64
	for _, c := range b {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + int64(c-'0')
	}
	return id
}

// ReadyVar returns a LazyVar that already holds val.
func ReadyVar(val xdm.Sequence) *LazyVar {
	l := &LazyVar{val: val}
	l.state.Store(lazyDone)
	return l
}

// Value returns the variable's value, computing it if no reference has yet.
// A variable out of scope (see LazyVar) reports no value and no error.
func (l *LazyVar) Value() (xdm.Sequence, error) {
	v, _, err := l.get()
	return v, err
}

// Unbind takes the variable out of scope: a reference resolves as if the
// name were not bound by this scope.
func (l *LazyVar) Unbind() {
	if l.group != nil {
		defer l.group.Lock()()
	}
	l.state.Store(lazyUnbound)
}

func (l *LazyVar) get() (xdm.Sequence, bool, error) {
	if l.state.Load() == lazyDone {
		return l.val, true, nil
	}
	if l.group != nil {
		defer l.group.Lock()()
	}
	switch l.state.Load() {
	case lazyDone:
		return l.val, true, nil
	case lazyRunning, lazyUnbound:
		return nil, false, nil
	}
	l.state.Store(lazyRunning)
	v, err := l.Force()
	if err != nil {
		l.state.Store(lazyPending)
		return nil, true, err
	}
	l.val = v
	l.state.Store(lazyDone)
	return v, true, nil
}

// WithLazyVars returns a child scope binding every name in vars, each one
// evaluated on first lookup. One map scope rather than a WithVar per name
// keeps a lookup that passes through it to one map probe. The variables
// share lock, or a new one when it is nil (see LazyVar); binding them again
// moves them to that one, so it must not happen while one is being forced.
func (c *Context) WithLazyVars(vars map[xdm.QName]*LazyVar, lock *EvalLock) *Context {
	g := lock
	if g == nil {
		g = &EvalLock{}
	}
	m := make(map[varKey]*LazyVar, len(vars))
	for k, v := range vars {
		v.group = g
		m[varKey{k.URI, k.Local}] = v
	}
	n := *c
	n.Vars = nil
	n.bind = &varBinding{lazy: m}
	n.Parent = c
	return &n
}

// QualifyVar, when set, is consulted before a variable reference is resolved
// by name, and answers the name the reference should actually bind to.
//
// It exists for a host language whose variable scopes are not flat. XSLT 3.0
// packages are the case: two packages may each declare a global of the same
// name, and both bindings are live at once, so the name alone does not
// identify the variable. The host attaches the package to the expression via
// WithStaticHost and reads it back here. Returning the name unchanged, or
// leaving the field nil, is the ordinary flat behaviour.
type VarQualifier func(ctx *Context, name xdm.QName) xdm.QName

// LookupVar resolves a variable by expanded name, walking enclosing scopes.
// A variable bound by WithLazyVars whose evaluation fails reports no value;
// a reference in an expression reports the failure itself.
func (c *Context) LookupVar(name xdm.QName) (xdm.Sequence, bool) {
	v, ok, err := c.lookupVar(name)
	return v, ok && err == nil
}

// lookupVar is LookupVar with the failure of a lazily bound variable's
// evaluation, which is what a VarRef raises.
func (c *Context) lookupVar(name xdm.QName) (xdm.Sequence, bool, error) {
	if c.ev().QualifyVar != nil {
		if q := c.ev().QualifyVar(c, name); q != name {
			if v, ok, err := c.lookupVarPlain(q); ok {
				return v, true, err
			}
		}
	}
	return c.lookupVarPlain(name)
}

// lookupVarPlain is lookupVar without the host's qualifier, and is what the
// qualifier's own answer is resolved through.
func (c *Context) lookupVarPlain(name xdm.QName) (xdm.Sequence, bool, error) {
	key, keyed := "", false
	for s := c; s != nil; s = s.Parent {
		if b := s.bind; b != nil {
			if b.locals != nil {
				if v, ok := b.locals.Lookup(name.URI, name.Local); ok {
					return v, true, nil
				}
			} else if b.lazy != nil {
				if l, ok := b.lazy[varKey{name.URI, name.Local}]; ok {
					if v, ok, err := l.get(); ok || err != nil {
						return v, true, err
					}
				}
			} else if b.local == name.Local && b.uri == name.URI {
				return b.val, true, nil
			}
		}
		if len(s.Vars) == 0 {
			continue
		}
		if !keyed {
			key, keyed = name.Clark(), true
		}
		if v, ok := s.Vars[key]; ok {
			return v, true, nil
		}
	}
	return nil, false, nil
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
	st := *ctx.st()
	st.collation = c
	out := *ctx
	out.static = &st
	return &out
}

// Err reports cancellation, checked at loop boundaries during evaluation.
func (c *Context) Err() error {
	if x := c.ev().Ctx; x != nil {
		return x.Err()
	}
	return nil
}

// Descend returns a copy with the recursion depth incremented, erroring past
// the limit.
func (c *Context) Descend() (*Context, error) {
	if err := c.checkDepth(); err != nil {
		return nil, err
	}
	n := *c
	n.Depth++
	return &n, nil
}

// checkDepth is Descend's limit test, shared with the in-place count
// FuncCall.Eval uses for a leaf builtin.
func (c *Context) checkDepth() error {
	if lim := c.depthLimit(); int(c.Depth) >= lim {
		// XPDY0001 is kept because callers and the conformance suites read
		// it, but it properly means "no context item is defined" and this
		// is nothing of the sort: the expression is well-formed and has a
		// context, it is merely deeper than this processor will evaluate.
		// The sentinel is added alongside so a caller can tell a refusal
		// from a fault. See xdm.ErrResourceLimit.
		return fmt.Errorf("XPDY0001: recursion exceeded %d levels: %w",
			lim, xdm.ErrResourceLimit)
	}
	return nil
}

// countItems charges n items against the evaluation budget.
//
// Accumulating constructs call it as they build, so a runaway is stopped while
// it is running rather than after it has already allocated. A Context with no
// budget — one assembled by hand rather than through NewContext — is
// unbounded, which keeps the type usable as a plain value.
func (c *Context) countItems(n int) error {
	if c == nil || c.ev().items == nil || n <= 0 {
		return nil
	}
	if lim := c.itemLimit(); atomic.AddInt64(c.ev().items, int64(n)) > lim {
		// The code is kept -- the suites and callers read it -- and the
		// sentinel added, because this is the processor declining to
		// allocate rather than anything wrong with the expression.
		return fmt.Errorf(
			"XPDY0130: evaluation materialised more than %d items; "+
				"the expression is building a sequence too large to hold: %w",
			lim, xdm.ErrResourceLimit)
	}
	return nil
}

// itemLimit is the item bound in force: Env.MaxItems where the caller set
// a positive one, none where it set a negative one, MaxItems otherwise.
func (c *Context) itemLimit() int64 {
	switch {
	case c == nil || c.ev().MaxItems == 0:
		return MaxItems
	case c.ev().MaxItems < 0:
		return math.MaxInt64
	}
	return int64(c.ev().MaxItems)
}

// ChargeItems charges n items against the evaluation budget, reporting
// XPDY0130 when the budget is exhausted.
//
// It is exported for a host language that accumulates sequences in its own
// evaluator rather than through this package's Expr tree. XQuery's FLWOR is
// the case: §3.10 defines it over a materialised tuple stream, which
// xquery.flwor builds itself, so none of the accumulation reaches the
// constructs in this package that charge as they grow. Such a host must also
// hold the budget across the whole of its evaluation — see HoldItemBudget —
// or the reset in Compiled.Eval clears the counter under it once per
// iteration.
func (c *Context) ChargeItems(n int) error { return c.countItems(n) }

// makeSequence reserves room for n items in the evaluation budget and returns
// an empty sequence with that capacity.
//
// It is the counterpart of stringResult for the item budget, and it exists for
// the same reason: a built-in that materialises a sequence reaches no
// enclosing evaluator when a host language calls it directly, so a charge
// taken only at LetExpr, evalFor or the range operator is one a host can walk
// past. Reserving here makes the invariant local to the allocation.
//
// The reservation is taken BEFORE the make, so a request too large to allow is
// refused without ever allocating the backing array -- which is the point when
// n comes from the input, as it does for a codepoint sequence.
//
// One ownership rule per result: a caller that reserves with makeSequence must
// NOT also charge each append, or the sequence is paid for twice and a legal
// result is refused at half the documented limit.
func makeSequence(ctx *Context, n int) (xdm.Sequence, error) {
	if err := ctx.countItems(n); err != nil {
		return nil, err
	}
	return make(xdm.Sequence, 0, n), nil
}

// HoldItemBudget returns a copy of c on which Compiled.Eval will not reset the
// item budget, and arms a fresh budget for the evaluation about to begin.
//
// Compiled.Eval resets per expression because that is the right boundary for
// XPath and for XSLT, where the host evaluates one expression per node and a
// budget carried across all of them would refuse a legitimate transform. A
// host whose own evaluator loops over expressions has the opposite problem: a
// FLWOR calls Compiled.Eval once per tuple, so the per-expression reset clears
// the counter two million times and the budget never binds. Holding it moves
// the boundary out to the host's evaluation, which is where "how large may one
// evaluation's intermediate sequences grow" is actually asked.
//
// The flag rides on the value copy the scope-changing methods make — Descend,
// WithVar, WithFocus — so every nested evaluation inherits the hold, while the
// caller's own Context keeps the per-expression boundary it had. The counter
// itself is shared through the same pointer, so the hold measures the whole
// tree of nested evaluations against one allowance.
// A context that already holds the budget is returned unchanged rather than
// re-armed. Nothing calls it that way today, but an inner evaluation that
// reset the counter would clear the outer one's charges and hand the outer
// evaluation an allowance it has already spent — the leak this whole change
// exists to avoid, arriving from the other side.
func (c *Context) HoldItemBudget() *Context {
	if c == nil || c.heldItems {
		return c
	}
	n := *c
	n.heldItems = true
	n.resetItems()
	return &n
}

// resetItems starts a fresh item budget for one expression evaluation.
func (c *Context) resetItems() {
	if c != nil && c.ev().items != nil {
		atomic.StoreInt64(c.ev().items, 0)
	}
}

// countBytes charges n bytes of built string content against the evaluation
// budget.
//
// The constructs that concatenate call it as they build, so a runaway is
// stopped while it is running rather than after it has already allocated. A
// Context with no budget -- one assembled by hand rather than through
// NewContext -- is unbounded, which keeps the type usable as a plain value.
func (c *Context) countBytes(n int) error {
	if c == nil || c.ev().bytes == nil || n <= 0 {
		return nil
	}
	if atomic.AddInt64(c.ev().bytes, int64(n)) > MaxBytes {
		// XPDY0130 is this engine's own code for "the evaluation asked for
		// more than I will allocate", and the wording says bytes rather than
		// items so the two refusals are not confused. The suite sanctions it
		// for exactly this shape: fn/codepoints-to-string.xml's overflow case
		// builds an impossibly long string and accepts XPDY0130 for it. The
		// sentinel is added alongside so a caller can tell a refusal from a
		// fault. See xdm.ErrResourceLimit.
		return fmt.Errorf(
			"XPDY0130: evaluation built more than %d bytes of string "+
				"content; the expression is building a string too large to "+
				"hold: %w", MaxBytes, xdm.ErrResourceLimit)
	}
	return nil
}

// refundBytes returns n bytes of an over-reservation to the evaluation budget.
//
// It exists for a caller that charges a BLOCK it may not spend in full --
// serializeSink is the one, drawing 64 KiB at a time to keep the atomic off
// the per-write path -- and it is the mechanism that keeps that optimisation
// from turning the bound into an approximation: the block is charged when it
// is drawn and the unspent remainder is handed back, so the evaluation is
// charged for the bytes it actually built.
//
// It is never a way to un-charge bytes that were really written. The only
// caller passes a reserve it drew itself and has not spent, which is why this
// is unexported and takes no decision about what a refund means.
func (c *Context) refundBytes(n int) {
	if c == nil || c.ev().bytes == nil || n <= 0 {
		return
	}
	atomic.AddInt64(c.ev().bytes, -int64(n))
}

// ChargeBytes charges n bytes of built string content against the evaluation
// budget, reporting XPDY0130 when the budget is exhausted.
//
// It is exported for a host language that concatenates in its own evaluator
// rather than through this package's Expr tree. XSLT is the case: xsl:value-of
// joins its selected sequence with xslt's own code, so the text it appends
// reaches none of the functions here that charge as they grow.
func (c *Context) ChargeBytes(n int) error { return c.countBytes(n) }

// countNodes charges n constructed result-tree nodes against the evaluation
// budget.
//
// The builder calls it as it constructs, so a runaway is stopped while it is
// running rather than after it has already allocated -- which is the whole
// point here, the unbounded case having allocated 44 GB before it returned. A
// Context with no budget -- one assembled by hand rather than through
// NewContext -- is unbounded, which keeps the type usable as a plain value.
func (c *Context) countNodes(n int) error {
	if c == nil || c.ev().nodes == nil || n <= 0 {
		return nil
	}
	if atomic.AddInt64(c.ev().nodes, int64(n)) > MaxNodes {
		// XPDY0130 again, and the wording says nodes so that the three
		// refusals are not confused with one another. XPath 3.1 §2.3.1
		// sanctions exactly this: "limitations may exist on the maximum
		// numbers or sizes of various objects... An error must be raised if
		// such a limitation is exceeded [err:XPDY0130]". The sentinel is
		// added alongside so a caller can tell a refusal from a fault. See
		// xdm.ErrResourceLimit.
		return fmt.Errorf(
			"XPDY0130: evaluation constructed more than %d result-tree "+
				"nodes; the transformation is building a tree too large to "+
				"hold: %w", MaxNodes, xdm.ErrResourceLimit)
	}
	return nil
}

// ChargeNodes charges n constructed result-tree nodes against the evaluation
// budget, reporting XPDY0130 when the budget is exhausted.
//
// It is exported because the construction happens in xdmbuild, which names
// neither host language and imports neither this package nor any other beyond
// xdm. A host passes the charge in through xdmbuild.Policy instead, which is
// already the seam for everything the builder cannot know by itself.
func (c *Context) ChargeNodes(n int) error { return c.countNodes(n) }

// stringResult returns one xs:string after charging the bytes it took to build
// it, and is how a built-in that materialises a NEW string returns it.
//
// The charge belongs here rather than at an enclosing evaluator boundary
// because a host language does not pass through one. LetExpr, evalFor and the
// range operator each charge what they bind, which covers an expression
// written in XPath; a caller that resolves a built-in through the function
// library and invokes it directly -- which xslt and xquery do, and which any
// embedder may do -- reaches none of them, so an uncharged built-in hands that
// caller the whole allowance over again. Charging at the allocation makes the
// invariant local: whoever built the bytes paid for them.
//
// It charges the finished string, which is one allocation late. That is the
// right trade for a result whose size is bounded by its INPUT -- a case
// mapping grows by a small constant factor at worst, and the input was itself
// charged when it was built -- so the excess is bounded and the next call is
// refused. It is the wrong trade where the output has no such bound, which is
// what serializeSink exists for: see xpath/fn_serialize.go.
func stringResult(ctx *Context, s string) (xdm.Sequence, error) {
	if err := ctx.countBytes(len(s)); err != nil {
		return nil, err
	}
	return strSeq(s), nil
}

// HoldByteBudget returns a copy of c on which Compiled.Eval will not reset the
// byte budget, and arms a fresh budget for the construction about to begin.
//
// Compiled.Eval resets per expression because that is the right boundary for a
// bare XPath expression. A host that builds one result out of many expressions
// has the opposite problem: a query's "let" chain and a template's run of
// xsl:variable declarations each reach xpath once per binding, so the
// per-expression reset clears the counter between the doublings and a chain
// that doubles its result per line is never charged for the doubling. Holding
// it moves the boundary out to one query evaluation or one transform, which is
// where "how much string content must fit in memory at once" is actually
// asked.
//
// The flag rides on the value copy the scope-changing methods make, so every
// nested evaluation inherits the hold while the caller's own Context keeps the
// per-expression boundary it had. A context that already holds the budget is
// returned unchanged rather than re-armed: an inner construction that reset
// the counter would clear the outer one's charges and hand it an allowance it
// has already spent, which is the leak this exists to avoid arriving from the
// other side.
func (c *Context) HoldByteBudget() *Context {
	if c == nil || c.heldBytes {
		return c
	}
	n := *c
	n.heldBytes = true
	n.resetBytes()
	return &n
}

// AdoptBudget returns a copy of c spending src's item and byte allowances
// instead of its own, so that a nested evaluation continues its caller's
// budget rather than being granted a fresh one.
//
// It exists for a host language that starts a whole new evaluation inside an
// existing one and cannot simply pass the caller's Context down. XSLT's
// fn:transform is the case: the nested transformation builds its own runtime,
// and newRuntime calls NewContext, which mints both counters from scratch --
// so every level of a nest got the full MaxItems and MaxBytes over again while
// the depth budget correctly inherited. The house rule the depth budget
// already follows is that a budget is monotonic: a nested evaluation may spend
// the parent's remaining allowance, never reset it.
//
// Each counter is carried WITH its held flag, never without. The flag is what
// says where the budget's boundary is, and a counter forwarded without it
// would be reset by Compiled.Eval once per expression in the nested
// evaluation -- which would clear the CALLER's accumulated charges through the
// shared pointer and hand the caller an allowance it has already spent. That
// is the same leak HoldItemBudget's and HoldByteBudget's idempotence guards
// exist to prevent, arriving by another route, and it would be worse than the
// fresh allowance it replaced.
//
// A nil src, or a src with no budget, leaves c's own budget alone: a caller
// with nothing to inherit from is a fresh root, which is what a top-level
// evaluation legitimately is.
func (c *Context) AdoptBudget(src *Context) *Context {
	if c == nil || src == nil {
		return c
	}
	from := src.ev()
	if from.items == nil && from.bytes == nil && from.entities == nil &&
		from.nodes == nil {
		return c
	}
	n := *c
	env := *c.ev()
	if from.items != nil {
		// The bound travels with the counter it is checked against.
		env.items, n.heldItems, env.MaxItems = from.items, src.heldItems, from.MaxItems
	}
	if from.bytes != nil {
		env.bytes, n.heldBytes = from.bytes, src.heldBytes
	}
	// The entity allowance is inherited on the same house rule as the other
	// two: a nested evaluation may spend the parent's remainder, never reset
	// it. Without this a nested transform would hand fn:parse-xml the full
	// ceiling over again, which is the per-call mint this change removes
	// arriving one level up.
	if from.entities != nil {
		env.entities = from.entities
	}
	// The result-tree allowance inherits on the same house rule, and for the
	// sharper reason: fn:transform's nested transformation builds a whole
	// result tree of its own, so a nested transform granted a fresh ceiling
	// could build MaxNodes again at every level of the nest.
	if from.nodes != nil {
		env.nodes = from.nodes
	}
	n.env = &env
	return &n
}

// EntityBudget returns the entity-expansion allowance shared by every parse
// this evaluation performs, for a host that parses on the evaluation's behalf
// rather than through fn:parse-xml.
//
// A nil result means this Context carries no allowance -- a hand-built one --
// and the parse gets the ordinary per-document ceiling.
func (c *Context) EntityBudget() *xdm.EntityBudget {
	return c.ev().entities
}

// resetBytes starts a fresh byte budget for one expression evaluation.
func (c *Context) resetBytes() {
	if b := c.ev().bytes; b != nil {
		atomic.StoreInt64(b, 0)
	}
}

// regexVersion is the version of the regular expression dialect in force.
//
// The larger of Version and RegexVersion, so that raising one never lowers
// the other and the zero value of RegexVersion means "whatever Version says".
func (c *Context) regexVersion() Version {
	if c == nil {
		return XPath20
	}
	r, v := c.ev().RegexVersion, c.Version()
	if r > v {
		return r
	}
	return v
}

// libraryVersion is the version the function library is visible at.
//
// The larger of Version and LibraryVersion, on the same reasoning as
// regexVersion.
func (c *Context) libraryVersion() Version {
	if c == nil {
		return XPath20
	}
	l, v := c.ev().LibraryVersion, c.Version()
	if l > v {
		return l
	}
	return v
}

// EnvironmentResolver answers fn:environment-variable and
// fn:available-environment-variables. Nil disables both, which is the default
// and the safe one: the process environment routinely holds credentials, and
// nothing about running a stylesheet implies consent to read them.
//
// It is an interface rather than a bool for the same reason the other resource
// gates are. A caller who wants these functions to work usually wants a
// *chosen* set of variables visible, not the whole process environment — the
// grant is which names, not merely on or off. OSEnvironment is the widest
// implementation and has to be asked for by name.
//
// Both methods are answerable as the empty result, and that is what makes the
// gate conformance-safe: F&O 3.1 section 14.6.9 makes it
// implementation-dependent which variables are available, and section 14.6.8
// returns the empty sequence for a name that is not among them. So a withheld
// variable and an unset one are indistinguishable by design, and a stylesheet
// cannot tell the gate from a bare environment.
type EnvironmentResolver interface {
	// LookupEnvironment returns the value of name and whether it is
	// available. A resolver that hides a variable returns ok false, which is
	// the same answer an unset variable gives.
	LookupEnvironment(name string) (value string, ok bool)

	// EnvironmentNames returns the names LookupEnvironment will answer, in
	// any order. An empty result is legal and means no variable is exposed.
	EnvironmentNames() []string
}

// OSEnvironment is an EnvironmentResolver over the real process environment,
// exposing every variable the process holds.
//
// It is the widest grant this library offers and is never installed by
// default: a caller who sets it is saying that whatever runs in this context
// is trusted with the process's own secrets. Prefer a resolver over a fixed
// map of the variables a stylesheet actually needs.
type OSEnvironment struct{}

// LookupEnvironment reads the process environment.
func (OSEnvironment) LookupEnvironment(name string) (string, bool) {
	return os.LookupEnv(name)
}

// EnvironmentNames returns every variable name in the process environment,
// sorted. The order is fixed only so that two calls in one query agree; the
// spec fixes none, and an unstable one would make a test comparing two calls
// flap.
func (OSEnvironment) EnvironmentNames() []string {
	env := os.Environ()
	names := make([]string, 0, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			names = append(names, kv[:i])
		}
	}
	sort.Strings(names)
	return names
}
