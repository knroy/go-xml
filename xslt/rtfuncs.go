package xslt

import (
	"fmt"
	"net/url"
	"strconv"
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

// registerRuntimeFuncs adds the functions that need transform state.
func registerRuntimeFuncs(l *xpath.Library, rt *runtime) {
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
			if seq, ok := ctx.LookupVar(currentVar); ok {
				return seq, nil
			}
			// Outside any instruction (a bare XPath evaluation) the context
			// item is the only sensible answer.
			if ctx.Item == nil {
				return xdm.Empty, nil
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
			var out xdm.Sequence
			for _, it := range xdm.Atomize(args[0]) {
				uri := it.(*xdm.Atomic).String()
				// The zero-length URI names the document containing the
				// expression, which for a stylesheet is the stylesheet
				// itself. It is how a stylesheet carrying its own lookup
				// tables as literal data reads them, and it needs no
				// resolver because nothing is fetched.
				if strings.TrimSpace(uri) == "" && rt.sheet.source != nil {
					out = append(out, rt.sheet.source)
					continue
				}
				base := ""
				if n, ok := ctx.Item.(*xdm.Node); ok {
					base = n.BaseURI
				}
				if base == "" {
					// No context node, or one with no base of its own: the
					// static base URI is the stylesheet's, which is what a
					// relative reference in a stylesheet means. Without this
					// it resolves against the process's working directory.
					base = ctx.StaticBaseURI
				}
				tree, err := ctx.Docs.ResolveDocument(uri, base)
				if err != nil {
					return nil, fmt.Errorf("FODC0002: cannot retrieve %q: %w", uri, err)
				}
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
	}

	// The static functions are registered separately so that use-when, whose
	// context has no runtime at all, can have exactly these and nothing else.
	registerStaticFuncs(l)
}

// registerStaticFuncs adds the four functions section 3.12 makes available to
// a use-when expression: they answer questions about the *processor* rather
// than about the stylesheet or the source, so they need no runtime and are
// legal in a context that has none.
func registerStaticFuncs(l *xpath.Library) {
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "system-property"}, Arity: 1,
		Call: func(_ *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
			// XTDE1390: the argument must be a valid QName. A malformed one
			// would otherwise fall through to the empty string, which is
			// what a *valid* name for an unknown property returns — so the
			// two cases would be indistinguishable.
			if !isLexicalQName(name) {
				return nil, fmt.Errorf(
					"XTDE1390: system-property(%q) is not a valid QName", name)
			}
			// The other half of XTDE1390 — "there is no namespace declaration
			// in scope for the prefix of the QName" — is deliberately not
			// checked here. This library does not receive the stylesheet's
			// namespace context, and guessing at the prefix does not work:
			// a stylesheet binds the XSLT namespace to whatever prefix it
			// likes, and the suite uses "t:" and "xslt:" as often as "xsl:".
			// Rejecting the unfamiliar ones lost thirteen tests that were
			// asking a perfectly well-formed question.
			switch {
			case strings.HasSuffix(name, "version"):
				return xdm.One(xdm.NewString("2.0")), nil
			case strings.HasSuffix(name, "vendor"):
				return xdm.One(xdm.NewString("go-xml")), nil
			case strings.HasSuffix(name, "vendor-url"):
				return xdm.One(xdm.NewString("https://github.com/knroy/go-xml")), nil
			case strings.HasSuffix(name, "product-name"):
				return xdm.One(xdm.NewString("go-xml")), nil
			case strings.HasSuffix(name, "product-version"):
				return xdm.One(xdm.NewString("0.1")), nil
			}
			return xdm.One(xdm.NewString("")), nil
		},
	})

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "function-available"}, Arity: 1,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
			if err := checkAvailableArg("XTDE1400", "function-available", name); err != nil {
				return nil, err
			}
			prefix, local := xdm.SplitQName(name)
			uri := xdm.NSFN
			if prefix != "" {
				// Without the stylesheet's namespace context here, only the
				// standard prefixes can be checked.
				switch prefix {
				case "fn":
					uri = xdm.NSFN
				case "xs":
					uri = xdm.NSXS
				default:
					return xdm.One(xdm.NewBoolean(false)), nil
				}
			}
			for arity := 0; arity <= 4; arity++ {
				if _, ok := ctx.Funcs.Lookup(xdm.QName{URI: uri, Local: local}, arity); ok {
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
			if err := checkAvailableArg("XTDE1400", "function-available", name); err != nil {
				return nil, err
			}
			arity := 0
			for _, a := range xdm.Atomize(args[1]) {
				if at, ok := a.(*xdm.Atomic); ok {
					arity = int(at.Int64())
				}
			}
			prefix, local := xdm.SplitQName(name)
			uri := xdm.NSFN
			if prefix != "" {
				switch prefix {
				case "fn":
					uri = xdm.NSFN
				case "xs":
					uri = xdm.NSXS
				default:
					return xdm.One(xdm.NewBoolean(false)), nil
				}
			}
			_, ok := ctx.Funcs.Lookup(xdm.QName{URI: uri, Local: local}, arity)
			return xdm.One(xdm.NewBoolean(ok)), nil
		},
	})

	// fn:type-available asks whether a type is in the static context. Only
	// the built-in xs: types can be answered here: the imported schema is a
	// property of the stylesheet, which this library does not see, so a
	// user-defined name answers false rather than guessing.
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "type-available"}, Arity: 1,
		Call: func(_ *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
			prefix, local := xdm.SplitQName(name)
			if prefix != "" && prefix != "xs" && prefix != "xsd" {
				return xdm.One(xdm.NewBoolean(false)), nil
			}
			_, ok := xpath.BuiltinAtomicTypeCode(local)
			return xdm.One(xdm.NewBoolean(ok)), nil
		},
	})

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "element-available"}, Arity: 1,
		Call: func(_ *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
			if err := checkAvailableArg("XTDE1440", "element-available", name); err != nil {
				return nil, err
			}
			_, local := xdm.SplitQName(name)
			return xdm.One(xdm.NewBoolean(supportedInstructions[local])), nil
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
}

// generateID returns a stable identifier for a node.
//
// It must be constant for a node within a transform and distinct between
// nodes. Document order plus tree identity satisfies both, and unlike a
// pointer-derived value it is reproducible across runs, which keeps output
// diffable.
func generateID(it xdm.Item) (xdm.Sequence, error) {
	n, ok := it.(*xdm.Node)
	if !ok {
		return xdm.One(xdm.NewString("")), nil
	}
	return xdm.One(xdm.NewString("N" + strconv.Itoa(n.Order()))), nil
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
		return xdm.Empty, nil
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
		uri = rt.sheet.prefixes[prefix]
	}
	keyName := xdm.QName{URI: uri, Local: local}.Clark()
	defs, ok := rt.sheet.keys[keyName]
	if !ok {
		return nil, fmt.Errorf("XTDE1260: no xsl:key named %q", lexName)
	}

	// The third argument, when present, names the document to search; without
	// it the search covers the tree containing the context node.
	var root *xdm.Node
	if len(args) > 2 && len(args[2]) > 0 {
		if n, isNode := args[2][0].(*xdm.Node); isNode {
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
		k, err := rt.keySearchKey(kv.(*xdm.Atomic), coll)
		if err != nil {
			return nil, err
		}
		out = append(out, index[k]...)
	}
	return xdm.SortDocumentOrder(out), nil
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
		vals, err := def.use.Eval(ctx.WithFocus(n, 1, 1))
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
			seq, _ := ctx.LookupVar(currentGroupVar)
			return seq, nil
		},
	})
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "current-grouping-key"}, Arity: 0,
		Call: func(ctx *xpath.Context, _ []xdm.Sequence) (xdm.Sequence, error) {
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
	return b.ResolveReference(r).String()
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
func checkAvailableArg(code, fn, name string) error {
	if isLexicalQName(name) {
		return nil
	}
	return fmt.Errorf("%s: %s(%q) is not a valid QName", code, fn, name)
}
