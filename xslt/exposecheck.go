package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// Static checking of a package's own xsl:expose declarations, section 3.5.2.
//
// A used package's manifest is checked on the way in, by readUsePackage,
// because its answers decide what the using package may see. The *principal*
// package's manifest decides nothing — nobody uses it — but its declarations
// are still subject to every static rule, and a package that would be
// rejected as a library has to be rejected as the top level too. That is what
// this pass is for: it runs the same visibility arithmetic over the module's
// own components purely for its errors.
func checkExposeDeclarations(root *xdm.Node) error {
	if !isXSL(root, "package") {
		// Only an xsl:package can carry xsl:expose. A plain xsl:stylesheet
		// with one is caught by the element grammar, not here.
		return nil
	}
	var exposes []*exposeRule
	order := 0
	for _, ch := range root.ChildElements() {
		if !isXSL(ch, "expose") {
			continue
		}
		r, err := parseExposeRule(ch, order)
		if err != nil {
			return err
		}
		order++
		exposes = append(exposes, r)
	}
	if len(exposes) == 0 {
		return nil
	}
	comps, err := packageComponents(root)
	if err != nil {
		return err
	}
	return applyExpose(comps, exposes)
}

// checkAbstractExposure is XTSE3025: an xsl:expose may not make a component
// abstract unless the declaration already said so.
//
// The rule reads like XTSE3010's, and the two are distinguished by which half
// of the pair is implicit. XTSE3010 is stated only "when the component
// declaration has an explicit visibility attribute", so a declaration that
// omits one falls outside it — and without a second rule, exposing an
// ordinary function as abstract would be legal, which it plainly is not: an
// abstract component has no body, and a declaration that supplied one is not
// abstract however the manifest labels it. XTSE3025 covers that gap, and it
// has no wildcard exemption, since a wildcard cannot supply the missing body
// either. expose-912c and -922 through -924 turn on exactly the difference.
func checkAbstractExposure(c *component, r *exposeRule) error {
	if r.vis != visAbstract || c.declared == visAbstract {
		return nil
	}
	return fmt.Errorf(
		"XTSE3025: xsl:expose gives %s visibility=\"abstract\", but its "+
			"declaration is not abstract", c.sym)
}
