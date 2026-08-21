package xsd

import (
	"github.com/knroy/go-xml/xdm"
)

// readElementDecl reads an <xs:element>.
//
// The same element serves two roles: at the top level it declares a global
// element, and inside a content model it is a particle. This reads the
// declaration; readParticle wraps it with occurrence bounds.
func (p *parser) readElementDecl(el *xdm.Node, scope Scope) *ElementDecl {
	d := &ElementDecl{Scope: scope}

	name := el.AttrValue("name")
	if name != "" {
		if scope == ScopeGlobal || p.doc.elementFormQualified ||
			el.AttrValue("form") == "qualified" {
			d.Name = p.qnameFor(name)
		} else {
			// An unqualified local element is in the absent namespace,
			// whatever the document's target namespace is. This is what
			// elementFormDefault controls, and getting it backwards
			// makes every local element unmatchable.
			d.Name = xdm.QName{Local: name}
		}
	}

	d.Nillable = p.boolAttr(el, "nillable", false)
	d.Abstract = p.boolAttr(el, "abstract", false)
	d.Constraint = p.valueConstraint(el)

	block, err := p.derivationSet(el, "block")
	if err != nil {
		p.errs = append(p.errs, err)
	} else if el.Attr("", "block") != nil {
		d.DisallowedSubstitutions = block
	} else {
		d.DisallowedSubstitutions = p.doc.blockDefault
	}

	final, err := p.derivationSet(el, "final")
	if err != nil {
		p.errs = append(p.errs, err)
	} else if el.Attr("", "final") != nil {
		d.SubstitutionGroupExclusions = final
	} else {
		d.SubstitutionGroupExclusions = p.doc.finalDefault
	}

	// The type is either named by the type attribute or given inline, and
	// never both — src-element.3.
	typeAttr := el.Attr("", "type")
	inline := childElement(el, "simpleType", "complexType")
	switch {
	case typeAttr != nil && inline != nil:
		p.errs = append(p.errs, errorAt(el, "src-element.3",
			"an element declaration may not have both a type attribute "+
				"and an inline type definition"))
	case typeAttr != nil:
		p.resolveTypeRef(el, typeAttr.Value, func(t Type) { d.Type = t })
	case inline != nil:
		if inline.Name.Local == "simpleType" {
			d.Type = p.readSimpleType(inline)
		} else {
			d.Type = p.readComplexType(inline)
		}
	default:
		// No type: the declaration takes its substitution group head's
		// type if it has one, and xs:anyType otherwise. The head may not
		// be resolved yet, so this is deferred.
		p.fixups = append(p.fixups, func() error {
			if d.Type != nil {
				return nil
			}
			if d.SubstitutionGroup != nil && d.SubstitutionGroup.Type != nil {
				d.Type = d.SubstitutionGroup.Type
			} else {
				d.Type = p.schema.anyType()
			}
			return nil
		})
	}

	if sg := el.AttrValue("substitutionGroup"); sg != "" {
		if scope != ScopeGlobal {
			p.errs = append(p.errs, errorAt(el, "src-element.3",
				"only a top-level element declaration may have a "+
					"substitutionGroup"))
		} else {
			name, err := p.resolveQName(el, "substitutionGroup", sg)
			if err != nil {
				p.errs = append(p.errs, err)
			} else {
				p.fixups = append(p.fixups, func() error {
					head, ok := p.schema.Elements[name]
					if !ok {
						return errorAt(el, "src-resolve",
							"substitutionGroup %q names no element declaration", sg)
					}
					d.SubstitutionGroup = head
					return nil
				})
			}
		}
	}

	for _, c := range contentChildren(el) {
		if c.Name.URI != NSSchema {
			continue
		}
		switch c.Name.Local {
		case "key", "keyref", "unique":
			if ic := p.readIdentityConstraint(c); ic != nil {
				d.IdentityConstraints = append(d.IdentityConstraints, ic)
			}
		}
	}

	return d
}

// readAttributeDecl reads an <xs:attribute> as a declaration.
func (p *parser) readAttributeDecl(el *xdm.Node, scope Scope) *AttributeDecl {
	d := &AttributeDecl{Scope: scope}

	name := el.AttrValue("name")
	if name != "" {
		if scope == ScopeGlobal || p.doc.attributeFormQualified ||
			el.AttrValue("form") == "qualified" {
			d.Name = p.qnameFor(name)
		} else {
			d.Name = xdm.QName{Local: name}
		}
	}
	d.Constraint = p.valueConstraint(el)

	typeAttr := el.Attr("", "type")
	inline := childElement(el, "simpleType")
	switch {
	case typeAttr != nil && inline != nil:
		p.errs = append(p.errs, errorAt(el, "src-attribute.4",
			"an attribute declaration may not have both a type attribute "+
				"and an inline simpleType"))
	case typeAttr != nil:
		p.resolveTypeRef(el, typeAttr.Value, func(t Type) {
			st, ok := t.(*SimpleType)
			if !ok {
				p.errs = append(p.errs, errorAt(el, "src-resolve",
					"attribute type %q is a complex type; attributes must "+
						"have a simple type", typeAttr.Value))
				return
			}
			d.Type = st
		})
	case inline != nil:
		d.Type = p.readSimpleType(inline)
	default:
		d.Type = p.schema.anySimpleType()
	}

	return d
}

// readAttributeUse reads an <xs:attribute> inside a complex type or attribute
// group, where it is an attribute *use* rather than a declaration.
//
// A use with ref= points at a global declaration; a use with name= carries an
// inline local one. The two forms are mutually exclusive.
func (p *parser) readAttributeUse(el *xdm.Node) *AttributeUse {
	use := &AttributeUse{}

	switch el.AttrValue("use") {
	case "required":
		use.Required = true
	case "prohibited":
		// A prohibited use removes an inherited attribute. It is modelled
		// by returning nil so that the attribute simply does not appear
		// in the type's uses.
		return nil
	case "", "optional":
	default:
		p.errs = append(p.errs, errorAt(el, "",
			"use=%q is not one of required, optional or prohibited",
			el.AttrValue("use")))
	}

	use.Constraint = p.valueConstraint(el)

	if ref := el.AttrValue("ref"); ref != "" {
		if el.AttrValue("name") != "" {
			p.errs = append(p.errs, errorAt(el, "src-attribute.3.1",
				"an attribute use may not have both ref and name"))
			return nil
		}
		name, err := p.resolveQName(el, "ref", ref)
		if err != nil {
			p.errs = append(p.errs, err)
			return nil
		}
		p.fixups = append(p.fixups, func() error {
			decl, ok := p.schema.Attributes[name]
			if !ok {
				return errorAt(el, "src-resolve",
					"attribute ref %q names no attribute declaration", ref)
			}
			use.Decl = decl
			return nil
		})
		return use
	}

	use.Decl = p.readAttributeDecl(el, ScopeLocal)
	return use
}

// readNotation reads an <xs:notation>.
func (p *parser) readNotation(el *xdm.Node) *NotationDecl {
	name := el.AttrValue("name")
	if name == "" {
		p.errs = append(p.errs, errorAt(el, "",
			"a notation declaration must have a name"))
		return nil
	}
	return &NotationDecl{
		Name:   p.qnameFor(name),
		Public: el.AttrValue("public"),
		System: el.AttrValue("system"),
	}
}

// readAttributeGroupDef reads a top-level <xs:attributeGroup>.
func (p *parser) readAttributeGroupDef(el *xdm.Node) *AttributeGroupDef {
	name := el.AttrValue("name")
	if name == "" {
		p.errs = append(p.errs, errorAt(el, "",
			"a top-level attributeGroup must have a name"))
		return nil
	}
	g := &AttributeGroupDef{Name: p.qnameFor(name)}
	g.AttributeWildcard = p.readAttributes(el, &g.AttributeUses)
	return g
}

// readAttributes reads the attribute uses, attribute group references and
// attribute wildcard of a complex type or attribute group.
//
// References to attribute groups are flattened into the containing component's
// uses, which is what the spec's {attribute uses} property holds: a set of
// uses, with no record of which group they arrived through.
//
// The target is passed in rather than returned because a group may be defined
// after the reference to it, so the flattening happens in a fixup that runs
// later. Returning a slice and letting the caller assign it would leave the
// fixup appending to a variable nobody reads — which is exactly the bug this
// signature exists to prevent, and which silently dropped every
// attribute-group attribute.
func (p *parser) readAttributes(el *xdm.Node, target *[]*AttributeUse) *Wildcard {
	var wildcard *Wildcard

	for _, c := range contentChildren(el) {
		if c.Name.URI != NSSchema {
			continue
		}
		switch c.Name.Local {
		case "attribute":
			if u := p.readAttributeUse(c); u != nil {
				*target = append(*target, u)
			}

		case "attributeGroup":
			ref := c.AttrValue("ref")
			if ref == "" {
				p.errs = append(p.errs, errorAt(c, "src-attribute_group.1",
					"an attributeGroup reference must have a ref"))
				continue
			}
			name, err := p.resolveQName(c, "ref", ref)
			if err != nil {
				p.errs = append(p.errs, err)
				continue
			}
			p.fixups = append(p.fixups, func() error {
				g, ok := p.schema.AttributeGroups[name]
				if !ok {
					return errorAt(c, "src-resolve",
						"attributeGroup ref %q names no attribute group", ref)
				}
				*target = append(*target, g.AttributeUses...)
				return nil
			})

		case "anyAttribute":
			wildcard = p.readWildcard(c)
		}
	}
	return wildcard
}

// readWildcard reads an <xs:any> or <xs:anyAttribute>.
func (p *parser) readWildcard(el *xdm.Node) *Wildcard {
	w := &Wildcard{}

	switch el.AttrValue("processContents") {
	case "", "strict":
		w.ProcessContents = ProcessStrict
	case "lax":
		w.ProcessContents = ProcessLax
	case "skip":
		w.ProcessContents = ProcessSkip
	default:
		p.errs = append(p.errs, errorAt(el, "",
			"processContents=%q is not one of strict, lax or skip",
			el.AttrValue("processContents")))
	}

	ns := el.AttrValue("namespace")
	if ns == "" {
		ns = "##any"
	}
	switch ns {
	case "##any":
		w.Kind = NSAny
	case "##other":
		// ##other is "not the target namespace". In a document with no
		// target namespace it becomes "not absent", which still excludes
		// unqualified names by clause 2.3 of Wildcard allows Namespace
		// Name — see Wildcard.Allows.
		w.Kind = NSNot
		w.Namespace = []string{p.doc.targetNS}
	default:
		w.Kind = NSEnumerated
		for _, word := range splitFields(ns) {
			switch word {
			case "##targetNamespace":
				w.Namespace = append(w.Namespace, p.doc.targetNS)
			case "##local":
				// ##local is the absent namespace, spelled as the
				// empty string in the enumerated set.
				w.Namespace = append(w.Namespace, "")
			default:
				w.Namespace = append(w.Namespace, word)
			}
		}
	}
	return w
}

// splitFields splits on XML whitespace only.
//
// strings.Fields splits on every Unicode space, which would treat U+00A0 as a
// separator. XML does not, so a namespace list containing one would be split
// into names that match nothing.
func splitFields(s string) []string {
	var out []string
	start := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// resolveTypeRef resolves a type named by an attribute, deferring until every
// document has been read.
func (p *parser) resolveTypeRef(el *xdm.Node, ref string, set func(Type)) {
	name, err := p.resolveQName(el, "type", ref)
	if err != nil {
		p.errs = append(p.errs, err)
		return
	}
	p.fixups = append(p.fixups, func() error {
		t, ok := p.schema.Types[name]
		if !ok {
			return errorAt(el, "src-resolve",
				"type %q names no type definition", ref)
		}
		set(t)
		return nil
	})
}
