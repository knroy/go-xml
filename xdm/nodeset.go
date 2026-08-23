package xdm

import (
	"sort"
	"sync/atomic"
)

// newCounter returns a monotonically increasing, concurrency-safe counter.
func newCounter() func() int {
	var n int64
	return func() int { return int(atomic.AddInt64(&n, 1)) }
}

// SortDocumentOrder sorts a sequence of nodes into document order and removes
// duplicates.
//
// Every path expression in XPath 2.0 returns nodes in document order with
// duplicates removed, and so do the union, intersect and except operators.
// Doing it in one place means the axis implementations can emit nodes in
// whatever order is natural for them (reverse axes emit backwards) without
// each having to re-sort.
//
// Items that are not nodes are an error at the call sites that use this, so
// they are passed through unsorted rather than silently dropped; the caller
// type-checks first.
func SortDocumentOrder(seq Sequence) Sequence {
	if len(seq) < 2 {
		return seq
	}
	nodes := make([]*Node, 0, len(seq))
	for _, it := range seq {
		n, ok := it.(*Node)
		if !ok {
			return seq
		}
		nodes = append(nodes, n)
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].Compare(nodes[j]) < 0
	})
	out := make(Sequence, 0, len(nodes))
	var prev *Node
	for _, n := range nodes {
		if prev != nil && n == prev {
			continue
		}
		out = append(out, n)
		prev = n
	}
	return out
}

// Union returns the document-ordered union of two node sequences.
func Union(a, b Sequence) Sequence {
	return SortDocumentOrder(Concat(a, b))
}

// Intersect returns the nodes present in both sequences, in document order.
func Intersect(a, b Sequence) Sequence {
	in := make(map[*Node]bool, len(b))
	for _, it := range b {
		if n, ok := it.(*Node); ok {
			in[n] = true
		}
	}
	var out Sequence
	for _, it := range a {
		if n, ok := it.(*Node); ok && in[n] {
			out = append(out, n)
		}
	}
	return SortDocumentOrder(out)
}

// Except returns the nodes of a that are not in b, in document order.
func Except(a, b Sequence) Sequence {
	in := make(map[*Node]bool, len(b))
	for _, it := range b {
		if n, ok := it.(*Node); ok {
			in[n] = true
		}
	}
	var out Sequence
	for _, it := range a {
		if n, ok := it.(*Node); ok && !in[n] {
			out = append(out, n)
		}
	}
	return SortDocumentOrder(out)
}

// Atomize converts a sequence to atomic values, replacing each node with its
// typed value. This is the fn:data() operation, applied implicitly wherever
// XPath 2.0 requires atomic operands.
//
// Every item in the result is an *Atomic. Callers rely on that: two dozen of
// them assert the type without checking, because within the data model there
// is nothing else atomisation can produce.
//
// Opaque items are the exception, and they are dropped here rather than passed
// through. They carry engine-internal state — the transform runtime, grouping
// bookkeeping — through the closed Item interface, and a stylesheet that names
// the internal namespace could reach one:
//
//	xmlns:gi="urn:goxslt:internal" ... distinct-values($gi:runtime)
//
// Passing it through made that expression panic with an interface-conversion
// error, which in a server embedding this engine is a denial of service
// triggered by stylesheet text. An Opaque has no typed value, so dropping it
// is also what the data model implies: it is not a node and not an atomic
// value, so fn:data has nothing to return for it.
func Atomize(seq Sequence) Sequence {
	out := make(Sequence, 0, len(seq))
	for _, it := range seq {
		switch v := it.(type) {
		case *Node:
			// A node whose type is a LIST type has a typed value that is a
			// SEQUENCE, one atomic value per whitespace-separated token --
			// not a single string that happens to contain spaces. XDM 3.3
			// and 6.2 both say so, and it is why
			// "count(data(@nmtokens-attr))" must answer 3 for "red green
			// blue" rather than 1, and why string-join(...,',') over such an
			// attribute yields "red,green,blue".
			//
			// Node.Atomize returns a single *Atomic and so structurally
			// cannot express this; the expansion therefore happens here, at
			// the one place that turns nodes into atomized sequences.
			if items, ok := v.AtomizeList(); ok {
				out = append(out, items...)
				continue
			}
			out = append(out, v.Atomize())
		case *Opaque:
			// Not atomisable; see above.
		default:
			out = append(out, it)
		}
	}
	return out
}

// IsXMLWhitespace reports whether s consists entirely of XML whitespace.
//
// XML defines whitespace as exactly four characters: space, tab, carriage
// return and line feed. Go's strings.TrimSpace uses unicode.IsSpace, which
// additionally matches U+00A0 (no-break space) and other Unicode separators —
// so using it to decide whether a text node is "just whitespace" silently
// deletes a &nbsp; that the author put there deliberately.
func IsXMLWhitespace(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\r', '\n':
		default:
			return false
		}
	}
	return true
}
