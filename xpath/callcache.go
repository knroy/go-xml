package xpath

import (
	"reflect"

	"github.com/knroy/go-xml/internal/xpathleaf"
)

// callResolution is one call site's resolved target. It stays valid while the
// context presents the same library value, static host and library version,
// and no *Library on the chain behind it has been added to or re-parented.
type callResolution struct {
	funcs     FunctionLibrary
	host      any
	libv      Version
	links     []libLink
	fn        Function
	params    []SequenceType
	hasParams bool
}

// libLink is one *Library of a cached chain as it stood when the entry was
// made.
type libLink struct {
	lib    *Library
	parent FunctionLibrary
	gen    uint64
}

func (c *callResolution) current() bool {
	for _, l := range c.links {
		if l.lib.gen != l.gen || l.lib.Parent != l.parent {
			return false
		}
	}
	return true
}

// resolve is lookupFor plus lookupSpecParams, cached on the call site. A host
// language builds its library once per stylesheet, so one call site is
// otherwise resolved through the same chain of maps, the package-scoping test
// and the version gate thousands of times with one answer.
//
// Only a chain whose answers are a pure function of the key is cached: one
// made of *Library and of host wrappers that declare themselves stable (see
// xpathleaf.StableLibrary). Any other FunctionLibrary is looked up every
// time, since nothing here can see when its answers change.
//
// ponytail: one entry per call site, kept for the first library it was made
// under; a site evaluated under several only caches the first.
func (e *FuncCall) resolve(ctx *Context) (Function, []SequenceType, bool, bool) {
	libv := ctx.libraryVersion()
	c := e.resolved.Load()
	if c != nil && c.libv == libv &&
		c.funcs == ctx.Funcs && c.host == ctx.StaticHost && c.current() {
		return c.fn, c.params, c.hasParams, true
	}
	fn, ok := lookupFor(ctx, e.Name, len(e.Args))
	if !ok {
		return fn, nil, false, false
	}
	params, hasParams := lookupSpecParams(fn.Name, fn.Arity)
	// An entry is replaced only under the library it was made for. A site
	// evaluated under a new library every time -- XQuery's lifted operands
	// build one per evaluation -- keeps its first entry and is looked up,
	// rather than allocating an entry per call that is never hit.
	if c != nil && c.funcs != ctx.Funcs {
		return fn, params, hasParams, true
	}
	if links, stable := stableChain(ctx.Funcs); stable && comparableValue(ctx.StaticHost) {
		e.resolved.Store(&callResolution{funcs: ctx.Funcs, host: ctx.StaticHost,
			libv: libv, links: links, fn: fn, params: params, hasParams: hasParams})
	}
	return fn, params, hasParams, true
}

// stableChain lists the *Library links behind lib, or reports that some link
// is a library whose answers this package cannot vouch for.
func stableChain(lib FunctionLibrary) ([]libLink, bool) {
	var links []libLink
	for steps := 0; lib != nil; steps++ {
		if steps == 64 {
			return nil, false
		}
		switch l := lib.(type) {
		case *Library:
			if l == nil {
				return links, true
			}
			links = append(links, libLink{lib: l, parent: l.Parent, gen: l.gen})
			lib = l.Parent
		case xpathleaf.StableLibrary:
			if !comparableValue(l) {
				return nil, false
			}
			lib, _ = l.WrappedLibrary().(FunctionLibrary)
		default:
			return nil, false
		}
	}
	return links, true
}

// comparableValue guards the == in resolve: interface equality panics on two
// values of one uncomparable dynamic type.
func comparableValue(v any) bool {
	return v == nil || reflect.TypeOf(v).Comparable()
}
