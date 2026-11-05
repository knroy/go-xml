package xslt

import "github.com/knroy/go-xml/v2/xdm"

// usePackageOverrides reports whether xsl:use-package el can change what a
// declaration of the using module means: a child other than xsl:accept and
// xsl:override, or an xsl:override holding a template rule (a rule added to
// a mode of the used package, whose rules the trust walk does not see).
//
// A used package never overrides its user's components (§3.6: overriding
// runs from the using package into the used one), its accumulators and its
// unnamed mode are its own (§3.5.5, §3.5.2), and a name this module uses
// but does not declare -- a function, template, attribute set or mode of
// the used package -- is not in the trust walk's tables, so it stays
// unmodelled (or modeUnresolved). So for the module that is the whole
// top-level package (newDeclTrust's caller passes shared=false only for
// c.sheet.source), a plain xsl:use-package leaves every declaration seen
// here the one in force. So does an xsl:override of a function, variable,
// parameter, named template or attribute set: the overriding declaration
// belongs to the using package (§3.5.3.3; formerly cited as 3.6.2), sits in this module, and its body's
// names resolve in this package -- as an inlined copy of it does
// (streamcall.go). su-filter-102 (f:filter-001 overridden) and
// override-misc-007's principal container are the cases.
func usePackageOverrides(el *xdm.Node) bool {
	for _, c := range el.ChildElements() {
		switch {
		case isXSL(c, "accept"):
		case isXSL(c, "override"):
			for _, d := range c.ChildElements() {
				switch {
				case isXSL(d, "function"), isXSL(d, "variable"), isXSL(d, "param"), isXSL(d, "attribute-set"):
				case isXSL(d, "template") && d.Attr("", "match") == nil:
				default:
					return true
				}
			}
		default:
			return true
		}
	}
	return false
}
