package xslt

// Streamed grouping (full streaming, component C8; DESIGN section 1.7,
// "Grouping"; XSLT 3.0 §19.8.4.19).
//
// An xsl:for-each-group with group-adjacent, group-starting-with or
// group-ending-with whose population is read off the cursor -- a path of
// child steps from the context, or the current group of an enclosing one --
// is read once, and each group is processed as the cursor reaches it:
//
//   - the population is walked by popWalk, each item handed over at its
//     start tag. Its key (group-adjacent) or whether it matches the pattern
//     is decided on its view, which is what §19.8.4.19 requires of them
//     (motionless), by the tree's own code (adjacentKeyOf, Pattern.Matches).
//   - a group's body runs at its first item's start tag, the item's view
//     its focus, position() the group's number. Its one consuming
//     instruction (§19.8: one per construct) reads either the first item
//     (".") as any context, or current-group(): a split whose first step is
//     the group's members (streamsplit.go, group), which groupRun hands
//     over one by one, deciding at each whether it still belongs to the
//     group -- a lazy current-group(), read once and in order. That covers
//     a select (copy-of, value-of, sequence, count(), sum(), ...),
//     xsl:for-each and xsl:iterate over it, xsl:apply-templates over it, and
//     a nested xsl:for-each-group over it.
//   - what the body did not read of its group is skipped, member by member,
//     until the next group's first item, which the walk has met by then.
//
// Memory is one item of look-ahead and what the body keeps: a split that
// would collect the group's members as grounded copies (which can be the
// whole document) is refused; copy-of copies them one at a time.
//
// Not streamed (the container runs on the tree): group-by and xsl:sort,
// which §19.8.4.19 makes free-ranging over a streamed population; a
// population that is not child steps from the context (a crawling one with
// a consuming body is free-ranging; outermost(), predicates reading
// has-children, attributes and leaves are not taken); a key or pattern a
// view cannot decide.
//
// Errors are the tree's, in the tree's order. The tree evaluates the select
// (its predicates), then the collation, then every item's key, then the
// bodies; the stream meets them interleaved. So when one comes, the rest of
// the population is read on -- predicates, and after a body's error keys too,
// evaluated as the tree would -- and the first in the tree's order is the
// error (groupRun.settle). A container would re-run on the tree anyway; a
// principal input (TransformReader) has nothing to re-run.

import (
	"errors"
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// memberSource hands over elements of one depth, each at its start tag: the
// population of a streamed grouping, or the members of its current group.
type memberSource interface {
	// next finishes with the element handed over last -- it is skipped
	// unless read says what it was handed to read it to its end -- and
	// returns the next one, at its start tag; ok is false after the last.
	next(read bool) (ev xdmstream.Event, ok bool, err error)
}

// --- Planning -------------------------------------------------------------

// groupOp plans an xsl:for-each-group whose select consumes, its focus f.
func (b *streamBuilder) groupOp(el *xdm.Node, f vfocus) bool {
	if b.groundedSelect(el) {
		return b.groundedGroup(el, f)
	}
	for a := range el.Attrs() {
		n := a.Name()
		if n.URI != "" {
			continue
		}
		switch n.Local {
		case "select", "group-starting-with", "group-ending-with":
			// The patterns are checked once compiled (noteStreamGroup).
		case "group-adjacent":
			e := parseAt(el, a.Value())
			if e == nil || !(viewCheck{pos: true, key: b.key}).safe(e, vElem, false) {
				return b.refuse("group-adjacent not motionless on the view: " + a.Value())
			}
		case "group-by":
			return b.heldGroup(el, f) // streamgroup_held.go
		case "collation":
			if !b.avt(el, a.Value(), f) {
				return false
			}
		case "composite":
		default:
			if !streamAttrsOK[n.Local] {
				return b.refuse("attribute " + n.Local + " of " + elName(el))
			}
		}
	}
	fromGroup := isCurrentGroup(parseAt(el, el.AttrValue("select")))
	if fromGroup && !b.grp {
		return b.refuse("xsl:for-each-group over a current group out of reach")
	}
	var body []*xdm.Node
	for ch := range el.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.ContainsAny(ch.Value(), "{}"):
			return b.refuse("text with a brace")
		case ch.Kind() != xdm.KindElement:
		case isXSL(ch, "sort"):
			return b.heldGroup(el, f) // streamgroup_held.go
		default:
			body = append(body, ch)
		}
	}
	op := &streamOp{x: b.x, el: el, doc: f == vNone, group: true, fromGroup: fromGroup,
		inGroup: b.grp, key: b.key}
	inner := &streamBuilder{pl: b.pl, x: b.x, ops: &op.inner, kinds: &op.kinds, grp: true, key: true}
	if !inner.members(el, body, vElem) {
		return b.refuse(inner.why)
	}
	b.x.ops[el] = op
	*b.ops = append(*b.ops, op)
	return true
}

// dotPattern reports whether p is the pattern ".", which matches every
// item (§5.5.2) and reads nothing of it: motionless (§19.8.10), decided on
// any view (patternClasses).
func dotPattern(p *Pattern) bool {
	return strings.TrimSpace(p.src) == "." && len(p.alts) == 0 && len(p.general) == 1
}

// copiedPopulation reports whether op is a grouping whose grounded
// population is nodes: copies the select makes (copy-of, snapshot), which
// the tree makes too, so holding them is not the memory rule's concern
// (the tree holds the document besides).
func copiedPopulation(op *streamOp) bool {
	return op.groundPop && !op.heldPop && op.split != nil && op.split.class() == "nodes"
}

// numberCopies is what the tree's evaluation of a path select does to
// the copies a grouping's population is (Order/copy-of()): its result is
// sorted, which numbers each copy's tree in sequence order (fragments are
// numbered when first compared, xdm.SortDocumentOrder), and so fixes how
// the copies order against one another before the body compares them.
// The split hands the same copies over in the same order, unsorted.
func numberCopies(op *streamOp, seq xdm.Sequence) xdm.Sequence {
	if _, ok := op.sel.Expr().(*xpath.PathExpr); !ok {
		return seq
	}
	for _, it := range seq {
		if _, ok := it.(*xdm.Node); !ok {
			return seq
		}
	}
	return xdm.SortDocumentOrder(seq)
}

// groundedSelect reports whether the analysis assessed the select of el, an
// xsl:for-each-group, as grounded: a population of copies or values
// (§19.8.4.19's first rule), which the tree's grouping takes whole.
func (b *streamBuilder) groundedSelect(el *xdm.Node) bool {
	root := b.pl.root(planSite{el, el.AttrValue("select")})
	if root == nil {
		return false
	}
	r := b.pl.rec(root)
	return r != nil && r.known && !r.unverified && r.props.posture == postureGrounded
}

// groundedGroup plans an xsl:for-each-group whose population is grounded:
// its select is read off the cursor, and the grouping -- any of its forms,
// group-by and xsl:sort too -- runs on the tree's code over the items (the
// design's "group-by over grounded items"), atomic values (noteStreamGroup).
func (b *streamBuilder) groundedGroup(el *xdm.Node, f vfocus) bool {
	for a := range el.Attrs() {
		n := a.Name()
		switch {
		case n.URI != "":
		case n.Local == "collation":
			// Evaluated in the instruction's focus, the context.
			if !b.avt(el, a.Value(), f) {
				return false
			}
		case n.Local == "select", n.Local == "group-by", n.Local == "group-adjacent",
			n.Local == "group-starting-with", n.Local == "group-ending-with", n.Local == "composite":
			// Of the items: tree code over values and copies.
		case !streamAttrsOK[n.Local]:
			return b.refuse("attribute " + n.Local + " of " + elName(el))
		}
	}
	for ch := range el.Children() {
		if !isXSL(ch, "sort") {
			continue
		}
		// A sort's attribute value templates are evaluated in the
		// instruction's focus; its key, of each group's items.
		for a := range ch.Attrs() {
			if a.Name().URI == "" && a.Name().Local != "select" && !b.avt(ch, a.Value(), f) {
				return false
			}
		}
	}
	op := &streamOp{x: b.x, el: el, doc: f == vNone, group: true, groundPop: true, inGroup: b.grp, key: b.key}
	b.x.ops[el] = op
	*b.ops = append(*b.ops, op)
	b.kinds[streamtrace.KindSelect]++
	return true
}

// noteStreamGroup records the compiled form of n, an xsl:for-each-group of
// the plans being compiled, and splits its select.
func (c *compiler) noteStreamGroup(n *xdm.Node, instr *forEachGroupInstr, ns xpath.NamespaceResolver) {
	for _, op := range c.streamOps(n) {
		if !op.group {
			continue
		}
		op.instr, op.sel = instr, instr.sel
		if op.groundPop {
			// A select op: the tree groups its value, values or copies (a
			// grounded select). Copies are in trees of their own, which
			// order (current-group()/@a sorts by it) as they were made:
			// fn:copy-of numbers each tree as it makes it (noteCopy), in
			// tree mode as here, so they order alike.
			if op.split, op.dead = c.splitFor(instr.sel, ns, op.doc, false, op.inGroup, op.key); op.dead != "" {
				continue
			}
			if c := op.split.class(); c != "output" && c != "bounded" && c != "nodes" {
				op.dead = "grouping population of nodes (" + c + ")"
				continue
			}
			op.x.byInstr[instr] = op
			continue
		}
		if op.dead = groupRefusal(instr); op.dead != "" {
			continue
		}
		op.keyAttr = keyAttribute(instr, ns)
		if !op.fromGroup {
			if op.split, op.dead = c.splitFor(instr.sel, ns, op.doc, false, false, op.key); op.dead != "" {
				continue
			}
			if op.dead = populationRefusal(op.split); op.dead != "" {
				continue
			}
		}
		op.x.byInstr[instr] = op
	}
}

// keyAttribute is the attribute a group-adjacent key is, when it is one by
// name (@name, not composite): read off the start tag, without a view or an
// XPath evaluation, it is what the expression would return.
func keyAttribute(instr *forEachGroupInstr, ns xpath.NamespaceResolver) *xdm.QName {
	if instr.groupAdjacent == nil || instr.composite || instr.groupAdjacent.CompatMode() {
		return nil
	}
	e := reparse(instr.groupAdjacent, ns)
	if pe, ok := e.(*xpath.PathExpr); ok && !pe.Root && len(pe.Steps) == 1 {
		e = pe.Steps[0]
	}
	st, ok := e.(*xpath.Step)
	if !ok || st.Axis != xpath.AxisAttribute || len(st.Predicates) > 0 {
		return nil
	}
	if nt, ok := st.Test.(*xpath.NameTest); ok && !nt.AnyURI && !nt.AnyLocal {
		return &nt.Name
	}
	return nil
}

// groupRefusal says why the compiled instr cannot group a streamed
// population, "" when it can.
func groupRefusal(instr *forEachGroupInstr) string {
	switch {
	case instr.groupBy != nil:
		return "group-by over a streamed population"
	case len(instr.sorts) > 0:
		return "xsl:sort over a streamed population"
	}
	for _, p := range []*Pattern{instr.groupStartsWith, instr.groupEndsWith} {
		if p == nil {
			continue
		}
		if _, why := patternClasses(p); why != "" {
			return why
		}
	}
	return ""
}

// populationRefusal says why s, the split of a grouping's select, is not a
// population popWalk reads: a path of child steps to elements, predicates
// decided on the candidate's view.
func populationRefusal(s *streamSplit) string {
	if !s.bare() || s.group || s.outermost || s.innermost || s.hasBranches() {
		return "grouping population is not a path of child steps"
	}
	for _, st := range s.steps {
		if leaf, elem := testKinds(st.test); st.axis != xpath.AxisChild || leaf || !elem {
			return "grouping population is not a path of child steps to elements"
		}
		for _, p := range st.preds {
			if p.kids {
				return "grouping population: a predicate reads has-children()"
			}
		}
	}
	return ""
}

// readyGroup readies a grouping op whose context element is at depth minD or
// deeper, its body ops at the depth of the population's items.
func (x *streamExec) readyGroup(op *streamOp, minD int, kinds *[streamtrace.NumKinds]int64) string {
	k := minD // a current group's members are where its first one is
	if !op.fromGroup {
		k += op.split.minDepth()
	}
	if why := x.readyOps(op.inner, k, &op.kinds); why != "" {
		return why
	}
	kinds[streamtrace.KindGroup]++
	for i := range kinds {
		kinds[i] += op.kinds[i]
	}
	return ""
}

// ctxDepth is the least depth of the context of the split of op, a
// consuming instruction whose own context is at depth minD or deeper: the
// stand-in above the current group's members for a split of current-group().
func (op *streamOp) ctxDepth(minD int) int {
	if op.split != nil && op.split.group {
		return minD - 1
	}
	return minD
}

// --- Running --------------------------------------------------------------

// groupAt is where the ops of a group's body read the group: nil unless op
// is in the body of the group running.
func (run *streamRun) groupAt(op *streamOp) *groupAt {
	if op.inGroup {
		return run.grp
	}
	return nil
}

// traceGroup, when set, is told of each streamed grouping run, for the
// fuzzers' tallies.
var traceGroup func()

// group runs i, a grouping op of the plan running, on the cursor.
func (run *streamRun) group(rt *runtime, out *outputBuilder, i *forEachGroupInstr, op *streamOp) error {
	if run.used || op.fromGroup && run.grp == nil {
		return errStreamPlan
	}
	run.used = true
	if traceGroup != nil {
		traceGroup()
	}
	g := &groupRun{sr: run, rt: rt, i: i, keyAttr: op.keyAttr}
	if op.fromGroup {
		g.src = run.grp.src
	} else {
		w := &popWalk{s: op.split, d: run.d}
		w.r = splitRun{s: op.split, ctx: rt.ctx, c: run.c}
		w.filt = xdmstream.Filter{Accept: op.split.accept, Depth: run.d}
		w.insts = []*splitInst{op.split.inst(0)}
		g.src, g.walk = w, w
	}
	if i.groupAdjacent != nil {
		coll, err := i.resolveCollation(rt)
		if err != nil {
			return g.settle(err, 2)
		}
		g.coll = coll
	}
	return g.run(out)
}

// popWalk is a grouping's population: the matches of s, a path of child
// steps from the context at depth d (0: the document node).
type popWalk struct {
	s    *streamSplit
	r    splitRun // the predicates' context and cursor
	d    int
	filt xdmstream.Filter
	// insts[j] is the context of step j: the context's, then that of each
	// element open below it that matched the steps before.
	insts []*splitInst
	at    bool // the item handed over last is at its start tag
	done  bool
	// predErr is the error a predicate raised, the select's.
	predErr error
}

func (p *popWalk) next(read bool) (xdmstream.Event, bool, error) {
	if p.done {
		return xdmstream.Event{}, false, nil
	}
	skip := p.at && !read
	p.at = false
	c := p.r.c
	for {
		var (
			ev  xdmstream.Event
			err error
		)
		if skip {
			ev, err = c.SkipTag(&p.filt)
		} else {
			ev, err = c.NextTag(&p.filt)
		}
		skip = false
		if err != nil {
			return ev, false, err
		}
		if ev.Kind == xdmstream.EndDocument || ev.Kind == xdmstream.EndElement && ev.Depth == p.d {
			p.done = true
			return xdmstream.Event{}, false, nil
		}
		if ev.Kind != xdmstream.StartElement {
			continue
		}
		// What was open at the event's depth or below has closed (the
		// filter reports end tags only at the context's depth).
		e := ev.Depth - p.d
		if len(p.insts) > e {
			p.insts = p.insts[:e]
		}
		j := e - 1 // the step it may match
		if j != len(p.insts)-1 {
			skip = true // below an element that matched nothing
			continue
		}
		st := &p.s.steps[j]
		ok := st.test.Matches(ev.Node, xdm.KindElement)
		if ok {
			cand := splitAt{r: &p.r, n: ev.Node}
			if ok, err = cand.preds(st, p.insts[j]); err != nil {
				p.predErr = err
				return ev, false, err
			}
		}
		switch {
		case !ok:
			skip = true
		case j+1 == len(p.s.steps):
			p.at = true
			return ev, true, nil
		default:
			p.insts = append(p.insts, p.s.inst(j+1))
		}
	}
}

// groupItem is an item of the population, classified: its event, view, and
// for group-adjacent its key (kept and collated), for the patterns whether
// it matches the one given.
type groupItem struct {
	ev           xdmstream.Event
	view         *xdm.Node
	key          xdm.Sequence
	keyStr       string
	starts, ends bool
}

// ready makes the view of it, an item opening a group, the cursor still at
// its start tag; and the key it keeps (current-grouping-key()) its own
// string, not one into the parse's text, which the key read off the start
// tag points into.
func (it *groupItem) ready(c *xdmstream.Cursor) error {
	if it.view != nil {
		return nil
	}
	v, err := c.View()
	if err != nil {
		return err
	}
	it.view = v
	for k, a := range it.key {
		switch v, _ := a.(*xdm.Atomic); {
		case v == nil:
		case v.Type == xdm.TypeUntypedAtomic:
			it.key[k] = xdm.NewUntypedAtomic(strings.Clone(v.String()))
		case v.Type == xdm.TypeString:
			it.key[k] = xdm.NewString(strings.Clone(v.String()))
		}
	}
	return nil
}

// groupRun is one execution of a streamed grouping: the population (src),
// and, as a memberSource itself, the members of the group whose body runs.
type groupRun struct {
	sr   *streamRun
	rt   *runtime
	i    *forEachGroupInstr
	src  memberSource
	walk *popWalk // src, when it is the select's
	coll xpath.Collation
	// keyAttr is the group-adjacent key's attribute, read off the start tag
	// (keyAttribute).
	keyAttr *xdm.QName
	pos     int // the population's items classified
	// The current group: its first item, the members handed over, whether
	// it has ended and how -- at an item of the next group (pend), at the end
	// of the population (done), or after a member that matches
	// group-ending-with (endAfter; owed is then what next was told of it).
	cur      groupItem
	handed   int
	closed   bool
	endAfter bool
	owed     bool
	pend     *groupItem
	done     bool
	// keyErr is the error a key or pattern raised.
	keyErr error
}

// classify reads what the grouping needs of the item at ev, its view's; an
// item whose key is an attribute gets its view only once it opens a group
// (view).
func (g *groupRun) classify(ev xdmstream.Event) (groupItem, error) {
	g.pos++
	it := groupItem{ev: ev}
	if g.keyAttr != nil {
		var vals xdm.Sequence
		if a := ev.Node.Attr(g.keyAttr.URI, g.keyAttr.Local); a != nil {
			vals = xdm.One(a)
		}
		var err error
		if it.key, it.keyStr, err = adjacentKeyFrom(g.rt, vals, g.coll, false); err != nil {
			g.keyErr = err
		}
		return it, err
	}
	v, err := g.sr.c.View()
	if err != nil {
		return groupItem{}, err
	}
	it.view = v
	switch i := g.i; {
	case i.groupAdjacent != nil:
		it.key, it.keyStr, err = adjacentKeyOf(g.rt, v, g.pos, 0, i.groupAdjacent, g.coll, i.composite)
	case i.groupStartsWith != nil:
		it.starts, err = i.groupStartsWith.Matches(v, g.rt.clearRegexGroups().ctx)
	case i.groupEndsWith != nil:
		it.ends, err = i.groupEndsWith.Matches(v, g.rt.clearRegexGroups().ctx)
	}
	if err != nil {
		g.keyErr = err
	}
	return it, err
}

// next implements memberSource: the members of the current group.
func (g *groupRun) next(read bool) (xdmstream.Event, bool, error) {
	switch {
	case g.closed:
		return xdmstream.Event{}, false, nil
	case g.handed == 0:
		g.handed = 1
		return g.cur.ev, true, nil
	case g.endAfter:
		g.closed, g.owed = true, read
		return xdmstream.Event{}, false, nil
	}
	ev, ok, err := g.src.next(read)
	if err != nil {
		return ev, false, err
	}
	if !ok {
		g.closed, g.done = true, true
		return ev, false, nil
	}
	it, err := g.classify(ev)
	if err != nil {
		return ev, false, err
	}
	in := true
	switch {
	case g.i.groupAdjacent != nil:
		in = it.keyStr == g.cur.keyStr
	case g.i.groupStartsWith != nil:
		in = !it.starts
	}
	if !in {
		g.closed, g.pend = true, &it
		return xdmstream.Event{}, false, nil
	}
	g.handed++
	g.endAfter = it.ends
	return ev, true, nil
}

// errStreamGroup is a group's body that read some of the group and left
// the rest: no consumer does (a split reads its context through).
var errStreamGroup = errors.New("streaming: a current group read in part")

// run reads the population, running the body for each group as it starts.
func (g *groupRun) run(out *outputBuilder) error {
	ev, ok, err := g.src.next(true)
	if err != nil {
		return g.settle(err, g.phase(err))
	}
	if !ok {
		return nil
	}
	it, err := g.classify(ev)
	if err != nil {
		return g.settle(err, 3)
	}
	keyed := g.i.groupBy != nil || g.i.groupAdjacent != nil
	run := g.sr
	for n := 1; ; n++ {
		if err := g.rt.ctx.Err(); err != nil {
			return err
		}
		if err := it.ready(run.c); err != nil {
			return err
		}
		g.cur, g.handed, g.closed, g.endAfter, g.pend = it, 0, false, it.ends, nil
		// The body's focus is the group's first item, as the tree's; the
		// group itself is read by the body's consumer, through g, and
		// current-group() anywhere else is a posture fault (streamGroupMark).
		sub := g.rt.withCurrent(it.view, n, 0).clearCurrentRule()
		sub = sub.withGroupingScope(streamGroupMark, it.key)
		sub = sub.withGroupingKeyPresence(keyed)
		d, used, grp := run.d, run.used, run.grp
		run.d, run.used, run.grp = it.ev.Depth, false, &groupAt{src: g, d: it.ev.Depth, view: it.view}
		err := execSequence(g.i.body, sub, out)
		read := run.used
		run.d, run.used, run.grp = d, used, grp
		if err != nil {
			return g.settle(err, g.phase(err))
		}
		// What the body did not read of its group is passed over.
		switch {
		case g.closed:
		case g.handed > 0:
			return errStreamGroup
		default:
			g.handed = 1 // the first item: read, or not, by the body
			for !g.closed {
				if _, _, err := g.next(read); err != nil {
					return g.settle(err, g.phase(err))
				}
				read = false
			}
		}
		switch {
		case g.pend != nil:
			it = *g.pend
		case g.done:
			return nil
		default: // ended after a member matching group-ending-with
			ev, ok, err := g.src.next(g.owed)
			if err != nil {
				return g.settle(err, g.phase(err))
			}
			if !ok {
				return nil
			}
			if it, err = g.classify(ev); err != nil {
				return g.settle(err, 3)
			}
		}
	}
}

// phase is where err comes from, in the tree's order of evaluation: 1 the
// select's predicates, 3 a key or pattern, 4 anything else -- the body, or
// the document (a parse error, which reading on meets again).
func (g *groupRun) phase(err error) int {
	switch {
	case g.walk != nil && g.walk.predErr == err:
		return 1
	case g.keyErr == err:
		return 3
	}
	return 4
}

// settle returns the error the tree raises when the stream met err, of
// phase p (see phase; 2 is the collation): the tree evaluates the select
// whole first, then the collation, then every key, then the bodies. So the
// rest of the population is read on, its predicates evaluated (they are
// the select's) and, after a body's error, its keys, and the first error in
// that order is the tree's.
func (g *groupRun) settle(err error, p int) error {
	if p <= 1 {
		return err
	}
	g.keyErr = nil
	for {
		ev, ok, e := g.src.next(true)
		if e != nil {
			return e // a predicate's, or the document's
		}
		if !ok {
			return err
		}
		if p > 3 {
			if _, e := g.classify(ev); e != nil {
				p, err = 3, e // the first key error, after err's body
			}
		}
	}
}

// streamGroupMark is the current group a streamed group's body is given:
// its consumer reads the group through groupRun, so current-group()
// evaluated anywhere else is a defect of the plan, which the posture guard
// reports (rtfuncs.go) rather than answering with a wrong group.
var streamGroupMark = xdm.Sequence{xdm.NewString("streamed current group")}

// streamGroupFault is current-group()'s answer to streamGroupMark.
func streamGroupFault(seq xdm.Sequence) {
	if len(seq) == 1 && len(streamGroupMark) == 1 && seq[0] == streamGroupMark[0] {
		streamtrace.Guard()
		panic(&streamtrace.PostureError{Op: "current-group()", Kind: "group"})
	}
}
