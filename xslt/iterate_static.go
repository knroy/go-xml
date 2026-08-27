package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// checkIterateStatic enforces the placement rules section 8.4 puts on
// xsl:break, xsl:next-iteration and xsl:on-completion.
//
// They are checked from the element itself rather than from xsl:iterate
// because two of the three errors are about an element that has *no*
// containing xsl:iterate at all, which an iterate-rooted walk would never
// reach.
func checkIterateStatic(el *xdm.Node) error {
	switch el.Name.Local {
	case "break", "next-iteration":
		// XTSE0010: the element is only defined as part of xsl:iterate's
		// content, so one that is not lexically inside an xsl:iterate at all
		// is an element appearing where the grammar does not allow it. A
		// template called from within the loop does not count: the rule is
		// lexical, so iterate-006 places xsl:next-iteration in a named
		// template and expects the error even though the call happens
		// inside the loop.
		it := enclosingIterate(el)
		if it == nil {
			return fmt.Errorf(
				"XTSE0010: xsl:%s may only appear within xsl:iterate",
				el.Name.Local)
		}
		// XTSE3120: and only in a tail position within the loop body.
		if !inTailPositionOf(el, it) {
			return fmt.Errorf(
				"XTSE3120: xsl:%s must be in a tail position within the "+
					"body of xsl:iterate", el.Name.Local)
		}
		if el.Name.Local == "break" {
			return checkSelectOrContent(el)
		}
		// XTSE3130: every xsl:with-param must name a parameter the innermost
		// containing xsl:iterate declares. XTSE0670 first: the duplicate is
		// reported ahead of the mismatch, as it is for xsl:call-template.
		if err := checkDuplicateWithParams(el); err != nil {
			return err
		}
		return checkNextIterationParams(el, it)

	case "on-completion":
		return checkSelectOrContent(el)

	case "iterate":
		// XTSE0580: two xsl:param children of the same xsl:iterate may not
		// share a name. The equivalent rule for a template lives with the
		// template compiler; xsl:iterate reads its parameters itself, so
		// nothing else was looking.
		seen := map[xdm.QName]bool{}
		for _, c := range el.ChildElements() {
			if !isXSL(c, "param") {
				continue
			}
			qn, ok := paramName(c)
			if !ok {
				continue
			}
			if seen[qn] {
				return fmt.Errorf(
					"XTSE0580: xsl:iterate has two parameters named %s",
					c.AttrValue("name"))
			}
			seen[qn] = true
		}
	}
	return nil
}

// checkSelectOrContent is XTSE3125: @select and a sequence constructor are
// alternatives, not a pair.
//
// Whitespace-only text is not content -- the suite indents these elements --
// but an xsl:fallback is, because the element has no fallback behaviour to
// give it.
func checkSelectOrContent(el *xdm.Node) error {
	if el.Attr("", "select") == nil || !hasSequenceContent(el) {
		return nil
	}
	return fmt.Errorf(
		"XTSE3125: xsl:%s has both a select attribute and content",
		el.Name.Local)
}

// hasSequenceContent reports whether an element's children amount to a
// sequence constructor rather than to layout whitespace.
func hasSequenceContent(el *xdm.Node) bool {
	for _, c := range el.Children {
		switch c.Kind {
		case xdm.KindElement, xdm.KindPI:
			return true
		case xdm.KindText:
			if !isWhitespaceOnly(c.Value) {
				return true
			}
		}
	}
	return false
}

func isWhitespaceOnly(s string) bool {
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
		default:
			return false
		}
	}
	return true
}

// checkNextIterationParams is XTSE3130.
func checkNextIterationParams(el, iter *xdm.Node) error {
	declared := map[xdm.QName]bool{}
	for _, c := range iter.ChildElements() {
		if !isXSL(c, "param") {
			continue
		}
		if qn, ok := paramName(c); ok {
			declared[qn] = true
		}
	}
	for _, c := range el.ChildElements() {
		if !isXSL(c, "with-param") {
			continue
		}
		qn, ok := paramName(c)
		if !ok || declared[qn] {
			continue
		}
		return fmt.Errorf(
			"XTSE3130: xsl:with-param %s names no xsl:param of the "+
				"containing xsl:iterate", c.AttrValue("name"))
	}
	return nil
}

// paramName resolves the name attribute of an xsl:param or xsl:with-param.
// The prefix is dropped from the key so that two spellings of one namespace
// compare equal.
func paramName(el *xdm.Node) (xdm.QName, bool) {
	a := el.Attr("", "name")
	if a == nil {
		return xdm.QName{}, false
	}
	qn, err := resolveQNameAttr(el, a.Value)
	if err != nil {
		return xdm.QName{}, false
	}
	return xdm.QName{URI: qn.URI, Local: qn.Local}, true
}

// enclosingIterate returns the innermost xsl:iterate el sits inside, or nil.
//
// The search stops at an element that starts a new sequence constructor
// belonging to something else -- a template, a function, an xsl:on-completion
// body -- because an xsl:break there is not part of the loop's own body even
// though the loop element is an ancestor.
func enclosingIterate(el *xdm.Node) *xdm.Node {
	for cur := el.Parent; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			return nil
		}
		if cur.Name.URI != xdm.NSXSL {
			continue
		}
		switch cur.Name.Local {
		case "iterate":
			return cur
		case "on-completion", "template", "function", "variable", "param",
			"stylesheet", "transform":
			return nil
		}
	}
	return nil
}

// inTailPositionOf reports whether el is in a tail position within the
// sequence constructor forming the body of iter, as section 8.4 defines it.
//
// The walk goes outwards from el: at each level the node must be the last
// instruction of its parent's constructor (xsl:fallback ignored), and the
// parent must be one of the constructs the definition lets a tail position
// pass through.
func inTailPositionOf(el, iter *xdm.Node) bool {
	cur := el
	for {
		parent := cur.Parent
		if parent == nil {
			return false
		}
		// An xsl:when or xsl:otherwise is a branch of its xsl:choose, not an
		// instruction in a sequence constructor, so its position among its
		// siblings says nothing: every branch of a tail-position xsl:choose
		// is itself in a tail position. The choose's own position is tested
		// on the next turn of the loop.
		if !isXSL(parent, "choose") && !isLastInstruction(parent, cur) {
			return false
		}
		if parent == iter {
			return true
		}
		if parent.Name.URI != xdm.NSXSL {
			// A literal result element ends the chain: its content is the
			// content of the element being built, not a continuation of the
			// loop body.
			return false
		}
		switch parent.Name.Local {
		case "if", "when", "otherwise", "choose", "try", "catch":
		default:
			return false
		}
		cur = parent
	}
}

// isLastInstruction reports whether child is the last instruction of parent's
// sequence constructor.
//
// xsl:fallback is ignored, as the definition of a tail position says. So are
// xsl:param and xsl:sort, which are declarations at the head of a constructor
// rather than instructions in it, and whitespace text, which the suite writes
// for indentation.
func isLastInstruction(parent, child *xdm.Node) bool {
	for i := len(parent.Children) - 1; i >= 0; i-- {
		c := parent.Children[i]
		// The element under test is never skipped as trailing matter: an
		// xsl:catch reached from inside is exactly the case the definition
		// calls a tail position within xsl:try, and skipping it here would
		// scan past it and answer no.
		if c == child {
			return true
		}
		switch c.Kind {
		case xdm.KindText:
			if isWhitespaceOnly(c.Value) {
				continue
			}
		case xdm.KindElement:
			if c.Name.URI == xdm.NSXSL {
				switch c.Name.Local {
				case "fallback", "param", "sort", "on-completion", "catch":
					continue
				}
			}
		default:
			continue
		}
		return c == child
	}
	return false
}

// checkDuplicateWithParams is XTSE0670 for xsl:next-iteration. The rule is
// the same one xsl:call-template obeys; section 8.4 refers to it by number
// rather than restating it.
func checkDuplicateWithParams(el *xdm.Node) error {
	seen := map[xdm.QName]bool{}
	for _, c := range el.ChildElements() {
		if !isXSL(c, "with-param") {
			continue
		}
		qn, ok := paramName(c)
		if !ok {
			continue
		}
		if seen[qn] {
			return fmt.Errorf(
				"XTSE0670: xsl:next-iteration has two xsl:with-param "+
					"elements named %q", c.AttrValue("name"))
		}
		seen[qn] = true
	}
	return nil
}
