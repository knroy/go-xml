package xslt

// apply-templates over fresh copies of the stream (lane L3: si-copy-200,
// `apply-templates select="copy-of()" mode="change"`; streamable-139,
// `apply-templates select="text() ! snapshot(.)" mode="path"`).
//
// Every item the select returns is a new node, in a tree of its own, which
// no rule can tell from the tree's copy: so the select is read off the
// cursor as a select op's (the copies made as the content goes by), and the
// templates are applied to the copies exactly as the tree applies them, with
// nothing of the stream intercepted (treeApply). The mode need not be
// streamable: its rules run on trees.
//
// copy-of(.) of the context element is the element's copy built from the
// events (copyEvents, as the document's is): the memory of that copy, which
// the tree holds beside its source.

import (
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// freshItems reports whether every item e returns is a new node or a value:
// copy-of() or snapshot() of anything, or P ! copy-of(.) / P ! snapshot(.).
func freshItems(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.FuncCall:
		return x.Name.URI == fnNS && (x.Name.Local == "copy-of" || x.Name.Local == "snapshot")
	case *xpath.SimpleMap:
		return freshItems(x.Right)
	}
	return false
}

// treeApplyOp plans el, an xsl:apply-templates whose select returns only
// fresh copies (freshItems), at focus f: as a select op whose items are then
// dispatched on the tree. It reports false, with nothing planned, for any
// other select.
func (b *streamBuilder) treeApplyOp(el *xdm.Node, f vfocus) (done, ok bool) {
	a := el.Attr("", "select")
	if a == nil || f == vLeaf {
		return false, false
	}
	if e := parseAt(el, a.Value()); e == nil || !freshItems(e) {
		return false, false
	}
	if !b.applyAttrs(el, f) || !b.withParams(el, f) {
		return true, false
	}
	op := &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key, treeApply: true}
	b.x.ops[el] = op
	*b.ops = append(*b.ops, op)
	return true, true
}

// noteTreeApply splits the select of op, a treeApply op (noteStreamApply).
func (c *compiler) noteTreeApply(op *streamOp, instr Instruction, sel *xpath.Compiled, ns xpath.NamespaceResolver) {
	op.instr, op.sel = instr, sel
	if s := elemCopySplit(sel, op.doc); s != nil {
		op.split, op.dead = s, ""
	} else if op.split, op.dead = c.splitForDoc(sel, ns, op.doc, false, op.inGroup, op.key); op.dead != "" {
		return
	}
	op.x.byInstr[instr] = op
}

// elemCopySplit is the split of copy-of() or copy-of(.) of a context
// element (not the document node: docCopySplit's): the copy built off the
// cursor (docCopyEval).
func elemCopySplit(sel *xpath.Compiled, doc bool) *streamSplit {
	f, ok := sel.Expr().(*xpath.FuncCall)
	if doc || !ok || f.Name.URI != fnNS || f.Name.Local != "copy-of" || len(f.Args) > 1 ||
		len(f.Args) == 1 && !isContextItem(f.Args[0]) {
		return nil
	}
	items := xpathleaf.WithExpr(sel, &xpath.VarRef{Name: splitItems}).(*xpath.Compiled)
	return &streamSplit{ctxItem: true, docCopy: true, outerVar: splitItems, outer: items}
}

// treeApplyFeed is the feed of op, a treeApply op, for instr.
func (run *streamRun) treeApplyFeed(instr Instruction, op *streamOp) *burstFeed {
	return &burstFeed{instr: instr, treeApply: true, keep: run,
		eval: func(rt *runtime) (xdm.Sequence, error) { return run.evalOp(rt, op) }}
}

// executeTreeApply runs i, a treeApply op of the plan running, whose feed is
// f: the select read off the cursor, then i run as the tree runs it over
// those items, with a feed that knows only them (so nothing below it reads
// the stream).
func (i *applyTemplatesInstr) executeTreeApply(rt *runtime, out *outputBuilder, f *burstFeed) error {
	if sa := rt.runAccums(f.keep); sa != nil {
		sa.keep++
		defer func() { sa.keep-- }()
	}
	seq, err := f.eval(rt)
	if err != nil {
		return err
	}
	sub := *rt
	sub.bursts = &burstFeed{instr: i, eval: func(*runtime) (xdm.Sequence, error) { return seq, nil }}
	return i.Execute(&sub, out)
}

// buildElem copies the element the cursor is at, at its start tag, into a
// parentless element, as fn:copy-of does (copyItem: deepCopy with the
// inherited namespaces, the whole typing kept).
func buildElem(rt *runtime, run *streamRun) (*xdm.Node, error) {
	b := newOutputBuilder(rt)
	if err := copyEvents(rt, b, run.c, run.d, false, nil); err != nil {
		return nil, err
	}
	if err := b.Refused(); err != nil {
		return nil, err
	}
	seq := b.Sequence()
	if len(seq) != 1 {
		return nil, errStreamPlan
	}
	return seq[0].(*xdm.Node), nil
}
