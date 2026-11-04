package xslt

import "github.com/knroy/go-xml/v2/xdm"

// usePackageOverrides reports whether xsl:use-package el can change what a
// declaration of the using module means: it has an xsl:override (or
// anything but xsl:accept, conservatively).
//
// A used package never overrides its user's components (§3.6: overriding
// runs from the using package into the used one), its accumulators and its
// unnamed mode are its own (§3.5.5, §3.5.2), and a name this module uses
// but does not declare -- a function, template, attribute set or mode of
// the used package -- is not in the trust walk's tables, so it stays
// unmodelled (or modeUnresolved). So for the module that is the whole
// top-level package (newDeclTrust's caller passes shared=false only for
// c.sheet.source), a plain xsl:use-package leaves every declaration seen
// here the one in force. An xsl:override is left shared: its functions are
// collected here, and inlining one (streamcall.go) would resolve its
// variable references in this module's scope, not the used package's.
// override-misc-007's principal container (accumulator-after of its own
// streamable accumulator) is the case.
func usePackageOverrides(el *xdm.Node) bool {
	for _, c := range el.ChildElements() {
		if !isXSL(c, "accept") {
			return true
		}
	}
	return false
}
