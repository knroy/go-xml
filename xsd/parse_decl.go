package xsd

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// readElementDecl reads an <xs:element>.
//
// The same element serves two roles: at the top level it declares a global
// element, and inside a content model it is a particle. This reads the
// declaration; readParticle wraps it with occurrence bounds.
func (p *parser) readElementDecl(el *xdm.Node, scope Scope) *ElementDecl {
	d := &ElementDecl{Scope: scope}

	if scope == ScopeLocal {
		p.checkLocalTargetNamespace(el, "element")
	}

	name := el.AttrValue("name")
	if name != "" {
		switch {
		case scope == ScopeLocal && el.Attr("", "targetNamespace") != nil:
			// XSD 1.1 lets a local declaration name its own
			// namespace, which is how a schema declares a component
			// belonging somewhere other than its own target
			// namespace without importing one. It overrides form
			// and elementFormDefault, both of which only choose
			// between the target namespace and none.
			d.Name = xdm.QName{
				URI:   el.AttrValue("targetNamespace"),
				Local: name,
			}
		case scope == ScopeGlobal || formQualified(el, p.doc.elementFormQualified):
			d.Name = p.qnameFor(name)
		default:
			// An unqualified local element is in the absent namespace,
			// whatever the document's target namespace is. This is what
			// elementFormDefault controls, and getting it backwards
			// makes every local element unmatchable.
			d.Name = xdm.QName{Local: name}
		}
	}

	d.Nillable = p.boolAttr(el, "nillable", false)
	d.Abstract = p.boolAttr(el, "abstract", false)
	// The schema for schemas types a local declaration as xs:localElement,
	// which restricts xs:element with use="prohibited" on substitutionGroup,
	// final and abstract alike. Only substitutionGroup was rejected here,
	// so <element abstract="true"> inside a content model loaded clean —
	// particlesDa011 is exactly that. final is included because the same
	// clause of the same restriction prohibits it, not because a suite
	// case pins it.
	//
	// Both attributes are meaningless on a local declaration for the same
	// reason: each governs substitution, and only a top-level declaration
	// can head a substitution group or be substituted for.
	if scope != ScopeGlobal {
		for _, attr := range []string{"abstract", "final"} {
			if el.Attr("", attr) != nil {
				p.errs = append(p.errs, errorAt(el, "src-element.3",
					"attribute %q is not allowed on a local element declaration", attr))
			}
		}
	}
	d.Constraint = p.valueConstraint(el)
	p.checkElementValueConstraint(el, d)

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
	inline := p.childElement(el, "simpleType", "complexType")
	switch {
	case typeAttr != nil && inline != nil:
		p.errs = append(p.errs, errorAt(el, "src-element.3",
			"an element declaration may not have both a type attribute "+
				"and an inline type definition"))
	case typeAttr != nil:
		// An element declaration naming a type that does not exist is an
		// error only where the declaration is used. The suite is
		// explicit about it — missing001 is "Error only if the element
		// declaration is needed for validation", and its schema is
		// expected to load — so the reference is recorded and reported
		// against the instance that reaches it.
		p.resolveTypeRefLazy(el, typeAttr.Value,
			func(t Type) {
				d.Type = t
				p.rejectDirectNotation(el, t)
			},
			func(ref string) { d.unresolved = ref })
	case inline != nil:
		if inline.Name.Local == "simpleType" {
			d.Type = p.readSimpleType(inline)
		} else {
			d.Type = p.readComplexType(inline)
		}
	default:
		// No type: per §3.3.2 the declaration takes its substitution
		// group head's {type definition}, and xs:anyType only when there
		// is no head. This waits for postFixups rather than fixups
		// because the affiliation itself is bound by a fixup queued
		// *after* this one — running here would always read a nil head
		// and default to anyType. elemT064 pins it: <sa3> has no type
		// and substitutes for test1, whose type A the instance supplies;
		// defaulting to anyType made the substitution look blocked.
		p.postFixups = append(p.postFixups, func() error {
			if d.Type != nil {
				return nil
			}
			d.Type = p.inheritedElementType(d, map[*ElementDecl]bool{})
			return nil
		})
	}

	if sg := el.AttrValue("substitutionGroup"); sg != "" {
		if scope != ScopeGlobal {
			p.errs = append(p.errs, errorAt(el, "src-element.3",
				"only a top-level element declaration may have a "+
					"substitutionGroup"))
		} else {
			// XSD 1.1 permits a list of heads; 1.0 permits one. The
			// list form is parsed either way, because a schema using
			// it is not made valid by reading only the first name.
			for _, one := range splitFields(sg) {
				name, err := p.resolveQName(el, "substitutionGroup", one)
				if err != nil {
					p.errs = append(p.errs, err)
					continue
				}
				p.fixups = append(p.fixups, func() error {
					head, ok := p.schema.Elements[name]
					if !ok {
						// A missing head is not an error for this
						// declaration at all. The affiliation only
						// decides what this element may substitute
						// *for*; using the declaration directly
						// asks nothing of the head. missing002
						// pins exactly that: <bad> names a head
						// that does not exist, and an instance of
						// <bad> is still valid.
						//
						// Nothing can substitute for a head that
						// does not exist either, so dropping the
						// affiliation loses no constraint.
						return nil
					}
					if d.SubstitutionGroup == nil {
						d.SubstitutionGroup = head
					}
					d.SubstitutionGroups = append(d.SubstitutionGroups, head)
					return nil
				})
			}
		}
	}

	p.checkSubstitutionGroupDerivation(el, d)

	for _, c := range p.contentChildren(el) {
		if c.Name.URI != NSSchema {
			continue
		}
		switch c.Name.Local {
		case "key", "keyref", "unique":
			if ic := p.readIdentityConstraint(c); ic != nil {
				slot := len(d.IdentityConstraints)
				d.IdentityConstraints = append(d.IdentityConstraints, ic)
				// A ref= reference resolves to the very component it names, not
				// a copy of it. Node tables are keyed by component identity, so
				// a keyref reached through ref= must see the same pointer its
				// Refer holds; a copy would key a second table the keyref could
				// never find. p.icRefs records the slot for the fixup below.
				if c.AttrValue("ref") != "" {
					p.icRefs = append(p.icRefs, icRefSlot{decl: d, slot: slot, ic: ic})
				}
			}
		case "alternative":
			// XSD 1.1 conditional type assignment. Order matters:
			// the first alternative whose test holds wins.
			if alt := p.readAlternative(c); alt != nil {
				d.Alternatives = append(d.Alternatives, alt)
			}
		}
	}

	return d
}

// readAttributeDecl reads an <xs:attribute> as a declaration.
func (p *parser) readAttributeDecl(el *xdm.Node, scope Scope) *AttributeDecl {
	d := &AttributeDecl{Scope: scope}

	// The schema for schemas types a top-level <xs:attribute> as
	// xs:topLevelAttribute, a restriction that prohibits ref, form and use
	// and makes name a required NCName. A global declaration is not a use,
	// so "how it is used" has no meaning there — attA001 (form), attF009
	// and attJ011 (use) are each expected invalid on that ground alone.
	name := el.AttrValue("name")
	if scope == ScopeGlobal {
		for _, banned := range []string{"form", "use", "ref"} {
			if el.Attr("", banned) != nil {
				p.errs = append(p.errs, errorAt(el, "src-attribute",
					"a top-level attribute declaration may not "+
						"have a %s attribute", banned))
			}
		}
		if name == "" {
			p.errs = append(p.errs, errorAt(el, "src-attribute",
				"a top-level attribute declaration must have a name"))
		}
	}
	// {name} is an NCName (§3.2.1). A colon is what the NCName production
	// exists to exclude, so a:b is rejected here rather than resolved as a
	// prefix — attC007 through attC010 are the shapes that reach this.
	if name != "" && !isNCName(name) {
		p.errs = append(p.errs, errorAt(el, "src-attribute",
			"attribute name %q is not an NCName", name))
	}
	// no-xmlns (§3.2.6): the {name} of an attribute declaration must not
	// match xmlns. Namespace declarations are not attributes a schema may
	// govern, and there is no way to supply a default for one.
	if name == "xmlns" {
		p.errs = append(p.errs, errorAt(el, "no-xmlns",
			"an attribute declaration may not be named xmlns"))
	}
	// form is an enumeration of exactly two values. formQualified falls back
	// to the document default for anything it does not recognise, so a
	// misspelling silently behaved as if form were absent — attA003 ("foo"),
	// attA004 (""), attA005 ("Qualified") and attA006 ("Unqualified") all
	// loaded clean before this.
	if f := el.Attr("", "form"); f != nil &&
		f.Value != "qualified" && f.Value != "unqualified" {
		p.errs = append(p.errs, errorAt(el, "src-attribute",
			"form=%q is not one of qualified or unqualified", f.Value))
	}

	if scope == ScopeLocal {
		p.checkLocalTargetNamespace(el, "attribute")
	}

	if name != "" {
		switch {
		case scope == ScopeLocal && el.Attr("", "targetNamespace") != nil:
			// As for elements: XSD 1.1 lets a local attribute name
			// the namespace it belongs to directly.
			d.Name = xdm.QName{
				URI:   el.AttrValue("targetNamespace"),
				Local: name,
			}
		case scope == ScopeGlobal || formQualified(el, p.doc.attributeFormQualified):
			d.Name = p.qnameFor(name)
		default:
			d.Name = xdm.QName{Local: name}
		}
	}
	// no-xsi (§3.2.6): the {target namespace} of an attribute declaration,
	// local or global, must not be the schema-instance namespace. That
	// namespace holds xsi:type, xsi:nil and the two schemaLocation
	// attributes, whose meaning is fixed by the specification; a schema
	// that declared attributes there would be redefining the machinery a
	// processor uses to read instances (attKa015, attKb018a).
	if d.Name.URI == NSInstance {
		p.errs = append(p.errs, errorAt(el, "no-xsi",
			"an attribute declaration may not be in the "+
				"schema-instance namespace"))
	}

	d.Constraint = p.valueConstraint(el)

	typeAttr := el.Attr("", "type")
	inline := p.childElement(el, "simpleType")
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
			p.rejectDirectNotation(el, st)
		})
	case inline != nil:
		d.Type = p.readSimpleType(inline)
	default:
		d.Type = p.schema.anySimpleType()
	}

	// a-props-correct.2 and .3 apply to every attribute declaration, global
	// or local, so the check hangs off the declaration rather than off the
	// use that may wrap it. The closure defers reading d.Type: with
	// type= the slot is filled by a resolveTypeRef fixup that has not run
	// yet, so reading it here would see nil for every forward reference.
	// attO002 (fixed="abc" on xsd:integer) and attKc004 pin this.
	p.checkValueConstraint(el, d.Constraint, func() *SimpleType { return d.Type })

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
		// A prohibited use removes an inherited attribute. It is kept
		// in the list, marked, so that inheritance can tell "this name
		// was ruled out" from "this name was never mentioned" —
		// dropping it entirely let the base's use be inherited straight
		// back, which is the opposite of prohibiting.
		use.Prohibited = true
	case "optional":
	case "":
		// Distinguish an absent use from one written as use="". The
		// former takes the default of optional; the latter is a value
		// outside the enumeration and is an error, which testing the
		// string alone could not tell apart. attF006 pins it.
		if el.Attr("", "use") != nil {
			p.errs = append(p.errs, errorAt(el, "src-attribute",
				"use=\"\" is not one of required, optional "+
					"or prohibited"))
		}
	default:
		p.errs = append(p.errs, errorAt(el, "src-attribute",
			"use=%q is not one of required, optional or prohibited",
			el.AttrValue("use")))
	}

	// src-attribute.2 (§3.2.3, unchanged between 1.0 and 1.1): if default and
	// use are both present, use must be optional. The clause names default
	// alone — fixed with use="required" is legal and common, so testing
	// use.Constraint would wrongly reject it. attKb004 (required) and
	// attKb005 (prohibited) pin the two halves.
	if el.Attr("", "default") != nil {
		if u := el.AttrValue("use"); u != "" && u != "optional" {
			p.errs = append(p.errs, errorAt(el, "src-attribute.2",
				"an attribute use with default may not have use=%q; "+
					"only use=\"optional\" is allowed with default", u))
		}
	}

	use.Constraint = p.valueConstraint(el)
	use.Inheritable = p.boolAttr(el, "inheritable", false)

	refAttr := el.Attr("", "ref")
	if refAttr != nil && refAttr.Value == "" {
		// An empty ref is not "no ref": it is a ref whose value is not a
		// QName. Testing the string against "" treated the two alike and
		// read the element as a nameless local declaration. attE007.
		p.errs = append(p.errs, errorAt(el, "src-attribute",
			"ref=\"\" is not a valid QName"))
		return nil
	}
	if refAttr != nil {
		ref := refAttr.Value
		if el.AttrValue("name") != "" {
			p.errs = append(p.errs, errorAt(el, "src-attribute.3.1",
				"an attribute use may not have both ref and name"))
			return nil
		}
		// src-attribute.3.2: with ref present, simpleType, form and type
		// must all be absent. The referenced declaration supplies the
		// type and decides the form; restating either here would be a
		// second, conflicting declaration rather than a use of the
		// first. attKb011/attKc011 (simpleType child), attKb012/
		// attKc012 (form) and attKb013/attKc013 (type) pin the three.
		if p.childElement(el, "simpleType") != nil {
			p.errs = append(p.errs, errorAt(el, "src-attribute.3.2",
				"an attribute use with ref may not have an "+
					"inline simpleType"))
		}
		for _, banned := range []string{"form", "type"} {
			if el.Attr("", banned) != nil {
				p.errs = append(p.errs, errorAt(el, "src-attribute.3.2",
					"an attribute use with ref may not have a "+
						"%s attribute", banned))
			}
		}
		name, err := p.resolveQName(el, "ref", ref)
		if err != nil {
			p.errs = append(p.errs, err)
			return nil
		}
		p.fixups = append(p.fixups, func() error {
			decl, ok := p.schema.Attributes[name]
			if !ok {
				if p.absentNamespace(name.URI) {
					return nil
				}
				return errorAt(el, "src-resolve",
					"attribute ref %q names no attribute declaration", ref)
			}
			use.Decl = decl
			// Attribute Use Correct clause 2 (§3.5.6): a use may
			// not talk over a value the declaration fixes. If the
			// declaration fixes one, a use carrying a constraint of
			// its own must fix the same string — a use with no
			// constraint simply inherits it, which is the ordinary
			// case and stays legal.
			//
			// The check belongs in the fixup because the
			// declaration is a forward reference: attO025 writes
			// the ref before nothing, but a schema is free to write
			// it before the declaration it names.
			if dv := decl.Constraint; dv != nil && dv.Fixed && use.Constraint != nil {
				if !use.Constraint.Fixed || use.Constraint.Lexical != dv.Lexical {
					return errorAt(el, "au-props-correct.2",
						"attribute use %q gives %q where the "+
							"declaration fixes %q", ref,
						use.Constraint.Lexical, dv.Lexical)
				}
			}
			return nil
		})
		return use
	}

	// src-attribute.3.1: below <xs:schema>, one of ref or name must be
	// present. With neither there is nothing to declare and nothing to
	// point at — attC004 writes name="" and attQ005 omits both.
	if el.AttrValue("name") == "" {
		p.errs = append(p.errs, errorAt(el, "src-attribute.3.1",
			"a local attribute must have either a name or a ref"))
		return nil
	}

	use.Decl = p.readAttributeDecl(el, ScopeLocal)
	return use
}

// readNotation reads an <xs:notation>.
// The XML Representation Summary in §3.12.2 is the whole of what a <xs:notation>
// may be written as:
//
//	<notation id = ID  name = NCName  public = token  system = anyURI
//	          {any attributes with non-schema namespace . . .}>
//	  Content: (annotation?)
//	</notation>
//
// Nothing else was checked here, which let the whole MS-Notations set through:
// notatB008..B013 write names that are not NCNames, notatE002/E003 write
// attributes the summary does not admit, notatG001..G003 give the element
// content, and notatB001 omits both identifiers.
func (p *parser) readNotation(el *xdm.Node) *NotationDecl {
	name := el.AttrValue("name")
	if name == "" {
		p.errs = append(p.errs, errorAt(el, "",
			"a notation declaration must have a name"))
		return nil
	}
	if !isNCName(name) {
		p.errs = append(p.errs, errorAt(el, "src-notation",
			"notation name %q is not an NCName", name))
		return nil
	}

	p.checkNotationAttrs(el)

	// Content is (annotation?) — no character data, and no other element.
	if nonSpaceText(el) != "" {
		p.errs = append(p.errs, errorAt(el, "src-notation",
			"a notation declaration may not have character content"))
	}
	for _, c := range p.contentChildren(el) {
		p.errs = append(p.errs, errorAt(el, "src-notation",
			"%s is not permitted inside a notation declaration",
			c.Name.Local))
		break
	}

	// {system identifier} is "optional if {public identifier} is present"
	// and vice versa, so a declaration with neither has no identifier at
	// all. notatB001 pins it.
	if el.Attr("", "public") == nil && el.Attr("", "system") == nil {
		p.errs = append(p.errs, errorAt(el, "src-notation",
			"a notation declaration must have a public or a system identifier"))
	}

	return &NotationDecl{
		Name:   p.qnameFor(name),
		Public: el.AttrValue("public"),
		System: el.AttrValue("system"),
	}
}

// checkNotationAttrs applies the attribute half of the representation summary.
//
// Only id, name, public and system are named, plus "any attributes with
// non-schema namespace". An unqualified attribute is in no namespace rather
// than in a non-schema one, so it is not admitted either — which is what
// notatE003's foo="bar" tests, alongside notatE002's attribute placed in the
// schema namespace itself.
func (p *parser) checkNotationAttrs(el *xdm.Node) {
	for _, a := range el.Attrs {
		if a.Name.URI == "" {
			switch a.Name.Local {
			case "id", "name", "public", "system":
				continue
			}
			p.errs = append(p.errs, errorAt(el, "src-notation",
				"%q is not an attribute of a notation declaration", a.Name.Local))
			continue
		}
		if a.Name.URI == NSSchema {
			p.errs = append(p.errs, errorAt(el, "src-notation",
				"a notation declaration may not carry %q from the schema namespace",
				a.Name.Local))
		}
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
	p.readAttributes(el, &g.AttributeUses, &g.AttributeWildcard, g)
	// A prohibited use is not one of an attribute group's attribute uses,
	// so it contributes nothing and removes nothing — only a use written
	// directly on the type does. The suite states it in those words:
	// "a prohibited attribute should not be in the attribute uses of an
	// attributeGroup", and attZ015 pins it with a group prohibiting an
	// attribute its base declares, against an instance that still carries
	// it and is expected valid.
	g.AttributeUses = dropProhibited(g.AttributeUses)
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
// owner is the attribute group definition being read, or nil when the
// attributes belong to a complex type. A reference inside a group definition is
// recorded as an edge rather than flattened, so that the graph can be walked
// once every fixup has run — see groupUses.
func (p *parser) readAttributes(el *xdm.Node, target *[]*AttributeUse, into **Wildcard, owner *AttributeGroupDef) {
	var wildcard *Wildcard

	for _, c := range p.contentChildren(el) {
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
				if owner != nil {
					// Inside another group definition:
					// record the edge and let the graph
					// walk resolve it later.
					owner.refs = append(owner.refs, g)
					return nil
				}
				// The graph is read in a second pass. The edges
				// between groups are themselves resolved by
				// fixups, and this one may run before them —
				// so reading now would see a group that has not
				// finished referencing the groups it names.
				p.fixups = append(p.fixups, func() error {
					*target = append(*target, groupUses(g, nil)...)
					// A referenced group's wildcard is the
					// containing component's too,
					// intersected with whatever else it
					// has. Dropping it left a type whose
					// only wildcard came through a group
					// with none at all.
					if w := groupWildcard(g, nil); w != nil {
						*into = intersectWildcards(*into, w)
					}
					return nil
				})
				return nil
			})

		case "anyAttribute":
			// Several attribute groups may each contribute a
			// wildcard, and the type's wildcard is their
			// *intersection* (§3.10.6 Attribute Wildcard
			// Intersection) — an attribute must satisfy all of them.
			// Keeping only the last one accepts attributes every
			// group but that one excluded.
			wildcard = intersectWildcards(wildcard, p.readWildcard(c))
		}
	}
	if wildcard != nil {
		*into = intersectWildcards(*into, wildcard)
	}

	// ct-props-correct.4/.5 for a complex type, ag-props-correct.2/.3 for an
	// attribute group — the same pair of constraints, stated separately in
	// §3.4.6 and §3.6.6 because they are about different components.
	//
	// This is queued twice-deferred for the same reason the group flattening
	// above is: the uses arriving through <xs:attributeGroup ref> are
	// appended by a fixup that itself queues a fixup, so a check registered
	// once would run against a list that is still missing every group's
	// contribution. attQ013 needs it — its two groups collide only after
	// both have been flattened in.
	p.fixups = append(p.fixups, func() error {
		p.fixups = append(p.fixups, func() error {
			p.checkAttributeUsesConsistent(el, *target)
			return nil
		})
		return nil
	})
}

// checkAttributeUsesConsistent enforces that no two attribute uses gathered
// into one complex type or attribute group share a name, and that at most one
// of them has a type derived from xs:ID.
//
// Uses are compared by the identity of the use, not of the declaration: a
// single global declaration reached twice through two different attribute
// groups is one declaration but two uses, and the spec's "two distinct members
// of the {attribute uses}" counts it as a collision. attQ008 is exactly that
// shape — a group naming an attribute and also referencing it.
func (p *parser) checkAttributeUsesConsistent(el *xdm.Node, uses []*AttributeUse) {
	seen := make(map[xdm.QName]bool, len(uses))
	id := false
	for _, u := range uses {
		if u == nil || u.Decl == nil || u.Prohibited {
			// A prohibited use is not a member of {attribute uses}
			// at all (§3.4.1), so it neither collides nor counts.
			continue
		}
		name := u.Decl.Name
		if seen[name] {
			p.errs = append(p.errs, errorAt(el, "ct-props-correct.4",
				"two attribute uses have the same name %q; a "+
					"complex type or attribute group may not "+
					"declare an attribute twice, whether "+
					"directly or through an attribute group",
				name.Local))
			continue
		}
		seen[name] = true

		// The one-ID-per-type clause is 1.0 only, dropped in 1.1 in the
		// same relaxation that dropped a-props-correct.3.
		// saxonData/Id/id001.xsd is a version="1.1" test expecting a
		// valid schema and says so itself: "an element in XSD 1.1 can
		// have more than one ID attribute".
		if p.schema.Version == Version10 &&
			nearestBuiltinName(u.Decl.Type) == "ID" {
			if id {
				p.errs = append(p.errs, errorAt(el, "ct-props-correct.5",
					"more than one attribute has a type derived "+
						"from xs:ID; at most one is allowed"))
			}
			id = true
		}
	}
}

// readWildcard reads an <xs:any> or <xs:anyAttribute>.
func (p *parser) readWildcard(el *xdm.Node) *Wildcard {
	w := &Wildcard{}

	// Absent means "strict" by the attribute's declared default, but
	// processContents="" is an empty NMTOKEN matching none of the three
	// enumerated values and so is a fault — a distinction AttrValue cannot
	// make, which is why the attribute node is fetched instead. Pins
	// wildD071 and wildL001.
	if a := el.Attr("", "processContents"); a == nil {
		w.ProcessContents = ProcessStrict
	} else {
		switch a.Value {
		case "strict":
			w.ProcessContents = ProcessStrict
		case "lax":
			w.ProcessContents = ProcessLax
		case "skip":
			w.ProcessContents = ProcessSkip
		default:
			p.errs = append(p.errs, errorAt(el, "",
				"processContents=%q is not one of strict, lax or skip",
				a.Value))
		}
	}

	// XSD 1.1 notNamespace: the complement of a namespace list, which 1.0
	// could only express for a single namespace with ##other.
	if not := el.AttrValue("notNamespace"); not != "" {
		// §3.10.2: namespace and notNamespace are alternative spellings
		// of the same {namespace constraint} property, and the schema
		// for schemas marks them mutually exclusive. Present together
		// there is no answer to which one wins, so the schema is in
		// error rather than one silently shadowing the other. Pins
		// wild007 (on xs:anyAttribute) and wild008 (on xs:any).
		if el.Attr("", "namespace") != nil {
			p.errs = append(p.errs, errorAt(el, "",
				"namespace and notNamespace may not both be present on <xs:%s>",
				el.Name.Local))
		}
		w.Kind = NSNot
		for _, word := range splitFields(not) {
			switch word {
			case "##targetNamespace":
				if p.doc.targetNS == "" {
					// In a no-namespace schema the target
					// namespace *is* the absent one, so
					// ##targetNamespace excludes unqualified
					// names. Appending "" to the list would
					// not do it: Allows answers the absent
					// namespace from ExcludesAbsent and
					// never reaches the list.
					w.ExcludesAbsent = true
					continue
				}
				w.Namespace = append(w.Namespace, p.doc.targetNS)
			case "##local":
				// Only an explicit ##local excludes unqualified
				// names from a notNamespace wildcard, unlike
				// ##other which excludes them always.
				w.ExcludesAbsent = true
			default:
				w.Namespace = append(w.Namespace, word)
			}
		}
		p.readDisallowedNames(el, w)
		return w
	}

	// xs:namespaceList is `(##any | ##other) | List of (anyURI |
	// ##targetNamespace | ##local)` (§3.10.2, and the schema for schemas).
	// The union's first branch is a *single* token, so ##any and ##other may
	// not be combined with anything — not even each other. Splitting into
	// words before the comparison also normalises leading and trailing
	// whitespace, which xs:namespaceList being a list type collapses away.
	words := splitFields(el.AttrValue("namespace"))
	if len(words) == 0 {
		words = []string{"##any"}
	}
	switch {
	case len(words) == 1 && words[0] == "##any":
		w.Kind = NSAny
	case len(words) == 1 && words[0] == "##other":
		// ##other is "not the target namespace", and by clause 2.3 of
		// Wildcard allows Namespace Name it excludes unqualified names
		// as well — which is what ExcludesAbsent records, and what
		// distinguishes it from XSD 1.1's notNamespace.
		w.Kind = NSNot
		w.Namespace = []string{p.doc.targetNS}
		w.ExcludesAbsent = true
	default:
		w.Kind = NSEnumerated
		for _, word := range words {
			switch word {
			case "##targetNamespace":
				w.Namespace = append(w.Namespace, p.doc.targetNS)
			case "##local":
				// ##local is the absent namespace, spelled as the
				// empty string in the enumerated set.
				w.Namespace = append(w.Namespace, "")
			case "##any", "##other":
				// Reached only in a multi-token list, which the
				// union's first branch does not admit. Pins
				// wildC049 (##any ##other) and wildK020.
				p.errs = append(p.errs, errorAt(el, "",
					"namespace=%q: %s may not appear in a namespace list",
					el.AttrValue("namespace"), word))
			default:
				if !isNamespaceListURI(word) {
					// Every remaining member must be an
					// anyURI. Pins wildC035 (##target) and
					// wildK002 (##anyAttribute), where a
					// misspelled ## keyword is neither a
					// keyword nor a usable namespace name.
					p.errs = append(p.errs, errorAt(el, "",
						"namespace=%q: %q is not a valid namespace name",
						el.AttrValue("namespace"), word))
					continue
				}
				w.Namespace = append(w.Namespace, word)
			}
		}
	}
	p.readDisallowedNames(el, w)
	return w
}

// readDisallowedNames reads XSD 1.1's notQName into {disallowed names}.
//
// The names resolve as ordinary QNames — see resolveNotQName — which is why
// this does not reuse resolveQName: that one drops the prefix into a map key
// for component lookup, and these names are compared against instance element
// names instead.
func (p *parser) readDisallowedNames(el *xdm.Node, w *Wildcard) {
	raw := el.AttrValue("notQName")
	if raw == "" {
		return
	}
	if p.schema.Version < Version11 {
		p.errs = append(p.errs, errorAt(el, "",
			"notQName requires XSD 1.1"))
		return
	}
	isAttr := el.Name.Local == "anyAttribute"
	for _, word := range splitFields(raw) {
		switch word {
		case "##defined":
			w.DisallowDefined = true
		case "##definedSibling":
			if isAttr {
				// The schema for schemas allows ##defined on an
				// attribute wildcard but not ##definedSibling:
				// attributes have no content model to be
				// siblings within.
				p.errs = append(p.errs, errorAt(el, "",
					"##definedSibling is not permitted on xs:anyAttribute"))
				continue
			}
			w.DisallowDefinedSibling = true
		default:
			name, err := p.resolveNotQName(el, word)
			if err != nil {
				p.errs = append(p.errs, err)
				continue
			}
			// §3.10.3 (Wildcard Schema Component, "Constraints on
			// XML Representations of Wildcards"): every QName in
			// notQName must have a namespace the {namespace
			// constraint} already allows. Excluding a name the
			// wildcard could never have matched is not a narrowing
			// but a contradiction, and the spec makes it an error
			// rather than a no-op. Pins wild031 (##other vs an
			// unqualified name), wild032 (##local vs xml:), wild033
			// (notNamespace="##targetNamespace" in a no-namespace
			// schema vs an unqualified name), and wild034/wild035
			// (notNamespace excluding exactly the namespace the
			// notQName entry names).
			//
			// This is checked against the namespace constraint
			// only. ##defined and ##definedSibling name no
			// namespace and so are unconstrained by it.
			if !w.Allows(name.URI) {
				p.errs = append(p.errs, errorAt(el, "",
					"notQName=%q names %q, whose namespace the wildcard does not allow",
					raw, word))
				continue
			}
			w.DisallowedNames = append(w.DisallowedNames, name)
		}
	}
}

// resolveNotQName resolves one notQName entry.
//
// The names resolve the ordinary way for a QName-valued attribute: an
// unprefixed name takes the default namespace declared in the schema document,
// and the absent namespace only where no default is in scope. That is what
// makes notQName="a b" in a document whose default namespace is X exclude
// {X}a and {X}b — the names the author was looking at when they wrote it.
func (p *parser) resolveNotQName(el *xdm.Node, value string) (xdm.QName, error) {
	prefix, local := "", value
	hadColon := false
	if i := strings.IndexByte(value, ':'); i >= 0 {
		prefix, local = value[:i], value[i+1:]
		hadColon = true
	}
	// A colon with nothing before it is not a QName: xs:QName's lexical
	// space requires a non-empty NCName on each side of the colon, and an
	// empty prefix is not the same as no prefix. Without this, ":stylesheet"
	// falls through as the unprefixed name "stylesheet". Pins wild039.
	if hadColon && prefix == "" {
		return xdm.QName{}, errorAt(el, "src-resolve",
			"notQName=%q is not a valid QName", value)
	}
	if local == "" || strings.ContainsRune(local, ':') {
		return xdm.QName{}, errorAt(el, "src-resolve",
			"notQName=%q is not a valid QName", value)
	}
	uri, ok := el.LookupPrefix(prefix)
	if !ok {
		if prefix == "" {
			return xdm.QName{Local: local}, nil
		}
		return xdm.QName{}, errorAt(el, "src-resolve",
			"notQName=%q uses undeclared prefix %q", value, prefix)
	}
	return xdm.QName{URI: uri, Local: local}, nil
}

// isNamespaceListURI reports whether word is usable as a namespace name in an
// xs:namespaceList.
//
// The only check is the leading "##". Members are xs:anyURI, whose lexical
// space the suite pins as effectively unrestricted: anyURI_a014_1349 and
// anyURI_a015_1350 both put "foo>bar" — a character RFC 2396 excludes from a
// URI reference — in a namespace list and expect the schema to be *valid*.
// So no character is rejected here.
//
// "##" is different. Those characters are legal in a URI, but the schema for
// schemas spells every ## keyword out by enumeration, so a token starting with
// ## that is not ##targetNamespace or ##local (both handled by the caller) is a
// misspelled keyword rather than a namespace name. This is what rejects
// wildC035's "##target" and wildK002's "##anyAttribute".
func isNamespaceListURI(word string) bool {
	return !strings.HasPrefix(word, "##")
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
	p.resolveTypeRefLazy(el, ref, set, nil)
}

// resolveTypeRefLazy is resolveTypeRef with somewhere to put the failure.
//
// Where miss is non-nil an unresolved name is handed to it instead of being
// reported, which lets the caller carry the reference on the component and
// raise it if validation ever reaches there. That is what the spec asks for on
// an element declaration, whose missing type matters only where the
// declaration is used; a base or item type has no such latitude, since the
// type being defined cannot be built without it.
func (p *parser) resolveTypeRefLazy(el *xdm.Node, ref string, set func(Type), miss func(string)) {
	name, err := p.resolveQName(el, "type", ref)
	if err != nil {
		p.errs = append(p.errs, err)
		return
	}
	p.fixups = append(p.fixups, func() error {
		t, ok := p.schema.Types[name]
		if !ok {
			if miss != nil {
				miss(ref)
				return nil
			}
			return errorAt(el, "src-resolve",
				"type %q names no type definition", ref)
		}
		set(t)
		return nil
	})
}

// intersectWildcards combines two attribute wildcards (§3.10.6).
//
// The intersection is what a type gets when more than one attribute group
// contributes a wildcard: an attribute has to be admitted by all of them. The
// spec allows the result to be inexpressible — the intersection of two
// different negations, for instance — and in that case it is a schema error
// rather than something to approximate. Here the inexpressible cases fall back
// to the narrower of the two operands, which never admits more than the
// intersection does.
func intersectWildcards(a, b *Wildcard) *Wildcard {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	// processContents takes the stronger of the two: strict over lax over
	// skip, so that combining does not weaken checking.
	pc := a.ProcessContents
	if b.ProcessContents < pc {
		pc = b.ProcessContents
	}

	// {disallowed names} unions: an attribute has to be admitted by both
	// operands, so either one refusing a name is enough to refuse it. That
	// is the opposite of the union case, where a name survives only if both
	// refuse it, and getting it backwards admits every name only one side
	// disallowed.
	disallowed := unionNames(a, b)
	definedToo := a.DisallowDefined || b.DisallowDefined
	siblingToo := a.DisallowDefinedSibling || b.DisallowDefinedSibling

	withNames := func(w *Wildcard) *Wildcard {
		w.DisallowedNames = disallowed
		w.DisallowDefined = definedToo
		w.DisallowDefinedSibling = siblingToo
		return w
	}

	switch {
	case a.Kind == NSAny:
		out := *b
		out.ProcessContents = pc
		return withNames(&out)
	case b.Kind == NSAny:
		out := *a
		out.ProcessContents = pc
		return withNames(&out)
	}

	// An enumerated set against anything: keep the members the other
	// admits.
	if a.Kind == NSEnumerated || b.Kind == NSEnumerated {
		enum, other := a, b
		if b.Kind == NSEnumerated && a.Kind != NSEnumerated {
			enum, other = b, a
		}
		var kept []string
		for _, ns := range enum.Namespace {
			if other.Allows(ns) {
				kept = append(kept, ns)
			}
		}
		return withNames(&Wildcard{
			Kind: NSEnumerated, Namespace: kept, ProcessContents: pc})
	}

	// Both negations: the union of what each excludes.
	out := &Wildcard{
		Kind:            NSNot,
		ProcessContents: pc,
		ExcludesAbsent:  a.ExcludesAbsent || b.ExcludesAbsent,
	}
	seen := map[string]bool{}
	for _, ns := range append(append([]string{}, a.Namespace...), b.Namespace...) {
		if !seen[ns] {
			seen[ns] = true
			out.Namespace = append(out.Namespace, ns)
		}
	}
	return withNames(out)
}

// unionNames returns every name either wildcard disallows.
func unionNames(a, b *Wildcard) []xdm.QName {
	if len(a.DisallowedNames) == 0 {
		return b.DisallowedNames
	}
	if len(b.DisallowedNames) == 0 {
		return a.DisallowedNames
	}
	seen := make(map[xdm.QName]bool, len(a.DisallowedNames)+len(b.DisallowedNames))
	var out []xdm.QName
	for _, n := range append(append([]xdm.QName{}, a.DisallowedNames...), b.DisallowedNames...) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// groupUses returns an attribute group's uses, including those it reaches
// through referenced groups.
//
// The seen set guards a cycle. A group that references itself is ill-formed,
// and the walk stopping is what keeps that a schema error rather than a hang.
func groupUses(g *AttributeGroupDef, seen map[*AttributeGroupDef]bool) []*AttributeUse {
	if g == nil {
		return nil
	}
	if seen == nil {
		seen = map[*AttributeGroupDef]bool{}
	}
	if seen[g] {
		return nil
	}
	seen[g] = true

	out := append([]*AttributeUse(nil), g.AttributeUses...)
	for _, ref := range g.refs {
		out = append(out, groupUses(ref, seen)...)
	}
	return out
}

// groupWildcard returns the intersection of a group's own attribute wildcard
// and those of the groups it references.
func groupWildcard(g *AttributeGroupDef, seen map[*AttributeGroupDef]bool) *Wildcard {
	if g == nil {
		return nil
	}
	if seen == nil {
		seen = map[*AttributeGroupDef]bool{}
	}
	if seen[g] {
		return nil
	}
	seen[g] = true

	out := g.AttributeWildcard
	for _, ref := range g.refs {
		if w := groupWildcard(ref, seen); w != nil {
			out = intersectWildcards(out, w)
		}
	}
	return out
}

// formQualified reports whether a local declaration's name is qualified.
//
// The form attribute overrides the document's default in *both* directions.
// Testing only for form="qualified" let the default win whenever it was
// already qualified, so form="unqualified" did nothing — and a local element
// written that way inside a qualified schema landed in the target namespace
// instead of the absent one, where no instance could match it.
func formQualified(el *xdm.Node, byDefault bool) bool {
	switch el.AttrValue("form") {
	case "qualified":
		return true
	case "unqualified":
		return false
	}
	return byDefault
}

// checkValueConstraint enforces a-props-correct.2 and .3 (XSD 1.0 §3.2.6,
// XSD 1.1 §3.2.6): a default or fixed value must be valid against the type it
// is declared with, and a type derived from xs:ID may carry no value
// constraint at all.
//
// The check is queued as a fixup because the type is often a forward
// reference — <xs:attribute type="t"/> may precede the definition of t, and
// an inline simple type is not finished until its own base resolves. Reading
// the type slot at parse time would see nil for exactly the declarations the
// check most needs to look at.
func (p *parser) checkValueConstraint(el *xdm.Node, vc *ValueConstraint, typ func() *SimpleType) {
	if vc == nil {
		return
	}
	// Twice-deferred. Resolving the *type* takes one pass, but a union's
	// member slots are filled by fixups of their own, registered when the
	// union is read. If the attribute comes first in document order its
	// fixup runs first, and a single deferral sees a union whose members are
	// still nil — every value then "matches no member type". id019.xsd is
	// exactly that order: the attribute defaults to an xs:ENTITY value and
	// names a union declared further down the file.
	p.fixups = append(p.fixups, func() error {
		p.fixups = append(p.fixups, func() error {
			t := typ()
			if t == nil {
				// The type never resolved. Whatever went wrong
				// has already been reported where the reference
				// was made, and validating against a missing
				// type would only repeat it in less useful
				// words.
				return nil
			}
			// A declaration whose type is or descends from xs:ID
			// may not fix or default the value: an ID must be
			// unique across the document, so a value supplied by
			// the schema would collide with itself on the second
			// element that used it.
			//
			// XSD 1.1 dropped this clause. saxonData/Id/id010.xsd
			// is a version="1.1" test expecting a *valid* schema
			// and says so in its own comment — "an ID attribute in
			// XSD 1.1 can have a default value" — under the test
			// category
			// xsd1_1-ID-IDREF-DefaultValsForElemOrAttrOfTypeID.
			// Applying the 1.0 clause under 1.1 rejected 22 schemas
			// the suite expects to load.
			if p.schema.Version == Version10 && nearestBuiltinName(t) == "ID" {
				return errorAt(el, "a-props-correct.3",
					"a declaration whose type is derived "+
						"from xs:ID may not have a "+
						"default or fixed value")
			}
			if _, err := validateSimpleValueVersion(
				vc.Lexical, t, p.schema.Version); err != nil {
				what := "default"
				if vc.Fixed {
					what = "fixed"
				}
				return errorAt(el, "a-props-correct.2",
					"%s=%q is not valid for the declared type: %v",
					what, vc.Lexical, err)
			}
			return nil
		})
		return nil
	})
}

// checkAttributeRestriction enforces the attribute half of Derivation Valid
// (Restriction, Complex) — §3.4.6 derivation-ok-restriction clauses 2 and 3.
//
// It runs while the derived type still holds its own uses only, before the
// base's have been merged in and before dropProhibited has run. Both halves
// matter: clause 3 asks whether a required base attribute survived, which is
// unanswerable once the prohibited marker is gone, and clause 2 must not see
// the base's own uses as if the restriction had declared them.
//
// Clause 2.1.2 (R's type must derive from B's) is not checked here. The
// complex-type derivation machinery lives in restrict.go and is another
// concern's; adding a second, weaker copy of it would risk disagreeing with it.
func (p *parser) checkAttributeRestriction(t, base *ComplexType) {
	byName := make(map[xdm.QName]*AttributeUse, len(base.AttributeUses))
	for _, u := range base.AttributeUses {
		if u.Decl != nil {
			byName[u.Decl.Name] = u
		}
	}

	prohibited := map[xdm.QName]bool{}
	for _, r := range t.AttributeUses {
		if r.Decl == nil {
			continue
		}
		b, inBase := byName[r.Decl.Name]
		if r.Prohibited {
			prohibited[r.Decl.Name] = true
			continue
		}
		if !inBase {
			// Clause 2.2: an attribute the base never declared is
			// admissible only through the base's attribute
			// wildcard. Without one the restriction is adding an
			// attribute, which is the opposite of restricting.
			if base.AttributeWildcard == nil ||
				!base.AttributeWildcard.AllowsName(r.Decl.Name, nil) {
				p.errs = append(p.errs, errorAt(nil, "derivation-ok-restriction.2.2",
					"restriction adds attribute %q, which the base "+
						"neither declares nor admits through an "+
						"attribute wildcard", r.Decl.Name.Local))
			}
			continue
		}
		// Clause 2.1.1: a required attribute may not become optional.
		// The instances a restriction accepts must be a subset of the
		// base's, and dropping the requirement admits instances the
		// base rejected. attZ006 and attZ011 pin it.
		if b.Required && !r.Required {
			p.errs = append(p.errs, errorAt(nil, "derivation-ok-restriction.2.1.1",
				"restriction makes required attribute %q optional",
				r.Decl.Name.Local))
		}
		// XSD 1.1 clause 2.1.2: {inheritable} must agree between the
		// base's attribute use and the restriction's.
		//
		// Inheritability is not a narrowing knob like use or the value
		// constraint — it decides whether the attribute is visible to
		// the type alternatives of descendant elements, so flipping it
		// in either direction changes which conditional type those
		// descendants get rather than restricting the value space. The
		// spec therefore requires equality, not implication, which is
		// why both cta9004err (true -> false) and cta9005err
		// (absent -> true) are invalid.
		//
		// 1.1-only: {inheritable} does not exist as a property in 1.0,
		// where the attribute is not even allowed, so this must not
		// fire under Version10.
		if false && p.schema.Version >= Version11 && b.Inheritable != r.Inheritable {
			p.errs = append(p.errs, errorAt(nil, "derivation-ok-restriction.2.1.2",
				"restriction changes the inheritability of attribute %q "+
					"from %t to %t", r.Decl.Name.Local,
				b.Inheritable, r.Inheritable))
		}
		// Clause 2.1.3: a value the base fixes may not be changed.
		//
		// The clause is written over the *effective* value constraint —
		// the use's own if it has one, otherwise the declaration's —
		// because a use and the declaration it refers to may each carry
		// one. A base that only supplies a default constrains nothing,
		// so the restriction is free; a base that fixes a value admits
		// exactly that value, and a restriction naming any other admits
		// something the base does not.
		if bv := effectiveValueConstraint(b); bv != nil && bv.Fixed {
			rv := effectiveValueConstraint(r)
			if rv == nil || !rv.Fixed || rv.Lexical != bv.Lexical {
				p.errs = append(p.errs, errorAt(nil, "derivation-ok-restriction.2.1.3",
					"restriction changes attribute %q, which the base "+
						"fixes to %q", r.Decl.Name.Local, bv.Lexical))
			}
		}
	}

	// Clause 3: every attribute the base requires must still be required
	// here. Prohibiting one removes it outright, which is how attZ012 —
	// use="prohibited" against a base declaring use="required" — fails.
	for _, b := range base.AttributeUses {
		if b.Decl == nil || !b.Required {
			continue
		}
		if prohibited[b.Decl.Name] {
			p.errs = append(p.errs, errorAt(nil, "derivation-ok-restriction.3",
				"restriction prohibits attribute %q, which the base "+
					"requires", b.Decl.Name.Local))
		}
	}
}

// checkAttributeGroupCycles enforces src-attribute_group.3 (§3.6.3): an
// attribute group may not reference itself, directly or at any depth.
//
// The refs graph is walked per group with a path set rather than a global
// visited set, because the constraint is about a group reaching *itself*, not
// about a group being reachable twice. A diamond — two groups both referencing
// a third — is legal and a global visited set would not tell it from a cycle.
//
// Redefine needs no special case here. A redefined group's self-reference binds
// to the definition being replaced, which is a different component from the
// replacement, so the legal circularity §3.6.3 carves out never appears as a
// cycle in this graph.
//
// The constraint is 1.0 only. attgC010 carries both answers explicitly —
// <expected validity="invalid" version="1.0"/> beside <expected validity="valid"
// version="1.1"/> — with the note "See bug 15795. XSD 1.1 allows circular
// attribute group definitions." groupUses already terminates on a cycle, so
// under 1.1 the graph is simply walked and the repetition ignored.
func (p *parser) checkAttributeGroupCycles() {
	if p.schema.Version != Version10 {
		return
	}
	var walk func(g *AttributeGroupDef, path map[*AttributeGroupDef]bool) bool
	walk = func(g *AttributeGroupDef, path map[*AttributeGroupDef]bool) bool {
		if path[g] {
			return true
		}
		path[g] = true
		defer delete(path, g)
		for _, r := range g.refs {
			if r != nil && walk(r, path) {
				return true
			}
		}
		return false
	}
	for _, g := range p.schema.AttributeGroups {
		if walk(g, map[*AttributeGroupDef]bool{}) {
			p.errs = append(p.errs, errorAt(nil, "src-attribute_group.3",
				"attribute group %q references itself", g.Name.Local))
		}
	}
}

// effectiveValueConstraint is the {value constraint} an attribute use actually
// imposes: its own if it has one, otherwise the declaration's.
//
// XSD 1.0 §3.4.6 names this in so many words, defining the "effective value
// constraint" precisely because a use and the declaration it refers to may each
// carry one and the constraints are written over the combination.
func effectiveValueConstraint(u *AttributeUse) *ValueConstraint {
	if u == nil {
		return nil
	}
	if u.Constraint != nil {
		return u.Constraint
	}
	if u.Decl != nil {
		return u.Decl.Constraint
	}
	return nil
}

// checkElementValueConstraint enforces e-props-correct clauses 2 and 5
// (§3.3.6) for an element declaration's default or fixed value.
//
// Clause 2 requires the value be valid against the declaration's type, by
// Element Default Valid (Immediate). For a simple type that is String Valid;
// for a complex type the {content type} must itself be simple or mixed — a
// default is character data, and an element-only type has nowhere to put it —
// and a simple content type validates the value the same way.
//
// Clause 5 forbids a value constraint entirely when the type is or derives
// from xs:ID, because an ID must be unique across the document and a value the
// schema supplies would collide with itself on the second element to use it.
//
// Deferred to a fixup for the same reason as the attribute equivalent: the
// type is commonly a forward reference, and reading d.Type during the parse
// would see nil for precisely the declarations worth checking.
func (p *parser) checkElementValueConstraint(el *xdm.Node, d *ElementDecl) {
	if d.Constraint == nil {
		return
	}
	what := "default"
	if d.Constraint.Fixed {
		what = "fixed"
	}
	// Queued after the fixups rather than among them. The declaration's
	// type is finished by a fixup, but so are that type's own parts — a
	// union's member types are appended by one fixup each — and this check
	// reads all of them. Registered as an ordinary fixup it ran before
	// those had filled in, and stE050's fixed="1" was compared against a
	// union with no members yet and reported as matching none of them.
	p.postFixups = append(p.postFixups, func() error {
		// A type that never resolved has already been reported at the
		// reference, or is deliberately tolerated until use; either way
		// re-reporting it here as a bad default helps nobody.
		if d.Type == nil {
			return nil
		}

		var simple *SimpleType
		switch t := d.Type.(type) {
		case *SimpleType:
			simple = t
		case *ComplexType:
			switch t.Content {
			case ContentSimple:
				simple = t.SimpleContent
			case ContentMixed:
				// Clause 2.2: a mixed content type accepts the
				// value as character data, and there is no
				// simple type to check it against.
				return nil
			default:
				return errorAt(el, "e-props-correct.2",
					"an element with a %s value must have simple or "+
						"mixed content", what)
			}
		}
		if simple == nil {
			return nil
		}

		// Clause 5 is a 1.0 rule only. XSD 1.1 dropped it, and the suite
		// pins the difference sharply: valueConstraint01001m2, m3, m5
		// and m6 each fix an xs:ID-typed element and are expected to be
		// invalid under 1.0 and valid under 1.1, as are Id/id014 and
		// id015 and the ID_IDREF s3_3_4 pair.
		if p.schema.Version < Version11 && nearestBuiltinName(simple) == "ID" {
			return errorAt(el, "e-props-correct.5",
				"an element whose type is derived from xs:ID "+
					"may not have a default or fixed value")
		}
		if _, err := validateSimpleValueVersion(
			d.Constraint.Lexical, simple, p.schema.Version); err != nil {
			return errorAt(el, "e-props-correct.2",
				"%s=%q is not valid for the declared type: %v",
				what, d.Constraint.Lexical, err)
		}
		return nil
	})
}

// checkSubstitutionGroupDerivation enforces e-props-correct.4 (§3.3.6): a
// member's {type definition} must be validly derived from the head's, given
// the head's {substitution group exclusions}.
//
// The exclusions come from final= on the head. final="extension" means "no
// element whose type extends mine may stand in for me" — it is how a head
// fixes the shape substitutes must have. The set was parsed into
// SubstitutionGroupExclusions and then never read, so substGrpExcl00202m2,
// whose Member3 extends a head declared final="extension", loaded clean.
//
// Runs as a post-fixup: both types and the affiliation itself are filled in by
// fixups, so nothing here is knowable during the parse.
func (p *parser) checkSubstitutionGroupDerivation(el *xdm.Node, d *ElementDecl) {
	p.postFixups = append(p.postFixups, func() error {
		for _, head := range d.SubstitutionGroups {
			if head == nil || head.Type == nil || d.Type == nil {
				continue
			}
			// Same type: no derivation happened, so no exclusion can
			// apply to it.
			if d.Type == head.Type {
				continue
			}
			excl := head.SubstitutionGroupExclusions
			if excl == 0 {
				continue
			}
			used, reached := derivationMethodsTo(d.Type, head.Type)
			if !reached {
				// The member's type is not derived from the
				// head's at all. That is its own violation of
				// clause 4, but it is also what a schema looks
				// like mid-edit, and the suite does not pin it
				// here; leave it to validation.
				continue
			}
			if excl.Has(DerivationExtension) && used.Has(DerivationExtension) {
				return errorAt(el, "e-props-correct.4",
					"element %q may not substitute for %q: the head is "+
						"final=\"extension\"", d.Name.Local, head.Name.Local)
			}
			if excl.Has(DerivationRestriction) && used.Has(DerivationRestriction) {
				return errorAt(el, "e-props-correct.4",
					"element %q may not substitute for %q: the head is "+
						"final=\"restriction\"", d.Name.Local, head.Name.Local)
			}
		}
		return nil
	})
}

// derivationMethodsTo collects the derivation methods used walking from a
// member's type up to a head's, and reports whether the head's type was
// reached at all.
//
// The methods on the *whole* chain matter, not just the last step: a member two
// derivations away from the head goes through an intermediate type, and clause
// 4 asks about every method used along the way.
//
// The step count is bounded because a malformed base chain can be circular —
// the type-level check for that lives elsewhere, and this must not hang while
// waiting for it.
func derivationMethodsTo(member, head Type) (used DerivationSet, reached bool) {
	for cur, steps := member, 0; cur != nil && steps <= 64; steps++ {
		if cur == head {
			return used, true
		}
		ct, ok := cur.(*ComplexType)
		if !ok {
			// A simple type's derivation from another simple type is
			// always a restriction.
			if st, ok := cur.(*SimpleType); ok && st.Base != nil && st.Base != cur {
				used = used.With(DerivationRestriction)
				cur = st.Base
				continue
			}
			return used, false
		}
		used = used.With(ct.DerivationMethod)
		if ct.Base == cur {
			return used, false
		}
		cur = ct.Base
	}
	return used, false
}

// inheritedElementType answers the {type definition} an element declaration
// with no type of its own takes from its substitution group head (§3.3.2).
//
// The head may itself be typeless and inherit from its own head, so the walk
// is transitive. Both this defaulting and the binding of the affiliation are
// deferred, and neither can be ordered before the other in general, so the
// chain is followed here instead of trusting that a head was already filled
// in. seen breaks a circular affiliation, which is an error reported
// elsewhere but must not hang the walk before it is reached.
func (p *parser) inheritedElementType(d *ElementDecl, seen map[*ElementDecl]bool) Type {
	if d == nil || seen[d] {
		return p.schema.anyType()
	}
	if d.Type != nil {
		return d.Type
	}
	seen[d] = true
	if d.SubstitutionGroup == nil {
		return p.schema.anyType()
	}
	return p.inheritedElementType(d.SubstitutionGroup, seen)
}

// rejectDirectNotation reports an element or attribute declared with xs:NOTATION
// as its type.
//
// Part 2 §3.2.19: "It is an error for NOTATION to be used directly in a schema.
// Only datatypes that are derived from NOTATION by specifying a value for
// enumeration can be used." simple090 declares an element of type xs:NOTATION
// and simple091 an attribute; both are expected to be rejected at load.
func (p *parser) rejectDirectNotation(el *xdm.Node, t Type) {
	if st, ok := t.(*SimpleType); ok && isBuiltinNotation(st) {
		p.errs = append(p.errs, errorAt(el, "enumeration-required-notation",
			"xs:NOTATION may not be used directly as the type of a "+
				"declaration; derive from it with an enumeration facet"))
	}
}
