package xslt

// The streamed XPath split (full streaming, component C4; DESIGN section 1.5).
//
// A consuming expression is taken apart around its one consuming path, P,
// as burstSplit does for the burst shapes, but P runs on the stream cursor
// (internal/xdmstream) instead of a fixed burst pattern:
//
//   - P is a run of downward steps from the context -- child, descendant,
//     and descendant-or-self::node() (so //) -- by name, wildcard or kind,
//     perhaps under outermost() or innermost(). It is matched as the
//     document goes by: each open element keeps the steps it is a context
//     for (an NFA over the step list), so a match is known at its start
//     tag, in document order, once. A predicate is motionless, evaluated on
//     the candidate's view (§19.8.4: attributes, names, ancestors), or a
//     positional integer, a counter kept per context (per parent for a
//     child step, per context node for a descendant step) -- never a
//     boolean read of a number (the old burst bug).
//   - The tail is what follows P in the path, evaluated per match: on the
//     match's view when it is motionless (@v, name()), on a grounded copy
//     otherwise; string() and data() read the match's text off the cursor.
//   - The outer is the rest of the expression, with $items or, where the
//     path is the argument of a reducer, $value in its place: ordinary
//     XPath, evaluated once at the context's end with the context's view as
//     its focus. Reducers (count, exists, empty, boolean, not, sum, avg,
//     min, max, head and [n]) fold as the matches come; anything else
//     collects.
//   - A choice -- if (motionless) then A else B -- evaluates its condition
//     on the view first and runs only the chosen branch's split.
//
// It fails closed: splitStream refuses (with the reason) whatever it does
// not positively recognise, and the caller runs the expression on the tree.
// It executes nothing by itself; C5's streamed instructions call it.

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// The variables the parts of a split are bound to, in a namespace no
// stylesheet can write.
var (
	splitItems = xdm.QName{URI: "urn:x-go-xml:split", Local: "items"}
	splitAcc   = xdm.QName{URI: "urn:x-go-xml:split", Local: "acc"}
	splitValue = xdm.QName{URI: "urn:x-go-xml:split", Local: "value"}
	splitN     = xdm.QName{URI: "urn:x-go-xml:split", Local: "n"}
	// splitKids is has-children(.) of the node a predicate or the outer
	// is evaluated on: a view cannot answer it, the cursor's Peek can.
	splitKids = xdm.QName{URI: "urn:x-go-xml:split", Local: "kids"}
)

// errSplitGiveUp ends a fold that cannot be exact (min or max over values of
// several types); the caller runs the expression on the tree.
var errSplitGiveUp = errors.New("split: the fold cannot be exact")

// splitMode is what a match of P must give the outer.
type splitMode uint8

const (
	matchCount  splitMode = iota // that it matched: the outer only counts
	matchText                    // its string value, read off the cursor (Text)
	matchView                    // the tail, evaluated on its view
	matchGround                  // a copy of it, whole (Ground / GroundLater)
	// matchBody is a loop's (C5, streamexec.go): its body runs at the match's
	// start tag and may read the match's content off the cursor.
	matchBody
)

// splitStep is one downward step of P.
type splitStep struct {
	axis   xpath.Axis // child, descendant, or descendant-or-self::node()
	test   xpath.NodeTest
	preds  []splitPred
	pos    bool  // a predicate is positional
	branch uint8 // the operand of a set operator it is a step of (streamsplit_union.go)
}

// splitPred is a predicate of a step: positional (e nil, at the position),
// or motionless, evaluated on the candidate.
type splitPred struct {
	at   int64
	e    *xpath.Compiled
	kids bool // e reads $splitKids
	up   bool // e reads a leaf's ancestors: its focus is Cursor.Leaf's copy
	// pe, with e nil, is a positional test other than a literal, of the
	// position counted as for at (positionPred, streamsplit_union.go).
	pe *xpath.Compiled
}

// streamSplit is an expression split for the cursor.
type streamSplit struct {
	doc bool // the context is the document node
	// recAccums says each match is grounded with its accumulator values
	// recorded, for xsl:copy-of copy-accumulators="yes" (streamaccum_ground_l4.go).
	recAccums bool

	// cond, when set, makes this a choice: then or els runs as cond, on the
	// context's view, says. Nothing else is set.
	cond      *xpath.Compiled
	then, els *streamSplit

	// steps is P, nil when the expression consumes nothing but, with
	// ctxItem, the context itself (string(.), copy-of(.)), or nothing.
	steps   []splitStep
	ctxItem bool
	// ctxOut says the context is grounded for an outer whose values are
	// atomized (groundContextSplit, streamsplit_l6.go).
	ctxOut bool
	// group says P starts at the current group (streamgroup.go): its first
	// step's candidates are the group's members, which a memberSource hands
	// over, not the children of the context.
	group                bool
	outermost, innermost bool
	nests                bool // a match can lie inside another
	noLeaves             bool // a text, comment or PI match gives nothing
	// keepLeaves says a text, comment or PI match is kept as a node, a copy
	// that is the original to order and identity (Cursor.Leaf), and
	// leafOnly that no element is one.
	keepLeaves, leafOnly bool
	// climb says the tail climbs to ancestors (streamleaf.go): its nodes are
	// those of the match's view, taken once each across the matches;
	// climbNodes and climbEach split a tail whose last step makes values
	// into the nodes and what each gives; climbElems says the nodes kept
	// may be elements, which only a view's properties may be read of.
	// climbSort says the tail's nodes are not in document order across the
	// matches (parent::, a positional predicate): they are held, and taken
	// sorted, each once, at the context's end.
	climb, climbElems, climbSort bool
	// mapClimb says the tail is the right of P ! R, R climbing: each
	// match's nodes are its own, as a simple map gives them.
	mapClimb              bool
	climbNodes, climbEach *xpath.Compiled
	// json says each match, or the context, is written as JSON text off
	// the cursor, which is the item it gives (streamfn_json.go).
	json *jsonSplit
	// copiedOut says the instruction holding the split copies what the
	// outer gives (streamfn_copies.go).
	copiedOut bool
	// predFail and tailFail say a predicate of P, or the tail, may raise an
	// error: the tree evaluates them all, so the split may not stop early.
	predFail, tailFail bool
	later              bool // matches are grounded with GroundLater
	mode               splitMode
	// tail is evaluated per match (matchView, matchGround); tailText is
	// "string" or "data" when the tail is that function of the match.
	tail     *xpath.Compiled
	tailText string
	atomize  bool // the outer, or the fold, atomizes the items
	strVal   bool // the outer is fn:string: a typed match gives its string value
	red      string
	nth      int64
	fold     *xpath.Compiled // red sum, min, max, avg: red(($acc, $items), ...)
	avg      *xpath.Compiled // $acc div $n
	outer    *xpath.Compiled
	outerVar xdm.QName
	kids     bool // the outer (or a choice above) reads has-children(.)
	// accept and leaves are what walk reads of the cursor (accepts); insts
	// holds a context of each step, shared by the nodes that are one for
	// a step without positions to count.
	accept func(*xdm.Node) bool
	leaves bool
	insts  []splitInst
	own1   []*splitInst // &insts[j] at j
	// attr, for a matchView tail that is an attribute by name whose values
	// are all the outer takes, names it: it is read off the start tag.
	attr *xdm.QName
	// noRoot gives up (errSplitGiveUp) at a match that would ground the
	// document element: that copy is the tree (C5's streamed copy-of).
	noRoot bool
	// hold caps what the split collects at what the tree holds (streamhold.go);
	// counted hands the outer stand-ins for counted matches (streamfold.go).
	hold, counted bool
	// self and set, for P with branches (streamsplit_union.go): the
	// branches the context itself ends, and how the ended ones make a match.
	self uint64
	set  *splitSet
	// checks are evaluated before the walk: the type checks of a set
	// operator's foreign operand (foreignSets, streamsplit_union.go).
	checks []*xpath.Compiled
	// fresh says the tail makes no node of a match's copy, and skip how many
	// matches give nothing (streamshapes.go).
	fresh bool
	skip  int64
	// climbSet, for P whose branches climb each its own way
	// (streamsplit_climbset.go): their tails and the operator.
	climbSet *climbSetPlan
	// laxAtom says a typed match is atomized as xdm.Atomize does, without
	// AtomizeChecked's errors (streamcopy_l3leaf.go).
	laxAtom bool
	// keepEmpty says a typed match whose typed value is empty is kept as
	// the node, its outer simple content (streamcopy_l3text.go).
	keepEmpty bool
	// docCopy says the expression is a copy of the document, built off the
	// cursor; docText that it is the document's string value, read off it
	// (streamcopy_l3.go).
	docCopy, docText bool
}

// splitStream splits e, an expression whose context item is the document
// node (doc) or an element (§19.8: a striding focus), for the cursor; or it
// returns nil and why not. e is taken apart: the caller parses it afresh
// for the split (reparse), as noteBurstSelect does. base is the compiled
// expression e was parsed from, which gives the parts their static context.
func splitStream(e xpath.Expr, base *xpath.Compiled, doc bool) (*streamSplit, string) {
	return splitStreamIn(e, base, doc, false, false)
}

// splitStreamIn is splitStream in the body of a streamed xsl:for-each-group
// (streamgroup.go): with group, current-group() is a path whose first step
// is the group's members; with key, current-grouping-key() is a value
// known at the focus, as a motionless operand.
func splitStreamIn(e xpath.Expr, base *xpath.Compiled, doc, group, key bool) (*streamSplit, string) {
	return splitStreamAt(e, base, doc, group, key, false)
}

// splitStreamAt is splitStreamIn; with views, a path whose items are the
// views of ancestors it climbed to is returned as it is, for a loop whose
// body reads only those views (streampop_climb.go).
func splitStreamAt(e xpath.Expr, base *xpath.Compiled, doc, group, key, views bool) (*streamSplit, string) {
	part := func(x xpath.Expr) *xpath.Compiled { return xpathleaf.WithExpr(base, x).(*xpath.Compiled) }
	focus := vElem
	if doc {
		focus = vNone
	}
	e = hoistChoice(e, focus, key)
	e = hoistMapChoice(e, focus, key) // streamsplit_k2.go
	if x, ok := e.(*xpath.IfExpr); ok {
		vc := viewCheck{kids: true, key: key}
		if vc.safe(x.Cond, focus, true) && !(vc.safe(x.Then, focus, false) && vc.safe(x.Else, focus, false)) {
			t, why := splitStreamAt(x.Then, base, doc, group, key, views)
			if t == nil {
				return nil, why
			}
			f, why := splitStreamAt(x.Else, base, doc, group, key, views)
			if f == nil {
				return nil, why
			}
			s := &streamSplit{doc: doc, cond: part(x.Cond), then: t, els: f}
			s.kids = readsKids(s.cond) || t.kids || f.kids
			return s, ""
		}
	}
	w := &splitter{focus: focus, doc: doc, group: group, key: key}
	root := e
	if !w.slot(e, func(y xpath.Expr) { root = y }, useKeep) {
		return nil, w.why
	}
	s := &streamSplit{doc: doc, outerVar: splitItems}
	if w.pe != nil {
		if why := s.path(w, part); why != "" {
			return nil, why
		}
		if w.json != nil {
			if why := s.jsonAfter(w); why != "" { // streamfn_json.go
				return nil, why
			}
		}
	}
	if s.red != "" && w.putRed != nil {
		w.putRed(&xpath.VarRef{Name: splitValue})
		s.outerVar = splitValue
	} else if w.pe != nil {
		w.put(&xpath.VarRef{Name: splitItems})
	}
	s.outer = part(root)
	if group {
		// The outer runs on the tree's code, where a streamed group's
		// current-group() is the posture guard's mark (streamgroup.go):
		// only the path may read the group (sx-MapExpr-101, si-group-040).
		cg := false
		walkExprCalls(root, func(x *xpath.FuncCall) { cg = cg || isCurrentGroup(x) })
		if cg {
			return nil, "current-group() outside the consuming path: " + root.String()
		}
	}
	if s.climbElems && (s.red == "" || s.red == "nth") && !(views && isVarRef(root, splitItems)) && !(viewCheck{items: true}).safe(root, focus, false) {
		return nil, "ancestors climbed to read beyond their views: " + root.String()
	}
	s.kids = readsKids(s.outer)
	if s.kids && s.ctxItem {
		return nil, "has-children(.) beside the context's own content"
	}
	if s.kids && s.group {
		return nil, "has-children(.) beside the current group"
	}
	return s, ""
}

// path builds P, its tail and the mode from the consuming path w found.
func (s *streamSplit) path(w *splitter, part func(xpath.Expr) *xpath.Compiled) string {
	p := w.path
	s.red, s.nth = w.red, w.nth
	if p.ctxItem {
		// The context consumed whole: string(.), data(.), copy-of(.).
		s.ctxItem, s.atomize = true, w.use == useAtomize || w.use == useString || splitFolds[s.red]
		s.strVal = w.use == useString
		if s.red != "" && !splitFolds[s.red] {
			s.red = "" // count(.), exists(.) are motionless; head(.) keeps it
		}
		s.folds(w, part)
		return ""
	}
	s.outermost, s.innermost = p.oi == "outermost", p.oi == "innermost"
	if p.br != nil {
		if why := s.branched(p); why != "" {
			return why
		}
	}
	s.skip = p.skip
	if s.group = p.group; s.group && p.oi != "" {
		return p.oi + "() of the current group"
	}
	// A step whose predicate needs more than the candidate's view
	// (ITEM[position() lt 4], rec[item]) ends P: it and the rest become a
	// tail, evaluated on each match's grounded copy, which holds all of the
	// match's content (burstPattern's cut). The dos step of a // goes with it.
	cut := len(p.steps)
	for i, st := range p.steps {
		if p.br != nil {
			break // branched checked them all
		}
		st.Predicates = firstAnd(st.Predicates) // streamlast_mm2.go
		if !stepPredsOK(st) {
			cut = i
			break
		}
	}
	if cut > 0 && p.steps[cut-1].Axis == xpath.AxisDescendantOrSelf {
		cut--
	}
	if cut > 0 && cut < len(p.steps) {
		if p.oi != "" {
			return p.oi + "() of a path with a consuming predicate"
		}
		rest := make([]xpath.Expr, 0, len(p.steps)-cut+len(p.tail))
		for _, st := range p.steps[cut:] {
			rest = append(rest, st)
		}
		p.tail, p.steps = append(rest, p.tail...), p.steps[:cut]
	}
	positional := false
	for _, st := range p.steps {
		ss := splitStep{axis: st.Axis, test: st.Test}
		if p.br != nil {
			ss.branch = p.br.bits[len(s.steps)]
		}
		if st.Axis != xpath.AxisChild {
			s.nests = true
		}
		leaf, elem := testKinds(st.Test)
		for _, pr := range st.Predicates {
			if n, ok := positionAt(pr); ok {
				ss.preds = append(ss.preds, splitPred{at: n})
				ss.pos, positional = true, true
				continue
			}
			if positionPred(pr) {
				ss.preds = append(ss.preds, splitPred{pe: part(pr)})
				ss.pos, positional = true, true
				continue
			}
			vc := viewCheck{kids: true, key: w.key}
			// safe first: it rewrites has-children(.) to boolean($kids).
			// A leaf's open ancestors are read as views (up), on its copy
			// under theirs (Cursor.Leaf), as a pattern's are.
			ok := !elem || vc.safe(pr, vElem, true)
			up := false
			if ok && leaf && !vc.safe(pr, vLeaf, true) {
				lc := vc
				lc.up = true
				ok, up = lc.safe(pr, vLeaf, true), true
			}
			if !ok || !booleanShaped(pr) {
				return "predicate " + pr.String() + ": not motionless or positional"
			}
			c := part(pr)
			ss.preds = append(ss.preds, splitPred{e: c, kids: readsKids(c), up: up})
			if !splitCannotFail(pr) {
				s.predFail = true
			}
			if readsKids(c) {
				// It peeks: the candidate is then descended into, and a
				// later step's match may be the very element.
				w.kids = true
			}
		}
		s.steps = append(s.steps, ss)
	}
	if s.outermost {
		// The content of an outermost match is not read for matches, so a
		// position counted across it, on a descendant step, would be wrong.
		for _, st := range s.steps {
			if st.pos && st.axis != xpath.AxisChild {
				return "outermost() over a positional descendant step"
			}
		}
	}
	if climbBranched(p) {
		return s.climbSetPath(w, p, part) // streamsplit_climbset.go
	}
	// A group's members are grounded one at a time: the cursor is left at a
	// member's end, where the next member is looked for, not at the
	// context's, where Flush would hand GroundLater's copies over.
	s.later = (!s.nests || !positional) && !s.group
	last := p.steps[len(p.steps)-1]
	leaf, elem := testKinds(last.Test)
	if p.br != nil {
		leaf, elem = branchKinds(p) // every branch's last step (streamsplit_union.go)
	}
	if last.Axis == xpath.AxisDescendantOrSelf {
		// The context itself is a result.
		if len(p.tail) == 0 {
			return "the context itself is a result"
		}
		if first, ok := p.tail[0].(*xpath.Step); s.doc && (!ok || first.Axis != xpath.AxisAttribute) {
			return "the document node itself is a result"
		}
	}
	if len(p.tail) > 0 {
		if first, ok := p.tail[0].(*xpath.Step); ok &&
			(first.Axis == xpath.AxisAttribute || first.Axis == xpath.AxisNamespace) {
			// A text, comment or PI match has no attributes: it gives nothing.
			s.noLeaves, leaf = true, false
		}
	}
	var tail xpath.Expr
	switch len(p.tail) {
	case 0:
	case 1:
		tail = p.tail[0]
	default:
		tail = &xpath.PathExpr{Steps: p.tail}
	}
	if p.mapR != nil {
		return s.mapPath(w, mapTail(tail, p.mapR), p.mapR, leaf, elem, last.Axis, part)
	}
	nodes := tail == nil || endsInAttribute(tail)
	if !nodes && (s.red == "boolean" || s.red == "not") {
		s.red = "" // the EBV of atomic values is not their existence
		w.putRed = nil
	}
	countOnly := tail == nil && splitCounts[s.red]
	s.atomize = w.use == useAtomize || w.use == useString || splitFolds[s.red]
	s.strVal = w.use == useString
	if tf := textTail(tail); tf != "" {
		s.tailText, tail = tf, nil
	}
	switch {
	case tail == nil && countOnly:
		s.mode = matchCount
	case tail == nil && (s.atomize || s.tailText != ""):
		s.mode = matchText
		if elem && s.nests {
			s.mode = matchGround
		}
	case tail == nil:
		s.mode = matchGround
		s.keepLeaves, s.leafOnly = leaf, leaf && !elem
	case leaf:
		// Only leaves match: the tail runs on each one's copy, whole.
		if elem || !(viewCheck{}).safe(tail, vLeaf, splitCounts[s.red]) {
			return "a tail after text, comment or PI matches"
		}
		s.mode, s.keepLeaves, s.leafOnly = matchView, true, true
	case !tailKindOK(tail):
		return "tail " + tail.String() + ": may give both nodes and values (XPTY0018)"
	case w.mapped:
		if !(viewCheck{}).safe(tail, vElem, true) {
			return "simple map " + tail.String() + ": not motionless on the match's view"
		}
		s.mode, s.mapClimb = matchView, true
		s.climbElems = !endsInLeafNodes(tail) && !valueTail(tail)
	case climbs(tail):
		// Matches share ancestors: the tree makes each once, then what
		// follows of it (//P/ancestor::*/data(@a) too); climbing takes
		// each once, in document order, off the matches' views.
		if why := s.climbing(tail, part); why != "" {
			return "tail " + tail.String() + ": " + why
		}
		s.mode = matchView
	case (viewCheck{}).safe(tail, vElem, splitCounts[s.red]):
		s.mode = matchView
	case (pathTail(p.tail) || burstSafe(tail, true)) && (!s.nests || s.outermost || tailValue(tail) || ownAttributes(p.tail)):
		// Nested matches' copies overlap: a tail reaching below the match
		// would give a node once per match holding it, the tree once. The
		// matches of outermost(P) do not nest (streamsplit_l6.go: sf-outermost-102).
		s.mode = matchGround
	default:
		return "tail " + tail.String() + ": not within the match"
	}
	if last.Axis == xpath.AxisDescendantOrSelf && s.mode != matchView {
		return "the context itself is a result"
	}
	if w.kids && (s.mode == matchText || s.mode == matchGround) {
		return "has-children(.) on a match that is read whole"
	}
	if tail != nil {
		s.tail = part(tail)
		s.tailFail = !splitCannotFail(tail)
		if st, ok := tail.(*xpath.Step); ok && s.mode == matchView && s.atomize &&
			st.Axis == xpath.AxisAttribute && len(st.Predicates) == 0 {
			if nt, ok := st.Test.(*xpath.NameTest); ok && !nt.AnyURI && !nt.AnyLocal {
				s.attr = &nt.Name
			}
		}
	}
	if leaf && s.mode == matchGround {
		// A leaf match is taken at once: so must the elements be, to keep
		// document order.
		s.later = false
	}
	if p.br != nil {
		if why := s.branchChecks(); why != "" {
			return why
		}
	}
	s.accepts()
	s.folds(w, part)
	return ""
}

// folds compiles the fold of a sum, avg, min or max reducer.
func (s *streamSplit) folds(w *splitter, part func(xpath.Expr) *xpath.Compiled) {
	if splitFolds[s.red] {
		c := w.redCall
		name := c.Name
		if s.red == "avg" {
			name.Local = "sum"
		}
		args := append([]xpath.Expr{&xpath.SequenceExpr{Items: []xpath.Expr{
			&xpath.VarRef{Name: splitAcc}, &xpath.VarRef{Name: splitItems}}}}, c.Args[1:]...)
		s.fold = part(&xpath.FuncCall{Name: name, Args: args})
		if s.red == "avg" {
			s.avg = part(&xpath.BinaryOp{Op: "div", Left: &xpath.VarRef{Name: splitAcc}, Right: &xpath.VarRef{Name: splitN}})
		}
	}
}

// class names what holding the split's result costs, for the plan's memory
// rule (DESIGN section 2.1, rule 3): "bounded" (a fold), "output" (atomic values
// collected), "nodes" (grounded copies collected: more than the tree holds
// for them, so a plan must not collect them), "subtree" (the context
// grounded whole).
func (s *streamSplit) class() string {
	switch {
	case s.docCopy:
		return "output" // the copy built is the result's own (streamcopy_l3.go)
	case s.cond != nil:
		a, b := s.then.class(), s.els.class()
		if splitClassRank[a] > splitClassRank[b] {
			return a
		}
		return b
	case s.json != nil:
		return "output" // the JSON text (streamfn_json.go)
	case s.ctxOut:
		return "output" // streamsplit_l6.go
	case s.ctxItem && !s.atomize:
		return "subtree"
	case s.red != "":
		return "bounded"
	case s.steps == nil || s.atomize || s.tailText != "" || s.leafOnly || s.climb || s.mapClimb ||
		(s.mode == matchView || s.mode == matchGround) && s.tail != nil && valueTail(s.tail.Expr()):
		// A grounded match's copy is let go once its tail has made values;
		// a kept leaf is a copy of it alone, its ancestors' copies shared;
		// climbed nodes are the result's own, each once.
		return "output"
	}
	return "nodes"
}

var splitClassRank = map[string]int{"bounded": 0, "output": 1, "nodes": 2, "subtree": 3}

// valueTail reports, conservatively, whether a tail makes only atomic
// values: its last step does, perhaps filtered.
func valueTail(e xpath.Expr) bool {
	if seqOfValues(e) { // streamsplit_k2.go
		return true
	}
	if pe, ok := e.(*xpath.PathExpr); ok && len(pe.Steps) > 0 {
		e = pe.Steps[len(pe.Steps)-1]
	}
	for {
		f, ok := e.(*xpath.FilterExpr)
		if !ok {
			break
		}
		e = f.Base
	}
	if f, ok := e.(*xpath.FuncCall); ok && f.Name.URI == fnNS && splitAtomicFns[f.Name.Local] {
		return true
	}
	return atomicValued(e)
}

// splitCounts are the reducers that need only how many items there are, and
// splitFolds those that fold their values.
var (
	splitCounts = map[string]bool{"count": true, "exists": true, "empty": true, "boolean": true, "not": true}
	splitFolds  = map[string]bool{"sum": true, "avg": true, "min": true, "max": true}
	// splitReducers are the reducer calls, by most arguments.
	splitReducers = map[string]int{"count": 1, "exists": 1, "empty": 1, "boolean": 1, "not": 1,
		"sum": 2, "avg": 1, "min": 2, "max": 2, "head": 1}
)

// splitCannotFail reports, conservatively, whether evaluating e on any node
// can raise no error: existence tests, names, string comparisons of
// attributes or names with a literal.
func splitCannotFail(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Literal, *xpath.VarRef:
		return true
	case *xpath.Step:
		return x.Axis != xpath.AxisNamespace && len(x.Predicates) == 0
	case *xpath.PathExpr:
		for _, st := range x.Steps {
			if !splitCannotFail(st) || !isStep(st) {
				return false
			}
		}
		return !x.Root
	case *xpath.FuncCall:
		if x.Name.URI != fnNS {
			return false
		}
		switch x.Name.Local {
		case "not", "boolean", "exists", "empty", "name", "local-name", "namespace-uri", "node-name", "true", "false":
			for _, a := range x.Args {
				if !splitCannotFail(a) {
					return false
				}
			}
			return len(x.Args) <= 1
		}
	case *xpath.BinaryOp:
		switch x.Op {
		case "and", "or":
			return splitCannotFail(x.Left) && splitCannotFail(x.Right)
		case "=", "!=":
			l, lok := x.Left.(*xpath.Literal)
			r, rok := x.Right.(*xpath.Literal)
			switch {
			case lok && l.Val.Type == xdm.TypeString:
				return splitCannotFail(x.Right)
			case rok && r.Val.Type == xdm.TypeString:
				return splitCannotFail(x.Left)
			}
		}
	}
	return false
}

func isStep(e xpath.Expr) bool {
	_, ok := e.(*xpath.Step)
	return ok
}

// stepPredsOK reports whether every predicate of st is one P can apply at a
// candidate's start tag: a positional integer, or boolean-shaped and
// motionless on the view (has-children(.) aside, which P answers by Peek
// only where it cannot cut).
func stepPredsOK(st *xpath.Step) bool {
	leaf, elem := testKinds(st.Test)
	for _, pr := range st.Predicates {
		if _, ok := positionAt(pr); ok || positionPred(pr) {
			continue
		}
		if elem && !(viewCheck{}).safe(pr, vElem, true) || leaf && !(viewCheck{}).safe(pr, vLeaf, true) || !booleanShaped(pr) {
			return false
		}
	}
	return true
}

// tailKindOK reports whether a tail gives only nodes or only values, as a
// path's last step must (XPTY0018 otherwise, which the tree raises over the
// whole path and a match at a time cannot).
func tailKindOK(e xpath.Expr) bool {
	if pe, ok := e.(*xpath.PathExpr); ok {
		e = pe.Steps[len(pe.Steps)-1]
	}
	if f, ok := e.(*xpath.FuncCall); ok && f.Name.URI == fnNS && splitAtomicFns[f.Name.Local] {
		return true
	}
	return atomicValued(e) || tailValue(e) || nodeValued(e) || copyTail(e) || seqOfValues(e) // streamsplit_k2.go
}

// splitAtomicFns return atomic values, beside those atomicValued knows.
var splitAtomicFns = map[string]bool{"node-name": true, "nilled": true, "base-uri": true,
	"document-uri": true, "tokenize": true, "string-to-codepoints": true, "lang": true}

// ownAttributes reports whether a tail returns attributes of the match
// itself: attribute steps, perhaps after self steps.
func ownAttributes(tail []xpath.Expr) bool {
	for i, x := range tail {
		st, ok := x.(*xpath.Step)
		if !ok || st.Axis != xpath.AxisSelf && (st.Axis != xpath.AxisAttribute || i != len(tail)-1) {
			return false
		}
	}
	return len(tail) > 0
}

// climbs reports whether a path goes up from the node it starts at.
func climbs(e xpath.Expr) bool {
	steps := []xpath.Expr{e}
	if pe, ok := e.(*xpath.PathExpr); ok {
		steps = pe.Steps
	}
	for _, x := range steps {
		if st, ok := x.(*xpath.Step); ok {
			switch st.Axis {
			case xpath.AxisParent, xpath.AxisAncestor, xpath.AxisAncestorOrSelf:
				return true
			}
		}
	}
	return false
}

// nodeValued reports, conservatively, whether e can only return nodes.
func nodeValued(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Step:
		return true
	case *xpath.PathExpr:
		return len(x.Steps) > 0 && nodeValued(x.Steps[len(x.Steps)-1])
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !nodeValued(it) {
				return false
			}
		}
		return len(x.Items) > 0
	case *xpath.BinaryOp:
		return x.Op == "union" || x.Op == "intersect" || x.Op == "except"
	case *xpath.FilterExpr:
		return nodeValued(x.Base)
	}
	return false
}

// positionAt reports whether a predicate is a positional integer literal, and
// which position it asks for.
func positionAt(e xpath.Expr) (int64, bool) {
	l, ok := e.(*xpath.Literal)
	if !ok || l.Val.Type != xdm.TypeInteger {
		return 0, false
	}
	n, err := strconv.ParseInt(l.Val.String(), 10, 64)
	if err != nil {
		n = -1 // beyond any position: matches nothing
	}
	return n, true
}

// testKinds reports whether a node test of P can match a text, comment or
// processing instruction, and an element; neither for a test P refuses.
func testKinds(t xpath.NodeTest) (leaf, elem bool) {
	switch x := t.(type) {
	case *xpath.NameTest:
		return false, true
	case *xpath.KindTest:
		if x.TypeName != "" || x.SchemaDeclared || x.Content != nil {
			return false, false
		}
		switch {
		case x.Any:
			return true, true
		case x.Kind == xdm.KindElement:
			return false, true
		case x.Kind == xdm.KindText || x.Kind == xdm.KindComment || x.Kind == xdm.KindPI:
			return true, false
		}
	}
	return false, false
}

// textTail reports whether the tail is fn:string or fn:data of the match.
func textTail(e xpath.Expr) string {
	f, ok := e.(*xpath.FuncCall)
	if !ok || f.Name.URI != fnNS || f.Name.Local != "string" && f.Name.Local != "data" {
		return ""
	}
	if len(f.Args) == 0 {
		return f.Name.Local
	}
	if _, ok := f.Args[0].(*xpath.ContextItem); ok && len(f.Args) == 1 {
		return f.Name.Local
	}
	return ""
}

func readsKids(c *xpath.Compiled) bool { return slices.Contains(c.FreeVariables(), splitKids) }

// --- Finding the consuming path ---------------------------------------------

// splitUse is how an expression's items are consumed by what holds it.
type splitUse uint8

const (
	useKeep    splitUse = iota // as they are
	useAtomize                 // as values
	useCount                   // only how many (count, exists, empty)
	useEBV                     // as an effective boolean value
	useString                  // as string values: fn:string's argument, not its typed value
)

// splitPathParts is a consuming path taken apart.
type splitPathParts struct {
	steps   []*xpath.Step  // P
	tail    []xpath.Expr   // what follows P, per match
	oi      string         // "outermost" or "innermost" around P
	ctxItem bool           // the path is "." itself
	group   bool           // its first step is current-group()'s members
	br      *splitBranches // the operands of a set operator (streamsplit_union.go)
	mapR    xpath.Expr     // "P ! R": R, mapped over each match (streamshapes.go)
	skip    int64          // "tail(P)": P's first matches left out (streamshapes.go)
}

// splitter finds the one consuming path of an expression and checks that
// everything around it is evaluable once, at the end, with the context's
// view for focus: motionless, or consuming the path's items as burstSplit
// allows (functions that take them whole, comparisons, filters and maps
// that stay within each item).
type splitter struct {
	focus vfocus
	doc   bool
	// pe is the consuming path, put puts an expression in its place, and
	// use is how its holder consumes it.
	pe   xpath.Expr
	put  func(xpath.Expr)
	use  splitUse
	path splitPathParts
	// red is the reducer holding pe directly; redCall is its call, if it
	// is one, and putRed puts the reduced value in the reducer's place.
	red     string
	redCall *xpath.FuncCall
	putRed  func(xpath.Expr)
	nth     int64
	kids    bool // a predicate of P reads has-children(.)
	// group and key are splitStreamIn's; groupStep is the step that stands
	// for current-group() in the path found.
	group, key bool
	groupStep  *xpath.Step
	// mapped says the path found is P ! R, R climbing (streamleaf.go): R
	// is the tail, a match at a time, its nodes not merged across matches.
	mapped bool
	why    string
	// json says the path found is the node of fn:xml-to-json (streamfn_json.go).
	json *jsonSplit
}

// isCurrentGroup reports whether e is a call on fn:current-group().
func isCurrentGroup(e xpath.Expr) bool {
	f, ok := e.(*xpath.FuncCall)
	return ok && f.Name.URI == fnNS && f.Name.Local == "current-group" && len(f.Args) == 0
}

// newGroupStep is the step current-group() is read as, filtered by preds:
// child::element() of a context whose children are the group's members,
// each an element at its start tag (streamgroup.go hands them over).
func (w *splitter) newGroupStep(preds []xpath.Expr) *xpath.Step {
	w.groupStep = &xpath.Step{Axis: xpath.AxisChild, Test: &xpath.KindTest{Kind: xdm.KindElement}, Predicates: preds}
	return w.groupStep
}

func (w *splitter) refuse(why string) bool {
	if w.why == "" {
		w.why = why
	}
	return false
}

// slot checks an operand consumed as use: motionless, it is evaluated with
// the outer; otherwise it must hold the consuming path.
func (w *splitter) slot(e xpath.Expr, put func(xpath.Expr), use splitUse) bool {
	if !(w.group && readsGroup(e)) && (viewCheck{kids: true, key: w.key}).safe(e, w.focus, use == useCount || use == useEBV) { // streamgroup_copies_mm7.go
		return true
	}
	return w.walk(e, put, use)
}

// noPath checks an operand that may not hold the consuming path, because
// the tree might not evaluate it (the right of and/or, a branch of an if
// below the top): an error in it the stream would raise and the tree not.
func (w *splitter) noPath(e xpath.Expr, use splitUse, why string) bool {
	if (viewCheck{kids: true, key: w.key}).safe(e, w.focus, use == useCount || use == useEBV) {
		return true
	}
	return w.refuse(why)
}

func (w *splitter) walk(e xpath.Expr, put func(xpath.Expr), use splitUse) bool {
	if pp, ok := w.pathOf(e); ok {
		if w.pe != nil {
			return w.refuse("a second consuming operand")
		}
		w.pe, w.put, w.use, w.path = e, put, use, pp
		if use == useEBV && w.red == "" {
			// if (P), P and ..., not as a call: the existence of P.
			w.red, w.putRed = "boolean", put
		}
		return true
	}
	if w.why != "" {
		return false
	}
	if done, ok := w.collection(e); done { // streamshapes.go
		return ok
	}
	switch x := e.(type) {
	case *xpath.FuncCall:
		if done, ok := w.outerOfClimbed(x); done { // streamsplit_foreign.go
			return ok
		}
		switch x.Name.URI {
		case fnNS:
			if done, ok := w.jsonCall(x, put, use); done { // streamfn_json.go
				return ok
			}
			if !burstConsumers[x.Name.Local] && !splitWholeFns[x.Name.Local] {
				return w.refuse("function " + x.Name.Local + "()")
			}
		case xdm.NSXS, xdm.NSMath:
		default:
			if done, ok := w.sheetCall(x); done { // streamsplit_calls.go
				return ok
			}
			return w.refuse("function " + x.Name.Local + "()")
		}
		if len(x.Args) == 0 && !w.doc && (splitFocusAtomizers[x.Name.Local] || x.Name.Local == "copy-of" || x.Name.Local == "snapshot") && x.Name.URI == fnNS {
			// string() is string(.): the context's own content; copy-of()
			// is copy-of(.) (si-iterate-036).
			x.Args = []xpath.Expr{&xpath.ContextItem{}}
		}
		if focusDependent[keyOf(x.Name, len(x.Args))] && !splitViewFns[w.focus][x.Name.Local] {
			return w.refuse("function " + x.Name.Local + "() of the document node")
		}
		argUse := useKeep
		switch {
		case x.Name.URI != fnNS || splitWholeAtomizers[x.Name.Local] || burstAtomizers[x.Name.Local] && x.Name.Local != "count" &&
			x.Name.Local != "exists" && x.Name.Local != "empty":
			argUse = useAtomize
		case x.Name.Local == "count" || x.Name.Local == "exists" || x.Name.Local == "empty":
			argUse = useCount
		case x.Name.Local == "boolean" || x.Name.Local == "not":
			argUse = useEBV
		}
		if x.Name.URI == fnNS && x.Name.Local == "string" {
			argUse = useString // a node's string value, which a typed node's value is not
		}
		// A reducer holds the path directly: walk takes its first argument
		// as the path itself.
		direct := false
		if n, ok := splitReducers[x.Name.Local]; ok && x.Name.URI == fnNS && len(x.Args) >= 1 && len(x.Args) <= n && w.pe == nil {
			_, direct = w.pathOf(x.Args[0])
		}
		for k := range x.Args {
			if !w.slot(x.Args[k], func(y xpath.Expr) { x.Args[k] = y }, argUse) {
				return false
			}
			if k == 0 && direct && w.pe != nil {
				w.red, w.redCall, w.putRed = x.Name.Local, x, put
				if x.Name.Local == "head" {
					w.red, w.nth, w.redCall = "nth", 1, nil
				}
			}
		}
		return true
	case *xpath.BinaryOp:
		if done, ok := w.foreignUnion(x); done { // streamsplit_foreign.go
			return ok
		}
		if !burstOps[x.Op] && x.Op != "to" { // to: both atomized, as + is (lane K2)
			return w.refuse("operator " + x.Op)
		}
		if x.Op == "and" || x.Op == "or" {
			return w.slot(x.Left, func(y xpath.Expr) { x.Left = y }, useEBV) &&
				w.noPath(x.Right, useEBV, "the consuming path on the right of "+x.Op)
		}
		return w.slot(x.Left, func(y xpath.Expr) { x.Left = y }, useAtomize) &&
			w.slot(x.Right, func(y xpath.Expr) { x.Right = y }, useAtomize)
	case *xpath.UnaryOp:
		return w.slot(x.Operand, func(y xpath.Expr) { x.Operand = y }, useAtomize)
	case *xpath.StringConcat: // A || A: both absorbed (G2)
		return w.slot(x.Left, func(y xpath.Expr) { x.Left = y }, useAtomize) &&
			w.slot(x.Right, func(y xpath.Expr) { x.Right = y }, useAtomize)
	case *xpath.CastExpr:
		return w.slot(x.Operand, func(y xpath.Expr) { x.Operand = y }, useAtomize)
	case *xpath.InstanceOfExpr:
		return w.slot(x.Operand, func(y xpath.Expr) { x.Operand = y }, useKeep)
	case *xpath.TreatExpr:
		// The treat tests the items as they are: nodes, not their values.
		return w.slot(x.Operand, func(y xpath.Expr) { x.Operand = y }, useKeep)
	case *xpath.IfExpr:
		return w.slot(x.Cond, func(y xpath.Expr) { x.Cond = y }, useEBV) &&
			w.noPath(x.Then, use, "the consuming path in a branch below the top") &&
			w.noPath(x.Else, use, "the consuming path in a branch below the top")
	case *xpath.SequenceExpr:
		// The EBV of a sequence is not that of its items: boolean(($b, P))
		// is FORG0006 for a non-empty P, true for an empty one.
		u := use
		if u == useEBV {
			u = useKeep
		}
		for k := range x.Items {
			if !w.slot(x.Items[k], func(y xpath.Expr) { x.Items[k] = y }, u) {
				return false
			}
		}
		return true
	case *xpath.FilterExpr:
		if n, ok := firstPosition(x.Predicates); ok && len(x.Predicates) == 1 && w.pe == nil {
			if _, ok := w.pathOf(x.Base); ok {
				w.red, w.nth, w.putRed = "nth", n, put
				return w.walk(x.Base, func(y xpath.Expr) { x.Base = y }, use)
			}
		}
		// The predicates see each item, positioned in the whole sequence.
		last := false
		if !allBurstSafe(x.Predicates, false) {
			if last = lastPreds(x.Predicates); !last { // streamlast_mm2.go
				return w.refuse("filter " + e.String())
			}
		}
		if !w.slot(x.Base, func(y xpath.Expr) { x.Base = y }, useKeep) {
			return false
		}
		if last && (!w.path.ctxItem || w.red != "") {
			return w.refuse("filter " + e.String() + ": last() of items not all at hand")
		}
		return true
	case *xpath.SimpleMap:
		if !burstSafe(x.Right, false) && w.mapClimb(x, put, use) {
			return true
		}
		if !burstSafe(x.Right, false) {
			return w.refuse("simple map " + e.String())
		}
		return w.slot(x.Left, func(y xpath.Expr) { x.Left = y }, useKeep)
	case *xpath.QuantifiedExpr:
		if (!burstSafe(x.Test, false) || !focusFree(x.Test)) && !valueFor(x.Bindings, x.Test) { // streamsplit_k2.go
			return w.refuse("quantified test " + x.Test.String())
		}
		return w.bindings(x.Bindings)
	case *xpath.ForExpr:
		if (!burstSafe(x.Return, false) || !focusFree(x.Return)) && !valueFor(x.Bindings, x.Return) { // streamsplit_k2.go
			return w.refuse("for return " + x.Return.String())
		}
		return w.bindings(x.Bindings)
	case *xpath.ContextItem:
		return w.refuse("the context item")
	case *xpath.PathExpr, *xpath.Step:
		if done, ok := w.pathOverClimbed(e); done { // streamsplit_foreign.go
			return ok
		}
		if u := distributedPath(e); u != nil { // streamsplit_foreign.go
			put(u)
			return w.walk(u, put, use)
		}
		if u := foreignPath(e); u != nil { // streamsplit_k2.go
			put(u)
			return w.walk(u, put, use)
		}
		return w.refuse("path " + e.String())
	}
	return w.refuse("expression " + e.String())
}

func (w *splitter) bindings(bs []xpath.Binding) bool {
	for k := range bs {
		if !w.slot(bs[k].Seq, func(y xpath.Expr) { bs[k].Seq = y }, useKeep) {
			return false
		}
	}
	return true
}

// splitFocusAtomizers are the functions whose zero-argument form atomizes
// the context item.
var splitFocusAtomizers = map[string]bool{"string": true, "data": true, "number": true,
	"string-length": true, "normalize-space": true}

func firstPosition(ps []xpath.Expr) (int64, bool) {
	if len(ps) == 0 {
		return 0, false
	}
	return positionAt(ps[0])
}

// pathOf takes e apart as the consuming path, when it is one: downward steps
// from the context, perhaps under outermost() or innermost(), and a tail; or
// the context item itself, for an element. A path that fails the checks
// refuses (w.why).
func (w *splitter) pathOf(e xpath.Expr) (splitPathParts, bool) {
	var pp splitPathParts
	var list []xpath.Expr
	switch x := e.(type) {
	case *xpath.ContextItem:
		if w.doc {
			return pp, false
		}
		pp.ctxItem = true
		return pp, true
	case *xpath.Step:
		list = []xpath.Expr{x}
	case *xpath.FilterExpr:
		if !w.group || !isCurrentGroup(x.Base) {
			return pp, false
		}
		// current-group()[p]: the members p keeps, p seeing each at its
		// position in the group -- the members' step, so filtered.
		list = []xpath.Expr{w.newGroupStep(x.Predicates)}
		pp.group = true
	case *xpath.BinaryOp:
		return w.setOf(x)
	case *xpath.SimpleMap:
		// R over each match as its tail (streamshapes.go) is the map exactly;
		// a map read as a path (streamsplit_union.go) only where that declines.
		if pp, ok := w.mapPathOf(x); ok {
			return pp, true
		}
		return w.mapOf(x)
	case *xpath.FuncCall:
		if w.group && isCurrentGroup(x) {
			list = []xpath.Expr{w.newGroupStep(nil)}
			pp.group = true
			break
		}
		if t, ok := w.tailCallOf(x); ok {
			return t, true
		}
		if !outerInner(x) {
			return pp, false
		}
		inner, ok := w.pathOf(x.Args[0])
		if ok && (inner.mapR != nil || inner.skip > 0) {
			return pp, false
		}
		if !ok || inner.oi != "" || inner.ctxItem || len(inner.tail) > 0 {
			if ok && climbedOuter(inner) { // streamsplit_foreign.go
				return pp, false
			}
			if ok {
				w.refuse(x.Name.Local + "() of a path with a tail")
			}
			return pp, false
		}
		inner.oi = x.Name.Local
		return inner, true
	case *xpath.PathExpr:
		if x.Root && !w.doc {
			return pp, false // fn:root(.): free-ranging from an element
		}
		list = x.Steps
		if len(list) > 0 && !x.Root {
			switch f := list[0].(type) {
			case *xpath.ContextItem:
				list = list[1:]
			case *xpath.BinaryOp:
				return w.unionSteps(f, list[1:])
			case *xpath.FilterExpr:
				if w.group && isCurrentGroup(f.Base) {
					list = append([]xpath.Expr{w.newGroupStep(f.Predicates)}, list[1:]...)
					pp.group = true
				}
			case *xpath.FuncCall:
				if w.group && isCurrentGroup(f) {
					list = append([]xpath.Expr{w.newGroupStep(nil)}, list[1:]...)
					pp.group = true
					break
				}
				if t, ok := w.tailCallOf(f); ok {
					t.tail = list[1:]
					return t, true
				}
				if outerInner(f) {
					inner, ok := w.pathOf(f)
					if !ok {
						return pp, false
					}
					inner.tail = list[1:]
					return inner, true
				}
			}
		}
	default:
		return pp, false
	}
	k := 0
	for ; k < len(list); k++ {
		st, ok := list[k].(*xpath.Step)
		if !ok || !downward(st) {
			break
		}
		pp.steps = append(pp.steps, st)
	}
	if len(pp.steps) == 0 {
		return pp, false
	}
	pp.tail = list[k:]
	return pp, true
}

// downward reports whether st is a step P can take: child or descendant by a
// test it can match on the cursor, or descendant-or-self::node() bare.
func downward(st *xpath.Step) bool {
	leaf, elem := testKinds(st.Test)
	switch st.Axis {
	case xpath.AxisChild, xpath.AxisDescendant:
		return leaf || elem
	case xpath.AxisDescendantOrSelf:
		kt, ok := st.Test.(*xpath.KindTest)
		return ok && kt.Any && len(st.Predicates) == 0
	}
	return false
}

// --- What a view can answer -------------------------------------------------

// vfocus is the node an expression is evaluated on.
type vfocus uint8

const (
	vElem vfocus = iota // an element's view: no content
	vLeaf               // a complete node with no parent (a copied leaf, an attribute)
	vNone               // none may be read (the document node of a split)
)

// viewCheck reports whether expressions read of their focus only what a
// view has (§19.8: motionless). With kids, has-children(.) is rewritten in
// place to read $splitKids, which the cursor answers by Peek. With pos,
// position() is admitted: the focus is an instruction's, whose position is
// known (C5's motionless parts), not a candidate's (a split's predicates).
// With noIdent, nothing may ask for the identity of a node, nor reach the
// document through one (C6: a copy of a text node stands in for it). With
// key, current-grouping-key() is admitted: it is a value the focus has, in
// the body of a streamed xsl:for-each-group (streamgroup.go).
//
// For accumulators (streamaccum.go): with acc, accumulator-before of a
// named accumulator is admitted on an element's view or the document node,
// which the run's observer answers, and with after accumulator-after (the
// focus read to its end first, or read by the call); with up, a leaf has its
// parent (a live node of the parse, not a copy), so upward steps from it
// are admitted, to an element as from a view; with curOK, current() is
// admitted as a node of kind cur (an accumulator rule's matched node).
type viewCheck struct {
	kids, pos, noIdent, key bool
	// last admits fn:last() of the focus (streamBuilder.last).
	last                  bool
	acc, after, up, curOK bool
	cur                   vfocus
	// lookup admits $value(k): a lookup in an accumulator's value, a map or
	// an array by its declared type, which atomizes k.
	lookup bool
	// accum says the expression is an accumulator rule's (streamaccum.go).
	accum bool
	// leafAcc admits accumulator-before of a leaf, a pattern's last step
	// matched live on the cursor (streamaccum.go: the last leaf observed).
	leafAcc bool
	// items says the split's $items (or its nth, $value) are views of
	// elements, read as a focus of kind vElem is (streamleaf.go).
	items bool
	// globals, when set, is the element the expression is written on:
	// key() over a global variable's document is admitted (viewKeyOK,
	// streamsplit_l6.go).
	globals *xdm.Node
	// ents admits unparsed-entity-uri(x) and unparsed-entity-public-id(x)
	// of the document node's stand-in (streamcopy_l3doc.go, docEntities).
	ents bool
	// peek admits has-children(.) of an element's view, which the stream
	// answers by a Peek at its start tag (streamkids_l6.go); set only where
	// every such call is of the focus itself (topKids).
	peek bool
	// inlined says the expression is a call's inlined body (streamview_leafcall.go).
	inlined bool
}

// safe reports whether e reads of focus f only what f has. nodeOK says the
// nodes e returns are not atomized by what holds e.
func (vc viewCheck) safe(e xpath.Expr, f vfocus, nodeOK bool) bool {
	switch x := e.(type) {
	case *xpath.VarRef:
		return !vc.items || !isSplitItems(x) || nodeOK
	case *xpath.Literal:
		return true
	case *xpath.ContextItem:
		return f == vLeaf || f == vElem && nodeOK
	case *xpath.Step:
		return vc.path([]xpath.Expr{x}, f, nodeOK)
	case *xpath.PathExpr:
		if x.Root {
			return false
		}
		if v, ok := firstVar(x.Steps); ok && vc.items && isSplitItems(v) {
			return vc.path(x.Steps[1:], vElem, nodeOK)
		}
		if len(x.Steps) > 0 && focusFree(x.Steps[0]) {
			// $v/...: the rest reads the variable's nodes, not the focus.
			return burstSafe(e, true)
		}
		return vc.path(x.Steps, f, nodeOK)
	case *xpath.FilterExpr:
		if freeFilter(x) || f != vNone && callsCurrent(x) && currentFilter(vc, x, f) { // streamsplit_fns.go, streamlast_mm2.go
			return true
		}
		if atomicValued(x.Base) {
			// The predicates' focus is the base's atomic values, never a
			// view: they read of the stream only what a leaf's value would
			// (si-iterate-037: tokenize(., ' ')[matches(., 'x')]).
			for _, p := range x.Predicates {
				if !vc.safe(p, vLeaf, true) {
					return false
				}
			}
			return vc.safe(x.Base, f, nodeOK)
		}
		for _, p := range x.Predicates {
			if !vc.safe(p, vElem, true) || !vc.safe(p, vLeaf, true) {
				return false
			}
		}
		return vc.safe(x.Base, f, nodeOK)
	case *xpath.BinaryOp:
		switch x.Op {
		case "is", "<<", ">>", "union", "intersect", "except":
			if f == vLeaf && leafAttrSetOp(x) { // streamsplit_l6.go
				return true
			}
			if vc.noIdent {
				return false
			}
		}
		switch x.Op {
		case "is", "<<", ">>", "and", "or":
			return vc.safe(x.Left, f, true) && vc.safe(x.Right, f, true)
		case "union", "intersect", "except":
			return vc.safe(x.Left, f, nodeOK) && vc.safe(x.Right, f, nodeOK)
		}
		return vc.safe(x.Left, f, false) && vc.safe(x.Right, f, false)
	case *xpath.UnaryOp:
		return vc.safe(x.Operand, f, false)
	case *xpath.StringConcat:
		return vc.safe(x.Left, f, false) && vc.safe(x.Right, f, false)
	case *xpath.CastExpr:
		return vc.safe(x.Operand, f, false)
	case *xpath.InstanceOfExpr:
		return vc.safe(x.Operand, f, true) || docInstanceOK(vc, x, f) // streamtee_docnode.go
	case *xpath.TreatExpr:
		return vc.safe(x.Operand, f, nodeOK)
	case *xpath.IfExpr:
		return vc.safe(x.Cond, f, true) && vc.safe(x.Then, f, nodeOK) && vc.safe(x.Else, f, nodeOK)
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !vc.safe(it, f, nodeOK) {
				return false
			}
		}
		return true
	case *xpath.LetExpr:
		return vc.bound(x.Bindings, f) && vc.safe(x.Return, f, nodeOK)
	case *xpath.ForExpr:
		return vc.bound(x.Bindings, f) && vc.safe(x.Return, f, nodeOK)
	case *xpath.QuantifiedExpr:
		return vc.bound(x.Bindings, f) && vc.safe(x.Test, f, true)
	case *xpath.SimpleMap:
		if v, ok := x.Left.(*xpath.VarRef); ok && vc.items && isSplitItems(v) {
			return vc.safe(x.Right, vElem, nodeOK) // the climbed views alone
		}
		if vc.items && !itemsOut(x.Left) && (f == vNone || endsInLeafNodes(x.Left)) {
			// No view comes out of the left: its items are values, nodes
			// of variables, or attributes and namespaces.
			return vc.safe(x.Left, f, true) && vc.safe(x.Right, vLeaf, nodeOK)
		}
		return vc.safe(x.Left, f, true) && vc.safe(x.Right, vElem, nodeOK) && vc.safe(x.Right, vLeaf, nodeOK)
	case *xpath.MapConstructor:
		for i := range x.Keys {
			if !vc.safe(x.Keys[i], f, false) || !vc.safe(x.Values[i], f, false) {
				return false
			}
		}
		return true
	case *xpath.DynamicCall:
		// A function item a variable holds may read the focus or the stream
		// (accumulator-after#1): only a call known to be a lookup is taken.
		// A map or array variable's "$m('k')" was made map:get or array:get
		// as it parsed (parseAt, mapCalls); in an accumulator rule,
		// $value(k) where its type says it is a map or an array.
		if accumValueCallOK(vc, x, f) { // streamaccum_mm5.go
			return true
		}
		if !(vc.accum && vc.lookup && isVarRef(x.Target, xdm.QName{Local: "value"})) {
			return false
		}
		for _, a := range x.Args {
			if !vc.safe(a, f, false) {
				return false
			}
		}
		return true
	case *xpath.LookupExpr:
		return x.Base != nil && vc.safe(x.Base, f, false) && (x.Expr == nil || vc.safe(x.Expr, f, false))
	case *xpath.ArrayConstructor:
		for _, m := range x.Members {
			if !vc.safe(m, f, false) {
				return false
			}
		}
		return true
	case *xpath.FuncCall:
		return vc.call(x, f, nodeOK)
	case *xpath.NamedFunctionRef, *xpath.InlineFunctionExpr:
		return vc.funcItemSafe(e) // streamsplit_fns.go
	}
	return false
}

func (vc viewCheck) bound(bs []xpath.Binding, f vfocus) bool {
	for _, b := range bs {
		if !vc.safe(b.Seq, f, false) {
			return false
		}
	}
	return true
}

// path checks a relative path from focus f: up to ancestors (their names and
// attributes are in the view), to attributes or namespaces, or on self; never
// down. A path ending in elements returns view nodes, which may not be
// atomized (nodeOK).
func (vc viewCheck) path(steps []xpath.Expr, f vfocus, nodeOK bool) bool {
	steps = flatSteps(steps) // streamlast_mm2.go
	cur, doc := f, false
	for i, s := range steps {
		st, ok := s.(*xpath.Step)
		if ok && cur == vNone && vc.pos && docSelfStep(st) { // streamtee_docnode.go
			doc = true // the stand-in, or nothing: it stays the focus
			continue
		}
		if !ok {
			if i == 0 && vc.curOK && isCurrentCall(s) {
				cur = vc.cur
				continue
			}
			if isContextItem(s) && cur != vNone {
				continue // ./x is x: the focus stays what it is
			}
			if !vc.safe(s, cur, nodeOK) {
				return false
			}
			cur = vLeaf // its items: values, or nodes it returned
			continue
		}
		up := st.Axis == xpath.AxisParent || st.Axis == xpath.AxisAncestor || st.Axis == xpath.AxisAncestorOrSelf
		if cur == vNone || cur == vLeaf && st.Axis != xpath.AxisSelf && !(vc.up && up) {
			return false
		}
		switch st.Axis {
		case xpath.AxisAttribute, xpath.AxisNamespace:
			cur = vLeaf
		case xpath.AxisParent, xpath.AxisAncestor, xpath.AxisAncestorOrSelf, xpath.AxisSelf:
			if up {
				cur = vElem // a live leaf's ancestors (up), read as views
			}
		default:
			return false
		}
		for _, p := range st.Predicates {
			pf, pc := cur, vc
			if st.Axis == xpath.AxisParent || st.Axis == xpath.AxisAncestor || st.Axis == xpath.AxisAncestorOrSelf {
				// The step's positions are among the ancestors, all of
				// which the view has (function-5007: ancestor-or-self::*[last()]).
				pf, pc.pos, pc.last = vElem, true, true
			}
			if !pc.safe(p, pf, true) {
				return false
			}
		}
	}
	return cur == vLeaf || nodeOK && !(doc && cur == vNone)
}

// splitViewFns are the focus-dependent functions a focus can answer: an
// element's view its name, URIs and language; a complete parentless node
// also its value.
var splitViewFns = [...]map[string]bool{
	vElem: {"node-name": true, "nilled": true, "base-uri": true, "document-uri": true, "name": true,
		"local-name": true, "namespace-uri": true, "lang": true},
	vLeaf: {"node-name": true, "nilled": true, "string": true, "data": true, "number": true,
		"string-length": true, "normalize-space": true, "name": true, "local-name": true, "namespace-uri": true},
	// The document node's names are empty whatever its content: the
	// outer reads them off an empty stand-in, as burst execution does.
	vNone: {"node-name": true, "nilled": true, "name": true, "local-name": true, "namespace-uri": true},
}

// splitNodeFns read nothing of their argument's nodes but their names, URIs
// or number.
var splitNodeFns = map[string]bool{"name": true, "local-name": true, "namespace-uri": true,
	"node-name": true, "count": true, "exists": true, "empty": true, "boolean": true, "not": true,
	"base-uri": true, "document-uri": true, "nilled": true}

func (vc viewCheck) call(x *xpath.FuncCall, f vfocus, nodeOK bool) bool {
	switch x.Name.URI {
	case fnNS:
		n := x.Name.Local
		if n == "has-children" && vc.peek && f == vElem && (len(x.Args) == 0 || len(x.Args) == 1 && isContextItem(x.Args[0])) {
			return true // streamkids_l6.go
		}
		if n == "has-children" && vc.kids && f != vLeaf {
			if len(x.Args) == 0 || len(x.Args) == 1 && isContextItem(x.Args[0]) {
				// has-children(.): the cursor's Peek answers it (DESIGN section 1.4).
				x.Name.Local, x.Args = "boolean", []xpath.Expr{&xpath.VarRef{Name: splitKids}}
				return true
			}
		}
		if n == "position" && vc.pos && len(x.Args) == 0 || n == "last" && vc.last && len(x.Args) == 0 || n == "current-grouping-key" && vc.key && len(x.Args) == 0 {
			return true
		}
		if n == "current" && vc.curOK && len(x.Args) == 0 {
			return vc.cur == vLeaf || vc.cur == vElem && nodeOK
		}
		if (n == "accumulator-before" && vc.acc || n == "accumulator-after" && vc.after) && len(x.Args) == 1 {
			lit, ok := x.Args[0].(*xpath.Literal)
			return ok && lit.Val != nil && (f == vElem || f == vNone || (vc.accum || vc.leafAcc) && f == vLeaf) // a rule's leaf: streamaccum_l4.go
		}
		if viewIdentOK(vc, x, f) { // streamview_k2.go
			return true
		}
		if docURIOK(vc, x, f) { // streamtee_docnode.go
			return true
		}
		if accumDocURIOK(vc, x) { // streamaccum_l4.go
			return true
		}
		if (vc.ents && f == vNone || f == vElem) && len(x.Args) == 1 && (n == "unparsed-entity-uri" || n == "unparsed-entity-public-id") {
			// streamcopy_l3doc.go; at an element's view, its tree's DTD is
			// the document's (xdm newViews), read before the first start
			// tag (sf-unparsed-entity-02).
			return vc.safe(x.Args[0], f, false)
		}
		if viewLeafCopyOK(x, f) { // streamview_l7.go
			return true
		}
		if viewKeyOK(vc, x, f) { // streamsplit_l6.go
			return true
		}
		if (n == "copy-of" || n == "snapshot") && f == vLeaf && (len(x.Args) == 0 || len(x.Args) == 1 && isContextItem(x.Args[0])) {
			return true // a new copy of the leaf's complete copy and its ancestors' views (streamcopy_l3apply.go)
		}
		if (n == "copy-of" || n == "snapshot") && len(x.Args) == 1 && focusFree(x.Args[0]) {
			// A copy of a variable's value: nothing of the focus; but a
			// copy of climbed views would copy their content.
			return !vc.items || vc.safe(x.Args[0], f, false)
		}
		if docOfArgFns[n] && len(x.Args) == 2 && focusFree(x.Args[1]) {
			// id('x', $c), unparsed-entity-uri('e', $c): the document of
			// a variable's node, as copy-of($c) above, not the stream's.
			return vc.safe(x.Args[0], f, false) && (!vc.items || vc.safe(x.Args[1], f, false))
		}
		if burstFnBanned[n] || n == "copy-of" || n == "snapshot" || n == "current" ||
			n == "has-children" && f != vLeaf || vc.noIdent && identFns[n] {
			return false
		}
		if focusDependent[keyOf(x.Name, len(x.Args))] && !splitViewFns[f][n] {
			return false
		}
		for _, a := range x.Args {
			if !vc.safe(a, f, splitNodeFns[n] || vc.items && nodeOK && splitPassFns[n]) {
				return false
			}
		}
		return true
	case xdm.NSXS, xdm.NSMath, xdm.NSMap, xdm.NSArray:
		for _, a := range x.Args {
			if !vc.safe(a, f, false) {
				return false
			}
		}
		return true
	}
	return vc.sheetCall(x, f) // streamsplit_calls.go
}

// identFns read a node's identity, or reach the document or other nodes
// through it, whatever focus they are given.
var identFns = map[string]bool{"generate-id": true, "path": true, "root": true, "base-uri": true,
	"document-uri": true, "lang": true, "has-children": true, "id": true, "idref": true,
	"element-with-id": true, "in-scope-prefixes": true, "namespace-uri-for-prefix": true,
	"unparsed-entity-uri": true, "unparsed-entity-public-id": true, "deep-equal": true}

// docOfArgFns reach the document of their second argument's node.
var docOfArgFns = map[string]bool{"id": true, "idref": true, "element-with-id": true,
	"unparsed-entity-uri": true, "unparsed-entity-public-id": true}

// isCurrentCall reports whether e is a call on fn:current().
func isCurrentCall(e xpath.Expr) bool {
	x, ok := e.(*xpath.FuncCall)
	return ok && x.Name.URI == fnNS && x.Name.Local == "current" && len(x.Args) == 0
}

func isContextItem(e xpath.Expr) bool {
	_, ok := e.(*xpath.ContextItem)
	return ok
}

// --- Running a split on the cursor -----------------------------------------

// splitInst is a context of one step of P: a node the steps before it
// reached. n counts, per predicate, the candidates that reached it, for the
// positional ones.
type splitInst struct {
	step int
	n    []int64
}

// splitFrame is what an open node is a context for: the child steps whose
// context it is (own), and the descendant steps whose context it or an
// ancestor is (desc; shared with the parent's until it adds one).
type splitFrame struct {
	own  []*splitInst
	desc []*splitInst
	base int    // desc[:base] are the parent's
	hit  uint64 // the branches its node ends (streamsplit_union.go)
}

func (f *splitFrame) live() bool { return len(f.own) > 0 || len(f.desc) > 0 }

func (s *streamSplit) inst(j int) *splitInst {
	st := &s.steps[j]
	if !st.pos && s.insts != nil {
		return &s.insts[j] // no counters: nothing to keep apart
	}
	in := &splitInst{step: j}
	if st.pos {
		in.n = make([]int64, len(st.preds))
	}
	return in
}

// reach makes the node of f a context for step j, and reports whether that
// makes it a match (j is past the last step).
func (s *streamSplit) reach(f *splitFrame, j int) bool {
	if s.isEnd(j) {
		if j > 0 {
			f.hit |= 1 << s.steps[j-1].branch
		}
		return true
	}
	st := &s.steps[j]
	if st.axis == xpath.AxisChild {
		for _, in := range f.own {
			if in.step == j {
				return false
			}
		}
		if len(f.own) == 0 && !st.pos && s.own1 != nil {
			f.own = s.own1[j : j+1 : j+1] // shared: an append copies it
			return false
		}
		f.own = append(f.own, s.inst(j))
		return false
	}
	// One context per descendant step serves all below it unless it counts
	// positions, which are per context node.
	from := 0
	if st.pos {
		from = f.base
	}
	for _, in := range f.desc[from:] {
		if in.step == j {
			return false // a dos one's self match is had already
		}
	}
	f.desc = append(f.desc[:len(f.desc):len(f.desc)], s.inst(j))
	if st.axis == xpath.AxisDescendantOrSelf {
		return s.reach(f, j+1)
	}
	return false
}

// enter is n arriving under the node of pf: the frame it gets, and whether
// it is a match.
func (s *streamSplit) enter(pf *splitFrame, at *splitAt) (f splitFrame, match bool, err error) {
	f.desc, f.base = pf.desc, len(pf.desc)
	for _, list := range [2][]*splitInst{pf.own, pf.desc} {
		for _, in := range list {
			st := &s.steps[in.step]
			if !st.test.Matches(at.n, xdm.KindElement) {
				continue
			}
			ok, err := at.preds(st, in)
			if err != nil {
				return f, false, err
			}
			if !ok || at.leaf && !s.isEnd(in.step+1) {
				continue // a leaf is the context of nothing
			}
			if s.reach(&f, in.step+1) {
				match = true
			}
		}
	}
	if s.set != nil {
		match = s.set.holds(f.hit)
	}
	return f, match, nil
}

// splitAt is a candidate: n, live on the cursor unless stable (a node of a
// grounded copy), with its view or copy made when a predicate needs one.
type splitAt struct {
	r      *splitRun
	n      *xdm.Node
	leaf   bool
	stable bool
	focus  *xdm.Node
	peeked bool
}

func (a *splitAt) node() (*xdm.Node, error) {
	switch {
	case a.stable:
		return a.n, nil
	case a.focus != nil:
	case a.leaf:
		a.focus = xdm.Copy(a.n) // an event node may not be kept
	default:
		v, err := a.r.c.View()
		if err != nil {
			return nil, err
		}
		a.focus = v
	}
	return a.focus, nil
}

func (a *splitAt) hasKids() (bool, error) {
	switch {
	case a.stable:
		return a.n.FirstChild() != nil, nil
	case a.leaf:
		return false, nil
	}
	if _, err := a.node(); err != nil {
		return false, err
	}
	ev, err := a.r.c.Peek()
	a.peeked = true
	return ev.Kind != xdmstream.EndElement, err
}

// preds applies st's predicates to the candidate, as a context of in.
func (a *splitAt) preds(st *splitStep, in *splitInst) (bool, error) {
	for i := range st.preds {
		p := &st.preds[i]
		if p.e == nil {
			in.n[i]++
			if p.pe != nil {
				seq, err := p.pe.Eval(a.r.ctx.WithFocus(xdm.NewInteger(in.n[i]), int(in.n[i]), 0))
				if err != nil {
					return false, err
				}
				if ok, err := xpath.EffectiveBooleanValue(seq); err != nil || !ok {
					return false, err
				}
				continue
			}
			if in.n[i] != p.at {
				return false, nil
			}
			continue
		}
		if p.up && a.leaf && !a.stable && a.focus == nil {
			l, err := a.r.c.Leaf()
			if err != nil {
				return false, err
			}
			a.focus = l
		}
		n, err := a.node()
		if err != nil {
			return false, err
		}
		ctx := a.r.ctx.WithFocus(n, 1, 1)
		if p.kids {
			k, err := a.hasKids()
			if err != nil {
				return false, err
			}
			ctx = ctx.WithVar(splitKids, xdm.One(xdm.NewBoolean(k)))
		}
		seq, err := p.e.Eval(ctx)
		if err != nil {
			return false, err
		}
		if ok, err := xpath.EffectiveBooleanValue(seq); err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// splitFoldBatch is how many values a fold takes at a time.
const splitFoldBatch = 512

// splitRun is one evaluation of a split.
type splitRun struct {
	s   *streamSplit
	ctx *xpath.Context
	c   *xdmstream.Cursor
	// what the matches gave: items (collected, or the nth), n (count),
	// found, and for a fold acc, the values waiting buf, their number cnt
	// and their type typ.
	items xdm.Sequence
	n     int64
	found bool
	acc   xdm.Sequence
	buf   xdm.Sequence
	cnt   int64
	typ   string
	idx   int64
	// done says the result is decided: the rest is only read, as far as
	// the tree would have raised an error in it. silent counts the open
	// regions (inside an outermost match) whose matches are not results.
	done   bool
	silent int
	// seen counts matches, for innermost; skipped those s.skip left out.
	seen, skipped int64
	// each, for a loop (loop), is handed every match in place of take: a
	// view (matchView) or a copy (matchGround). body runs a matchBody loop's
	// body at a match's start tag, at depth d, and reports whether it read
	// the match to its end.
	each func(it xdm.Item) error
	body func(v *xdm.Node, d int) (bool, error)
	// groundRoot grounds a match that is the document element, as any
	// other, for a principal input (streammain_rootground.go).
	groundRoot bool
	// src, for a group split, hands over the members: the walk's context is
	// then a stand-in at the depth above them, whose children they are.
	src memberSource
	// hold is what a holding split has collected (streamhold.go).
	hold *splitHold
	// climbed is the last node a climbing tail gave, and held what a
	// climbSort one gave (streamleaf.go).
	climbed *xdm.Node
	held    []*xdm.Node
	// hit is the branches the match at hand ends, and cset what a
	// climbSet split recorded (streamsplit_climbset.go).
	hit  uint64
	cset *climbSetRun
	// rootCopy, when set, copies a match that is the document element as
	// events instead of giving up (noRoot; streamcopy_l3.go).
	rootCopy func() error
}

// read is the walk's next event: the cursor's, through the filter f
// (skipping the element at hand first); or, for a group split at the top
// (no member open), the next member, at its start tag, and after the last
// the end of the stand-in context, at depth d.
func (r *splitRun) read(skip bool, f *xdmstream.Filter, top bool, d int) (xdmstream.Event, error) {
	switch {
	case r.src != nil && top:
		ev, ok, err := r.src.next(!skip)
		if err != nil || ok {
			ev.Closed = 0 // what closed before it is above the stand-in
			return ev, err
		}
		return xdmstream.Event{Kind: xdmstream.EndElement, Depth: d}, nil
	case skip:
		return r.c.SkipTag(f)
	}
	return r.c.NextTag(f)
}

// eval evaluates s over the cursor c at depth d: 0, before its first
// event, with the document node for context; or d >= 1, at the start tag
// Next just returned, of the element at depth d, the context. It reads to
// the context's end: through EndDocument, or to the element's EndElement,
// which is then the cursor's current event. A parse error is the cursor's;
// errSplitGiveUp says the tree must answer instead.
func (s *streamSplit) eval(ctx *xpath.Context, c *xdmstream.Cursor, d int) (xdm.Sequence, error) {
	return s.evalAt(ctx, c, d, nil)
}

// groupAt is where a split in the body of a streamed xsl:for-each-group
// reads: the current group's members from src, the first of them at its
// start tag, at depth d, its view the body's focus.
type groupAt struct {
	src  memberSource
	d    int
	view *xdm.Node
}

// at moves r to g when s, the split it runs, starts at the current group:
// the walk's context is then the stand-in above the members.
func (r *splitRun) at(g *groupAt, d int) int {
	if g != nil && r.s.group {
		r.src = g.src
		return g.d - 1
	}
	return d
}

// evalAt is eval, in the body of a streamed xsl:for-each-group when g is
// set (d is then g.d, the first member's).
func (s *streamSplit) evalAt(ctx *xpath.Context, c *xdmstream.Cursor, d int, g *groupAt) (xdm.Sequence, error) {
	if err := s.runChecks(ctx); err != nil {
		return nil, err
	}
	ctx = ctx.HoldItemBudget().HoldByteBudget()
	octx := ctx
	var view *xdm.Node
	if d == 0 {
		// The outer reads of the document node only what any has
		// (splitViewFns[vNone]): an empty one stands in for it.
		octx = ctx.WithFocus(xdm.NewTree().Root, 1, 1)
	} else {
		v, err := c.View()
		if err != nil {
			return nil, err
		}
		view, octx = v, ctx.WithFocus(v, 1, 1)
	}
	if s.kids {
		ev, err := c.Peek()
		if err != nil {
			return nil, err
		}
		k := ev.Kind != xdmstream.EndElement && ev.Kind != xdmstream.EndDocument
		octx = octx.WithVar(splitKids, xdm.One(xdm.NewBoolean(k)))
	}
	for s.cond != nil {
		seq, err := s.cond.Eval(octx)
		if err != nil {
			return nil, err
		}
		ok, err := xpath.EffectiveBooleanValue(seq)
		if err != nil {
			return nil, err
		}
		if ok {
			s = s.then
		} else {
			s = s.els
		}
	}
	r := &splitRun{s: s, ctx: ctx, c: c}
	if err := r.run(view, r.at(g, d)); err != nil {
		return nil, err
	}
	value, err := r.finish()
	if err != nil {
		return nil, err
	}
	return s.outer.Eval(octx.WithVar(s.outerVar, value))
}

// loop runs P for an xsl:for-each or xsl:iterate (C5) over the cursor c at
// depth d, as eval reads it, handing each match over as s.mode says: to each
// (matchView, matchGround) or, at its start tag, to body (matchBody). s is a
// bare path: no choice, tail, reducer or outer.
func (s *streamSplit) loop(ctx *xpath.Context, c *xdmstream.Cursor, d int,
	each func(xdm.Item) error, body func(*xdm.Node, int) (bool, error)) error {
	return s.loopAt(ctx, c, d, nil, each, body)
}

// loopAt is loop, in the body of a streamed xsl:for-each-group when g is set.
func (s *streamSplit) loopAt(ctx *xpath.Context, c *xdmstream.Cursor, d int, g *groupAt,
	each func(xdm.Item) error, body func(*xdm.Node, int) (bool, error)) error {

	if err := s.runChecks(ctx); err != nil {
		return err
	}
	var v *xdm.Node
	if d > 0 {
		var err error
		if v, err = c.View(); err != nil {
			return err
		}
	}
	r := &splitRun{s: s, ctx: ctx, c: c, each: each, body: body}
	return r.run(v, r.at(g, d))
}

// run reads the context, the element whose view is v or the document.
func (r *splitRun) run(v *xdm.Node, d int) error {
	s, c := r.s, r.c
	switch {
	case s.docText && v == nil && d == 0: // streamcopy_l3.go
		t, err := docString(c)
		if err != nil {
			return err
		}
		return r.take(xdm.One(xdm.NewUntypedAtomic(t)))
	case s.docText || s.docCopy:
		return errStreamPlan // docCopyEval builds the copy
	}
	if s.ctxItem {
		if s.json != nil {
			return r.jsonContext(v, d) // streamfn_json.go
		}
		if s.atomize && v != nil && typedValue(s, v) { // streamvalidin.go
			items, err := s.groundAtomized(c)
			if err != nil {
				return err
			}
			return r.take(items)
		}
		if s.atomize {
			t, err := c.Text()
			if err != nil {
				return err
			}
			return r.take(xdm.One(xdm.NewUntypedAtomic(t)))
		}
		g, err := groundRecording(c, s) // streamaccum_ground_l4.go
		if err != nil {
			return err
		}
		return r.take(xdm.One(g))
	}
	var root splitFrame
	if s.steps != nil && s.rootFrame(&root) && !s.doc {
		// .//@a: the context is a match (path admits only a view tail).
		if r.skipOne() {
			r.seen++
		} else {
			items, err := r.tailOf(v)
			if err != nil {
				return err
			}
			r.seen++
			if err := r.take(items); err != nil {
				return err
			}
		}
	}
	if err := r.walk(root, d); err != nil {
		return err
	}
	if s.later && s.mode == matchGround && d > 0 {
		return c.Flush()
	}
	return nil
}

// splitOpen is an element the walk is inside.
type splitOpen struct {
	f splitFrame
	// For innermost: the element is a match, seen is the match count when
	// it opened, and pending what it gives if it proves innermost.
	match   bool
	seen    int64
	pending xdm.Sequence
	// silent says it is an outermost match read only for its candidates'
	// predicates.
	silent bool
}

// walk reads the context's content, matching P.
//
// It reads the cursor with NextTag: only start tags whose name some step of
// P tests for are reported (leaves only when a step can match one), and the
// elements passed over are descended into. Such an element is a match of no
// step but a // one, so walk makes up its frame (hidden) when an event below
// it arrives.
func (r *splitRun) walk(root splitFrame, d int) error {
	s, c := r.s, r.c
	stack := []splitOpen{{f: root}}
	// pop closes the open element on top of the stack.
	pop := func() error {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.silent {
			r.silent--
		}
		if top.match && r.seen == top.seen {
			// innermost: nothing matched inside it.
			if s.mode == matchCount {
				top.pending = xdm.Sequence{nil}
			}
			return r.take(top.pending)
		}
		return nil
	}
	// The end tags of the open elements are wanted only where closing one
	// does more than drop its frame: an innermost match's, and a silent
	// region's (outermost).
	ends := s.innermost || s.outermost
	fd := d
	if r.src != nil {
		fd = d + 1 // inside a member: its own end tag is the last event
	}
	want := xdmstream.Filter{Accept: s.accept, Leaves: s.leaves, Depth: fd, Ends: ends}
	none := xdmstream.Filter{Accept: acceptNone, Depth: fd, Ends: ends}
	skip := false
	for {
		// Nothing left to find, nor any predicate whose error the tree
		// would raise: only the ends of the open elements are wanted.
		idle := s.steps == nil || r.done && !s.predFail && !s.tailFail
		filt := &want
		if idle {
			filt = &none
		}
		var (
			ev  xdmstream.Event
			err error
		)
		ev, err = r.read(skip, filt, len(stack) == 1, d)
		skip = false
		if err != nil {
			return err
		}
		if ev.Kind == xdmstream.EndDocument {
			return nil
		}
		if ev.Kind == xdmstream.EndElement && ev.Depth == d {
			return nil
		}
		// The elements passed over that closed are gone; those above the
		// event, open, get their frames.
		for ev.Closed > 0 && d+len(stack)-1 >= ev.Closed {
			if err := pop(); err != nil {
				return err
			}
		}
		at := ev.Depth
		if ev.Kind == xdmstream.EndElement {
			at++
		}
		for d+len(stack) > at {
			if err := pop(); err != nil {
				return err
			}
		}
		for d+len(stack) < at {
			stack = append(stack, splitOpen{f: s.hidden(&stack[len(stack)-1].f)})
		}
		if ev.Kind == xdmstream.EndElement {
			if err := pop(); err != nil {
				return err
			}
			continue
		}
		if idle {
			if ev.Kind == xdmstream.StartElement {
				skip = true
			}
			continue
		}
		cand := splitAt{r: r, n: ev.Node, leaf: ev.Kind != xdmstream.StartElement}
		f, match, err := s.enter(&stack[len(stack)-1].f, &cand)
		if err != nil {
			return err
		}
		r.hit = f.hit
		quiet := r.silent > 0 || r.done && !s.tailFail
		if cand.leaf {
			if match && !quiet {
				r.seen++ // a leaf match makes its ancestors not innermost
				if !s.noLeaves {
					if err := r.leaf(ev.Node, ev.Depth); err != nil {
						return err
					}
				}
			}
			continue
		}
		if err := r.ctx.Err(); err != nil {
			return err
		}
		open := splitOpen{f: f}
		decided, descend := false, f.live()
		if match && !quiet {
			r.seen++
			if s.outermost {
				// Nothing inside it is a result; it is read only for the
				// predicates' errors, or because it was peeked at.
				open.f = splitFrame{}
				if descend = s.predFail && f.live(); descend {
					open.f, open.silent = f, true
				}
			}
			mode := s.mode
			if s.red == "nth" && s.tail == nil && s.tailText == "" && !s.innermost && r.idx+1 < s.nth {
				mode = matchCount // only the nth is read
			}
			switch mode {
			case matchCount:
				if s.innermost {
					open.match, open.seen = true, r.seen
				} else if err := r.take(xdm.Sequence{nil}); err != nil {
					return err
				}
			case matchText:
				if s.json != nil {
					decided = true
					if err := r.jsonMatch(ev); err != nil { // streamfn_json.go
						return err
					}
					break
				}
				if typedValue(s, ev.Node) { // streamvalidin.go
					items, err := s.groundAtomized(c)
					if err != nil {
						return err
					}
					decided = true
					if err := r.take(items); err != nil {
						return err
					}
					break
				}
				t, err := c.Text()
				if err != nil {
					return err
				}
				decided = true
				if err := r.take(xdm.One(s.textAtomic(t))); err != nil {
					return err
				}
			case matchView:
				if s.attr != nil {
					// The values of one attribute: off the start tag.
					var items xdm.Sequence
					if a := ev.Node.Attr(s.attr.URI, s.attr.Local); a != nil {
						items = s.atomized(xdm.One(a))
						if s.red == "" && len(items) == 1 {
							// Kept: its own string, not one into the
							// parse's text block, which it would pin.
							if v, ok := items[0].(*xdm.Atomic); ok && v.Type == xdm.TypeUntypedAtomic {
								items[0] = xdm.NewUntypedAtomic(strings.Clone(v.String()))
							}
						}
					}
					if s.innermost {
						open.match, open.seen, open.pending = true, r.seen, items
					} else if err := r.take(items); err != nil {
						return err
					}
					break
				}
				v, err := c.View()
				if err != nil {
					return err
				}
				items := xdm.One(v) // a loop's: the match itself
				if s.tail != nil && !s.innermost && r.skipOne() {
					break
				}
				if s.tail != nil {
					if items, err = r.tailOf(v); err != nil {
						return err
					}
				}
				if s.innermost {
					open.match, open.seen, open.pending = true, r.seen, items
				} else if err := r.take(items); err != nil {
					return err
				}
			case matchGround:
				if s.noRoot && ev.Depth == 1 && !r.groundRoot {
					if r.skipOne() {
						break // tail(*): the document element is left out, as take leaves it
					}
					decided = true
					if r.rootCopy == nil {
						return errSplitGiveUp
					}
					if err := r.rootCopy(); err != nil { // streamcopy_l3.go
						return err
					}
					break
				}
				decided = true
				if err := r.ground(f); err != nil {
					return err
				}
			case matchBody:
				v, err := c.View()
				if err != nil {
					return err
				}
				if decided, err = r.body(v, ev.Depth); err != nil {
					return err
				}
			}
		}
		switch {
		case decided:
		case descend || cand.peeked || open.match:
			if open.silent {
				r.silent++
			}
			stack = append(stack, open)
		default:
			skip = true
		}
	}
}

// acceptNone takes no element: NextTag then reports only end tags.
func acceptNone(*xdm.Node) bool { return false }

// accepts sets s.accept, the start tags walk must see -- those of elements
// some step tests for, all when a step's test is not a name -- and
// s.leaves, whether a step can match a leaf.
//
// A descendant-or-self::node() step (//) matches every node, as the
// context of the step after it: hidden makes that frame for an element
// passed over. It is not tested for, unless it is the last step, when every
// element is a match.
func (s *streamSplit) accepts() {
	s.insts = make([]splitInst, len(s.steps))
	s.own1 = make([]*splitInst, len(s.steps))
	for j := range s.insts {
		s.insts[j].step = j
		s.own1[j] = &s.insts[j]
	}
	var tests []xpath.NodeTest
	for i, st := range s.steps {
		if i == 0 && s.group {
			continue // its candidates are the members: not read by the filter
		}
		leaf, _ := testKinds(st.test)
		s.leaves = s.leaves || leaf
		if st.axis == xpath.AxisDescendantOrSelf && !s.isEnd(i+1) {
			continue
		}
		if _, ok := st.test.(*xpath.NameTest); !ok {
			return // accept stays nil: every start tag
		}
		tests = append(tests, st.test)
	}
	s.accept = func(n *xdm.Node) bool {
		for _, t := range tests {
			if t.Matches(n, xdm.KindElement) {
				return true
			}
		}
		return false
	}
}

// hidden is the frame of an element under pf that walk passed over, which
// no step but a // one matches: what enter would make of it.
func (s *streamSplit) hidden(pf *splitFrame) splitFrame {
	f := splitFrame{desc: pf.desc, base: len(pf.desc)}
	for _, in := range pf.desc {
		if s.steps[in.step].axis == xpath.AxisDescendantOrSelf {
			s.reach(&f, in.step+1)
		}
	}
	return f
}

// atomized is items atomized; for fn:string's argument (strVal), a node is
// its string value instead: string(@a) of a typed @a="2.00" is "2.00", not
// the "2" of its xs:decimal.
func (s *streamSplit) atomized(items xdm.Sequence) xdm.Sequence {
	if !s.strVal {
		return xdm.Atomize(items)
	}
	out := make(xdm.Sequence, len(items))
	for k, it := range items {
		if n, ok := it.(*xdm.Node); ok {
			out[k] = xdm.NewUntypedAtomic(n.StringValue())
		} else {
			out[k] = it
		}
	}
	return out
}

// textAtomic is the value a match's string value t gives.
func (s *streamSplit) textAtomic(t string) *xdm.Atomic {
	if s.tailText == "string" {
		return xdm.NewString(t)
	}
	return xdm.NewUntypedAtomic(t)
}

// leaf takes a text, comment or PI match n (live on the cursor, or in a
// grounded copy, d 0): counted, atomized, or kept (keepLeaves) -- a live one,
// at depth d, as its copy, the original to order and identity (Cursor.Leaf),
// which a matchBody loop's body is handed as a match.
func (r *splitRun) leaf(n *xdm.Node, d int) error {
	s := r.s
	if s.json != nil {
		return errJSONStream // streamfn_json.go: not an element or document node
	}
	switch {
	case s.mode == matchCount || s.red == "nth" && !s.atomize && s.tailText == "" && s.tail == nil && r.idx+1 < s.nth:
		return r.take(xdm.Sequence{nil})
	case s.tailText == "string":
		return r.take(xdm.One(xdm.NewString(n.StringValue())))
	case s.keepLeaves && (s.tail != nil || !s.atomize && s.tailText == ""):
		if d > 0 {
			c, err := r.c.Leaf()
			if err != nil {
				return err
			}
			n = c
		}
		if r.body != nil {
			_, err := r.body(n, d)
			return err
		}
		if s.tail != nil {
			if r.skipOne() {
				return nil
			}
			items, err := s.tail.Eval(r.ctx.WithFocus(n, 1, 1))
			if err != nil {
				return err
			}
			return r.take(items)
		}
		return r.take(xdm.One(n))
	}
	return r.take(s.atomized(xdm.One(n)))
}

// ground copies the element the cursor is at and takes it, and the matches
// inside it, from the copy.
func (r *splitRun) ground(f splitFrame) error {
	if r.s.later {
		return r.c.GroundLater(func(g *xdm.Node) error { return r.grounded(g, &f) })
	}
	g, err := groundRecording(r.c, r.s) // streamaccum_ground_l4.go
	if err != nil {
		return err
	}
	return r.grounded(g, &f)
}

func (r *splitRun) grounded(g *xdm.Node, f *splitFrame) error {
	s := r.s
	if r.done && !s.tailFail {
		// Decided while the copy waited: only predicates inside it remain.
		if !s.predFail || !f.live() {
			return nil
		}
		r.silent++
		_, err := r.walkTree(g, f)
		r.silent--
		return err
	}
	if s.innermost {
		found, err := r.walkTree(g, f)
		if err != nil || found {
			return err
		}
		return r.yield(g)
	}
	if err := r.yield(g); err != nil {
		return err
	}
	if !s.nests || !f.live() || s.outermost && !s.predFail {
		return nil
	}
	if s.outermost {
		r.silent++
		defer func() { r.silent-- }()
	}
	_, err := r.walkTree(g, f)
	return err
}

// walkTree finds the matches inside n, a node of a grounded copy whose frame
// is pf, and takes them; it reports whether there was one.
func (r *splitRun) walkTree(n *xdm.Node, pf *splitFrame) (found bool, err error) {
	s := r.s
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if r.done && !s.predFail && !s.tailFail {
			break
		}
		at := splitAt{r: r, n: ch, stable: true, leaf: ch.Kind() != xdm.KindElement}
		f, match, err := s.enter(pf, &at)
		if err != nil {
			return found, err
		}
		quiet := r.silent > 0 || r.done && !s.tailFail
		if at.leaf {
			if match && !quiet {
				found = true
				if !s.noLeaves {
					if err := r.leaf(ch, 0); err != nil {
						return found, err
					}
				}
			}
			continue
		}
		if match && !quiet {
			found = true
			if s.innermost {
				in, err := r.walkTree(ch, &f)
				if err != nil {
					return found, err
				}
				if !in {
					if err := r.yield(ch); err != nil {
						return found, err
					}
				}
				continue
			}
			if err := r.yield(ch); err != nil {
				return found, err
			}
			if s.outermost {
				if !s.predFail || !f.live() {
					continue
				}
				r.silent++
				_, err := r.walkTree(ch, &f)
				r.silent--
				if err != nil {
					return found, err
				}
				continue
			}
		}
		if f.live() {
			in, err := r.walkTree(ch, &f)
			if err != nil {
				return found, err
			}
			found = found || in
		}
	}
	return found, nil
}

// yield takes what an element match n of a grounded copy gives.
func (r *splitRun) yield(n *xdm.Node) error {
	s := r.s
	switch {
	case s.tail != nil:
		if r.skipOne() {
			return nil
		}
		items, err := s.tail.Eval(r.ctx.WithFocus(n, 1, 1))
		if err != nil {
			return err
		}
		return r.take(items)
	case s.tailText != "":
		return r.take(xdm.One(s.textAtomic(n.StringValue())))
	case s.atomize:
		return r.take(s.atomized(xdm.One(n)))
	}
	return r.take(xdm.One(n))
}

// skipOne reports whether the match at hand is one tail(Q) leaves out, and
// counts it. A match's tail is evaluated only after it: tail(Q) ! R and
// tail(Q)/T apply R or T to Q's matches but the first, so the first's tail
// must not run (its error is not the tree's: tail(//PRICE) ! (.+1) on a
// first PRICE of "x").
func (r *splitRun) skipOne() bool {
	if r.skipped < r.s.skip {
		r.skipped++
		return true
	}
	return false
}

// take hands items to the reducer or the collection. A nil item stands for
// a match that is only counted.
func (r *splitRun) take(items xdm.Sequence) error {
	s := r.s
	if r.skipOne() {
		return nil
	}
	if r.each != nil {
		for _, it := range items {
			if err := r.each(it); err != nil {
				return err
			}
		}
		return nil
	}
	switch s.red {
	case "":
		if s.atomize && !s.keepEmpty { // streamcopy_l3text.go
			items = s.atomized(items)
		}
		if s.counted {
			items = standIns(len(items))
		}
		if s.hold {
			if err := r.holdOn(items); err != nil {
				return err
			}
		}
		r.items = append(r.items, items...)
	case "count":
		r.n += int64(len(items))
	case "exists", "empty", "boolean", "not":
		if len(items) > 0 && !r.done {
			r.found, r.done = true, true
		}
	case "nth":
		for _, it := range items {
			if r.done {
				break
			}
			if r.idx++; r.idx == s.nth && it != nil {
				r.items, r.done = xdm.One(it), true
				if s.atomize {
					r.items = s.atomized(r.items)
				}
				break
			}
		}
	default:
		atoms := xdm.Atomize(items)
		r.buf = append(r.buf, atoms...)
		r.cnt += int64(len(atoms))
		if len(r.buf) >= splitFoldBatch {
			return r.fold()
		}
	}
	return nil
}

// fold folds the values waiting into the accumulator.
func (r *splitRun) fold() error {
	if len(r.buf) == 0 {
		return nil
	}
	// min and max over one type compare one way throughout; over several,
	// promotion makes the answer depend on the order of the comparisons.
	// The context item's values come in one take, folded once: the tree's
	// min or max of them (streamsplit_onetake.go).
	if r.s.red != "sum" && r.s.red != "avg" && !r.s.oneTake() && !sameAtomicType(r.buf, &r.typ) {
		return errSplitGiveUp
	}
	acc, err := r.s.fold.Eval(r.ctx.WithVar(splitAcc, r.acc).WithVar(splitItems, r.buf))
	if err != nil {
		return err
	}
	r.acc, r.buf = acc, r.buf[:0]
	return nil
}

// finish is the value the outer is given: the items, or the reducer's.
func (r *splitRun) finish() (xdm.Sequence, error) {
	if err := r.climbFinish(); err != nil {
		return nil, err
	}
	b := func(v bool) (xdm.Sequence, error) { return xdm.One(xdm.NewBoolean(v)), nil }
	switch r.s.red {
	case "", "nth":
		return r.items, nil
	case "count":
		return xdm.One(xdm.NewInteger(r.n)), nil
	case "exists", "boolean":
		return b(r.found)
	case "empty", "not":
		return b(!r.found)
	}
	if err := r.fold(); err != nil {
		return nil, err
	}
	if r.s.red == "avg" {
		if r.cnt == 0 {
			return nil, nil
		}
		return r.s.avg.Eval(r.ctx.WithVar(splitAcc, r.acc).WithVar(splitN, xdm.One(xdm.NewInteger(r.cnt))))
	}
	if r.acc == nil {
		return r.s.fold.Eval(r.ctx.WithVar(splitAcc, nil).WithVar(splitItems, nil))
	}
	return r.acc, nil
}
