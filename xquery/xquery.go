package xquery

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
	"github.com/knroy/go-xml/xpath"
)

// Options configure how a query is compiled.
//
// The zero value is the specification's defaults: boundary whitespace is
// stripped, construction preserves types, an unprefixed element name is in no
// namespace, and an unprefixed function name is in the fn: namespace.
type Options struct {
	// BaseURI is the static base URI. It is stamped on constructed elements
	// and is what a relative reference resolves against.
	BaseURI string

	// DeclarationBaseURI is the URI of the resource the query text was read
	// from. It is used for one thing only: resolving a relative
	// "declare base-uri" against it, as §4.5 requires.
	//
	// It is deliberately separate from BaseURI. The two answer different
	// questions -- "what does a prolog declaration resolve against" and "what
	// does the query run under" -- and conflating them is what made every
	// earlier attempt at K2-BaseURIProlog-4 a net loss: one value for both
	// also stamped that value on constructed elements and on
	// fn:static-base-uri when the query declared nothing, which the suite's
	// base-URI-12/14/23/24 and K2-BaseURIFunc-30 all detect. Kept apart,
	// setting this one changes nothing except the resolution a declaration
	// asks for: a query with no "declare base-uri" is unaffected by it.
	DeclarationBaseURI string

	// BoundarySpace decides whether whitespace that only separates markup
	// survives into a constructed element. The zero value strips it, which is
	// what a query with no "declare boundary-space" gets.
	BoundarySpace BoundarySpace

	// Construction decides whether a copied node keeps its type annotation.
	// The zero value preserves it.
	Construction Construction

	// DefaultElementNamespace is applied to an unprefixed element name. It is
	// never applied to an attribute name.
	DefaultElementNamespace string

	// Namespaces are prefix bindings available to the query, as though its
	// prolog had declared them. The eight predeclared prefixes of §4.1 —
	// xml, xs, xsi, fn, local, math, map and array — are bound already, as is
	// err, which §3.16 binds so that "catch err:FODC0002" works with no
	// declaration. None of them need to appear here.
	Namespaces map[string]string

	// Modules are library modules available to "import module" (§4.12),
	// registered by target namespace with their source text.
	//
	// This is the module store the specification leaves to the
	// implementation, and it is the only way to import a module that grants
	// the query no reach whatever: the caller supplies the text, so nothing
	// is opened and nothing is fetched. An import with no "at" clause names
	// a namespace and nothing else, and resolves against this.
	//
	// The store is consulted before ModuleResolver, so a registered module
	// shadows any location a query might name for that namespace.
	Modules []Module

	// ModuleResolver locates a library module that Modules does not have.
	// When nil, NOTHING IS FETCHED: an "at" location is never opened, and an
	// import that the store cannot answer raises XQST0059.
	//
	// It is off by default for the reason xsd.Options.Resolver is — it hands
	// control of what this process reads to whoever wrote the query — and the
	// exposure here is worse than a schema's, because an "at" location is a
	// string chosen by the query's author and a query is the more commonly
	// untrusted input of the two. A host that sets this is granting the
	// queries it compiles the reach the resolver has, and should give one
	// that is rooted or table-driven rather than one that will open anything.
	//
	// MapModuleResolver answers from memory and follows no location at all.
	ModuleResolver ModuleResolver

	// MaxModules bounds how many library modules one compilation may load,
	// counting those reached transitively. A module that imports two modules
	// that each import two more is a fan-out with no natural bound, in the
	// same shape as a schema's include graph. Zero means DefaultMaxModules.
	//
	// Exceeding it FAILS the compilation with an error wrapping
	// xdm.ErrResourceLimit. It never yields a query compiled against the
	// modules that fitted: a partial static context is how an import comes to
	// look successful while half a library is missing.
	MaxModules int

	// MaxModuleBytes bounds the total source text one compilation may read
	// through ModuleResolver and Modules, cumulatively rather than per
	// module — a budget spent one module at a time is not spent at all. Zero
	// means DefaultMaxModuleBytes.
	//
	// Exceeding it fails the compilation with an error wrapping
	// xdm.ErrResourceLimit, on the same reasoning as MaxModules: a truncated
	// module is a module whose declarations are partly missing.
	MaxModuleBytes int64

	// Schemas are schemas available to "import schema" (§4.11), registered
	// by target namespace.
	//
	// This is the schema store the specification leaves to the
	// implementation, and it is the only way to import a schema that grants
	// the query no reach whatever: the caller supplies the components or the
	// text, so nothing is opened and nothing is fetched. An import with no
	// "at" clause names a namespace and nothing else, and resolves against
	// this.
	//
	// The store is consulted before SchemaResolver, so a registered schema
	// shadows any location a query might name for that namespace.
	Schemas []Schema

	// SchemaResolver locates a schema document that Schemas does not have.
	// When nil, NOTHING IS FETCHED: an "at" location is never opened, and an
	// import that the store cannot answer raises XQST0059.
	//
	// It is off by default for the same reason ModuleResolver is: an "at"
	// location is a string chosen by the query's author, and a query is
	// untrusted input. The same resolver is handed to xsd for the imported
	// schema's own xs:include and xs:import, so an imported schema can reach
	// no further than the query's import was granted.
	SchemaResolver SchemaResolver

	// MaxSchemaBytes bounds the total schema source text one compilation may
	// read through SchemaResolver and Schemas, cumulatively rather than per
	// import. Zero means DefaultMaxSchemaBytes.
	//
	// Exceeding it fails the compilation with an error wrapping
	// xdm.ErrResourceLimit rather than XQST0059: a truncated schema is a
	// schema whose components are partly missing, and compiling against a
	// partial static context is what this package refuses to do.
	MaxSchemaBytes int64
}

// A Query is a compiled query, safe for concurrent use.
//
// Compiling separates what can be decided from the text — namespaces, the
// shape of every constructor, and every expression in it — from what depends
// on the input. Nothing about a Query changes when it runs, so one may be
// evaluated from several goroutines at once.
type Query struct {
	body []node
	sc   *staticContext
	src  string

	// vars and funcs are the prolog's variable and function declarations, in
	// source order. Both are order-independent by §4.14 and §4.15, so the
	// order here is for error reporting rather than for evaluation.
	vars  []*varDecl
	funcs []*funcDecl

	// contextItem is "declare context item", when the prolog made one.
	contextItem *contextItemDecl

	// formats are the decimal formats the prolog declared, keyed by Clark
	// name with the empty key for the default.
	formats map[string]*xpath.DecimalFormat

	// serialization is the prolog's "declare option output:*" set, keyed by
	// the serialization parameter's local name. See SerializationOptions.
	serialization map[string]string

	// lib is the function library the query's own declared functions live in,
	// built once at compile time and chained to the builtins. It is built
	// here rather than per evaluation because a Query is immutable and safe
	// for concurrent use, and building it once is what keeps it so.
	lib *xpath.Library

	// modLibs is the function library each imported module's own bodies run
	// against, keyed by that module's static context. See Query.moduleLib.
	modLibs map[*staticContext]*xpath.Library

	// modules are the library modules this query imported, transitively, in
	// initialisation order (§4.12). They are held on the Query rather than
	// merged into vars and funcs so that the two stay distinguishable: an
	// imported variable is initialised in its OWN module's static context,
	// not the importer's, and merging them would lose the context each one
	// has to be evaluated in.
	modules []*libModule
}

// Compile compiles a query.
//
// What is implemented is the prolog, the constructors, FLWOR and the
// XQuery-only expression forms: direct and computed constructors, every FLWOR
// clause including group by, order by and the two window clauses, try/catch,
// switch, typeswitch, quantified and ordered/unordered expressions, the
// extension expression and the string constructor. A query that is only an
// expression compiles too, since every XPath 3.1 expression is an XQuery
// expression.
//
// "import module" is implemented (§4.12): a library module is found in
// Options.Modules or through Options.ModuleResolver, and contributes its
// public functions and variables. Nothing is fetched unless a resolver was
// configured -- see Options.ModuleResolver.
//
// "import schema" is implemented (§4.11): a schema is found in Options.Schemas
// or through Options.SchemaResolver, and its type and declaration names reach
// the static context before the query body is parsed, so "cast as my:t",
// "instance of my:t", "element(*, my:t)", "schema-element(my:e)" and
// "validate" are all judged against it. Nothing is fetched unless a resolver
// was configured -- see Options.SchemaResolver.
func Compile(src string, opts Options) (*Query, error) {
	sc := newStaticContext()
	sc.baseURI = opts.BaseURI
	sc.declBase = opts.DeclarationBaseURI
	sc.boundarySpace = opts.BoundarySpace
	sc.construction = opts.Construction
	sc.defaultElementNS = opts.DefaultElementNamespace
	for prefix, uri := range opts.Namespaces {
		if err := sc.bind(prefix, uri); err != nil {
			return nil, err
		}
	}

	// XQuery 3.1 section 4.1 normalises the line endings of the query text
	// before anything reads it: a carriage return, alone or followed by a
	// line feed, is one line feed. The rule is the one XML applies to a
	// parsed document, applied here to the query itself, and it reaches a
	// string literal along with everything else -- the literal is part of the
	// query text, so '&#xd;&#xa;' in the source is a single line feed in the
	// value, which is what line-ending-Q002 and -Q003 assert. Doing it here
	// rather than in the lexer is what keeps every offset the parser reports
	// an offset into the same string.
	src = normalizeLineEndings(src)

	// The parser starts at the expression version its static context's
	// declared XQuery version implies -- the default until parseVersionDecl
	// says otherwise, which it does before anything else is read.
	p := &parser{src: src, sc: sc, version: sc.xqVersion.xpathVersion(),
		declaredNS: map[string]bool{}, opts: opts}
	// The version declaration, the prolog and the body are read in that order
	// because each changes how the next is read: a version declaration can
	// refuse the whole query, and every prolog declaration is applied to the
	// static context that the body's names then resolve against.
	if err := p.parseVersionDecl(); err != nil {
		return nil, err
	}
	if err := p.parseProlog(); err != nil {
		return nil, err
	}
	body, err := p.parseQueryBody()
	if err == nil && body == nil {
		// [1] Module ::= VersionDecl? (LibraryModule | MainModule) and
		// [3] MainModule ::= Prolog QueryBody: a main module whose text runs
		// out after the prolog has no query body, which is a grammar error
		// rather than an empty result. K2-Axes-97 is "declare function
		// local:foo() external;" and nothing else, and asks for XPST0003.
		//
		// The check is here and not in parseQueryBody, which is reused to
		// read the braced body of a constructor, a switch and a typeswitch --
		// and "attribute name {}" has an empty one legitimately.
		err = p.errorf("XPST0003: a main module must have a query body")
	}
	if err != nil {
		return nil, err
	}
	// The constructor prefixes are handed to the module context now that the
	// whole module has been read, so that fn:format-number's third argument
	// can name a format through a prefix a constructor bound. See
	// parser.ctorPrefixes and staticContext.resolveFormatName.
	sc.ctorPrefixes = p.ctorPrefixes
	// The imports are followed now that the whole prolog has been read. They
	// are followed here rather than in the parser because the budget is
	// per-compilation: a module that loaded its own imports could not be
	// counted against the same allowance. See moduleLoader.
	mods, err := loadModules(p.moduleImports, opts, sc)
	if err != nil {
		return nil, err
	}
	q := &Query{body: body, sc: sc, src: src, vars: p.vars, funcs: p.funcs,
		contextItem: p.contextItem, formats: p.formats,
		serialization: p.serialization, modules: mods}
	// §4.12 adds the imported declarations to this module's static context,
	// so a clash between an import and this module -- or between two imports
	// -- is the same error a duplicate declaration within one module is.
	if err := checkImportedNames(mods, q.vars, q.funcs); err != nil {
		return nil, err
	}
	q.lib = q.registerFunctions(nil)
	// Built after q.lib, which each module's own library chains onto.
	q.buildModuleLibs()
	if err := q.checkStaticCalls(); err != nil {
		return nil, err
	}
	return q, nil
}

// Eval compiles and runs a query in one step.
func Eval(src string, ctx *xpath.Context, opts Options) (xdm.Sequence, error) {
	q, err := Compile(src, opts)
	if err != nil {
		return nil, err
	}
	return q.Eval(ctx)
}

// Eval runs the query and returns its result sequence.
//
// ctx supplies the context item, the variable bindings and the function
// library, exactly as it does for an XPath expression. A nil function library
// is legal and means the query may not call anything.
func (q *Query) Eval(ctx *xpath.Context) (xdm.Sequence, error) {
	if ctx == nil {
		ctx = xpath.NewContext(nil, xpath.Builtins())
	}
	if err := q.checkBodyVars(ctx); err != nil {
		return nil, err
	}
	ctx, err := q.prepare(ctx)
	if err != nil {
		return nil, err
	}
	out := xdmbuild.New(policy{sc: q.sc})
	ref := &builderRef{b: out}
	// The item budget is armed here, for the query body, because this is the
	// boundary that matches the one xpath.Compiled.Eval draws for an
	// expression: one evaluation, however many nested expressions it runs.
	//
	// It has to be *this* package that arms it. The body is not one compiled
	// expression — a FLWOR and a constructor are parsed here and evaluated by
	// this package's own node tree, reaching xpath once per clause and once
	// per tuple — so leaving the boundary to Compiled.Eval reset the counter
	// on every iteration and the documented MaxItems was never reached on any
	// query whose body this package evaluates itself.
	//
	// After prepare rather than before, so that a global variable's
	// initialiser keeps the per-expression budget it has always had: the
	// prolog is setup, and a query with fifty globals is not one evaluation
	// that materialised the sum of them.
	ctx = ctx.HoldItemBudget()
	// The byte budget needs the same boundary for the same reason. A chain of
	// "let"s, each concatenating the previous string with itself, reaches
	// xpath once per binding, so the per-expression reset cleared the counter
	// between the doublings and the whole 640 MB was built uncharged.
	ctx = ctx.HoldByteBudget()
	ec := &evalContext{xp: ctx, sc: q.sc}
	for _, n := range q.body {
		if err := n.eval(ref, ec); err != nil {
			return nil, err
		}
	}
	return out.Sequence(), nil
}

// prepare installs the module's static context on the evaluation context:
// the declared functions, the declared global variables and the declared
// context item.
//
// It runs per evaluation rather than per compilation because the values are
// dynamic — a global's initialiser may call fn:current-dateTime, or read a
// document, or depend on an external variable the caller bound differently
// this time — while the plan that produces them is not.
//
// The function library is chained onto whatever the caller supplied rather
// than replacing it, so a host that registered extension functions keeps them
// and the query's own declarations sit in front.
func (q *Query) prepare(ctx *xpath.Context) (*xpath.Context, error) {
	sub := *ctx
	// Installed unconditionally, as xslt does: whether the processor *can*
	// validate is a property of the processor, and this one always can --
	// F&O 3.1 §17.5.3 reserves FOJS0004 for one that cannot. Whether the
	// query may then write "instance of element(j:map, j:mapType)" is the
	// separate question "import schema" answers. A caller that installed its
	// own validator keeps it.
	if sub.Validator == nil {
		sub.Validator = jsonTreeValidator{}
	}
	if len(q.funcs) > 0 || len(q.formats) > 0 || len(q.modules) > 0 {
		if ctx.Funcs == nil || ctx.Funcs == xpath.FunctionLibrary(q.lib.Parent) {
			sub.Funcs = q.lib
		} else {
			// The caller's library is not the one the query was compiled
			// against, so a fresh chain is built over theirs. This is the
			// only per-evaluation allocation the prolog costs, and only for a
			// query that declares functions and a caller that supplied a
			// library of their own.
			sub.Funcs = q.registerFunctions(ctx.Funcs)
		}
	}
	// The base URI and the default collation are stamped on each compiled
	// expression by compileExpr, because xpath models both as static
	// properties of an expression. Setting the context's base URI as well is
	// what fn:static-base-uri and fn:doc's relative resolution read.
	if q.sc.baseURI != "" {
		sub.StaticBaseURI = q.sc.baseURI
	}
	out := &sub
	// One binder serves both halves. The context item's initialiser may name
	// a global and a global's initialiser may read the context item, so the
	// two are one dependency graph rather than two phases: bindContextItem
	// initialises just the variables it names, and the rest are done after,
	// against a context that now has the item in it.
	b := q.newVarBinder(out)
	if q.contextItem != nil {
		var err error
		if out, err = q.bindContextItem(out, b); err != nil {
			return nil, err
		}
		if b != nil {
			// The variables the context item forced are already bound on the
			// binder's own context; carrying them onto the one that now holds
			// the item keeps both sets in scope for what follows.
			b.rebase(out)
		}
	}
	// §4.16 lets a library module constrain the context item's type without
	// supplying its value, and that constraint binds on the value the main
	// module ends up with: "the context item declarations in all modules must
	// be consistent", and the item must match each. So this runs whether or
	// not the main module declared one -- an importer that declares nothing
	// still owes the imported type -- and it runs after the declaration
	// above, because that is what settles which value is being checked.
	if err := q.checkImportedContextItemTypes(out); err != nil {
		return nil, err
	}
	if b == nil {
		return out, nil
	}
	for _, d := range q.allVars() {
		if err := b.visit(d); err != nil {
			return nil, err
		}
	}
	return b.ctx, nil
}

// checkImportedContextItemTypes applies the context item type declared by
// each imported library module (§4.16).
//
// A library module's declaration carries no value — XQST0113 forbids one — so
// the only thing it contributes is a type, and that type has to be satisfied
// by whatever context item the query is evaluated with. The check is a match
// rather than a conversion, on the same rule bindContextItem follows: §4.16
// says the value must *match* the declared type, and the function conversion
// rules are not applied to the context item.
//
// An absent context item is not this check's complaint. A module that
// declares a type says what the item must be IF there is one; whether there
// has to be one at all is bindContextItem's XPDY0002, and reporting an
// absence here would blame the import for a value the main module never
// supplied.
//
// contextDecl-050 and -051 are the cases. Both import a module declaring
// "context item as xs:date external" and then supply an xs:integer and an
// element respectively; both want XPTY0004. Before this the module's
// declaration was parsed and thrown away with the rest of its prolog, so the
// queries returned true and false instead of failing.
func (q *Query) checkImportedContextItemTypes(ctx *xpath.Context) error {
	if ctx == nil || ctx.Item == nil {
		return nil
	}
	for _, m := range q.modules {
		if m.contextItem == nil || m.contextItem.typ == nil {
			continue
		}
		if _, err := m.contextItem.typ.match(xdm.Sequence{ctx.Item},
			"the context item"); err != nil {
			return err
		}
	}
	return nil
}

// bindContextItem applies "declare context item" (§4.16).
//
// An external declaration takes the caller's item when there is one and falls
// back to its default when there is not. A non-external one replaces the
// caller's item outright, which is what a query that computes its own context
// is asking for. XPDY0002 is the error when neither supplies one.
func (q *Query) bindContextItem(ctx *xpath.Context, b *varBinder) (
	*xpath.Context, error) {
	d := q.contextItem
	sub := *ctx
	if d.external && ctx.Item != nil {
		// Only the type check applies: the value is the caller's. match
		// rather than convert, for the reason given below.
		if _, err := d.typ.match(xdm.Sequence{ctx.Item},
			"the context item"); err != nil {
			return nil, err
		}
		return ctx, nil
	}
	if d.init == nil && d.body == nil {
		if ctx.Item != nil {
			return ctx, nil
		}
		return nil, fmt.Errorf(
			"XPDY0002: no value was supplied for the declared context item")
	}
	// The globals the initialiser names are bound first, and only those.
	// "declare context item := $y[3]" needs $y to have a value; it does not
	// need the variables declared after it, and one of those may well be the
	// one that reads the context item — contextDecl-016 declares $x as
	// fn:position() precisely to check that it sees the item this sets. So
	// the dependency is followed exactly as far as it goes and no further.
	if b != nil {
		if err := b.bindNamed(d.references()); err != nil {
			return nil, err
		}
		sub = *b.ctx
	}
	seq, err := q.evalBody(d.body, d.init, &sub, q.sc)
	if err != nil {
		return nil, err
	}
	// match rather than convert: §4.16 says the value of the initialiser must
	// *match* the declared type, and the suite spells out that the function
	// conversion rules are not the ones meant. contextDecl-039 declares "as
	// xs:double := 1.234" and wants XPTY0004 rather than the promoted double,
	// and contextDecl-044 wants the same of an external default; both are
	// described as "function conversion rules not applied to context item".
	// This is the same distinction §4.14 draws for a variable declaration, and
	// the context item is no more converted than a variable is.
	seq, err = d.typ.match(seq, "the context item")
	if err != nil {
		return nil, err
	}
	switch len(seq) {
	case 0:
		// An initialiser that produced nothing is XPTY0004, not an absent
		// context item. The specification names no error here and leaving the
		// item absent would be the other defensible reading, but the suite
		// settles it: contextDecl-032 initialises from "(1 to 17)[20]" and
		// contextDecl-060 from "()", and both want XPTY0004. Binding nothing
		// would instead surface later as XPDY0002 from whatever read ".",
		// blaming the reference rather than the declaration that is at fault.
		return nil, fmt.Errorf(
			"XPTY0004: the context item initialiser produced an empty sequence")
	case 1:
		sub.Item = seq[0]
	default:
		return nil, fmt.Errorf(
			"XPTY0004: the context item must be a single item, not %d", len(seq))
	}
	sub.Position, sub.Size = 1, 1
	return &sub, nil
}

// String returns the query's source.
func (q *Query) String() string { return q.src }

// SerializationOptions returns the serialization parameters the prolog
// declared, keyed by the parameter's local name and carrying its lexical
// value.
//
// Evaluating a query and serialising its result are two steps, and only the
// first belongs here: Eval hands back a sequence, and what a caller does with
// it — write it as XML, as JSON, as nothing at all — is the caller's
// decision. But the *parameters* for that second step are stated in the
// query, by "declare option output:method" and its siblings (XQuery 3.1
// §2.2.4), and a caller that never sees them would have to re-parse the
// prolog to find out what the query asked for. This is how it asks instead.
//
// The values are unvalidated lexical forms. Whether "indent" says "yes" or
// something meaningless is the serialiser's judgement to make, since only it
// knows which parameters it honours; the names, however, are checked at
// compile time, an unknown one being XQST0109.
//
// The returned map is a copy, so a caller may keep or modify it without
// disturbing the Query, which is otherwise immutable and safe for concurrent
// use. Nil is returned when the prolog declared none.
func (q *Query) SerializationOptions() map[string]string {
	if len(q.serialization) == 0 {
		return nil
	}
	out := make(map[string]string, len(q.serialization))
	for k, v := range q.serialization {
		out[k] = v
	}
	return out
}

// parseQueryBody parses the body of a main module.
//
// A query body is one expression, but an expression may be a comma-separated
// sequence and may be a constructor, so this reads whichever it finds. It is
// reached with the prolog already consumed and applied.
func (p *parser) parseQueryBody() ([]node, error) {
	p.skipSpaceAndComments()
	if err := p.refuseUnimplemented(); err != nil {
		return nil, err
	}
	var out []node
	for {
		p.skipSpaceAndComments()
		if p.eof() {
			return out, nil
		}
		n, err := p.parseItem()
		if err != nil {
			return nil, err
		}
		out = append(out, n)
		p.skipSpaceAndComments()
		if !p.consume(",") {
			break
		}
	}
	p.skipSpaceAndComments()
	if !p.eof() {
		return nil, p.errorf("XPST0003: unexpected %q",
			firstToken(p.src[p.pos:]))
	}
	return out, nil
}

// parseItem parses one item of a query body: a constructor, or an expression
// handed to xpath.
func (p *parser) parseItem() (node, error) {
	// FLWOR and the quantified expressions are read here rather than handed
	// to xpath, because both admit syntax XPath's grammar does not: every
	// clause but "for" and "let", and a type declaration on a bound variable.
	// A plain "for $x in E return F" would compile either way; taking it here
	// keeps one implementation rather than two that must agree.
	p.skipSpaceAndComments()
	start := p.pos
	n, err := p.parseBareItem()
	if err != nil {
		return nil, err
	}
	// A path step or a predicate may follow the construct — "<e/>/name()",
	// "(for $x in E return F)[1]". The construct is parsed here and the step
	// compiled by xpath over its value, because the two halves need different
	// parsers and neither can read the other's.
	n, err = p.withTrailingPath(n)
	if err != nil {
		return nil, err
	}
	// An operator may follow it instead — "<a>10000</a> = 10000". That does
	// not bind tightly enough for withTrailingPath's rewrite, so the item is
	// re-read from its start with every XQuery-only primary in it lifted into
	// a variable, and xpath compiles the operators over those. See operand.go.
	if op, ok, err := p.retryAsOperandSubst(start); ok || err != nil {
		return op, err
	}
	return n, nil
}

// retryAsOperandSubst re-reads the item beginning at start as an expression
// whose XQuery-only primaries are substituted, when what follows the item is
// an operator rather than the end of it.
//
// The item is over at a top-level comma, at a closing bracket this did not
// open, and at a clause keyword — the same boundary scanExprSingleSource
// draws, and the one every caller of parseItem relies on. Anything else after
// it is an operator whose left operand is what was just parsed, so the item
// was not the whole expression and has to be read again as one.
func (p *parser) retryAsOperandSubst(start int) (node, bool, error) {
	save := p.pos
	p.skipSpaceAndComments()
	if p.eof() {
		p.pos = save
		return nil, false, nil
	}
	switch p.src[p.pos] {
	case ',', ')', ']', '}':
		p.pos = save
		return nil, false, nil
	}
	p.pos = start
	src, err := p.scanExprSingleSource()
	if err != nil || strings.TrimSpace(src) == "" {
		p.pos = save
		return nil, false, nil
	}
	end := p.pos
	sub := &parser{src: src, sc: p.sc, version: p.version, depth: p.depth}
	items, ok, err := sub.parseOperandSubst()
	if err != nil || !ok {
		p.pos = save
		return nil, false, err
	}
	p.pos = end
	return &enclosed{items: items}, true, nil
}

// parseBareItem parses one item without the path that may follow it.
func (p *parser) parseBareItem() (node, error) {
	if p.looksLikeFLWOR() {
		f, err := p.parseFLWOR()
		if err != nil {
			return nil, err
		}
		return &flworNode{f}, nil
	}
	if p.looksLikeQuantified() {
		q, err := p.parseQuantified()
		if err != nil {
			return nil, err
		}
		return &quantifiedNode{q}, nil
	}
	// The other XQuery-only forms — try/catch, switch, typeswitch, ordered
	// and unordered, the extension expression and the string constructor —
	// each begin with a keyword or a delimiter the expression parser beneath
	// would read as something else: "try" as a function call, "``[" as two
	// empty string literals and a predicate.
	if n, ok, err := p.parseXQueryOnly(); ok || err != nil {
		return n, err
	}
	switch {
	case p.lookingAt("<!--"):
		return p.parseDirComment()
	case p.lookingAt("<?"):
		return p.parseDirPI()
	case p.lookingAt("<"):
		return p.parseDirElement()
	}
	if n, ok, err := p.parseComputed(); ok || err != nil {
		return n, err
	}
	return p.parseExprItem()
}

// parseExprItem takes the run of source up to the next top-level comma and
// hands it to xpath.
//
// The comma has to be found here rather than by the expression parser because
// a query body's items are separated by one, and a constructor between two of
// them is not something xpath can read.
func (p *parser) parseExprItem() (node, error) {
	start := p.pos
	depth := 0
	for !p.eof() {
		// A string literal, a comment, a pragma or a string constructor is
		// not expression source: a comma or a bracket inside any of them is
		// an ordinary character and must not end the item.
		if end, ok, err := skipNonSyntax(p.src, p.pos); ok {
			if err != nil {
				return nil, err
			}
			p.pos = end + 1
			continue
		}
		switch p.src[p.pos] {
		case '<':
			// A direct constructor's markup is not expression source, and
			// nothing inside it can be scanned as though it were: a quote in
			// a CDATA section does not open a string literal, and "(:" in
			// element content does not open a comment. Where the "<" is at
			// operand position the constructor is parsed, which is the only
			// way to find where it ends — its extent is decided by its
			// markup, not by a bracket count — and the scan resumes after it.
			//
			// Where it is the less-than operator the byte is ordinary and
			// falls through. Where it is markup this scan cannot read, the
			// parse fails and the byte is treated as ordinary too, so the
			// error comes from the parse that follows rather than from here.
			if !startsMarkup(p.src, p.pos,
				lastSignificantOperandAware(p.src[start:p.pos])) {
				break
			}
			sub := &parser{src: p.src, pos: p.pos, sc: p.sc,
				version: p.version, depth: p.depth + 1}
			if _, err := sub.parseConstructorHere(); err != nil {
				break
			}
			p.pos = sub.pos
			continue
		case '(':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				// A closing bracket this scan never opened ends the item: it
				// belongs to the parenthesis or the call this expression is
				// an argument of.
				goto done
			}
			depth--
		case '[', '{':
			depth++
		case ',':
			if depth == 0 {
				goto done
			}
		}
		p.pos++
	}
done:
	src := strings.TrimSpace(p.src[start:p.pos])
	if src == "" {
		return nil, p.errorf("XPST0003: expected an expression")
	}
	if needsXQueryParser(src) {
		// A constructor or a FLWOR somewhere inside the expression, rather
		// than at its start: "(<a/>, <b/>)" and "count(for $x in E group by
		// $k return $x)" are both expressions xpath cannot read whole, and
		// neither begins with the construct that makes it so.
		c, err := p.parseFromSource(src)
		if err != nil {
			return nil, err
		}
		return &enclosed{items: c.items}, nil
	}
	c, err := p.compileExpr(src)
	if err != nil {
		return nil, err
	}
	return &enclosed{expr: c}, nil
}

// skipSpaceAndComments consumes whitespace and XQuery comments, which may
// appear anywhere whitespace may and which nest.
//
// It reports whether anything was consumed, which the prolog needs: "declare
// namespace" and "declarenamespace" differ only in that a separator was
// there, and a comment is a legal separator — "declare(:x:)namespace" is a
// namespace declaration.
//
// This deliberately does not go through skipNonSyntax, which steps over every
// non-syntax region. A.2.4.1 gives Whitespace ::= S | Comment, and a comment
// is the only one of the four that is ignorable: a string literal, a pragma
// and a string constructor are all expressions, so consuming one here would
// swallow a token rather than the space before it.
func (p *parser) skipSpaceAndComments() bool {
	start := p.pos
	for {
		p.skipSpace()
		if !p.lookingAt("(:") {
			return p.pos > start
		}
		end, err := skipComment(p.src, p.pos)
		if err != nil {
			// Leave it: the expression parser will report it in context.
			return p.pos > start
		}
		p.pos = end + 1
	}
}

// refuseUnimplemented reports a clear error for the parts of XQuery this
// package does not implement yet.
//
// A prolog parsed as though it were an expression produces an error naming a
// token rather than the feature, which sends the reader looking for a syntax
// mistake that is not there. Naming the feature is the more useful failure
// while the implementation is incomplete.
func (p *parser) refuseUnimplemented() error {
	if hasKeywordPrefix(p.src[p.pos:], "module namespace") {
		// A library module is not a main module: [1] Module ::= VersionDecl?
		// (LibraryModule | MainModule), and only a main module has a query
		// body. Compile is asked for a main module, so a library module here
		// is a grammar error and XPST0003 -- not XQST0059, which is about an
		// import that could not be resolved and is the wrong complaint when
		// nothing has been imported. K2-ModuleProlog-1 asks for XPST0003 by
		// name; that a resolver is also missing is a separate, later fact.
		return p.errorf(
			"XPST0003: a library module has no query body, and cannot be " +
				"evaluated as a main module")
	}
	// Nothing else is refused by name any more: FLWOR, the quantified
	// expressions, the prolog and the XQuery-only expression forms are all
	// implemented, so a query using one gets a real error from the construct
	// that is actually wrong rather than a blanket refusal.
	return nil
}

// hasKeywordPrefix reports whether s begins with a keyword, not merely with
// its letters: "letter" does not begin with "let".
func hasKeywordPrefix(s, kw string) bool {
	if !strings.HasPrefix(s, kw) {
		return false
	}
	if strings.HasSuffix(kw, " ") {
		return true
	}
	rest := s[len(kw):]
	return rest == "" || !isNameByte(rest[0])
}

func isNameByte(c byte) bool {
	return c == '-' || c == '_' || c == '.' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') || c >= 0x80
}

// firstToken returns enough of s to name what was unexpected.
func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\r\n"); i > 0 {
		s = s[:i]
	}
	if len(s) > 24 {
		s = s[:24] + "..."
	}
	return s
}

// unimplemented is the error for a construct the grammar has and this package
// does not yet parse.
func unimplemented(what string) error {
	return fmt.Errorf("XPST0003: %s is not implemented yet", what)
}

// normalizeLineEndings applies XQuery 3.1 section 4.1's end-of-line handling
// to the query text: "\r\n" and a lone "\r" each become "\n".
//
// The common case is a query with no carriage return at all, which is
// returned untouched rather than rebuilt.
func normalizeLineEndings(src string) string {
	if !strings.ContainsRune(src, '\r') {
		return src
	}
	var sb strings.Builder
	sb.Grow(len(src))
	for i := 0; i < len(src); i++ {
		if src[i] != '\r' {
			sb.WriteByte(src[i])
			continue
		}
		sb.WriteByte('\n')
		if i+1 < len(src) && src[i+1] == '\n' {
			i++
		}
	}
	return sb.String()
}
