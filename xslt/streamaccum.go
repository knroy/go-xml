package xslt

// Streamed accumulators (full streaming, component C9; DESIGN section 1.7,
// "Accumulators"; XSLT 3.0 §18.2).
//
// A streamed run -- a streamable xsl:source-document's core plan, or
// TransformReader's principal input -- computes the accumulators applicable
// to its document as a cursor observer: the observer is told of every node
// in document order (xdmstream.Observer: elements at their start and end,
// leaves once, inside skipped and grounded elements too, then the document's
// end), and takes for each accumulator the step accumulatorValuesFor's walk
// takes over a tree: the start rules at a node's start, the end rules at its
// end, the same rule chosen (applyAccumRules), the same conversion, the same
// item accounting (accumExtend). Rules are evaluated on the parse's own node
// while the observer is told of it: its ancestors are open, and its name,
// attributes and (for a leaf) value complete; nothing of it is kept.
//
// The values a node of the stream has are kept on frames, one per open
// element, by depth, with the element's ordinal, and the document's at depth
// 0: fn:accumulator-before of a view (or of the document node's stand-in)
// reads its frame, found by depth and checked by ordinal; -after reads it
// once the element has closed. A call that would need what is no longer
// held is a fault of the plan (errAccumOffStream), never an answer: the
// container re-runs on the tree. -after of the context, not read yet, reads
// it to its end first (§19.8.9.1: consuming), as the one reader of it.
//
// Errors. The tree computes an accumulator over the whole document at the
// first call on it, and that call raises the first error of the walk, from
// wherever in the document it comes, and of the first accumulator read whose
// walk fails. The stream finds an error where it arises: it is kept, raised
// by any later read of that accumulator, and once the document has been
// read, the run fails with the error of the earliest-read accumulator that
// has one -- before any other error the run met after that read (a parse
// error still comes first, as xdm.Parse reports one before the transform
// starts). Tree mode words these errors the same whatever the call site.
//
// What is computed must be provably what the tree computes, so the
// stylesheet is checked when it compiles (accumStreamWhy), and the container
// (or, for TransformReader, the mode) left to the tree otherwise: every
// applicable accumulator that is streamable must have patterns a node's
// start tag decides (no positional predicate, nothing below or beside the
// node, its ancestors read as views), an initial value and rule values read
// of the node only what it has, in the top-level package, and values that
// hold no node of the parse (an atomic as type, or values made of values).
// Rules reading accumulators are refused.

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// errAccumOffStream is a call on an accumulator for a node of the stream
// whose values the run does not hold: a fault of the plan.
var errAccumOffStream = fmt.Errorf("%w: an accumulator value the stream does not hold", errStreamPlan)

// --- Compiling --------------------------------------------------------------

// accumStreamWhy reports why the observer cannot compute def as the tree
// does, or "".
func accumStreamWhy(def *accumulatorDef) string {
	name := "accumulator " + def.name.Lexical()
	if def.pkg != 0 || def.el == nil {
		return name + ": in a package"
	}
	e := parseAt(def.el, def.el.AttrValue("initial-value"))
	if e == nil || !(viewCheck{noIdent: true}).safe(e, vNone, false) || !accumValueSafe(e) {
		return name + ": initial-value " + def.el.AttrValue("initial-value")
	}
	atomic := atomicSequenceType(def.el.AttrValue("as"))
	// A map or array $value is a lookup's target: what it is applied to is
	// atomized, and what it returns is a value it holds.
	as := strings.TrimSpace(def.el.AttrValue("as"))
	lookup := strings.HasPrefix(as, "map(") || strings.HasPrefix(as, "array(")
	for _, r := range def.rules {
		can, why := accumPatternClasses(r.match)
		if why != "" {
			return name + ": " + why
		}
		src, at := "", r.el
		switch {
		case r.select_ != nil:
			src = r.el.AttrValue("select")
		case r.body != nil:
			// A sequence constructor of one xsl:sequence: its select is the
			// value.
			var one *xdm.Node
			for ch := range r.el.Children() {
				switch {
				case ch.Kind() == xdm.KindElement && one == nil && isXSL(ch, "sequence") &&
					ch.Attr("", "select") != nil && ch.NumAttrs() == 1:
					one = ch
				case ch.Kind() == xdm.KindElement,
					ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) != "":
					return name + ": a rule's sequence constructor"
				}
			}
			if one == nil {
				return name + ": a rule's sequence constructor"
			}
			src, at = one.AttrValue("select"), one
		default:
			continue // the empty sequence
		}
		e := parseAt(at, src)
		if e == nil || !atomic && !accumValueSafe(e) {
			return name + ": a rule's value may hold a node: " + src
		}
		for cl, f := range [numClasses]vfocus{classElem: vElem, classLeaf: vLeaf, classDoc: vNone} {
			if can[cl] && nodeClass(cl) != classAttr &&
				!(viewCheck{noIdent: true, up: true, curOK: true, cur: f, lookup: lookup, accum: true}).safe(e, f, false) {
				return name + ": a rule's value reads more than the node has: " + src
			}
		}
	}
	return ""
}

// accumPatternClasses is patternClasses for an accumulator rule, whose
// pattern is matched on the parse's own node at its start: a leaf has its
// ancestors there, so a pattern may name them, and current() is the node.
func accumPatternClasses(p *Pattern) (can [numClasses]bool, why string) {
	if len(p.general) > 0 || strings.Contains(p.src, "accumulator-") {
		return can, "pattern " + p.src
	}
	for _, a := range p.alts {
		if a.call != nil {
			return can, "pattern " + p.src + ": id() or key()"
		}
		var c [numClasses]bool
		if len(a.steps) == 0 {
			if !a.absolute {
				return can, "pattern " + p.src
			}
			can[classDoc] = true // "/"
			continue
		}
		last := a.steps[len(a.steps)-1]
		switch t := last.nodeTest.(type) {
		case *xpath.NameTest:
			if last.namespace {
				return can, "pattern " + p.src + ": namespace nodes"
			}
			c[classElem], c[classAttr] = !last.attribute, last.attribute
		case *xpath.KindTest:
			if t.TypeName != "" || t.SchemaDeclared || t.Content != nil || last.namespace {
				return can, "pattern " + p.src + ": kind test " + t.String()
			}
			switch {
			case last.attribute:
				c[classAttr] = t.Any || t.Kind == xdm.KindAttribute
			case t.Any:
				c[classElem], c[classLeaf] = true, true
				c[classDoc] = last.explicitAxis || last.self
			case t.Kind == xdm.KindElement:
				c[classElem] = true
			case t.Kind == xdm.KindDocument:
				c[classDoc] = true
			case t.Kind == xdm.KindText || t.Kind == xdm.KindComment || t.Kind == xdm.KindPI:
				c[classLeaf] = true
			default:
				return can, "pattern " + p.src + ": kind test " + t.String()
			}
		default:
			return can, "pattern " + p.src
		}
		if c[classDoc] && (len(a.steps) > 1 || last.descendant) {
			return can, "pattern " + p.src + ": a document node below another"
		}
		for cl, f := range [numClasses]vfocus{classElem: vElem, classLeaf: vLeaf, classAttr: vLeaf, classDoc: vNone} {
			if !c[cl] {
				continue
			}
			for i, st := range a.steps {
				for _, pr := range st.preds {
					pf := vElem
					if i == len(a.steps)-1 {
						pf = f
					}
					if needsPosition(pr) || pf == vNone ||
						!(viewCheck{noIdent: true, up: true, curOK: true, cur: f}).safe(pr, pf, true) {
						return can, "pattern " + p.src + ": predicate " + pr.String() + " not decided at the start tag"
					}
				}
			}
		}
		for i := range c {
			can[i] = can[i] || c[i]
		}
	}
	return can, ""
}

// accumValueSafe reports, conservatively, whether e returns no node of its
// focus: atomic values, or values made of $value and other variables (whose
// values hold none), as a map, a sequence or a choice of them.
func accumValueSafe(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.VarRef, *xpath.Literal:
		return true
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !accumValueSafe(it) {
				return false
			}
		}
		return true
	case *xpath.IfExpr:
		return accumValueSafe(x.Then) && accumValueSafe(x.Else)
	case *xpath.DynamicCall:
		return isVarRef(x.Target, xdm.QName{Local: "value"}) // a value $value holds
	case *xpath.MapConstructor:
		for _, v := range x.Values {
			if !accumValueSafe(v) {
				return false
			}
		}
		return true
	case *xpath.FuncCall:
		switch {
		case x.Name.URI == xdm.NSMap && x.Name.Local == "put" && len(x.Args) == 3:
			return accumValueSafe(x.Args[0]) && accumValueSafe(x.Args[2]) // the key is atomized
		case x.Name.URI == xdm.NSMap && x.Name.Local == "entry" && len(x.Args) == 2:
			return accumValueSafe(x.Args[1])
		case x.Name.URI == fnNS && len(x.Args) > 0:
			switch x.Name.Local {
			case "head", "tail", "reverse", "subsequence", "remove":
				return accumValueSafe(x.Args[0])
			}
		}
	}
	return atomicValued(e)
}

// accumRefusal reports why a streamed run of a document whose applicable
// accumulators are set (nil: all) cannot compute them, or "". main says the
// run is TransformReader's, whose tree-mode answer computes a non-streamable
// accumulator as well (a source-document's raises XTDE3362 on reading one).
func (s *Stylesheet) accumRefusal(set *modeAccumulators, main bool) string {
	for _, k := range slices.Sorted(maps.Keys(s.accumulators)) {
		def := s.accumulators[k]
		if !accumApplies(set, def) {
			continue
		}
		if !def.streamable {
			if main {
				return "accumulator " + def.name.Lexical() + " not streamable"
			}
			continue
		}
		if why := accumStreamWhy(def); why != "" {
			return why
		}
	}
	return ""
}

// accumApplies reports whether def is applicable under set (nil: all).
func accumApplies(set *modeAccumulators, def *accumulatorDef) bool {
	return set == nil || set.all || set.names[def.name.Clark()]
}

// linkStreamAccums leaves on the tree each streamed container whose
// document has an applicable accumulator the observer cannot compute, once
// every accumulator has compiled.
func (c *compiler) linkStreamAccums() {
	for _, sd := range c.accumLinks {
		if sd.instr.stream == nil || len(c.sheet.accumulators) == 0 {
			continue
		}
		if why := c.sheet.accumRefusal(sd.instr.accums, false); why != "" {
			sd.instr.stream = nil
			if traceCore != nil {
				traceCore(sd.el, why)
			}
		}
	}
	c.accumLinks = nil
}

// mainAccumRefusal is accumRefusal for a principal input streamed in mode m,
// whose applicable accumulators the mode declares (noteInitialAccumulators).
func (s *Stylesheet) mainAccumRefusal(m string) string {
	if len(s.accumulators) == 0 {
		return ""
	}
	set, ok := s.modeAccums[m]
	if !ok {
		set = emptyModeAccumulators
	}
	return s.accumRefusal(set, true)
}

// --- Running ----------------------------------------------------------------

// streamAccums computes the accumulators of one streamed run.
type streamAccums struct {
	rt   *runtime
	ctx  *xpath.Context
	run  *streamRun
	root *xdm.Node // the document node's stand-in
	defs []*accumulatorDef
	at   map[*accumulatorDef]int
	// cur is each accumulator's value now, ext its item accounting, errs
	// its first error, read when it was first read (0: never; reads counts).
	cur   []xdm.Sequence
	ext   []accumExtend
	errs  []error
	read  []int
	reads int
	// frames[d] holds the values of the element open at depth d, or the
	// last one there (0: the document node); ended says the document's end
	// has been met.
	frames []accumFrame
	ended  bool
}

type accumFrame struct {
	ord           uint64
	before, after []xdm.Sequence
	closed        bool
}

// observeAccums starts computing, for run, the accumulators applicable to
// its document under set (nil: all), when the stylesheet declares any, and
// returns the observer (nil when it declares none). streamed says the
// document was asked to stream (xsl:source-document), which XTDE3362 bars a
// non-streamable accumulator from. It must be called before the cursor's
// first event; stop ends it.
func (run *streamRun) observeAccums(rt *runtime, set *modeAccumulators, streamed bool) *streamAccums {
	s := rt.sheet
	if len(s.accumulators) == 0 {
		return nil
	}
	if streamed {
		rt.streamedTrees[run.root] = true
	}
	if set != nil {
		rt.treeAccums[run.root] = set
	}
	r := *rt
	r.bursts = nil // the rules are no instruction of a plan
	sa := &streamAccums{rt: &r, ctx: rt.ctx, run: run, root: run.root, at: map[*accumulatorDef]int{}}
	for _, k := range slices.Sorted(maps.Keys(s.accumulators)) {
		if def := s.accumulators[k]; def.streamable && def.pkg == 0 && accumApplies(set, def) {
			sa.defs = append(sa.defs, def)
		}
	}
	slices.SortFunc(sa.defs, func(a, b *accumulatorDef) int { return a.declOrder - b.declOrder })
	n := len(sa.defs)
	sa.cur, sa.ext, sa.errs, sa.read = make([]xdm.Sequence, n), make([]accumExtend, n), make([]error, n), make([]int, n)
	f := sa.frame(0)
	for i, def := range sa.defs {
		sa.at[def] = i
		// accumulatorValuesFor's start: the initial value, at the document node.
		cur, err := def.initial.Eval(withFocusCurrent(sa.ctx, sa.root, 1, 1))
		if err != nil {
			err = fmt.Errorf("evaluating xsl:accumulator %s initial-value: %w", def.name.Lexical(), err)
		} else {
			cur, err = sa.rt.convertAccum(def, cur)
		}
		sa.cur[i], sa.errs[i] = cur, err
	}
	sa.step(sa.root, false, f.before)
	rt.streamAccums = append(rt.streamAccums, sa)
	run.c.Observe(sa.observe)
	return sa
}

// stop ends sa's registration with the runtime.
func (sa *streamAccums) stop() {
	if sa == nil {
		return
	}
	l := sa.rt.streamAccums
	if i := slices.Index(l, sa); i >= 0 {
		sa.rt.streamAccums = slices.Delete(l, i, i+1)
	}
}

// frame returns the frame at depth d, made if need be.
func (sa *streamAccums) frame(d int) *accumFrame {
	for len(sa.frames) <= d {
		n := len(sa.defs)
		sa.frames = append(sa.frames, accumFrame{before: make([]xdm.Sequence, n), after: make([]xdm.Sequence, n)})
	}
	return &sa.frames[d]
}

// observe is the cursor's observer.
func (sa *streamAccums) observe(n *xdm.Node, d int, end bool, ord uint64) {
	switch {
	case n == nil:
		sa.step(sa.root, true, sa.frames[0].after)
		sa.frames[0].closed, sa.ended = true, true
	case n.Kind() != xdm.KindElement:
		sa.step(n, false, nil)
		sa.step(n, true, nil)
	case !end:
		f := sa.frame(d)
		f.ord, f.closed = ord, false
		sa.step(n, false, f.before)
	default:
		f := sa.frame(d)
		sa.step(n, true, f.after)
		f.closed = true
	}
}

// step applies the start (or end: post) rules of each accumulator to n, and
// records the values in into (when set). An accumulator that has failed
// stays failed: the tree's walk stops at its first error.
func (sa *streamAccums) step(n *xdm.Node, post bool, into []xdm.Sequence) {
	for i, def := range sa.defs {
		if sa.errs[i] == nil {
			next, err := sa.rt.applyAccumRules(def, n, sa.cur[i], sa.ctx, post)
			if err == nil {
				next, err = sa.ext[i].extend(sa.ctx, sa.cur[i], next)
			}
			if err != nil {
				sa.errs[i] = err
			} else {
				sa.cur[i] = next
			}
		}
		if into != nil {
			into[i] = sa.cur[i]
		}
	}
}

// streamAccumsOf returns the observer of the run that node is of: a view
// or a grounded copy of its document's elements, or its document node's
// stand-in; nil for a node of no stream. A node copied out of a stream no
// run is observing is a fault (errAccumOffStream): the tree's walk over its
// copy would answer for the copy, not the document.
func (rt *runtime) streamAccumsOf(node *xdm.Node) (*streamAccums, error) {
	for i := len(rt.streamAccums) - 1; i >= 0; i-- {
		sa := rt.streamAccums[i]
		if node == sa.root {
			return sa, nil
		}
		if _, ok := sa.run.c.Ordinal(node); ok {
			return sa, nil
		}
	}
	if xdmburst.FromStream(node) {
		return nil, errAccumOffStream
	}
	return nil, nil
}

// value is fn:accumulator-before (or -after: post) of def at node, a node
// of sa's stream, the checks of fnAccumulator made.
func (sa *streamAccums) value(def *accumulatorDef, node *xdm.Node, post bool) (xdm.Sequence, error) {
	i, ok := sa.at[def]
	if !ok {
		return nil, errAccumOffStream
	}
	if sa.read[i] == 0 {
		sa.reads++
		sa.read[i] = sa.reads
	}
	if sa.errs[i] != nil {
		return nil, sa.errs[i]
	}
	d, ord := 0, uint64(0)
	if node != sa.root {
		ord, _ = sa.run.c.Ordinal(node)
		for p := node.Parent(); p != nil; p = p.Parent() {
			d++
		}
	}
	if d >= len(sa.frames) || sa.frames[d].ord != ord {
		return nil, errAccumOffStream
	}
	if !post {
		return sa.frames[d].before[i], nil
	}
	if !sa.frames[d].closed {
		if err := sa.consume(d); err != nil {
			return nil, err
		}
		if sa.errs[i] != nil {
			return nil, sa.errs[i]
		}
	}
	return sa.frames[d].after[i], nil
}

// consume reads the context of the instruction running, the element at
// depth d (0: the document), to its end, for an -after of it: only while
// nothing has read it, and nothing reads it but on the cursor.
func (sa *streamAccums) consume(d int) error {
	run := sa.run
	if run.used || run.off || run.grp != nil || run.d != d {
		return errAccumOffStream
	}
	run.used = true
	if d == 0 {
		return run.drain()
	}
	f := &xdmstream.Filter{Accept: acceptNone, Depth: d}
	for {
		ev, err := run.c.NextTag(f)
		switch {
		case err != nil:
			return err
		case ev.Kind == xdmstream.EndDocument:
			return errAccumOffStream
		case ev.Kind == xdmstream.EndElement && ev.Depth == d:
			if !sa.frames[d].closed {
				return errAccumOffStream
			}
			return nil
		}
	}
}

// pending is the error the tree's run would raise for the accumulators
// read, the document read whole: that of the earliest-read one that has an
// error, or nil.
func (sa *streamAccums) pending() error {
	if sa == nil {
		return nil
	}
	best := -1
	for i, r := range sa.read {
		if r > 0 && sa.errs[i] != nil && (best < 0 || r < sa.read[best]) {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	return sa.errs[best]
}

// finish ends a source-document run whose body succeeded: when an
// accumulator was read, the document is read to its end (nothing reads it
// any more), and an error the tree would have raised is the run's.
func (sa *streamAccums) finish() error {
	if sa == nil || sa.reads == 0 {
		return nil
	}
	if !sa.ended {
		if err := sa.run.drain(); err != nil {
			return err
		}
	}
	return sa.pending()
}

// accumAfterOp plans el, an instruction that consumes its focus f, when
// what consumes is accumulator-after of the focus and nothing else: it runs
// as it is, and the call reads the focus to its end first (consume), as the
// one reader of it. It is an op of the plan, so that a loop runs its body at
// each match's start tag (matchBody), and a sequence constructor counts it
// as its consumer.
func (b *streamBuilder) accumAfterOp(el *xdm.Node, f vfocus) bool {
	if b.grp || b.noIdent || !mentionsAccumAfter(el) {
		return false
	}
	// Checked on a builder of its own: a check that fails part-way must
	// leave nothing in this plan.
	var kinds [streamtrace.NumKinds]int64
	var ops []*streamOp
	sb := &streamBuilder{pl: &streamPlan{recs: map[any]*planRec{}}, x: newStreamExec(), ops: &ops,
		kinds: &kinds, key: b.key, after: true}
	if !sb.motionless(el, f) || len(ops) > 0 {
		return false
	}
	*b.ops = append(*b.ops, &streamOp{x: b.x, el: el, doc: f == vNone, accAfter: true})
	b.kinds[streamtrace.KindTree]++
	return true
}

// mentionsAccumAfter reports whether an attribute of el or of an element in
// it names accumulator-after.
func mentionsAccumAfter(el *xdm.Node) bool {
	found := false
	walkElements(el, func(e *xdm.Node) bool {
		for a := range e.Attrs() {
			found = found || strings.Contains(a.Value(), "accumulator-after")
		}
		return !found
	})
	return found
}
