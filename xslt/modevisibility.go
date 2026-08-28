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
	// stated distinguishes a declaration that wrote visibility="private"
	// from one that merely defaulted to it. Outside a package only the
	// former withholds the mode as an entry point; see eligibleInitialMode.
	stated bool
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
	stated := vis != ""
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
		c.modeVisibility[m] = modeVisibility{
			visibility: vis, precedence: precedence, stated: stated}
	}
}

// exposeImplicitModes applies the principal package's xsl:expose declarations
// to modes that no xsl:mode declares.
//
// 6.6.1: "If a mode name is used (for example in an xsl:template declaration
// or an xsl:apply-templates instruction) and no declaration of that mode
// appears in the stylesheet, the mode is implicitly declared with default
// properties." An implicitly declared mode is a component like any other, so
// an xsl:expose naming it -- by name or by wildcard -- governs its visibility,
// and 6.6.1 makes a mode that is not public or final ineligible as an entry
// point.
//
// Without this the mode had no recorded visibility at all and
// eligibleInitialMode let it through as undeclared. package-001j writes
// xsl:expose component="mode" names="*" visibility="private" over a template
// rule in mode "start" and expects XTDE0045 for a transform started there.
//
// Only a real xsl:package is considered, because only a package has a
// manifest: xsl:expose is not allowed on xsl:stylesheet.
func (c *compiler) exposeImplicitModes() {
	if c.sheet.source == nil {
		return
	}
	root := firstElement(c.sheet.source)
	if root == nil || !isXSL(root, "package") {
		return
	}
	var exposes []*exposeRule
	order := 0
	for _, ch := range root.ChildElements() {
		if !isXSL(ch, "expose") {
			continue
		}
		r, err := parseExposeRule(ch, order)
		if err != nil {
			return
		}
		order++
		exposes = append(exposes, r)
	}
	if len(exposes) == 0 {
		return
	}
	for _, t := range c.sheet.templates {
		for _, m := range t.Mode {
			if m == "" {
				// The unnamed mode is always invocable, 6.6.1, and has no
				// name for an xsl:expose to reach it by.
				continue
			}
			if _, declared := c.modeVisibility[m]; declared {
				// An explicit xsl:mode declaration has already been recorded,
				// and applyExpose over the real components settles that one.
				continue
			}
			sym := symbolicName{kind: kindMode, arity: -1,
				name: clarkQName(m)}
			var vis visibility
			for _, r := range exposes {
				if ok, _ := r.matches(sym); ok {
					vis = r.vis
				}
			}
			if vis == "" {
				continue
			}
			if c.modeVisibility == nil {
				c.modeVisibility = map[string]modeVisibility{}
			}
			c.modeVisibility[m] = modeVisibility{
				visibility: string(vis), stated: true}
		}
	}
}

// publishModeVisibility flattens the precedence-resolved declarations onto the
// stylesheet, which is what the invocation consults.
func (c *compiler) publishModeVisibility() {
	c.exposeImplicitModes()
	if len(c.modeVisibility) == 0 {
		return
	}
	c.sheet.modeVisibility = make(map[string]string, len(c.modeVisibility))
	c.sheet.modeVisibilityStated = make(map[string]bool, len(c.modeVisibility))
	for m, v := range c.modeVisibility {
		c.sheet.modeVisibility[m] = v.visibility
		c.sheet.modeVisibilityStated[m] = v.stated
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
	vis, ok := s.modeVisibility[mode]
	if !ok {
		return true
	}
	// Visibility is a property of a package boundary. A plain xsl:stylesheet
	// has none — nothing can use it as a library — so a mode it declared
	// without saying anything about visibility stays invocable, which is what
	// the many xsl:stylesheet cases invoked in a bare-declared mode rely on.
	//
	// An explicit visibility="private" is different: it is the stylesheet
	// saying so, and 6.6.1 has it withhold the mode as an entry point whether
	// or not the module calls itself a package. package-001i writes exactly
	// that on an xsl:stylesheet and expects XTDE0045.
	if !s.isPackage && !s.modeVisibilityStated[mode] {
		return true
	}
	return vis == "public" || vis == "final"
}


// clarkQName parses the Clark form the mode tables key on back into a QName,
// which is what an exposeRule matches against.
func clarkQName(s string) xdm.QName {
	if strings.HasPrefix(s, "{") {
		if i := strings.IndexByte(s, '}'); i >= 0 {
			return xdm.QName{URI: s[1:i], Local: s[i+1:]}
		}
	}
	return xdm.QName{Local: s}
}
