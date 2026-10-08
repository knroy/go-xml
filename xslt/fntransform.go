package xslt

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// fn:transform runs a transformation described by an options map and returns
// its results as a map, F&O 3.1 section 14.7.1.
//
// It lives in xslt rather than in xpath because it needs an XSLT processor,
// and xpath does not depend on xslt -- the layering is one-directional and
// stays that way. xpath registers a stub that calls the processor this
// package's init registers with it, for a caller with no transform of its own,
// and raises FOXT0004 in a program that does not link xslt; this overrides it
// for the duration of a transform, exactly as key() and current() are bound
// per transform by registerRuntimeFuncs.
//
// The nested transform inherits the outer one's resolvers. A stylesheet that
// could reach documents through fn:transform that it could not reach through
// fn:doc would be a hole in the sandbox rather than a feature, so
// stylesheet-location and the source it names resolve through the same
// (possibly nil) resolvers the caller supplied.
func registerTransformFunc(l *xpath.Library, rt *runtime) {
	l.Add(xpath.Function{
		Name:  xdm.QName{URI: xdm.NSFN, Local: "transform"},
		Arity: 1,
		Since: xpath.XPath31,
		Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			it, err := args[0].Single()
			if err != nil {
				return nil, err
			}
			opts, ok := it.(*xdm.MapItem)
			if !ok {
				return nil, xdm.ErrType(
					"fn:transform: the options must be a map, got %s", it.TypeName())
			}
			return runNestedTransform(ctx, callerOf(rt), opts)
		},
	})
}

// transformCaller is what a nested transform inherits from whoever called
// fn:transform: the resolvers and settings in opts, the package resolver, the
// recursion count and its bound, the deadline, and whether the call is made
// from inside a Compile that holds compileMu. The body of fn:transform reads
// these and nothing else, so a stylesheet and a caller with no stylesheet at
// all -- an XQuery query, a bare xpath.Eval -- run the one implementation.
type transformCaller struct {
	opts            TransformOptions
	pkgs            PackageResolver
	depth, maxDepth int
	goCtx           context.Context
	static          bool
	// cache is the outer stylesheet's compiled-stylesheet cache, or nil for
	// a caller with no stylesheet and for the static phase.
	cache *nestedCache
}

// callerOf is the caller a running (or static-phase) stylesheet makes.
func callerOf(rt *runtime) transformCaller {
	c := transformCaller{
		opts: rt.opts, pkgs: rt.sheet.pkgResolver,
		depth: rt.depth, maxDepth: rt.maxDepth,
		goCtx: rt.goCtx, static: rt.static,
	}
	if !rt.static {
		c.cache = &rt.sheet.nested
	}
	return c
}

// fn:transform for a caller with no transformation of its own. xpath cannot
// import this package, so it calls whatever processor is registered with it
// (xpath/processors.go), and importing xslt registers this one. Inside a
// stylesheet the per-transform function registerTransformFunc binds wins over
// it, as it always did.
//
// Everything the nested transform inherits comes from the caller's Context,
// on the sandbox rule registerTransformFunc states: stylesheet-location,
// source-location and the nested stylesheet's own fn:doc resolve through
// ctx.Docs and nothing else, so a query with no resolver gets the FOXT0002
// refusal a stylesheet with none gets. There is no package resolver, so
// package-name is refused too. The recursion count starts at the call's own
// depth and the bound is the caller's, so a query whose stylesheet calls back
// into a query that calls fn:transform again is charged one level per hop.
func init() {
	xpath.RegisterTransformProcessor(func(ctx *xpath.Context, opts *xdm.MapItem) (xdm.Sequence, error) {
		maxDepth := ctx.MaxDepth
		if maxDepth == 0 {
			maxDepth = xpath.MaxDepth
		}
		c := transformCaller{
			opts: TransformOptions{
				Documents:        ctx.Docs,
				Collections:      ctx.Collections,
				Texts:            ctx.Texts,
				Environment:      ctx.Environment,
				MaxDepth:         maxDepth,
				ImplicitTimezone: ctx.ImplicitTimezone,
			},
			maxDepth: maxDepth,
			goCtx:    ctx.Ctx,
		}
		if ctx.HasNow {
			c.opts.Now = ctx.Now
		}
		return runNestedTransform(ctx, c, opts)
	})
}

// transformOption reads one entry of the options map by its string key.
func transformOption(m *xdm.MapItem, name string) (xdm.Sequence, bool) {
	seq, ok, err := m.Get(xdm.NewString(name))
	if err != nil || !ok {
		return nil, false
	}
	return seq, true
}

// transformHasAny reports whether any of the named options is present.
func transformHasAny(m *xdm.MapItem, names ...string) bool {
	for _, n := range names {
		if _, ok := transformOption(m, n); ok {
			return true
		}
	}
	return false
}

// transformString reads a string-valued option.
func transformString(m *xdm.MapItem, name string) (string, bool, error) {
	seq, ok := transformOption(m, name)
	if !ok {
		return "", false, nil
	}
	it, err := seq.Single()
	if err != nil {
		return "", true, xdm.ErrType(
			"fn:transform: %s must be a single value", name)
	}
	// A node is atomized rather than refused. An option written as element
	// content -- <xsl:map-entry key="'stylesheet-location'">a.xsl</xsl:map-entry>
	// -- arrives as a text node, not a string, and the value it stands for is
	// its string value. Refusing it raised XPTY0004 on a map that says exactly
	// what a string-valued one says.
	if n, ok := it.(*xdm.Node); ok {
		return n.StringValue(), true, nil
	}
	a, ok := it.(*xdm.Atomic)
	if !ok {
		return "", true, xdm.ErrType(
			"fn:transform: %s must be a string, got %s", name, it.TypeName())
	}
	return a.String(), true, nil
}

// transformBool reads an xs:boolean option, def when it is absent. The option
// parameter conventions (F&O 3.1 1.5.4) convert the value with the function
// conversion rules: a node atomizes to untypedAtomic, which is cast (FORG0001
// when it is not a boolean lexical form); any other type, or other than one
// item, is XPTY0004. That is the code fn-transform-err-4 and err-5 expect for
// mistyped options, rather than FOXT0002.
func transformBool(m *xdm.MapItem, name string, def bool) (bool, error) {
	seq, ok := transformOption(m, name)
	if !ok {
		return def, nil
	}
	it, err := seq.Single()
	if err != nil {
		return false, xdm.ErrType("fn:transform: %s must be a single xs:boolean", name)
	}
	var a *xdm.Atomic
	switch v := it.(type) {
	case *xdm.Node:
		a = xdm.NewUntypedAtomic(v.StringValue())
	case *xdm.Atomic:
		a = v
	default:
		return false, xdm.ErrType(
			"fn:transform: %s must be an xs:boolean, got %s", name, it.TypeName())
	}
	if a.Type == xdm.TypeUntypedAtomic {
		if a, err = xpath.CastAtomic(a, xdm.TypeBoolean); err != nil {
			return false, err
		}
	}
	if a.Type != xdm.TypeBoolean {
		return false, xdm.ErrType(
			"fn:transform: %s must be an xs:boolean, got %s", name, a.TypeName())
	}
	return a.String() == "true", nil
}

// transformQName reads a QName-valued option.
//
// The value must be read structurally rather than through String(), which
// gives a QName's LEXICAL form and so drops the namespace URI: reading
// QName('http://example.com/mf','evaluate') as a string yields "evaluate" and
// would look up a function in no namespace. A string is still accepted, since
// the option is commonly written as one, and then names a function in no
// namespace -- there is no prefix context in an options map to resolve
// against.
func transformQName(m *xdm.MapItem, name string) (xdm.QName, bool, error) {
	seq, ok := transformOption(m, name)
	if !ok {
		return xdm.QName{}, false, nil
	}
	it, err := seq.Single()
	if err != nil {
		return xdm.QName{}, true, xdm.ErrType(
			"fn:transform: %s must be a single value", name)
	}
	a, ok := it.(*xdm.Atomic)
	if !ok {
		return xdm.QName{}, true, xdm.ErrType(
			"fn:transform: %s must be a QName or string, got %s",
			name, it.TypeName())
	}
	if q := a.QName(); q != nil {
		return *q, true, nil
	}
	return xdm.QName{Local: a.String()}, true, nil
}

// transformArray reads an array-valued option as the ordered argument list it
// stands for -- function-params is the only one.
func transformArray(m *xdm.MapItem, name string) ([]xdm.Sequence, bool, error) {
	seq, ok := transformOption(m, name)
	if !ok {
		return nil, false, nil
	}
	it, err := seq.Single()
	if err != nil {
		return nil, true, xdm.ErrType(
			"fn:transform: %s must be a single array", name)
	}
	arr, ok := it.(*xdm.ArrayItem)
	if !ok {
		return nil, true, xdm.ErrType(
			"fn:transform: %s must be an array, got %s", name, it.TypeName())
	}
	out := make([]xdm.Sequence, 0, arr.Len())
	for i := 1; i <= arr.Len(); i++ {
		mem, merr := arr.Member(i)
		if merr != nil {
			return nil, true, merr
		}
		out = append(out, mem)
	}
	return out, true, nil
}

// transformParams reads a map-valued option -- stylesheet-params and its
// siblings -- as the name-keyed bindings a transform takes.
//
// A key is a QName in the data model, and the bindings the engine takes are
// keyed by Clark name, so an unprefixed name and one in a namespace both
// arrive in the form the runtime looks them up by. F&O 3.1 types each of these
// options map(xs:QName, item()*), and coercion does not convert map keys, so
// a string key is FOXT0002 (fn-transform-err-18), as other processors report.
func transformParams(m *xdm.MapItem, name string) (map[string]xdm.Sequence, error) {
	seq, ok := transformOption(m, name)
	if !ok {
		return nil, nil
	}
	it, err := seq.Single()
	if err != nil {
		return nil, xdm.ErrType("fn:transform: %s must be a single map", name)
	}
	sub, ok := it.(*xdm.MapItem)
	if !ok {
		return nil, xdm.ErrType(
			"fn:transform: %s must be a map, got %s", name, it.TypeName())
	}
	out := map[string]xdm.Sequence{}
	err = sub.Entries(func(key *xdm.Atomic, value xdm.Sequence) error {
		q := key.QName()
		if q == nil {
			return xdm.Errorf("FOXT0002",
				"fn:transform: %s keys must be xs:QName, got %s %q",
				name, key.TypeName(), key.String())
		}
		out[q.Clark()] = value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// checkRequestedProperties applies requested-properties (F&O 3.1 14.7.1). A
// key is a name fn:system-property answers for and its value the setting
// asked for, true() and false() standing for yes and no. A request is met
// when it equals what this processor reports; a name system-property does not
// define reports "", as system-property itself does. F&O leaves an unmet
// request implementation-defined, and with no other processor to offer it is
// FOXT0001, "no suitable XSLT processor is available". xsl:version is skipped,
// as F&O directs.
func checkRequestedProperties(opts *xdm.MapItem) error {
	seq, ok := transformOption(opts, "requested-properties")
	if !ok {
		return nil
	}
	it, err := seq.Single()
	if err != nil {
		return xdm.ErrType("fn:transform: requested-properties must be a single map")
	}
	m, ok := it.(*xdm.MapItem)
	if !ok {
		return xdm.ErrType(
			"fn:transform: requested-properties must be a map, got %s", it.TypeName())
	}
	return m.Entries(func(key *xdm.Atomic, value xdm.Sequence) error {
		q := key.QName()
		if q == nil {
			return xdm.ErrType(
				"fn:transform: requested-properties key %q is not a QName", key.String())
		}
		if q.URI == xdm.NSXSL && q.Local == "version" {
			return nil
		}
		v, verr := value.Single()
		a, ok := v.(*xdm.Atomic)
		if verr != nil || !ok {
			return xdm.ErrType(
				"fn:transform: requested property %s must be a single atomic value", q.Clark())
		}
		want := a.String()
		if a.Type == xdm.TypeBoolean {
			want = "no"
			if a.Bool() {
				want = "yes"
			}
		}
		have := ""
		if q.URI == xdm.NSXSL {
			have = systemProperties[q.Local]
		}
		if want != have {
			return xdm.Errorf("FOXT0001",
				"fn:transform: requested property %s = %q cannot be met "+
					"(this processor reports %q)", q.Clark(), want, have)
		}
		return nil
	})
}

// runNestedTransform is fn:transform's body.
//
// Option names F&O does not define are ignored. F&O 3.1 says the option
// parameter conventions apply to this map, and they say: "It is not an error
// if the options map contains options with names other than those described
// in this specification. ... Implementations must ignore such entries unless
// they have a specific implementation-defined meaning." F&O 4.0 repeats it
// per XSLT version: "if anything else is present, it is ignored". So a
// misspelled key, a QName-named vendor option and an option from a later
// edition all fall through without a word.
//
// Unknown names were refused as FOXT0002 for a while, after post-process was
// found accepted and never run: the output looked right, one transformation
// short. That lesson stands, but it is about the options F&O defines -- one
// the spec names must be implemented or refused, not read and dropped -- and
// a name check never enforced it, since every spec name passes it. What it
// did do was refuse a conformant caller's extension keys, Saxon's
// source-location among them, which F&O 4.0 has since standardised and which
// is read below. The spec-defined options still accepted without effect are
// listed in docs/known-gaps.md.
func runNestedTransform(ctx *xpath.Context, rt transformCaller, opts *xdm.MapItem) (xdm.Sequence, error) {
	// One level of nesting is charged before the nested stylesheet is even
	// loaded, so that the refusal happens without adding another frame. The
	// depth is taken from the CALL rather than from rt, for the same reason
	// xpath/funcitem.go takes it from the caller and not the closure: rt is
	// the runtime the stylesheet was entered with, and for a stylesheet that
	// transforms itself that is the same shallow depth every time round.
	// ctx.Depth is the depth of the fn:transform call actually being made.
	depth := rt.depth
	if ctx.Depth > depth {
		depth = ctx.Depth
	}
	depth++
	if rt.maxDepth > 0 && depth > rt.maxDepth {
		// Worded and coded like the depth refusals it sits beside --
		// xpath.Context.Descend's XPDY0001, with xdm.ErrResourceLimit added
		// so a caller can tell a refusal to compute from a bad stylesheet.
		// It must be an ordinary error: the condition it replaces was a Go
		// stack overflow, which is a runtime fatal that recover() cannot
		// catch and that takes the host process with it.
		return nil, fmt.Errorf(
			"XPDY0001: fn:transform nesting exceeded %d levels: %w",
			rt.maxDepth, xdm.ErrResourceLimit)
	}

	// xslt-version selects the processor, and a 2.0 one ignores the options
	// 3.0 added; see transformXSLTVersion.
	maxVersion, err := transformXSLTVersion(opts)
	if err != nil {
		return nil, err
	}
	if maxVersion == 2.0 {
		if opts, err = optionsFor20(opts); err != nil {
			return nil, err
		}
	}

	// F&O 3.1 lists the invocation methods as "exactly one of the following
	// combinations", and initial-mode belongs only to apply-templates; FOXT0002
	// is its code for "two mutually-exclusive parameters".
	if _, ok := transformOption(opts, "initial-mode"); ok {
		for _, other := range []string{"initial-template", "initial-function"} {
			if _, ok := transformOption(opts, other); ok {
				return nil, xdm.Errorf("FOXT0002",
					"fn:transform: initial-mode and %s are mutually exclusive", other)
			}
		}
	}
	if _, ok := transformOption(opts, "source-node"); ok {
		if _, ok := transformOption(opts, "initial-match-selection"); ok {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: source-node and initial-match-selection "+
					"are mutually exclusive")
		}
	}

	if err := checkRequestedProperties(opts); err != nil {
		return nil, err
	}

	useCache, err := transformBool(opts, "cache", true)
	if err != nil {
		return nil, err
	}

	sheet, err := cachedNestedStylesheet(ctx, rt, opts, useCache)
	if err != nil {
		return nil, err
	}

	var source, sourceNode *xdm.Node
	if seq, ok := transformOption(opts, "source-node"); ok {
		it, serr := seq.Single()
		if serr != nil {
			return nil, xdm.ErrType("fn:transform: source-node must be a single node")
		}
		n, ok := it.(*xdm.Node)
		if !ok {
			return nil, xdm.ErrType(
				"fn:transform: source-node must be a node, got %s", it.TypeName())
		}
		source, sourceNode = n, n
	}
	if loc, ok, lerr := transformString(opts, "source-location"); lerr != nil {
		return nil, lerr
	} else if ok {
		doc, derr := transformSourceLocation(ctx, rt, opts, source, loc)
		if derr != nil {
			return nil, derr
		}
		source = doc
	}

	topts := rt.opts
	// The nested runtime picks up where this one left off instead of
	// restarting at zero. See TransformOptions.nestedDepth.
	topts.nestedDepth = depth
	// The nested transformation spends this call's remaining item and byte
	// allowances rather than a fresh pair, on the same policy. See
	// TransformOptions.nestedBudget.
	topts.nestedBudget = ctx
	// The nested transform is a transformation of its own: the outer one's
	// entry point, its parameters and its initial mode say nothing about it.
	// Only what the options map states, plus the resolvers, carries over.
	topts.Params = nil
	topts.InitialTemplate = ""
	topts.InitialTemplateURI = ""
	topts.InitialFunction = xdm.QName{}
	topts.InitialFunctionParams = nil
	topts.InitialMode = ""
	topts.InitialMatchSelection = nil
	topts.InitialTemplateParams = nil
	topts.InitialTemplateTunnelParams = nil
	topts.InitialModeParams = nil
	topts.InitialModeTunnelParams = nil

	if p, perr := transformParams(opts, "stylesheet-params"); perr != nil {
		return nil, perr
	} else if p != nil {
		topts.Params = p
	}
	if p, perr := transformParams(opts, "template-params"); perr != nil {
		return nil, perr
	} else if p != nil {
		topts.InitialTemplateParams = p
	}
	if p, perr := transformParams(opts, "tunnel-params"); perr != nil {
		return nil, perr
	} else if p != nil {
		topts.InitialTemplateTunnelParams = p
	}
	// initial-template is a QName, read structurally for the reason
	// transformQName gives: a template in a namespace is otherwise looked up
	// by its local name alone. fn-transform-2 names app:main this way.
	if q, ok, verr := transformQName(opts, "initial-template"); verr != nil {
		return nil, verr
	} else if ok {
		topts.InitialTemplate, topts.InitialTemplateURI = q.Local, q.URI
	} else if !transformHasAny(opts, "initial-function", "initial-mode",
		"source-node", "source-location", "initial-match-selection") {
		// F&O 3.1's invocation rule: with none of these the method is
		// call-template on xsl:initial-template, so a stylesheet without one
		// is XTDE0040 (fn-transform-err-1), not the XTDE0044 of an
		// apply-templates that has nothing to select.
		topts.InitialTemplate, topts.InitialTemplateURI = "initial-template", xdm.NSXSL
	}
	// initial-function and function-params are read as a pair. 2.3.5 infers
	// the arity from "the length of the parameter list", so the arguments are
	// not merely values but half the entry point's identity. F&O 3.1 lists
	// both as required for call-function invocation, so one without the
	// other is FOXT0002 (fn-transform-err-16): defaulting an absent list to
	// empty would quietly call the nullary function instead.
	if fname, ok, ferr := transformQName(opts, "initial-function"); ferr != nil {
		return nil, ferr
	} else if ok {
		topts.InitialFunction = fname
		args, present, aerr := transformArray(opts, "function-params")
		if aerr != nil {
			return nil, aerr
		}
		if !present {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: initial-function was supplied without function-params")
		}
		topts.InitialFunctionParams = args
	} else if _, present, aerr := transformArray(opts, "function-params"); aerr != nil {
		return nil, aerr
	} else if present {
		// function-params without initial-function names arguments for no
		// function. FOXT0002 is the code for options that do not identify a
		// coherent invocation, which is what err-2 and err-3 use it for.
		return nil, xdm.Errorf("FOXT0002",
			"fn:transform: function-params was supplied without "+
				"initial-function, so it names the arguments of no function")
	}
	if v, ok, verr := transformString(opts, "initial-mode"); verr != nil {
		return nil, verr
	} else if ok {
		topts.InitialMode = v
	}
	if seq, ok := transformOption(opts, "initial-match-selection"); ok {
		topts.InitialMatchSelection = seq
	}
	if v, ok, verr := transformString(opts, "base-output-uri"); verr != nil {
		return nil, verr
	} else if ok {
		topts.BaseOutputURI = v
	}

	// F&O 3.1: with source-node "the global-context-item ... is the root of
	// the tree containing the supplied node", and for apply-templates "the
	// source-node acts as the initial-match-selection". The two differ only
	// for a node that is not a root, which fn-transform-82b passes.
	if sourceNode != nil && sourceNode.Root() != sourceNode {
		source = sourceNode.Root()
		if topts.InitialTemplate == "" && topts.InitialFunction.Local == "" {
			topts.InitialMatchSelection = xdm.Sequence{sourceNode}
		}
	}
	// global-context-item replaces the global context item source-node and
	// source-location supply, while what they select stays the initial match
	// selection (fn-transform-82c). Given alone, it is option (a) for
	// starting at xsl:initial-template "while also supplying a context item
	// for use when evaluating global variables".
	if seq, ok := transformOption(opts, "global-context-item"); ok {
		it, gerr := seq.Single()
		if gerr != nil {
			return nil, xdm.ErrType(
				"fn:transform: global-context-item must be a single item")
		}
		// Transform takes the global context item as its source document,
		// so an atomic or function item has nowhere to go.
		n, ok := it.(*xdm.Node)
		if !ok {
			return nil, xdm.Errorf("FOXT0001",
				"fn:transform: a global-context-item that is not a node "+
					"(%s) is not supported", it.TypeName())
		}
		if topts.InitialTemplate == "" && topts.InitialFunction.Local == "" &&
			topts.InitialMatchSelection == nil {
			if source != nil {
				topts.InitialMatchSelection = xdm.Sequence{source}
			} else if topts.InitialMode == "" {
				topts.InitialTemplate, topts.InitialTemplateURI =
					"initial-template", xdm.NSXSL
			}
		}
		source = n
	}

	res, err := sheet.Transform(rt.goCtx, source, topts)
	if err != nil {
		return nil, annotateNested(err, opts)
	}
	return transformResultMap(ctx, opts, res)
}

// transformSourceLocation loads the document the source-location option
// names. Saxon has read it since 9.8; F&O 4.0 standardises it: "If relative,
// it is resolved against the static base URI of the fn:transform function
// call. The document at this location is parsed, and the document node acts
// as the initial-match-selection". It is used exactly where source-node would
// be, so it is also the global context item.
//
// It resolves through the caller's document resolver, the one fn:doc uses,
// for the reason the sandbox comment on registerTransformFunc gives: no
// resolver refuses it, and so does a location outside the resolver's roots.
// The refusal is FOXT0002, as it is for stylesheet-location. The wrappers the
// outer Transform installed are peeled off first -- the outer stylesheet's
// xsl:strip-space is not the nested one's, and the nested Transform strips
// the source by its own declarations.
//
// The document is parsed whole. F&O 4.0 allows streaming it when the initial
// mode is streamable; fn:transform here never streams.
func transformSourceLocation(
	ctx *xpath.Context, rt transformCaller, opts *xdm.MapItem, source *xdm.Node, loc string,
) (*xdm.Node, error) {
	// F&O 4.0 asks for "exactly one of source-node, source-location, or
	// initial-match-selection".
	if source != nil {
		return nil, xdm.Errorf("FOXT0002",
			"fn:transform: source-location and source-node are mutually exclusive")
	}
	if _, ok := transformOption(opts, "initial-match-selection"); ok {
		return nil, xdm.Errorf("FOXT0002",
			"fn:transform: source-location and initial-match-selection "+
				"are mutually exclusive")
	}
	docs := callerDocuments(rt.opts.Documents)
	if docs == nil {
		return nil, xdm.Errorf("FOXT0002",
			"fn:transform: document access is disabled "+
				"(no resolver configured): source-location %q", loc)
	}
	var tree *xdm.Tree
	var err error
	if cr, ok := docs.(xpath.ContextDocumentResolver); ok {
		// Charged to the calling evaluation's entity allowance, as fn:doc is.
		tree, err = cr.ResolveDocumentIn(ctx, loc, ctx.StaticBaseURI)
	} else {
		tree, err = docs.ResolveDocument(loc, ctx.StaticBaseURI)
	}
	if err != nil {
		return nil, xdm.Errorf("FOXT0002",
			"fn:transform: cannot retrieve source-location %q: %v", loc, err)
	}
	return tree.Root, nil
}

// annotateNested names the nested stylesheet in an entry-point error.
//
// XTDE0044 raised inside fn:transform reads "no initial match selection and no
// source document", worded for a caller who invoked the processor directly. On
// this path that is actively misleading: the stylesheet without an entry point
// is the one fn:transform just loaded, not the one the author is looking at,
// and the report sent a user hunting through an outer stylesheet whose
// xsl:initial-template was present and correct all along. See issue #4.
//
// Only the entry-point codes are annotated, and only by appending the
// identity: the message and the code are otherwise left exactly as the engine
// wrote them, so error-code matching in the suites is unaffected. Wrapping
// with %w keeps xdm.ErrorCode able to read the code through the wrapper.
func annotateNested(err error, opts *xdm.MapItem) error {
	switch xdm.ErrorCode(err) {
	case "XTDE0044", "XTDE0040", "XTDE0041", "XTDE0045":
	default:
		return err
	}
	return fmt.Errorf("%w (in the stylesheet invoked by fn:transform: %s)",
		err, nestedStylesheetLabel(opts))
}

// nestedStylesheetLabel describes which stylesheet fn:transform was running,
// in whatever terms the options made available.
func nestedStylesheetLabel(opts *xdm.MapItem) string {
	if loc, ok, err := transformString(opts, "stylesheet-location"); err == nil && ok {
		return fmt.Sprintf("stylesheet-location %q", loc)
	}
	if _, ok := transformOption(opts, "stylesheet-node"); ok {
		return "the stylesheet supplied as stylesheet-node"
	}
	if _, ok := transformOption(opts, "stylesheet-text"); ok {
		return "the stylesheet supplied as stylesheet-text"
	}
	return "an unidentified stylesheet"
}

// nestedStylesheet compiles the stylesheet the options name.
//
// The three spellings are mutually exclusive and one is required: FOXT0002 is
// "the supplied options do not identify a stylesheet", which covers naming
// none of them and naming more than one.
func nestedStylesheet(ctx *xpath.Context, rt transformCaller, opts *xdm.MapItem) (*Stylesheet, error) {
	base, _, err := transformString(opts, "stylesheet-base-uri")
	if err != nil {
		return nil, err
	}
	// A relative stylesheet-base-uri resolves against the static base URI of
	// the call, the rule F&O 3.1 states for base-output-uri; err-9a passes
	// "transform/include.xsl" and expects its xsl:include to resolve.
	if base != "" {
		base = resolveAgainst(ctx.StaticBaseURI, base)
	}
	var named []string
	for _, k := range []string{"stylesheet-location", "stylesheet-node", "stylesheet-text", "package-name"} {
		if _, ok := transformOption(opts, k); ok {
			named = append(named, k)
		}
	}
	if len(named) > 1 {
		return nil, xdm.Errorf("FOXT0002",
			"fn:transform: %s are mutually exclusive", strings.Join(named, " and "))
	}

	// package-name names a library package by its name and version range,
	// exactly as xsl:use-package does, rather than locating a file. It is a
	// fourth spelling of "which stylesheet", so it is tried alongside the
	// other three, and it resolves through the same PackageResolver the
	// caller gave the outer compilation -- a nested transform that could
	// reach packages the outer one could not would be a hole in the sandbox.
	if name, ok, nerr := transformString(opts, "package-name"); nerr != nil {
		return nil, nerr
	} else if ok {
		// An absent package-version means "any version", which is the same
		// default xsl:use-package applies when it states no version.
		vers, _, verr := transformString(opts, "package-version")
		if verr != nil {
			return nil, verr
		}
		if vers == "" {
			vers = "*"
		}
		if rt.pkgs == nil {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: package-name %q cannot be resolved "+
					"(no package resolver configured)", name)
		}
		root, perr := rt.pkgs.ResolvePackage(name, vers)
		if perr != nil {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: cannot retrieve package %q version %q: %v",
				name, vers, perr)
		}
		return compileNested(ctx, rt, opts, root, base)
	}

	if seq, ok := transformOption(opts, "stylesheet-node"); ok {
		it, serr := seq.Single()
		if serr != nil {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: stylesheet-node must be a single node")
		}
		n, ok := it.(*xdm.Node)
		if !ok {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: stylesheet-node must be a node")
		}
		return compileNested(ctx, rt, opts, n, base)
	}

	if text, ok, terr := transformString(opts, "stylesheet-text"); terr != nil {
		return nil, terr
	} else if ok {
		tree, perr := xdm.ParseString(text, nestedParseOptions(rt, base))
		if perr != nil {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: parsing stylesheet-text: %v", perr)
		}
		return compileNested(ctx, rt, opts, tree.Root, base)
	}

	if loc, ok, lerr := transformString(opts, "stylesheet-location"); lerr != nil {
		return nil, lerr
	} else if ok {
		if rt.opts.Documents == nil {
			// A stylesheet-location that cannot be read identifies no
			// stylesheet, which is FOXT0002 rather than FOXT0001. FOXT0001 is
			// the code for a transformation this processor cannot RUN -- the
			// QT3 cases raise it only for an unavailable vendor named in
			// requested-properties, never for a file that is not there.
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: document access is disabled "+
					"(no resolver configured): %q", loc)
		}
		if base == "" {
			base = ctx.StaticBaseURI
		}
		// ResolveModule is preferred over ResolveDocument because it hands
		// back the URI it resolved to as well as the tree, and the nested
		// stylesheet's own xsl:import and xsl:include resolve against that.
		// Passing the relative location through as the base left "../x.xsl"
		// inside the nested module with nothing to resolve against.
		if mr := moduleResolverFor(rt.opts.Documents); mr != nil {
			root, abs, merr := resolveModule(
				mr, ctx.EntityBudget(), loc, base)
			if merr != nil {
				return nil, xdm.Errorf("FOXT0002",
					"fn:transform: cannot retrieve stylesheet-location %q: %v",
					loc, merr)
			}
			return compileNested(ctx, rt, opts, root, abs)
		}
		tree, derr := rt.opts.Documents.ResolveDocument(loc, base)
		if derr != nil {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: cannot retrieve stylesheet-location %q: %v", loc, derr)
		}
		return compileNested(ctx, rt, opts, tree.Root, loc)
	}

	return nil, xdm.Errorf("FOXT0002",
		"fn:transform: the options identify no stylesheet")
}

// nestedParseOptions are the parse settings a nested stylesheet is read with.
func nestedParseOptions(rt transformCaller, base string) xdm.ParseOptions {
	return xdm.ParseOptions{BaseURI: base}
}

// compileNested compiles a stylesheet for fn:transform.
//
// A static error keeps its XSLT code. F&O 3.1: "If a static or dynamic error
// is reported by the XSLT processor, this function fails with a dynamic
// error, retaining the XSLT error code" -- fn-transform-err-9 expects
// XTSE0165. Only a failure with no code of its own is reported as FOXT0002,
// as it always was.
func compileNested(
	ctx *xpath.Context, rt transformCaller, opts *xdm.MapItem, root *xdm.Node, base string,
) (*Stylesheet, error) {
	// static-params binds the nested stylesheet's static parameters, which
	// are fixed before static analysis and so belong to the compilation, not
	// to the run (F&O 3.1 14.7.1; GitHub issue #15).
	static, err := transformParams(opts, "static-params")
	if err != nil {
		return nil, err
	}
	maxVersion, err := transformXSLTVersion(opts)
	if err != nil {
		return nil, err
	}
	// A nested stylesheet may itself xsl:include or xsl:import. The caller's
	// document resolver is reused when it can also resolve modules --
	// FileResolver satisfies both interfaces -- and otherwise the nested
	// stylesheet simply cannot reach further modules, which is the same
	// refusal a nil resolver gives everywhere else.
	mr := moduleResolverFor(rt.opts.Documents)
	copts := CompileOptions{
		Resolver: mr,
		BaseURI:  base,
		// The nested stylesheet may itself use packages, and it resolves them
		// through the same resolver the outer compilation was given -- which
		// is also what lets a stylesheet loaded BY package-name be found at
		// all when it in turn names one.
		PackageResolver: rt.pkgs,
		// The nested compilation spends the CALLING evaluation's entity
		// allowance rather than minting its own. Without this, a stylesheet
		// calling fn:transform in a loop would hand each nested compilation a
		// fresh ceiling for the modules it imports -- the per-module mint this
		// change removes, arriving one level up. Same house rule as
		// xpath.Context.AdoptBudget: a nested operation may spend the parent's
		// remainder, never reset it.
		moduleBudget: ctx.EntityBudget(),
		StaticParams: static,
		MaxVersion:   maxVersion,
	}
	// A transform reached from the static phase is running INSIDE a Compile
	// that holds compileMu, so it must not ask for the lock again. rt.static
	// is set only from the runtime the static phase builds, and is the only
	// thing that distinguishes the two paths.
	compile := Compile
	if rt.static {
		compile = compileNestedLocked
	}
	sheet, err := compile(root, copts)
	if err != nil {
		if xdm.ErrorCode(err) != "" {
			return nil, fmt.Errorf("fn:transform: compiling the stylesheet: %w", err)
		}
		return nil, xdm.Errorf("FOXT0002",
			"fn:transform: compiling the stylesheet: %v", err)
	}
	return sheet, nil
}

// transformResultMap builds the map fn:transform returns.
//
// The principal result is keyed by the base output URI, or by "output" when
// there is none, and each secondary result by the URI it was written to.
// delivery-format decides what the values are: a document node, the
// serialized string, or the raw sequence.
func transformResultMap(ctx *xpath.Context, opts *xdm.MapItem, res *Result) (xdm.Sequence, error) {
	format, _, err := transformString(opts, "delivery-format")
	if err != nil {
		return nil, err
	}
	switch format {
	case "", "document", "serialized", "raw":
	default:
		return nil, xdm.Errorf("FOXT0002",
			"fn:transform: unknown delivery-format %q", format)
	}

	// F&O 3.1 applies post-process to every result document "in whatever
	// form it would otherwise be delivered", so it is read before anything
	// is delivered and applied to each entry after its format is chosen.
	pp, pperr := transformFunc(opts, "post-process", 2)
	if pperr != nil {
		return nil, pperr
	}

	// serialization-params is read whatever the delivery format, so a bad
	// value is refused rather than ignored, and acts only on the serialized
	// principal result: F&O 3.1 gives it as "Serialization parameters for the
	// principal result document", so a secondary keeps its own settings.
	pres := *res
	pres.output, pres.charMap, err = transformSerializationParams(opts, res.output, res.charMap)
	if err != nil {
		return nil, err
	}

	b := xdm.NewMapBuilder()
	principal, perr := deliverResult(format, res.Nodes, &pres)
	if perr != nil {
		return nil, perr
	}
	// XSLT 3.0 2.3.6.2 keeps, for APIs that want it, the earlier rule that an
	// empty raw result yields a result tree "if and only if the transformation
	// generates no secondary results". fn-transform-43 asserts it: its 2.0
	// stylesheet writes only result documents, and an empty principal entry
	// delivered beside them is a serialized "" that parse-xml rejects. Raw
	// delivery is exempt, since there the empty sequence is the result.
	hasPrincipal := format == "raw" || len(res.Nodes) > 0 || len(res.Secondary) == 0
	for _, sec := range res.Secondary {
		hasPrincipal = hasPrincipal || sec.Href == ""
	}
	// An xsl:result-document with no href IS the principal output. Section
	// 24.3 changes the current output URI only "during execution of an
	// xsl:result-document instruction with an href attribute"; with none it
	// stays the base output URI, which names the principal result. Keying it
	// as a secondary under "" left the principal entry holding the empty tree
	// the stylesheet never wrote to, so ?output serialized to nothing. The
	// engine raises XTDE1490 if a second href-less instruction runs or if the
	// principal tree also has content, so at most one reaches here and it can
	// never collide with the tree deliverResult just rendered.
	for _, sec := range res.Secondary {
		if sec.Href != "" {
			continue
		}
		sec.Output, sec.charMap, err = transformSerializationParams(opts, sec.Output, sec.charMap)
		if err != nil {
			return nil, err
		}
		v, verr := deliverSecondary(format, sec)
		if verr != nil {
			return nil, verr
		}
		principal = v
	}
	// 14.7.1 keys the principal result by the base output URI when there is
	// one, and by "output" when there is not.
	key := "output"
	if u, ok, uerr := transformString(opts, "base-output-uri"); uerr == nil && ok && u != "" {
		key = u
	}
	if hasPrincipal {
		principal, perr = postProcess(ctx, pp, key, principal)
		if perr != nil {
			return nil, perr
		}
		if err := b.Set(xdm.NewString(key), principal); err != nil {
			return nil, err
		}
	}
	for _, sec := range res.Secondary {
		// The href-less document was folded into the principal entry above.
		if sec.Href == "" {
			continue
		}
		v, verr := deliverSecondary(format, sec)
		if verr != nil {
			return nil, verr
		}
		// F&O 3.1 keys a secondary result by "the absolute URI of the result
		// document", which is the href resolved against the base output URI;
		// BaseURI holds exactly that whenever there was something to resolve
		// against.
		key := sec.Href
		if sec.BaseURI != "" {
			key = sec.BaseURI
		}
		v, verr = postProcess(ctx, pp, key, v)
		if verr != nil {
			return nil, verr
		}
		if err := b.Set(xdm.NewString(key), v); err != nil {
			return nil, err
		}
	}
	return xdm.Sequence{b.Build()}, nil
}

// transformSerializationParams applies the serialization-params option over
// the unnamed output definition o and its character map cm.
//
// F&O 3.1: the map "follows the same rules that apply to a map supplied as the
// second argument of fn:serialize", so a value of the wrong type is XPTY0004
// and a well-typed but invalid one SEPM0016; a QName key names an
// implementation-defined parameter, of which there are none here. A supplied
// value "overrides or augments" xsl:output "following the same rules as when
// one xsl:output declaration overrides another with lower import precedence":
// cdata-section-elements and suppress-indentation take the union, a character
// map supplements the stylesheet's and wins where both map a character, and
// anything else replaces. An empty sequence restores the default.
func transformSerializationParams(
	opts *xdm.MapItem, o OutputSettings, cm map[rune]string,
) (OutputSettings, map[rune]string, error) {
	seq, ok := transformOption(opts, "serialization-params")
	if !ok {
		return o, cm, nil
	}
	it, err := seq.Single()
	if err != nil {
		return o, cm, xdm.ErrType("fn:transform: serialization-params must be a single map")
	}
	m, ok := it.(*xdm.MapItem)
	if !ok {
		return o, cm, xdm.ErrType(
			"fn:transform: serialization-params must be a map, got %s", it.TypeName())
	}
	err = m.Entries(func(key *xdm.Atomic, val xdm.Sequence) error {
		if key.Type == xdm.TypeQName {
			return nil
		}
		name := key.String()
		if len(val) == 0 {
			switch name {
			case "cdata-section-elements":
				o.CDataElements = nil
			case "suppress-indentation":
				o.SuppressIndentation = nil
			case "use-character-maps":
				cm = nil
			case "escape-uri-attributes":
				o.EscapeURIAttributes = nil
			case "include-content-type":
				o.IncludeContentType = nil
			case "item-separator":
				o.ItemSeparator = nil
			case "build-tree":
				o.BuildTree = nil
			default:
				// An empty sequence leaves the parameter absent, which
				// no lexical value spells, so it is stored unchecked.
				return applySerializationParam(&o, name, "")
			}
			return nil
		}
		switch name {
		case "use-character-maps":
			add, err := xpath.CharacterMapParam(val)
			if err != nil {
				return err
			}
			merged := maps.Clone(cm)
			if merged == nil {
				merged = map[rune]string{}
			}
			maps.Copy(merged, add)
			cm = merged
			return nil
		case "cdata-section-elements", "suppress-indentation":
			var names []xdm.QName
			for _, v := range val {
				a, ok := v.(*xdm.Atomic)
				if !ok || a.Type != xdm.TypeQName {
					return xdm.ErrType("serialization parameter %q takes QNames", name)
				}
				names = append(names, *a.QName())
			}
			// Concat, not append: the slices are the stylesheet's own.
			if name == "cdata-section-elements" {
				o.CDataElements = slices.Concat(o.CDataElements, names)
			} else {
				o.SuppressIndentation = slices.Concat(o.SuppressIndentation, names)
			}
			return nil
		}
		if len(val) != 1 {
			return xdm.ErrType("serialization parameter %q takes a single value", name)
		}
		a, ok := val[0].(*xdm.Atomic)
		if !ok {
			return xdm.ErrType("serialization parameter %q takes an atomic value", name)
		}
		v := a.String()
		switch name {
		case "allow-duplicate-names", "byte-order-mark", "escape-uri-attributes",
			"include-content-type", "indent", "omit-xml-declaration", "standalone",
			"undeclare-prefixes":
			if a.Type != xdm.TypeBoolean && a.Type != xdm.TypeUntypedAtomic {
				return xdm.ErrType("serialization parameter %q takes a boolean", name)
			}
			b, err := xpath.CastAtomic(a, xdm.TypeBoolean)
			if err != nil {
				return xdm.ErrType("serialization parameter %q takes a boolean", name)
			}
			v = "no"
			if b.Bool() {
				v = "yes"
			}
		case "html-version":
			// xs:decimal, read through its lexical form.
		default:
			// A QName method names a user-defined method, and there are none.
			isMethod := name == "method" || name == "json-node-output-method"
			if isMethod && a.Type == xdm.TypeQName {
				return xdm.Errorf("SEPM0016",
					"serialization parameter %q: unsupported method %s", name, v)
			}
			if a.Type != xdm.TypeString && a.Type != xdm.TypeAnyURI && a.Type != xdm.TypeUntypedAtomic {
				return xdm.ErrType("serialization parameter %q takes a string", name)
			}
			switch v {
			case "xml", "html", "xhtml", "text", "json", "adaptive":
			default:
				if isMethod {
					return xdm.Errorf("SEPM0016",
						"serialization parameter %q: unsupported method %q", name, v)
				}
			}
		}
		return SetSerializationParam(&o, name, v)
	})
	return o, cm, err
}

// transformFunc reads a function-valued option.
//
// F&O 3.1 gives post-process the type function(xs:string, item()*) as item()*,
// so arity is part of what makes the option valid: a one-argument function is
// not a post-processor that was merely given the wrong body, it is the wrong
// option value, and FOXT0002 is the code for that.
func transformFunc(m *xdm.MapItem, name string, arity int) (*xdm.FunctionItem, error) {
	seq, ok := transformOption(m, name)
	if !ok {
		return nil, nil
	}
	it, err := seq.Single()
	if err != nil {
		return nil, xdm.ErrType(
			"fn:transform: %s must be a single function", name)
	}
	f, ok := it.(*xdm.FunctionItem)
	if !ok {
		return nil, xdm.ErrType(
			"fn:transform: %s must be a function, not %T", name, it)
	}
	if f.Arity != arity {
		return nil, xdm.Errorf("FOXT0002",
			"fn:transform: %s must take %d arguments, not %d",
			name, arity, f.Arity)
	}
	return f, nil
}

// postProcess applies the post-process option to one delivered result.
//
// F&O 3.1: "A function that is used to post-process each result document of
// the transformation (both the principal result and secondary results), in
// whatever form it would otherwise be delivered." So it runs after the
// delivery format has been applied, on every entry of the map, and its return
// value -- not the original -- is what the entry holds.
//
// The call carries the evaluation context so that a post-processor which
// itself calls fn:transform is charged the nesting it adds. Without it the
// bound would be escapable by moving the recursion into the post-processor,
// which is the same hole the depth charge above closes for the direct case.
func postProcess(ctx *xpath.Context, f *xdm.FunctionItem, key string, val xdm.Sequence) (xdm.Sequence, error) {
	if f == nil {
		return val, nil
	}
	out, err := f.Invoke(ctx, []xdm.Sequence{
		{xdm.NewString(key)},
		val,
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// deliverResult renders the principal result in the requested format.
func deliverResult(format string, nodes xdm.Sequence, res *Result) (xdm.Sequence, error) {
	switch format {
	case "raw":
		return nodes, nil
	case "serialized":
		var buf bytes.Buffer
		if err := res.Serialize(&buf); err != nil {
			return nil, err
		}
		return xdm.Sequence{xdm.NewString(buf.String())}, nil
	default:
		// "document" and the default: the result tree as a document node,
		// which is what res.Tree already is.
		if t := res.Tree(); t != nil {
			return xdm.Sequence{t}, nil
		}
		return nodes, nil
	}
}

// deliverSecondary renders one xsl:result-document in the requested format.
func deliverSecondary(format string, sec SecondaryResult) (xdm.Sequence, error) {
	switch format {
	case "raw":
		return sec.Nodes, nil
	case "serialized":
		var buf bytes.Buffer
		if err := sec.Serialize(&buf, nil); err != nil {
			return nil, err
		}
		return xdm.Sequence{xdm.NewString(buf.String())}, nil
	default:
		doc := &xdm.Node{Kind: xdm.KindDocument}
		for _, it := range sec.Nodes {
			if n, ok := it.(*xdm.Node); ok {
				doc.Children = append(doc.Children, n)
			}
		}
		return xdm.Sequence{doc}, nil
	}
}

// moduleResolverFor returns the ModuleResolver behind a DocumentResolver, or
// nil when there is none.
//
// Transform wraps the caller's resolver twice: in a stripSpaceResolver when
// the stylesheet declares xsl:strip-space, and in a readDocResolver so that
// fn:doc reads can be recorded. Both implement DocumentResolver alone, so a
// plain type assertion failed and a nested stylesheet could not reach its own
// xsl:import -- which is every stylesheet DocBook xslTNG runs through
// fn:transform. The strip-space layer is the one that hid longest, because it
// is only installed when the stylesheet declares it.
func moduleResolverFor(d xpath.DocumentResolver) ModuleResolver {
	for {
		switch v := d.(type) {
		case nil:
			return nil
		case ModuleResolver:
			return v
		case *readDocResolver:
			d = v.inner
		case *stripSpaceResolver:
			d = v.inner
		default:
			return nil
		}
	}
}

// callerDocuments returns the DocumentResolver the caller supplied, beneath
// the per-transform wrappers moduleResolverFor also looks through.
func callerDocuments(d xpath.DocumentResolver) xpath.DocumentResolver {
	for {
		switch v := d.(type) {
		case *readDocResolver:
			d = v.inner
		case *stripSpaceResolver:
			d = v.inner
		default:
			return d
		}
	}
}
