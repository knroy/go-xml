package xslt

// Counting instead of holding (lane g1, bucket G).
//
// An outer that reads the matches only for how many there are and where --
// count, exists, empty, boolean or not, over the matches passed through
// remove, insert-before, subsequence, head, tail, reverse, unordered,
// one-or-more, zero-or-one, exactly-one or a comma with other values --
// gives the same answer, and the same error, over any elements in their
// place. Such a split neither grounds nor views a match: it counts it
// (matchCount) and hands the outer one shared stand-in element per match,
// 16 bytes each, where the tree holds at least a record per element. No
// copy is made, nothing is capped, nothing falls back.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// splitStandIn is the element a counted match is in the outer.
var splitStandIn = xdm.NewNode(xdm.KindElement, xdm.QName{Local: "match"}, "")

// countAt makes s, a split at the document node collecting element
// matches, count them when its outer needs no more; it reports whether it
// did.
func (s *streamSplit) countAt() bool {
	if s.tail != nil || s.tailText != "" || s.mode != matchGround || s.red != "" || s.atomize {
		return false
	}
	f, ok := s.outer.Expr().(*xpath.FuncCall)
	for ok && f.Name.URI == fnNS && f.Name.Local == "data" && len(f.Args) == 1 {
		f, ok = f.Args[0].(*xpath.FuncCall) // xsl:value-of's atomization of the reducer's value
	}
	if !ok || f.Name.URI != fnNS || len(f.Args) != 1 || !splitCounts[f.Name.Local] || !s.passes(f.Args[0]) {
		return false
	}
	s.mode, s.counted = matchCount, true
	return true
}

// passes reports whether e gives the items of $items through by position
// only, with values not made of them.
func (s *streamSplit) passes(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.VarRef:
		return x.Name == splitItems
	case *xpath.SequenceExpr:
		n := 0
		for _, it := range x.Items {
			switch {
			case !refersTo(s.outer, it, splitItems):
			case s.passes(it):
				n++
			default:
				return false
			}
		}
		return n == 1
	case *xpath.FuncCall:
		if x.Name.URI != fnNS {
			return false
		}
		seq := 0 // the argument the items pass through
		switch x.Name.Local {
		case "remove", "subsequence", "head", "tail", "reverse", "unordered",
			"one-or-more", "zero-or-one", "exactly-one":
		case "insert-before":
			if len(x.Args) == 3 && refersTo(s.outer, x.Args[2], splitItems) {
				seq = 2
			}
		default:
			return false
		}
		for i, a := range x.Args {
			if i == seq {
				if !s.passes(a) {
					return false
				}
			} else if refersTo(s.outer, a, splitItems) {
				return false
			}
		}
		return len(x.Args) > 0
	}
	return false
}

// standIns is n stand-ins, for the counted matches take was given.
func standIns(n int) xdm.Sequence {
	seq := make(xdm.Sequence, n)
	for i := range seq {
		seq[i] = splitStandIn
	}
	return seq
}
