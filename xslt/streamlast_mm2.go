package xslt

// Lane MM2: fn:last() in the body of a motionless xsl:for-each.
//
// A motionless for-each's select reads only what the focus's view has
// (ancestor-or-self::node(), @*, a variable's items): the tree's
// xsl:for-each evaluates it whole, then runs the body once per item with
// position() and last() of that sequence. last() there is the loop's,
// known before the body starts; it is not the rule's (the size of the
// apply-templates population, which the stream does not know).

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

// loopContent checks the body of el, a motionless xsl:for-each, at an
// element's view, with fn:last() of the focus admitted.
func (b *streamBuilder) loopContent(el *xdm.Node) bool {
	old := b.last
	b.last = true
	ok := b.content(el, vElem)
	b.last = old
	return ok
}

// leafNext checks el when it is an xsl:next-match or xsl:apply-imports in
// the body of a rule for a text, comment, PI or attribute node (focus
// vLeaf, run off the cursor on the complete node). The tree's instruction
// hands that node, with its focus, to the next rule of the current mode:
// planRules checks every rule of that mode for the node's class, and the
// built-in rules, as for an xsl:apply-templates of it. Not at the top of the
// result, where a built-in's copy would stand for the stream's node (the
// tree keeps the source's own there).
func (b *streamBuilder) leafNext(el *xdm.Node, f vfocus) (done, ok bool) {
	if !isXSL(el, "next-match") && !isXSL(el, "apply-imports") || f != vLeaf || !b.x.rule {
		return false, false
	}
	if topOf(el) != "" {
		return true, b.refuse(elName(el) + " at the top of a leaf rule's result")
	}
	return true, b.applyAttrs(el, f) && b.withParams(el, f)
}

// copiesOnly reports whether the content of b, an xsl:variable with @as
// and no @select, is xsl:copy-of instructions alone: its nodes are the
// copies they make, new trees, never a node of the stream.
func copiesOnly(b *xdm.Node) bool {
	n := 0
	for ch := range b.Children() {
		switch ch.Kind() {
		case xdm.KindText:
			if strings.TrimSpace(ch.Value()) != "" {
				return false
			}
		case xdm.KindElement:
			if !isXSL(ch, "copy-of") {
				return false
			}
			n++
		}
	}
	return n > 0
}
