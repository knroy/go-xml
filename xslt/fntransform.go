package xslt

import (
	"bytes"
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
	a, ok := it.(*xdm.Atomic)
	if !ok {
		return "", true, xdm.ErrType(
			"fn:transform: %s must be a string, got %s", name, it.TypeName())
	}
	return a.String(), true, nil
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
		return nil, err
	}
	return transformResultMap(opts, res)
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
			// The same refusal fn:doc gives, under fn:transform's own code:
			// FOXT0001 is "the transformation cannot be invoked", and it
			// cannot be when the stylesheet it names cannot be read.
			return nil, xdm.Errorf("FOXT0001",
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
				return nil, xdm.Errorf("FOXT0001",
					"fn:transform: cannot retrieve stylesheet-location %q: %v",
					loc, merr)
			}
			return compileNested(rt, root, abs)
		}
		tree, derr := rt.opts.Documents.ResolveDocument(loc, base)
		if derr != nil {
			return nil, xdm.Errorf("FOXT0001",
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
	sheet, err := Compile(root, CompileOptions{
		Resolver: mr,
		BaseURI:  base,
	})
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
