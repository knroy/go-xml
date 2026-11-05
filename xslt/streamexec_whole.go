package xslt

// Whole-document copies (lane MM6): a select that needs a grounded copy of
// the whole document element before it can make anything streams holding
// that one copy, which costs what the tree costs and saves nothing.

// wholeMatch reports whether a rule body's select whose split grounds the
// rule's match itself (f(snapshot(.)), sf-unparsed-entity-03) may run when
// the match may be the document element (minD 1): the memory rule refuses
// it there, but the copy is of one element, the document element at most
// once, holding its records as the tree does. Deeper matches the memory
// rule admits already; a group's or a choice's split stays refused.
func wholeMatch(x *streamExec, s *streamSplit, minD int) bool {
	return x.rule && minD == 1 && s.ctxItem && s.class() == "subtree" && s.cond == nil && !s.group
}
