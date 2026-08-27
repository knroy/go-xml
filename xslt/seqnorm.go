package xslt

import "github.com/knroy/go-xml/xdm"

// joinAdjacentAtomics is step 1 of sequence normalisation (XSLT and XQuery
// Serialization 3.1, section 2): every maximal run of adjacent atomic values
// in the result sequence becomes one text node holding their string values
// joined by a single space.
//
// The output builder already applies this rule while constructing complex
// content -- inside an element, where an atomic value has to become text
// immediately. At the top level of the result sequence the values survive as
// atomic items so that xsl:sequence can return them, so the join happens here
// instead, on the way out.
//
// It runs after insertItemSeparator, and that ordering is what makes an
// explicit item-separator win: the separator text nodes it inserts sit
// between the values, so no two atomic items are adjacent any more and no
// space is added. That includes item-separator="", whose zero-length text
// node still breaks the adjacency -- which is exactly the "no separator
// anywhere, not even between atomic values" the attribute asks for.
func joinAdjacentAtomics(seq xdm.Sequence) xdm.Sequence {
	// A run needs two items, and the common result with no adjacent pair
	// should not pay for a copy of the slice.
	if !hasAdjacentAtomics(seq) {
		return seq
	}
	out := make(xdm.Sequence, 0, len(seq))
	prevAtomic := false
	for _, it := range seq {
		a, ok := it.(*xdm.Atomic)
		if !ok {
			out = append(out, it)
			prevAtomic = false
			continue
		}
		if prevAtomic {
			out[len(out)-1].(*xdm.Node).Value += " " + a.String()
			continue
		}
		out = append(out, &xdm.Node{Kind: xdm.KindText, Value: a.String()})
		prevAtomic = true
	}
	return out
}

func hasAdjacentAtomics(seq xdm.Sequence) bool {
	prev := false
	for _, it := range seq {
		_, ok := it.(*xdm.Atomic)
		if ok && prev {
			return true
		}
		prev = ok
	}
	return false
}
