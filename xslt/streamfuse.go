package xslt

// Fused fork prongs (lane G7: CPU). A fork whose reading prongs are each one
// fold over the document -- count(P), sum(P/@a), max(P/number(@a)),
// exists(P[@k = 'x']) ... -- reads the document once: one walk of the
// cursor runs every prong's split side by side, then the prongs execute one
// after another, as the tree runs them, each handed its split's value where
// its instruction asks for it. The tee (streamfork.go) parses the document
// once per reading prong; this parses it once.
//
// A prong is fused when its one consumer is an expression op whose split
// the walk can run as walk (streamsplit.go) runs it, deciding nothing but to
// descend into an element or skip it: a path of downward steps, no choice,
// innermost, outermost, leaf match or has-children test, each match counted or
// read off its view (its attributes, a motionless tail), and no variable in
// the expression, so that its predicates and tail read in the fork's
// context what they would in the prong's. Each prong's walk state is its
// own: frames, counters, its fold; an element is descended into when some
// prong's frame there is live, and a prong that would have skipped it reads
// nothing inside.
//
// Errors are the tee's. A prong whose predicate, tail or fold fails stops
// there and has that error, met where it evaluates its consumer; a parse
// error, or the context's, is every other prong's, met there too, or at its
// end when it never reads (as the tee's drain). The outer of each split is
// evaluated in the prong, where its consumer asks for it.

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// traceFused, for the tests, is told of each fused fork's walk and how
// many prongs it read for.
var traceFused func(prongs int)

// fusedValue is what the walk read for a prong's op: its split's value, or
// the error the prong meets there.
type fusedValue struct {
	op    *streamOp
	value xdm.Sequence
	err   error
	// common is the parse's error, or the context's: every reader meets it,
	// one that never reads too (as the tee's drain does).
	common error
}

// fusable reports whether op's reading prongs, cons, can share one walk of
// the cursor c.
func fusable(op *streamOp, cons []int, c *xdmstream.Cursor) bool {
	if !c.Fresh() {
		return false // the tee refuses it as it should
	}
	for _, k := range cons {
		ops := op.prongs[k]
		if len(ops) != 1 || !fusableOp(ops[0]) {
			return false
		}
	}
	return true
}

// fusableOp reports whether the walk can run p's split (see the top).
func fusableOp(p *streamOp) bool {
	s := p.split
	if p.loop || p.copies || p.apply != applyNone || p.group || p.fork || p.inGroup || p.accAfter ||
		p.dead != "" || !p.doc || p.sel == nil || len(p.sel.FreeVariables()) > 0 || s == nil {
		return false
	}
	if s.cond != nil || s.kids || s.ctxItem || s.steps == nil || s.group || s.innermost || s.outermost ||
		!s.doc || s.mode != matchCount && s.mode != matchView {
		return false
	}
	for j, st := range s.steps {
		for _, pr := range st.preds {
			if pr.kids {
				return false // has-children peeks: a decision of its own
			}
		}
		// A leaf is read (leaves) only for a step that can test one: the
		// walk passes over leaves, so none may match, nor count.
		if leaf, _ := testKinds(st.test); leaf && (j == len(s.steps)-1 || len(st.preds) > 0) {
			return false
		}
	}
	return true
}

// fusedProng is one prong's side of the walk.
type fusedProng struct {
	r    *splitRun
	stop bool // done reading: idle, or failed (err)
	err  error
}

// fuse runs the splits of the ops on the cursor, the document node their
// context, in one walk, and returns what each read.
func (run *streamRun) fuse(rt *runtime, ops []*streamOp) []fusedValue {
	ctx := rt.ctx.HoldItemBudget().HoldByteBudget()
	ps := make([]fusedProng, len(ops))
	n := len(ops)
	// frames[l*n+k] is prong k's frame at the open element at level l (0:
	// the document node); dead[l*n+k] says k does not read inside it.
	frames := make([]splitFrame, n)
	dead := make([]bool, n)
	for k, op := range ops {
		ps[k].r = &splitRun{s: op.split, ctx: ctx, c: run.c}
		op.split.reach(&frames[k], 0)
	}
	accept := func(el *xdm.Node) bool {
		for k := range ps {
			if a := ps[k].r.s.accept; !ps[k].stop && (a == nil || a(el)) {
				return true
			}
		}
		return false
	}
	filt := xdmstream.Filter{Accept: accept}
	level := 0
	// push opens level l+1 under level l, k's frame there made by mk.
	push := func(mk func(k int, pf *splitFrame) (splitFrame, bool)) {
		l := level
		for k := range n {
			f, d := splitFrame{}, true
			if !dead[l*n+k] {
				f, d = mk(k, &frames[l*n+k])
			}
			frames = append(frames[:(l+1)*n+k], f)
			dead = append(dead[:(l+1)*n+k], d)
		}
		level++
	}
	hidden := func(k int, pf *splitFrame) (splitFrame, bool) {
		f := ps[k].r.s.hidden(pf)
		return f, ps[k].stop || !f.live()
	}
	at := make([]splitFrame, n) // what each prong makes of the element
	live := make([]bool, n)
	var common error // the parse's or the context's: every reader's
	skip := false
	for common == nil {
		var ev xdmstream.Event
		var err error
		if skip {
			ev, err = run.c.SkipTag(&filt)
		} else {
			ev, err = run.c.NextTag(&filt)
		}
		skip = false
		if err != nil {
			common = err
			break
		}
		if ev.Kind == xdmstream.EndDocument {
			break
		}
		for ev.Closed > 0 && level >= ev.Closed {
			level--
		}
		for level+1 > ev.Depth {
			level--
		}
		for level+1 < ev.Depth {
			push(hidden)
		}
		if err := ctx.Err(); err != nil {
			common = err
			break
		}
		clear(at)
		clear(live)
		deeper := false
		for k := range ps {
			p := &ps[k]
			s := p.r.s
			if p.stop || dead[level*n+k] {
				continue
			}
			if p.r.done && !s.predFail && !s.tailFail {
				p.stop = true // idle: nothing left to find
				continue
			}
			pf := &frames[level*n+k]
			if s.accept != nil && !s.accept(ev.Node) {
				at[k] = s.hidden(pf)
			} else {
				cand := splitAt{r: p.r, n: ev.Node}
				f, match, err := s.enter(pf, &cand)
				if err == nil && match && !(p.r.done && !s.tailFail) {
					err = p.matched(ev)
				}
				if err != nil {
					p.stop, p.err = true, err
					continue
				}
				at[k] = f
			}
			if live[k] = at[k].live(); live[k] {
				deeper = true
			}
		}
		if !deeper {
			skip = true
			continue
		}
		push(func(k int, _ *splitFrame) (splitFrame, bool) { return at[k], !live[k] })
	}
	vals := make([]fusedValue, n)
	for k, op := range ops {
		vals[k].op, vals[k].common = op, common
		switch p := &ps[k]; {
		case p.err != nil:
			vals[k].err = p.err
		case common != nil:
			vals[k].err = common
		default:
			vals[k].value, vals[k].err = p.r.finish()
		}
	}
	return vals
}

// matched takes the match at ev's start tag, as walk does in matchCount and
// matchView.
func (p *fusedProng) matched(ev xdmstream.Event) error {
	r := p.r
	s := r.s
	r.seen++
	mode := s.mode
	if s.red == "nth" && s.tail == nil && s.tailText == "" && r.idx+1 < s.nth {
		mode = matchCount // only the nth is read
	}
	if mode == matchCount {
		return r.take(xdm.Sequence{nil})
	}
	if s.attr != nil {
		var items xdm.Sequence
		if a := ev.Node.Attr(s.attr.URI, s.attr.Local); a != nil {
			items = xdm.Atomize(xdm.One(a))
			if s.red == "" && len(items) == 1 {
				// Kept: its own string, not one into the parse's text block.
				if v, ok := items[0].(*xdm.Atomic); ok && v.Type == xdm.TypeUntypedAtomic {
					items[0] = xdm.NewUntypedAtomic(strings.Clone(v.String()))
				}
			}
		}
		return r.take(items)
	}
	v, err := r.c.View()
	if err != nil {
		return err
	}
	items := xdm.One(v)
	if s.tail != nil {
		if items, err = s.tail.Eval(r.ctx.WithFocus(v, 1, 1)); err != nil {
			return err
		}
	}
	return r.take(items)
}

// forkFused runs a fork whose reading prongs, cons, are fusable: one walk,
// then each prong in order, its op handed what the walk read for it. errs
// gets each prong's error; kept says a prong kept a leaf (streamRun.kept).
func (run *streamRun) forkFused(rt *runtime, op *streamOp, body []Instruction, cons []int,
	outs []*outputBuilder, errs []error) (kept bool) {
	ops := make([]*streamOp, len(cons))
	for j, k := range cons {
		ops[j] = op.prongs[k][0]
	}
	vals := run.fuse(rt, ops)
	if traceFused != nil {
		traceFused(len(ops))
	}
	for j, k := range cons {
		pr := &streamRun{x: run.x, c: run.c, root: run.root, top: run.top, i: run.i, href: run.href,
			unread: run.unread, pre: &vals[j], typed: run.typed}
		sub := *rt
		sub.bursts = &burstFeed{run: pr}
		err := body[k].Execute(&sub, outs[k])
		if err == nil && !pr.used {
			err = vals[j].common // it never read: as the tee's drain
		}
		errs[k] = err
		kept = kept || pr.kept
	}
	return kept
}

// preEval is evalOp's for a prong of a fused fork: the value its op's split
// read, the outer evaluated here, where the op is.
func (run *streamRun) preEval(rt *runtime, op *streamOp) (xdm.Sequence, error) {
	p := run.pre
	if op != p.op || run.used {
		return nil, errStreamPlan
	}
	run.used = true
	if p.err == errSplitGiveUp && run.i != nil {
		return run.fallback(rt, op.sel)
	}
	if p.err != nil {
		return nil, p.err
	}
	s := op.split
	octx := rt.ctx.HoldItemBudget().HoldByteBudget().WithFocus(xdm.NewTree().Root, 1, 1)
	return s.outer.Eval(octx.WithVar(s.outerVar, p.value))
}
