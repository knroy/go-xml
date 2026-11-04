package xslt

import "github.com/knroy/go-xml/v2/xdm"

// asRefusesNodes reports whether the @as of n, a variable or parameter, has
// a map or an array item type: no node is an instance of it, and none is
// converted to one (a map or array test is an instance check, deep, with no
// coercion: convertAsLazy). A built-in rule's node in its value -- the
// source's own in the tree, a copy of it off the stream -- is then the
// same type error in both modes, whose message names the variable and the
// type only, and is never kept (si-fork-812).
func asRefusesNodes(n *xdm.Node) bool {
	st, err := compileSequenceType(n.AttrValue("as"), newNSResolver(n, ""))
	return err == nil && st != nil && (st.stype.IsMapTest || st.stype.IsArrayTest)
}
