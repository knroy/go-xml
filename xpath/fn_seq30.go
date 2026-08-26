package xpath

import (
	"github.com/knroy/go-xml/xdm"
)

// registerSeq30Funcs adds the sequence and node functions XPath 3.0 introduces
// that need nothing from the function-item machinery.
//
// Each is marked Since XPath30, so a 2.0 expression calling one gets XPST0017
// — the same "unknown function" every other processor raises — rather than a
// working answer.
func registerSeq30Funcs(l *Library) {
	// fn:head($arg as item()*) as item()?  ==  $arg[1]
	l.registerFnSince(XPath30, "head", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args) == 0 || len(args[0]) == 0 {
			return xdm.Empty(), nil
		}
		return xdm.One(args[0][0]), nil
	})

	// fn:tail($arg as item()*) as item()*  ==  subsequence($arg, 2)
	l.registerFnSince(XPath30, "tail", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args) == 0 || len(args[0]) < 2 {
			return xdm.Empty(), nil
		}
		// A fresh slice rather than a reslice of the argument: sequences are
		// passed around by value here, and handing back an alias of the
		// caller's backing array would let a later append write into it.
		out := make(xdm.Sequence, len(args[0])-1)
		copy(out, args[0][1:])
		return out, nil
	})

	// fn:has-children() / fn:has-children($node as node()?) as xs:boolean
	//
	// The zero-argument form reads the context item, and is focus-dependent
	// where the one-argument form is not.
	l.registerFnSince(XPath30, "has-children", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		var n *xdm.Node
		if len(args) == 0 {
			// contextNodeArg rather than ContextNode: this is a function
			// argument defaulting to the context item, not an axis step, so a
			// non-node focus is XPTY0004 and not XPTY0020.
			node, err := contextNodeArg(ctx)
			if err != nil {
				return nil, err
			}
			n = node
		} else {
			// An empty sequence is false rather than an error.
			if len(args[0]) == 0 {
				return boolSeq(false), nil
			}
			it, err := args[0].Single()
			if err != nil {
				return nil, err
			}
			node, ok := it.(*xdm.Node)
			if !ok {
				return nil, xdm.ErrType("fn:has-children: argument is not a node")
			}
			n = node
		}
		return boolSeq(len(n.Children) > 0), nil
	})

	// fn:innermost($nodes as node()*) as node()*
	//
	// Defined as "$nodes except $nodes/ancestor::node()": every node that is
	// not an ancestor of another member.
	l.registerFnSince(XPath30, "innermost", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		nodes, err := argNodes(args, 0, "fn:innermost")
		if err != nil {
			return nil, err
		}
		if len(nodes) == 0 {
			return xdm.Empty(), nil
		}
		// The set of nodes that some member has as an ancestor. Walking each
		// member's ancestor chain once is O(n·depth) and needs no ordering,
		// where comparing every pair would be O(n²).
		ancestors := map[*xdm.Node]bool{}
		for _, n := range nodes {
			for p := parentOf(n); p != nil; p = parentOf(p) {
				if ancestors[p] {
					// This chain has already been walked to the root by an
					// earlier member; the rest of it is known.
					break
				}
				ancestors[p] = true
			}
		}
		out := make(xdm.Sequence, 0, len(nodes))
		for _, n := range nodes {
			if !ancestors[n] {
				out = append(out, n)
			}
		}
		return xdm.SortDocumentOrder(out), nil
	})

	// fn:outermost($nodes as node()*) as node()*
	//
	// Defined as "$nodes[not(ancestor::node() intersect $nodes)]": every node
	// that does not have another member as an ancestor.
	//
	// The spec notes that the apparently simpler "$nodes except
	// $nodes/descendant::node()" is wrong, because an attribute is not a
	// descendant of its parent element. Walking the ancestor chain — which
	// does include an attribute's element — avoids that trap.
	l.registerFnSince(XPath30, "outermost", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		nodes, err := argNodes(args, 0, "fn:outermost")
		if err != nil {
			return nil, err
		}
		if len(nodes) == 0 {
			return xdm.Empty(), nil
		}
		member := make(map[*xdm.Node]bool, len(nodes))
		for _, n := range nodes {
			member[n] = true
		}
		out := make(xdm.Sequence, 0, len(nodes))
		for _, n := range nodes {
			covered := false
			for p := parentOf(n); p != nil; p = parentOf(p) {
				if member[p] {
					covered = true
					break
				}
			}
			if !covered {
				out = append(out, n)
			}
		}
		return xdm.SortDocumentOrder(out), nil
	})
}

// parentOf returns the parent of a node, including an attribute's element.
func parentOf(n *xdm.Node) *xdm.Node {
	if n == nil {
		return nil
	}
	return n.Parent
}

// argNodes returns argument i as a slice of nodes, rejecting any item that is
// not one. The declared type is node()*, so a non-node is XPTY0004 rather than
// something to atomise.
func argNodes(args []xdm.Sequence, i int, fn string) ([]*xdm.Node, error) {
	if i >= len(args) || len(args[i]) == 0 {
		return nil, nil
	}
	out := make([]*xdm.Node, 0, len(args[i]))
	for _, it := range args[i] {
		n, ok := it.(*xdm.Node)
		if !ok {
			return nil, xdm.ErrType("%s: argument is not a node", fn)
		}
		out = append(out, n)
	}
	return out, nil
}
