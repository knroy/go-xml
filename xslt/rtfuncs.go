package xslt

import (
	"fmt"
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
			// to the same resolver gate.
			if ctx.Docs == nil {
				return nil, fmt.Errorf(
					"FODC0002: document() is disabled (no resolver configured)")
			}
			var out xdm.Sequence
			for _, it := range xdm.Atomize(args[0]) {
				uri := it.(*xdm.Atomic).String()
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

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "system-property"}, Arity: 1,
		Call: func(_ *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
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

	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "element-available"}, Arity: 1,
		Call: func(_ *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
			name := stringArg(args[0])
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

	// Resolve the key name. Only unprefixed names are resolvable here, since
	// the stylesheet's namespace context is not available at call time.
	_, local := xdm.SplitQName(lexName)
	keyName := xdm.QName{Local: local}.Clark()
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
			return nil, err
		}
		root = n.Root()
	}

	index, err := rt.keyIndexFor(keyName, defs, root, ctx)
	if err != nil {
		return nil, err
	}

	var out xdm.Sequence
	for _, kv := range xdm.Atomize(args[1]) {
		k := kv.(*xdm.Atomic).String()
		out = append(out, index[k]...)
	}
	return xdm.SortDocumentOrder(out), nil
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
			vals, err := def.use.Eval(ctx.WithFocus(n, 1, 1))
			if err != nil {
				return err
			}
			for _, v := range xdm.Atomize(vals) {
				k := v.(*xdm.Atomic).String()
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
				vals, err := def.use.Eval(ctx.WithFocus(a, 1, 1))
				if err != nil {
					return err
				}
				for _, v := range xdm.Atomize(vals) {
					k := v.(*xdm.Atomic).String()
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
