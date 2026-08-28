package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
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
//
// tmpl is the containing xsl:template, whose @name decides which values of
// @use are open to the declaration.
func compileContextItem(el, tmpl *xdm.Node) (*contextItemDecl, error) {
	// XTSE0090: the summary gives xsl:context-item exactly two attributes.
	// The check is here rather than left to the element table because this
	// engine reads every 3.0 module as forwards-compatible, which withholds
	// the table from all of them; @select is the one a stylesheet reaches
	// for, xsl:param having accustomed it to the idea.
	for _, a := range el.Attrs {
		if a.Name.URI != "" {
			continue
		}
		switch a.Name.Local {
		case "as", "use":
		default:
			if standardAttributes[a.Name.Local] {
				continue
			}
			return nil, fmt.Errorf(
				"attribute %q is not allowed on xsl:context-item (XTSE0090)",
				a.Name.Local)
		}
	}

	d := &contextItemDecl{use: "optional"}
	if a := el.Attr("", "use"); a != nil {
		// The value space is a closed set of tokens, so surrounding
		// whitespace is layout rather than part of the value -- 3.7 says so
		// of every such attribute, and context-item-007 writes use=" required "
		// under xml:space="preserve" to insist on it.
		switch v := strings.TrimSpace(a.Value); v {
		case "required", "optional", "absent":
			d.use = v
		default:
			return nil, fmt.Errorf(
				"XTSE0020: xsl:context-item/@use must be required, optional "+
					"or absent, not %q", a.Value)
		}
	}
	// "If the containing xsl:template element has no name attribute then the
	// only permitted value is required." A template rule is always entered
	// through a focus, so declaring the item optional or absent would be
	// declaring something the invocation cannot honour.
	if tmpl != nil && tmpl.Attr("", "name") == nil && d.use != "required" {
		return nil, fmt.Errorf(
			"XTSE0020: xsl:context-item/@use must be \"required\" in a "+
				"template with no name attribute, not %q", d.use)
	}
	as := strings.TrimSpace(el.AttrValue("as"))
	if as != "" {
		// A type cannot be required of an item declared absent, because
		// there is no item to have one. The two elements state the same rule
		// under different codes -- XTSE3088 for xsl:context-item, XTSE3089
		// for xsl:global-context-item -- so the code is read off the element
		// rather than fixed, and a caller matching on one gets the one the
		// specification assigns to what it wrote.
		if d.use == "absent" {
			code := "XTSE3088"
			if el.Name.Local == "global-context-item" {
				code = "XTSE3089"
			}
			return nil, fmt.Errorf(
				"%s: xsl:%s may not have an as attribute when use=\"absent\"",
				code, el.Name.Local)
		}
		// The attribute is typed ItemType, not SequenceType: an occurrence
		// indicator would be saying how many context items there are, and
		// there is exactly one. context-item-901 writes xs:integer? and
		// expects XTSE0020, the code for a value outside an attribute's
		// permitted range.
		if strings.HasSuffix(as, "?") || strings.HasSuffix(as, "*") ||
			strings.HasSuffix(as, "+") {
			return nil, fmt.Errorf(
				"XTSE0020: xsl:context-item/@as is an ItemType and admits no "+
					"occurrence indicator, so %q is not one", as)
		}
		res := newNSResolver(el, "")
		t, err := compileSequenceType(as, res)
		if err != nil {
			return nil, fmt.Errorf("in xsl:context-item/@as: %w", err)
		}
		if err := checkAnnotationTypeKnown(t, res); err != nil {
			return nil, err
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

// checkAnnotationTypeKnown rejects an element()/attribute() test whose type
// argument names a type no schema in the static context defines.
//
// The XPath parser records the type argument lexically, because at run time
// the comparison is against the annotation the node carries rather than
// against a resolved schema component -- so a name nothing defines parses
// cleanly and simply matches nothing. That is the right behaviour for a
// pattern, where failing to match is the answer. It is the wrong behaviour
// for xsl:context-item/@as, where the declaration is a static promise about
// the template's focus: a type that cannot exist makes every call fail at
// run time with XTTE0590, reporting a type mismatch for a type the
// stylesheet never had. context-item-903 writes
// as="element(*, my:percentage)" against no imported schema and expects the
// error at compile time.
//
// XTSE0020 is the code XSLT gives an attribute whose value is outside its
// permitted range, and the suite accepts it here alongside XPST0008.
func checkAnnotationTypeKnown(t *sequenceType, res *nsResolver) error {
	if t == nil {
		return nil
	}
	kt, ok := t.stype.ItemType.(*xpath.KindTest)
	if !ok || kt.TypeNameLexical == "" {
		return nil
	}
	prefix, local := xdm.SplitQName(kt.TypeNameLexical)
	uri := ""
	if prefix == "" {
		uri = res.DefaultElementNamespace()
	} else {
		u, found := res.ResolvePrefix(prefix)
		if !found {
			// An unbound prefix is XPST0081 and belongs to the parser, which
			// has already had its chance; saying nothing here leaves that
			// diagnosis where it is.
			return nil
		}
		uri = u
	}
	if uri == xdm.NSXS {
		// A built-in type is in every static context, whether or not any
		// schema was imported.
		return nil
	}
	if _, _, ok := res.LookupSchemaType(xdm.QName{URI: uri, Local: local}); ok {
		return nil
	}
	return fmt.Errorf(
		"XTSE0020: xsl:context-item/@as names the type %s, which no schema "+
			"in the static context defines", kt.TypeNameLexical)
}
