package xslt

// oneTake reports whether the split's matches reach the reducer in one take:
// the context item itself (min(.), max(.)), atomized as a whole at its end
// (splitRun.run). A min or max of them is folded once over all its values,
// which is the tree's min or max of the same sequence -- the same promotion,
// the same comparisons, the same error -- so it never gives up, and needs no
// tree to fall back to below the document node. It holds the context's
// values, which the tree holds as well.
func (s *streamSplit) oneTake() bool { return s.ctxItem && s.steps == nil }
