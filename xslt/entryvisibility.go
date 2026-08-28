package xslt

import (
	"fmt"
	"strconv"
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
		// 3.5.2 gives the visibility of a component two sources: "the value
		// of the visibility declaration on the declaration itself (if
		// present), and the rules given in the xsl:expose declarations of the
		// package manifest." The manifest half was missing here, so a
		// template made public only by an xsl:expose was recorded private and
		// refused as an entry point. expose-007 exposes every template with
		// names="*" and starts at "main".
		//
		// Only a declaration with no visibility attribute of its own consults
		// the manifest, which is what the sentence says: an explicit value on
		// the declaration is the one that stands, and a manifest that
		// disagrees with it is XTSE3010 rather than an override.
		if v := exposedVisibility(el); v != "" {
			vis = string(v)
		} else {
			vis = "private"
		}
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

// recordFunctionVisibility notes the visibility of a stylesheet function, so
// that the target expression of xsl:evaluate can tell whether it may call it.
//
// A declaration with no visibility attribute defaults to private, 3.5.2, and
// the default is the case that matters: 10.4.1 keeps the stylesheet's private
// functions out of the target expression's static context, and almost no
// stylesheet writes the attribute.
//
// The key carries arity because a function library is keyed by name AND
// arity, and two declarations of one name at different arities are two
// separate components with separate visibilities.
func (c *compiler) recordFunctionVisibility(el *xdm.Node, name xdm.QName, arity int) {
	vis := strings.TrimSpace(el.AttrValue("visibility"))
	if vis == "" {
		vis = "private"
	}
	if c.sheet.functionVisibility == nil {
		c.sheet.functionVisibility = map[string]string{}
	}
	// Last declaration wins, for the reason recordTemplateVisibility gives:
	// a used package's components are compiled before the using package's,
	// so the later record is the one that should stand.
	c.sheet.functionVisibility[functionVisibilityKey(name, arity)] = vis
}

// functionVisibilityKey is the key both halves of the map agree on.
func functionVisibilityKey(name xdm.QName, arity int) string {
	return name.Clark() + "#" + strconv.Itoa(arity)
}

// evaluateMayCall reports whether the target expression of xsl:evaluate may
// call the stylesheet function of this name and arity.
//
// Section 10.4.1 excludes "functions in the static context of the
// xsl:evaluate instruction that are private", which the suite draws a sharp
// line on: evaluate-006 and evaluate-045 are the same stylesheet calling the
// same f:square(5), differing only in that 006 writes visibility="public" on
// the declaration. 006 expects 25 and 045 expects XTDE3160.
//
// A name the map does not know is not a stylesheet function at all -- it is a
// builtin, which this rule says nothing about -- so it is callable.
func (s *Stylesheet) evaluateMayCall(name xdm.QName, arity int) bool {
	vis, ok := s.functionVisibility[functionVisibilityKey(name, arity)]
	if !ok {
		return true
	}
	return vis != "private" && vis != "hidden"
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

// exposedVisibility answers the visibility the containing package's xsl:expose
// declarations give a component declaration, or "" where none matches.
//
// The declaration's own package is the one whose manifest governs it, so the
// search starts at the module element the declaration is a child of. A module
// reached by xsl:include is part of the including package and its manifest is
// the includer's, but an xsl:expose may only appear as a child of xsl:package,
// so a declaration in an included module simply finds no rules here -- which
// is the same answer walking up to the package would give for a manifest that
// listed it by a name it does not have.
func exposedVisibility(el *xdm.Node) visibility {
	root := el.Parent
	if root == nil || !isXSL(root, "package") {
		return ""
	}
	comps, err := packageComponents(root)
	if err != nil {
		return ""
	}
	var target *component
	for _, comp := range comps {
		if comp.el == el {
			target = comp
			break
		}
	}
	if target == nil {
		return ""
	}
	var exposes []*exposeRule
	order := 0
	for _, ch := range root.ChildElements() {
		if !isXSL(ch, "expose") {
			continue
		}
		r, err := parseExposeRule(ch, order)
		if err != nil {
			return ""
		}
		order++
		exposes = append(exposes, r)
	}
	if len(exposes) == 0 {
		return ""
	}
	if err := applyExpose(comps, exposes); err != nil {
		return ""
	}
	// applyExpose leaves a component no rule matched at its declared
	// visibility, which for a declaration with no attribute is the private
	// default -- the same answer the caller falls back to.
	return target.vis
}
