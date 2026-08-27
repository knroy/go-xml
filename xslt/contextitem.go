package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// contextItemDecl is xsl:context-item, XSLT 3.0 section 10.1.1.
//
// It declares what the enclosing template expects of the context item: what
// type it must be, and whether it must be there at all. The declaration is
// checked when the template is entered, which is why it lives on Template
// rather than being compiled into the body — a template invoked by
// xsl:call-template with no focus must fail before its first instruction
// runs, not at whatever expression first happens to need a context item.
type contextItemDecl struct {
	// as is the required item type, or nil for the default item(), which
	// admits anything and so needs no check.
	as *sequenceType
	// use is "required", "optional" or "absent"; the spec's default is
	// optional.
	use string
}

// compileContextItem reads an xsl:context-item child of xsl:template.
func compileContextItem(el *xdm.Node) (*contextItemDecl, error) {
	d := &contextItemDecl{use: "optional"}
	if v := el.AttrValue("use"); v != "" {
		switch v {
		case "required", "optional", "absent":
			d.use = v
		default:
			return nil, fmt.Errorf(
				"XTSE0020: xsl:context-item/@use must be required, optional "+
					"or absent, not %q", v)
		}
	}
	as := el.AttrValue("as")
	if as != "" {
		// XTSE3088: a type cannot be required of an item declared absent,
		// because there is no item to have one.
		if d.use == "absent" {
			return nil, fmt.Errorf(
				"XTSE3088: xsl:context-item may not have an as attribute " +
					"when use=\"absent\"")
		}
		t, err := compileSequenceType(as, newNSResolver(el, ""))
		if err != nil {
			return nil, fmt.Errorf("in xsl:context-item/@as: %w", err)
		}
		d.as = t
	}
	return d, nil
}

// check applies the declaration to the context item a template was entered
// with. A nil declaration accepts anything, which is what a template that
// declares nothing means.
func (d *contextItemDecl) check(item xdm.Item) error {
	if d == nil {
		return nil
	}
	if item == nil {
		// XTTE3090: the template requires a context item and the caller
		// supplied none. Only "required" is an error -- "optional" is the
		// default precisely so that an absent item is ordinary, and "absent"
		// asks for exactly this.
		if d.use == "required" {
			return fmt.Errorf(
				"XTTE3090: the template requires a context item, but none " +
					"was supplied")
		}
		return nil
	}
	if d.use == "absent" {
		// A template declaring the item absent is saying it does not read
		// one. The spec gives no error for a caller supplying one anyway --
		// the item is simply not part of the template's focus -- so this is
		// not a failure.
		return nil
	}
	if d.as == nil {
		return nil
	}
	// XTTE0590, the same code xsl:param uses. Matches rather than convertAs:
	// section 10.1.1 is explicit that "no attempt is made to convert the
	// context item to the required type (using the function conversion rules
	// or otherwise)", so an xs:untypedAtomic item that would *cast* to the
	// declared type still does not match it.
	if !d.as.stype.Matches(xdm.One(item)) {
		return fmt.Errorf(
			"XTTE0590: the context item does not match the type required by "+
				"xsl:context-item: %s", d.as.source())
	}
	return nil
}
