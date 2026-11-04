package xslt

// Expression shapes of the streamed split beyond its paths (full streaming,
// gap lane G3): simple maps whose right operand is evaluated per match.
//
// "P ! R", P the consuming path, evaluates R once per node of P, in order,
// with that node as focus, and concatenates the results: which is what the
// split does with a match's tail. So R becomes the tail (after P's own tail,
// as "(tail) ! R"), evaluated on the match's view when it reads only the
// start tag, on a grounded copy of the match otherwise. Unlike a path's last
// step, R's results are neither sorted nor deduplicated, and may mix nodes
// and values. M6's mapAsPath already reads an atomic-valued R as the path
// P/R; this takes the rest: R making nodes.
//
// R may not read position() or last() (its focus is the match alone), nor
// climb out of the match (a copy's ancestors are shallow). It may make new
// nodes (parse-xml, a stylesheet function given only atomic values): those
// are what the tree makes too, so collecting them costs what the tree's
// result costs, not the grounded copies it is made from. A node of the copy
// itself is kept only where matches cannot nest (their copies would be
// distinct nodes where the tree's are one).

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// mapPathOf is pathOf for "P ! R": P's parts with R to map over each match.
func (w *splitter) mapPathOf(x *xpath.SimpleMap) (splitPathParts, bool) {
	pp, ok := w.pathOf(x.Left)
	if !ok || pp.ctxItem || pp.group || !mapSafe(x.Right) || climbsAny(&xpath.PathExpr{Steps: pp.tail}) || climbBranched(pp) {
		// A tail climbing to ancestors is the walk's (G4, mapClimb and the
		// items check): those are shared by matches, which a path
		// deduplicates and a per-match map would not (G2).
		return splitPathParts{}, false
	}
	if pp.mapR != nil {
		pp.mapR = &xpath.SimpleMap{Left: pp.mapR, Right: x.Right}
	} else {
		pp.mapR = x.Right
	}
	return pp, true
}

// mapTail is the tail a match gives for a path with a map: its own tail,
// then R over each of its items.
func mapTail(tail, r xpath.Expr) xpath.Expr {
	if tail == nil {
		return r
	}
	return &xpath.SimpleMap{Left: tail, Right: r}
}

// mapPath finishes path for P ! R: tail is what each match gives (R over
// P's own tail), last P's last axis. Over text, comment or PI matches alone
// it runs on each one's kept copy, as path's tail does (lane K1), where it
// reads only what that copy has.
func (s *streamSplit) mapPath(w *splitter, tail, r xpath.Expr, leaf, elem bool, last xpath.Axis,
	part func(xpath.Expr) *xpath.Compiled) string {
	if s.red == "boolean" || s.red == "not" {
		// R's items may be values: their EBV is not their existence.
		s.red, w.putRed = "", nil
	}
	s.atomize, s.fresh = w.use == useAtomize || splitFolds[s.red], mapFresh(r)
	switch {
	case leaf && (elem || !(viewCheck{}).safe(tail, vLeaf, splitCounts[s.red])):
		return "a map over text, comment or PI matches"
	case leaf:
		s.mode, s.keepLeaves, s.leafOnly = matchView, true, true
		if why := s.branchChecks(); s.hasBranches() && why != "" {
			return why
		}
	case climbMapUp(tail): // streamfn_climb.go
		if why := climbMapCheck(tail, s.atomize); why != "" {
			return why
		}
		s.mode = matchGround
	case (viewCheck{}).safe(tail, vElem, splitCounts[s.red]):
		s.mode = matchView
	case !s.nests || s.fresh:
		s.mode = matchGround
	case s.atomize && climbMapCalls(tail) && climbMapCheck(tail, true) == "": // streamfn_climb.go
		s.mode = matchGround
	default:
		return "simple map " + r.String() + ": nodes of nested matches"
	}
	if last == xpath.AxisDescendantOrSelf && s.mode != matchView {
		return "the context itself is a result"
	}
	if w.kids && s.mode == matchGround {
		return "has-children(.) on a match that is read whole"
	}
	s.tail, s.tailFail = part(tail), !splitCannotFail(tail)
	s.accepts()
	s.folds(w, part)
	return ""
}

// mapSafe reports whether R, mapped over a grounded copy of a match, reads
// only what the copy has: burstSafe, without fn:position or a step up; or a
// call on a stylesheet function given only atomic values, which cannot reach
// the copy at all.
func mapSafe(r xpath.Expr) bool {
	if climbsAny(r) {
		return false
	}
	if climbMapUp(r) { // streamfn_climb.go; mapPath checks R whole
		_, ok := climbMapKind(r)
		return ok
	}
	if mapSetSafe(r) { // streamsplit_k2.go
		return true
	}
	if f, ok := r.(*xpath.FuncCall); ok && stylesheetFn(f.Name) {
		for _, a := range f.Args {
			if !mapAtomic(a) || !burstSafe(a, true) {
				return burstSafe(r, true) // a marked call (streamsplit_calls.go)
			}
		}
		return true
	}
	return burstSafe(r, true)
}

// stylesheetFn reports whether a function name is a stylesheet's: in no
// namespace of built-in functions.
func stylesheetFn(q xdm.QName) bool {
	switch q.URI {
	case "", fnNS, xdm.NSXS, xdm.NSMath, xdm.NSMap, xdm.NSArray:
		return false
	}
	return true
}

// mapAtomic reports, conservatively, whether e makes only atomic values.
func mapAtomic(e xpath.Expr) bool {
	_, ok := e.(*xpath.StringConcat)
	return ok || atomicValued(e)
}

// mapFresh reports, conservatively, whether R makes no node of the match's
// copy: only values, or nodes it makes itself (parse-xml, or a stylesheet
// function given only atomic values), perhaps navigated within.
func mapFresh(r xpath.Expr) bool {
	switch x := r.(type) {
	case *xpath.FuncCall:
		if stylesheetFn(x.Name) {
			return sheetCallFresh(x) // streamsplit_calls.go
		}
		if x.Name.URI == fnNS && (x.Name.Local == "parse-xml" || x.Name.Local == "parse-xml-fragment") {
			return true
		}
	case *xpath.PathExpr:
		return !x.Root && len(x.Steps) > 0 && !isStep(x.Steps[0]) && mapFresh(x.Steps[0])
	case *xpath.SimpleMap:
		return mapFresh(x.Right)
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !mapFresh(it) {
				return false
			}
		}
		return true
	}
	return mapAtomic(r)
}

// climbsAny reports whether a step up appears anywhere in e (burstSafe has
// ruled out the other axes off the match): R's focus is a copy whose
// ancestors have their names and attributes only.
func climbsAny(e xpath.Expr) bool {
	in := func(es ...xpath.Expr) bool {
		for _, x := range es {
			if x != nil && climbsAny(x) {
				return true
			}
		}
		return false
	}
	switch x := e.(type) {
	case *xpath.Step:
		return climbs(x) || in(x.Predicates...)
	case *xpath.PathExpr:
		return x.Root || in(x.Steps...)
	case *xpath.FilterExpr:
		return in(x.Base) || in(x.Predicates...)
	case *xpath.BinaryOp:
		return in(x.Left, x.Right)
	case *xpath.StringConcat:
		return in(x.Left, x.Right)
	case *xpath.SimpleMap:
		return in(x.Left, x.Right)
	case *xpath.UnaryOp:
		return in(x.Operand)
	case *xpath.CastExpr:
		return in(x.Operand)
	case *xpath.TreatExpr:
		return in(x.Operand)
	case *xpath.InstanceOfExpr:
		return in(x.Operand)
	case *xpath.FuncCall:
		return in(x.Args...)
	case *xpath.IfExpr:
		return in(x.Cond, x.Then, x.Else)
	case *xpath.SequenceExpr:
		return in(x.Items...)
	case *xpath.LetExpr:
		return in(bindingExprs(x.Bindings)...) || in(x.Return)
	case *xpath.ForExpr:
		return in(bindingExprs(x.Bindings)...) || in(x.Return)
	case *xpath.QuantifiedExpr:
		return in(bindingExprs(x.Bindings)...) || in(x.Test)
	case *xpath.MapConstructor:
		return in(x.Keys...) || in(x.Values...)
	case *xpath.ArrayConstructor:
		return in(x.Members...)
	case *xpath.LookupExpr:
		return in(x.Base, x.Expr)
	}
	return false
}

// tailCallOf is pathOf for "tail(Q)", Q a path with no tail of its own: Q's
// matches but the first (a match of a bare path is one item).
func (w *splitter) tailCallOf(f *xpath.FuncCall) (splitPathParts, bool) {
	if f.Name.URI != fnNS || f.Name.Local != "tail" || len(f.Args) != 1 {
		return splitPathParts{}, false
	}
	pp, ok := w.pathOf(f.Args[0])
	if !ok || pp.ctxItem || pp.group || len(pp.tail) > 0 || pp.mapR != nil {
		return splitPathParts{}, false
	}
	pp.skip++
	return pp, true
}

// hoistChoice rewrites F(if (C) then A else B), F a built-in function of one
// argument and C motionless at the focus, as if (C) then F(A) else F(B): the
// split then runs the chosen branch's split alone (DESIGN section 1.5's
// choice groups). Both evaluate C first, then F of the branch's value.
func hoistChoice(e xpath.Expr, focus vfocus, key bool) xpath.Expr {
	f, ok := e.(*xpath.FuncCall)
	if !ok || len(f.Args) != 1 || f.Name.URI != fnNS && f.Name.URI != xdm.NSXS {
		return e
	}
	x, ok := hoistChoice(f.Args[0], focus, key).(*xpath.IfExpr)
	if !ok || !(viewCheck{kids: true, key: key}).safe(x.Cond, focus, true) {
		return e
	}
	return &xpath.IfExpr{Cond: x.Cond,
		Then: &xpath.FuncCall{Name: f.Name, Args: []xpath.Expr{x.Then}},
		Else: &xpath.FuncCall{Name: f.Name, Args: []xpath.Expr{x.Else}}}
}

// textTemplate plans a text node t with a brace, a child of el, focus f. It
// is literal text unless expand-text is in force (compileText's rule); then
// a text value template, whose parts are evaluated as an attribute value
// template's are (avt.eval, through evalStreamExpr): each motionless at the
// focus, or one the consumer, an expression op split as the text compiles
// (compileText calls noteStreamAVT). The §19.8 analysis does not see text
// value templates (bodyRule reads child elements only), so the plan judges
// the parts itself and counts a consuming one as a consumer of el's
// sequence constructor. It returns the op, nil when nothing consumes.
func (b *streamBuilder) textTemplate(t, el *xdm.Node, f vfocus) (*streamOp, bool) {
	if !expandTextAt(el) {
		return nil, true
	}
	exprs, ok := avtExpressions(emptyBracesRemoved(t.Value()))
	if !ok {
		return nil, b.refuse("text value template " + t.Value())
	}
	var op *streamOp
	for _, src := range exprs {
		if e := parseAt(el, src); e != nil && (viewCheck{pos: true, noIdent: b.noIdent, key: b.key, acc: !b.noIdent && !b.grp, after: b.after}).safe(e, f, false) {
			continue
		}
		if op != nil {
			return nil, b.refuse("two consuming parts of a text value template")
		}
		op = &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key, avt: src}
	}
	return op, true
}

// addTextOp adds a text value template's consuming part to the plan.
func (b *streamBuilder) addTextOp(op *streamOp) {
	*b.ops = append(*b.ops, op)
	b.x.exprOps = append(b.x.exprOps, op)
	b.kinds[streamtrace.KindSelect]++
}

// consumingIf plans an xsl:if whose test reads its context (§19.8.4.21:
// the test is the operand, the body then motionless, as a choice group's
// condition): the test is an expression op, split as boolean(test) as the
// instruction compiles (noteStreamTest), the body checked as motionless.
// done is false when the test is motionless: the usual plan applies.
func (b *streamBuilder) consumingIf(el *xdm.Node, f vfocus) (done, ok bool) {
	op := b.testOp(el, f)
	if op == nil {
		return false, false
	}
	if !b.content(el, f) {
		return true, false
	}
	b.kinds[streamtrace.KindChoose]++
	b.addTextOp(op)
	return true, true
}

// consumingChoose is consumingIf for an xsl:choose: one xsl:when's test
// reads the context; every other test and every branch is motionless.
func (b *streamBuilder) consumingChoose(el *xdm.Node, f vfocus) (done, ok bool) {
	var op *streamOp
	for _, ch := range el.ChildElements() {
		if isXSL(ch, "when") {
			if t := b.testOp(ch, f); t != nil {
				if op != nil {
					return true, b.refuse("two consuming tests in xsl:choose")
				}
				op = t
			}
		}
	}
	if op == nil {
		return false, false
	}
	for ch := range el.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "",
			ch.Kind() == xdm.KindComment, ch.Kind() == xdm.KindPI:
		case ch.Kind() == xdm.KindElement && (isXSL(ch, "when") || isXSL(ch, "otherwise")):
			if ch != op.el && !b.attrs(ch, f) || !b.content(ch, f) {
				return true, false
			}
		default:
			return true, b.refuse("xsl:choose content")
		}
	}
	b.kinds[streamtrace.KindChoose]++
	b.addTextOp(op)
	return true, true
}

// testOp is the op of el's test when the view cannot answer it, its other
// attributes being ones nothing reads; nil otherwise.
func (b *streamBuilder) testOp(el *xdm.Node, f vfocus) *streamOp {
	src := el.AttrValue("test")
	if e := parseAt(el, src); e == nil || (viewCheck{pos: true, noIdent: b.noIdent, key: b.key, acc: !b.noIdent && !b.grp, after: b.after}).safe(e, f, true) {
		return nil
	}
	for a := range el.Attrs() {
		if n := a.Name(); n.URI == "" && n.Local != "test" && (!streamAttrsOK[n.Local] || n.Local == "select") {
			return nil
		}
	}
	return &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key, avt: src}
}

// noteStreamTest splits test, the test of n, an xsl:if or xsl:when whose
// test an op of the plans being compiled consumes, as boolean(test): its
// effective boolean value, which folds a path to whether it has a match.
func (c *compiler) noteStreamTest(n *xdm.Node, test *xpath.Compiled, ns xpath.NamespaceResolver) {
	if c.stream == nil && len(c.ruleExecs) == 0 {
		return
	}
	for _, x := range append([]*streamExec{c.stream}, c.ruleExecs...) {
		if x == nil {
			continue
		}
		for _, op := range x.exprOps {
			if op.el != n || op.instr != nil || op.avt != n.AttrValue("test") {
				continue
			}
			op.instr, op.sel = exprInstr{}, test
			e := reparse(test, ns)
			switch {
			case test.CompatMode():
				op.dead = "XPath 1.0 compatibility mode"
			case e == nil:
				op.dead = "test does not parse again"
			default:
				e = &xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: "boolean"}, Args: []xpath.Expr{c.inlineCalls(e)}}
				var why string
				if op.split, why = splitStreamIn(e, test, op.doc, op.inGroup, op.key); op.split == nil {
					op.dead = "split: " + why
				} else {
					x.byExpr[test] = op
				}
			}
		}
	}
}

// evalStreamBool is the effective boolean value of test, an xsl:if's or
// xsl:when's, read from the stream when it is an op of the plan running.
func (rt *runtime) evalStreamBool(test *xpath.Compiled) (bool, error) {
	if f := rt.bursts; f != nil && f.run != nil && !f.run.off {
		if op := f.run.x.byExpr[test]; op != nil {
			seq, err := f.run.evalOp(rt, op)
			if err != nil {
				return false, err
			}
			return xpath.EffectiveBooleanValue(seq)
		}
	}
	return test.EvalBool(rt.ctx)
}

// collection is walk for the constructors and functions of arrays and maps
// and for lookups: their operands keep their items (a member is not
// atomized, nor is a map's value), so the consuming path is a slot of one of
// them as it is, and the outer builds the array or map at the end from the
// items it collected. A map's keys, and a lookup's key, are atomized and
// must not hold the path. done is false for any other expression.
func (w *splitter) collection(e xpath.Expr) (done, ok bool) {
	switch x := e.(type) {
	case *xpath.ArrayConstructor:
		for k := range x.Members {
			if !w.slot(x.Members[k], func(y xpath.Expr) { x.Members[k] = y }, useKeep) {
				return true, false
			}
		}
		return true, true
	case *xpath.MapConstructor:
		for k := range x.Keys {
			if !w.noPath(x.Keys[k], useAtomize, "the consuming path in a map key") ||
				!w.slot(x.Values[k], func(y xpath.Expr) { x.Values[k] = y }, useKeep) {
				return true, false
			}
		}
		return true, true
	case *xpath.LookupExpr:
		if x.Base == nil {
			return true, w.refuse("a unary lookup")
		}
		if x.Expr != nil && !w.noPath(x.Expr, useAtomize, "the consuming path in a lookup key") {
			return true, false
		}
		return true, w.slot(x.Base, func(y xpath.Expr) { x.Base = y }, useKeep)
	case *xpath.FuncCall:
		if x.Name.URI != xdm.NSArray && x.Name.URI != xdm.NSMap {
			return false, false
		}
		if len(x.Args) == 1 && x.Name.URI == xdm.NSArray && x.Name.Local == "size" {
			for a, k := arrayOf(x.Args[0]), 0; a != nil && k < len(a.Members); k++ {
				// The size of a square array is its number of members, whatever
				// they hold: each member is still evaluated, but only counted.
				a.Members[k] = &xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: "count"}, Args: []xpath.Expr{a.Members[k]}}
			}
		}
		for k := range x.Args {
			if !w.slot(x.Args[k], func(y xpath.Expr) { x.Args[k] = y }, useKeep) {
				return true, false
			}
		}
		return true, true
	}
	return false, false
}

// arrayOf is e as a square array constructor, or nil.
func arrayOf(e xpath.Expr) *xpath.ArrayConstructor {
	if a, ok := e.(*xpath.ArrayConstructor); ok && !a.Curly {
		return a
	}
	return nil
}
