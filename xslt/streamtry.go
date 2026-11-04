package xslt

// Streamed xsl:try (full streaming, component C11; DESIGN section 1.7,
// XSLT 3.0 §8.3 and §19.8.4.39).
//
// The try and its xsl:catch clauses are a choice group: either the try's
// body (or select) or a catch may consume, not both. The core takes a try
// whose catches are all motionless: the body is planned as any sequence
// constructor is, its one consumer read off the cursor, and the try runs its
// own Execute (try.go), which already writes the body into a buffer of its
// own (tryBuffer) and drops it on an error. What the stream adds is the
// cursor: a caught error may leave it anywhere inside the context, so
// before the catch runs (on the view) the rest of the context is skipped,
// still parsed and checked, as the tree has parsed it before the body ran.
//
// What the tree does that the stream must do the same:
//
//   - A parse, limit or read error is the document's, which the tree raises
//     before the body runs and no xsl:try inside the body can catch. The
//     cursor's errors carry no error code, so catchable refuses them as it
//     does the engine's own; an error the skip meets is the try's.
//   - Which error the catch sees (§2.14: the tree's first, in its order).
//     The stream evaluates a consumer's parts in another order than the tree:
//     a split walks P once, applying each match's predicates, tail and fold
//     as it goes, where the tree evaluates each step's predicates over all
//     candidates, then the tail over all matches, then the fold, and the
//     outer's other operands before or beside P. A try is taken only when at
//     most one of those parts can fail (exactInTry); then the first error is
//     the same error, at the same match. A loop is exact when its select
//     cannot fail (the tree evaluates it whole before any body) and its
//     body's consumers are. apply-templates and grouping are refused.
//   - GroundLater's copies arrive late and an error in what they feed stops
//     the parse, which no skip can resume: the consumers of a try ground
//     each match at once instead (Ground).
//
// A consuming catch (the design grounds the context first) is refused: no
// W3C case has one (every xsl:catch of the streamable stylesheets writes a
// literal, the err: variables, or nothing), so it stays on the tree.
// rollback-output="no" is refused too: a streamed xsl:copy-of writes each
// copy as it is read, where the tree's has written nothing when its select
// fails, so XTDE3530 would not be raised alike.

import (
	"slices"
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// traceTry, when set, is told of each streamed try that caught an error.
var traceTry func()

// tryBuffer is in streamtry_out.go.

// tryOp plans el, an xsl:try that consumes its context, focus f.
func (b *streamBuilder) tryOp(el *xdm.Node, f vfocus) bool {
	body, ok := b.tryParts(el, f)
	if !ok {
		return false
	}
	n := len(*b.ops)
	if el.Attr("", "select") != nil {
		op := &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key}
		b.x.ops[el] = op
		*b.ops = append(*b.ops, op)
		b.kinds[streamtrace.KindSelect]++
	} else if !b.members(el, body, f) {
		return false
	}
	b.kinds[streamtrace.KindChoose]++
	b.x.tries = append(b.x.tries, (*b.ops)[n:])
	return true
}

// tryMotionless checks el, an xsl:try the analysis found motionless: an
// sTree, the tree's code on the view.
func (b *streamBuilder) tryMotionless(el *xdm.Node, f vfocus) bool {
	body, ok := b.tryParts(el, f)
	if !ok {
		return false
	}
	if sel := el.Attr("", "select"); sel != nil && !b.expr(el, sel.Value(), f, false) {
		return false
	}
	for _, ch := range body {
		if !b.motionless(ch, f) {
			return false
		}
	}
	return true
}

// tryParts checks the attributes and catches of el, an xsl:try at focus f,
// and returns the elements of its body.
func (b *streamBuilder) tryParts(el *xdm.Node, f vfocus) ([]*xdm.Node, bool) {
	if b.grp {
		// A caught error inside a group's body would leave the group's
		// members half read.
		return nil, b.refuse("xsl:try in the body of a streamed grouping")
	}
	if !b.tryAttrs(el, "rollback-output") {
		return nil, false
	}
	if v := strings.TrimSpace(el.AttrValue("rollback-output")); v != "" && v != "yes" && v != "true" && v != "1" {
		return nil, b.refuse("xsl:try rollback-output=" + v)
	}
	if tryReadsFallible(el) { // streamtry_l4.go
		return nil, b.refuse("xsl:try reading an accumulator")
	}
	var body []*xdm.Node
	for ch := range el.Children() {
		switch {
		case ch.Kind() == xdm.KindText:
			if strings.ContainsAny(ch.Value(), "{}") {
				return nil, b.refuse("text with a brace")
			}
		case ch.Kind() != xdm.KindElement, isXSL(ch, "fallback"):
		case isXSL(ch, "catch"):
			if !b.catchOK(ch, f) {
				return nil, false
			}
		default:
			body = append(body, ch)
		}
	}
	return body, true
}

// readsAccumulator says whether el or what it holds names
// accumulator-before or -after. The tree raises an accumulator's error at its
// first read, inside the try; the stream knows it only when it reaches the
// failing node, and may raise it after the try has ended (streamaccum.go).
func readsAccumulator(el *xdm.Node) bool {
	for n := range el.Descendants() {
		for a := range n.Attrs() {
			if strings.Contains(a.Value(), "accumulator-") {
				return true
			}
		}
		if n.Kind() == xdm.KindText && strings.Contains(n.Value(), "accumulator-") {
			return true
		}
	}
	for a := range el.Attrs() {
		if strings.Contains(a.Value(), "accumulator-") {
			return true
		}
	}
	return false
}

// catchOK checks an xsl:catch: motionless at focus f (a view, or the
// document node's stand-in), its select a value. A consuming one fails it.
func (b *streamBuilder) catchOK(el *xdm.Node, f vfocus) bool {
	if !b.tryAttrs(el, "errors") {
		return false
	}
	// Checked strictly, every select by the view: the analysis's records
	// would let a consuming one through as some split's, and a catch has
	// none.
	sb := b.strict()
	if sel := el.Attr("", "select"); sel != nil && !sb.expr(el, sel.Value(), f, false) || !sb.content(el, f) {
		return b.refuse(sb.why)
	}
	return true
}

// tryAttrs checks the attributes of el beside its select and own: those
// that change nothing a view or the split would do.
func (b *streamBuilder) tryAttrs(el *xdm.Node, own string) bool {
	for a := range el.Attrs() {
		if n := a.Name(); n.URI == "" && n.Local != own && !streamAttrsOK[n.Local] {
			return b.refuse("attribute " + n.Local + " of " + elName(el))
		}
	}
	return true
}

// readyTries finishes the plan's tries once their consumers are ready: each
// exact in the tree's error order, and grounding at once.
func (x *streamExec) readyTries() string {
	for _, ops := range x.tries {
		for _, op := range ops {
			if why := op.exactInTry(); why != "" {
				return "xsl:try: " + why
			}
			op.groundNow()
		}
	}
	return ""
}

// exactInTry says why op, a consumer in the body of a streamed xsl:try,
// could raise another error than the tree's first ("" when it cannot).
func (op *streamOp) exactInTry() string {
	switch {
	case op.apply != applyNone:
		if op.applyCannotFail() { // streamtry_l4.go
			return ""
		}
		return "xsl:apply-templates in its body"
	case op.group:
		return "xsl:for-each-group in its body"
	case op.fork:
		return elName(op.el) + " with several consuming prongs in its body"
	case op.split == nil:
		return "a consumer with no split in its body"
	case op.loop:
		if op.split.failing() > 0 {
			return "a loop whose select may fail in its body"
		}
		if op.split.mode == matchBody {
			for _, in := range op.inner {
				if why := in.exactInTry(); why != "" {
					return why
				}
			}
		}
		return ""
	}
	if op.split.failing() > 1 {
		return "a select whose parts may fail in another order than the tree's: " + op.sel.Source()
	}
	return ""
}

// failing counts the parts of s whose errors the stream meets in another
// order than the tree: each predicate of P that may fail, the tail, the
// fold, and the operands of the outer other than the path to P's value. A
// choice's condition comes first in both.
func (s *streamSplit) failing() int {
	if s.cond != nil {
		return max(s.then.failing(), s.els.failing())
	}
	n := 0
	for _, st := range s.steps {
		checked := false // "." converted to a number by an earlier predicate
		for _, p := range st.preds {
			if p.e != nil && !tryCannotFail(p.e.Expr()) && !(checked && numberCompare(p.e.Expr())) {
				n++
			}
			checked = checked || p.e != nil && numericCheck(p.e.Expr()) // streamtry_fold.go
		}
	}
	if s.tail != nil && !tryCannotFail(s.tail.Expr()) {
		n++
	}
	if splitFolds[s.red] && !s.foldCannotFail() { // streamtry_fold.go
		n++
	}
	if s.outer != nil && besideFails(s.outer, s.outer.Expr(), s.outerVar) {
		n++
	}
	return n
}

// besideFails reports whether an operand of e, a part of base, that is not
// on the way to $v may fail. Unknown expressions are taken to.
func besideFails(base *xpath.Compiled, e xpath.Expr, v xdm.QName) bool {
	var kids []xpath.Expr
	switch x := e.(type) {
	case *xpath.VarRef:
		return false
	case *xpath.FuncCall:
		kids = x.Args
	case *xpath.BinaryOp:
		kids = []xpath.Expr{x.Left, x.Right}
	case *xpath.UnaryOp:
		kids = []xpath.Expr{x.Operand}
	case *xpath.CastExpr:
		kids = []xpath.Expr{x.Operand}
	case *xpath.TreatExpr:
		kids = []xpath.Expr{x.Operand}
	case *xpath.InstanceOfExpr:
		kids = []xpath.Expr{x.Operand}
	case *xpath.SequenceExpr:
		kids = x.Items
	case *xpath.FilterExpr:
		kids = append([]xpath.Expr{x.Base}, x.Predicates...)
	case *xpath.SimpleMap:
		// The left is evaluated whole, then the right per item (xpath
		// SimpleMap.Eval): after every error of the path's items, in the
		// tree as in the stream, which evaluates the outer at the end.
		if refersTo(base, x.Left, v) {
			return besideFails(base, x.Left, v)
		}
		return !tryCannotFail(e)
	default:
		return !tryCannotFail(e)
	}
	for _, k := range kids {
		if refersTo(base, k, v) {
			if besideFails(base, k, v) {
				return true
			}
		} else if !tryCannotFail(k) {
			return true
		}
	}
	return false
}

// refersTo reports whether e, a part of base, reads the variable v.
func refersTo(base *xpath.Compiled, e xpath.Expr, v xdm.QName) bool {
	return slices.Contains(xpathleaf.WithExpr(base, e).(*xpath.Compiled).FreeVariables(), v)
}

// tryCannotFail is splitCannotFail, also admitting a comparison of
// position() or last() with an integer, and paths whose steps' predicates
// cannot fail.
func tryCannotFail(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Step:
		if x.Axis == xpath.AxisNamespace {
			return false
		}
		for _, p := range x.Predicates {
			if !tryCannotFail(p) {
				return false
			}
		}
		return true
	case *xpath.PathExpr:
		for _, st := range x.Steps {
			if !isStep(st) && !isContextItem(st) || !tryCannotFail(st) {
				return false
			}
		}
		return !x.Root
	case *xpath.BinaryOp:
		switch x.Op {
		case "eq", "ne", "lt", "le", "gt", "ge", "=", "!=", "<", "<=", ">", ">=":
			if isFocusCount(x.Left) && isIntLiteral(x.Right) || isIntLiteral(x.Left) && isFocusCount(x.Right) {
				return true
			}
		case "and", "or":
			return tryCannotFail(x.Left) && tryCannotFail(x.Right)
		}
	case *xpath.ContextItem:
		return true
	}
	return splitCannotFail(e)
}

func isFocusCount(e xpath.Expr) bool {
	f, ok := e.(*xpath.FuncCall)
	return ok && f.Name.URI == fnNS && (f.Name.Local == "position" || f.Name.Local == "last") && len(f.Args) == 0
}

func isIntLiteral(e xpath.Expr) bool {
	l, ok := e.(*xpath.Literal)
	return ok && l.Val.Type == xdm.TypeInteger
}

// groundNow has op, and the consumers in its body, ground each match at
// once (Ground), not when the document element closes (GroundLater).
func (op *streamOp) groundNow() {
	op.split.groundNow()
	for _, in := range op.inner {
		in.groundNow()
	}
}

func (s *streamSplit) groundNow() {
	if s == nil {
		return
	}
	s.later = false
	s.then.groundNow()
	s.els.groundNow()
}

// streamTry is a streamed xsl:try as it runs: the core's run, and whether
// the context had been read before the try's body ran.
type streamTry struct {
	run  *streamRun
	used bool
}

// streamTry returns the try state of the core run rt is in; its methods
// do nothing outside one, or where nothing is read off the cursor.
func (rt *runtime) streamTry() streamTry {
	if f := rt.bursts; f != nil && f.run != nil && !f.run.off {
		return streamTry{run: f.run, used: f.run.used}
	}
	return streamTry{}
}

// caught is told the try's body failed with an error a catch takes: when
// the body read the context, the rest of it is skipped, parsed and checked,
// before the catch runs. Its error, the document's, is the try's.
func (t streamTry) caught() error {
	run := t.run
	if run == nil || t.used || !run.used {
		return nil
	}
	if traceTry != nil {
		traceTry()
	}
	ev := run.c.At()
	if run.d == 0 {
		if ev.Kind == xdmstream.EndDocument {
			return nil
		}
		return run.drain()
	}
	if ev.Kind == xdmstream.EndElement && ev.Depth == run.d {
		return nil
	}
	f := xdmstream.Filter{Accept: acceptNone, Depth: run.d}
	for {
		ev, err := run.c.NextTag(&f)
		if err != nil {
			return err
		}
		if ev.Kind == xdmstream.EndDocument || ev.Kind == xdmstream.EndElement && ev.Depth == run.d {
			return nil
		}
	}
}
