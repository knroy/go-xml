package xslt

// A held select at the document node with at most one match (lane MM4).
//
// A split that holds what it collects (streamhold.go) gives up when the
// held records pass what a tree-mode parse holds by the latest item; at the
// document node of a principal input there is no tree to give up to, so the
// main plan refuses it (givesUp). A path that can match at most one node --
// /doc/chap[2] -- never gets there: its one match is held in one batch tree,
// the match's subtree and its ancestors, and the tree by the match's end
// holds the match's subtree and every record before it, the ancestors
// among them. So held never passes tree, and the split never gives up.

import "github.com/knroy/go-xml/v2/xpath"

// oneMatch reports whether P, from the document node, matches at most one
// node: child steps only, each the document element (a name test: the
// document node has one element child) or taking one position.
func (s *streamSplit) oneMatch() bool {
	if !s.doc || s.cond != nil || s.hasBranches() || len(s.steps) == 0 {
		return false
	}
	for j, st := range s.steps {
		if st.axis != xpath.AxisChild {
			return false
		}
		if _, ok := st.test.(*xpath.NameTest); ok && j == 0 {
			continue
		}
		one := false
		for _, p := range st.preds {
			if p.e == nil && p.pe == nil && p.at > 0 {
				one = true
			}
		}
		if !one {
			return false
		}
	}
	return true
}
