package xslt

// Streamed xsl:fork and xsl:map (full streaming, component C10; DESIGN
// section 1.7, "Fork and map"; §19.8.4.20, §19.8.4.23).
//
// Both are the one construct of §19.8 under which several operands may
// consume the same context: the prongs of a fork, the entries of a map.
//
//   - At most one prong consumes: the fork is a sequence constructor (16.1:
//     "the result can be determined by treating the content as a sequence
//     constructor"), planned as one (members), its consumer read off the
//     cursor as any other; an xsl:map is its sequence constructor, the maps
//     merged afterwards as the tree merges them.
//   - Several consume, at the document node of a container: a tee. Each
//     consuming prong runs on a cursor of its own over the one read of the
//     document's bytes (xdmstream.Tee), the prongs taking turns, and writes
//     into a builder of its own; the builders are appended in prong order
//     (the fork's) or merged in entry order (the map's). The motionless
//     prongs run as they are. Every prong's error is kept and the first in
//     prong order is the instruction's, which is the tree's: the tree runs
//     the prongs one after another, so its error is that of the first prong
//     that fails, whatever happens in the prongs after it.
//   - Several consume an element (a loop's body, a rule's): refused here;
//     a loop then runs its body on a grounded copy of each match (the burst
//     body rules, which take xsl:fork and xsl:map), which reads it as a tree.
//
// Deviation from DESIGN 1.7 (an event tee: one parse, each prong fed its
// events): each prong parses the bytes itself. The cursor's consumers --
// the split, apply, group walks -- decide per start tag to descend, skip,
// ground or read the text, and a parse takes one decision; an event tee
// would have to rebuild grounded copies, views and their identities outside
// xdm. A parse per prong gives each exactly what one cursor gives, every
// limit and error included, for the CPU of one parse per consuming prong.
//
// The prongs take turns on goroutines of their own (one runs at a time).
// Two things they share are made safe for that: the local variables stack,
// whose bindings each prong makes are set aside at the end of its turn and
// put back at the start of the next (swap); and xpath's EvalLock, which a
// goroutine holds once a host call has shared it (a function item the host
// made, reaching a prong by any route: parameters, static ones, globals,
// maps, partial applications, collections), so that the next prong, or the
// transform after the fork, would wait for it forever. So every goroutine
// lets it go when its turn may end -- the transform's before the tee, each
// prong's, and each parse's, at every switch (the cursor's Handoff, which
// the tee's cursors inherit), each prong's when it is done -- and takes it
// back when its turn goes on (xpathleaf.LockYield).

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
)

// forkOp plans el, an xsl:fork that consumes its context, focus f.
func (b *streamBuilder) forkOp(el *xdm.Node, f vfocus) bool {
	var prongs []*xdm.Node
	for ch := range el.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "",
			ch.Kind() == xdm.KindComment, ch.Kind() == xdm.KindPI:
		case ch.Kind() != xdm.KindElement || isXSL(ch, "fallback"):
			return b.refuse("xsl:fork content")
		case isXSL(ch, "sequence"), isXSL(ch, "for-each-group"):
			prongs = append(prongs, ch)
		default:
			return b.refuse("xsl:fork content")
		}
	}
	return b.prongs(el, prongs, f)
}

// mapOp plans el, an xsl:map that consumes its context. Of entries alone,
// each entry is a prong; otherwise it is its sequence constructor (§19.8.4.23).
func (b *streamBuilder) mapOp(el *xdm.Node, f vfocus) bool {
	var entries []*xdm.Node
	for ch := range el.Children() {
		switch {
		case ch.Kind() == xdm.KindText && strings.TrimSpace(ch.Value()) == "",
			ch.Kind() == xdm.KindComment, ch.Kind() == xdm.KindPI:
		case ch.Kind() == xdm.KindElement && isXSL(ch, "map-entry"):
			entries = append(entries, ch)
		default:
			b.kinds[streamtrace.KindElement]++
			return b.attrs(el, f) && b.seq(el, f)
		}
	}
	return b.attrs(el, f) && b.prongs(el, entries, f)
}

// mapEntryOp plans a consuming xsl:map-entry: its key motionless, its select
// read off the cursor (an expression op, split as it compiles:
// noteStreamParam), or its sequence constructor planned.
func (b *streamBuilder) mapEntryOp(el *xdm.Node, f vfocus) bool {
	for a := range el.Attrs() {
		switch n := a.Name(); {
		case n.URI != "":
		case n.Local == "key":
			if !b.expr(el, a.Value(), f, false) {
				return false
			}
		case n.Local == "select":
		case !streamAttrsOK[n.Local]:
			return b.refuse("attribute " + n.Local + " of xsl:map-entry")
		}
	}
	b.kinds[streamtrace.KindElement]++
	if el.Attr("", "select") == nil {
		return b.seq(el, f)
	}
	op := &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key}
	*b.ops = append(*b.ops, op)
	b.x.exprOps = append(b.x.exprOps, op)
	b.kinds[streamtrace.KindSelect]++
	return true
}

// prongs plans the prongs of el, a fork or map, at focus f.
func (b *streamBuilder) prongs(el *xdm.Node, prongs []*xdm.Node, f vfocus) bool {
	consuming := 0
	for _, p := range prongs {
		r := b.pl.rec(p)
		if r == nil || !r.known || r.unverified {
			return b.refuse("no trusted record for " + elName(p))
		}
		if r.props.sweep == sweepConsuming {
			consuming++
		}
	}
	if consuming <= 1 {
		return b.members(el, prongs, f)
	}
	if f != vNone || b.x.rule || b.grp {
		return b.refuse(elName(el) + ": several prongs consume an element")
	}
	op := &streamOp{x: b.x, el: el, doc: true, fork: true}
	for _, p := range prongs {
		var ops []*streamOp
		pb := &streamBuilder{pl: b.pl, x: b.x, ops: &ops, kinds: b.kinds, key: b.key}
		if !pb.members(el, []*xdm.Node{p}, f) {
			return b.refuse(pb.why)
		}
		if ops == nil {
			ops = []*streamOp{} // read nothing: the prong runs as it is
			if b.pl.rec(p).props.sweep == sweepConsuming {
				return b.refuse(elName(el) + ": a consuming prong with no consumer")
			}
		}
		op.prongs = append(op.prongs, ops)
	}
	b.x.ops[el] = op
	*b.ops = append(*b.ops, op)
	b.kinds[streamtrace.KindFork]++
	return true
}

// noteStreamFork records the compiled form of n, a fork or map op of the
// plans being compiled, body its compiled prongs (or entries).
func (c *compiler) noteStreamFork(n *xdm.Node, instr Instruction, body []Instruction) {
	for _, op := range c.streamOps(n) {
		if !op.fork {
			continue
		}
		op.instr = instr
		if len(body) != len(op.prongs) {
			op.dead = elName(n) + ": prongs and compiled instructions differ (use-when)"
			continue
		}
		op.x.byInstr[instr] = op
	}
}

// readyFork readies the prongs of a fork op, at the document node.
func (x *streamExec) readyFork(op *streamOp, kinds *[streamtrace.NumKinds]int64) string {
	for _, ops := range op.prongs {
		if why := x.readyOps(ops, 0, kinds); why != "" {
			return why
		}
	}
	return ""
}

// forkOf returns the fork op instr, an xsl:fork or xsl:map, runs as on the
// streamed run of rt, or nil when it runs as it is.
func (rt *runtime) forkOf(instr Instruction) *streamOp {
	f := rt.bursts
	if f == nil || f.run == nil || f.run.off {
		return nil
	}
	if op := f.run.x.byInstr[instr]; op != nil && op.fork {
		return op
	}
	return nil
}

// fork runs op's prongs, body, at the document node: the consuming ones on
// a tee of the cursor, each into a builder of its own; then appends what
// they wrote to out in prong order. The error is the first prong's, in
// prong order, that failed.
func (run *streamRun) fork(rt *runtime, op *streamOp, body []Instruction, out *outputBuilder) error {
	if run.used || run.d != 0 {
		return errStreamPlan
	}
	run.used = true
	outs := make([]*outputBuilder, len(body))
	errs := make([]error, len(body))
	var cons []int
	for k := range body {
		outs[k] = newOutputBuilder(rt)
		if len(op.prongs[k]) > 0 {
			cons = append(cons, k)
		} else {
			errs[k] = body[k].Execute(rt, outs[k])
		}
	}
	// What a prong binds is set aside between its turns, above mark, and
	// so is the base of the frame it is in.
	st := rt.localStack()
	mark, base := 0, 0
	if st != nil {
		mark = st.Len()
		base = st.SetBase(0)
		st.SetBase(base)
	}
	type held struct {
		d    *xpathleaf.Locals
		base int
	}
	saved := make([]held, len(cons))
	kept := false
	back := xpathleaf.LockYield(rt.evalLock)
	cerrs := run.c.Tee(len(cons), func(j int, c *xdmstream.Cursor) error {
		defer xpathleaf.LockYield(rt.evalLock) // done: let go, for good
		k := cons[j]
		pr := &streamRun{x: run.x, c: c, root: run.root, top: run.top, i: run.i, href: run.href,
			unread: run.unread}
		sub := *rt
		sub.bursts = &burstFeed{run: pr}
		err := body[k].Execute(&sub, outs[k])
		if err == nil && !pr.used {
			err = pr.drain()
		}
		if pr.kept {
			kept = true
		}
		return err
	}, func(j int, in bool) {
		if st == nil {
			return
		}
		if in {
			if s := saved[j]; s.d != nil {
				st.Reattach(s.d)
				st.SetBase(s.base)
				saved[j] = held{}
			}
			return
		}
		saved[j] = held{st.Detach(mark), st.SetBase(base)}
	})
	back()
	run.kept = run.kept || kept
	for j, k := range cons {
		errs[k] = cerrs[j]
	}
	for k := range body {
		if errs[k] != nil {
			return errs[k]
		}
		if err := outs[k].Refused(); err != nil {
			return err
		}
	}
	for k := range body {
		if err := appendSequence(outs[k].Sequence(), out); err != nil {
			return err
		}
	}
	return nil
}
