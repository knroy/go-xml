package xquery

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// fn:load-xquery-module (F&O 3.1 section 14.6.1) is implemented here and
// installed into xpath's stub when this package is initialised, so a program
// that imports xquery -- a blank import is enough -- gets the function in
// XPath, XQuery and XSLT alike. See xpath/processors.go.
//
// The call is compiled as a main module that only imports the namespace:
//
//	import module namespace m = "URI" at "hint", ...; ()
//
// That reuses "import module" whole -- the module store, the resolver, the
// budgets, %private, the module's own static context and the initialisation
// order. The module's globals are then bound exactly as for any query, and
// the result is read off them. Only the target namespace's own public
// declarations are listed, because the result "does not include global
// variables or functions declared in such a transitively-imported module".
//
// The module text is read ONLY through Context.Modules, which is the same
// confinement "import module" has: an XQuery caller's Options.Modules and
// Options.ModuleResolver, or whatever an XPath or XSLT host installed. With
// none, nothing is read and the call is FOQM0002, as an import would be
// XQST0059: the module was not found because nothing was allowed to look.
func init() { xpath.RegisterXQueryModuleLoader(loadXQueryModule) }

// queryModules is what an XQuery query installs as Context.Modules: its own
// module options, so that a module loaded from it sees the module store and
// the schemas "import module" and "import schema" would see.
type queryModules struct{ opts Options }

// Resolve implements xpath.ModuleResolver: the store first, then the
// resolver, the order the module loader uses.
func (m queryModules) Resolve(ns string, hints []string, base string) (io.ReadCloser, string, error) {
	for _, mod := range m.opts.Modules {
		if mod.Namespace == ns {
			return io.NopCloser(strings.NewReader(mod.Source)), mod.BaseURI, nil
		}
	}
	if m.opts.ModuleResolver == nil {
		return noModuleResolver{}.Resolve(ns, hints, base)
	}
	return m.opts.ModuleResolver.Resolve(ns, hints, base)
}

// moduleCall is the options map of one call, checked.
type moduleCall struct {
	hints []string
	item  xdm.Item
	vars  map[string]xdm.Sequence
}

func loadXQueryModule(ctx *xpath.Context, uri string, options *xdm.MapItem) (xdm.Sequence, error) {
	if uri == "" {
		return nil, xdm.Errorf("FOQM0001", "fn:load-xquery-module: the module URI is empty")
	}
	call, err := moduleOptions(options)
	if err != nil {
		return nil, err
	}

	var opts Options
	switch m := ctx.Modules.(type) {
	case queryModules:
		opts = Options{Modules: m.opts.Modules, ModuleResolver: m.opts.ModuleResolver,
			MaxModules: m.opts.MaxModules, MaxModuleBytes: m.opts.MaxModuleBytes,
			Schemas: m.opts.Schemas, SchemaResolver: m.opts.SchemaResolver,
			MaxSchemaBytes: m.opts.MaxSchemaBytes}
	case nil:
	default:
		opts.ModuleResolver = m
	}
	opts.BaseURI = ctx.StaticBaseURI

	src := "import module namespace m = " + quoteLiteral(uri)
	for i, h := range call.hints {
		if i == 0 {
			src += " at "
		} else {
			src += ", "
		}
		src += quoteLiteral(h)
	}
	q, err := Compile(src+"; ()", opts)
	if err != nil {
		return nil, moduleCompileError(uri, err)
	}

	// FOQM0005 rather than the XPTY0004 the binding itself would raise: the
	// values come from the caller, and §14.6.1 says they must conform
	// "without conversion".
	for _, mod := range q.modules {
		for _, d := range mod.vars {
			if v, ok := call.vars[d.name.Clark()]; ok && d.external {
				if _, err := d.typ.match(v, "the external variable $"+d.name.Lexical()); err != nil {
					return nil, xdm.Errorf("FOQM0005", "fn:load-xquery-module: %v", err)
				}
			}
		}
		if call.item != nil && mod.contextItem != nil {
			if _, err := mod.contextItem.typ.match(xdm.Sequence{call.item}, "the context item"); err != nil {
				return nil, xdm.Errorf("FOQM0005", "fn:load-xquery-module: %v", err)
			}
		}
	}

	// A fresh context: the module sees none of the caller's variables,
	// functions or focus, but it spends the caller's budgets and reads
	// through the caller's resolvers.
	sub := xpath.NewContext(call.item, xpath.Builtins()).AdoptBudget(ctx)
	sub.Ctx = ctx.Ctx
	sub.Docs, sub.Collections, sub.Texts = ctx.Docs, ctx.Collections, ctx.Texts
	sub.Entities, sub.Environment, sub.Modules = ctx.Entities, ctx.Environment, ctx.Modules
	sub.Depth, sub.MaxDepth = ctx.Depth, ctx.MaxDepth
	sub.ImplicitTimezone, sub.Now, sub.HasNow = ctx.ImplicitTimezone, ctx.Now, ctx.HasNow
	for k, v := range call.vars {
		sub.Vars[k] = v
	}
	bound, err := q.prepare(sub)
	if err != nil {
		return nil, err
	}

	vars, funcs := xdm.NewMap(), xdm.NewMap()
	for _, mod := range q.modules {
		if mod.ns != uri {
			continue
		}
		for _, d := range mod.visibleVars() {
			v, _ := bound.LookupVar(d.name)
			if vars, err = vars.Put(xdm.NewQNameValue(d.name), v); err != nil {
				return nil, err
			}
		}
		for _, d := range mod.visibleFuncs() {
			key := xdm.NewQNameValue(d.name)
			byArity := xdm.NewMap()
			if prev, ok, _ := funcs.Get(key); ok {
				byArity = prev[0].(*xdm.MapItem)
			}
			if byArity, err = byArity.Put(xdm.NewInteger(int64(len(d.params))),
				xdm.One(moduleFunction(d, bound))); err != nil {
				return nil, err
			}
			if funcs, err = funcs.Put(key, xdm.One(byArity)); err != nil {
				return nil, err
			}
		}
	}
	out, err := xdm.NewMap().Put(xdm.NewString("variables"), xdm.One(vars))
	if err != nil {
		return nil, err
	}
	out, err = out.Put(xdm.NewString("functions"), xdm.One(funcs))
	return xdm.One(out), err
}

// moduleFunction is a library module's function as a function item that
// runs in the module's own context -- its globals and its library -- wherever
// it is called from. A named reference would take its variables from the
// call site, which is right inside one query and wrong here: the caller has
// none of the module's globals.
func moduleFunction(d *funcDecl, bound *xpath.Context) *xdm.FunctionItem {
	fn, _ := bound.Funcs.Lookup(d.name, len(d.params))
	return &xdm.FunctionItem{Name: d.name, Arity: len(d.params), Signature: fn.Signature,
		Invoke: func(c any, args []xdm.Sequence) (xdm.Sequence, error) {
			call, ok := c.(*xpath.Context)
			if !ok {
				return nil, xdm.ErrType("%s invoked without an evaluation context", d.name.Lexical())
			}
			sub := bound.AdoptBudget(call)
			sub.Ctx, sub.Depth, sub.MaxDepth = call.Ctx, call.Depth, call.MaxDepth
			return fn.Call(sub, args)
		}}
}

// moduleCompileError maps a failed compile onto §14.6.1's codes: a module
// that could not be found is FOQM0002, a resource limit stays what it is,
// and any other static error in the module is FOQM0003.
func moduleCompileError(uri string, err error) error {
	if errors.Is(err, xdm.ErrResourceLimit) {
		return err
	}
	code := "FOQM0003"
	if xdm.ErrorCode(err) == "XQST0059" {
		code = "FOQM0002"
	}
	return &xdm.Error{Code: code, Err: err,
		Message: fmt.Sprintf("fn:load-xquery-module(%q): %v", uri, err)}
}

// moduleOptions checks the options map under the option parameter
// conventions: a known key of the wrong type is XPTY0004, an unknown key is
// ignored. vendor-options is checked and then ignored, which is what the
// specification asks of an option in a namespace the processor does not
// recognise -- and this processor recognises none.
func moduleOptions(m *xdm.MapItem) (moduleCall, error) {
	var c moduleCall
	if m == nil {
		return c, nil
	}
	get := func(key string) (xdm.Sequence, bool) {
		v, ok, _ := m.Get(xdm.NewString(key))
		return v, ok
	}
	if v, ok := get("xquery-version"); ok {
		a, err := v.Single()
		at, isAtomic := a.(*xdm.Atomic)
		if err != nil || !isAtomic || (at.Type != xdm.TypeDecimal && at.Type != xdm.TypeInteger) {
			return c, xdm.ErrType("fn:load-xquery-module: xquery-version must be an xs:decimal")
		}
		if r, ok := new(big.Rat).SetString(at.String()); !ok || r.Cmp(big.NewRat(31, 10)) > 0 {
			return c, xdm.Errorf("FOQM0006",
				"fn:load-xquery-module: no XQuery processor for version %s is available", at)
		}
	}
	if v, ok := get("location-hints"); ok {
		for _, it := range v {
			a, ok := it.(*xdm.Atomic)
			if !ok || (a.Type != xdm.TypeString && a.Type != xdm.TypeAnyURI && a.Type != xdm.TypeUntypedAtomic) {
				return c, xdm.ErrType("fn:load-xquery-module: location-hints must be strings")
			}
			c.hints = append(c.hints, a.String())
		}
	}
	if v, ok := get("context-item"); ok {
		if len(v) > 1 {
			return c, xdm.ErrType("fn:load-xquery-module: context-item must be at most one item")
		}
		if len(v) == 1 {
			c.item = v[0]
		}
	}
	for _, key := range []string{"variables", "vendor-options"} {
		v, ok := get(key)
		if !ok {
			continue
		}
		it, err := v.Single()
		vm, isMap := it.(*xdm.MapItem)
		if err != nil || !isMap {
			return c, xdm.ErrType("fn:load-xquery-module: %s must be a map(xs:QName, item()*)", key)
		}
		err = vm.Entries(func(k *xdm.Atomic, val xdm.Sequence) error {
			if k.Type != xdm.TypeQName {
				return xdm.ErrType("fn:load-xquery-module: a key of %s must be an xs:QName, got %s", key, k.TypeName())
			}
			if key == "variables" {
				if c.vars == nil {
					c.vars = map[string]xdm.Sequence{}
				}
				c.vars[k.QName().Clark()] = val
			}
			return nil
		})
		if err != nil {
			return c, err
		}
	}
	return c, nil
}

// quoteLiteral writes s as an XQuery string literal.
func quoteLiteral(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
