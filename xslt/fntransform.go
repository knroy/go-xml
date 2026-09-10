package xslt

import (
	"bytes"
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// fn:transform runs a transformation described by an options map and returns
// its results as a map, F&O 3.1 section 14.7.1.
//
// It lives in xslt rather than in xpath because it needs an XSLT processor,
// and xpath does not depend on xslt -- the layering is one-directional and
// stays that way. xpath registers a stub that raises FOXT0004, which is the
// honest answer for a caller evaluating a bare XPath expression; this
// overrides it for the duration of a transform, exactly as key() and
// current() are bound per transform by registerRuntimeFuncs.
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
			return runNestedTransform(ctx, rt, opts)
		},
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
// arrive in the form the runtime looks them up by.
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
		if q := key.QName(); q != nil {
			out[q.Clark()] = value
			return nil
		}
		// A key that is not a QName is a string naming one in no namespace,
		// which is how most callers write it.
		out[xdm.QName{Local: key.String()}.Clark()] = value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// runNestedTransform is fn:transform's body.
func runNestedTransform(ctx *xpath.Context, rt *runtime, opts *xdm.MapItem) (xdm.Sequence, error) {
	sheet, err := nestedStylesheet(ctx, rt, opts)
	if err != nil {
		return nil, err
	}

	var source *xdm.Node
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
		source = n
	}

	topts := rt.opts
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
	if v, ok, verr := transformString(opts, "initial-template"); verr != nil {
		return nil, verr
	} else if ok {
		topts.InitialTemplate = v
	}
	// initial-function and function-params are read as a pair. 2.3.5 infers
	// the arity from "the length of the parameter list", so the arguments are
	// not merely values but half the entry point's identity, and an absent
	// function-params is the empty list -- an invocation of the nullary
	// function of that name, which is XTDE0041 if the stylesheet has none.
	// That is why the array is read even when the option is absent rather
	// than left nil to mean "unspecified".
	if fname, ok, ferr := transformQName(opts, "initial-function"); ferr != nil {
		return nil, ferr
	} else if ok {
		topts.InitialFunction = fname
		args, _, aerr := transformArray(opts, "function-params")
		if aerr != nil {
			return nil, aerr
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

	res, err := sheet.Transform(rt.goCtx, source, topts)
	if err != nil {
		return nil, annotateNested(err, opts)
	}
	return transformResultMap(opts, res)
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
// none of them.
func nestedStylesheet(ctx *xpath.Context, rt *runtime, opts *xdm.MapItem) (*Stylesheet, error) {
	base, _, err := transformString(opts, "stylesheet-base-uri")
	if err != nil {
		return nil, err
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
		if rt.sheet.pkgResolver == nil {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: package-name %q cannot be resolved "+
					"(no package resolver configured)", name)
		}
		root, perr := rt.sheet.pkgResolver.ResolvePackage(name, vers)
		if perr != nil {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: cannot retrieve package %q version %q: %v",
				name, vers, perr)
		}
		return compileNested(rt, root, base)
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
		return compileNested(rt, n, base)
	}

	if text, ok, terr := transformString(opts, "stylesheet-text"); terr != nil {
		return nil, terr
	} else if ok {
		tree, perr := xdm.ParseString(text, nestedParseOptions(rt, base))
		if perr != nil {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: parsing stylesheet-text: %v", perr)
		}
		return compileNested(rt, tree.Root, base)
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
			root, abs, merr := mr.ResolveModule(loc, base)
			if merr != nil {
				return nil, xdm.Errorf("FOXT0002",
					"fn:transform: cannot retrieve stylesheet-location %q: %v",
					loc, merr)
			}
			return compileNested(rt, root, abs)
		}
		tree, derr := rt.opts.Documents.ResolveDocument(loc, base)
		if derr != nil {
			return nil, xdm.Errorf("FOXT0002",
				"fn:transform: cannot retrieve stylesheet-location %q: %v", loc, derr)
		}
		return compileNested(rt, tree.Root, loc)
	}

	return nil, xdm.Errorf("FOXT0002",
		"fn:transform: the options identify no stylesheet")
}

// nestedParseOptions are the parse settings a nested stylesheet is read with.
func nestedParseOptions(rt *runtime, base string) xdm.ParseOptions {
	return xdm.ParseOptions{BaseURI: base}
}

// compileNested compiles a stylesheet for fn:transform, reporting a
// compilation failure as FOXT0002 rather than letting the XSLT code escape.
//
// The static errors of the nested stylesheet are its own, and a caller of
// fn:transform is entitled to see them as "this transformation could not be
// invoked" rather than as an error of the calling stylesheet.
func compileNested(rt *runtime, root *xdm.Node, base string) (*Stylesheet, error) {
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
		PackageResolver: rt.sheet.pkgResolver,
	}
	// A transform reached from the static phase is running INSIDE a Compile
	// that holds compileMu, so it must not ask for the lock again. rt.static
	// is set only on the runtime the static phase builds, and is the only
	// thing that distinguishes the two paths.
	compile := Compile
	if rt.static {
		compile = compileNestedLocked
	}
	sheet, err := compile(root, copts)
	if err != nil {
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
func transformResultMap(opts *xdm.MapItem, res *Result) (xdm.Sequence, error) {
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

	b := xdm.NewMapBuilder()
	principal, perr := deliverResult(format, res.Nodes, res)
	if perr != nil {
		return nil, perr
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
	if err := b.Set(xdm.NewString(key), principal); err != nil {
		return nil, err
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
		if err := b.Set(xdm.NewString(sec.Href), v); err != nil {
			return nil, err
		}
	}
	return xdm.Sequence{b.Build()}, nil
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
