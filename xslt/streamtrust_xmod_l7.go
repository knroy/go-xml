package xslt

// Mode trust across xsl:import and xsl:include (lane L7,
// si-apply-imports-068..070).
//
// A module that imports or includes another is "shared" (streamtrust.go):
// nothing declared in it can be trusted, since another module may add rules
// to a mode or override a declaration. Kept as it is for every declaration
// but one: the template rules of a streamable mode. When the trust walk is
// given the stylesheet's every module (xmods), a mode is verified by walking
// the rules of ALL modules -- each must meet §6.6.4 on its own, overridden or
// not -- and its streamability is read across them by import precedence.
// Functions, attribute sets, accumulators and named templates stay
// unverified (verified() still refuses every other key while shared), so a
// rule resting on any of them fails, and its mode with it. Only a principal
// module's containers and the rule plans get xmods, and only in a
// stylesheet with no package: every other multi-module stylesheet is
// unchanged.

import (
	"slices"

	"github.com/knroy/go-xml/v2/xdm"
)

// crossModules returns the document elements of every module of the
// stylesheet -- the trees compilation reads -- when cross-module mode trust
// applies: more than one module, none a package or using one. nil
// otherwise.
func (c *compiler) crossModules() []*xdm.Node {
	if c.sheet == nil || c.sheet.source == nil {
		return nil
	}
	seen := map[*xdm.Node]bool{}
	var out []*xdm.Node
	add := func(n *xdm.Node) {
		if n != nil && n.Kind() != xdm.KindElement {
			n = firstElement(n)
		}
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	add(c.sheet.source)
	if len(out) == 0 {
		return nil
	}
	for _, v := range c.staticDone {
		add(v)
	}
	if len(out) < 2 {
		return nil
	}
	for _, r := range out {
		if isXSL(r, "package") {
			return nil
		}
		pkg := false
		walkElements(r, func(el *xdm.Node) bool {
			pkg = pkg || isXSL(el, "use-package")
			return !pkg
		})
		if pkg {
			return nil
		}
	}
	return out
}

// crossMode reports whether key is one the cross-module walk may verify
// although the module is shared: a mode's rules.
func (t *declTrust) crossMode(key any) bool {
	_, ok := key.(modeKey)
	return ok && t.xmods != nil
}

// modeRoots are the modules whose rules a mode's verification walks.
func (t *declTrust) modeRoots(root *xdm.Node) []*xdm.Node {
	if t.xmods != nil {
		return t.xmods
	}
	return []*xdm.Node{root}
}

// crossModeStreamable reads a mode's @streamable across the modules: the
// principal module's declaration wins (it has the highest import
// precedence); otherwise every declaration must agree. known false when
// none says, or they disagree.
func (t *declTrust) crossModeStreamable(clark string) (yes, known bool) {
	vals := map[bool]bool{}
	for i, r := range t.xmods {
		var here []bool
		walkElements(r, func(el *xdm.Node) bool {
			if isXSL(el, "mode") && el.Attr("", "streamable") != nil {
				for _, m := range modeNamesOf(el) {
					if m == clark {
						here = append(here, isYes(el.AttrValue("streamable")))
					}
				}
			}
			return true
		})
		if i == 0 && len(here) > 0 {
			return here[len(here)-1], true // the principal's
		}
		for _, v := range here {
			vals[v] = true
		}
	}
	if len(vals) != 1 {
		return false, false
	}
	return vals[true], true
}

// crossStreamableModes is streamableModeNames over every module, each mode
// read by crossModeStreamable.
func (t *declTrust) crossStreamableModes() map[string]bool {
	out := map[string]bool{}
	for _, r := range t.xmods {
		walkElements(r, func(el *xdm.Node) bool {
			if isXSL(el, "mode") {
				for _, m := range modeNamesOf(el) {
					if yes, known := t.crossModeStreamable(m); yes && known {
						out[m] = true
					}
				}
			}
			return true
		})
	}
	return out
}

// crossRulesPlan is rulesPlanFor's walk for a stylesheet of several
// modules (crossModules), shared by every module root.
func (c *compiler) crossRulesPlan(xm []*xdm.Node) *rulesPlan {
	if c.l7CrossDone {
		return c.l7Cross
	}
	c.l7CrossDone = true
	t := newDeclTrust(xm[0], false)
	t.xmods = xm
	modes := t.crossStreamableModes()
	if len(modes) == 0 || c.streamMayAlias(true) { // streamalias_l7.go
		return nil
	}
	t.plan = &streamPlan{recs: map[any]*planRec{}, sites: map[planSite][]planRoot{}}
	names := make([]string, 0, len(modes))
	for m := range modes {
		names = append(names, m)
	}
	slices.Sort(names)
	for _, m := range names {
		t.modeVerified(xm[0], m)
	}
	c.l7Cross = &rulesPlan{pl: t.plan, trust: t, modes: modes}
	return c.l7Cross
}
