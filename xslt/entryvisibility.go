package xslt

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
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
//
// The rule is confined to a real xsl:package, for the reason
// eligibleInitialTemplate and the mode rule are: visibility is a property of a
// COMPONENT of a package, and a plain xsl:stylesheet has no package boundary
// for anything to be private with respect to. Applying the private default
// outside one makes every function a stylesheet declares unreachable from its
// own xsl:evaluate, which is not a boundary the author drew -- they simply did
// not write an attribute that has nothing to govern.
//
// This is also what the reference implementation does: Saxon's own XSLT 3.0
// results report evaluate-045 as "wrongError", so no released processor
// enforces the default outside a package, and the real stylesheets that drive
// xsl:evaluate from data -- DocBook xslTNG calls its own fp: functions from
// every one of its 613 test documents -- depend on that reading. Inside an
// xsl:package the declared visibility is still honoured exactly as before.
func (s *Stylesheet) evaluateMayCall(name xdm.QName, arity int) bool {
	if !s.isPackage {
		return true
	}
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

// packageScopedLibrary is the function library the runtime evaluates against.
//
// It resolves an ordinary call exactly as the flat library does -- 3.6.3.4
// puts every non-hidden function of the assembled stylesheet in the static
// context of every expression -- and answers a DYNAMIC reference by 3.6.3.5
// instead: "the set of components that are available to be referenced are
// those that are declared in the package where this function call appears,
// including components declared within an xsl:override declaration in that
// package, but excluding components declared with visibility='abstract'. If
// the relevant component has been overridden in a different package, the
// overriding declarations are not considered."
//
// The two answers genuinely differ, which is why the wrapper exists rather
// than the flat library being narrowed: a call to f:add() written in a used
// package resolves to whatever the assembly settled on, while
// function-lookup(QName(...,'add'), 2) written in the same place resolves to
// that package's own declaration.
type packageScopedLibrary struct {
	inner xpath.FunctionLibrary
	sheet *Stylesheet
}

func (p packageScopedLibrary) Lookup(name xdm.QName, arity int) (xpath.Function, bool) {
	return p.inner.Lookup(name, arity)
}

// LookupFrom implements xpath.ScopedFunctionLibrary.
//
// 3.6.3.4 puts in a package's static context "the components of the packages
// it uses that are visible to it", which for a function means public, final or
// abstract. A PRIVATE function of a used package is not among them, so a call
// to it written in the using package resolves to nothing and is XPST0017 --
// use-package-003 calls p:f-private from the package that uses
// use-package-base-001 and expects exactly that.
//
// The flat library cannot express this on its own, because the same
// declaration must stay reachable from INSIDE the package that wrote it:
// use-package-base-001's public p:f and final p:f-final both call the private
// p:f-private, and use-package-001 and -002 require those to work. Pruning the
// component would break them, which is why referencedWithin keeps it. The
// answer therefore has to depend on the caller, and it does: the package a
// call was written in rides on the context as hostPackage.
//
// Only a call from a package that does NOT declare the function is filtered.
// A package's own private functions are fully visible to it -- privacy is a
// boundary between packages, not within one -- so the declaring package's own
// calls take the flat library's answer unchanged.
func (p packageScopedLibrary) LookupFrom(
	ctx *xpath.Context, name xdm.QName, arity int,
) (xpath.Function, bool) {
	if p.sheet == nil ||
		!p.sheet.functionHiddenFrom(packageOf(ctx), name, arity) {
		return p.inner.Lookup(name, arity)
	}
	return xpath.Function{}, false
}

// functionHiddenFrom reports whether a call written in pkg may not see the
// stylesheet function of this name and arity.
//
// It says yes only when the name is declared somewhere and EVERY package that
// declares it both is not pkg and gives it a visibility 3.6.3.4 withholds --
// private or hidden. One visible declaration anywhere is enough to bind the
// call, because the flat library holds a single entry per name and arity and
// that entry is the one the call would reach; refusing it because some other
// package also declares the name privately would take away a function the
// caller is entitled to.
//
// A declaration in pkg's own set settles it immediately: privacy is a boundary
// between packages, never within one, so a package sees everything it declares
// whatever visibility it wrote. use-package-base-001's public p:f calls its own
// private p:f-private, which use-package-001 requires to work.
//
// It is confined to a real xsl:package, for the reason evaluateMayCall is
// confined to one: visibility is a property of a COMPONENT of a package, and
// a plain xsl:stylesheet has no package boundary for anything to be private
// with respect to. The private DEFAULT of 3.5.2 would otherwise make every
// function a stylesheet declares uncallable from its own expressions.
//
// A name no package declares is not a stylesheet function at all -- it is a
// builtin, which this rule says nothing about -- so it stays visible.
func (s *Stylesheet) functionHiddenFrom(pkg int, name xdm.QName, arity int) bool {
	if !s.isPackage || s.pkgFuncVis == nil {
		return false
	}
	key := functionVisibilityKey(name, arity)
	declared := false
	for p, byName := range s.pkgFuncVis {
		vis, ok := byName[key]
		if !ok {
			continue
		}
		if p == pkg {
			return false
		}
		declared = true
		if vis != "private" && vis != "hidden" {
			return false
		}
	}
	return declared
}

// LookupDynamic implements xpath.DynamicFunctionLibrary.
//
// A name the stylesheet does not declare at any arity is not a stylesheet
// function at all -- it is a builtin, which 3.6.3.5 says nothing about -- so
// it falls through to the flat library. Only a name the stylesheet does
// declare is subject to the package rule, and then the package's own map is
// the whole answer: a declaration in another package is invisible whether it
// is private, overridden, or merely elsewhere.
func (p packageScopedLibrary) LookupDynamic(
	ctx *xpath.Context, name xdm.QName, arity int,
) (xpath.Function, bool) {
	if p.sheet == nil || p.sheet.pkgFuncs == nil ||
		!p.sheet.declaresFunction(name) {
		return p.inner.Lookup(name, arity)
	}
	fn, ok := p.sheet.pkgFuncs[packageOf(ctx)][functionVisibilityKey(name, arity)]
	return fn, ok
}

// declaresFunction reports whether any xsl:function in the stylesheet, in any
// package and at any arity, declares this name.
//
// The question is asked to tell a stylesheet function from a builtin that
// happens to share a name space with one; see LookupDynamic.
func (s *Stylesheet) declaresFunction(name xdm.QName) bool {
	prefix := name.Clark() + "#"
	for _, byName := range s.pkgFuncs {
		for k := range byName {
			if strings.HasPrefix(k, prefix) {
				return true
			}
		}
	}
	return false
}
