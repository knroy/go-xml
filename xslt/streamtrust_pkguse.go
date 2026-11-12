package xslt

import "github.com/knroy/go-xml/v2/xdm"

// A streamable xsl:source-document of a used package (lane MM3:
// override-misc-007's use-base). Its module is not the whole stylesheet, so
// compileSourceDocument passed shared and the trust walk verified nothing.
// But a package the top-level module uses directly, through an
// xsl:use-package of xsl:accept children only, that reaches no other module
// (streampkgframe_l7.go's terms for a frame package), has no declaration
// another module can replace: overriding runs only through xsl:override in
// the user (§3.5.3.3; formerly cited as 3.6.2), its accumulators and modes are its own (§3.5.5,
// §3.5.2), and an xsl:accept changes only who may name a component from
// outside. Its own declarations are then the ones in force for its code, as
// the top-level package's are for its own. A streamed run of its container
// computes the package's own accumulators (sourceDocumentInstr.pkg,
// accumRefusalIn, observeAccumsOf); a rule there reading another
// accumulator is refused.

// enterOwnPkg makes u's root ownPkg while u compiles, when u qualifies; the
// returned function restores the previous one.
func (c *compiler) enterOwnPkg(u *usePackageDecl) func() {
	prev := c.ownPkg
	restore := func() { c.ownPkg = prev }
	c.ownPkg = nil
	if c.usedPackageDepth != 1 || u == nil || u.root == nil || c.sheet == nil || c.sheet.source == nil {
		return restore
	}
	top := firstElement(c.sheet.source)
	if top == nil || documentElementOf(u.el) != top || reachesOtherModules(u.root) {
		return restore
	}
	for _, ch := range u.el.ChildElements() {
		if !isXSL(ch, "accept") {
			return restore
		}
	}
	c.ownPkg = u.root
	return restore
}

// inOwnPkg reports whether n is in the used package compiling now whose
// declarations are its own (enterOwnPkg).
func (c *compiler) inOwnPkg(n *xdm.Node) bool {
	return c.ownPkg != nil && documentElementOf(n) == c.ownPkg
}
