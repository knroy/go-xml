package xslt

// A cross-package call frame (lane L7; use-package-150/-152, moved from L2).
//
// L2's call frames (streamfn_inline.go) plan a call on a stylesheet function
// whose body is a sequence constructor as that body, inside the caller's
// streamed plan, and compile a copy of the body against the plan. They take
// only functions of the caller's own module. This file lets them take a
// function of a package the top-level package uses directly, on terms that
// make the copy's names bind as the package's own compile bound them:
//
//   - pkgFrameState is recorded per used package while it compiles
//     (enterPkgFrame, hooked in compileUsedPackage): its rebuilt module root,
//     the package serial (compilePackage) and its static variables, which
//     compileUsedPackage restores away on return. The copy of a body is
//     compiled with them put back (pkgScope).
//   - Only a package that the top-level module uses directly, through an
//     xsl:use-package with no xsl:override (nothing of the package is
//     replaced: the package's declarations are the ones in force for its own
//     code), that reaches no other module, and that has at least one
//     accepted frame function, gets a state. Without one nothing here does
//     anything (every hook below returns its "not mine" answer first).
//   - An accepted frame function (pkgScopeFree) has a body that names
//     nothing whose binding could depend on the compiler's state: its own
//     parameters and local variables, built-in functions (no key(),
//     format-number(), accumulator-*, current-*, a document loader,
//     function-lookup, transform), dynamic calls on its own variables, and
//     xsl:apply-templates in a mode the package declares that no other
//     module declares or names. A wrapper (pkgWrapper: one xsl:sequence that
//     calls an accepted function, passing its own parameters of the same
//     type and literal values, with the same result type) is read as that
//     call (j:xml-to-json#1 -> #2).
//   - The package's streamable modes get a rules plan of their own
//     (pkgRulesPlan), over the package root; while the package compiles,
//     streamFuncs answers the package's functions (its rules' j:escape,
//     j:indent are judged in the package's table). The top-level plan
//     verifies such a mode only for apply ops written in the package
//     (link: pkgOK), so a top-level apply into a package mode is unchanged.
//   - The frame's call may be at the document node ("." as a node()
//     parameter): inlineCheck admits it for an accepted package function
//     only.
// At run time the frame is L2's inlineCallInstr: the body's value collected
// into a temporary builder, converted by the function's declared type as
// userFunction.call does ("XTTE0780" family via convertAsLazy), and only then
// written; any state it was not compiled for declines with errStreamPlan.

import (
	"reflect"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"weak"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// pkgFrameState is what a used package's frames need of its compile.
type pkgFrameState struct {
	root       *xdm.Node // the package's rebuilt module root
	top        *xdm.Node // the top-level module root that uses it
	serial     int       // compilePackage while it compiled
	staticVars []staticVar
	fns        map[funcKey]*streamFunc // the package's own functions
	frames     map[funcKey]*streamFunc // accepted frame functions and wrappers
	rp         *rulesPlan
	rpDone     bool
}

// pkgFrameRoots maps a package root (weakly) to its state, so the free
// functions of the inline code can tell a frame package's nodes.
var pkgFrameRoots sync.Map // weak.Pointer[xdm.Node] -> *pkgFrameState

func pkgFrameOf(root *xdm.Node) *pkgFrameState {
	if root == nil {
		return nil
	}
	v, ok := pkgFrameRoots.Load(weak.Make(root))
	if !ok {
		return nil
	}
	return v.(*pkgFrameState)
}

// enterPkgFrame records u's state when u qualifies, and makes it the
// package being compiled until the returned function runs. Called in
// compileUsedPackage once compilePackage and the static variables are u's.
func (c *compiler) enterPkgFrame(u *usePackageDecl) func() {
	none := func() {}
	if c.usedPackageDepth != 1 || u == nil || u.root == nil || c.sheet == nil || c.sheet.source == nil {
		return none
	}
	top := firstElement(c.sheet.source)
	if top == nil || documentElementOf(u.el) != top || reachesOtherModules(u.root) {
		return none
	}
	for _, ch := range u.el.ChildElements() {
		if !isXSL(ch, "accept") {
			return none // an xsl:override replaces something of the package
		}
	}
	st := &pkgFrameState{root: u.root, top: top, serial: compilePackage, staticVars: c.staticVars,
		fns: collectStreamFuncs(u.root), frames: map[funcKey]*streamFunc{}}
	local := collectStreamFuncs(top)
	for k, f := range st.fns {
		if _, mine := local[k]; !mine && sheetBody(f.body) == nil && pkgScopeFree(f.body, u.root, top) {
			st.frames[k] = f
		}
	}
	for k, f := range st.fns {
		if _, mine := local[k]; !mine && st.frames[k] == nil && pkgWrapper(f, st) != nil {
			st.frames[k] = f
		}
	}
	if len(st.frames) == 0 {
		return none
	}
	key := weak.Make(u.root)
	if _, loaded := pkgFrameRoots.Swap(key, st); !loaded {
		goruntime.AddCleanup(u.root, func(k weak.Pointer[xdm.Node]) { pkgFrameRoots.Delete(k) }, key)
	}
	if c.l7PkgFrames == nil {
		c.l7PkgFrames = map[*xdm.Node]*pkgFrameState{}
	}
	c.l7PkgFrames[u.root] = st
	prev := c.l7PkgCur
	c.l7PkgCur = st
	return func() { c.l7PkgCur = prev }
}

// pkgScope puts back the package state fn's body compiled in, when fn is a
// frame package's function; the returned function restores the caller's.
func (c *compiler) pkgScope(fn *xdm.Node) func() {
	st := pkgFrameOf(documentElementOf(fn))
	if st == nil {
		return func() {}
	}
	savedPkg, savedVars, savedCur := compilePackage, c.staticVars, c.l7PkgCur
	compilePackage, c.staticVars, c.l7PkgCur = st.serial, st.staticVars, st
	return func() { compilePackage, c.staticVars, c.l7PkgCur = savedPkg, savedVars, savedCur }
}

// pkgFrameFn reports whether f is a frame package's accepted frame function
// or wrapper: the trust walk verifies it by §19.8.5 like a local one
// (notePkgFuncs), and the inline route plans it.
func pkgFrameFn(f *streamFunc) bool {
	st := pkgFrameOf(documentElementOf(f.body))
	if st == nil {
		return false
	}
	g := st.frames[keyOfFn(f)]
	return g != nil && g.body == f.body
}

// keyOfFn is f's name and arity key.
func keyOfFn(f *streamFunc) funcKey {
	q, err := resolveQNameAttr(f.body, f.body.AttrValue("name"))
	if err != nil {
		return funcKey{}
	}
	return keyOf(q, f.arity())
}

// isPkgFrameFn reports whether fn is an accepted frame function of a frame
// package (not a wrapper: wrappers are rewritten away by pkgTarget).
func isPkgFrameFn(fn *xdm.Node) bool {
	st := pkgFrameOf(documentElementOf(fn))
	if st == nil {
		return false
	}
	for _, f := range st.frames {
		if f.body == fn && sheetBody(fn) == nil {
			return true
		}
	}
	return false
}

// pkgTarget is inlineTarget's fallback for a call, in an expression of the
// top-level module root, on a frame package's function: the one such call,
// a wrapper's rewritten in e (in place) into the call it makes.
func pkgTarget(e xpath.Expr, root *xdm.Node) (*xpath.FuncCall, *xdm.Node) {
	var states []*pkgFrameState
	pkgFrameRoots.Range(func(_, v any) bool {
		if st := v.(*pkgFrameState); st.top == root {
			states = append(states, st)
		}
		return true
	})
	if len(states) == 0 {
		return nil, nil
	}
	var found *xpath.FuncCall
	var st0 *pkgFrameState
	var f0 *streamFunc
	n := 0
	walkExprCalls(e, func(x *xpath.FuncCall) {
		if !stylesheetFn(x.Name) {
			return
		}
		for _, st := range states {
			if f := st.frames[keyOf(x.Name, len(x.Args))]; f != nil {
				n++
				found, st0, f0 = x, st, f
			}
		}
	})
	if n != 1 {
		return nil, nil
	}
	if sheetBody(f0.body) == nil {
		return found, f0.body
	}
	g, args := pkgWrapperCall(f0, st0, found)
	if g == nil || !replaceCallWith(e, found, args) {
		return nil, nil
	}
	return args, g.body
}

// replaceCallWith puts y in e where x is (in place).
func replaceCallWith(e xpath.Expr, x *xpath.FuncCall, y *xpath.FuncCall) bool {
	done := false
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) {
		if done || v.Kind() != reflect.Interface || !v.CanSet() {
			return
		}
		if c, ok := v.Interface().(*xpath.FuncCall); ok && c == x {
			v.Set(reflect.ValueOf(xpath.Expr(y)))
			done = true
		}
	})
	return done
}

// pkgWrapper is the accepted frame function f's body calls, when f is a
// wrapper; nil otherwise.
func pkgWrapper(f *streamFunc, st *pkgFrameState) *streamFunc {
	g, _ := pkgWrapperCall(f, st, nil)
	return g
}

// pkgWrapperCall checks f, a one-xsl:sequence function whose select is a
// call g(...) on an accepted instruction-body function of st, each argument
// one of f's parameters of g's parameter's declared type, or a literal (a
// string or number, (), an empty map); f's result type g's. With x, a call
// on f, it also returns the call on g that x makes.
func pkgWrapperCall(f *streamFunc, st *pkgFrameState, x *xpath.FuncCall) (*streamFunc, *xpath.FuncCall) {
	body := sheetBody(f.body)
	call, ok := body.(*xpath.FuncCall)
	if !ok || !stylesheetFn(call.Name) {
		return nil, nil
	}
	g := st.fns[keyOf(call.Name, len(call.Args))]
	if g == nil || sheetBody(g.body) != nil || !pkgScopeFree(g.body, st.root, st.top) || g == f {
		return nil, nil
	}
	if normType(f.asType) != normType(g.asType) {
		return nil, nil
	}
	fParams, _ := splitFunction(f.body)
	gParams, _ := splitFunction(g.body)
	if len(gParams) != len(call.Args) {
		return nil, nil
	}
	out := &xpath.FuncCall{Name: call.Name, Args: make([]xpath.Expr, len(call.Args))}
	for j, a := range call.Args {
		if v, ok := a.(*xpath.VarRef); ok {
			i := -1
			for k, p := range fParams {
				if q, ok := paramName(p); ok && q == v.Name {
					i = k
				}
			}
			if i < 0 || normType(fParams[i].AttrValue("as")) != normType(gParams[j].AttrValue("as")) {
				return nil, nil
			}
			if x != nil {
				if i >= len(x.Args) {
					return nil, nil
				}
				out.Args[j] = x.Args[i]
			}
			continue
		}
		if !literalArg(a) {
			return nil, nil
		}
		out.Args[j] = a
	}
	return g, out
}

func normType(t string) string { return strings.Join(strings.Fields(t), "") }

// literalArg: a literal, (), or an empty map constructor: nothing whose
// meaning depends on where it is compiled.
func literalArg(a xpath.Expr) bool {
	switch x := a.(type) {
	case *xpath.Literal:
		return true
	case *xpath.SequenceExpr:
		return len(x.Items) == 0
	case *xpath.MapConstructor:
		return len(x.Keys) == 0 && len(x.Values) == 0
	}
	return false
}

// pkgFrameBanned are the built-ins a frame body may not call: they read
// package-local declarations, the focus-free run state, or documents.
var pkgFrameBanned = map[string]bool{"key": true, "format-number": true, "accumulator-before": true,
	"accumulator-after": true, "current-group": true, "current-grouping-key": true,
	"current-merge-group": true, "current-merge-key": true, "doc": true, "document": true,
	"collection": true, "uri-collection": true, "unparsed-text": true, "unparsed-text-lines": true,
	"unparsed-text-available": true, "json-doc": true, "function-lookup": true, "transform": true,
	"load-xquery-module": true, "unparsed-entity-uri": true, "unparsed-entity-public-id": true,
	"element-with-id": true, "id": true, "idref": true, "system-property": true,
	"function-available": true, "element-available": true, "type-available": true, "available-system-properties": true,
	"environment-variable": true, "available-environment-variables": true, "current-output-uri": true,
	"static-base-uri": true, "default-collation": true, "default-language": true}

// pkgFrameInstrs are the XSLT instructions a frame body may hold.
var pkgFrameInstrs = map[string]bool{"choose": true, "when": true, "otherwise": true, "if": true,
	"variable": true, "sequence": true, "apply-templates": true, "with-param": true,
	"value-of": true, "copy-of": true, "for-each": true, "text": true}

// pkgScopeFree reports whether the xsl:function fn of the package rooted at
// root names nothing whose binding depends on the compiler's state (see the
// file comment). top is the top-level module root, which must not declare
// or name a mode fn's body applies templates in.
func pkgScopeFree(fn, root, top *xdm.Node) bool {
	bound := map[xdm.QName]bool{}
	params, body := splitFunction(fn)
	for _, p := range params {
		q, ok := paramName(p)
		if !ok {
			return false
		}
		bound[q] = true
	}
	for a := range fn.Attrs() {
		if n := a.Name(); n.URI == "" && !inlineFunctionAttrs[n.Local] {
			return false
		}
	}
	ok := true
	var walk func(el *xdm.Node)
	walk = func(el *xdm.Node) {
		if !ok {
			return
		}
		xsl := el.Name().URI == xdm.NSXSL
		if xsl && !pkgFrameInstrs[el.Name().Local] {
			ok = false
			return
		}
		if isXSL(el, "variable") {
			q, err := resolveQNameAttr(el, el.AttrValue("name"))
			if err != nil {
				ok = false
				return
			}
			bound[q] = true
		}
		if isXSL(el, "apply-templates") && !pkgModeOK(el, root, top) {
			ok = false
			return
		}
		for a := range el.Attrs() {
			n := a.Name()
			var srcs []string
			switch {
			case !xsl && n.URI == xdm.NSXSL:
				ok = false // xsl:use-attribute-sets and the like on a literal element
				return
			case n.URI != "":
				continue
			case xsl && (n.Local == "select" || n.Local == "test"):
				srcs = []string{a.Value()}
			case xsl && (n.Local == "as" || n.Local == "name" || n.Local == "mode" || n.Local == "tunnel" ||
				n.Local == "required" || n.Local == "disable-output-escaping" || n.Local == "use-when"):
				continue
			case xsl && n.Local == "separator":
				var ok2 bool
				if srcs, ok2 = avtExpressions(a.Value()); !ok2 {
					ok = false
					return
				}
			case xsl:
				ok = false
				return
			default:
				var ok2 bool
				if srcs, ok2 = avtExpressions(a.Value()); !ok2 {
					ok = false
					return
				}
			}
			for _, src := range srcs {
				if !pkgExprFree(parseAt(el, src), bound) {
					ok = false
					return
				}
			}
		}
		for ch := range el.Children() {
			if ch.Kind() == xdm.KindText && !isXSL(el, "text") && strings.ContainsAny(ch.Value(), "{}") {
				ok = false
				return
			}
			if ch.Kind() == xdm.KindElement {
				walk(ch)
			}
		}
	}
	for _, el := range body {
		walk(el)
	}
	return ok && len(body) > 0
}

// pkgExprFree reports whether e reads only variables in bound, calls only
// built-ins outside pkgFrameBanned, and makes dynamic calls only on such
// variables.
func pkgExprFree(e xpath.Expr, bound map[xdm.QName]bool) bool {
	if e == nil {
		return false
	}
	ok := true
	builtin := func(q xdm.QName) bool {
		switch q.URI {
		case fnNS:
			return !pkgFrameBanned[q.Local]
		case xdm.NSXS, xdm.NSMap, xdm.NSArray, xdm.NSMath:
			return true
		}
		return false
	}
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) {
		if !ok || v.Kind() != reflect.Interface {
			return
		}
		switch x := v.Interface().(type) {
		case *xpath.VarRef:
			ok = bound[x.Name]
		case *xpath.FuncCall:
			ok = builtin(x.Name)
		case *xpath.NamedFunctionRef:
			ok = builtin(x.Name)
		case *xpath.DynamicCall:
			vr, isVar := x.Target.(*xpath.VarRef)
			ok = isVar && bound[vr.Name]
		case *xpath.InlineFunctionExpr:
			ok = false
		}
	})
	return ok
}

// pkgModeOK reports whether apply-templates el names, explicitly, a mode the
// package rooted at root declares and the top-level module neither declares
// nor writes a template rule for.
func pkgModeOK(el, root, top *xdm.Node) bool {
	a := el.Attr("", "mode")
	if a == nil {
		return false
	}
	toks := strings.Fields(a.Value())
	if len(toks) != 1 || strings.HasPrefix(toks[0], "#") {
		return false
	}
	q, err := resolveQNameAttr(el, toks[0])
	if err != nil {
		return false
	}
	m := xdm.QName{URI: q.URI, Local: q.Local}.Clark()
	declared := false
	walkElements(root, func(d *xdm.Node) bool {
		if isXSL(d, "mode") && containsString(modeNamesOf(d), m) {
			declared = true
		}
		return true
	})
	if !declared {
		return false
	}
	clash := false
	walkElements(top, func(d *xdm.Node) bool {
		if isXSL(d, "mode") && containsString(modeNamesOf(d), m) ||
			isXSL(d, "template") && containsString(templateModeNames(d), m) {
			clash = true
		}
		return !clash
	})
	return !clash
}

// pkgRulesPlan is rulesPlanFor's walk for a frame package's module.
func (c *compiler) pkgRulesPlan(st *pkgFrameState) *rulesPlan {
	if st.rpDone {
		return st.rp
	}
	st.rpDone = true
	modes := streamableModeNames(st.root)
	if len(modes) == 0 || c.streamMayAlias(true) { // streamalias_l7.go
		return nil
	}
	t := newDeclTrust(st.root, false)
	if t.shared {
		return nil
	}
	t.plan = &streamPlan{recs: map[any]*planRec{}, sites: map[planSite][]planRoot{}}
	names := make([]string, 0, len(modes))
	for m := range modes {
		names = append(names, m)
	}
	slices.Sort(names)
	for _, m := range names {
		t.modeVerified(st.root, m)
	}
	st.rp = &rulesPlan{pl: t.plan, trust: t, modes: modes}
	return st.rp
}

// attachPkgModes gives rp, the top-level module's rules plan, the frame
// packages' streamable modes it does not have itself.
func (c *compiler) attachPkgModes(rp *rulesPlan) {
	if rp == nil {
		return
	}
	for _, st := range c.l7PkgFrames {
		p := c.pkgRulesPlan(st)
		if p == nil {
			continue
		}
		for m := range p.modes {
			if rp.modes[m] {
				continue // the top package's own: never looked up in the package's
			}
			if rp.pkg == nil {
				rp.pkg = map[string]*rulesPlan{}
				rp.pkgRoots = map[*xdm.Node]bool{}
			}
			rp.pkg[m] = p
			rp.pkgRoots[st.root] = true
		}
	}
}

// pkgVerified reports whether mode m is a frame package's, verified by its
// own walk.
func (rp *rulesPlan) pkgVerified(m string) bool {
	if rp == nil || rp.modes[m] {
		return false
	}
	p := rp.pkg[m]
	return p != nil && p.verified(m)
}

// pkgApply reports whether op is written in a frame package's module whose
// modes rp may look up.
func (rp *rulesPlan) pkgApply(op *streamOp) bool {
	return rp != nil && rp.pkgRoots[documentElementOf(op.el)]
}

// pkgDocParam is inlineCheck's admission of a frame function's call at the
// document node: the first argument "." (the document node), its parameter
// typed node() or item() (any occurrence) or untyped, which take the
// document node unconverted.
func pkgDocParam(fn, param *xdm.Node, a xpath.Expr, f vfocus) bool {
	if f != vNone || !isPkgFrameFn(fn) {
		return false
	}
	if _, ctx := a.(*xpath.ContextItem); !ctx {
		return false
	}
	switch strings.TrimRight(normType(param.AttrValue("as")), "?*+") {
	case "", "item()", "node()":
		return true
	}
	return false
}
