package xslt

// An xsl:try whose body applies templates (lane L4), and the
// declarations it reads from the module: a plan is only built for a module
// that is the whole stylesheet (streamtrust.go), so they are the ones in
// force. (An xsl:try reading accumulators needs nothing here: a read raises
// its error in both modes exactly when the value depends on a failed
// computation, §18.2.5.)

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// accumDeclNamed returns the one top-level xsl:accumulator of the module
// named name, an NCName, or nil.
func accumDeclNamed(top *xdm.Node, name string) *xdm.Node {
	if !xdm.IsNCName(name) {
		return nil
	}
	var found *xdm.Node
	for ch := range top.Children() {
		if ch.Kind() == xdm.KindElement && isXSL(ch, "accumulator") && strings.TrimSpace(ch.AttrValue("name")) == name {
			if found != nil {
				return nil
			}
			found = ch
		}
	}
	return found
}

// applyCannotFail reports whether op, an xsl:apply-templates in the body of
// a streamed try, can raise no error but the built-in rules' own, at the
// node that has no rule: then the stream, dispatching the children in
// document order as the tree does, meets the tree's first error at the
// same node, and nothing of what it reads is grounded for later. op must be
// a bare apply-templates of the children in the unnamed mode, whose rules
// in the module (the whole stylesheet: streamtrust.go) are made of
// instructions that cannot fail (bodyCannotFail), with patterns whose
// predicates cannot fail.
func (op *streamOp) applyCannotFail() bool {
	el := op.el
	if op.apply != applyKids || !isXSL(el, "apply-templates") || el.NumAttrs() != 0 || hasContent(el) {
		return false
	}
	top := documentElementOf(el)
	found := false
	walkElements(top, func(e *xdm.Node) bool {
		found = e.Attr("", "default-mode") != nil || e.Attr(xdm.NSXSL, "default-mode") != nil
		return !found
	})
	if found {
		return false
	}
	modes := 0
	for ch := range top.Children() {
		if ch.Kind() != xdm.KindElement || ch.Name().URI != xdm.NSXSL {
			continue
		}
		switch ch.Name().Local {
		case "import", "include", "use-package":
			return false
		case "mode":
			if n := strings.TrimSpace(ch.AttrValue("name")); n != "" && n != "#unnamed" {
				continue
			}
			modes++
			switch strings.TrimSpace(ch.AttrValue("on-no-match")) {
			case "", "fail", "shallow-copy", "shallow-skip", "text-only-copy", "deep-skip":
			default:
				return false
			}
		case "template":
			if ch.Attr("", "match") == nil || !inUnnamedMode(ch.AttrValue("mode")) {
				continue
			}
			for a := range ch.Attrs() {
				if n := a.Name(); n.URI == "" && n.Local != "match" && n.Local != "mode" && n.Local != "priority" && n.Local != "name" {
					return false
				}
			}
			if !patternCannotFail(parseAt(ch, ch.AttrValue("match"))) || !bodyCannotFail(ch) {
				return false
			}
		}
	}
	return modes <= 1
}

// inUnnamedMode reports whether a template's mode attribute puts it in the
// unnamed mode (no default-mode in force).
func inUnnamedMode(modes string) bool {
	f := strings.Fields(modes)
	if len(f) == 0 {
		return true
	}
	for _, m := range f {
		if m == "#default" || m == "#all" || m == "#unnamed" {
			return true
		}
	}
	return false
}

// patternCannotFail reports whether a pattern, parsed as an expression,
// is a union of paths whose predicates cannot fail.
func patternCannotFail(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.BinaryOp:
		return (x.Op == "union" || x.Op == "|") && patternCannotFail(x.Left) && patternCannotFail(x.Right)
	case *xpath.PathExpr:
		for _, st := range x.Steps {
			if !isStep(st) || !tryCannotFail(st) {
				return false
			}
		}
		return true
	case *xpath.Step:
		return tryCannotFail(x)
	}
	return false
}

// bodyCannotFail reports whether the sequence constructor of el is made of
// literal text and result elements, attribute-free xsl:copy, bare
// xsl:apply-templates, xsl:value-of select=".", xsl:text, and xsl:try of
// those with catches of those: nothing that can fail.
func bodyCannotFail(el *xdm.Node) bool {
	for ch := range el.Children() {
		if !nodeCannotFail(ch) {
			return false
		}
	}
	return true
}

// nodeCannotFail is bodyCannotFail for one node of a sequence constructor.
func nodeCannotFail(ch *xdm.Node) bool {
	switch ch.Kind() {
	case xdm.KindText:
		return !strings.ContainsAny(ch.Value(), "{}")
	case xdm.KindElement:
	default:
		return true
	}
	if ch.Name().URI != xdm.NSXSL {
		for a := range ch.Attrs() {
			if a.Name().URI == xdm.NSXSL && a.Name().Local != "exclude-result-prefixes" ||
				strings.ContainsAny(a.Value(), "{}") {
				return false
			}
		}
		return bodyCannotFail(ch)
	}
	switch ch.Name().Local {
	case "copy":
		for a := range ch.Attrs() {
			if n := a.Name(); n.URI == "" && n.Local != "copy-namespaces" && n.Local != "inherit-namespaces" ||
				strings.ContainsAny(a.Value(), "{}") {
				return false
			}
		}
		return bodyCannotFail(ch)
	case "apply-templates":
		return ch.NumAttrs() == 0 && !hasContent(ch)
	case "value-of":
		return ch.NumAttrs() == 1 && strings.TrimSpace(ch.AttrValue("select")) == "." && !hasContent(ch)
	case "text":
		for t := range ch.Children() {
			if t.Kind() == xdm.KindElement {
				return false
			}
		}
		return ch.NumAttrs() == 0
	case "try":
		if ch.NumAttrs() != 0 {
			return false
		}
		for c := range ch.Children() {
			if c.Kind() == xdm.KindElement && isXSL(c, "catch") {
				for a := range c.Attrs() {
					if a.Name().URI == "" && a.Name().Local != "errors" {
						return false
					}
				}
				if !bodyCannotFail(c) {
					return false
				}
			} else if !nodeCannotFail(c) {
				return false
			}
		}
		return true
	}
	return false
}

// copyAccumsNo reports whether name is copy-accumulators="no" on el, an
// xsl:copy-of or xsl:copy (lane L4): the default, which the plan takes when
// the attribute is absent (accumulator-049s).
func copyAccumsNo(el *xdm.Node, name xdm.QName) bool {
	return name.URI == "" && name.Local == "copy-accumulators" && (isXSL(el, "copy-of") || isXSL(el, "copy")) &&
		!yesAttr(el, "copy-accumulators")
}
