package xslt

// A copy-of, or a sequence in element content, whose select is a choice of
// paths (lane L3: streamable-084/086, "if (lang('en')) then Address else
// ..."): the condition is motionless, evaluated on the context's view
// first, and the chosen branch's matches are copied a match at a time as
// they are read (op.copies), as a select that is one path is. Collecting
// them instead (memory's "collects grounded copies") held every match of
// the document element's content.

import (
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// copiesChoice reports whether s, a choice split of a copying select whose
// context element is at depth minD or deeper, may hand each branch's items
// over as they are read: every branch is a path whose matches are copied
// whole and lie below the document element, or the empty sequence.
func copiesChoice(s *streamSplit, minD int) bool {
	if s.kids {
		return false // has-children(.) beside the content: Peek is not taken here
	}
	if s.cond != nil {
		return copiesChoice(s.then, minD) && copiesChoice(s.els, minD)
	}
	if s.steps == nil && !s.ctxItem {
		e, ok := s.outer.Expr().(*xpath.SequenceExpr)
		return ok && len(e.Items) == 0
	}
	c := s.class()
	return (c == "nodes" || c == "subtree") && s.red == "" && !s.ctxItem && !s.group && !s.noRoot &&
		(isVarRef(s.outer.Expr(), splitItems) || copiedItems(s.outer.Expr())) && minD+s.minDepth() >= 2
}

// choose returns the branch of s that its conditions choose, evaluated as
// evalAt evaluates them: on the view of the context at depth d, the cursor
// at its start tag, or on the document node's stand-in (d == 0). s is
// returned when it is not a choice.
func (s *streamSplit) choose(ctx *xpath.Context, c *xdmstream.Cursor, d int) (*streamSplit, error) {
	if s.cond == nil {
		return s, nil
	}
	ctx = ctx.HoldItemBudget().HoldByteBudget()
	octx := ctx.WithFocus(xdm.NewTree().Root, 1, 1)
	if d > 0 {
		v, err := c.View()
		if err != nil {
			return nil, err
		}
		octx = ctx.WithFocus(v, 1, 1)
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
	return s, nil
}
