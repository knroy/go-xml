package xslt

// The analysis plan side map (full streaming, component C3).
//
// The §19.8 analysis returns one posture and sweep per container and forgets
// how it got there. A streamed execution plan needs more: for each construct
// inside the container, its own posture and sweep, and which of its operands
// was the consuming one, or the alternatives of a choice group. This file
// records exactly that, as the trusting walk (burstVerdict) computes it, in a
// map the plan builders read. It adds no rule and changes no answer: the
// recorder only reads what the rules compute.
//
// The recorder rides on the declTrust, which exists only on the trusting walk
// of a streamable xsl:source-document; every refusal path has a nil trust, so
// it records nothing and pays one nil test per construct. Nested checks the
// trust makes on demand -- a stylesheet function's body, a streamable mode's
// rules, an attribute set, an accumulator -- record into the same map, each
// in the context its own check assessed it in.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// streamPlan is the side map of one trusting walk.
//
// Keys of recs, and the src of a planOperand, are one of:
//   - xpath.Expr: a node of an expression the walk parsed (the analysis
//     parses each attribute itself, so these are its own ASTs; sites gives
//     the root for an attribute), except "." and "?" (unkeyable);
//   - *xdm.Node: an instruction or literal result element of the stylesheet;
//   - planBody: the sequence constructor formed by an element's children;
//   - planAVT: one attribute or text value template, by element and source;
//   - planSite: an XPath operand of an instruction (src only; resolve with
//     root).
type streamPlan struct {
	// verdict is the container's, exactly as burstVerdict reads it.
	verdict planRec
	recs    map[any]*planRec
	sites   map[planSite][]planRoot
	frames  []planFrame
	// funcs are the module's functions, when they are the ones in force
	// (declTrust.local): a motionless check may read a call as its body
	// (inlinedSafe, streamlast_mm2.go).
	funcs map[funcKey]*streamFunc
	// sets and modFns are each module's attribute sets and functions, by
	// its document element, made on first use for every construct planned
	// from this record (streamcall.go useSets, streamview_leafcall.go).
	sets   map[*xdm.Node]map[xdm.QName][]*xdm.Node
	modFns map[*xdm.Node]map[funcKey]*streamFunc
}

// planRec is what the analysis computed for one construct.
//
// known and unverified are the walk's own bits as they stood when the
// construct's rule returned. Both only ever move one way, so known means
// nothing up to and including this construct was unmodelled, and !unverified
// that nothing rested on an unverified declaration. Where the container's
// verdict is known and verified the bits are therefore exact for every
// construct in it; elsewhere a construct can read unknown (or unverified)
// because of something assessed before it -- the safe direction.
type planRec struct {
	props             props
	known, unverified bool
	// ctx and ctxAllowsChildren are the focus the construct was assessed
	// under (§19.6), which is what a strategy is chosen against (DESIGN
	// §2.3): a predicate's construct has its step's posture, a for-each
	// body's members the select's. ctxType is the context item type of that
	// focus (§19.2), which §19.8.9.18's root rule and a leading "/" read.
	ctx               posture
	ctxAllowsChildren bool
	ctxType           itemType
	// ops are the operands of the general-rules combination (§19.8.1)
	// that produced props, in the order the rule listed them; nil when the
	// construct's own cascade produced them (a path, a for-each select, a
	// clause that returns roaming).
	ops []planOperand
	// conflict says the walk assessed the construct more than once with
	// different results (noteBodyModelled re-walks a body in another
	// posture, say). A plan builder must not use such a record.
	conflict bool
}

// planOperand is one operand as combine saw it.
type planOperand struct {
	src         any
	props       props // the operand's own
	usage       usage // after the §19.8.1 absorption-to-inspection downgrade
	sweep       sweep // the adjusted sweep S′
	consuming   bool  // potentially consuming (§19.8.1)
	choice      bool  // a member of the construct's choice operand group
	higherOrder bool
}

// planBody keys the sequence constructor of el.
type planBody struct{ el *xdm.Node }

// planAVT keys an attribute value template written on el, or a text value
// template in one of its text children.
type planAVT struct {
	el  *xdm.Node
	src string
}

// planSite is an XPath expression written on el, by its source text.
type planSite struct {
	el  *xdm.Node
	src string
}

// planRoot is one parse of a site, with the focus it was assessed under.
type planRoot struct {
	root              xpath.Expr
	ctx               posture
	ctxAllowsChildren bool
	ctxType           itemType
}

type planFrame struct {
	ops []planOperand
	p   props
	set bool
}

// buildStreamPlan runs the trusting walk of burstVerdict over the streamable
// xsl:source-document sd, recording as it goes.
func buildStreamPlan(sd *xdm.Node, shared bool, xmods ...*xdm.Node) *streamPlan {
	t := newDeclTrust(documentElementOf(sd), shared)
	t.xmods = xmods // streamtrust_xmod_l7.go
	t.plan = &streamPlan{recs: map[any]*planRec{}, sites: map[planSite][]planRoot{}}
	if t.local() {
		t.plan.funcs = collectStreamFuncs(documentElementOf(sd))
	}
	p, known, unverified := sequenceConstructorVerdict(sd, postureStriding, t.sets, t.funcs, t)
	t.plan.verdict = planRec{props: p, known: known, unverified: unverified}
	return t.plan
}

// trusted is burstVerdict's test: execution may rely on the container.
func (pl *streamPlan) trusted() bool {
	v := pl.verdict
	return v.known && !v.unverified && v.props.streamable() && v.props.posture == postureGrounded
}

// rec returns the record for key, or nil when the walk did not reach it or
// reached it with different results.
func (pl *streamPlan) rec(key any) *planRec {
	if r := pl.recs[key]; r != nil && !r.conflict {
		return r
	}
	return nil
}

// root returns the parse of the site the walk assessed, or nil when it parsed
// none, or more than one under different foci.
func (pl *streamPlan) root(s planSite) xpath.Expr {
	rs := pl.sites[s]
	if len(rs) == 0 {
		return nil
	}
	for _, r := range rs[1:] {
		if r.ctx != rs[0].ctx || r.ctxAllowsChildren != rs[0].ctxAllowsChildren || r.ctxType != rs[0].ctxType {
			return nil
		}
	}
	return rs[0].root
}

// consumer returns the index of the construct's one consuming operand: -1
// when none consumes, and ok false when several do (alternatives of a choice
// group, or motionless transmissions such as "(@a, @b)") or when the record
// has no operands.
func (r *planRec) consumer() (i int, ok bool) {
	if r.ops == nil {
		return -1, false
	}
	i = -1
	for j, o := range r.ops {
		if o.consuming {
			if i >= 0 {
				return -1, false
			}
			i = j
		}
	}
	return i, true
}

func (t *declTrust) planOf() *streamPlan {
	if t == nil {
		return nil
	}
	return t.plan
}

func (pl *streamPlan) enter() { pl.frames = append(pl.frames, planFrame{}) }

// leave records the construct whose rule just returned p.
func (pl *streamPlan) leave(key any, p props, ctx posture, kids bool, ctxType itemType, known, unverified bool) {
	f := pl.frames[len(pl.frames)-1]
	pl.frames = pl.frames[:len(pl.frames)-1]
	r := planRec{props: p, known: known, unverified: unverified, ctx: ctx, ctxAllowsChildren: kids, ctxType: ctxType}
	if f.set && f.p == p {
		r.ops = f.ops
	}
	old := pl.recs[key]
	if old == nil {
		pl.recs[key] = &r
		return
	}
	if !old.same(&r) {
		old.conflict = true
	}
}

func (r *planRec) same(o *planRec) bool {
	if r.props != o.props || r.known != o.known || r.unverified != o.unverified ||
		r.ctx != o.ctx || r.ctxAllowsChildren != o.ctxAllowsChildren || r.ctxType != o.ctxType ||
		len(r.ops) != len(o.ops) || (r.ops == nil) != (o.ops == nil) {
		return false
	}
	for i := range r.ops {
		if r.ops[i] != o.ops[i] {
			return false
		}
	}
	return true
}

// combined notes a combination made directly by the innermost construct.
func (pl *streamPlan) combined(ops []operand, p props) {
	if len(pl.frames) == 0 {
		return
	}
	f := &pl.frames[len(pl.frames)-1]
	f.ops = make([]planOperand, len(ops))
	for i, o := range ops {
		f.ops[i] = planOperand{src: o.src, props: o.props, usage: o.adjustedUsage(),
			sweep: o.adjustedSweep(), consuming: o.potentiallyConsuming(),
			choice: o.choiceGroup, higherOrder: o.higherOrder}
	}
	f.p, f.set = p, true
}

func (pl *streamPlan) site(el *xdm.Node, src string, root xpath.Expr, ctx posture, kids bool, ctxType itemType) {
	s := planSite{el, src}
	pl.sites[s] = append(pl.sites[s], planRoot{root, ctx, kids, ctxType})
}

// The recording wrappers. Each rule function is the one the analysis always
// had, renamed; without a plan the wrapper is a nil test and a call.

func (a *analyzer) expr(e xpath.Expr) props {
	pl := a.trust.planOf()
	if pl == nil || e == nil || unkeyable(e) {
		return a.exprRule(e)
	}
	pl.enter()
	p := a.exprRule(e)
	pl.leave(e, p, a.ctxPosture, a.ctxAllowsChildren, a.ctxType, a.known, a.unverified)
	return p
}

// unkeyable: "." and the "?" argument placeholder are zero-size AST nodes,
// and Go does not make pointers to distinct zero-size values distinct, so
// every "." of every expression may share one key. They get no record; the
// operand entry their parent holds carries their posture and sweep (and its
// src still says, by type, what the operand was).
func unkeyable(e xpath.Expr) bool {
	switch e.(type) {
	case *xpath.ContextItem, *xpath.ArgumentPlaceholder:
		return true
	}
	return false
}

func (a *analyzer) combine(ops []operand, singletonBuiltin bool) props {
	p := combine(ops, singletonBuiltin)
	if pl := a.trust.planOf(); pl != nil {
		pl.combined(ops, p)
	}
	return p
}

func (a *instrAnalyzer) instruction(el *xdm.Node) props {
	pl := a.trust.planOf()
	if pl == nil {
		return a.instructionRule(el)
	}
	pl.enter()
	p := a.instructionRule(el)
	pl.leave(el, p, a.ctxPosture, a.ctxAllowsChildren, a.ctxType, a.known, a.unverified)
	return p
}

func (a *instrAnalyzer) body(el *xdm.Node) props {
	pl := a.trust.planOf()
	if pl == nil {
		return a.bodyRule(el)
	}
	pl.enter()
	p := a.bodyRule(el)
	pl.leave(planBody{el}, p, a.ctxPosture, a.ctxAllowsChildren, a.ctxType, a.known, a.unverified)
	return p
}

func (a *instrAnalyzer) avtPropsOf(src string, el *xdm.Node) props {
	pl := a.trust.planOf()
	if pl == nil {
		return a.avtRule(src, el)
	}
	pl.enter()
	p := a.avtRule(src, el)
	pl.leave(planAVT{el, src}, p, a.ctxPosture, a.ctxAllowsChildren, a.ctxType, a.known, a.unverified)
	return p
}

func (a *instrAnalyzer) combine(ops []operand, singletonBuiltin bool) props {
	p := combine(ops, singletonBuiltin)
	if pl := a.trust.planOf(); pl != nil {
		pl.combined(ops, p)
	}
	return p
}
