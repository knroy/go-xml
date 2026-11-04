package xslt

// A copy-of at the document node whose tail runs on a grounded copy of the
// document element, for a principal input (lane MM4, function-5004).
//
// copy-of(doc/f(section[1])/section/p[2]) is split as the path doc, with
// the rest a tail run on a grounded copy of each match (matchGround). The
// match is the document element, which the split gives up on (noRoot): for
// a container, a copy of it costs what the tree costs, and the tree answers
// (streamRun.fallback). A principal input has no tree to fall back to, so
// the main plan refused it (mayGiveUp). Grounding the document element
// there holds its records, which a tree-mode parse holds too, with the
// document node and its other children besides; the tail then runs on the
// copy as it does on the copy of any deeper match.

import (
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// rootGround reports whether op, a copy-of at the document node whose split
// gives up only at the document element (noRoot), may ground that element
// for a principal input: one path, no choice, hold, fold or group, each
// match grounded for a tail.
func rootGround(op *streamOp) bool {
	s := op.split
	_, ok := op.instr.(*copyOfInstr)
	return ok && op.copies && s != nil && s.cond == nil && s.noRoot && !s.hold && !s.nests && s.tail != nil &&
		s.tailText == "" && s.mode == matchGround && !s.group && s.fold == nil && s.red == ""
}

// loopGroundRoot is loopAt at the document node, a match that is the
// document element grounded as any other match.
func (s *streamSplit) loopGroundRoot(rt *runtime, c *xdmstream.Cursor, g *groupAt, each func(it xdm.Item) error) error {
	if err := s.runChecks(rt.ctx); err != nil {
		return err
	}
	r := &splitRun{s: s, ctx: rt.ctx, c: c, each: each, groundRoot: true}
	return r.run(nil, r.at(g, 0))
}
