package xslt

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// internalNS is the namespace for bindings this package threads through the
// XPath context. The xpath package must not import xslt (that would be a
// cycle), so state that XSLT functions need — the runtime, the current group,
// the regex captures — travels as reserved variable bindings.
//
// Stylesheets cannot reach these: a variable reference resolves its prefix
// against the stylesheet's own namespace declarations, and nothing binds a
// prefix to this URI.
const internalNS = "urn:goxslt:internal"

// runtimeVar is the binding that carries the transform runtime.
var runtimeVar = xdm.QName{URI: internalNS, Local: "runtime"}

// currentVar carries the focus as it stood when the enclosing XSLT
// instruction began, which is what fn:current returns.
var currentVar = xdm.QName{URI: internalNS, Local: "current"}

// runtimeFrom recovers the runtime from an XPath context.
func runtimeFrom(ctx *xpath.Context) (*runtime, bool) {
	seq, ok := ctx.LookupVar(runtimeVar)
	if !ok || len(seq) == 0 {
		return nil, false
	}
	o, ok := seq[0].(*xdm.Opaque)
	if !ok {
		return nil, false
	}
	rt, ok := o.Value.(*runtime)
	if !ok {
		return nil, false
	}
	// The runtime's own context is stale by the time a nested expression
	// runs, so the caller's context is grafted on: key() must see the current
	// focus, not the one captured when the transform started.
	n := *rt
	n.ctx = ctx
	return &n, true
}

// runtimeFuncNames lists the functions bound per transform rather than at
// compile time, by registerRuntimeFuncs and registerGroupingFuncs together.
//
// These are absent from the stylesheet's compile-time library because each one
// closes over a *runtime that does not exist until a transform starts. A
// static check that resolves function names against that library must still
// treat them as declared, or it would reject key() and current() — which is
// exactly where they are most often written.
//
// The list is kept beside registerRuntimeFuncs so the two are edited together;
// TestRuntimeFuncNamesMatchRegistration holds the list and both registrars to
// each other.
var runtimeFuncNames = map[string]bool{
	"accumulator-after":    true,
	"accumulator-before":   true,
	"copy-of":              true,
	"current":              true,
	"current-group":        true,
	"current-grouping-key": true,
	"current-merge-group":  true,
	"current-merge-key":    true,
	"current-output-uri":   true,
	"document":             true,
	"element-available":    true,
	"function-available":   true,
	"generate-id":          true,
	"key":                  true,
	"regex-group":          true,
	"snapshot":             true,
	"system-property":      true,
	"type-available":       true,
}

// registerRuntimeFuncs adds the functions that need transform state.
func registerRuntimeFuncs(l *xpath.Library, rt *runtime) {
	// The EXSLT extension functions ride along here. They need no transform
	// state, but this is the library a running stylesheet actually sees, and
	// binding them per transform rather than in xpath.Builtins keeps them out
	// of the reach of every plain XPath caller — the same separation
	// RegisterXSLTFuncs draws for the XSLT-only functions.
	//
	// The name is in the EXSLT namespace, so this does not touch
	// runtimeFuncNames, which is keyed on locals in the fn namespace only.
	xpath.RegisterEXSLTFuncs(l)

	// fn:copy-of and fn:snapshot need no transform state either, but they are
	// bound here for the same reason: they are XSLT's, not XPath's, and a
	// bare xpath.Eval caller has no business seeing them. See copyfuncs.go.
	registerCopyFuncs(l, rt)

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "key"}, Arity: 2,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			return fnKey(rt, ctx, args)
		},
	})
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "key"}, Arity: 3,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			return fnKey(rt, ctx, args)
		},
	})

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "accumulator-before"}, Arity: 1,
		// XSLT 3.0 added accumulators, so a 2.0 stylesheet must get
		// XPST0017 rather than a working call. A version="3.0" stylesheet
		// compiles as XPath 3.1, which is what the gate tests.
		Since: xpath.XPath31,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			return fnAccumulator(rt, ctx, args, false)
		},
	})
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "accumulator-after"}, Arity: 1,
		Since: xpath.XPath31,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			return fnAccumulator(rt, ctx, args, true)
		},
	})

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "current"}, Arity: 0,
		Call: func(ctx *xpath.Context, _ []xdm.Sequence) (xdm.Sequence, error) {
			// current() is the node the enclosing XSLT instruction is
			// processing, which is NOT the context item once evaluation has
			// descended into a predicate. That distinction is the whole
			// reason the function exists:
			//
			//   preceding-sibling::*[local-name() = local-name(current())]
			//
			// must compare each sibling against the node being numbered, not
			// against itself — where the test is trivially true and counts
			// every sibling.
			// A dynamic call carries no XSLT focus across it, and XTDE1360
			// makes current#0() behave as if the context item were absent --
			// so the fallback to the context item below must not apply.
			if currentIsAbsent(ctx) {
				return nil, fmt.Errorf(
					"XTDE1360: current() was reached by a dynamic function " +
						"call, which is evaluated as if the context item " +
						"were absent")
			}
			if seq, ok := ctx.LookupVar(currentVar); ok {
				return seq, nil
			}
			// Outside any instruction (a bare XPath evaluation) the context
			// item is the only sensible answer. With no context item there is
			// no answer at all: XTDE1360 says so in as many words — "if the
			// current function is evaluated within an expression that is
			// evaluated when the context item is undefined, a non-recoverable
			// dynamic error occurs". Returning the empty sequence made
			// current() inside a stylesheet function, where the context item
			// is absent, quietly yield nothing.
			if ctx.Item == nil {
				return nil, fmt.Errorf(
					"XTDE1360: current() was called where the context item " +
						"is undefined")
			}
			return xdm.One(ctx.Item), nil
		},
	})

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "generate-id"}, Arity: 0,
		Call: func(ctx *xpath.Context, _ []xdm.Sequence) (xdm.Sequence, error) {
			return generateID(ctx.Item)
		},
	})
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "generate-id"}, Arity: 1,
		Call: func(_ *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			if len(args[0]) == 0 {
				return xdm.One(xdm.NewString("")), nil
			}
			return generateID(args[0][0])
		},
	})

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "document"}, Arity: 1,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			// fn:document is the XSLT 1.0 spelling of fn:doc, and is subject
			// to the same resolver gate — except for document(""), which
			// fetches nothing and is handled below.
			if ctx.Docs == nil && !onlyEmptyURIs(args[0]) {
				return nil, fmt.Errorf(
					"FODC0002: document() is disabled (no resolver configured)")
			}
			// Each item contributes a URI. Atomizing the whole argument
			// first threw the nodes away, and with them the one thing
			// section 16.1 needs: a relative reference taken from a node
			// resolves "against the base URI of the node that contained
			// it". document(doc('a/b/c.xml')/*/filename) must therefore
			// resolve filename's text against a/b/, not against the
			// stylesheet. Only an atomic argument, which carries no base
			// of its own, falls back to the static base URI.
			type docRequest struct{ uri, base string }
			var reqs []docRequest
			for _, it := range args[0] {
				switch v := it.(type) {
				case *xdm.Node:
					// The base URI in force *at* the node, not the field.
					// Only an element carrying xml:base has one of its own,
					// so document(@file) -- the ordinary spelling, and an
					// attribute -- read back empty and fell through to the
					// stylesheet's own directory, resolving every relative
					// reference taken from a source document against the
					// wrong place entirely. fn:document in
					// xpath/fn_document.go has always walked; this arity-1
					// shadow did not, and the two disagreed.
					b := inScopeBaseURI(v)
					// A relative reference held in a node resolves against
					// that node's base URI, and a parentless node has none --
					// the example XTDE1162 gives. The static base URI is not
					// a fallback: 16.1 makes the containing node's base the
					// one that applies, so its absence is the error.
					if b == "" && xpath.IsRelativeReference(v.StringValue()) {
						return nil, fmt.Errorf(
							"XTDE1162: document(%q) takes its relative "+
								"reference from a node with no base URI",
							v.StringValue())
					}
					if b == "" {
						b = ctx.StaticBaseURI
					}
					reqs = append(reqs, docRequest{v.StringValue(), b})
				default:
					for _, a := range xdm.Atomize(xdm.Sequence{it}) {
						// Section 16.1 resolves a relative reference from
						// an atomic value against "the base URI from the
						// static context", which is the base URI of the
						// element the call is written on — not the context
						// node's, whose base belongs to the *source*
						// document. Preferring the context node meant
						// document('x.xml') in an included module looked
						// for x.xml beside the input rather than beside
						// the module.
						base := ctx.StaticBaseURI
						if base == "" {
							// A stylesheet compiled without a base URI
							// leaves the static context with nothing; the
							// context node's base is then the only thing to
							// resolve against, and it is better than the
							// process's working directory.
							//
							// The nil check is not redundant with the type
							// assertion: a transform started from a named
							// template has no context item, and the
							// interface then holds a typed nil rather than
							// no value at all, so the assertion succeeds and
							// the field access is what faults.
							if n, ok := ctx.Item.(*xdm.Node); ok && n != nil {
								base = n.BaseURI
							}
						}
						reqs = append(reqs, docRequest{a.(*xdm.Atomic).String(), base})
					}
				}
			}
			var out xdm.Sequence
			seen := map[*xdm.Node]bool{}
			for _, r := range reqs {
				uri := r.uri
				// The zero-length URI names the document containing the
				// expression, which for a stylesheet is the stylesheet
				// itself. It is how a stylesheet carrying its own lookup
				// tables as literal data reads them, and it needs no
				// resolver because nothing is fetched.
				// The zero-length URI resolves against the base URI in force
				// where the call is written, and then names whatever document
				// is at the result. Where nothing has moved that base, the
				// answer is the containing stylesheet module, which is
				// returned directly because nothing needs fetching -- that is
				// how a stylesheet carrying its own lookup tables as literal
				// data reads them.
				//
				// 16.1's note is explicit that this holds only "unless XML
				// entities or xml:base are used": an xml:base on the template
				// resolves the empty reference to a *different* URI, naming a
				// different resource. base-uri-050 writes document('') under
				// three nested xml:base attributes and requires three
				// different answers, so the shortcut is taken only when the
				// base still agrees with the module's own.
				if strings.TrimSpace(uri) == "" && rt.sheet.source != nil &&
					sameResource(r.base, rt.sheet.source.BaseURI) {
					if !seen[rt.sheet.source] {
						seen[rt.sheet.source] = true
						out = append(out, rt.sheet.source)
					}
					continue
				}
				// A fragment identifier that is not legal for an XML media
				// type is XTRE1160, and it is decidable from the URI string
				// alone -- so it is diagnosed before the resource is
				// fetched, which is the only way to reach the right answer
				// for a URI this engine will not retrieve at all.
				// fn:document in xpath/fn_document.go does the same; this
				// arity-1 shadow reported the refusal to fetch instead, so
				// the URI never got as far as the check.
				if !xpath.FragmentIsValidXMLName(uri) {
					return nil, fmt.Errorf(
						"XTRE1160: %q has a fragment identifier that is "+
							"not valid for an XML media type", uri)
				}
				if ctx.Docs == nil {
					return nil, fmt.Errorf(
						"FODC0002: document() is disabled (no resolver configured)")
				}
				tree, err := ctx.Docs.ResolveDocument(uri, r.base)
				if err != nil {
					return nil, fmt.Errorf("FODC0002: cannot retrieve %q: %w", uri, err)
				}
				if tree == nil || tree.Root == nil {
					return nil, fmt.Errorf("FODC0002: %q retrieved no document", uri)
				}
				// 16.1 requires two calls naming the same resource to
				// return the same node, so a repeated URI in one call
				// must not put the same tree in the result twice.
				if seen[tree.Root] {
					continue
				}
				seen[tree.Root] = true
				out = append(out, tree.Root)
			}
			return out, nil
		},
	})

	// fn:unparsed-entity-uri and fn:unparsed-entity-public-id report the
	// identifiers of an entity the processor never reads. They are XSLT's
	// own functions, and they answer from the document containing the
	// context node, which is what makes them runtime functions rather than
	// static ones.
	for _, fn := range []struct {
		name   string
		public bool
		code   string
	}{
		{"unparsed-entity-uri", false, "XTDE1370"},
		{"unparsed-entity-public-id", true, "XTDE1380"},
	} {
		public, code, fname := fn.public, fn.code, fn.name
		l.Add(xpath.Function{
			Name: xdm.QName{URI: xdm.NSFN, Local: fn.name}, Arity: 1,
			Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
				name := stringArg(args[0])
				n, ok := ctx.Item.(*xdm.Node)
				// XTDE1370 and XTDE1380 say the same thing of their own
				// function: it is an error "when there is no context node, or
				// when the root of the tree containing the context node is
				// not a document node". Both halves matter — a temporary tree
				// rooted at an element has a context node but no document to
				// hold an entity declaration, so answering the zero-length
				// string there would report "no such entity" for a question
				// that could never have had an answer.
				if !ok {
					return nil, fmt.Errorf(
						"%s: %s() has no context node", code, fname)
				}
				if n.Root().Kind != xdm.KindDocument {
					return nil, fmt.Errorf(
						"%s: the root of the tree containing the context node "+
							"of %s() is not a document node", code, fname)
				}
				sys, pub, _, found := n.Tree().UnparsedEntity(name)
				if !found {
					// An entity that is not declared, or is declared parsed,
					// yields the zero-length string.
					return xdm.One(xdm.NewAnyURI("")), nil
				}
				if public {
					return xdm.One(xdm.NewString(pub)), nil
				}
				// The system identifier is resolved against the base URI of
				// the document holding the declaration, which is where a
				// relative one is written.
				return xdm.One(xdm.NewAnyURI(resolveAgainst(n.Root().BaseURI, sys))), nil
			},
		})

		// XSLT 3.0 20.4 adds a two-argument form naming the document to ask,
		// so that a function with no focus of its own can still ask -- which
		// is the point of it: sf-unparsed-entity-03 passes snapshot(.) into
		// an xsl:function and asks there. The second argument is a node, and
		// the tree that holds it answers.
		l.Add(xpath.Function{
			Name: xdm.QName{URI: xdm.NSFN, Local: fn.name}, Arity: 2,
			Call: func(_ *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
				name := stringArg(args[0])
				var n *xdm.Node
				if len(args[1]) == 1 {
					n, _ = args[1][0].(*xdm.Node)
				}
				if n == nil {
					return nil, fmt.Errorf(
						"%s: the second argument of %s() is not a node",
						code, fname)
				}
				if n.Root().Kind != xdm.KindDocument {
					return nil, fmt.Errorf(
						"%s: the root of the tree named by %s() is not a "+
							"document node", code, fname)
				}
				sys, pub, _, found := n.Tree().UnparsedEntity(name)
				if !found {
					return xdm.One(xdm.NewAnyURI("")), nil
				}
				if public {
					return xdm.One(xdm.NewString(pub)), nil
				}
				return xdm.One(xdm.NewAnyURI(
					resolveAgainst(n.Root().BaseURI, sys))), nil
			},
		})
	}

	// The static functions are registered separately so that use-when, whose
	// context has no runtime at all, can have exactly these and nothing else.
	registerStaticFuncs(l, rt.resolveFunctionName, rt.resolveTypeName, rt.schemaHasType)
}

// registerStaticFuncs adds the four functions section 3.12 makes available to
// a use-when expression: they answer questions about the *processor* rather
// than about the stylesheet or the source, so they need no runtime and are
// legal in a context that has none.
func registerStaticFuncs(l *xpath.Library, resolve, resolveType prefixResolver, schemaHasType func(xdm.QName) bool) {
	// fn:available-system-properties answers from the same table
	// fn:system-property does, and is available wherever it is -- including
	// a use-when, which section 3.12 makes a static context like any other.
	registerSystemPropertyFuncs(l)

	// A nil resolver means there is no stylesheet to resolve prefixes
	// against: this is the use-when library, built before the prefix map
	// exists. system-property must not report XTDE1390 there, because it
	// cannot tell an unbound prefix from one the module bound to the XSLT
	// namespace under a name it does not recognise.
	haveStylesheet := resolve != nil
	if resolve == nil {
		resolve = defaultFunctionNS
	}
	// A use-when library has no stylesheet: there is no prefix map and no
	// imported schema, so a type name can only be resolved conventionally
	// and no schema type exists to find.
	if resolveType == nil {
		resolveType = defaultElementNS
	}
	if schemaHasType == nil {
		schemaHasType = func(xdm.QName) bool { return false }
	}
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "system-property"}, Arity: 1,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
			// XTDE1390: the argument must be a valid QName. A malformed one
			// would otherwise fall through to the empty string, which is
			// what a *valid* name for an unknown property returns — so the
			// two cases would be indistinguishable.
			// 3.0 widened the argument to an EQName, whose URIQualifiedName
			// form carries its own namespace and so needs no prefix in scope
			// -- which is the reason a stylesheet writes one.
			if isEQName(name) {
				end := strings.IndexByte(name, '}')
				if name[2:end] != xdm.NSXSL {
					return xdm.One(xdm.NewString("")), nil
				}
				if val, ok := systemPropertyValue(
					name[end+1:], ctx.Version); ok {
					return xdm.One(xdm.NewString(val)), nil
				}
				return xdm.One(xdm.NewString("")), nil
			}
			if !isLexicalQName(name) {
				return nil, fmt.Errorf(
					"XTDE1390: system-property(%q) is not a valid QName", name)
			}
			// The other half of XTDE1390 — "there is no namespace declaration
			// in scope for the prefix of the QName" — needs the prefix
			// resolved rather than guessed at. A stylesheet binds the XSLT
			// namespace to whatever prefix it likes, and the suite uses "t:"
			// and "xslt:" as often as "xsl:", so rejecting the unfamiliar
			// prefixes outright once cost thirteen tests. Resolving them
			// against the stylesheet's own bindings answers both halves: an
			// unbound prefix is the error, and a bound one names the XSLT
			// namespace or it does not.
			//
			// resolve puts an unprefixed name in the function namespace,
			// which is right for function-available but not here: an
			// unprefixed system property name is in no namespace, so it is
			// never one of the XSLT-defined properties. system-property-010
			// pins that even when xpath-default-namespace is the XSLT one.
			prefix, local := xdm.SplitQName(name)
			uri := ""
			if prefix != "" {
				var ok bool
				if uri, _, ok = resolve(name); !ok {
					if haveStylesheet {
						return nil, fmt.Errorf(
							"XTDE1390: system-property(%q): no namespace "+
								"declaration is in scope for the prefix", name)
					}
					// Without a stylesheet the prefix is taken on trust: a
					// use-when that asks for xslt:version under its own
					// binding is asking a well-formed question.
					uri = xdm.NSXSL
				}
			}
			// Only the properties in the XSLT namespace are defined. Section
			// 16.6.5 says any other name — including an unprefixed one, whose
			// expanded name is in no namespace even when the default element
			// namespace is the XSLT one — returns the empty string.
			if uri != xdm.NSXSL {
				return xdm.One(xdm.NewString("")), nil
			}
			// The table is in sysprops.go, shared with
			// fn:available-system-properties: section 18.2 requires the two
			// to agree, and a switch here plus a list there would drift.
			if val, ok := systemPropertyValue(local, ctx.Version); ok {
				return xdm.One(xdm.NewString(val)), nil
			}
			return xdm.One(xdm.NewString("")), nil
		},
	})

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "function-available"}, Arity: 1,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
			uri, local, ok, err := availableName(
				ctx, "XTDE1400", "function-available", name, resolve)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, unboundPrefixError("function-available", name)
			}
			for arity := 0; arity <= 4; arity++ {
				if _, ok := xpath.LookupVisible(ctx, xdm.QName{URI: uri, Local: local}, arity); ok {
					return xdm.One(xdm.NewBoolean(true)), nil
				}
			}
			return xdm.One(xdm.NewBoolean(false)), nil
		},
	})

	// The two-argument form names an arity: function-available('fn:concat', 2)
	// asks whether the function exists with exactly that many arguments,
	// where the one-argument form asks whether it exists at all.
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "function-available"}, Arity: 2,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
			uri, local, ok, err := availableName(
				ctx, "XTDE1400", "function-available", name, resolve)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, unboundPrefixError("function-available", name)
			}
			arity := 0
			for _, a := range xdm.Atomize(args[1]) {
				if at, ok := a.(*xdm.Atomic); ok {
					arity = int(at.Int64())
				}
			}
			_, ok = xpath.LookupVisible(ctx, xdm.QName{URI: uri, Local: local}, arity)
			return xdm.One(xdm.NewBoolean(ok)), nil
		},
	})

	// fn:type-available asks whether a type is in the static context. That is
	// the built-in xs: types plus whatever xsl:import-schema brought in, so
	// the schema has to be consulted as well: a stylesheet that imports a
	// schema declaring my:hatsize must answer true for it.
	//
	// The name is a TYPE name, not a function name, so it is resolved with
	// resolveType rather than with resolve: an unprefixed type name is in the
	// default *element* namespace (XPath 2.0 2.1.1), where an unprefixed
	// function name is in the default function namespace. Answering
	// type-available('shortString') by looking for fn:shortString was the
	// second half of why an imported schema never registered.
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "type-available"}, Arity: 1,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
			// XTDE1428 is the type-available spelling of the condition
			// XTDE1400 states for function-available: a name that is not a
			// QName at all is an error, not a type that happens to be absent.
			uri, local, ok, err := availableName(
				ctx, "XTDE1428", "type-available", name, resolveType)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, unboundTypePrefixError(name)
			}
			if uri != xdm.NSXS {
				// Not a built-in: the only way it can be available is for the
				// imported schema to declare it.
				return xdm.One(xdm.NewBoolean(schemaHasType(xdm.QName{URI: uri, Local: local}))), nil
			}
			if _, found := xpath.BuiltinAtomicTypeCode(local); found {
				return xdm.One(xdm.NewBoolean(true)), nil
			}
			// The built-in types that are not atomic are not in the atomic
			// table but are still in the static context of every processor:
			// the two complex types at the root of the hierarchy, and the
			// three built-in list types. Asking whether a type is available
			// is not asking whether a value can have it, so an abstract or
			// complex type answers true.
			if builtinNonAtomicTypes[local] {
				return xdm.One(xdm.NewBoolean(true)), nil
			}
			return xdm.One(xdm.NewBoolean(schemaHasType(xdm.QName{URI: uri, Local: local}))), nil
		},
	})

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "element-available"}, Arity: 1,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
			_, local, _, err := availableName(
				ctx, "XTDE1440", "element-available", name,
				func(n string) (string, string, bool) {
					_, l := xdm.SplitQName(n)
					return "", l, true
				})
			if err != nil {
				return nil, err
			}
			if !supportedInstructions[local] {
				return xdm.One(xdm.NewBoolean(false)), nil
			}
			// An instruction XSLT 3.0 introduced is not available to a
			// stylesheet declaring an earlier version, whatever this engine
			// implements. try-013 is that test: it asks about xsl:try and
			// xsl:catch from a version="2.0" stylesheet and requires false
			// for both, because the whole point of asking is to find out
			// whether the instruction may be used here.
			if instructionsSince30[local] && !ctx.Version.AtLeast31() {
				return xdm.One(xdm.NewBoolean(false)), nil
			}
			return xdm.One(xdm.NewBoolean(true)), nil
		},
	})
}

// supportedInstructions backs fn:element-available.
var supportedInstructions = map[string]bool{
	"apply-templates": true, "call-template": true, "value-of": true,
	"for-each": true, "for-each-group": true, "if": true, "choose": true,
	"variable": true, "element": true, "attribute": true, "comment": true,
	"processing-instruction": true, "copy": true, "copy-of": true,
	"sequence": true, "text": true, "message": true, "analyze-string": true,
	"number": true,
	// XSLT 3.0. xsl:catch is listed even though it is not an instruction in
	// its own right: element-available answers for any XSLT element name, and
	// try-012 asks about both halves of the pair.
	"try": true, "catch": true,
	"on-empty": true, "on-non-empty": true, "where-populated": true,
	"fork":    true,
	"iterate": true, "break": true, "next-iteration": true,
	"evaluate": true,
	"map":      true, "map-entry": true,
	"source-document": true, "context-item": true,
	// xsl:merge and its three companions. The last three are not instructions
	// in their own right, but element-available answers for any XSLT element
	// name and a stylesheet asking about the set asks about all four.
	"merge": true, "merge-source": true, "merge-key": true, "merge-action": true,
}

// instructionsSince30 are the entries above that XSLT 3.0 introduced.
//
// Kept as a second set rather than a field on the first because
// supportedInstructions answers "does this engine implement it" and this
// answers "may this stylesheet use it" -- two questions that happen to share
// a key and would be confusing to conflate under one name.
var instructionsSince30 = map[string]bool{
	"try": true, "catch": true,
	"on-empty": true, "on-non-empty": true, "where-populated": true,
	"fork":    true,
	"iterate": true, "break": true, "next-iteration": true,
	"evaluate": true,
	"map":      true, "map-entry": true,
	"source-document": true, "context-item": true,
	"merge": true, "merge-source": true, "merge-key": true, "merge-action": true,
}

// generateID returns a stable identifier for a node.
//
// It must be constant for a node within a transform and distinct between
// nodes. Document order plus tree identity satisfies both, and unlike a
// pointer-derived value it is reproducible across runs, which keeps output
// diffable.
func generateID(it xdm.Item) (xdm.Sequence, error) {
	// Shared with the XPath 3.0 fn:generate-id rather than duplicated: two
	// spellings of the same identity would let generate-id() answer
	// differently for the same node depending on whether it was called from a
	// stylesheet or from a bare expression.
	return xdm.One(xdm.NewString(xpath.GenerateID(it))), nil
}

// fnKey implements fn:key.
//
// The index is built lazily on first use and cached per (key name, document).
// Building it eagerly for every declared key would scan the document once per
// key even when a stylesheet uses none of them, and rule sets routinely
// declare keys for code lists they only consult on some documents.
// stringArg reads a function argument declared xs:string.
//
// The parameter is atomized, which is what the function calling rules
// require: a node reaches it as its typed value, not as itself. Asserting
// *xdm.Atomic instead panics on any node argument, and the XSLT suite calls
// system-property() with one — a panic in a request handler is a denial of
// service, so this is a safety fix rather than a conformance one.
func stringArg(seq xdm.Sequence) string {
	atoms := xdm.Atomize(seq)
	if len(atoms) == 0 {
		return ""
	}
	if a, ok := atoms[0].(*xdm.Atomic); ok {
		return a.String()
	}
	return ""
}

func fnKey(rt *runtime, ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
	nameSeq := xdm.Atomize(args[0])
	if len(nameSeq) == 0 {
		return xdm.Empty(), nil
	}
	lexName := nameSeq[0].(*xdm.Atomic).String()

	// XTDE1260 covers three cases in one sentence: the name "is not a valid
	// QName, or ... there is no namespace declaration in scope for the prefix
	// of the QName, or ... the name obtained by expanding the QName is not
	// the same as the expanded name of any xsl:key declaration".
	if !isLexicalQName(lexName) {
		return nil, fmt.Errorf(
			"XTDE1260: key(%q): the name is not a valid QName", lexName)
	}
	prefix, local := xdm.SplitQName(lexName)
	// The stylesheet's namespace context is not available at call time, so
	// the prefix is resolved against the bindings the stylesheet collected at
	// compile time. An unresolvable prefix and a resolvable one naming no key
	// are the same error, so failing to find a binding falls through to the
	// lookup rather than being reported separately.
	uri := ""
	if prefix != "" {
		bound := false
		if uri, bound = rt.sheet.prefixes[prefix]; !bound {
			// An unbound prefix is XTDE1260 in its own right. Falling through
			// with the empty URI instead made key("your:k", ...) find the key
			// declared as "k" in no namespace, which is a different key.
			return nil, fmt.Errorf(
				"XTDE1260: key(%q): no namespace declaration is in scope for "+
					"the prefix %q", lexName, prefix)
		}
	}
	keyName := xdm.QName{URI: uri, Local: local}.Clark()
	defs, ok := rt.sheet.keys[keyName]
	if !ok {
		return nil, fmt.Errorf("XTDE1260: no xsl:key named %q", lexName)
	}

	// The third argument, when present, names the document to search; without
	// it the search covers the tree containing the context node.
	var root, top *xdm.Node
	if len(args) > 2 && len(args[2]) > 0 {
		if n, isNode := args[2][0].(*xdm.Node); isNode {
			top = n
			root = n.Root()
		}
	}
	if root == nil {
		n, err := ctx.ContextNode()
		if err != nil {
			return nil, fmt.Errorf(
				"XTDE1270: key() has no context node to search from")
		}
		root = n.Root()
	}
	// XTDE1270: it is an error "to call the key function with two arguments
	// if there is no context node, or if the root of the tree containing the
	// context node is not a document node; or to call the function with three
	// arguments if the root of the tree containing the node supplied in the
	// third argument is not a document node."
	//
	// Both forms come down to the same requirement, which is why one check
	// serves them: a key is an index over a document, and a temporary tree
	// rooted at an element is not one. Searching it anyway simply found
	// nothing, so the stylesheet saw an empty result rather than a mistake.
	if root.Kind != xdm.KindDocument {
		return nil, fmt.Errorf(
			"XTDE1270: key() searches a tree whose root is not a document node")
	}

	index, err := rt.keyIndexFor(keyName, defs, root, ctx)
	if err != nil {
		return nil, err
	}

	var out xdm.Sequence
	// The sought value is folded by the same collation the index was built
	// with, so that a case-blind key finds a node whose value differs only in
	// case. Every definition of a given name shares a collation in practice;
	// the first is used, which is what the index was keyed by.
	var coll xpath.Collation
	if len(defs) > 0 {
		if c, err := rt.keyCollation(defs[0]); err == nil {
			coll = c
		}
	}
	for _, kv := range xdm.Atomize(args[1]) {
		a := kv.(*xdm.Atomic)
		k, err := rt.keySearchKey(a, coll)
		if err != nil {
			return nil, err
		}
		out = append(out, index[k]...)

		// 3.8: under backwards compatibility key() compares by string value,
		// as 1.0's did -- 1.0 had one string type and one numeric type, and
		// key('k', 1.0) found the nodes keyed on "1". The index is typed, so
		// the untyped form of the sought value is looked up *as well as* the
		// typed one rather than instead of it: backwards-043 declares the same
		// key twice, once in a 1.0 module and once in a 2.0 one, and both
		// declarations feed the one shared index, so coercing the index side
		// would split it in two.
		if !ctx.Compat {
			continue
		}
		alt := compatKeyValue(a)
		if alt == a {
			continue
		}
		ak, err := rt.keySearchKey(alt, coll)
		if err != nil {
			return nil, err
		}
		if ak != k {
			out = append(out, index[ak]...)
		}
	}
	if top != nil && top.Kind != xdm.KindDocument {
		// Section 16.3: the third argument names a *subtree*, not a document.
		// "The selected subtree is the set of nodes that have $top as an
		// ancestor-or-self node", and a node is selected only when
		// "$N/ancestor-or-self::node() intersect $top" is non-empty. The index
		// is still built over the whole tree — that is what makes it an index
		// — so the restriction is applied to the result. Treating $top as its
		// own root returned every matching node in the document instead of
		// the ones under it.
		var kept xdm.Sequence
		for _, it := range out {
			if n, isNode := it.(*xdm.Node); isNode && hasAncestorOrSelf(n, top) {
				kept = append(kept, it)
			}
		}
		out = kept
	}
	return xdm.SortDocumentOrder(out), nil
}

// hasAncestorOrSelf reports whether top is n or one of its ancestors.
func hasAncestorOrSelf(n, top *xdm.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p == top {
			return true
		}
	}
	return false
}

// keyLookupKey is the string a key value is indexed and looked up by.
//
// Section 16.3 compares key values by value, using the "eq" rules, not by
// their lexical form: a key declared use="xs:dateTime(.)" must find a node
// whose stored value names the same instant even when the two are written in
// different timezones, and an xs:double key must find NaN. Indexing on the
// string form did neither.
func (rt *runtime) keyLookupKey(a *xdm.Atomic, coll xpath.Collation) (string, error) {
	return xpath.GroupingKey(a, coll, rt.ctx.ImplicitTimezone)
}

// keySearchKey is keyLookupKey for the value being searched for.
//
// It differs in one case: NaN. fn:key selects the nodes whose key value is
// *equal* to the sought value, and NaN equals nothing, itself included — so
// key('i', xs:double('NaN')) must select nothing even though nodes with a NaN
// key value are in the index. Giving the search a key the index cannot contain
// is what expresses that.
func (rt *runtime) keySearchKey(a *xdm.Atomic, coll xpath.Collation) (string, error) {
	if a.IsNaN() {
		return "\x00fn:key-NaN-matches-nothing", nil
	}
	return rt.keyLookupKey(a, coll)
}

// keyValues computes the key values one node contributes.
//
// A key is declared either with a use expression or with a sequence
// constructor, and the two produce their value differently: the expression is
// atomized, while the constructor builds a temporary tree whose string value
// is the key. Both may yield several values, which puts the node under each.
func (rt *runtime) keyValues(def *keyDef, ctx *xpath.Context, n *xdm.Node) ([]*xdm.Atomic, error) {
	if def.use != nil {
		// current() inside xsl:key/@use is the node being keyed. WithFocus
		// sets the context item but not the current-node variable the
		// function reads, so the use expression saw whatever current() meant
		// where the index happened to be built.
		// current() inside xsl:key/@use is the node being keyed. WithFocus
		// sets the context item but not the current-node variable the
		// function reads, so the use expression saw whatever current() meant
		// where the index happened to be built.
		vals, err := def.use.Eval(
			ctx.WithFocus(n, 1, 1).WithVar(currentVar, xdm.One(n)))
		if err != nil {
			return nil, err
		}
		out := make([]*xdm.Atomic, 0, len(vals))
		for _, v := range xdm.Atomize(vals) {
			if a, ok := v.(*xdm.Atomic); ok {
				out = append(out, a)
			}
		}
		return out, nil
	}

	// The constructor form. The focus is the matched node, as it is for the
	// expression form, so the same key definition reads the same way.
	sub := rt.temporaryOutput()
	sub.ctx = ctx.WithFocus(n, 1, 1)
	out := newOutputBuilder()
	if err := execSequence(def.body, sub, out); err != nil {
		return nil, err
	}
	// The constructor's result keeps its type. An xsl:sequence yielding an
	// xs:integer gives an integer key, which key('k', 4) then finds; building
	// a temporary tree and taking its string value instead turned every such
	// key into a string that no typed lookup could match.
	//
	// A constructor that produced nodes or text has no atomic value of its
	// own, so the tree's string value remains the answer for those.
	items := out.sequence()
	atoms := xdm.Atomize(items)
	if len(atoms) == 0 {
		return []*xdm.Atomic{xdm.NewString(out.toTree().StringValue())}, nil
	}
	vals := make([]*xdm.Atomic, 0, len(atoms))
	for _, it := range atoms {
		if a, ok := it.(*xdm.Atomic); ok {
			vals = append(vals, a)
		}
	}
	return vals, nil
}

// keyIndexFor returns the index for a key over a document, building it if
// necessary.
func (rt *runtime) keyIndexFor(name string, defs []*keyDef, root *xdm.Node,
	ctx *xpath.Context) (map[string]xdm.Sequence, error) {

	ck := keyCacheKey{name: name, tree: root.Tree()}
	if idx, ok := rt.keyIndex[ck]; ok {
		return idx, nil
	}
	// A key whose own match or use expression calls key() for this same key
	// over this same document is circular. The cache below is only written
	// after the whole scan finishes, so without this mark the re-entrant
	// call would start a second scan, and that one a third, until the
	// recursion guard reported XPDY0001 instead of the actual diagnosis.
	if rt.keyBuilding[ck] {
		return nil, fmt.Errorf(
			"XTDE0640: key %q is defined circularly: its own definition "+
				"calls key(%q, ...) over the same document", name, name)
	}
	rt.keyBuilding[ck] = true
	defer delete(rt.keyBuilding, ck)

	// Synthesizing the namespace axis is only worth doing where a key can
	// match a namespace node; see canMatchNamespaceNode.
	nsKeys := false
	for _, def := range defs {
		if def.match.canMatchNamespaceNode() {
			nsKeys = true
			break
		}
	}

	idx := map[string]xdm.Sequence{}
	var walk func(*xdm.Node) error
	walk = func(n *xdm.Node) error {
		for _, def := range defs {
			ok, err := def.match.Matches(n, ctx)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			vals, err := rt.keyValues(def, ctx, n)
			if err != nil {
				return err
			}
			coll, err := rt.keyCollation(def)
			if err != nil {
				return err
			}
			for _, kv := range vals {
				k, err := rt.keyLookupKey(kv, coll)
				if err != nil {
					return err
				}
				idx[k] = append(idx[k], n)
			}
		}
		// Attributes can be key targets, so they are visited too.
		for _, a := range n.Attrs {
			for _, def := range defs {
				ok, err := def.match.Matches(a, ctx)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
				vals, err := rt.keyValues(def, ctx, a)
				if err != nil {
					return err
				}
				coll, err := rt.keyCollation(def)
				if err != nil {
					return err
				}
				for _, kv := range vals {
					k, err := rt.keyLookupKey(kv, coll)
					if err != nil {
						return err
					}
					idx[k] = append(idx[k], a)
				}
			}
		}
		// XSLT 3.0 lets a pattern match a namespace node, so the index walk
		// has to offer them. They are synthesized per element from the whole
		// in-scope set rather than read off the element's own declarations:
		// key-087 keys on the namespace axis, where an inherited binding
		// belongs to every element that inherits it.
		if nsKeys && n.Kind == xdm.KindElement {
			for _, nsNode := range xpath.NamespaceNodesOf(n) {
				for _, def := range defs {
					ok, err := def.match.Matches(nsNode, ctx)
					if err != nil {
						return err
					}
					if !ok {
						continue
					}
					vals, err := rt.keyValues(def, ctx, nsNode)
					if err != nil {
						return err
					}
					coll, err := rt.keyCollation(def)
					if err != nil {
						return err
					}
					for _, kv := range vals {
						k, err := rt.keyLookupKey(kv, coll)
						if err != nil {
							return err
						}
						idx[k] = append(idx[k], nsNode)
					}
				}
			}
		}
		for _, ch := range n.Children {
			if err := walk(ch); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}

	rt.keyIndex[ck] = idx
	return idx, nil
}

// registerGroupingFuncs adds the functions that read grouping and regex state.
// Like key() and current(), they reach that state through internal variable
// bindings rather than a typed context field.
func registerGroupingFuncs(l *xpath.Library) {
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "current-group"}, Arity: 0,
		Call: func(ctx *xpath.Context, _ []xdm.Sequence) (xdm.Sequence, error) {
			// Section 14.4 made calling this outside a grouping an error in
			// 3.0, where 2.0 answered the empty sequence; see
			// grouping_absent.go for why an in-scope marker is needed to
			// tell that apart from an empty group.
			if !groupingInScope(ctx) {
				return nil, errNoGrouping(ctx, "XTDE1061", "current-group")
			}
			seq, _ := ctx.LookupVar(currentGroupVar)
			return seq, nil
		},
	})
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "current-grouping-key"}, Arity: 0,
		Call: func(ctx *xpath.Context, _ []xdm.Sequence) (xdm.Sequence, error) {
			if !groupingInScope(ctx) {
				return nil, errNoGrouping(ctx, "XTDE1071",
					"current-grouping-key")
			}
			// 14.4: the key is absent, not empty, inside a
			// group-starting-with or group-ending-with, which partition by
			// position rather than by value.
			if !groupingKeyPresent(ctx) {
				return nil, errNoGrouping(ctx, "XTDE1071",
					"current-grouping-key")
			}
			seq, _ := ctx.LookupVar(currentGroupingKeyVar)
			return seq, nil
		},
	})
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "regex-group"}, Arity: 1,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			atoms := xdm.Atomize(args[0])
			if len(atoms) == 0 {
				return xdm.One(xdm.NewString("")), nil
			}
			idx, err := xpath.CastAtomic(atoms[0].(*xdm.Atomic), xdm.TypeInteger)
			if err != nil {
				return nil, err
			}
			groups, _ := ctx.LookupVar(regexGroupsVar)
			// Int64 truncates, so a group number outside int64 wrapped into a
			// valid index and returned an unrelated group. Anything that does
			// not fit is out of range by definition.
			n := -1
			if idx.FitsInt64() {
				if v := idx.Int64(); v >= 0 && v <= int64(len(groups)) {
					n = int(v)
				}
			}
			if n < 0 || n >= len(groups) {
				// An out-of-range group is "", not an error: a pattern branch
				// may legitimately not participate in a given match.
				return xdm.One(xdm.NewString("")), nil
			}
			return xdm.One(groups[n]), nil
		},
	})
}

// onlyEmptyURIs reports whether every argument item is the zero-length URI.
//
// document("") names the stylesheet and reaches no resolver, so the gate that
// refuses document access without one must not refuse it.
func onlyEmptyURIs(seq xdm.Sequence) bool {
	for _, it := range xdm.Atomize(seq) {
		if strings.TrimSpace(it.(*xdm.Atomic).String()) != "" {
			return false
		}
	}
	return true
}

// resolveAgainst resolves a possibly-relative reference against a base URI,
// returning the reference unchanged when the base is unusable.
func resolveAgainst(base, ref string) string {
	if ref == "" || base == "" {
		return ref
	}
	b, err := url.Parse(base)
	if err != nil || !b.IsAbs() {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	resolved := b.ResolveReference(r)
	if resolved.RawPath != "" {
		resolved.Path, resolved.RawPath = resolved.RawPath, ""
	}
	out := resolved.String()
	// net/url percent-escapes on the way out anything it does not consider
	// legal in a path, and a system identifier is a URI reference the
	// document author wrote, not text to be escaped. The case that matters
	// is the backslash: a DTD naming "images\repository\pic.jpg" came back
	// as "images%5Crepository%5Cpic.jpg", and unparsed-entity-50 is a
	// stylesheet that splits the returned path on "\" to get the filename —
	// after escaping there is no separator left to split on, so it keeps the
	// whole directory chain.
	//
	// Setting RawPath does not help: EscapedPath validates it against Path
	// and discards any RawPath it would itself have escaped, so the value
	// has to be put back after String() has run. Only escapes this function
	// introduced are undone — a %5C the author wrote survives, because it
	// was never a literal backslash in ref to begin with.
	if strings.ContainsAny(ref, "\\") && !strings.Contains(ref, "%5C") &&
		!strings.Contains(ref, "%5c") {
		out = strings.ReplaceAll(out, "%5C", "\\")
	}
	return out
}

// isLexicalQName reports whether s has the form of a QName: an NCName, or two
// separated by one colon.
func isLexicalQName(s string) bool {
	if s == "" {
		return false
	}
	parts := strings.Split(s, ":")
	if len(parts) > 2 {
		return false
	}
	for _, p := range parts {
		if !xdm.IsNCName(p) {
			return false
		}
	}
	return true
}

// keyCollation resolves the collation a key declaration compares under.
//
// An unrecognised URI is FOCH0002 where a collation is *used*, but a key
// declared with one that this engine does not implement should not make the
// whole index fail: the codepoint collation is the documented fallback, and
// it is what the key would have used had no collation been named.
func (rt *runtime) keyCollation(def *keyDef) (xpath.Collation, error) {
	if def.collation == "" {
		return nil, nil
	}
	coll, err := xpath.ResolveCollation(def.collation)
	if err != nil {
		return nil, nil
	}
	return coll, nil
}

// checkAvailableArg applies the lexical half of XTDE1400 and XTDE1440.
//
// Both say the same thing about their argument: it "does not evaluate to a
// string that is a valid QName, or ... there is no namespace declaration in
// scope for the prefix of the QName". Only the first half is decidable here,
// because these functions are registered in a library that does not carry the
// stylesheet's namespace context; an unbound prefix still answers false rather
// than raising, which is the pre-existing behaviour and is not made worse by
// catching the malformed names.
//
// The distinction matters because a malformed name and a valid name for an
// absent function are otherwise indistinguishable: both would answer false,
// so a stylesheet asking about "c#" would silently be told the instruction
// does not exist rather than that it asked a meaningless question.
// unboundPrefixError reports XTDE1400 for a lexically valid QName whose prefix
// nothing binds.
//
// XSLT 2.0 appendix E gives XTDE1400 both halves of the condition: "if the
// argument does not evaluate to a string that is a valid lexical QName, or if
// there is no namespace declaration in scope for the prefix of the QName".
// checkAvailableArg already raised it for the first half; answering false for
// the second let extension-functions-0104 run to completion.
func unboundPrefixError(fn, name string) error {
	prefix, _ := xdm.SplitQName(name)
	return fmt.Errorf("XTDE1400: %s(%q): no namespace declaration is in scope for prefix %q", fn, name, prefix)
}

func checkAvailableArg(code, fn, name string) error {
	if isLexicalQName(name) {
		return nil
	}
	return fmt.Errorf("%s: %s(%q) is not a valid QName", code, fn, name)
}

// resolveFunctionName expands the lexical QName that function-available and
// type-available are given.
//
// The prefix has to be resolved against the stylesheet's own namespace
// declarations, not against a fixed table: "fn" is only conventionally bound
// to the function namespace, and function-available-1006 binds it to the 2003
// draft URI precisely to check that a stylesheet that does so gets false back.
// The XPath context carries no namespace resolver, so the stylesheet-wide
// prefix map collected at compile time is used, the same way fn:key does.
//
// An unprefixed name is in the default function namespace, which is the
// standard function namespace — not the default element namespace.
func (rt *runtime) resolveFunctionName(name string) (uri, local string, ok bool) {
	prefix, local := xdm.SplitQName(name)
	if prefix == "" {
		return xdm.NSFN, local, true
	}
	if rt != nil && rt.sheet != nil {
		if u, found := rt.sheet.prefixes[prefix]; found {
			return u, local, true
		}
	}
	// A prefix nothing binds cannot name a function. Answering false is what
	// the spec requires of a name that does not resolve.
	return "", local, false
}

// resolveTypeName expands the lexical QName fn:type-available is given.
//
// It differs from resolveFunctionName in exactly one place, and the difference
// is the whole point: an unprefixed name here is in the default ELEMENT
// namespace, not in the function namespace. XPath 2.0 2.1.1 says so, and
// type-available-0149 depends on it — its schema has no targetNamespace, so
// its four type names are unprefixed and live in no namespace at all.
//
// The stylesheet-wide prefix map is used for a prefixed name, the same
// superset-is-the-safe-direction reasoning resolveFunctionName records.
func (rt *runtime) resolveTypeName(name string) (uri, local string, ok bool) {
	prefix, local := xdm.SplitQName(name)
	if prefix == "" {
		// No default element namespace is tracked per expression here, so an
		// unprefixed name is taken as being in no namespace, which is what a
		// stylesheet without xpath-default-namespace means.
		return "", local, true
	}
	if rt != nil && rt.sheet != nil {
		if u, found := rt.sheet.prefixes[prefix]; found {
			return u, local, true
		}
	}
	return "", local, false
}

// schemaHasType reports whether xsl:import-schema brought in a global type
// declaration of this name. The schema is a property of the stylesheet, which
// the runtime does hold — the function library simply never asked.
func (rt *runtime) schemaHasType(name xdm.QName) bool {
	if rt == nil || rt.sheet == nil || rt.sheet.schema == nil {
		return false
	}
	_, ok := rt.sheet.schema.Types[name]
	return ok
}

// defaultElementNS is the type-name resolver used where no stylesheet is in
// scope. Unprefixed means no namespace; the conventional prefixes are all it
// otherwise knows.
func defaultElementNS(name string) (uri, local string, ok bool) {
	prefix, local := xdm.SplitQName(name)
	switch prefix {
	case "":
		return "", local, true
	case "xs", "xsd":
		return xdm.NSXS, local, true
	}
	return "", local, false
}

// prefixResolver expands the lexical QName that function-available and
// type-available are given into a namespace URI and local name.
type prefixResolver func(name string) (uri, local string, ok bool)

// defaultFunctionNS is the resolver used where no stylesheet is in scope: it
// knows only the conventional prefixes, which is enough for a use-when
// expression evaluated before the stylesheet's prefix map exists.
func defaultFunctionNS(name string) (uri, local string, ok bool) {
	prefix, local := xdm.SplitQName(name)
	switch prefix {
	case "":
		return xdm.NSFN, local, true
	case "fn":
		return xdm.NSFN, local, true
	case "xs", "xsd":
		return xdm.NSXS, local, true
	case "xsl":
		// system-property is one of the four functions a use-when expression
		// may call, and every property it can usefully name is in the XSLT
		// namespace. Without this the conventional prefix does not resolve
		// there and every such call became XTDE1390.
		return xdm.NSXSL, local, true
	}
	return "", local, false
}

// builtinNonAtomicTypes are the built-in schema types fn:type-available must
// report even though no atomic value can carry them.
var builtinNonAtomicTypes = map[string]bool{
	"anyType":  true,
	"untyped":  true,
	"ENTITIES": true,
	"IDREFS":   true,
	"NMTOKENS": true,
}

// inScopeBaseURI returns the base URI in force at a node.
//
// Only an element carrying xml:base has a base URI of its own; every other
// node -- an attribute above all, which is how a stylesheet usually names a
// document -- takes the one in force at its parent. This mirrors
// inheritedBaseURI in xpath/fn_node.go, which is unexported; duplicating four
// lines is preferable to widening that package's API for one caller.
func inScopeBaseURI(n *xdm.Node) string {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.BaseURI != "" {
			return cur.BaseURI
		}
	}
	return ""
}

// compatKeyValue gives the xs:untypedAtomic form of a key value sought under
// XSLT 1.0 backwards compatibility, or the value unchanged when it already is
// one. fnKey looks it up alongside the typed form; see there for why both.
func compatKeyValue(a *xdm.Atomic) *xdm.Atomic {
	if a.Type == xdm.TypeUntypedAtomic || a.IsNaN() {
		return a
	}
	return xdm.NewUntypedAtomic(a.String())
}
