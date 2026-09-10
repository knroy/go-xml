package xpath

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
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

	// Version is the language version the expression was compiled under.
	//
	// It reaches the function library because a few functions differ between
	// versions in ways the parser cannot settle: fn:matches and its siblings
	// accept the "q" flag and the 3.0 regular expression constructs only
	// under 3.0, and must raise the same errors as any other processor when
	// asked to be 2.0. The zero value is XPath20, so a Context built by an
	// existing caller behaves exactly as it did before.
	Version Version

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

	// StaticHost is an opaque value the host language attached to the
	// expression being evaluated; see Compiled.WithStaticHost. This package
	// never interprets it, only carries it.
	StaticHost any

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
	StaticNamespaces NamespaceResolver

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

	// Validator validates a tree fn:json-to-xml has just built, when the
	// call asked for validate=true. Nil means the processor cannot do it,
	// which is FOJS0004 rather than a silent untyped result; see
	// TreeValidator.
	Validator TreeValidator

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
	Compat bool

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

	// Depth guards against unbounded recursion in user-defined functions and
	// named templates, which the spec does not bound.
	Depth int

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

	// heldItems suppresses Compiled.Eval's per-expression reset of items,
	// because a host language is measuring a larger evaluation against the
	// same counter. Set by HoldItemBudget, and copied along with the rest of
	// the Context by every scope change, which is what carries the hold into
	// the nested evaluations it has to cover.
	heldItems bool

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

	// heldBytes suppresses Compiled.Eval's per-expression reset of bytes,
	// because a host language is measuring a larger evaluation against the
	// same counter. Set by HoldByteBudget; the same mechanism as heldItems,
	// and separate from it because the two budgets have different natural
	// boundaries -- a FLWOR for items, one constructed value for bytes.
	heldBytes bool

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

// MaxDepth bounds recursive evaluation by default.
//
// It is a denial-of-service guard for a caller evaluating an expression it did
// not write, not a conformance limit: nothing in the specification caps
// recursion, and a query is entitled to recurse as deeply as it likes. A
// caller that trusts its input can raise the bound through Context.MaxDepth,
// which is what the conformance harnesses do — xslt.TransformOptions has
// carried the same escape hatch for template recursion all along.
const MaxDepth = 500

// depthLimit is the bound in force, which is Context.MaxDepth where the caller
// set one and MaxDepth otherwise.
func (c *Context) depthLimit() int {
	if c.MaxDepth > 0 {
		return c.MaxDepth
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
	if cr, ok := ctx.Docs.(ContextDocumentResolver); ok {
		return cr.ResolveDocumentIn(ctx, uri, base)
	}
	return ctx.Docs.ResolveDocument(uri, base)
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
	if cr, ok := ctx.Collections.(ContextCollectionResolver); ok {
		return cr.ResolveCollectionIn(ctx, uri, base)
	}
	return ctx.Collections.ResolveCollection(uri, base)
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
	// nil for a function whose signature has not been recorded, which is most
	// of the library: such a function is matched on arity alone, the answer
	// every function item gave before signatures existed. Annotating one is
	// therefore a narrowing, and only ever makes a test that wrongly answered
	// true answer false.
	Signature []string
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
		bytes:    new(int64),
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
func (c *Context) LookupVar(name xdm.QName) (xdm.Sequence, bool) {
	if c.QualifyVar != nil {
		if q := c.QualifyVar(c, name); q != name {
			if v, ok := c.lookupVarPlain(q); ok {
				return v, true
			}
		}
	}
	return c.lookupVarPlain(name)
}

// lookupVarPlain is LookupVar without the host's qualifier, and is what the
// qualifier's own answer is resolved through.
func (c *Context) lookupVarPlain(name xdm.QName) (xdm.Sequence, bool) {
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
	if lim := c.depthLimit(); c.Depth >= lim {
		// XPDY0001 is kept because callers and the conformance suites read
		// it, but it properly means "no context item is defined" and this
		// is nothing of the sort: the expression is well-formed and has a
		// context, it is merely deeper than this processor will evaluate.
		// The sentinel is added alongside so a caller can tell a refusal
		// from a fault. See xdm.ErrResourceLimit.
		return nil, fmt.Errorf("XPDY0001: recursion exceeded %d levels: %w",
			lim, xdm.ErrResourceLimit)
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
		// The code is kept -- the suites and callers read it -- and the
		// sentinel added, because this is the processor declining to
		// allocate rather than anything wrong with the expression.
		return fmt.Errorf(
			"XPDY0130: evaluation materialised more than %d items; "+
				"the expression is building a sequence too large to hold: %w",
			MaxItems, xdm.ErrResourceLimit)
	}
	return nil
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
	if c != nil && c.items != nil {
		atomic.StoreInt64(c.items, 0)
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
	if c == nil || c.bytes == nil || n <= 0 {
		return nil
	}
	if atomic.AddInt64(c.bytes, int64(n)) > MaxBytes {
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

// ChargeBytes charges n bytes of built string content against the evaluation
// budget, reporting XPDY0130 when the budget is exhausted.
//
// It is exported for a host language that concatenates in its own evaluator
// rather than through this package's Expr tree. XSLT is the case: xsl:value-of
// joins its selected sequence with xslt's own code, so the text it appends
// reaches none of the functions here that charge as they grow.
func (c *Context) ChargeBytes(n int) error { return c.countBytes(n) }

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

// resetBytes starts a fresh byte budget for one expression evaluation.
func (c *Context) resetBytes() {
	if c != nil && c.bytes != nil {
		atomic.StoreInt64(c.bytes, 0)
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
	if c.RegexVersion > c.Version {
		return c.RegexVersion
	}
	return c.Version
}

// libraryVersion is the version the function library is visible at.
//
// The larger of Version and LibraryVersion, on the same reasoning as
// regexVersion.
func (c *Context) libraryVersion() Version {
	if c == nil {
		return XPath20
	}
	if c.LibraryVersion > c.Version {
		return c.LibraryVersion
	}
	return c.Version
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
// gate conformance-safe: F&O 3.1 section 16.2.1 makes it
// implementation-dependent which variables are available, and section 16.2.2
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
