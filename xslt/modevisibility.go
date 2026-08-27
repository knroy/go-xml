package xslt

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// modeVisibility records an xsl:mode/@visibility at the precedence it was
// declared at, so that a higher-precedence declaration can override a lower
// one the way every other mode property does.
type modeVisibility struct {
	visibility string
	precedence int
}

// recordModeVisibility notes what an xsl:mode declaration says about
// visibility, keeping only the highest-precedence answer per mode.
//
// A declaration that omits @visibility still counts: section 6.6.1 gives a
// mode declared without one the default visibility "private", and mode-1705b
// turns on exactly that — its "private-mode" is declared bare and is thereby
// ineligible as an initial mode.
func (c *compiler) recordModeVisibility(el *xdm.Node, precedence int) {
	vis := strings.TrimSpace(el.AttrValue("visibility"))
	if vis == "" {
		vis = "private"
	}
	if c.modeVisibility == nil {
		c.modeVisibility = map[string]modeVisibility{}
	}
	for _, m := range modeNamesOf(el) {
		if prev, ok := c.modeVisibility[m]; ok && prev.precedence > precedence {
			continue
		}
		c.modeVisibility[m] = modeVisibility{visibility: vis, precedence: precedence}
	}
}

// publishModeVisibility flattens the precedence-resolved declarations onto the
// stylesheet, which is what the invocation consults.
func (c *compiler) publishModeVisibility() {
	if len(c.modeVisibility) == 0 {
		return
	}
	c.sheet.modeVisibility = make(map[string]string, len(c.modeVisibility))
	for m, v := range c.modeVisibility {
		c.sheet.modeVisibility[m] = v.visibility
	}
}

// eligibleInitialModes returns the set of mode names an invocation may name
// as its initial mode, or nil when every declared mode is eligible.
//
// XSLT 3.0 section 6.6.1 makes a mode invocable from outside only if it is
// visible from outside: a public or final mode may be named, a private or
// hidden one may not, and naming an ineligible mode is XTDE0045. Two
// exemptions keep otherwise-private modes reachable:
//
//   - The unnamed mode is always eligible. mode-1803 states it directly: the
//     default mode "is implicitly private, but is still publicly invocable",
//     since a transform with no initial mode at all has to start somewhere.
//   - The mode named by the *root* element's @default-mode is eligible
//     whatever its visibility, because that attribute is the package's own
//     statement of where it expects to be entered. mode-1705a declares its
//     mode "a" bare, hence private, and is still invoked in it.
//
// The exemption is confined to the root: @default-mode on an inner element
// governs mode resolution within that element only and says nothing about the
// package's entry points, which is what mode-1714err asserts.
//
// A mode that no xsl:mode declares has no visibility to test — it exists only
// because template rules name it, which cannot happen from outside the
// package — so it stays eligible, as it was before visibility was modelled.
func (s *Stylesheet) eligibleInitialMode(mode string) bool {
	if mode == "" {
		return true
	}
	// Visibility is a property of a package boundary. A plain xsl:stylesheet
	// has none — nothing can use it as a library — so its modes are all
	// invocable, which is what the many xsl:stylesheet cases invoked in a
	// bare-declared mode rely on.
	if !s.isPackage {
		return true
	}
	vis, ok := s.modeVisibility[mode]
	if !ok {
		return true
	}
	return vis == "public" || vis == "final"
}
