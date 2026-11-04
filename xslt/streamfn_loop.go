package xslt

// Lane L2: calls whose first parameter takes one node, run as a loop over
// the argument's matches (su-absorbing-204: mf:check(*) and its recursion
// $input!*!mf:check(.); su-shallow-descent's f(/BOOKLIST)).
//
// Two shapes of an xsl:sequence or xsl:copy-of select:
//
//   - f(P, A2..An), the first parameter S declared node(), element(),
//     element(N) or item() (exactly one), P a consuming path: the body runs
//     at P's match, S that match; a second match, or none, is the tree's
//     XPTY0004, so the run declines (errStreamPlan: the container runs on
//     the tree, which raises it) -- the body's output is held until the
//     path has been read to its end, so nothing is written first;
//   - Q ! f(., A2..An), the select exactly that map: f once per match of Q,
//     in order, their values concatenated, which is the map; S of a type a
//     single element meets (none, item(), node(), element() with any
//     occurrence; element(N) when Q's last step tests N), A2..An free of the
//     focus (the map would evaluate them per item).
//
// The loop is a loop op of the caller's table (matchBody: the body at the
// match's start tag, its consumers reading the match off the cursor), its
// body planned at the match's focus with S read as "." (dropContextMaps
// makes $S!R plain R), S bound to the match's view while the body runs, so
// that what the body reads of S beside its consumers is what the view has --
// which the plan checks: every expression reading S at the body's own focus
// is a consumer's select or, read with S as ".", motionless on a view. A
// call on f from inside its own body is a call frame (streamfn_frame.go):
// the site's loop op has its body in the frame's table (op.frame), readied
// once there (readyFrames), the loop itself readied as matchBody when its
// matches cannot nest (readyFrameLoop).

import (
	"reflect"
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// loopCallOp plans el's call x on fn as a loop, when its shape is one;
// done false when it is not (item 4's inliner then decides).
func (b *streamBuilder) loopCallOp(el *xdm.Node, e xpath.Expr, x *xpath.FuncCall, fn *xdm.Node, f vfocus) (done, ok bool) {
	params, body := splitFunction(fn)
	if len(params) == 0 || len(params) != len(x.Args) {
		return false, false
	}
	t := strings.Join(strings.Fields(params[0].AttrValue("as")), "")
	var loopPath xpath.Expr
	mapLoop, one := false, false
	if sm, isMap := e.(*xpath.SimpleMap); isMap && sm.Right == xpath.Expr(x) && isContextItem(x.Args[0]) {
		mapLoop, loopPath = true, sm.Left
	} else if singleNodeType(t) && !isContextItem(x.Args[0]) {
		loopPath, one = x.Args[0], true
	} else {
		return false, false
	}
	refuse := func(why string) (bool, bool) { return true, b.refuse("call on " + x.Name.Local + "(): " + why) }
	if !isXSL(el, "sequence") && !isXSL(el, "copy-of") {
		return refuse("a loop call outside xsl:sequence or xsl:copy-of")
	}
	for a := range el.Attrs() {
		if n := a.Name(); n.URI == "" && n.Local != "select" {
			return refuse("attribute " + n.Local)
		}
	}
	if why := fnShapeWhy(fn, params, body); why != "" {
		return refuse(why)
	}
	s, nameOK := paramName(params[0])
	if !nameOK {
		return refuse("parameter name")
	}
	vc := viewCheck{key: b.key}
	if vc.safe(loopPath, f, true) {
		return refuse("the loop path does not consume")
	}
	vars := false
	walkExprVars(loopPath, func(*xpath.VarRef) { vars = true })
	if vars {
		return refuse("the loop path reads a variable")
	}
	for _, a := range x.Args[1:] {
		if !vc.safe(a, f, false) || mapLoop && !focusFree(a) {
			return refuse("an argument other than the first is not motionless")
		}
	}
	if !mapLoop {
		post := replaceCall(e, x)
		if post == nil || !vc.safe(post, f, true) {
			return refuse("what surrounds the call is not motionless")
		}
	}
	if !loopTypeOK(params[0], t, loopPath, mapLoop) {
		return refuse("the first parameter's type " + t)
	}
	allow := func(at *xdm.Node, name string, ex xpath.Expr) bool {
		if name == "select" && substSite(at) {
			if r := b.pl.rec(at); r != nil && r.props.sweep == sweepConsuming {
				return true
			}
		}
		bad := false
		e2 := dropContextMaps(substS(ex, s, func() xpath.Expr { return &xpath.ContextItem{} }, &bad))
		return !bad && (viewCheck{}).safe(e2, vElem, true)
	}
	if why := inlineBody(fn, s, true, allow); why != "" {
		return refuse(why)
	}
	ic := &inlinedCall{call: el, fn: fn, param: s, nParam: len(params), clear: true, loop: true, one: one}
	op := &streamOp{x: b.x, el: el, doc: f == vNone, loop: true, inGroup: b.grp, key: b.key, planned: true}
	if b.x.inlines == nil {
		b.x.inlines = map[*xdm.Node]*inlinedCall{}
	}
	top := b.x.rootPlan()
	if top.planning[fn] {
		fx, why := b.frameFor(el, ic, body, vElem, false)
		if fx == nil {
			return refuse(why)
		}
		op.frame, ic.frame = fx, fx
	} else {
		for _, other := range b.x.inlines {
			if other.fn == fn {
				return refuse("inlined twice in one plan")
			}
		}
		if top.planning == nil {
			top.planning = map[*xdm.Node]bool{}
		}
		top.planning[fn] = true
		inner := &streamBuilder{pl: b.pl, x: b.x, ops: &op.inner, kinds: &op.kinds, key: b.key, sub: ic}
		planned := inner.members(fn, body, vElem)
		delete(top.planning, fn)
		if !planned {
			return refuse(inner.why)
		}
	}
	b.x.inlines[el] = ic
	b.x.ops[el] = op
	*b.ops = append(*b.ops, op)
	return true, true
}

// fnShapeWhy says why fn's declaration cannot be inlined, or "".
func fnShapeWhy(fn *xdm.Node, params, body []*xdm.Node) string {
	for a := range fn.Attrs() {
		n := a.Name()
		if n.URI == xdm.NSXML || n.URI == "" && !inlineFunctionAttrs[n.Local] {
			return "xsl:function/@" + n.Local
		}
	}
	if len(body) == 0 {
		return "no body"
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
	return ""
}

// singleNodeType reports whether t takes exactly one node: node(),
// element(), element(N) or item().
func singleNodeType(t string) bool {
	switch t {
	case "node()", "element()", "item()":
		return true
	}
	return strings.HasPrefix(t, "element(") && strings.HasSuffix(t, ")") && !strings.Contains(t, ",") &&
		t != "element(*)"
}

// loopTypeOK reports whether the first parameter p (type t) takes each
// match of the loop path as it is: one element.
func loopTypeOK(p *xdm.Node, t string, path xpath.Expr, mapLoop bool) bool {
	base := strings.TrimRight(t, "?*+")
	if !mapLoop && t != base {
		return false // a call's argument must be exactly one node
	}
	switch base {
	case "", "item()", "node()", "element()":
		return mapLoop || base != ""
	}
	if !strings.HasPrefix(base, "element(") {
		return false
	}
	var last xpath.Expr = path
	if pe, ok := path.(*xpath.PathExpr); ok && len(pe.Steps) > 0 {
		last = pe.Steps[len(pe.Steps)-1]
	}
	st, ok := last.(*xpath.Step)
	if !ok || st.Axis != xpath.AxisChild && st.Axis != xpath.AxisDescendant {
		return false
	}
	nt, _ := st.Test.(*xpath.NameTest)
	return sameElementName(p, base+"*", nt)
}

// dropContextMaps rewrites ". ! R" as R, where R reads no position: with
// the first parameter read as the match, $S!R is the match's R.
func dropContextMaps(e xpath.Expr) xpath.Expr {
	if e == nil {
		return nil
	}
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) {
		if v.Kind() != reflect.Interface || !v.CanSet() {
			return
		}
		for {
			sm, ok := v.Interface().(*xpath.SimpleMap)
			if !ok || !isContextItem(sm.Left) || callsPosition(sm.Right) {
				return
			}
			v.Set(reflect.ValueOf(sm.Right))
		}
	})
	return e
}

// callsPosition reports whether e calls position() or last().
func callsPosition(e xpath.Expr) bool {
	found := false
	walkExprCalls(e, func(c *xpath.FuncCall) {
		if c.Name.URI == fnNS && (c.Name.Local == "position" || c.Name.Local == "last") {
			found = true
		}
	})
	return found
}

// readyFrameLoop readies op, a loop whose body is a call frame's (readied
// by readyFrames): the body runs at each match's start tag, which matches
// that may nest cannot have.
func (x *streamExec) readyFrameLoop(op *streamOp, kinds *[streamtrace.NumKinds]int64) string {
	s := op.split
	if s == nil {
		return "a recursive call's loop not compiled"
	}
	if s.nests && !(s.outermost && !s.predFail) {
		return "a recursive call's matches may nest"
	}
	s.mode = matchBody
	kinds[streamtrace.KindForEach]++
	return ""
}

// executeLoop runs i, a loop call: the body once per match of the loop
// path, S the match, at its start tag; then post over the values.
func (i *inlineCallInstr) executeLoop(rt *runtime, out *outputBuilder, f *burstFeed, vals []xdm.Sequence, body []Instruction) error {
	feed := rt.burstsFor(i)
	if feed == nil || feed.read == nil {
		return errStreamPlan
	}
	var res xdm.Sequence
	n := 0
	err := feed.read(func(it xdm.Item) error {
		n++
		if i.one && n > 1 {
			return errStreamPlan // the tree's XPTY0004: two nodes for one
		}
		r, err := i.run(rt, vals, body, xdm.One(it), true)
		if err != nil {
			return err
		}
		res = append(res, r...)
		return nil
	})
	if err != nil {
		return err
	}
	if i.one && n != 1 {
		return errStreamPlan // the tree's XPTY0004: no node for one
	}
	return i.output(rt, out, f, res)
}

// loopOps compiles the loop of in, a loop call at n, in each plan: the
// path's split, a bare path, the op's instruction in.
func (c *compiler) loopOps(n *xdm.Node, in *inlineCallInstr, sel, path *xpath.Compiled, ns xpath.NamespaceResolver, plans map[*streamExec]bool) string {
	for x := range plans {
		op := x.ops[n]
		if op == nil || !op.loop {
			return "the loop call has no loop op"
		}
		// path's source is the whole select (WithExpr keeps it), so a
		// split of it would read the call: the loop path of a fresh parse.
		lp := freshLoopPath(n, sel, ns)
		if lp == nil {
			return "the loop path is not found again"
		}
		e, checks := foreignSets(c.inlineCalls(lp))
		split, dead := splitStreamIn(e, path, op.doc, op.inGroup, op.key)
		if split != nil {
			for _, ck := range checks {
				split.checks = append(split.checks, xpathleaf.WithExpr(path, ck).(*xpath.Compiled))
			}
		} else {
			dead = "split: " + dead
		}
		if dead == "" && !split.bare() {
			dead = "the loop path is not a bare path"
		}
		if dead != "" {
			return dead
		}
		op.instr, op.sel, op.split = in, path, split
		x.byInstr[in] = op
	}
	return ""
}

// freshLoopPath is the loop path of n's select sel, parsed again: Q of
// Q ! f(.), or the call's first argument.
func freshLoopPath(n *xdm.Node, sel *xpath.Compiled, ns xpath.NamespaceResolver) xpath.Expr {
	e := reparse(sel, ns)
	root := documentElementOf(n)
	if e == nil || root == nil {
		return nil
	}
	x, _ := inlineTarget(e, root)
	if x == nil {
		return nil
	}
	if sm, ok := e.(*xpath.SimpleMap); ok && sm.Right == xpath.Expr(x) {
		return sm.Left
	}
	return x.Args[0]
}

// loopRead is e, an expression of a loop call's body (b.sub), with S read
// as "." (S is the match's view while the body runs, and its focus); e
// itself elsewhere.
func (b *streamBuilder) loopRead(e xpath.Expr) xpath.Expr {
	if e == nil || b.sub == nil || !b.sub.loop {
		return e
	}
	bad := false
	e2 := dropContextMaps(substS(e, b.sub.param, func() xpath.Expr { return &xpath.ContextItem{} }, &bad))
	if bad {
		return e
	}
	return e2
}
