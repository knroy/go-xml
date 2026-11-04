package xslt

// The streamed core (full streaming, component C5; DESIGN sections 1.6, 2).
//
// A trusted streamable xsl:source-document gets a core plan: a tree of
// sNodes over its body, built from the §19.8 plan map (C3) and run on the
// stream cursor (C1) with the streamed XPath split (C4). It is the only
// streamed path: the burst plans that came before it were retired into it
// (M1.6); their body rules live on in streamburst.go.
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
	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// traceRerun, when set, is told of the failure behind each re-run.
var traceRerun func(err error)

// traceCore, when set, is told why the core refused each trusted container
// ("" when it took it), for the coverage tests.
var traceCore func(sd *xdm.Node, why string)

// streamExec is the core plan of one container.
type streamExec struct {
	// top are the consuming instructions read at the document node, and ops
	// every one of the plan, by element; byInstr is filled as they compile.
	top     []*streamOp
	ops     map[*xdm.Node]*streamOp
	byInstr map[Instruction]*streamOp
	// byExpr holds the exprOps by their compiled expressions.
	byExpr map[*xpath.Compiled]*streamOp
	// kinds counts the plan's sNodes outside the loops; ready adds theirs.
	kinds [streamtrace.NumKinds]int64
	// drainFirst says nothing at the document node consumes: the document
	// is read through before the body runs, so its errors come first.
	drainFirst bool
	// promoted are the instructions promote took as consuming.
	promoted map[*xdm.Node]bool
	// exprOps are the ops of consuming expressions that are not an
	// instruction's select: a value template's part, an xsl:with-param's
	// select (byExpr, once compiled).
	exprOps []*streamOp
	// For apply ops (streamapply.go): class is the kind of node the plan's
	// context is, and modes the modes #current and xsl:next-match may name
	// in it (those of the template rule it is the body of, or that holds
	// the container); nil when no rule does.
	class nodeClass
	modes []string
	// rule says it is a template rule's body (streamapply.go), whose
	// context element may be, but need not be, the document element.
	rule bool
	// tries are the consumers of each streamed xsl:try's body (streamtry.go).
	tries [][]*streamOp
	// groundRule says it is a rule's body that runs on a grounded copy of
	// its element (streamtee_ground.go).
	groundRule bool
}

func newStreamExec() *streamExec {
	return &streamExec{ops: map[*xdm.Node]*streamOp{}, byInstr: map[Instruction]*streamOp{},
		byExpr: map[*xpath.Compiled]*streamOp{}, promoted: map[*xdm.Node]bool{}}
}

// streamOp is a consuming instruction of the plan.
type streamOp struct {
	x     *streamExec // the plan it is in
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
	// apply says it is an xsl:apply-templates, xsl:next-match or
	// xsl:apply-imports, and how it reads its context (streamapply.go).
	apply applyKind
	// group says it is an xsl:for-each-group (streamgroup.go), fromGroup
	// that its population is the current group of the one it is in; inGroup
	// that it is in the body of one, where current-group() is that group's,
	// and key that current-grouping-key() is in scope.
	group, fromGroup, inGroup, key bool
	keyAttr                        *xdm.QName // a grouping's key attribute (keyAttribute)
	// groundPop says a grouping's population is grounded: its select is
	// read as a select op's, the tree's code groups it (groundedGroup).
	groundPop bool
	// heldPop says that population is the stream's own nodes, held as
	// copies for the tree's grouping (streamgroup_held.go).
	heldPop bool
	// avt, for the consuming part of a literal result element's attribute
	// value template, is that part's expression (avtOp).
	avt string
	// fork says it is an xsl:fork or xsl:map whose prongs consume the
	// document node on a tee (streamfork.go); prongs are each prong's
	// consuming instructions, empty for one that reads nothing.
	fork   bool
	prongs [][]*streamOp
	// accAfter says it is an instruction that consumes its context only by
	// reading accumulator-after of it, which runs as it is (streamaccum.go).
	accAfter bool
	// pop, for a loop whose select is not a bare path, is its population
	// (streampop.go); split is then its stream part's.
	pop *streamPop
	// tee, for a variable's map constructor whose entries each consume, is
	// its plan in place of a split (streamtee_map.go).
	tee *streamTee
	// inTry says it is a fork in a streamed xsl:try's body (streamtee_try.go).
	inTry bool
}

// streamBuilder builds a plan from the source of a container.
type streamBuilder struct {
	pl    *streamPlan
	x     *streamExec
	ops   *[]*streamOp // the list the consuming instructions met go into
	kinds *[streamtrace.NumKinds]int64
	why   string
	// noIdent says the focus is a copy standing in for a node of the
	// document: nothing may ask for its identity (streamapply.go); up that
	// it is a leaf's copy with its ancestors' (streamleaf.go), so upward
	// steps from it are admitted, to an element as from a view.
	noIdent, up bool
	// grp says the instructions planned are those of a streamed group's body
	// (streamgroup.go), whose current-group() a consumer may read; key that
	// current-grouping-key() is in scope, there or in a loop below.
	grp, key bool
	// after says accumulator-after of the focus may be read: a consumer
	// before it in its sequence constructor has read the focus (streamaccum.go).
	after bool
}

func (b *streamBuilder) refuse(why string) bool {
	if b.why == "" {
		b.why = why
	}
	return false
}

// planStream builds the core plan of the streamable xsl:source-document sd,
// or returns nil and why not. shared says sd's module is not the whole
// stylesheet (streamtrust.go).
func planStream(sd *xdm.Node, shared bool) (*streamExec, string) {
	// @validation and @type: decided when the document is read (streamvalidin.go).
	pl := buildStreamPlan(sd, shared)
	if !pl.trusted() {
		return nil, "verdict not trusted"
	}
	x := newStreamExec()
	x.class, x.modes = classDoc, enclosingRuleModes(sd)
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
	defer func(after bool) { b.after = after }(b.after)
	b.after = false
	for ch := range el.Children() {
		switch ch.Kind() {
		case xdm.KindText:
			if strings.ContainsAny(ch.Value(), "{}") {
				op, ok := b.textTemplate(ch, el, f) // streamshapes.go
				if !ok {
					return false
				}
				if op != nil {
					if consumers++; consumers > 1 {
						return b.refuse("two consuming instructions in one sequence constructor")
					}
					b.addTextOp(op)
				}
			}
			continue
		case xdm.KindElement:
			if fallbackIgnored(ch) { // streamsplit_fns.go
				continue
			}
		default:
			continue
		}
		r := b.pl.rec(ch)
		if r == nil || !r.known || r.unverified {
			return b.refuse("no trusted record for " + elName(ch))
		}
		b.after = consumers > 0 && !b.grp
		if r.props.sweep == sweepConsuming && consumers == 0 && b.accumAfterOp(ch, f) {
			consumers++
			continue
		}
		switch r.props.sweep {
		case sweepMotionless:
			if !b.motionless(ch, f) && !b.promote(ch) {
				return false
			}
			if !b.x.promoted[ch] {
				b.kinds[streamtrace.KindTree]++
				continue
			}
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
		n := len(*b.ops)
		if !b.avtOps(el, f) {
			return false
		}
		b.kinds[streamtrace.KindElement]++
		if avt := len(*b.ops) > n; !b.seq(el, f) || avt && len(*b.ops) > n+1 {
			return b.refuse("two consuming instructions in one sequence constructor")
		}
		return true
	}
	if done, ok := b.instrAVTOp(el, f); done { // streamavt_k2.go
		return ok
	}
	local := el.Name().Local
	switch local {
	case "try":
		return b.tryOp(el, f)
	case "fork":
		return b.forkOp(el, f)
	case "map":
		return b.mapOp(el, f)
	case "map-entry":
		return b.mapEntryOp(el, f)
	case "message":
		return b.messageOp(el, f, true) // streammessage.go
	case "result-document":
		return b.resultDocOp(el, f) // streamresult.go
	}
	if el.Attr("", "select") != nil {
		switch local {
		case "value-of", "copy-of", "sequence", "attribute", "break":
			return b.selectOp(el, f)
		case "on-empty", "on-non-empty":
			return b.selectOp(el, f) // streamonempty.go
		case "comment", "processing-instruction":
			return b.selectOp(el, f) // their text constructed from the values, as xsl:sequence's
		case "perform-sort":
			return b.performSortOp(el, f) // streamsort_l7.go
		case "for-each", "iterate":
			return b.loopOp(el, f)
		case "copy":
			return b.copySelectOp(el, f) // streamcopy.go
		case "apply-templates":
			return b.applyOp(el, f)
		case "for-each-group":
			return b.groupOp(el, f)
		case "variable":
			return b.variableOp(el, f) // streamvariable.go
		}
		return b.refuse("consuming select of xsl:" + local)
	}
	switch local {
	case "copy":
		if f == vNone {
			return b.copyDoc(el)
		}
		fallthrough
	case "element", "attribute", "comment", "processing-instruction", "document",
		"sequence", "value-of", "variable", "where-populated", "on-empty", "on-non-empty":
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
		if done, ok := b.consumingIf(el, f); done { // streamshapes.go
			return ok
		}
		if !b.attrs(el, f) {
			return false
		}
		b.kinds[streamtrace.KindChoose]++
		return b.seq(el, f)
	case "choose":
		if done, ok := b.consumingChoose(el, f); done { // streamshapes.go
			return ok
		}
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
	case "apply-templates":
		return b.applyOp(el, f)
	case "next-match", "apply-imports":
		return b.nextOp(el, f)
	case "next-iteration":
		return b.nextIteration(el, f)
	}
	return b.refuse("consuming xsl:" + local)
}

// avtOps checks the attributes of el, a literal result element that
// consumes its context: their value templates motionless, or one part of
// one of them the consumer (an op whose split is made as the element
// compiles, noteStreamAVT), evaluated first, as the tree evaluates the
// attributes before the content.
func (b *streamBuilder) avtOps(el *xdm.Node, f vfocus) bool {
	var op *streamOp
	for a := range el.Attrs() {
		if !lreAttrOK(a) && !resultValidation(el, a.Name()) && !b.useSets(el, a, f) {
			return b.refuse("attribute xsl:" + a.Name().Local)
		}
		if a.Name().URI == xdm.NSXSL || !strings.ContainsAny(a.Value(), "{}") {
			continue
		}
		exprs, ok := avtExpressions(a.Value())
		if !ok {
			return b.refuse("attribute value template " + a.Value())
		}
		for _, src := range exprs {
			if e := parseAt(el, src); e != nil && (viewCheck{pos: true, noIdent: b.noIdent, up: b.up, key: b.key, acc: !b.noIdent && !b.grp, after: b.after}).safe(e, f, false) {
				continue
			}
			if op != nil {
				return b.refuse("two consuming attribute value templates")
			}
			op = &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key, avt: src}
		}
	}
	if op != nil {
		*b.ops = append(*b.ops, op)
		b.x.exprOps = append(b.x.exprOps, op)
		b.kinds[streamtrace.KindSelect]++
	}
	return true
}

// nextIteration plans an xsl:next-iteration whose one consuming parameter
// (§19.8.4.28: its value grounded, a copy of the item, or values made of
// it) is read off the cursor at the item, the others motionless values; a
// node they carry over is a copy, which stays valid (no stream node may be
// carried: the analysis makes it free-ranging).
func (b *streamBuilder) nextIteration(el *xdm.Node, f vfocus) bool {
	var op *streamOp
	for ch := range el.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "",
			ch.Kind() == xdm.KindComment, ch.Kind() == xdm.KindPI:
			continue
		case !isXSL(ch, "with-param"):
			return b.refuse(elName(ch) + " in xsl:next-iteration")
		}
		// The analysis records the instruction, not its parameters: one a
		// view cannot answer is the consumer, which the split decides.
		if sb := b.strict(); sb.motionless(ch, f) {
			continue
		}
		if op != nil || ch.Attr("", "select") == nil {
			return b.refuse("consuming xsl:with-param of xsl:next-iteration")
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
	b.kinds[streamtrace.KindElement]++
	return true
}

// noteStreamParam splits sel, the select of n, an xsl:with-param of the
// plans being compiled that consumes (nextIteration).
func (c *compiler) noteStreamParam(n *xdm.Node, sel *xpath.Compiled, ns xpath.NamespaceResolver) {
	if c.stream == nil && len(c.ruleExecs) == 0 {
		return
	}
	for _, x := range append([]*streamExec{c.stream}, c.ruleExecs...) {
		if x == nil {
			continue
		}
		for _, op := range x.exprOps {
			if op.el == n && op.avt == "" && op.instr == nil {
				op.instr, op.sel = exprInstr{}, sel
				if op.split, op.dead = c.splitFor(sel, ns, op.doc, false, op.inGroup, op.key); op.dead == "" {
					x.byExpr[sel] = op
				} else if op.tee = c.teeFor(op, sel, ns); op.tee != nil { // streamtee_map.go
					op.dead = ""
					x.byExpr[sel] = op
				}
			}
		}
	}
}

// noteStreamAVT splits the part of the value template a of the literal
// result element n that an op of the plans being compiled consumes.
func (c *compiler) noteStreamAVT(n *xdm.Node, a *avt, ns xpath.NamespaceResolver) {
	if c.stream == nil && len(c.ruleExecs) == 0 {
		return
	}
	for _, x := range append([]*streamExec{c.stream}, c.ruleExecs...) {
		if x == nil {
			continue
		}
		for _, op := range x.exprOps {
			if op.el != n || op.instr != nil {
				continue
			}
			for _, p := range a.parts {
				if p.expr == nil || p.expr.Source() != op.avt {
					continue
				}
				op.instr, op.sel = exprInstr{}, p.expr
				if op.split, op.dead = c.splitFor(p.expr, ns, op.doc, true, op.inGroup, op.key); op.dead == "" {
					x.byExpr[p.expr] = op
				}
				break
			}
		}
	}
}

// exprInstr stands for the instruction of an expression op (exprOps),
// which runs from its holder's: no feed is made for it.
type exprInstr struct{}

func (exprInstr) Execute(*runtime, *outputBuilder) error { return errStreamPlan }

// evalStreamExpr evaluates e -- a part of an attribute value template, a
// variable's or parameter's select -- from the stream when it is an
// expression op of the plan running.
func (rt *runtime) evalStreamExpr(e *xpath.Compiled) (xdm.Sequence, error) {
	if f := rt.bursts; f != nil && f.run != nil && !f.run.off {
		if op := f.run.x.byExpr[e]; op != nil {
			return f.run.evalOp(rt, op)
		}
	}
	return e.Eval(rt.ctx)
}

// selectOp plans a value-of, copy-of, sequence or attribute whose select
// consumes; its split is made as it compiles (noteStreamSelect).
func (b *streamBuilder) selectOp(el *xdm.Node, f vfocus) bool {
	if !b.attrs(el, f) {
		return false
	}
	op := &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key}
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
	op := &streamOp{x: b.x, el: el, doc: f == vNone, loop: true, inGroup: b.grp, key: b.key}
	inner := &streamBuilder{pl: b.pl, x: b.x, ops: &op.inner, kinds: &op.kinds, key: b.key}
	var body []*xdm.Node
	op.planned = true
	for ch := range el.Children() {
		if ch.Kind() != xdm.KindElement {
			if ch.Kind() == xdm.KindText && strings.ContainsAny(ch.Value(), "{}") {
				// A text value template motionless on each match's view
				// (streamshapes.go); a consuming one is refused.
				if tv, ok := inner.textTemplate(ch, el, vElem); !ok || tv != nil {
					return b.refuse("text with a brace")
				}
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
	op.planned = inner.members(el, body, vElem)
	op.grounded = burstBody(el) || !op.planned && b.groundedSelect(el) // streampop_snapshot.go
	if !op.planned && !op.grounded {
		return b.refuse(inner.why)
	}
	b.x.ops[el] = op
	*b.ops = append(*b.ops, op)
	return true
}

// members plans the elements body of el's sequence constructor, whose
// focus is f (a loop's: an element's view): seq, over a subset of the
// children.
func (b *streamBuilder) members(el *xdm.Node, body []*xdm.Node, f vfocus) bool {
	b.kinds[streamtrace.KindSeq]++
	consumers := 0
	defer func(after bool) { b.after = after }(b.after)
	for _, ch := range body {
		if fallbackIgnored(ch) { // streamsplit_fns.go
			continue
		}
		r := b.pl.rec(ch)
		if r == nil || !r.known || r.unverified {
			return b.refuse("no trusted record for " + elName(ch))
		}
		b.after = consumers > 0 && !b.grp
		if r.props.sweep == sweepConsuming && consumers == 0 && b.accumAfterOp(ch, f) {
			consumers++
			continue
		}
		if r.props.sweep == sweepMotionless {
			if !b.motionless(ch, f) && !b.promote(ch) {
				return false
			}
			if !b.x.promoted[ch] {
				b.kinds[streamtrace.KindTree]++
				continue
			}
		} else if r.props.sweep != sweepConsuming {
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

// promote takes el, an instruction the analysis calls motionless whose
// select a view cannot answer, as a consuming one instead, when it is one a
// split may take (selectOp): the split, which checks the select's own
// structure, decides at compile time. (The analysis's simple map is one such
// verdict: the sweep of A ! B is B's, where §19.8.8.7 says the wider.)
//
// A literal result element is promoted too, so that its content is planned
// as a consumer's: one such select may be inside it.
func (b *streamBuilder) promote(el *xdm.Node) bool {
	if el.Name().URI == xdm.NSXSL {
		switch el.Name().Local {
		case "value-of", "copy-of", "sequence":
			if el.Attr("", "select") == nil {
				return false
			}
		default:
			return false
		}
	}
	b.why = ""
	b.x.promoted[el] = true
	return true
}

// streamMotionless are the instructions an sTree may hold.
var streamMotionless = map[string]bool{
	"text": true, "value-of": true, "copy-of": true, "sequence": true, "attribute": true,
	"comment": true, "processing-instruction": true, "namespace": true, "variable": true,
	"element": true, "if": true, "choose": true, "when": true, "otherwise": true,
	"where-populated": true, "on-empty": true, "on-non-empty": true, "document": true,
	"copy": true, "next-iteration": true, "with-param": true, "break": true,
	"fork": true, "map": true, "map-entry": true,
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
		if local == "apply-templates" {
			return b.attrApply(el, f)
		}
		if local == "try" {
			return b.tryMotionless(el, f)
		}
		if local == "result-document" {
			return b.resultDocAttrs(el, f) && b.content(el, f) // streamresult.go
		}
		if b.noIdent && (local == "sequence" || local == "variable") {
			// They could hand the copy on as the node it stands for.
			return b.refuse("xsl:" + local + " on a copied text, comment or PI node")
		}
		if local == "message" {
			return b.messageOp(el, f, false) // streammessage.go
		}
		if local == "for-each" {
			return b.motionlessLoop(el, f) // streamvariable.go
		}
		if !streamMotionless[local] || local == "copy" && f == vNone {
			return b.refuse("motionless xsl:" + local)
		}
		// A node an xsl:with-param carries on is a copy, valid however
		// far the cursor moves: §19.8.4.28's usage (navigation of item()*)
		// makes passing a node of the stream free-ranging, so a trusted
		// container carries none.

	}
	return b.attrs(el, f) && b.content(el, f)
}

// content checks the children of el, a motionless instruction.
func (b *streamBuilder) content(el *xdm.Node, f vfocus) bool {
	for ch := range el.Children() {
		switch ch.Kind() {
		case xdm.KindText:
			if strings.ContainsAny(ch.Value(), "{}") {
				if op, ok := b.textTemplate(ch, el, f); !ok || op != nil {
					return b.refuse("text with a brace") // streamshapes.go
				}
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
	"mode": true, "tunnel": true, "required": true, "key": true,
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
			if !lreAttrOK(a) && !resultValidation(el, name) && !b.useSets(el, a, f) {
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
		if !streamAttrsOK[name.Local] && !resultValidation(el, name) && !b.useSets(el, a, f) {
			return b.refuse("attribute " + name.Local + " of " + elName(el))
		}
		switch name.Local {
		case "select":
			if r := b.pl.rec(el); r != nil && r.props.sweep == sweepConsuming || b.x.promoted[el] {
				continue
			}
			if !b.expr(el, a.Value(), f, false) {
				return false
			}
		case "test":
			if !b.expr(el, a.Value(), f, true) {
				return false
			}
		case "key": // xsl:map-entry's
			if !b.expr(el, a.Value(), f, false) {
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
	if e == nil || !(viewCheck{pos: true, noIdent: b.noIdent, up: b.up, key: b.key, acc: !b.noIdent && !b.grp, after: b.after}).safe(e, f, nodeOK) {
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
// sequence, attribute, comment or processing-instruction of the core plan
// being compiled; sel is its
// compiled select, written where ns resolves names.
func (c *compiler) noteStreamSelect(n *xdm.Node, instr Instruction, sel *xpath.Compiled,
	ns xpath.NamespaceResolver) {

	for _, op := range c.streamOps(n) {
		if op.loop || op.apply != applyNone {
			continue
		}
		op.instr, op.sel = instr, sel
		simple := isXSL(n, "value-of") || isXSL(n, "attribute")
		if op.split, op.dead = c.splitFor(sel, ns, op.doc, simple, op.inGroup, op.key); op.dead != "" {
			continue
		}
		op.x.byInstr[instr] = op
	}
}

// noteStreamLoop splits the select of n, a consuming for-each or iterate of
// the core plan being compiled.
func (c *compiler) noteStreamLoop(n *xdm.Node, instr Instruction, sel *xpath.Compiled,
	ns xpath.NamespaceResolver) {

	for _, op := range c.streamOps(n) {
		if !op.loop {
			continue
		}
		op.instr, op.sel = instr, sel
		if op.split, op.dead = c.splitFor(sel, ns, op.doc, false, op.inGroup, op.key); op.dead != "" {
			continue
		}
		if !op.split.bare() {
			if op.pop, op.split, op.dead = c.popFor(op, ns); op.dead != "" {
				continue
			}
		}
		op.x.byInstr[instr] = op
	}
}

// bare reports whether s is a path and nothing else: no choice, tail,
// reducer or outer, the matches what it returns.
func (s *streamSplit) bare() bool {
	return s.cond == nil && !s.ctxItem && s.red == "" && s.tail == nil && s.tailText == "" &&
		s.steps != nil && isVarRef(s.outer.Expr(), splitItems) && s.skip == 0
}

// streamOps returns the ops of the plans being compiled -- the container's,
// and those of the template rule whose body is (streamapply.go) -- that n is.
func (c *compiler) streamOps(n *xdm.Node) []*streamOp {
	var ops []*streamOp
	if c.stream != nil {
		if op := c.stream.ops[n]; op != nil {
			ops = append(ops, op)
		}
	}
	for _, x := range c.ruleExecs {
		if op := x.ops[n]; op != nil {
			ops = append(ops, op)
		}
	}
	return ops
}

// splitFor splits sel for the cursor, or says why not. simple says sel
// makes simple content (xsl:value-of, xsl:attribute), which atomizes what it
// returns. group and key are splitStreamIn's: sel is in the body of a
// streamed xsl:for-each-group, or in a loop below one.
func (c *compiler) splitFor(sel *xpath.Compiled, ns xpath.NamespaceResolver, doc, simple, group, key bool) (*streamSplit, string) {
	if sel.CompatMode() {
		return nil, "XPath 1.0 compatibility mode"
	}
	e := reparse(sel, ns)
	if e == nil {
		return nil, "select does not parse again"
	}
	e, checks := foreignSets(c.inlineCalls(e)) // streamsplit_union.go
	if _, ctx := e.(*xpath.ContextItem); simple && (ctx || noTextNodes(e)) {
		// Simple content (§5.7.2) drops zero-length text nodes, merges
		// adjacent ones, then atomizes: with no text node in the value the
		// first two do nothing, so it is data() of it, which the split
		// reads off the cursor instead of grounding each node. Of a choice,
		// it is the choice of data() of each branch, which the split takes.
		data := func(x xpath.Expr) xpath.Expr {
			return &xpath.FuncCall{Name: xdm.QName{URI: fnNS, Local: "data"}, Args: []xpath.Expr{x}}
		}
		if x, ok := e.(*xpath.IfExpr); ok {
			e = &xpath.IfExpr{Cond: x.Cond, Then: data(x.Then), Else: data(x.Else)}
		} else {
			e = liftData(data(e))
		}
	}
	s, why := splitStreamIn(e, sel, doc, group, key)
	if s == nil {
		return nil, "split: " + why
	}
	for _, x := range checks {
		s.checks = append(s.checks, xpathleaf.WithExpr(sel, x).(*xpath.Compiled))
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
		if isCurrentGroup(x) {
			return true // a streamed group's members are elements
		}
	case *xpath.SimpleMap:
		return noTextNodes(x.Right) // lane L2: what R gives
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !noTextNodes(it) {
				return false
			}
		}
		return true
	case *xpath.IfExpr:
		return noTextNodes(x.Then) && noTextNodes(x.Else)
	case *xpath.BinaryOp:
		switch x.Op { // streamsplit_union.go
		case "union":
			return noTextNodes(x.Left) && noTextNodes(x.Right)
		case "intersect":
			return noTextNodes(x.Left) || noTextNodes(x.Right)
		case "except":
			return noTextNodes(x.Left)
		}
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
	minD := 0
	if x.class == classElem {
		minD = 1 // a rule's context element: the document element, or below
	}
	if why := x.readyOps(x.top, minD, &x.kinds); why != "" {
		return why
	}
	return x.readyTries()
}

// readyOps readies ops, whose context element is at depth minD or deeper
// (0: the document node), adding their loops' sNodes to kinds.
func (x *streamExec) readyOps(ops []*streamOp, minD int, kinds *[streamtrace.NumKinds]int64) string {
	for _, op := range ops {
		switch {
		case op.accAfter:
			continue
		case op.instr == nil:
			return "not compiled (use-when)"
		case op.dead != "":
			return op.dead
		case op.fork:
			if why := x.readyFork(op, kinds); why != "" {
				return why
			}
			continue
		case op.apply != applyNone:
			if why := op.readyApply(); why != "" {
				return why
			}
			continue
		case op.group && !op.groundPop:
			if why := x.readyGroup(op, minD, kinds); why != "" {
				return why
			}
			continue
		case !op.loop:
			if why := x.readySelect(op, op.ctxDepth(minD)); why != "" {
				return why
			}
			continue
		}
		s := op.split
		k := op.ctxDepth(minD) + s.minDepth()
		if why, ok := x.readyPop(op, op.ctxDepth(minD), k, kinds); ok {
			if why != "" {
				return why
			}
			continue
		}
		switch {
		case op.planned && len(op.inner) == 0:
			// A motionless body reads its match's view only: matches below
			// it are found as the content goes by.
			s.mode = matchView
		case op.planned && (!s.nests || s.outermost && !s.predFail) && x.readyOps(op.inner, k, &op.kinds) == "":
			// Matches cannot nest, or those inside an outermost one are not
			// results and no predicate's error is to be raised there.
			s.mode = matchBody
		case op.grounded && k >= 1:
			// The body reads its match's content as it likes, on a copy
			// of one match at a time: a match that is the document element
			// is copied whole, which costs what its tree costs.
			s.mode = matchGround
			kinds[streamtrace.KindGround]++
			kinds[streamtrace.KindForEach]++
			continue
		default:
			return "loop body: neither streamed nor grounded (a match may be the document node)"
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
	if copiedPopulation(op) {
		return "" // streamgroup.go
	}
	if op.tee != nil {
		return op.tee.ready(op) // streamtee_map.go
	}
	s := op.split
	copies := isXSL(op.el, "copy-of") || isXSL(op.el, "sequence") && inElement(op.el)
	if c := s.class(); copies && (c == "nodes" || c == "subtree") &&
		s.cond == nil && s.red == "" && (isVarRef(s.outer.Expr(), splitItems) || copiedItems(s.outer.Expr())) { // streamsplit_fns.go
		switch {
		case s.group && minD+s.minDepth() < 2:
			// A group of the document element: the fallback a container's
			// select has at the document node is not open to a group's.
			return "copy-of may ground the document element"
		case s.ctxItem && (minD == 0 || minD < 2 && !x.rule):
			return "copy-of(.) of the document element"
		case s.ctxItem:
			// In a rule's body the match is copied whole, the document
			// element too, which costs what its tree does.
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

// inElement reports whether what el, an instruction, writes is the content
// of an element it is in: there a node xsl:sequence returns is copied, as
// xsl:copy-of copies it, and may be handed over a copy at a time. Anywhere
// else -- a variable's value, a template's, the top of the principal result
// -- the tree keeps the node itself.
func inElement(el *xdm.Node) bool {
	for n := el.Parent(); n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		if n.Name().URI != xdm.NSXSL {
			return true // a literal result element
		}
		switch n.Name().Local {
		case "element", "copy":
			return true
		case "if", "choose", "when", "otherwise", "for-each", "for-each-group", "iterate", "fork",
			"source-document": // g5: its result is written where it is, as an xsl:if's
		default:
			return false
		}
	}
	return false
}

// minDepth is the least depth below the context a match of P can be at.
func (s *streamSplit) minDepth() int {
	if s.hasBranches() {
		return s.branchMinDepth()
	}
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
	case c == "nodes" && s.fresh:
		// The tail's own nodes, not copies of the stream (streamshapes.go).
	case (c == "nodes" || c == "subtree") && s.group:
		// A group's members may be all of the document element's children.
		return "collects grounded copies (" + c + ") of the current group"
	case c == "nodes" && minD == 0 && !s.group:
		s.holdAt() // streamhold.go: held, capped at the tree's records
	case (c == "nodes" || c == "subtree") && minD < 2:
		return "collects grounded copies (" + c + ") of the document element"
	case (s.red == "min" || s.red == "max") && (minD > 0 || s.group) && !s.oneTake(): // streamsplit_onetake.go
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
	// root is the stand-in for the document node; top the document node of
	// the first view a dispatch met, whose properties root then takes; kept
	// says a built-in rule appended a copy of a leaf at the top of a sequence
	// (streamapply.go).
	root, top *xdm.Node
	kept      bool
	held      *outputBuilder // the container's result, when a sequence
	// grp is where the body of the streamed group running reads its group
	// (streamgroup.go).
	grp *groupAt
	// For the fallback of a select that gives up: the container, its href,
	// and how to unmark the document as read before the tree reads it (nil
	// for a principal input, which has no tree to fall back to: linkMain).
	i      *sourceDocumentInstr
	href   string
	unread func()
	// pre, for a prong of a fused fork, is what the walk read for its op
	// (streamfuse.go).
	pre *fusedValue
	// applyFeeds and kidFilters are applyFeed's and kidFilter's
	// (streamlive.go).
	applyFeeds map[*streamOp]*burstFeed
	kidFilters []*xdmstream.Filter
	// teed says a fork's tee has taken the cursor over (streamtee_try.go).
	teed bool
}

// feed returns the feed that drives instr from the cursor, or nil when instr
// runs as usual; rt is the runtime the instruction runs in.
func (run *streamRun) feed(instr Instruction, rt *runtime) *burstFeed {
	op := run.x.byInstr[instr]
	if op == nil || run.off {
		return nil
	}
	if op.apply != applyNone {
		return run.applyFeed(instr, op)
	}
	if op.group && !op.groundPop {
		return &burstFeed{instr: instr, run: run, group: op}
	}
	g := run.groupAt(op)
	if op.inGroup && g == nil {
		return &burstFeed{instr: instr, read: func(func(xdm.Item) error) error { return errStreamPlan },
			eval: func(*runtime) (xdm.Sequence, error) { return nil, errStreamPlan }}
	}
	if op.copies {
		return &burstFeed{instr: instr, read: func(each func(xdm.Item) error) error {
			if run.used {
				return errStreamPlan
			}
			run.used = true
			err := op.split.loopAt(rt.ctx, run.c, run.d, g, each, nil)
			if err == errSplitGiveUp && run.d == 0 && run.i != nil {
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
		return &burstFeed{instr: instr, eval: func(rt *runtime) (xdm.Sequence, error) { return run.evalOp(rt, op) }}
	}
	if op.pop != nil {
		return run.popFeed(instr, rt, op, g)
	}
	return &burstFeed{instr: instr, read: func(each func(xdm.Item) error) error {
		if run.used {
			return errStreamPlan
		}
		run.used = true
		d := run.d
		if op.split.mode == matchBody {
			return op.split.loopAt(rt.ctx, run.c, d, g, nil, func(v *xdm.Node, md int) (bool, error) {
				if v.Kind() != xdm.KindElement {
					// A kept leaf's copy, complete: off the cursor (streamleaf.go).
					return true, run.offEach(each, v)
				}
				run.d, run.used = md, false
				err := each(v)
				read := run.used
				run.d, run.used = d, true
				return read, err
			})
		}
		return op.split.loopAt(rt.ctx, run.c, d, g, func(it xdm.Item) error {
			off := run.off
			run.off = true
			err := each(it)
			run.off = off
			return err
		}, nil)
	}}
}

// evalOp evaluates the select of op, a consuming instruction that is not a
// loop, on the cursor.
func (run *streamRun) evalOp(rt *runtime, op *streamOp) (xdm.Sequence, error) {
	if run.pre != nil {
		return run.preEval(rt, op)
	}
	g := run.groupAt(op)
	if run.used || op.inGroup && g == nil {
		return nil, errStreamPlan
	}
	run.used = true
	var (
		seq xdm.Sequence
		err error
	)
	if op.tee != nil {
		seq, err = run.teeEval(rt, op) // streamtee_map.go
	} else {
		seq, err = op.split.evalAt(rt.ctx, run.c, run.d, g)
	}
	if err == errSplitGiveUp && run.d == 0 && run.i != nil {
		return run.fallback(rt, op.sel)
	}
	if err == nil && copiedPopulation(op) {
		seq = numberCopies(op, seq) // streamgroup.go
	}
	return seq, err
}

// fallback evaluates sel, a select at the document node whose split gave
// up, on the tree: the document is read again as a tree, and the stream is
// left where it is (nothing else reads it: its one consumer was sel).
func (run *streamRun) fallback(rt *runtime, sel *xpath.Compiled) (xdm.Sequence, error) {
	if traceBurstFallback != nil {
		traceBurstFallback(errSplitGiveUp)
	}
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
	if noBursts || streamtrace.Off() || validSourceDocumentURI(href) != nil || strings.ContainsRune(href, '#') {
		return false, nil
	}
	// The resolvers Transform wraps around the caller's are looked through
	// here, doing for this read what each does for a tree: readDocResolver
	// records it for XTDE1500 and answers from its own memory when the
	// transformation has read the document already, and stripSpaceResolver
	// strips whitespace, which the cursor does as it parses.
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
	vin, ok := i.validation.streamedValidation(rt, br, href, base, true) // streamvalidin.go
	if !ok {
		return false, nil
	}
	docURI, ok := br.wantsBursts(href, base)
	if !ok && (vin == nil || !vin.marked) {
		return false, nil
	}
	// Tree mode records the document only once it has loaded; a document
	// that fails to parse must not count as read here either (unread).
	unread := func() {}
	if rd != nil && docURI != "" {
		if !hasDoc(rd.read, docURI) {
			unread = func() { delDoc(rd.read, docURI) }
		}
		addDoc(rd.read, docURI)
		if hasDoc(rd.written, docURI) {
			return true, fmt.Errorf(
				"XTDE1500: this transformation reads %q, which xsl:result-document "+
					"has already written", docURI)
		}
	}
	if traceBursts != nil {
		traceBursts(href)
	}
	i.stream.count()
	sig, mark := signature(out), rt.localsMark()
	undoEffects := rt.effectsMark() // streammessage.go: secondary results and messages
	defer rt.holdSecondary()()      // secondaryout.go
	n0, b0 := xpathleaf.Charged(rt.ctx)
	// At the top of a sequence (the principal result's raw nodes, a
	// variable's, a function's) the tree keeps the source's own nodes where
	// they are appended as they are. The stream has copies of them, whose
	// parents, roots and identities are not the tree's: so the body writes
	// into a builder of its own first, and when a node of the stream is at
	// its top -- a view, a grounded copy, a text node a built-in rule copied --
	// the container runs on the tree instead, nothing having reached out.
	target, held := out, (*outputBuilder)(nil)
	if out.Open() == nil {
		held = newOutputBuilder(rt)
		target = held
	}
	kept := false
	err := i.runStream(rt, target, br, href, base, strip, unread, held, &kept, vin)
	if err == nil && held != nil {
		err = held.Refused()
	}
	switch {
	case err == nil && held == nil:
		return true, nil
	case err == nil && !kept && !keepsStreamNode(held.Sequence()):
		return true, appendSequence(held.Sequence(), out)
	}
	if err == nil {
		// Not a failure: the tree has the nodes this result must hold.
		streamtrace.Fallback()
		err = errKeptStreamNode
	} else {
		// The re-run. What the failed run charged is dropped with it.
		streamtrace.Rerun()
	}
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
	undoEffects()
	terr := i.executeTree(rt, out, href)
	if terr == nil && !clean {
		streamtrace.Guard()
		return true, fmt.Errorf("streaming: internal error: the streamed run failed where the tree did not: %w", err)
	}
	return true, terr
}

// errKeptStreamNode sends a container to the tree when its result, a
// sequence, would hold a node of the stream at its top.
var errKeptStreamNode = errors.New("streaming: a node of the stream at the top of the result")

// keepsStreamNode reports whether seq holds a node copied out of a stream.
func keepsStreamNode(seq xdm.Sequence) bool {
	for _, it := range seq {
		if n, ok := it.(*xdm.Node); ok && xdmburst.FromStream(n) {
			return true
		}
	}
	return false
}

// runStream runs i's body on a cursor over href. A posture fault is
// returned as its error; any other panic goes on. kept is set when a
// built-in rule appended a node of the stream at the top of held, the
// container's own result when that is a sequence (streamapply.go).
func (i *sourceDocumentInstr) runStream(rt *runtime, out *outputBuilder, br burstResolver,
	href, base string, strip func(*xdm.Node) bool, unread func(), held *outputBuilder, kept *bool,
	vin *streamValidation) (err error) {

	defer func() {
		if p := recover(); p != nil {
			pe, ok := p.(*streamtrace.PostureError)
			if !ok {
				panic(p)
			}
			err = pe
		}
	}()
	stream := br.streamDoc
	if vin != nil && !vin.asRead {
		stream = func(ctx *xpath.Context, uri, base string, fn func(*xdmstream.Cursor) error) error {
			return br.(*FileResolver).streamDocAs(ctx, uri, base, false, fn) // the copy's: no document URI
		}
	}
	return stream(rt.ctx, href, base, func(c *xdmstream.Cursor) error {
		if strip != nil {
			c.StripWhitespace(strip)
		}
		valid := func() error { return nil }
		if vin != nil {
			valid = vin.attach(c, strip) // streamvalidin.go
		}
		c.Handoff(func() func() { return xpathleaf.LockYield(rt.evalLock) }) // streamfork.go
		run := &streamRun{x: i.stream, c: c, i: i, href: href, unread: unread, root: streamStandIn(br, href, base, vin), held: held}
		defer func() { *kept = run.kept }()
		acc := run.observeAccums(rt, i.accums, true)
		defer acc.stop()
		// The body's focus is the document node, which the stream does not
		// build: the plan reads of it only what an empty one has.
		sub := rt.withCurrent(run.root, 1, 1).clearCurrentRule()
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
			if err := run.drain(); err != nil {
				return err
			}
		}
		// The document has been read to its end (a consuming op reads its
		// context to its end; run.drain reads what nothing did): whether it
		// is valid is known.
		if err := valid(); err != nil {
			return err
		}
		return acc.finish()
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

// streamDoc implements burstResolver. It reads the file as parseUncached
// does -- the same confinement, options, entity allowance and byte limit,
// and the same errors -- on a cursor fn reads, and caches nothing: there is
// no tree.
func (r *FileResolver) streamDoc(ctx *xpath.Context, uri, base string, fn func(*xdmstream.Cursor) error) error {
	return r.streamDocAs(ctx, uri, base, true, fn)
}

// streamDocAs is streamDoc, the document given no document URI unless
// docURI: what a validation's copy of it has (streamvalidin.go).
func (r *FileResolver) streamDocAs(ctx *xpath.Context, uri, base string, docURI bool, fn func(*xdmstream.Cursor) error) error {
	path, err := r.resolvePath(uri, base)
	if err != nil {
		return err
	}
	opts := r.parseOptions(path, false).WithEntityBudget(ctx.EntityBudget())
	if !docURI {
		opts.DocumentURI = ""
	}
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
