package xslt

// Calls on a used package's own functions (lane L7, su-filter-101,
// use-package-150/-152).
//
// A call in the top-level package on a function of a package it uses is
// not in the trust walk's tables (collectStreamFuncs reads the module), so
// the call was unmodelled. The function is taken -- verified in its own
// package and run as itself, never inlined -- when its body cannot reach a
// name whose binding depends on which package reads it:
//
//   - the function is declared in a package the top-level package uses
//     directly (its rebuilt tree, compileUsedPackage), once among them all,
//     and not by the top-level module itself (a local declaration or
//     override is the one in force, and is looked up first);
//   - its body is the inliner's shape (params and one xsl:sequence) and
//     calls no stylesheet function, names none (f#n) and makes no dynamic
//     call, so the body's verdict cannot rest on a function of either
//     package resolved by name in the wrong one.
//
// The trust walk verifies it by §19.8.5 like a local one (funcVerified),
// with the body parsed where it is written. The split marks the call
// (markSheetCalls over pkgStreamFuncs) and leaves it a call: rewriteCalls
// inlines only the module's own functions (streamFuncs), so the outer
// evaluates the call through the compiled function, which the runtime
// binds by the packages' rules (its $cat is the used package's, or an
// xsl:override of it).

import (
	"reflect"
	goruntime "runtime"
	"sync"
	"weak"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// pkgFuncs holds, for a top-level module root, the used packages'
// functions taken (by weak key, removed with the tree).
var pkgFuncs sync.Map // weak.Pointer[xdm.Node] -> map[funcKey]*streamFunc

// notePkgFuncs records the functions of the packages roots that root, the
// top-level module, uses directly.
func notePkgFuncs(root *xdm.Node, roots []*xdm.Node) {
	if root == nil || len(roots) == 0 {
		return
	}
	local := collectStreamFuncs(root)
	seen := map[funcKey]int{}
	out := map[funcKey]*streamFunc{}
	for _, r := range roots {
		for k, f := range collectStreamFuncs(r) {
			seen[k]++
			if _, mine := local[k]; !mine && selfContainedFn(f) {
				out[k] = f
			}
		}
	}
	for k, n := range seen {
		if n > 1 {
			delete(out, k)
		}
	}
	if len(out) == 0 {
		return
	}
	key := weak.Make(root)
	if _, loaded := pkgFuncs.Swap(key, out); !loaded {
		goruntime.AddCleanup(root, func(k weak.Pointer[xdm.Node]) { pkgFuncs.Delete(k) }, key)
	}
}

// pkgFuncsOf is what notePkgFuncs recorded for root, or nil.
func pkgFuncsOf(root *xdm.Node) map[funcKey]*streamFunc {
	if root == nil {
		return nil
	}
	v, ok := pkgFuncs.Load(weak.Make(root))
	if !ok {
		return nil
	}
	return v.(map[funcKey]*streamFunc)
}

// withPkgFuncs is fns with the used packages' functions of root added, the
// module's own winning; fns itself when there are none.
func withPkgFuncs(fns map[funcKey]*streamFunc, root *xdm.Node) map[funcKey]*streamFunc {
	p := pkgFuncsOf(root)
	if len(p) == 0 {
		return fns
	}
	out := make(map[funcKey]*streamFunc, len(fns)+len(p))
	for k, f := range p {
		out[k] = f
	}
	for k, f := range fns {
		out[k] = f
	}
	return out
}

// selfContainedFn reports whether f's body is the inliner's shape and
// reaches no stylesheet function.
func selfContainedFn(f *streamFunc) bool {
	body := sheetBody(f.body)
	if body == nil {
		return false
	}
	ok := true
	exprWalk(reflect.ValueOf(&body).Elem(), func(v reflect.Value) {
		if !ok || v.Kind() != reflect.Interface {
			return
		}
		switch x := v.Interface().(type) {
		case *xpath.DynamicCall:
			ok = false
		case *xpath.NamedFunctionRef:
			ok = !stylesheetFn(x.Name)
		case *xpath.FuncCall:
			ok = !stylesheetFn(x.Name)
		}
	})
	return ok
}

// pkgStreamFuncs is streamFuncs with the used packages' functions, for
// marking calls (never for inlining).
func (c *compiler) pkgStreamFuncs() map[funcKey]*streamFunc {
	return withPkgFuncs(c.streamFuncs(), firstElement(c.sheet.source))
}
