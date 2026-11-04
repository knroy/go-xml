package xslt

// Lane L2: a call on a stylesheet function whose body is a sequence
// constructor, planned and run as that body inside the caller's streamed
// plan (§19.8.5).
//
// The caller is an xsl:sequence or xsl:copy-of whose select holds one call
// f(P, A2..An) on a function of this module: P, the argument of the first
// parameter (§19.8.5: the streaming parameter, S), is the consuming path, at
// the caller's focus; the others, and what surrounds the call (post: the
// select with the call as $r), read only what the caller's focus view has.
// The body is planned by the caller's builder at the caller's focus, its
// consumers among the caller's ops, and compiled again (a copy of its
// instructions, distinct from the function's own) while the plan compiles,
// every split of it reading S as P (substS): S is referenced only where the
// body has no focus of its own (no for-each, for-each-group, iterate,
// analyze-string, merge or sort above it), at its expression's own focus,
// so P is evaluated at the caller's focus there, which is the call's: P read
// for S is P passed to S. The body's other top-level expressions read no
// focus at all (a function body has none: they would raise XPDY0002), and
// S's declared type converts P's nodes to themselves.
//
// At run time the instruction does what userFunction.call does -- the
// arguments converted, a frame of the globals and the parameters, no focus,
// the function context cleared, the result converted to the declared type --
// with the body's copy for the body, S unbound (a read of it the plan did
// not substitute fails, and the container runs on the tree), and then
// post's value is what the caller's instruction outputs. Run outside a plan
// it was compiled for (the tree's run, a re-run), it is the caller's
// instruction as compiled: the call itself.
//
// A function inlined once per plan: a call on it inside its own body stays a
// call (on values or copies, run on the tree as calls are).

import (
	"reflect"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// inlineR is post's variable: the call's value.
var inlineR = xdm.QName{URI: "urn:x-go-xml:inline", Local: "r"}

// inlinedCall is a call planned as its function's body (streamExec.inlines).
type inlinedCall struct {
	call   *xdm.Node // the caller's xsl:sequence or xsl:copy-of
	fn     *xdm.Node // the xsl:function
	param  xdm.QName // S
	nParam int
	clear  bool   // clear the function context (not in a streamed group's body)
	failed string // the body's compile failed: the plan is not ready
	// frame, for a call on the function from inside its own body, is the
	// call frame its body runs in (streamfn_frame.go).
	frame *streamExec
	// loop says the call's body runs once per match of its loop path, at
	// the match, the first parameter that match (streamfn_loop.go); one
	// that exactly one match is the tree's (XPTY0004 otherwise).
	loop, one bool
}

// inlineSubst is the compiler's substitution while an inlined body
// compiles (compiler.inline): S read as a fresh parse of P.
type inlineSubst struct {
	ic   *inlinedCall
	prev *inlineSubst
}

// inlineFunctionAttrs are the attributes of an xsl:function the inlined body
// does not change: no cache, collation, base or version of its own.
var inlineFunctionAttrs = map[string]bool{"name": true, "as": true, "streamability": true,
	"visibility": true, "override-extension-function": true, "override": true,
	"use-when": true, "exclude-result-prefixes": true}

// inlineCallOp plans el, a consuming xsl:sequence or xsl:copy-of, as the
// body of the stylesheet function its select calls, when it is one this
// takes; done false when el's select calls no function with such a body.
func (b *streamBuilder) inlineCallOp(el *xdm.Node, f vfocus) (done, ok bool) {
	if !isXSL(el, "sequence") && !isXSL(el, "copy-of") && !isXSL(el, "apply-templates") {
		return false, false
	}
	e := b.selectOf(el) // streamfn_frame.go: an enclosing body's parameter read as its argument
	root := documentElementOf(el)
	if e == nil || root == nil {
		return false, false
	}
	x, fn := inlineTarget(e, root)
	if x == nil {
		return false, false
	}
	if done, ok := b.loopCallOp(el, e, x, fn, f); done { // streamfn_loop.go
		return true, ok
	}
	if why := b.inlineCheck(el, e, x, fn, f); why != "" {
		if b.sub == nil {
			// Not inlined: the select is planned as before inlining
			// existed (the split's burst route runs the call itself on
			// values or grounded copies), refusing on its own what it
			// cannot take.
			return false, false
		}
		return true, b.refuse("call on " + x.Name.Local + "(): " + why)
	}
	params, body := splitFunction(fn)
	q, _ := paramName(params[0])
	ic := &inlinedCall{call: el, fn: fn, param: q, nParam: len(params), clear: !b.grp}
	if b.x.inlines == nil {
		b.x.inlines = map[*xdm.Node]*inlinedCall{}
	}
	top := b.x.rootPlan() // streamfn_frame.go
	if top.planning[fn] {
		// A call from inside the body being planned: its call frame.
		return true, b.frameCall(el, ic, body, f)
	}
	for _, other := range b.x.inlines {
		if other.fn == fn {
			return true, b.refuse("call on " + x.Name.Local + "(): inlined twice in one plan")
		}
	}
	b.x.inlines[el] = ic
	if top.planning == nil {
		top.planning = map[*xdm.Node]bool{}
	}
	top.planning[fn] = true
	prev := b.sub
	b.sub = ic
	ok = b.members(fn, body, f)
	b.sub = prev
	delete(top.planning, fn)
	if !ok {
		delete(b.x.inlines, el)
		return true, false
	}
	return true, true
}

// inlineTarget finds the one call in e on a function of root's module whose
// body is a sequence constructor of instructions (not K1's one-sequence
// shape, which the split reads as a call).
func inlineTarget(e xpath.Expr, root *xdm.Node) (*xpath.FuncCall, *xdm.Node) {
	var found *xpath.FuncCall
	var fn *xdm.Node
	n := 0
	walkExprCalls(e, func(c *xpath.FuncCall) {
		if !stylesheetFn(c.Name) {
			return
		}
		g := moduleFunction(root, c.Name, len(c.Args))
		if g == nil || sheetBody(g) != nil {
			return
		}
		n++
		found, fn = c, g
	})
	if n == 0 {
		return pkgTarget(e, root) // streampkgframe_l7.go: a used package's function
	}
	if n != 1 {
		return nil, nil
	}
	return found, fn
}

// moduleFunction is the one xsl:function of root's module named q with
// arity n; nil when there is none, more than one, or the module reaches
// others (whose precedence could pick another).
func moduleFunction(root *xdm.Node, q xdm.QName, n int) *xdm.Node {
	if reachesOtherModules(root) {
		return nil
	}
	var fn *xdm.Node
	count := 0
	walkElements(root, func(el *xdm.Node) bool {
		if !isXSL(el, "function") {
			return true
		}
		if fq, err := resolveQNameAttr(el, el.AttrValue("name")); err == nil &&
			fq.URI == q.URI && fq.Local == q.Local {
			ps, _ := splitFunction(el)
			if len(ps) == n {
				fn, count = el, count+1
			}
		}
		return false
	})
	if count != 1 {
		return nil
	}
	return fn
}

// splitFunction is fn's xsl:param elements and the elements after them.
func splitFunction(fn *xdm.Node) (params, body []*xdm.Node) {
	for _, ch := range fn.ChildElements() {
		if isXSL(ch, "param") && len(body) == 0 {
			params = append(params, ch)
			continue
		}
		body = append(body, ch)
	}
	return params, body
}

// inlineCheck says why el's call x on fn cannot be planned as fn's body at
// focus f, or "".
func (b *streamBuilder) inlineCheck(el *xdm.Node, e xpath.Expr, x *xpath.FuncCall, fn *xdm.Node, f vfocus) string {
	apply := isXSL(el, "apply-templates")
	for a := range el.Attrs() {
		if n := a.Name(); n.URI == "" && n.Local != "select" && !(apply && n.Local == "mode") {
			return "attribute " + n.Local
		}
	}
	if apply && len(el.ChildElements()) > 0 {
		return "xsl:with-param or xsl:sort on the apply"
	}
	rec := within(el, fn) // streamfn_frame.go: a call from inside fn's own body
	if rec && !insideFocusChange(el, fn) {
		return "a recursive call at the body's own focus"
	}
	for a := range fn.Attrs() {
		n := a.Name()
		if n.URI == xdm.NSXML || n.URI == "" && !inlineFunctionAttrs[n.Local] {
			return "xsl:function/@" + n.Local
		}
	}
	params, body := splitFunction(fn)
	if len(params) == 0 || len(params) != len(x.Args) || len(body) == 0 {
		return "arity"
	}
	for _, p := range params {
		if p.Attr("", "select") != nil || p.FirstChild() != nil {
			return "a parameter with a default"
		}
	}
	for ch := range fn.Children() {
		if ch.Kind() == xdm.KindText && strings.ContainsAny(ch.Value(), "{}") {
			return "text with a brace"
		}
	}
	s, ok := paramName(params[0])
	if !ok {
		return "parameter name"
	}
	p := x.Args[0]
	vc := viewCheck{key: b.key}
	if _, ctx := p.(*xpath.ContextItem); !(ctx && f == vElem) && vc.safe(p, f, true) {
		return "the first argument does not consume"
	}
	vars := false
	walkExprVars(p, func(*xpath.VarRef) { vars = true })
	if vars {
		return "the first argument reads a variable"
	}
	for _, a := range x.Args[1:] {
		if !vc.safe(a, f, false) {
			return "an argument other than the first is not motionless"
		}
	}
	post := replaceCall(e, x)
	if post == nil || !vc.safe(post, f, true) {
		return "what surrounds the call is not motionless"
	}
	if !streamParamTakes(params[0], p, b.grp, f) && !(rec && groupOfS(el, fn, s) && ofCurrentGroup(p)) &&
		!pkgDocParam(fn, params[0], p, f) { // streampkgframe_l7.go
		return "the first parameter's type converts " + p.String()
	}
	if why := inlineBody(fn, s, !b.grp, substAllowed); why != "" {
		return why
	}
	return ""
}

// replaceCall is e with its call x replaced by $r (in place); nil when x
// is not in e.
func replaceCall(e xpath.Expr, x *xpath.FuncCall) xpath.Expr {
	done := false
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) {
		if done || v.Kind() != reflect.Interface || !v.CanSet() {
			return
		}
		if c, ok := v.Interface().(*xpath.FuncCall); ok && c == x {
			v.Set(reflect.ValueOf(xpath.Expr(&xpath.VarRef{Name: inlineR})))
			done = true
		}
	})
	if !done {
		return nil
	}
	return e
}

// streamParamTakes reports whether S, the parameter p, takes the nodes of P
// unconverted: no type, item()* or node()*; element()* of a path whose last
// step selects only elements, or of a streamed group's members; and, of the
// context item (an element: the caller's focus is a view only of one),
// node(), element() or item() with any occurrence.
func streamParamTakes(p *xdm.Node, a xpath.Expr, grp bool, f vfocus) bool {
	t := strings.Join(strings.Fields(p.AttrValue("as")), "")
	if _, ctx := a.(*xpath.ContextItem); ctx {
		if f != vElem {
			return false
		}
		switch strings.TrimRight(t, "?*+") {
		case "", "item()", "node()", "element()":
			return true
		}
		return false
	}
	switch t {
	case "", "item()*", "node()*":
		return true
	case "element()*", namedElements(p, t):
		if grp && isCurrentGroup(a) && t == "element()*" {
			return true // a streamed group's members are elements
		}
		var last xpath.Expr = a
		if pe, ok := a.(*xpath.PathExpr); ok && len(pe.Steps) > 0 {
			last = pe.Steps[len(pe.Steps)-1]
		}
		st, ok := last.(*xpath.Step)
		if !ok || st.Axis != xpath.AxisChild && st.Axis != xpath.AxisDescendant {
			return false
		}
		nt, name := st.Test.(*xpath.NameTest)
		kt, kind := st.Test.(*xpath.KindTest)
		if t != "element()*" {
			return name && sameElementName(p, t, nt) // streamfn_frame.go
		}
		return name || kind && kt.Kind == xdm.KindElement && !kt.Any
	}
	return false
}

// focusChanging are the instructions whose content, and whose attributes
// other than select, have a focus of their own.
var focusChanging = map[string]bool{"for-each": true, "for-each-group": true, "iterate": true,
	"analyze-string": true, "merge": true, "merge-source": true, "sort": true, "key": true}

// inlineBody checks fn's body: S (s) read only at the body's own focus, at
// its expressions' own; nothing there reading a focus; and, with clear
// false (a streamed group's body, whose current group the run keeps),
// nothing that could read the group the call would have cleared.
func inlineBody(fn *xdm.Node, s xdm.QName, clear bool, allowS func(el *xdm.Node, name string, e xpath.Expr) bool) string {
	why := ""
	var walk func(el *xdm.Node, inner bool)
	walk = func(el *xdm.Node, inner bool) {
		for _, ch := range el.ChildElements() {
			if why != "" {
				return
			}
			if isXSL(ch, "param") && el == fn {
				continue
			}
			if !clear && ch.Name().URI == xdm.NSXSL {
				switch ch.Name().Local {
				case "evaluate":
					why = "xsl:" + ch.Name().Local + " in a group's body"
					return
				}
			}
			own := inner || isXSL(ch, "sort")
			for a := range ch.Attrs() {
				n := a.Name()
				if n.URI != "" {
					continue
				}
				aInner := own || focusChanging[ch.Name().Local] && ch.Name().URI == xdm.NSXSL && n.Local != "select"
				if w := inlineAttr(ch, n.Local, a.Value(), s, aInner, clear, allowS); w != "" {
					why = w
					return
				}
			}
			walk(ch, own || ch.Name().URI == xdm.NSXSL && focusChanging[ch.Name().Local])
		}
	}
	walk(fn, false)
	return why
}

// inlineAttr checks one attribute of el in the body.
func inlineAttr(el *xdm.Node, name, src string, s xdm.QName, inner, clear bool, allowS func(*xdm.Node, string, xpath.Expr) bool) string {
	xsl := el.Name().URI == xdm.NSXSL
	var exprs []string
	switch {
	case xsl && (name == "match" || name == "count" || name == "from" ||
		name == "group-starting-with" || name == "group-ending-with"):
		if strings.Contains(src, "$") && strings.Contains(src, s.Local) {
			return "the first parameter in a pattern"
		}
		return ""
	case xsl && (name == "select" || name == "test" || name == "group-by" || name == "group-adjacent" ||
		name == "value" || name == "start-at" || name == "use"):
		exprs = []string{src}
	case xsl && (name == "as" || name == "name" && (isXSL(el, "variable") || isXSL(el, "param") ||
		isXSL(el, "with-param") || isXSL(el, "call-template")) || name == "mode" || name == "tunnel" ||
		name == "required" || name == "static" || name == "visibility" || name == "use-when" ||
		name == "streamable" || name == "use-attribute-sets" || name == "exclude-result-prefixes" ||
		name == "extension-element-prefixes" || name == "xpath-default-namespace" ||
		name == "expand-text" || name == "version" || name == "default-collation" || name == "composite"):
		return ""
	default:
		var ok bool
		if exprs, ok = avtExpressions(src); !ok {
			return "attribute " + name
		}
	}
	for _, src := range exprs {
		e := parseAt(el, src)
		if e == nil {
			return "an expression that does not parse"
		}
		if inner {
			if mentionsVar(e, s) {
				return "the first parameter where the body has a focus of its own"
			}
			continue
		}
		if !focusFree(e) {
			return "an expression reading the focus a function body has not: " + src
		}
		if mentionsVar(e, s) && !allowS(el, name, e) { // streamfn_frame.go, streamfn_loop.go
			return "the first parameter outside a select the plan substitutes: " + src
		}
		if !clear && readsFunctionContext(e) {
			return "current-group() or the like in a group's body"
		}
		bad := false
		substS(e, s, func() xpath.Expr { return &xpath.ContextItem{} }, &bad)
		if bad {
			return "the first parameter away from its expression's focus: " + src
		}
	}
	return ""
}

func mentionsVar(e xpath.Expr, s xdm.QName) bool {
	found := false
	walkExprVars(e, func(v *xpath.VarRef) {
		if v.Name.URI == s.URI && v.Name.Local == s.Local {
			found = true
		}
	})
	return found
}

// readsFunctionContext reports whether e calls current-group(),
// current-grouping-key(), regex-group() or current-merge-group/-key().
func readsFunctionContext(e xpath.Expr) bool {
	found := false
	walkExprCalls(e, func(c *xpath.FuncCall) {
		if c.Name.URI == fnNS {
			switch c.Name.Local {
			case "current-group", "current-grouping-key", "regex-group",
				"current-merge-group", "current-merge-key":
				found = true
			}
		}
	})
	return found
}

// substS returns e with each reference to s replaced by mk(), where the
// reference is at e's own focus; bad is set when one is elsewhere (a
// predicate, a later step, the right of "!", an inline function), or s is
// bound again inside e.
func substS(e xpath.Expr, s xdm.QName, mk func() xpath.Expr, bad *bool) xpath.Expr {
	isS := func(q xdm.QName) bool { return q.URI == s.URI && q.Local == s.Local }
	var re func(e xpath.Expr, top bool) xpath.Expr
	res := func(es []xpath.Expr, top bool) {
		for i := range es {
			es[i] = re(es[i], top)
		}
	}
	re = func(e xpath.Expr, top bool) xpath.Expr {
		if *bad || e == nil {
			return e
		}
		switch x := e.(type) {
		case *xpath.VarRef:
			if isS(x.Name) {
				if !top {
					*bad = true
					return e
				}
				return mk()
			}
			return e
		case *xpath.Literal, *xpath.ContextItem, *xpath.NamedFunctionRef:
			return e
		case *xpath.Step:
			res(x.Predicates, false)
			return e
		case *xpath.PathExpr:
			for i := range x.Steps {
				x.Steps[i] = re(x.Steps[i], top && i == 0 && !x.Root)
			}
			if len(x.Steps) > 0 && !x.Root {
				// $s/rest with P a path: one path.
				switch a := x.Steps[0].(type) {
				case *xpath.PathExpr:
					x.Root = a.Root
					x.Steps = append(append([]xpath.Expr(nil), a.Steps...), x.Steps[1:]...)
				case *xpath.Step:
					x.Steps = append([]xpath.Expr{a}, x.Steps[1:]...)
				}
			}
			return e
		case *xpath.FilterExpr:
			x.Base = re(x.Base, top)
			res(x.Predicates, false)
			return e
		case *xpath.SimpleMap:
			x.Left, x.Right = re(x.Left, top), re(x.Right, false)
			return e
		case *xpath.FuncCall:
			res(x.Args, top)
			return e
		case *xpath.BinaryOp:
			x.Left, x.Right = re(x.Left, top), re(x.Right, top)
			return e
		case *xpath.UnaryOp:
			x.Operand = re(x.Operand, top)
			return e
		case *xpath.StringConcat:
			x.Left, x.Right = re(x.Left, top), re(x.Right, top)
			return e
		case *xpath.CastExpr:
			x.Operand = re(x.Operand, top)
			return e
		case *xpath.TreatExpr:
			x.Operand = re(x.Operand, top)
			return e
		case *xpath.InstanceOfExpr:
			x.Operand = re(x.Operand, top)
			return e
		case *xpath.IfExpr:
			x.Cond, x.Then, x.Else = re(x.Cond, top), re(x.Then, top), re(x.Else, top)
			return e
		case *xpath.SequenceExpr:
			res(x.Items, top)
			return e
		case *xpath.MapConstructor:
			res(x.Keys, top)
			res(x.Values, top)
			return e
		case *xpath.ArrayConstructor:
			res(x.Members, top)
			return e
		case *xpath.LetExpr, *xpath.ForExpr, *xpath.QuantifiedExpr:
			var bs []xpath.Binding
			switch y := x.(type) {
			case *xpath.LetExpr:
				bs = y.Bindings
			case *xpath.ForExpr:
				bs = y.Bindings
			case *xpath.QuantifiedExpr:
				bs = y.Bindings
			}
			for i := range bs {
				if isS(bs[i].Var) {
					*bad = true
					return e
				}
				bs[i].Seq = re(bs[i].Seq, top)
			}
			switch y := x.(type) {
			case *xpath.LetExpr:
				y.Return = re(y.Return, top)
			case *xpath.ForExpr:
				y.Return = re(y.Return, top)
			case *xpath.QuantifiedExpr:
				y.Test = re(y.Test, top)
			}
			return e
		}
		if mentionsVar(e, s) {
			*bad = true
		}
		return e
	}
	return re(e, true)
}

// apply is the compiler's substitution of S in e, a split's parse
// (inlineCalls): each enclosing inlined body's, innermost first.
func (s *inlineSubst) apply(e xpath.Expr) xpath.Expr {
	for ; s != nil; s = s.prev {
		ic := s.ic
		bad := false
		e2 := substS(e, ic.param, func() xpath.Expr { return inlineArg(ic) }, &bad)
		if !bad {
			e = e2
			if ic.loop {
				e = dropContextMaps(e) // streamfn_loop.go
			}
		}
	}
	return e
}

// inlineArg is a fresh parse of P, the first argument of ic's call.
func inlineArg(ic *inlinedCall) xpath.Expr {
	if ic.loop {
		return &xpath.ContextItem{} // each match, the body's focus (streamfn_loop.go)
	}
	e := parseAt(ic.call, ic.call.AttrValue("select"))
	root := documentElementOf(ic.call)
	if e == nil || root == nil {
		return &xpath.VarRef{Name: ic.param} // unbound: the run fails, the tree answers
	}
	x, _ := inlineTarget(e, root)
	if x == nil {
		return &xpath.VarRef{Name: ic.param}
	}
	return x.Args[0]
}

// readyInlines says why a plan's inlined body did not compile, or "".
func (x *streamExec) readyInlines() string {
	for _, ic := range x.inlines {
		if ic.failed != "" {
			return ic.failed
		}
	}
	return x.readyFrames() // streamfn_frame.go
}

// inlineInstrFor compiles n, a caller's xsl:sequence or xsl:copy-of whose
// call a plan being compiled takes as its function's body, into an
// inlineCallInstr; nil when no plan does (orig, the instruction n compiled
// to, stands). sel is n's select, written where ns resolves names.
func (c *compiler) inlineInstrFor(n *xdm.Node, orig Instruction, sel *xpath.Compiled, ns xpath.NamespaceResolver) Instruction {
	plans := map[*streamExec]bool{}
	frames := map[*streamExec]bool{}
	var ic *inlinedCall
	for _, x := range append([]*streamExec{c.stream}, c.ruleExecs...) {
		if x == nil {
			continue
		}
		if v := x.inlines[n]; v != nil {
			plans[x], ic = true, v
			if v.frame != nil {
				frames[v.frame] = true
			}
		}
	}
	if ic == nil {
		return nil
	}
	fail := func(why string) Instruction {
		for x := range plans {
			x.inlines[n].failed = "inlined call: " + why
		}
		return nil
	}
	e := reparse(sel, ns)
	root := documentElementOf(n)
	if e == nil || root == nil {
		return fail("select does not parse again")
	}
	x, _ := inlineTarget(e, root)
	if x == nil {
		return fail("the call is not found again")
	}
	in := &inlineCallInstr{orig: orig, plans: plans, baseURI: ic.fn.BaseURI(), clear: ic.clear}
	if q, err := resolveQNameAttr(ic.fn, ic.fn.AttrValue("name")); err == nil {
		in.name = q
	}
	part := func(y xpath.Expr) *xpath.Compiled { return xpathleaf.WithExpr(sel, y).(*xpath.Compiled) }
	in.args = make([]*xpath.Compiled, len(x.Args))
	for k := 1; k < len(x.Args); k++ {
		in.args[k] = part(x.Args[k])
	}
	var loopPath xpath.Expr
	if ic.loop {
		if sm, ok := e.(*xpath.SimpleMap); ok && sm.Right == xpath.Expr(x) {
			loopPath, e = sm.Left, x // Q ! f(.): the map's value is the loop's
		} else {
			loopPath = x.Args[0]
		}
	}
	post := replaceCall(e, x)
	if post == nil {
		return fail("the call is not found again")
	}
	in.post = part(post)
	defer c.pkgScope(ic.fn)() // streampkgframe_l7.go: a used package's body in its own compile state
	params, _ := splitFunction(ic.fn)
	for _, p := range params {
		v, err := c.compileVariable(p)
		if err != nil {
			return fail(err.Error())
		}
		in.params = append(in.params, v)
	}
	if as := ic.fn.AttrValue("as"); as != "" {
		t, err := compileSequenceType(as, newNSResolver(ic.fn, ""))
		if err != nil {
			return fail(err.Error())
		}
		in.returns = t
	}
	switch o := orig.(type) {
	case *sequenceInstr:
		in.seq = o
	case *copyOfInstr:
		in.copyOf = o
	case *applyTemplatesInstr:
		in.apply = o
	default:
		return fail("caller")
	}
	if ic.loop {
		if why := c.loopOps(n, in, sel, part(loopPath), ns, plans); why != "" { // streamfn_loop.go
			return fail(why)
		}
		in.loop, in.one = true, ic.one
	}
	if len(frames) > 0 {
		// A recursive call: its frame's body, compiled once per call site
		// (streamfn_frame.go), the frame's own call to it included.
		if len(frames) != len(plans) {
			return fail("a call both framed and not")
		}
		code, why := c.frameCodeFor(ic, frames, len(params))
		if why != "" {
			return fail(why)
		}
		in.code = code
		return in
	}
	prev := c.inline
	c.inline = &inlineSubst{ic: ic, prev: prev}
	body, err := c.compileSequenceFrom(ic.fn, ic.fn, len(params))
	c.inline = prev
	if err != nil {
		return fail(err.Error())
	}
	in.body = body
	return in
}

// inlineCallInstr is a caller's instruction whose call runs as the inlined
// body in the plans it was compiled for, and as compiled (orig) elsewhere.
type inlineCallInstr struct {
	orig    Instruction
	seq     *sequenceInstr
	copyOf  *copyOfInstr
	apply   *applyTemplatesInstr // the call's value applied to, off the cursor
	code    *frameCode           // a recursive call's: its frame's body
	loop    bool                 // the body once per match of the loop path (streamfn_loop.go)
	one     bool                 // and exactly one match
	plans   map[*streamExec]bool
	name    xdm.QName
	params  []*Variable
	args    []*xpath.Compiled // [0] nil: the streaming parameter's is P
	post    *xpath.Compiled
	body    []Instruction
	returns *sequenceType
	baseURI string
	clear   bool
}

func (i *inlineCallInstr) Execute(rt *runtime, out *outputBuilder) error {
	f := rt.bursts
	if f == nil || f.run == nil || f.run.off {
		return i.orig.Execute(rt, out)
	}
	if !i.plannedIn(f.run.x) { // streamfn_frame.go
		// A streamed run this was not compiled for: decline, the
		// container runs on the tree.
		return errStreamPlan
	}
	body := i.body
	if i.code != nil {
		if !i.code.done {
			return errStreamPlan
		}
		body = i.code.body
	}
	vals := make([]xdm.Sequence, len(i.params))
	for k := 1; k < len(i.params); k++ {
		v, err := i.args[k].Eval(rt.ctx)
		if err != nil {
			return err
		}
		vals[k] = v
	}
	if i.loop {
		return i.executeLoop(rt, out, f, vals, body) // streamfn_loop.go
	}
	res, err := i.run(rt, vals, body, nil, false)
	if err != nil {
		return err
	}
	return i.output(rt, out, f, res)
}

// output writes post's value over res, the call's, as the caller's
// instruction does.
func (i *inlineCallInstr) output(rt *runtime, out *outputBuilder, f *burstFeed, res xdm.Sequence) error {
	v, err := i.post.Eval(rt.ctx.WithVar(inlineR, res))
	if err != nil {
		return err
	}
	if i.apply != nil {
		// The tree's dispatch over the call's value, as an apply over
		// items not of the stream runs (executeForeign).
		run := f.run
		off := run.off
		run.off = true
		err := i.apply.applySelected(rt, out, v)
		run.off = off
		return err
	}
	if i.copyOf != nil {
		for _, it := range xdm.Flatten(v) {
			if err := i.copyOf.copyItem(rt, out, it); err != nil {
				return err
			}
		}
		return nil
	}
	for _, it := range v {
		if err := i.seq.appendItem(rt, out, it); err != nil {
			return err
		}
	}
	return nil
}

// run is userFunction.call over the inlined body, the streaming parameter
// left unbound.
func (i *inlineCallInstr) run(rt *runtime, vals []xdm.Sequence, body []Instruction, s0 xdm.Sequence, bind0 bool) (xdm.Sequence, error) {
	defer rt.leaveFrame(rt.enterFrame())
	sub := rt.globalBindings()
	for k, p := range i.params {
		if k == 0 && !bind0 {
			continue
		}
		val := vals[k]
		if k == 0 {
			val = s0
		}
		v, err := p.asType.convertAsLazy(val, func() string {
			return "parameter $" + p.Name.Lexical() + " of " + i.name.Lexical()
		}, funcParamCode(rt.sheet))
		if err != nil {
			return nil, err
		}
		sub = sub.bindLocal(p.Name, v)
	}
	sub = sub.withFocus(nil, 0, 0)
	if i.baseURI != "" {
		sub.ctx = sub.ctx.WithStaticBaseURI(i.baseURI)
	}
	if i.clear {
		sub = sub.clearFunctionContext()
	}
	if err := sub.descend(); err != nil {
		return nil, err
	}
	defer sub.ascend()
	sub.temporary = true
	if rl, ok := sub.ctx.Funcs.(restrictedLibrary); ok {
		c := *sub.ctx
		c.Funcs = rl.unrestrict()
		sub.ctx = &c
	}
	sub = sub.withOutputURI("")
	tmp := newOutputBuilder(rt)
	if err := execSequence(body, sub, tmp); err != nil {
		return nil, err
	}
	return i.returns.convertAsLazy(tmp.Sequence(),
		func() string { return "result of " + i.name.Lexical() }, "XTTE0780")
}

// foreignArgs reports whether every argument of x, a call on a stylesheet
// function, is foreign (streamsplit_union.go): values, or a variable's
// nodes, which a streamed body's variables hold grounded (§19.8.4.41), never
// the stream's. A function has no focus, so its body then reads nothing of
// the stream, whatever it is: the call is motionless at any focus, as one
// in the inlined body's recursion on copies is (mf:group($group-copy[...])).
func foreignArgs(x *xpath.FuncCall) bool {
	for _, a := range x.Args {
		if !foreign(a) || refsSplitItems(a) || readsFunctionContext(a) { // the stream's own nodes
			return false
		}
	}
	return true
}

// paramAtomics is, per xsl:param of fn, whether its type is atomic.
func paramAtomics(fn *xdm.Node) []bool {
	var out []bool
	for _, p := range fn.ChildElements() {
		if isXSL(p, "param") {
			out = append(out, atomicAs(p, p.AttrValue("as")))
		}
	}
	return out
}
