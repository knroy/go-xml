package xslt

// Lane L2: stylesheet function bodies that climb (streamability="ascent",
// "inspection", "filter" bodies that recurse on $n/.., test parent::X in a
// predicate, or return an ancestor).
//
// A burst call (streamsplit_calls.go) runs its function on a grounded copy
// of a match: the match whole, under shallow copies of its ancestors (name,
// namespaces, attributes, base URI) and a document node (xdmstream Ground).
// burstSafe admits a step up only to end in an attribute. This judges the
// bodies with a kind per value instead:
//
//	ckVal    atomic values, or nodes the expression makes itself;
//	ckDown   nodes of the copy at or below the match: anything goes;
//	ckUpAttr attributes or namespaces that may be an ancestor copy's (or
//	         ckDown nodes): they atomize as the originals do, but are the
//	         same node for several matches in the tree, distinct here;
//	ckUp     nodes that may be an ancestor's shallow copy: only its name,
//	         its attributes, a further step up, its existence (EBV, count,
//	         empty, exists), a kind test (instance of) or its identity
//	         within this one evaluation are read of it -- never its string
//	         value, children or descendants, which the copy lacks.
//
// Each parameter of each function in the closure gets the join of the kinds
// its call sites give it (the first function's are ckDown: the split passes
// a match or a value of one), to a fixpoint. A parameter declared with an
// atomic type is atomized by the call (§10.3.3): given ckUp it would read an
// ancestor's string value, and is refused. A function whose result may hold
// ancestor copies (ckUpAttr or ckUp, a type that is not atomic) is marked
// up: generic callers (burstSafe's callSafe) refuse it, and only a simple
// map whose value is atomized takes it (climbMapCheck, from mapPath), where
// the kind of R as a whole is ckUpAttr at most.

import (
	"reflect"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

type ckind uint8

const (
	ckVal ckind = iota
	ckDown
	ckUpAttr
	ckUp
)

func ckJoin(a, b ckind) ckind {
	if a > b {
		return a
	}
	return b
}

// climbFn is what the judge knows of one function of the closure.
type climbFn struct {
	names  []xdm.QName
	atomic []bool  // the parameter's declared type is atomic
	params []ckind // the join of what the call sites give
	res    ckind
}

// climbJudge checks bodies, or (fns nil) the right of a simple map in the
// split, where calls are read off their marks.
type climbJudge struct {
	fns     map[funcKey]*streamFunc
	bodies  map[*streamFunc]xpath.Expr
	info    map[*streamFunc]*climbFn
	vars    map[xdm.QName]ckind
	changed bool
	// noPos refuses fn:position, as burstSafe's noPos: R's focus is the
	// match alone.
	noPos bool
}

// climbClosure judges f's closure (bodies, as sheetClosure parsed it) with
// climbing allowed: ok, and the kind of f's result.
func climbClosure(f *streamFunc, fns map[funcKey]*streamFunc, bodies map[*streamFunc]xpath.Expr) (ckind, bool) {
	j := &climbJudge{fns: fns, bodies: bodies, info: map[*streamFunc]*climbFn{}}
	for g := range bodies {
		cf := &climbFn{}
		for _, p := range g.body.ChildElements() {
			if !isXSL(p, "param") {
				continue
			}
			q, ok := paramName(p)
			if !ok {
				return 0, false
			}
			cf.names = append(cf.names, q)
			cf.atomic = append(cf.atomic, atomicAs(p, p.AttrValue("as")))
			k := ckVal
			if g == f && !cf.atomic[len(cf.atomic)-1] {
				k = ckDown
			}
			cf.params = append(cf.params, k)
		}
		if len(cf.names) != g.arity() {
			return 0, false
		}
		j.info[g] = cf
	}
	for round := 0; ; round++ {
		if round > 64 {
			return 0, false // the lattice is four high: cannot happen
		}
		j.changed = false
		for g, body := range bodies {
			cf := j.info[g]
			j.vars = map[xdm.QName]ckind{}
			for i, q := range cf.names {
				j.vars[q] = cf.params[i]
			}
			k, ok := j.kind(body, ckDown)
			if !ok {
				return 0, false
			}
			if atomicAs(g.body, g.asType) {
				if k == ckUp {
					return 0, false // the result is atomized: an ancestor's string value
				}
				k = ckVal
			} else if k >= ckUpAttr && strings.Contains(g.asType, "schema-") {
				return 0, false
			}
			if k > cf.res {
				cf.res, j.changed = k, true
			}
		}
		if !j.changed {
			return j.info[f].res, true
		}
	}
}

// climbMapUp reports whether R holds a call marked up.
func climbMapUp(r xpath.Expr) bool {
	up := false
	walkExprCalls(r, func(x *xpath.FuncCall) {
		if sc, ok := sheetCallOf(x); ok && sc.up {
			up = true
		}
	})
	return up
}

// climbMapCalls reports whether R holds a call whose body climbs: on nested
// matches' copies it answers what the tree's nodes do (an ancestor's copy
// has its name and attributes), so an atomized R, keeping no node, may run
// on each (climbMapCheck judges R whole).
func climbMapCalls(r xpath.Expr) bool {
	climb := false
	walkExprCalls(r, func(x *xpath.FuncCall) {
		if sc, ok := sheetCallOf(x); ok && sc.climb {
			climb = true
		}
	})
	return climb
}

// climbMapKind is the kind of R, the right of a simple map evaluated on a
// grounded copy of a match.
func climbMapKind(r xpath.Expr) (ckind, bool) {
	j := &climbJudge{vars: map[xdm.QName]ckind{}, noPos: true}
	return j.kind(r, ckDown)
}

// climbMapCheck is mapPath's check of an R holding a call marked up: its
// value must be atomized (no node of it kept, so its identity cannot show)
// and hold no ancestor copy itself (whose string value the copy lacks).
func climbMapCheck(r xpath.Expr, atomize bool) string {
	k, ok := climbMapKind(r)
	switch {
	case !ok:
		return "simple map " + r.String() + ": reads more of an ancestor than its copy has"
	case !atomize || k > ckUpAttr:
		return "simple map " + r.String() + ": ancestors' copies kept"
	}
	return ""
}

// kind is the kind of e's value, ctx the kind of its focus; false when e
// reads of a value more than its kind allows, or is not one this knows.
func (j *climbJudge) kind(e xpath.Expr, ctx ckind) (ckind, bool) {
	switch x := e.(type) {
	case *xpath.Literal:
		return ckVal, true
	case *xpath.VarRef:
		if k, ok := j.vars[x.Name]; ok {
			return k, true
		}
		return ckDown, true // burstSafe's: a global or a caller's binding
	case *xpath.ContextItem:
		return ctx, true
	case *xpath.Step:
		return j.step(x, ctx)
	case *xpath.PathExpr:
		if x.Root {
			return 0, false
		}
		k := ctx
		for _, s := range x.Steps {
			var ok bool
			if k, ok = j.kind(s, k); !ok {
				return 0, false
			}
		}
		if len(x.Steps) == 0 {
			return 0, false
		}
		return k, true
	case *xpath.FilterExpr:
		k, ok := j.kind(x.Base, ctx)
		if !ok || !j.preds(x.Predicates, k) {
			return 0, false
		}
		return k, true
	case *xpath.BinaryOp:
		switch x.Op {
		case "and", "or":
			return ckVal, j.ebv(x.Left, ctx) && j.ebv(x.Right, ctx)
		case "is", "<<", ">>", "union", "intersect", "except":
			return 0, false
		}
		if !burstOps[x.Op] && x.Op != "to" && x.Op != "||" {
			return 0, false
		}
		return ckVal, j.atoms(ctx, x.Left, x.Right)
	case *xpath.UnaryOp:
		return ckVal, j.atoms(ctx, x.Operand)
	case *xpath.StringConcat:
		return ckVal, j.atoms(ctx, x.Left, x.Right)
	case *xpath.CastExpr:
		return ckVal, j.atoms(ctx, x.Operand)
	case *xpath.TreatExpr:
		return j.kind(x.Operand, ctx)
	case *xpath.InstanceOfExpr:
		k, ok := j.kind(x.Operand, ctx)
		if !ok || k >= ckUpAttr && !plainKindTest(x.Type) {
			return 0, false
		}
		return ckVal, true
	case *xpath.IfExpr:
		if !j.ebv(x.Cond, ctx) {
			return 0, false
		}
		a, ok := j.kind(x.Then, ctx)
		if !ok {
			return 0, false
		}
		b, ok := j.kind(x.Else, ctx)
		return ckJoin(a, b), ok
	case *xpath.SequenceExpr:
		k := ckVal
		for _, it := range x.Items {
			ik, ok := j.kind(it, ctx)
			if !ok {
				return 0, false
			}
			k = ckJoin(k, ik)
		}
		return k, true
	case *xpath.SimpleMap:
		l, ok := j.kind(x.Left, ctx)
		if !ok {
			return 0, false
		}
		return j.kind(x.Right, l)
	case *xpath.LetExpr, *xpath.ForExpr:
		var bs []xpath.Binding
		var ret xpath.Expr
		if l, ok := x.(*xpath.LetExpr); ok {
			bs, ret = l.Bindings, l.Return
		} else {
			f := x.(*xpath.ForExpr)
			bs, ret = f.Bindings, f.Return
		}
		restore := j.bind(bs, ctx)
		if restore == nil {
			return 0, false
		}
		defer restore()
		return j.kind(ret, ctx)
	case *xpath.QuantifiedExpr:
		restore := j.bind(x.Bindings, ctx)
		if restore == nil {
			return 0, false
		}
		defer restore()
		return ckVal, j.ebv(x.Test, ctx)
	case *xpath.FuncCall:
		return j.call(x, ctx)
	}
	// Anything else (maps, arrays, lookups, inline functions): burstSafe's,
	// when no climbed value can reach it.
	if ctx <= ckDown && !j.mentionsUp(e) && burstSafe(e, j.noPos) {
		if atomicValued(e) {
			return ckVal, true
		}
		return ckDown, true
	}
	return 0, false
}

// bind binds bs in order (each a sequence's kind, which is each item's),
// returning the function that unbinds them, or nil.
func (j *climbJudge) bind(bs []xpath.Binding, ctx ckind) func() {
	saved := map[xdm.QName]*ckind{}
	for _, b := range bs {
		k, ok := j.kind(b.Seq, ctx)
		if !ok {
			for q, v := range saved {
				j.unbind(q, v)
			}
			return nil
		}
		if _, done := saved[b.Var]; !done {
			if old, had := j.vars[b.Var]; had {
				saved[b.Var] = &old
			} else {
				saved[b.Var] = nil
			}
		}
		j.vars[b.Var] = k
	}
	return func() {
		for q, v := range saved {
			j.unbind(q, v)
		}
	}
}

func (j *climbJudge) unbind(q xdm.QName, old *ckind) {
	if old == nil {
		delete(j.vars, q)
	} else {
		j.vars[q] = *old
	}
}

// mentionsUp reports whether e reads a variable bound to a value that may
// hold an ancestor copy.
func (j *climbJudge) mentionsUp(e xpath.Expr) bool {
	up := false
	walkExprVars(e, func(v *xpath.VarRef) {
		if j.vars[v.Name] >= ckUpAttr {
			up = true
		}
	})
	return up
}

// step is the kind of a step's nodes from a focus of kind ctx.
func (j *climbJudge) step(st *xpath.Step, ctx ckind) (ckind, bool) {
	if ctx == ckVal {
		ctx = ckDown // a step on a value fails, as on the tree
	}
	var k ckind
	switch st.Axis {
	case xpath.AxisParent, xpath.AxisAncestor, xpath.AxisAncestorOrSelf:
		k = ckUp
	case xpath.AxisAttribute, xpath.AxisNamespace:
		k = ckDown
		if ctx >= ckUpAttr {
			k = ckUpAttr
		}
	case xpath.AxisSelf:
		k = ctx
	case xpath.AxisChild, xpath.AxisDescendant, xpath.AxisDescendantOrSelf:
		if ctx != ckDown {
			return 0, false
		}
		k = ckDown
	default:
		return 0, false
	}
	return k, j.preds(st.Predicates, k)
}

// preds checks predicates whose focus is of kind k: each is a boolean
// (EBV, any kind) or a position (an atomic value, ckVal).
func (j *climbJudge) preds(ps []xpath.Expr, k ckind) bool {
	for _, p := range ps {
		if _, ok := j.kind(p, k); !ok {
			return false
		}
	}
	return true
}

// ebv checks e read for its effective boolean value: of nodes, only that
// there is one.
func (j *climbJudge) ebv(e xpath.Expr, ctx ckind) bool {
	_, ok := j.kind(e, ctx)
	return ok
}

// atoms checks operands that are atomized: no ancestor copy among them.
func (j *climbJudge) atoms(ctx ckind, es ...xpath.Expr) bool {
	for _, e := range es {
		if k, ok := j.kind(e, ctx); !ok || k == ckUp {
			return false
		}
	}
	return true
}

// climbPass are the fn functions whose result is (part of) their first
// argument; the other arguments are atomized.
var climbPass = map[string]bool{
	"head": true, "tail": true, "exactly-one": true, "zero-or-one": true,
	"one-or-more": true, "unordered": true, "subsequence": true, "remove": true,
}

// climbNames read of a node only its name, or whether it is there.
var climbNames = map[string]bool{
	"name": true, "local-name": true, "namespace-uri": true, "node-name": true,
	"count": true, "exists": true, "empty": true, "boolean": true, "not": true,
}

func (j *climbJudge) call(x *xpath.FuncCall, ctx ckind) (ckind, bool) {
	switch x.Name.URI {
	case fnNS:
		n := x.Name.Local
		if burstFnBanned[n] || j.noPos && n == "position" {
			return 0, false
		}
		if len(x.Args) == 0 {
			if ctx >= ckUpAttr && !climbNames[n] && n != "position" && n != "true" && n != "false" {
				return 0, false // string(), data(), ... of the focus
			}
			if atomicValued(x) {
				return ckVal, true
			}
			return 0, false
		}
		kinds := make([]ckind, len(x.Args))
		for i, a := range x.Args {
			k, ok := j.kind(a, ctx)
			if !ok {
				return 0, false
			}
			kinds[i] = k
		}
		switch {
		case climbNames[n]:
			if n == "boolean" || n == "not" || len(x.Args) == 1 {
				return ckVal, true
			}
		case climbPass[n]:
			for _, k := range kinds[1:] {
				if k == ckUp {
					return 0, false
				}
			}
			return kinds[0], true
		}
		for _, k := range kinds {
			if k >= ckUpAttr && !(k == ckUpAttr && burstAtomizers[n]) {
				return 0, false
			}
		}
		// Over nodes of the copy at or below the match: burstSafe's callSafe.
		if atomicValued(x) || burstAtomizers[n] {
			return ckVal, true
		}
		return ckDown, true
	case xdm.NSXS, xdm.NSMath:
		if !j.atoms(ctx, x.Args...) {
			return 0, false
		}
		return ckVal, true
	case xdm.NSMap, xdm.NSArray:
		for _, a := range x.Args {
			if k, ok := j.kind(a, ctx); !ok || k >= ckUpAttr {
				return 0, false
			}
		}
		return ckDown, true
	}
	if !stylesheetFn(x.Name) {
		return 0, false
	}
	if j.fns == nil {
		return j.marked(x, ctx)
	}
	g := j.fns[keyOf(x.Name, len(x.Args))]
	cf := j.info[g]
	if g == nil || cf == nil {
		return 0, false
	}
	for i, a := range x.Args {
		k, ok := j.kind(a, ctx)
		if !ok {
			return 0, false
		}
		if cf.atomic[i] {
			if k == ckUp {
				return 0, false
			}
			continue
		}
		if k > cf.params[i] {
			cf.params[i], j.changed = k, true
		}
	}
	return cf.res, true
}

// marked is call for the split's own calls: their marks say what they
// return, given the matches' nodes (ckDown) or values.
func (j *climbJudge) marked(x *xpath.FuncCall, ctx ckind) (ckind, bool) {
	sc, ok := sheetCallOf(x)
	if !ok || !sc.values && !sc.burst {
		return 0, false
	}
	for _, a := range x.Args {
		k, ok := j.kind(a, ctx)
		if !ok || k > ckDown && !(sc.values && k == ckUpAttr) {
			return 0, false
		}
	}
	switch {
	case sc.atomic:
		return ckVal, true
	case sc.up:
		return sc.res, true
	case sc.values:
		return ckDown, true // made of values: nodes it makes, if any
	}
	return ckDown, true
}

// plainKindTest reports whether t tests only kinds and names, which a
// shallow copy of an ancestor answers as the original does.
func plainKindTest(t xpath.SequenceType) bool {
	if t.ItemType == nil {
		return !t.HasAtomicType && !t.IsFunctionTest && !t.IsMapTest && !t.IsArrayTest
	}
	switch it := t.ItemType.(type) {
	case *xpath.KindTest:
		return it.Content == nil && it.TypeName == "" && !it.SchemaDeclared
	case *xpath.NameTest:
		return true
	}
	return false
}

// walkExprNodes calls fn on every expression node in e.
func walkExprNodes(e xpath.Expr, fn func(any)) {
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.Interface {
			fn(v.Interface())
		}
	})
}

// walkExprCalls calls fn on every function call in e.
func walkExprCalls(e xpath.Expr, fn func(*xpath.FuncCall)) {
	walkExprNodes(e, func(v any) {
		if x, ok := v.(*xpath.FuncCall); ok {
			fn(x)
		}
	})
}

// walkExprVars calls fn on every variable reference in e.
func walkExprVars(e xpath.Expr, fn func(*xpath.VarRef)) {
	walkExprNodes(e, func(v any) {
		if x, ok := v.(*xpath.VarRef); ok {
			fn(x)
		}
	})
}

// climbMark completes a burst judgement (judgeSheetCall) of a call on f
// whose parameters are not all atomic: a closure burstSafe took is marked
// climb when a body steps up (to an attribute: its result may then be an
// ancestor copy's attribute, up when not atomized); one it refused is judged
// again here with climbing allowed.
func climbMark(sc *sheetCall, f *streamFunc, fns map[funcKey]*streamFunc, bodies map[*streamFunc]xpath.Expr) {
	for _, body := range bodies {
		if climbsAny(body) {
			sc.climb = true
		}
	}
	if sc.burst {
		if sc.climb && !sc.atomic {
			sc.up, sc.res = true, ckUp
		}
		return
	}
	for g := range bodies {
		if !plainParams(g.body) {
			return
		}
	}
	res, ok := climbClosure(f, fns, bodies)
	if !ok {
		return
	}
	sc.burst, sc.climb, sc.res = true, true, res
	sc.up = !sc.atomic && res >= ckUpAttr
	for _, body := range bodies {
		walkExprCalls(body, func(x *xpath.FuncCall) {
			if stylesheetFn(x.Name) {
				sc.keep = true
			}
		})
	}
}

// climbKeepsCall reports whether x, a call inlineCall could inline, is to
// stay a call (sheetCall.keep).
func climbKeepsCall(x *xpath.FuncCall) bool {
	sc, ok := sheetCallOf(x)
	return ok && sc.keep
}
