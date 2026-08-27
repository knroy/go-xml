package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// Which components a package will let an invocation start at, section 3.5.2
// and 6.6.1.
//
// A package is a boundary, and an invocation comes from outside it. Starting a
// transform at a named template or in a named mode is therefore a reference
// across that boundary, and it binds by the same rule every other cross-package
// reference does: only a public or final component can be named. A private one
// is reachable from inside the package and nowhere else, and an abstract one
// has no body to enter at all.
//
// The two errors differ only in what was named: XTDE0040 for a template,
// XTDE0045 for a mode.

// checkEntryVisibility applies the visibility rules an xsl:template or
// xsl:mode declaration must satisfy, XTSE0020 and XTSE0500.
//
// Both are about a visibility attribute written where the declaration has no
// component to attach it to.
func checkEntryVisibility(el *xdm.Node) error {
	if isXSL(el, "mode") {
		// xsl:mode/@name is a QName and nothing else. The pseudo-names an
		// @mode attribute on a template rule admits -- "#unnamed", "#all",
		// "#default", "#current" -- are ways of referring to a mode, not
		// ways of naming one, so a declaration written with one has named
		// nothing. package-909 writes name="#unnamed".
		if n := strings.TrimSpace(el.AttrValue("name")); strings.HasPrefix(n, "#") {
			return fmt.Errorf(
				"XTSE0020: %q is not a valid value for xsl:mode/@name; "+
					"the unnamed mode is declared by omitting the "+
					"attribute", n)
		}
	}
	vis := strings.TrimSpace(el.AttrValue("visibility"))
	if vis == "" {
		return nil
	}
	switch {
	case isXSL(el, "template") && el.AttrValue("name") == "":
		// Only a named template is a component. A template rule reaches a
		// using package through the mode it belongs to, so a visibility on
		// one would have nothing to govern -- and would read as a claim
		// about the mode, which 3.5.2 gives its own declaration.
		return fmt.Errorf(
			"XTSE0500: visibility is not allowed on an xsl:template with " +
				"no name attribute")
	case isXSL(el, "mode") && el.AttrValue("name") == "":
		// The unnamed mode is private to its package and cannot be made
		// otherwise: 6.6.1 makes it publicly invocable as an entry point,
		// but there is no name by which a using package could accept or
		// expose it, so public and final say something unsayable.
		if vis != "private" {
			return fmt.Errorf(
				"XTSE0020: the unnamed mode may not be declared "+
					"visibility=%q; only private is allowed", vis)
		}
	}
	return nil
}

// recordTemplateVisibility notes the visibility of a named template, so that
// the invocation can tell whether it may be entered.
//
// A declaration with no visibility attribute defaults to private, 3.5.2. The
// default matters as much as an explicit value here: package-001a declares
// its "main" template bare and is refused for exactly that reason.
func (c *compiler) recordTemplateVisibility(el *xdm.Node, name xdm.QName) {
	if el.AttrValue("name") == "" {
		return
	}
	vis := strings.TrimSpace(el.AttrValue("visibility"))
	if vis == "" {
		vis = "private"
	}
	if c.sheet.templateVisibility == nil {
		c.sheet.templateVisibility = map[string]string{}
	}
	// A template of a used package is compiled into the same stylesheet, and
	// the using package's own declaration of the same name is compiled later
	// and must win. Recording every declaration and keeping the last one
	// gives that without a precedence comparison, because the using package's
	// modules are compiled after the packages they use.
	c.sheet.templateVisibility[name.Clark()] = vis
}

// eligibleInitialTemplate reports whether an invocation may start at the named
// template, XTDE0040.
//
// The rule is confined to a real xsl:package for the same reason the mode rule
// is: a plain xsl:stylesheet has no package boundary, so nothing can be
// outside it and every named template stays invocable. Every 2.0 stylesheet in
// the suite that names an initial template depends on that.
func (s *Stylesheet) eligibleInitialTemplate(name xdm.QName) bool {
	if !s.isPackage {
		return true
	}
	vis, ok := s.templateVisibility[name.Clark()]
	if !ok {
		return true
	}
	return vis == "public" || vis == "final"
}
