package xslt

// Lane g5: global variables a streamed principal input may sit beside
// (bucket MP). A global's content is evaluated with the global context item
// as its focus, which TransformReader does not build: mainRefusal refuses
// any global whose value may read it. Content made only of literal text and
// literal result elements, with no expression and no value template in it,
// reads nothing.

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

// literalGlobal reports whether g, a global with content, is declared in the
// principal module with content that holds no expression.
func (s *Stylesheet) literalGlobal(g *Variable) bool {
	els := s.source.ChildElements()
	if len(els) == 0 {
		return false
	}
	var decl *xdm.Node
	for c := range els[0].Children() {
		if c.Kind() == xdm.KindElement && (isXSL(c, "variable") || isXSL(c, "param")) {
			if name, err := resolveQNameAttr(c, c.AttrValue("name")); err == nil && name == g.Name {
				if decl != nil {
					return false // an override: which one is g is not looked into
				}
				decl = c
			}
		}
	}
	if decl == nil || decl.AttrValue("select") != "" {
		return false
	}
	ok := true
	for c := range decl.Children() {
		if c.Kind() == xdm.KindElement {
			walkElements(c, func(e *xdm.Node) bool {
				ok = ok && literalContent(e)
				return ok
			})
		} else if c.Kind() == xdm.KindText && strings.ContainsAny(c.Value(), "{}") {
			ok = false
		}
	}
	return ok
}

// literalContent: a literal result element whose attributes are plain (no
// value template, no xsl: attribute) and whose text holds no brace, or an
// xsl:text.
func literalContent(e *xdm.Node) bool {
	if e.Name().URI == xdm.NSXSL {
		if e.Name().Local != "text" {
			return false
		}
		for a := range e.Attrs() {
			if a.Name().URI == "" && a.Name().Local != "disable-output-escaping" {
				return false
			}
		}
		return true
	}
	for a := range e.Attrs() {
		if a.Name().URI == xdm.NSXSL || strings.ContainsAny(a.Value(), "{}") {
			return false
		}
	}
	for c := range e.Children() {
		if c.Kind() == xdm.KindText && strings.ContainsAny(c.Value(), "{}") {
			return false
		}
	}
	return true
}
