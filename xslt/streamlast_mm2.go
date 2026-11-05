package xslt

// Lane MM2: fn:last() in the body of a motionless xsl:for-each.
//
// A motionless for-each's select reads only what the focus's view has
// (ancestor-or-self::node(), @*, a variable's items): the tree's
// xsl:for-each evaluates it whole, then runs the body once per item with
// position() and last() of that sequence. last() there is the loop's,
// known before the body starts; it is not the rule's (the size of the
// apply-templates population, which the stream does not know).

import (
	"reflect"
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// loopContent checks the body of el, a motionless xsl:for-each, at an
// element's view, with fn:last() of the focus admitted.
func (b *streamBuilder) loopContent(el *xdm.Node) bool {
	old := b.last
	b.last = true
	ok := b.content(el, vElem)
	b.last = old
	return ok
}

// leafNext checks el when it is an xsl:next-match or xsl:apply-imports in
// the body of a rule for a text, comment, PI or attribute node (focus
// vLeaf, run off the cursor on the complete node). The tree's instruction
// hands that node, with its focus, to the next rule of the current mode:
// planRules checks every rule of that mode for the node's class, and the
// built-in rules, as for an xsl:apply-templates of it. Not at the top of the
// result, where a built-in's copy would stand for the stream's node (the
// tree keeps the source's own there).
func (b *streamBuilder) leafNext(el *xdm.Node, f vfocus) (done, ok bool) {
	if !isXSL(el, "next-match") && !isXSL(el, "apply-imports") || f != vLeaf || !b.x.rule {
		return false, false
	}
	if topOf(el) != "" {
		return true, b.refuse(elName(el) + " at the top of a leaf rule's result")
	}
	return true, b.applyAttrs(el, f) && b.withParams(el, f)
}

// copiesOnly reports whether the content of b, an xsl:variable with @as
// and no @select, is xsl:copy-of instructions alone: its nodes are the
// copies they make, new trees, never a node of the stream.
func copiesOnly(b *xdm.Node) bool {
	n := 0
	for ch := range b.Children() {
		switch ch.Kind() {
		case xdm.KindText:
			if strings.TrimSpace(ch.Value()) != "" {
				return false
			}
		case xdm.KindElement:
			if !isXSL(ch, "copy-of") {
				return false
			}
			n++
		}
	}
	return n > 0
}

// foreignParams checks the xsl:with-params of el, an xsl:apply-templates
// over items not of the stream (applyForeign): motionless, or one whose
// value is grounded (§19.8.4.19: copy-of(.), snapshot(...)), read off the
// cursor as an expression op before the tree's rules run (executeForeign)
// (si-apply-templates-006). A copy is valid wherever the rules take it.
func (b *streamBuilder) foreignParams(el *xdm.Node, f vfocus) bool {
	var op *streamOp
	for ch := range el.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "",
			ch.Kind() == xdm.KindComment, ch.Kind() == xdm.KindPI:
			continue
		case !isXSL(ch, "with-param"):
			return b.refuse(elName(ch) + " in " + elName(el))
		}
		// As withParams checks them: a select refused there plans nothing.
		if sb := b.strict(); sb.attrs(ch, f) && sb.content(ch, f) {
			continue
		}
		// In a group the parameter would be read at the group's start,
		// before its members (TestStreamValuesApplyTakesNoParams).
		if op != nil || b.grp || ch.Attr("", "select") == nil || !b.groundedSelect(ch) { // streamgroup.go
			return b.refuse("consuming xsl:with-param of " + elName(el) + ": not grounded")
		}
		for a := range ch.Attrs() {
			if n := a.Name(); n.URI == "" && !streamAttrsOK[n.Local] {
				return b.refuse("attribute " + n.Local + " of xsl:with-param")
			}
		}
		op = &streamOp{x: b.x, el: ch, doc: f == vNone, inGroup: b.grp, key: b.key}
	}
	if op != nil {
		*b.ops = append(*b.ops, op)
		b.x.exprOps = append(b.x.exprOps, op)
		b.kinds[streamtrace.KindSelect]++
	}
	return true
}

// currentFilter reports whether x, a filter of items not of the stream
// whose predicates call fn:current(), reads only what the focus f has: the
// predicates read their items as they like (burstSafe), and each call of
// current() starts a path that vc admits at f, the instruction's focus
// (si-iterate-036: $highest[@salary ge current()/@salary]).
func currentFilter(vc viewCheck, x *xpath.FilterExpr, f vfocus) bool {
	if !constructorFocusFree(x.Base) || !burstSafe(x.Base, false) || refsSplitItems(x.Base) { // streamsplit_fns.go
		return false
	}
	vc.curOK, vc.cur = true, f
	for _, p := range x.Predicates {
		if !burstSafe(p, false) {
			return false
		}
		calls, ok := 0, true
		exprWalk(reflect.ValueOf(&p).Elem(), func(v reflect.Value) { // streamfork.go
			switch y := v.Interface().(type) {
			case *xpath.FuncCall:
				if isCurrentCall(y) {
					calls--
				}
			case *xpath.PathExpr:
				if c, isCall := y.Steps[0].(*xpath.FuncCall); isCall && isCurrentCall(c) && !y.Root {
					calls++
					ok = ok && vc.safe(y, f, false)
				}
			}
		})
		if !ok || calls != 0 {
			return false
		}
	}
	return true
}

// consumingParam reports whether one of the xsl:with-params of el reads
// more than a view has, all of them by a select (whose check plans
// nothing): an apply foreignParams takes, which withParams refuses.
func (b *streamBuilder) consumingParam(el *xdm.Node, f vfocus) bool {
	found := false
	for _, ch := range el.ChildElements() {
		if !isXSL(ch, "with-param") || ch.Attr("", "select") == nil {
			return false
		}
		found = found || !b.strict().attrs(ch, f)
	}
	return found
}

// paramOp reports whether one of params is an expression op of run's plan
// (foreignParams).
func (run *streamRun) paramOp(params []*Variable) bool {
	for _, p := range params {
		if p.Select != nil && run.x.byExpr[p.Select] != nil {
			return true
		}
	}
	return false
}

// headOnly reports whether op's population is head(P), the window
// alone: its first match is the one item, so a match inside it is not a
// result, as one inside an outermost match is not (si-lre-118: for-each
// select="head(//X)").
func headOnly(op *streamOp) bool {
	p := op.pop
	return p != nil && p.cond == nil && len(p.wins) == 1 && p.wins[0].kind == "head" &&
		p.fn == nil && p.post == nil && !p.ground && !p.off && !p.values
}

// inlinedSafe reports whether src, written on el, reads only what focus f
// has once its calls on stylesheet functions are read as their bodies
// (rewriteCalls, streamcall.go: only where the body is the call exactly),
// when the plan knows the functions in force (streamPlan.funcs). The tree's
// code then evaluates the calls, reading what the bodies read (function-5003:
// xsl:if test="sf:inspection(.)", the body $node instance of document-node()).
func (b *streamBuilder) inlinedSafe(el *xdm.Node, src string, vc viewCheck, f vfocus, nodeOK bool) bool {
	if b.pl.funcs == nil {
		return false
	}
	e := parseAt(el, src)
	if e == nil || !callsSheetFn(e) {
		return false
	}
	vc.peek = false
	return vc.safe(rewriteCalls(e, b.pl.funcs), f, nodeOK)
}

// callsSheetFn reports whether e calls a stylesheet function.
func callsSheetFn(e xpath.Expr) bool {
	found := false
	exprWalk(reflect.ValueOf(&e).Elem(), func(v reflect.Value) {
		if x, ok := v.Interface().(*xpath.FuncCall); ok && stylesheetFn(x.Name) {
			found = true
		}
	})
	return found
}

// firstAnd reads a predicate [position() = N and P], P reading neither
// position() nor last(), as [N][boolean(P)]: both keep the Nth candidate
// when P holds of it (function-5013). Whether P is evaluated on the others,
// and so whether an error of it there is raised, the "and" leaves to the
// processor (XPath 3.1 §3.8).
func firstAnd(preds []xpath.Expr) []xpath.Expr {
	var out []xpath.Expr
	for i, pr := range preds {
		b, ok := pr.(*xpath.BinaryOp)
		if !ok || b.Op != "and" || readsPosition(b.Right) {
			if out != nil {
				out = append(out, pr)
			}
			continue
		}
		n := positionEq(b.Left)
		if n == nil {
			if out != nil {
				out = append(out, pr)
			}
			continue
		}
		if out == nil {
			out = append([]xpath.Expr(nil), preds[:i]...)
		}
		out = append(out, n, fnCall("boolean", b.Right))
	}
	if out == nil {
		return preds
	}
	return out
}

// positionEq is the integer literal N of position() = N (or eq, either
// way round), or nil.
func positionEq(e xpath.Expr) xpath.Expr {
	b, ok := e.(*xpath.BinaryOp)
	if !ok || b.Op != "=" && b.Op != "eq" {
		return nil
	}
	l, r := b.Left, b.Right
	if isPositionCall(r) {
		l, r = r, l
	}
	if !isPositionCall(l) {
		return nil
	}
	if _, ok := positionAt(r); !ok {
		return nil
	}
	return r
}

// isPositionCall reports whether e is fn:position().
func isPositionCall(e xpath.Expr) bool {
	f, ok := e.(*xpath.FuncCall)
	return ok && f.Name.URI == fnNS && f.Name.Local == "position" && len(f.Args) == 0
}

// stepPredCalls inlines the calls in st's predicates (rewriteCalls), each
// at the predicate's own focus, the candidate (function-5013:
// p[2][sf:inspection(.) = false()]).
func stepPredCalls(st *xpath.Step, re func(xpath.Expr) xpath.Expr) {
	for i := range st.Predicates {
		st.Predicates[i] = re(st.Predicates[i])
	}
}

// flatSteps splices the steps of a relative path that is itself a step:
// E1/(E2/E3) is E1/E2/E3, the shape an inlined call leaves
// (./ancestor-or-self::*[last()]/local-name(), function-5007).
func flatSteps(steps []xpath.Expr) []xpath.Expr {
	for i, s := range steps {
		if pe, ok := s.(*xpath.PathExpr); ok && !pe.Root && len(pe.Steps) > 0 {
			out := append(append(append([]xpath.Expr(nil), steps[:i]...), pe.Steps...), steps[i+1:]...)
			return flatSteps(out)
		}
	}
	return steps
}

// lastPreds reports whether preds, refused by burstSafe, would pass were
// each fn:last() fn:position(): last() of the filtered sequence, which the
// split's outer has whole when the consuming part is the context item, a
// value taken once (streamable-130: tokenize(., '\s+')[position() * 2 gt
// last()]).
func lastPreds(preds []xpath.Expr) bool {
	var calls []*xpath.FuncCall
	for i := range preds {
		exprWalk(reflect.ValueOf(&preds[i]).Elem(), func(v reflect.Value) {
			if f, ok := v.Interface().(*xpath.FuncCall); ok && f.Name.URI == fnNS && f.Name.Local == "last" && len(f.Args) == 0 {
				calls = append(calls, f)
			}
		})
	}
	if len(calls) == 0 {
		return false
	}
	for _, f := range calls {
		f.Name.Local = "position"
	}
	ok := allBurstSafe(preds, false)
	for _, f := range calls {
		f.Name.Local = "last"
	}
	return ok
}
