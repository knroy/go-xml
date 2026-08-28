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
	// A detached root -- a parentless element built by a sequence constructor
	// -- has no tree to order it, so Compare falls back to an identity number
	// stamped on the root. That number is assigned on FIRST COMPARISON, which
	// makes it a function of the order the sort happens to ask questions in
	// rather than of the order the nodes were built in. sort.SliceStable runs
	// insertion sort over a short slice and compares (1,0) before (0,1), so a
	// variable declared as="element()*" holding four sibling elements came
	// back with its first two transposed, and fn:copy-of over a node sequence
	// -- one detached tree per item -- came out in an order that depended on
	// the sort's internals rather than on the sequence.
	//
	// Numbering every detached root here, walking the sequence forwards
	// before any comparison is made, ties the number to the position the
	// caller supplied. The spec leaves the relative order of nodes in
	// different trees implementation-dependent but requires it to be stable;
	// this is what makes it so. A root already numbered keeps its number, so
	// a sequence mixing nodes from earlier sorts still compares consistently.
	numberDetachedRoots(nodes)
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
		case *ArrayItem:
			// Atomizing an array is the atomization of its members, flattened:
			// XDM 3.1 defines data([[1,2],[3,4]]) as (1,2,3,4), not as two
			// items. Falling into the default case instead passed the array
			// through as though it were an atomic value, which is what made
			// fn:data on an array answer one item.
			out = append(out, Atomize(Flatten(One(v)))...)
		case *MapItem:
			// Atomizing a map is FOTY0013, like a function item, and for the
			// same reason: it has no typed value. It is dropped here and the
			// error raised by AtomizeChecked; see the FunctionItem case.
		case *Opaque:
			// Not atomisable; see above.
		case *FunctionItem:
			// Atomising a function item is FOTY0013. This function has no
			// error return and 58 call sites, so rather than change all of
			// them it drops the item, and the error is raised where a typed
			// value is actually demanded — see Sequence.AtomizeChecked, which
			// is what the argument helpers and fn:data use.
			//
			// Dropping rather than passing through is the safe half of the
			// choice: a function item that survived here would be treated as
			// an atomic value by whatever came next.
		default:
			out = append(out, it)
		}
	}
	return out
}

// AtomizeChecked is Atomize for a caller that must report FOTY0013 rather than
// silently discard a function item.
//
// XPath 3.0 makes atomising a function item an error, not a no-op: "data(f#1)"
// and "string(f#1)" both fail, and a function item reaching an arithmetic or
// comparison operator fails there too. Atomize cannot report it, so a caller
// that is about to demand a typed value uses this instead.
func AtomizeChecked(seq Sequence) (Sequence, error) {
	for _, it := range seq {
		switch v := it.(type) {
		case *FunctionItem:
			return nil, Errorf("FOTY0013",
				"a function item (%s) cannot be atomized", v.String())
		case *MapItem:
			// A map has no typed value, so atomizing one is the same error a
			// function item gives. array:sort([map{},1]) asserts FOTY0013
			// rather than the XPTY0004 a missing key would have produced.
			return nil, Errorf("FOTY0013",
				"a %s cannot be atomized", mapArrayString(v))
		case *ArrayItem:
			// An array *can* be atomized, but only if its members can: an
			// array holding a map is FOTY0013 through the array. Flatten
			// reduces it to the sequence its members hold, which is then
			// checked the same way.
			flat := Flatten(One(v))
			if len(flat) != 1 || flat[0] != Item(v) {
				if _, err := AtomizeChecked(flat); err != nil {
					return nil, err
				}
			}
		}
	}
	return Atomize(seq), nil
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

// numberDetachedRoots assigns cross-tree identities to the roots of any
// treeless nodes in ns, in the order they appear.
//
// detachedRootID is idempotent: a root that already has an id keeps it, so
// this only fixes the order in which previously unnumbered roots are first
// seen. Roots inside a real Tree are skipped — their order comes from the
// tree id, which the parser assigned.
func numberDetachedRoots(ns []*Node) {
	for _, n := range ns {
		if n.tree != nil {
			continue
		}
		root := n
		for root.Parent != nil {
			root = root.Parent
		}
		detachedRootID(root)
	}
}
