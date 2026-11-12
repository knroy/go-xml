package xslt

// Grouping a streamed population by key or sorting it (lane J1).
//
// §19.8.4.19 makes an xsl:for-each-group with group-by or xsl:sort over a
// streamed population streamable only as a child of xsl:fork (elsewhere it
// is free-ranging, and no plan is built). Its groups are not contiguous in
// the document: the population is held, as grounded copies of its members
// that are the originals to identity and order (GroundLater), and the
// tree's grouping runs over them as over a grounded population
// (groundedGroup). What is held is the split's: at the document node under
// the cap of streamhold.go (past it, the tree answers); below it, the memory
// rule's (s.memory): no copies of the document element's children.

import "github.com/knroy/go-xml/v2/xdm"

// heldGroup plans el, a grouping by key or with a sort over a streamed
// population, as a grounded one whose population the split holds.
func (b *streamBuilder) heldGroup(el *xdm.Node, f vfocus) bool {
	if !b.groundedGroup(el, f) {
		return false
	}
	b.x.ops[el].heldPop = true
	return true
}
