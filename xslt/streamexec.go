package xslt

// The streamed core (full streaming, component C5; DESIGN sections 1.6, 2).
//
// A trusted streamable xsl:source-document whose burst plan (streamburst.go)
// is refused gets a core plan: a tree of sNodes over its body, built from
// the §19.8 plan map (C3) and run on the stream cursor (C1) with the
// streamed XPath split (C4). Order: the burst plan is tried first and keeps
// every container it accepts; the core takes the rest (M1.6 retires the
// burst plans into it).
//
// The sNodes are a classification of the body, not a second interpreter:
//
//   - sTree: a motionless instruction. It runs its own Execute, with a view
//     of the context element (or, at the document node, an empty stand-in)
//     as its focus; every expression in it is checked to read of that focus
//     only what a view has (viewCheck).
//   - sSeq, sElement (a literal result element, xsl:element, xsl:copy,
//     xsl:attribute, xsl:comment, xsl:processing-instruction, xsl:document,
//     and xsl:sequence, xsl:value-of, xsl:variable with content), sChoose
//     (xsl:if, xsl:choose), sWherePop (xsl:where-populated): the wrappers of
//     the consuming instruction, which also run their own Execute; their
//     motionless parts are checked as sTree's.
//   - the consuming leaf, the only one intercepted (burstsFor, evalSelect):
//     a value-of, copy-of, sequence or attribute whose select is split
//     (Select), or an sForEach: xsl:for-each or xsl:iterate, P run on the
//     cursor and the body run per match -- on its view at the start tag
//     (matchBody: the body may read the match), or on its view without the
//     cursor (a motionless body; nested matches are found below it), or on
//     a grounded copy of each outermost match (sGround: whatever the body
//     reads, it reads of a tree; the burst path's body rules apply).
//   - sSourceDoc: a nested xsl:source-document, which reads nothing of this
//     stream: only its href is checked; its own body has its own plan.
//
// Whatever the builder does not positively recognise leaves the container
// on the tree. At run time each context's content is read by at most one
// consuming instruction (§19.8: one consuming operand per construct, outside
// choice groups); a context nothing read is skipped, the document drained.
//
// Failures re-run the container on the tree (DESIGN section 1.3). The body
// writes into the caller's builder as it goes; nothing leaves the process
// before the transformation ends, so what has been written is not yet
// flushed to anyone, but it cannot be taken back either. So:
//
//   - nothing written yet: the tree's run is the answer, output or error;
//   - written: an error the tree also raises is the tree's error, exactly
//     (the partial output is dropped with the failed transformation, or by
//     the xsl:try that catches it); a tree run that succeeds where the
//     stream failed is a streaming defect, reported as an internal error and
//     counted as a guard hit, as a posture fault after a write is.
//
// Faults that are not errors of the document or the stylesheet cannot come
// after a write by construction: errSplitGiveUp (a min or max over mixed
// types) is admitted only at the document node, where the select falls back
// to the tree by itself and the run goes on; a posture fault means the
// analysis or this builder is wrong (0 in every differential).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// noCore turns the core off, so that a test can compare it with the tree,
// and noBurstPlan the burst plans, so that the core takes their containers.
var noCore, noBurstPlan bool

// traceRerun, when set, is told of the failure behind each re-run.
var traceRerun func(err error)

// traceCore, when set, is told why the core refused each trusted container
// the burst plans refused ("" when it took it), for the coverage tests.
var traceCore func(sd *xdm.Node, why string)

// streamExec is the core plan of one container.
type streamExec struct {
	// top are the consuming instructions read at the document node, and ops
	// every one of the plan, by element; byInstr is filled as they compile.
	top     []*streamOp
	ops     map[*xdm.Node]*streamOp
	byInstr map[Instruction]*streamOp
	// kinds counts the plan's sNodes outside the loops; ready adds theirs.
	kinds [streamtrace.NumKinds]int64
	// drainFirst says nothing at the document node consumes: the document
	// is read through before the body runs, so its errors come first.
	drainFirst bool
}

// streamOp is a consuming instruction of the plan.
type streamOp struct {
	el    *xdm.Node
	doc   bool // its context is the document node
	loop  bool // xsl:for-each or xsl:iterate
	instr Instruction
	sel   *xpath.Compiled
	split *streamSplit
	dead  string // why it cannot run streamed, found as it compiled
	// copies says a copy-of hands each item over as it is read (its split
	// would collect grounded copies): copyOfInstr copies it and lets go.
	copies bool
	// For a loop: inner are the consuming instructions of the body, kinds
	// its sNodes, planned whether every one is one the core runs, and
	// grounded whether the body may run on a grounded copy instead.
	inner    []*streamOp
	kinds    [streamtrace.NumKinds]int64
	planned  bool
	grounded bool
}

// streamBuilder builds a plan from the source of a container.
type streamBuilder struct {
	pl    *streamPlan
	x     *streamExec
	ops   *[]*streamOp // the list the consuming instructions met go into
	kinds *[streamtrace.NumKinds]int64
	why   string
}

func (b *streamBuilder) refuse(why string) bool {
	if b.why == "" {
		b.why = why
	}
	return false
}

// planStream builds the core plan of the streamable xsl:source-document sd,
// or returns nil and why not. shared is planBursts'.
func planStream(sd *xdm.Node, shared bool) (*streamExec, string) {
	for _, a := range []string{"validation", "type", "use-accumulators"} {
		if sd.Attr("", a) != nil {
			return nil, "@" + a
		}
	}
	pl := buildStreamPlan(sd, shared)
	if !pl.trusted() {
		return nil, "verdict not trusted"
	}
	x := &streamExec{ops: map[*xdm.Node]*streamOp{}, byInstr: map[Instruction]*streamOp{}}
	b := &streamBuilder{pl: pl, x: x, ops: &x.top, kinds: &x.kinds}
	if !b.seq(sd, vNone) {
		return nil, b.why
	}
	x.drainFirst = len(x.top) == 0
	return x, ""
}

// elName is el's name for a refusal reason.
func elName(el *xdm.Node) string {
	if el.Name().URI == xdm.NSXSL {
		return "xsl:" + el.Name().Local
	}
	return "a literal result element"
}

// seq plans the sequence constructor of el, its context focus f.
func (b *streamBuilder) seq(el *xdm.Node, f vfocus) bool {
	b.kinds[streamtrace.KindSeq]++
	consumers := 0
	for ch := range el.Children() {
		switch ch.Kind() {
		case xdm.KindText:
			if strings.ContainsAny(ch.Value(), "{}") {
				return b.refuse("text with a brace") // perhaps a text value template
			}
			continue
		case xdm.KindElement:
		default:
			continue
		}
		r := b.pl.rec(ch)
		if r == nil || !r.known || r.unverified {
			return b.refuse("no trusted record for " + elName(ch))
		}
		switch r.props.sweep {
		case sweepMotionless:
			if !b.motionless(ch, f) {
				return false
			}
			b.kinds[streamtrace.KindTree]++
			continue
		case sweepConsuming:
		default:
			return b.refuse("free-ranging " + elName(ch))
		}
		if consumers++; consumers > 1 {
			return b.refuse("two consuming instructions in one sequence constructor")
		}
		if !b.consumer(ch, f) {
			return false
		}
	}
	return true
}

// consumer plans a consuming instruction el, its focus f.
func (b *streamBuilder) consumer(el *xdm.Node, f vfocus) bool {
	if el.Name().URI != xdm.NSXSL {
		if !b.attrs(el, f) {
			return false
		}
		b.kinds[streamtrace.KindElement]++
		return b.seq(el, f)
	}
	local := el.Name().Local
	if el.Attr("", "select") != nil {
		switch local {
		case "value-of", "copy-of", "sequence", "attribute":
			return b.selectOp(el, f)
		case "for-each", "iterate":
			return b.loopOp(el, f)
		}
		return b.refuse("consuming select of xsl:" + local)
	}
	switch local {
	case "copy":
		if f == vNone {
			return b.refuse("xsl:copy of the document node")
		}
		fallthrough
	case "element", "attribute", "comment", "processing-instruction", "document",
		"sequence", "value-of", "variable", "where-populated":
		if !b.attrs(el, f) {
			return false
		}
		k := streamtrace.KindElement
		if local == "where-populated" {
			k = streamtrace.KindWherePop
		}
		b.kinds[k]++
		return b.seq(el, f)
	case "if":
		if !b.attrs(el, f) {
			return false
		}
		b.kinds[streamtrace.KindChoose]++
		return b.seq(el, f)
	case "choose":
		b.kinds[streamtrace.KindChoose]++
		for ch := range el.Children() {
			switch {
			case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "",
				ch.Kind() == xdm.KindComment, ch.Kind() == xdm.KindPI:
			case ch.Kind() == xdm.KindElement && (isXSL(ch, "when") || isXSL(ch, "otherwise")):
				if !b.attrs(ch, f) || !b.seq(ch, f) {
					return false
				}
			default:
				return b.refuse("xsl:choose content")
			}
		}
		return true
	}
	return b.refuse("consuming xsl:" + local)
}

// selectOp plans a value-of, copy-of, sequence or attribute whose select
// consumes; its split is made as it compiles (noteStreamSelect).
func (b *streamBuilder) selectOp(el *xdm.Node, f vfocus) bool {
	if !b.attrs(el, f) {
		return false
	}
	op := &streamOp{el: el, doc: f == vNone}
	b.x.ops[el] = op
	*b.ops = append(*b.ops, op)
	b.kinds[streamtrace.KindSelect]++
	return true
}

// loopOp plans an xsl:for-each or xsl:iterate whose select consumes. Its
// strategy is chosen once it and its body have compiled (ready).
func (b *streamBuilder) loopOp(el *xdm.Node, f vfocus) bool {
	if !b.attrs(el, f) {
		return false
	}
	op := &streamOp{el: el, doc: f == vNone, loop: true}
	inner := &streamBuilder{pl: b.pl, x: b.x, ops: &op.inner, kinds: &op.kinds}
	var body []*xdm.Node
	op.planned = true
	for ch := range el.Children() {
		if ch.Kind() != xdm.KindElement {
			if ch.Kind() == xdm.KindText && strings.ContainsAny(ch.Value(), "{}") {
				return b.refuse("text with a brace")
			}
			continue
		}
		switch {
		case isXSL(ch, "sort"):
			return b.refuse("xsl:sort")
		case isXSL(ch, "param") && isXSL(el, "iterate"):
			if !iterateParamOK(ch) {
				return b.refuse("xsl:iterate parameter default reads the focus")
			}
		case isXSL(ch, "on-completion") && isXSL(el, "iterate"):
			// Its focus is absent: no stream to read.
			if !b.attrs(ch, vNone) || !b.content(ch, vNone) {
				return false
			}
		default:
			body = append(body, ch)
		}
	}
	// The body is planned in a builder of its own: a refusal there leaves
	// the grounded strategy, whose rules are the burst path's.
	op.planned = inner.members(el, body)
	op.grounded = burstBody(el)
	if !op.planned && !op.grounded {
		return b.refuse(inner.why)
	}
	b.x.ops[el] = op
	*b.ops = append(*b.ops, op)
	return true
}

// members plans the elements body of el's sequence constructor, whose
// focus is an element's view: seq, over a subset of the children.
func (b *streamBuilder) members(el *xdm.Node, body []*xdm.Node) bool {
	b.kinds[streamtrace.KindSeq]++
	consumers := 0
	for _, ch := range body {
		r := b.pl.rec(ch)
		if r == nil || !r.known || r.unverified {
			return b.refuse("no trusted record for " + elName(ch))
		}
		if r.props.sweep == sweepMotionless {
			if !b.motionless(ch, vElem) {
				return false
			}
			b.kinds[streamtrace.KindTree]++
			continue
		}
		if r.props.sweep != sweepConsuming {
			return b.refuse("free-ranging " + elName(ch))
		}
		if consumers++; consumers > 1 {
			return b.refuse("two consuming instructions in one sequence constructor")
		}
		if !b.consumer(ch, vElem) {
			return false
		}
	}
	return true
}

// streamMotionless are the instructions an sTree may hold.
var streamMotionless = map[string]bool{
	"text": true, "value-of": true, "copy-of": true, "sequence": true, "attribute": true,
	"comment": true, "processing-instruction": true, "namespace": true, "variable": true,
	"element": true, "if": true, "choose": true, "when": true, "otherwise": true,
	"where-populated": true, "on-empty": true, "on-non-empty": true, "document": true,
	"copy": true, "next-iteration": true, "with-param": true, "break": true,
}

// motionless checks an instruction the analysis found motionless: it, and
// everything in it, reads of its focus f only what a view (or, at the
// document node, the stand-in) has.
func (b *streamBuilder) motionless(el *xdm.Node, f vfocus) bool {
	if el.Name().URI == xdm.NSXSL {
		local := el.Name().Local
		if local == "source-document" {
			// A container of its own: it reads nothing of this stream.
			if !b.avt(el, el.AttrValue("href"), f) {
				return b.refuse("nested xsl:source-document")
			}
			b.kinds[streamtrace.KindSourceDoc]++
			return true
		}
		if !streamMotionless[local] || local == "copy" && f == vNone {
			return b.refuse("motionless xsl:" + local)
		}
		if local == "with-param" && !carriedAtomic(el) {
			return b.refuse("xsl:next-iteration carries a node")
		}
	}
	return b.attrs(el, f) && b.content(el, f)
}

// content checks the children of el, a motionless instruction.
func (b *streamBuilder) content(el *xdm.Node, f vfocus) bool {
	for ch := range el.Children() {
		switch ch.Kind() {
		case xdm.KindText:
			if strings.ContainsAny(ch.Value(), "{}") {
				return b.refuse("text with a brace")
			}
		case xdm.KindElement:
			if !b.motionless(ch, f) {
				return false
			}
		}
	}
	return true
}

// streamAttrsOK are the attributes of an XSLT instruction that change
// nothing a view or the split would have to do differently, beside select,
// test and the attribute value templates attrs checks.
var streamAttrsOK = map[string]bool{
	"version": true, "xpath-default-namespace": true, "default-collation": true,
	"exclude-result-prefixes": true, "extension-element-prefixes": true, "expand-text": true,
	"use-when": true, "default-mode": true, "copy-namespaces": true, "inherit-namespaces": true,
	"name": true, "namespace": true, "separator": true, "as": true, "select": true, "test": true,
	"disable-output-escaping": true, "href": true, "streamable": true,
}

// attrs checks el's own expressions: the select (values only: what it
// returns is atomized or copied) and test of an instruction, and every
// attribute value template, at focus f. A consuming select is the split's
// and is not checked here.
func (b *streamBuilder) attrs(el *xdm.Node, f vfocus) bool {
	xslEl := el.Name().URI == xdm.NSXSL
	for a := range el.Attrs() {
		name := a.Name()
		if !xslEl {
			if !lreAttrOK(a) {
				return b.refuse("attribute xsl:" + name.Local)
			}
			if !b.avt(el, a.Value(), f) {
				return false
			}
			continue
		}
		if name.URI != "" {
			continue
		}
		if !streamAttrsOK[name.Local] {
			return b.refuse("attribute " + name.Local + " of " + elName(el))
		}
		switch name.Local {
		case "select":
			if r := b.pl.rec(el); r != nil && r.props.sweep == sweepConsuming {
				continue
			}
			if !b.expr(el, a.Value(), f, false) {
				return false
			}
		case "test":
			if !b.expr(el, a.Value(), f, true) {
				return false
			}
		case "as":
		default:
			if !b.avt(el, a.Value(), f) {
				return false
			}
		}
	}
	return true
}

// expr checks the expression src written on el: it reads of focus f only
// what f has, and returns nodes of it only where nodeOK.
func (b *streamBuilder) expr(el *xdm.Node, src string, f vfocus, nodeOK bool) bool {
	e := parseAt(el, src)
	if e == nil || !(viewCheck{pos: true}).safe(e, f, nodeOK) {
		return b.refuse("not motionless on the view, in " + elName(el) + ": " + src)
	}
	return true
}

// avt checks an attribute value template written on el.
func (b *streamBuilder) avt(el *xdm.Node, src string, f vfocus) bool {
	if !strings.ContainsAny(src, "{}") {
		return true
	}
	exprs, ok := avtExpressions(src)
	if !ok {
		return b.refuse("attribute value template " + src)
	}
	for _, s := range exprs {
		if !b.expr(el, s, f, false) {
			return false
		}
	}
	return true
}

// --- Compiling ---------------------------------------------------------------

// noteStreamSelect splits the select of n, a consuming value-of, copy-of,
// sequence or attribute of the core plan being compiled; sel is its
// compiled select, written where ns resolves names.
func (c *compiler) noteStreamSelect(n *xdm.Node, instr Instruction, sel *xpath.Compiled,
	ns xpath.NamespaceResolver) {

	op := c.streamOp(n)
	if op == nil || op.loop {
		return
	}
	op.instr, op.sel = instr, sel
	simple := isXSL(n, "value-of") || isXSL(n, "attribute")
	if op.split, op.dead = c.splitFor(sel, ns, op.doc, simple); op.dead != "" {
		return
	}
	c.stream.byInstr[instr] = op
}

// noteStreamLoop splits the select of n, a consuming for-each or iterate of
// the core plan being compiled.
func (c *compiler) noteStreamLoop(n *xdm.Node, instr Instruction, sel *xpath.Compiled,
	ns xpath.NamespaceResolver) {

	op := c.streamOp(n)
	if op == nil || !op.loop {
		return
	}
	op.instr, op.sel = instr, sel
	if op.split, op.dead = c.splitFor(sel, ns, op.doc, false); op.dead != "" {
		return
	}
	s := op.split
	if s.cond != nil || s.ctxItem || s.red != "" || s.tail != nil || s.tailText != "" ||
		s.steps == nil || !isVarRef(s.outer.Expr(), splitItems) {
		op.dead = "loop select is not a bare path"
		return
	}
	c.stream.byInstr[instr] = op
}

func (c *compiler) streamOp(n *xdm.Node) *streamOp {
	if c.stream == nil {
		return nil
	}
	return c.stream.ops[n]
}

// splitFor splits sel for the cursor, or says why not. simple says sel
// makes simple content (xsl:value-of, xsl:attribute), which atomizes what it
// returns.
func (c *compiler) splitFor(sel *xpath.Compiled, ns xpath.NamespaceResolver, doc, simple bool) (*streamSplit, string) {
	if sel.CompatMode() {
		return nil, "XPath 1.0 compatibility mode"
	}
	e := reparse(sel, ns)
	if e == nil {
		return nil, "select does not parse again"
	}
	if _, ctx := e.(*xpath.ContextItem); simple && (ctx || noTextNodes(e)) {
		// Simple content (§5.7.2) drops zero-length text nodes, merges
		// adjacent ones, then atomizes: with no text node in the value the
		// first two do nothing, so it is data() of it, which the split
		// reads off the cursor instead of grounding each node.
		e = &xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: "data"}, Args: []xpath.Expr{e}}
	}
	s, why := splitStream(e, sel, doc)
	if s == nil {
		return nil, "split: " + why
	}
	return s, ""
}

// noTextNodes reports, conservatively, whether e can return no text node:
// a path ending in elements or attributes by name, or atomic values.
func noTextNodes(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Step:
		_, name := x.Test.(*xpath.NameTest)
		kt, kind := x.Test.(*xpath.KindTest)
		return name && x.Axis != xpath.AxisNamespace ||
			kind && kt.Kind == xdm.KindElement && !kt.Any
	case *xpath.PathExpr:
		return len(x.Steps) > 0 && noTextNodes(x.Steps[len(x.Steps)-1])
	case *xpath.FilterExpr:
		return noTextNodes(x.Base)
	case *xpath.FuncCall:
		if outerInner(x) {
			return noTextNodes(x.Args[0])
		}
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !noTextNodes(it) {
				return false
			}
		}
		return true
	}
	return atomicValued(e)
}

func isVarRef(e xpath.Expr, q xdm.QName) bool {
	v, ok := e.(*xpath.VarRef)
	return ok && v.Name == q
}

// ready finishes the plan once the container has compiled: every consuming
// instruction compiled and split, the memory rule kept (DESIGN section 2.1,
// rule 3), each loop's strategy chosen; it returns why not.
func (x *streamExec) ready() string {
	if why := x.readyOps(x.top, 0, &x.kinds); why != "" {
		return why
	}
	return ""
}

// readyOps readies ops, whose context element is at depth minD or deeper
// (0: the document node), adding their loops' sNodes to kinds.
func (x *streamExec) readyOps(ops []*streamOp, minD int, kinds *[streamtrace.NumKinds]int64) string {
	for _, op := range ops {
		switch {
		case op.instr == nil:
			return "not compiled (use-when)"
		case op.dead != "":
			return op.dead
		case !op.loop:
			if why := x.readySelect(op, minD); why != "" {
				return why
			}
			continue
		}
		s := op.split
		k := minD + s.minDepth()
		switch {
		case op.planned && len(op.inner) == 0:
			// A motionless body reads its match's view only: matches below
			// it are found as the content goes by.
			s.mode = matchView
		case op.planned && (!s.nests || s.outermost && !s.predFail) && x.readyOps(op.inner, k, &op.kinds) == "":
			// Matches cannot nest, or those inside an outermost one are not
			// results and no predicate's error is to be raised there.
			s.mode = matchBody
		case op.grounded && k >= 2:
			// The body reads its match's content as it likes, on a copy.
			s.mode = matchGround
			kinds[streamtrace.KindGround]++
			kinds[streamtrace.KindForEach]++
			continue
		default:
			return "loop body: neither streamed nor grounded below the document element"
		}
		kinds[streamtrace.KindForEach]++
		for i := range kinds {
			kinds[i] += op.kinds[i]
		}
	}
	return ""
}

// readySelect readies a select op whose context element is at depth minD or
// deeper. A copy-of whose split would collect grounded copies copies each as
// it is read instead (DESIGN section 1.6: copy-of streams to the output), so
// it holds one at a time; one that would ground the document element gives
// up there, which at the document node falls back to the tree.
func (x *streamExec) readySelect(op *streamOp, minD int) string {
	s := op.split
	if c := s.class(); isXSL(op.el, "copy-of") && (c == "nodes" || c == "subtree") &&
		s.cond == nil && s.red == "" && isVarRef(s.outer.Expr(), splitItems) {
		switch {
		case s.ctxItem && minD < 2:
			return "copy-of(.) of the document element"
		case minD == 0:
			s.noRoot = s.minDepth() < 2
		case minD+s.minDepth() < 2:
			return "copy-of may ground the document element"
		}
		op.copies = true
		return ""
	}
	if why := s.memory(minD); why != "" {
		return elName(op.el) + " " + why + ": " + op.sel.Source()
	}
	return ""
}

// minDepth is the least depth below the context a match of P can be at.
func (s *streamSplit) minDepth() int {
	n := 0
	for _, st := range s.steps {
		if st.axis != xpath.AxisDescendantOrSelf {
			n++
		}
	}
	return n
}

// memory applies the memory rule to a select split whose context element is
// at depth minD or deeper: grounded copies collected, or the context
// grounded, are below the tree only below the document element; and a fold
// that can give up falls back to the tree only at the document node.
func (s *streamSplit) memory(minD int) string {
	if s.cond != nil {
		if why := s.then.memory(minD); why != "" {
			return why
		}
		return s.els.memory(minD)
	}
	switch c := s.class(); {
	case (c == "nodes" || c == "subtree") && minD < 2:
		return "collects grounded copies (" + c + ") of the document element"
	case (s.red == "min" || s.red == "max") && minD > 0:
		return s.red + "() below the document node cannot fall back to the tree"
	}
	return ""
}

// --- Running -------------------------------------------------------------------

// errStreamPlan is a run the plan does not cover: a context read twice. It
// sends the container to the tree, as any failure does.
var errStreamPlan = errors.New("streaming: a context read twice")

// streamRun is one run of a core plan.
type streamRun struct {
	x *streamExec
	c *xdmstream.Cursor
	// d is the depth of the context the consuming instruction met next
	// reads (0: the document node), used whether it has been read, and off
	// that the instructions running now read a view or a copy, never the
	// cursor (a motionless or grounded loop body).
	d    int
	used bool
	off  bool
	// For the fallback of a select that gives up: the container, its href,
	// and how to unmark the document as read before the tree reads it.
	i      *sourceDocumentInstr
	href   string
	unread func()
}

// feed returns the feed that drives instr from the cursor, or nil when instr
// runs as usual; rt is the runtime the instruction runs in.
func (run *streamRun) feed(instr Instruction, rt *runtime) *burstFeed {
	op := run.x.byInstr[instr]
	if op == nil || run.off {
		return nil
	}
	if op.copies {
		return &burstFeed{instr: instr, read: func(each func(xdm.Item) error) error {
			if run.used {
				return errStreamPlan
			}
			run.used = true
			err := op.split.loop(rt.ctx, run.c, run.d, each, nil)
			if err == errSplitGiveUp && run.d == 0 {
				// The document element itself is copied: from the tree,
				// nothing of it having been copied yet.
				items, err := run.fallback(rt, op.sel)
				if err != nil {
					return err
				}
				for _, it := range xdm.Flatten(items) {
					if err := each(it); err != nil {
						return err
					}
				}
				return nil
			}
			return err
		}}
	}
	if !op.loop {
		return &burstFeed{instr: instr, eval: func(rt *runtime) (xdm.Sequence, error) {
			if run.used {
				return nil, errStreamPlan
			}
			run.used = true
			seq, err := op.split.eval(rt.ctx, run.c, run.d)
			if err == errSplitGiveUp && run.d == 0 {
				return run.fallback(rt, op.sel)
			}
			return seq, err
		}}
	}
	return &burstFeed{instr: instr, read: func(each func(xdm.Item) error) error {
		if run.used {
			return errStreamPlan
		}
		run.used = true
		d := run.d
		if op.split.mode == matchBody {
			return op.split.loop(rt.ctx, run.c, d, nil, func(v *xdm.Node, md int) (bool, error) {
				run.d, run.used = md, false
				err := each(v)
				read := run.used
				run.d, run.used = d, true
				return read, err
			})
		}
		return op.split.loop(rt.ctx, run.c, d, func(it xdm.Item) error {
			off := run.off
			run.off = true
			err := each(it)
			run.off = off
			return err
		}, nil)
	}}
}

// fallback evaluates sel, a select at the document node whose split gave
// up, on the tree: the document is read again as a tree, and the stream is
// left where it is (nothing else reads it: its one consumer was sel).
func (run *streamRun) fallback(rt *runtime, sel *xpath.Compiled) (xdm.Sequence, error) {
	streamtrace.Fallback()
	run.unread()
	root, err := run.i.load(rt, run.href)
	if err != nil {
		return nil, err
	}
	rt.streamedTrees[root] = true
	return sel.Eval(rt.withCurrent(root, 1, 1).ctx)
}

// drainAll passes over every event: NextTag returns EndDocument.
var drainAll = xdmstream.Filter{Accept: acceptNone}

// drain reads the rest of the context, the document, dropping it.
func (run *streamRun) drain() error {
	for {
		ev, err := run.c.NextTag(&drainAll)
		if err != nil {
			return err
		}
		if ev.Kind == xdmstream.EndDocument {
			return nil
		}
	}
}

// count records the run's sNodes in streamtrace.
func (x *streamExec) count() {
	streamtrace.Run(streamtrace.Core)
	for k, n := range x.kinds {
		if n > 0 {
			streamtrace.Node(streamtrace.Kind(k), n)
		}
	}
}

// executeStream runs i on its core plan when its resolver can stream the
// document, and reports whether it did (DESIGN section 2.1, rule 4).
func (i *sourceDocumentInstr) executeStream(rt *runtime, out *outputBuilder, href string) (bool, error) {
	if noBursts || noCore || streamtrace.Off() || validSourceDocumentURI(href) != nil || strings.ContainsRune(href, '#') {
		return false, nil
	}
	// The resolvers Transform wraps around the caller's, as executeBursts
	// looks through them; whitespace is stripped as the cursor parses.
	base := i.base(rt)
	pkg := packageOf(rt.ctx)
	var (
		rd    *readDocResolver
		br    burstResolver
		strip func(*xdm.Node) bool
	)
	for d := rt.ctx.Env().Docs; br == nil; {
		switch v := d.(type) {
		case *readDocResolver:
			if _, seen := v.docs[docKey{href, base, pkg}]; seen {
				return false, nil
			}
			rd, d = v, v.inner
		case *stripSpaceResolver:
			strip, d = v.sheet.whitespaceStripper(pkg), v.inner
		case burstResolver:
			br = v
		default:
			return false, nil
		}
	}
	docURI, ok := br.wantsBursts(href, base)
	if !ok {
		return false, nil
	}
	unread := func() {}
	if rd != nil && docURI != "" {
		if !rd.read[docURI] {
			unread = func() { delete(rd.read, docURI) }
		}
		rd.read[docURI] = true
		if rd.written[docURI] {
			return true, fmt.Errorf(
				"XTDE1500: this transformation reads %q, which xsl:result-document "+
					"has already written", docURI)
		}
	}
	i.stream.count()
	sig, mark := signature(out), rt.localsMark()
	n0, b0 := xpathleaf.Charged(rt.ctx)
	err := i.runStream(rt, out, br, href, base, strip, unread)
	if err == nil {
		return true, nil
	}
	// The re-run. What the failed run charged is dropped with it.
	streamtrace.Rerun()
	if traceRerun != nil {
		traceRerun(err)
	}
	rt.localsPop(mark)
	if n1, b1 := xpathleaf.Charged(rt.ctx); n1 > n0 || b1 > b0 {
		xpathleaf.Refund(rt.ctx, max(n1-n0, 0), max(b1-b0, 0))
	}
	clean := signature(out) == sig
	var pe *streamtrace.PostureError
	if errors.As(err, &pe) && !clean {
		return true, fmt.Errorf("streaming: internal error after output was written: %w", err)
	}
	unread()
	terr := i.executeTree(rt, out, href)
	if terr == nil && !clean {
		streamtrace.Guard()
		return true, fmt.Errorf("streaming: internal error: the streamed run failed where the tree did not: %w", err)
	}
	return true, terr
}

// runStream runs i's body on a cursor over href. A posture fault is
// returned as its error; any other panic goes on.
func (i *sourceDocumentInstr) runStream(rt *runtime, out *outputBuilder, br burstResolver,
	href, base string, strip func(*xdm.Node) bool, unread func()) (err error) {

	defer func() {
		if p := recover(); p != nil {
			pe, ok := p.(*streamtrace.PostureError)
			if !ok {
				panic(p)
			}
			err = pe
		}
	}()
	return br.streamDoc(rt.ctx, href, base, func(c *xdmstream.Cursor) error {
		if strip != nil {
			c.StripWhitespace(strip)
		}
		run := &streamRun{x: i.stream, c: c, i: i, href: href, unread: unread}
		// The body's focus is the document node, which the stream does not
		// build: the plan reads of it only what an empty one has.
		sub := rt.withCurrent(xdm.NewTree().Root, 1, 1).clearCurrentRule()
		sub.bursts = &burstFeed{run: run}
		if i.stream.drainFirst {
			if err := run.drain(); err != nil {
				return err
			}
			run.used = true
		}
		if err := execSequence(i.body, sub, out); err != nil {
			return err
		}
		if !run.used {
			return run.drain()
		}
		return nil
	})
}

// whitespaceStripper is xsl:strip-space for the cursor: whitespace-only text
// inside el is stripped as stripCopy strips it for the tree -- the package's
// declarations matched against el's name, unless the nearest xml:space on el
// or an ancestor says preserve. (The simple-type exemption reads an
// annotation, which a streamed document, never validated, does not have.)
func (s *Stylesheet) whitespaceStripper(pkg int) func(*xdm.Node) bool {
	return func(el *xdm.Node) bool {
		if !s.stripsElement(pkg, el.Name()) {
			return false
		}
		for n := el; n != nil; n = n.Parent() {
			if a := n.Attr(xdm.NSXML, "space"); a != nil {
				return a.Value() != "preserve"
			}
		}
		return true
	}
}

// streamDoc implements burstResolver. It reads the file as parseBursts does
// -- the same confinement, options, entity allowance and byte limit -- on a
// cursor fn reads, and caches nothing.
func (r *FileResolver) streamDoc(ctx *xpath.Context, uri, base string, fn func(*xdmstream.Cursor) error) error {
	path, err := r.resolvePath(uri, base)
	if err != nil {
		return err
	}
	opts := r.parseOptions(path, false).WithEntityBudget(ctx.EntityBudget())
	return r.openConfined(path, func(f *os.File) error {
		max, lim := r.byteLimit()
		tooBig := fmt.Errorf("%s exceeds the %d byte limit", path, max)
		var src io.Reader = f
		if max >= 0 {
			if fi, err := f.Stat(); err == nil && fi.Size() > max {
				return tooBig
			}
			src = io.LimitReader(f, lim)
		}
		cr := &countedReader{r: src}
		c := xdmstream.Open(cr, opts)
		defer c.Close()
		if err := fn(c); err != nil {
			return err
		}
		if max >= 0 && cr.n > max {
			return tooBig
		}
		return nil
	})
}
