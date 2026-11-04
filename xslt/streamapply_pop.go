package xslt

// xsl:apply-templates over a population (lane K3).
//
// An apply op took a select that is its stream part's matches through
// windows (windowsOnly). A population's other shapes -- a motionless choice
// between populations (if ($c) then () else P), motionless parts around the
// stream part ((P, $v), ($v, P)), an insertion (insert-before(P, 2, $v)) --
// hand items that are not the stream's as well: a variable's nodes, values.
// Those are dispatched as the tree dispatches them, off the cursor (the
// rules' bodies run on the tree's code); the stream part's matches as an
// apply op's are. streamPop.read evaluates the choice and the parts first,
// as the tree evaluates the whole select before it dispatches.
//
// Positions run over the whole population; the size is not known as it is
// read, and a verified streamable mode has no rule reading last()
// (§19.8.9.14 makes it free-ranging there), as for the stream's matches.
// Only an instruction with no xsl:with-param and no xsl:sort is taken, so
// that nothing the tree evaluates after the select comes before a part; and
// only in a 3.0 module, whose selection may hold values (no XTTE0520 to
// raise before the first item).

import "github.com/knroy/go-xml/v2/xdm"

// applyPop reports whether p, the population of the apply op compiled as
// instr, may be dispatched by executePop.
func applyPop(p *streamPop, instr Instruction) bool {
	a, ok := instr.(*applyTemplatesInstr)
	return ok && a.atomicOK && (groundApply(p) || p.values || len(a.sorts) == 0 && len(a.params) == 0 && dispatchable(p)) // streamsplit_l6.go
}

// dispatchable reports whether p hands the matches themselves, in every
// branch: no function of them, no copies, no values read off the cursor.
func dispatchable(p *streamPop) bool {
	if p.cond != nil {
		return dispatchable(p.then) && dispatchable(p.els)
	}
	return !p.values && !p.off && !p.ground && p.fn == nil
}

// executePop runs i, an apply op whose population applyPop took.
func (i *applyTemplatesInstr) executePop(rt *runtime, out *outputBuilder, run *streamRun, op *streamOp) error {
	if groundApplyOp(op) {
		return i.executeCopies(rt, out, run, op) // streamsplit_l6.go
	}
	if run.used {
		return errStreamPlan
	}
	params, tunnels, err := evalParams(rt, i.params)
	if err != nil {
		return err
	}
	// The depth is entered once the select's parts are evaluated, as the
	// tree enters it after the select.
	entered := false
	enter := func() error {
		if entered {
			return nil
		}
		entered = true
		return rt.descend()
	}
	defer func() {
		if entered {
			rt.ascend()
		}
	}()
	disp := rt
	if i.streamed {
		disp = rt.withoutGroupingScope()
	}
	mode := i.effectiveMode(rt)
	focus := focuser{disp, mode, false}
	pos := 0
	// each dispatches an item that is not the stream's (read runs it off the
	// cursor), as applyTemplatesInstr.Execute does.
	each := func(it xdm.Item) error {
		if err := enter(); err != nil {
			return err
		}
		if err := rt.ctx.Err(); err != nil {
			return err
		}
		pos++
		sub := disp.withCurrent(it, pos, 0)
		node, ok := it.(*xdm.Node)
		if !ok {
			return applyToAtomic(sub, it, mode, params, tunnels, out)
		}
		if err := rt.sheet.checkModeTyped(node, mode); err != nil {
			return err
		}
		return applyToNode(sub, node, mode, params, tunnels, out)
	}
	stream := func(_ func(xdm.Item) error, gate func() (bool, error)) error {
		if err := enter(); err != nil {
			return err
		}
		run.used = true
		return run.splitOf(op).loopAt(rt.ctx, run.c, run.d, run.groupAt(op), nil, func(v *xdm.Node, md int) (bool, error) { // streampop_l6.go
			if err := rt.ctx.Err(); err != nil {
				return false, err
			}
			if ok, err := gate(); err != nil || !ok {
				return false, err
			}
			pos++
			return run.element(focus, pos, v, md, mode, params, tunnels, out)
		})
	}
	if err := op.pop.read(run, rt, op, each, stream); err != nil {
		return err
	}
	return enter()
}
