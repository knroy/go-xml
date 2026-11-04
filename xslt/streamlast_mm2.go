package xslt

// Lane MM2: fn:last() in the body of a motionless xsl:for-each.
//
// A motionless for-each's select reads only what the focus's view has
// (ancestor-or-self::node(), @*, a variable's items): the tree's
// xsl:for-each evaluates it whole, then runs the body once per item with
// position() and last() of that sequence. last() there is the loop's,
// known before the body starts; it is not the rule's (the size of the
// apply-templates population, which the stream does not know).

import "github.com/knroy/go-xml/v2/xdm"

// loopContent checks the body of el, a motionless xsl:for-each, at an
// element's view, with fn:last() of the focus admitted.
func (b *streamBuilder) loopContent(el *xdm.Node) bool {
	old := b.last
	b.last = true
	ok := b.content(el, vElem)
	b.last = old
	return ok
}
