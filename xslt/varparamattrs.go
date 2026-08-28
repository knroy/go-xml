package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// Which attributes xsl:variable and xsl:param may carry where, sections 9.1
// and 9.2.
//
// The element table cannot state these rules, because it maps an element name
// to one set of attributes and these two elements have a different set
// depending on where in the tree they sit. 9.2 spells the split out for
// xsl:param:
//
//	as a child of xsl:stylesheet ... all attributes except tunnel are
//	permitted
//	as a child of xsl:template ... the permitted attributes are name,
//	select, as, required, and tunnel
//
// so static is a global-only attribute and tunnel a local-only one, and each
// is an error in the other position. xsl:variable's signature carries
// visibility, which is meaningful only on a declaration a package can expose,
// and static for the same reason.
//
// The distinction between the two codes is the one XTSE0090 and XTSE0020
// always draw: XTSE0090 is for an attribute the element's signature does not
// list at all, XTSE0020 for one it lists but whose use here is wrong. The
// signature of xsl:param has no visibility in any position, so that is
// XTSE0090; it does list tunnel and static, so the misplaced one is XTSE0020.

// globalOnlyAttrs are the attributes only a top-level declaration may carry.
var globalOnlyAttrs = map[string]bool{"static": true, "visibility": true}

// checkVarParamAttrs applies the positional attribute rules to one
// xsl:variable or xsl:param.
func checkVarParamAttrs(el *xdm.Node) error {
	if el.Name.URI != xdm.NSXSL {
		return nil
	}
	if el.Name.Local != "variable" && el.Name.Local != "param" {
		return nil
	}
	// Only a module that declares 3.0 or later. These attributes are XSLT
	// 3.0's, so to a 2.0 module they are unrecognised names its own grammar
	// check has already reported, and to a module declaring a version this
	// engine does not implement forwards compatibility must let them pass.
	if !moduleAtLeast30(el) {
		return nil
	}
	global := el.Parent != nil && el.Parent.Kind == xdm.KindElement &&
		el.Parent.Name.URI == xdm.NSXSL &&
		(isStylesheetRootName(el.Parent.Name.Local) ||
			// A declaration inside xsl:override stands in for a top-level
			// declaration of the used package, so it carries the same
			// attributes -- visibility above all, since 3.5.4 requires the
			// override to restate the component's visibility.
			el.Parent.Name.Local == "override")

	for _, a := range el.Attrs {
		if a.Name.URI != "" {
			continue
		}
		switch {
		case a.Name.Local == "visibility" && el.Name.Local == "param":
			// 9.2's signature for xsl:param has no visibility in any
			// position. An earlier draft allowed it, which is what these
			// cases are testing the removal of, so the code is XTSE0090 --
			// the attribute is not one the element has -- rather than
			// XTSE0020.
			return fmt.Errorf(
				"XTSE0090: visibility is not an attribute of xsl:param")
		case a.Name.Local == "visibility" && isStaticDecl(el) &&
			a.Value != "private":
			// 9.5: "When the static attribute is present with the value yes,
			// the visibility attribute must not have a value other than
			// private." A static variable's value is used while the package
			// is compiled, so letting another package override it would make
			// independent compilation impossible.
			return fmt.Errorf(
				"XTSE0020: a static xsl:%s may not have visibility=%q; "+
					"only private is allowed", el.Name.Local, a.Value)
		case globalOnlyAttrs[a.Name.Local] && !global:
			return fmt.Errorf(
				"XTSE0020: %s is only allowed on a top-level xsl:%s, not on "+
					"one inside a %s", a.Name.Local, el.Name.Local,
				el.Parent.Name.Lexical())
		case a.Name.Local == "tunnel" && global:
			// 9.2: a stylesheet parameter is supplied by the calling
			// application rather than passed down a template chain, so
			// tunnelling has nothing to mean here.
			return fmt.Errorf(
				"XTSE0020: tunnel is not allowed on a top-level xsl:%s; "+
					"a stylesheet parameter is set by the caller",
				el.Name.Local)
		}
	}
	return nil
}

// moduleAtLeast30 reports whether the module el belongs to declares XSLT 3.0
// or later.
//
// It reads the module element rather than the nearest ancestor-or-self
// carrying a version attribute, which is what xpathVersionAt does. A version
// written on a declaration selects forwards-compatible behaviour for that
// declaration's body; it does not move the declaration into a different
// stylesheet. static-015 writes version="1.0" on a static xsl:param of a
// version="3.0" module and expects the parameter to work.
func moduleAtLeast30(el *xdm.Node) bool {
	var outermost *xdm.Node
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			continue
		}
		outermost = cur
		if cur.Name.URI == xdm.NSXSL && isStylesheetRootName(cur.Name.Local) {
			return versionAt(cur) >= 3.0
		}
	}
	// A simplified stylesheet has no xsl:stylesheet element: the module is a
	// literal result element carrying xsl:version, and section 3.7 says that
	// attribute "has the same effect as the version attribute on
	// xsl:stylesheet". Without this the walk found no module element and
	// reported 2.0, so every attribute XSLT 3.0 added was rejected in a
	// simplified stylesheet declaring xsl:version="3.0" --
	// for-each-group-073 through -075 write composite there.
	if outermost != nil {
		return versionAt(outermost) >= 3.0
	}
	return false
}
