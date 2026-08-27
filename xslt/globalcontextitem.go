package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// globalContextItemDecl is xsl:global-context-item, XSLT 3.0 section 3.10.
//
// It declares what the *transform as a whole* expects of the global context
// item -- the item supplied to the transformation, from which every global
// variable's select expression is evaluated. xsl:context-item asks the same
// question of one template; this asks it of the package.
//
// It reuses contextItemDecl because the two carry the same three answers, and
// the checking rule is identical: match the declared type, no conversion.
// Only where the check runs differs.
type globalContextItemDecl struct {
	decl *contextItemDecl
	// el is the declaring element, kept so that a second declaration can be
	// compared against this one for consistency.
	el *xdm.Node
}

// compileGlobalContextItem reads an xsl:global-context-item declaration and
// records it on the stylesheet.
//
// A module may declare it at most once, and every module of one package must
// agree: XTSE3087 covers both, since two modules disagreeing is the same
// fault as one module saying it twice.
func (c *compiler) compileGlobalContextItem(el *xdm.Node) error {
	// nil for the template: the "@use must be required in a template with no
	// name" rule is about a template rule's focus, and a global declaration
	// is in no template at all.
	d, err := compileContextItem(el, nil)
	if err != nil {
		return err
	}
	// XTSE3089: streamable="yes" constrains the global variables, which this
	// engine does not stream. The attribute is accepted and the constraint is
	// not enforced, which is what a non-streaming processor may do: section
	// 19 makes streamability a property a processor may decline to analyse.
	if prev := c.sheet.globalContextItem; prev != nil {
		if !sameContextItemDecl(prev.decl, d) {
			return fmt.Errorf(
				"XTSE3087: the package contains inconsistent " +
					"xsl:global-context-item declarations")
		}
		return nil
	}
	c.sheet.globalContextItem = &globalContextItemDecl{decl: d, el: el}
	return nil
}

// sameContextItemDecl reports whether two declarations say the same thing.
//
// Compared by their written form rather than by the compiled type, because
// two spellings of one type are the same declaration and comparing compiled
// types would need a type-equality relation this package does not have.
func sameContextItemDecl(a, b *contextItemDecl) bool {
	if a.use != b.use {
		return false
	}
	as, bs := "", ""
	if a.as != nil {
		as = a.as.source()
	}
	if b.as != nil {
		bs = b.as.source()
	}
	return as == bs
}

// checkGlobalContextItem applies the declaration to the item a transform was
// started with.
//
// XTDE3086 rather than XTTE3090: the global context item has its own code for
// being required and absent, because the failure is a property of how the
// transformation was invoked rather than of any one template.
func (s *Stylesheet) checkGlobalContextItem(item xdm.Item) error {
	g := s.globalContextItem
	if g == nil {
		return nil
	}
	if item == nil {
		if g.decl.use == "required" {
			return fmt.Errorf(
				"XTDE3086: the stylesheet requires a global context item, " +
					"but none was supplied")
		}
		return nil
	}
	// use="absent" says the transformation reads no global context item. The
	// specification explicitly leaves it open whether supplying one anyway is
	// an error, so it is not made one here -- the note under section 3.10
	// says a processor may ignore it, and ignoring is the choice that cannot
	// reject a stylesheet another processor accepts.
	if g.decl.use == "absent" {
		return nil
	}
	return g.decl.check(item)
}
