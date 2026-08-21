package xsd

import (
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// readSimpleType reads an <xs:simpleType>.
func (p *parser) readSimpleType(el *xdm.Node) *SimpleType {
	t := &SimpleType{Facets: &FacetSet{}}
	if name := el.AttrValue("name"); name != "" {
		t.Name = p.qnameFor(name)
	}
	if f, err := p.derivationSet(el, "final"); err != nil {
		p.errs = append(p.errs, err)
	} else if el.Attr("", "final") != nil {
		t.FinalSet = f
	} else {
		t.FinalSet = p.doc.finalDefault
	}

	body := childElement(el, "restriction", "list", "union")
	if body == nil {
		p.errs = append(p.errs, errorAt(el, "src-simple-type.1",
			"a simpleType must have a restriction, list or union child"))
		return t
	}

	switch body.Name.Local {
	case "restriction":
		t.Variety = VarietyAtomic
		p.readSimpleRestriction(body, t)
	case "list":
		t.Variety = VarietyList
		p.readSimpleList(body, t)
	case "union":
		t.Variety = VarietyUnion
		p.readSimpleUnion(body, t)
	}
	return t
}

// readSimpleRestriction reads <xs:restriction> inside a simpleType.
func (p *parser) readSimpleRestriction(el *xdm.Node, t *SimpleType) {
	base := el.AttrValue("base")
	inline := childElement(el, "simpleType")
	switch {
	case base != "" && inline != nil:
		p.errs = append(p.errs, errorAt(el, "src-simple-type.2",
			"a restriction may not have both a base attribute and an "+
				"inline simpleType"))
	case base != "":
		p.resolveTypeRef(el, base, func(bt Type) {
			st, ok := bt.(*SimpleType)
			if !ok {
				p.errs = append(p.errs, errorAt(el, "src-resolve",
					"simpleType restriction base %q is a complex type", base))
				return
			}
			t.Base = st
			// The variety and primitive are inherited from the base:
			// restricting a list yields a list, not an atomic type.
			t.Variety = st.Variety
			t.Primitive = st.Primitive
			t.ItemType = st.ItemType
			t.MemberTypes = st.MemberTypes
		})
	case inline != nil:
		st := p.readSimpleType(inline)
		t.Base = st
		t.Variety = st.Variety
		t.Primitive = st.Primitive
		t.ItemType = st.ItemType
		t.MemberTypes = st.MemberTypes
	default:
		p.errs = append(p.errs, errorAt(el, "src-simple-type.2",
			"a restriction must have a base attribute or an inline simpleType"))
	}

	p.readFacets(el, t.Facets)
}

// readSimpleList reads <xs:list>.
func (p *parser) readSimpleList(el *xdm.Node, t *SimpleType) {
	item := el.AttrValue("itemType")
	inline := childElement(el, "simpleType")
	switch {
	case item != "" && inline != nil:
		p.errs = append(p.errs, errorAt(el, "src-simple-type.3",
			"a list may not have both an itemType attribute and an "+
				"inline simpleType"))
	case item != "":
		p.resolveTypeRef(el, item, func(bt Type) {
			st, ok := bt.(*SimpleType)
			if !ok {
				p.errs = append(p.errs, errorAt(el, "src-resolve",
					"list itemType %q is a complex type", item))
				return
			}
			t.ItemType = st
		})
	case inline != nil:
		t.ItemType = p.readSimpleType(inline)
	default:
		p.errs = append(p.errs, errorAt(el, "src-simple-type.3",
			"a list must have an itemType attribute or an inline simpleType"))
	}
	t.Base = p.schema.anySimpleType()

	// A list collapses whitespace, and the facet is fixed: the items are
	// separated by whitespace, so preserving it would make the separator
	// ambiguous.
	collapse := WhiteCollapse
	t.Facets.WhiteSpace = &collapse
	t.Facets.WhiteSpaceFixed = true
}

// readSimpleUnion reads <xs:union>.
//
// Member order is significant: validation takes the first member whose lexical
// space accepts the value, not the best or longest match, and that member
// becomes the value's type.
func (p *parser) readSimpleUnion(el *xdm.Node, t *SimpleType) {
	if list := el.AttrValue("memberTypes"); list != "" {
		for _, ref := range splitFields(list) {
			name, err := p.resolveQName(el, "memberTypes", ref)
			if err != nil {
				p.errs = append(p.errs, err)
				continue
			}
			// Each member is appended in order by its own fixup, and
			// the fixups run in the order they were registered, so
			// declaration order survives.
			slot := len(t.MemberTypes)
			t.MemberTypes = append(t.MemberTypes, nil)
			p.fixups = append(p.fixups, func() error {
				bt, ok := p.schema.Types[name]
				if !ok {
					return errorAt(el, "src-resolve",
						"union memberTypes names no type %q", ref)
				}
				st, ok := bt.(*SimpleType)
				if !ok {
					return errorAt(el, "src-resolve",
						"union member %q is a complex type", ref)
				}
				t.MemberTypes[slot] = st
				return nil
			})
		}
	}
	for _, c := range contentChildren(el) {
		if c.IsElement(NSSchema, "simpleType") {
			t.MemberTypes = append(t.MemberTypes, p.readSimpleType(c))
		}
	}
	if len(t.MemberTypes) == 0 {
		p.errs = append(p.errs, errorAt(el, "src-simple-type.4",
			"a union must have memberTypes or inline simpleType children"))
	}
	t.Base = p.schema.anySimpleType()
}

// readFacets reads the constraining facet children of a restriction.
func (p *parser) readFacets(el *xdm.Node, f *FacetSet) {
	for _, c := range contentChildren(el) {
		if c.Name.URI != NSSchema {
			continue
		}
		v := c.AttrValue("value")
		switch c.Name.Local {
		case "length":
			f.Length = p.uintFacet(c, v)
		case "minLength":
			f.MinLength = p.uintFacet(c, v)
		case "maxLength":
			f.MaxLength = p.uintFacet(c, v)
		case "totalDigits":
			f.TotalDigits = p.uintFacet(c, v)
		case "fractionDigits":
			f.FractionDigits = p.uintFacet(c, v)

		case "whiteSpace":
			var w WhiteSpace
			switch v {
			case "preserve":
				w = WhitePreserve
			case "replace":
				w = WhiteReplace
			case "collapse":
				w = WhiteCollapse
			default:
				p.errs = append(p.errs, errorAt(c, "",
					"whiteSpace=%q is not one of preserve, replace or collapse", v))
				continue
			}
			f.WhiteSpace = &w
			f.WhiteSpaceFixed = p.boolAttr(c, "fixed", false)

		case "pattern":
			pat, err := compilePattern(v)
			if err != nil {
				p.errs = append(p.errs, errorAt(c, "", "%v", err))
				continue
			}
			f.Patterns = append(f.Patterns, pat)

		case "enumeration":
			// An enumeration facet is a set, and several
			// <xs:enumeration> children contribute to one set rather
			// than each replacing the last.
			f.Enumerations = append(f.Enumerations, v)
			f.HasEnumerations = true

		case "minInclusive":
			f.MinInclusive = &v
		case "maxInclusive":
			f.MaxInclusive = &v
		case "minExclusive":
			f.MinExclusive = &v
		case "maxExclusive":
			f.MaxExclusive = &v

		case "assertion":
			// XSD 1.1: an assertion on a simple type is a facet
			// rather than a component, but compiles identically.
			if a := p.readAssert(c); a != nil {
				f.Assertions = append(f.Assertions, a)
			}

		case "explicitTimezone":
			// XSD 1.1: constrains whether a date or time value must
			// carry a timezone.
			switch v {
			case "required":
				tz := TimezoneRequired
				f.ExplicitTimezone = &tz
			case "prohibited":
				tz := TimezoneProhibited
				f.ExplicitTimezone = &tz
			case "optional":
				tz := TimezoneOptional
				f.ExplicitTimezone = &tz
			default:
				p.errs = append(p.errs, errorAt(c, "",
					"explicitTimezone=%q is not one of required, "+
						"prohibited or optional", v))
			}

		case "simpleType", "annotation":
			// Handled by the caller.

		default:
			p.errs = append(p.errs, errorAt(c, "",
				"xs:%s is not a constraining facet", c.Name.Local))
		}
	}
}

// uintFacet parses a facet value that is an xs:nonNegativeInteger.
func (p *parser) uintFacet(el *xdm.Node, v string) *uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
	if err != nil {
		p.errs = append(p.errs, errorAt(el, "",
			"xs:%s value %q is not a non-negative integer", el.Name.Local, v))
		return nil
	}
	return &n
}

// readComplexType reads an <xs:complexType>.
func (p *parser) readComplexType(el *xdm.Node) *ComplexType {
	t := &ComplexType{DerivationMethod: DerivationRestriction}
	if name := el.AttrValue("name"); name != "" {
		t.Name = p.qnameFor(name)
	}
	t.Abstract = p.boolAttr(el, "abstract", false)
	p.applyDefaultAttributes(el, t)
	defer p.applyDefaultOpenContent(t)

	if f, err := p.derivationSet(el, "final"); err != nil {
		p.errs = append(p.errs, err)
	} else if el.Attr("", "final") != nil {
		t.FinalSet = f
	} else {
		t.FinalSet = p.doc.finalDefault
	}
	if b, err := p.derivationSet(el, "block"); err != nil {
		p.errs = append(p.errs, err)
	} else if el.Attr("", "block") != nil {
		t.Prohibits = b
	} else {
		t.Prohibits = p.doc.blockDefault
	}

	mixed := p.boolAttr(el, "mixed", false)

	if sc := childElement(el, "simpleContent"); sc != nil {
		p.readSimpleContent(sc, t)
		return t
	}
	if cc := childElement(el, "complexContent"); cc != nil {
		p.readComplexContent(cc, t, mixed)
		return t
	}

	// No simpleContent or complexContent: the children are the content
	// model directly, and the base is xs:anyType by restriction.
	t.Base = p.schema.anyType()
	p.readTypeBody(el, t, mixed)
	return t
}

// readTypeBody reads the particle and attributes of a complex type from an
// element whose children are the content model.
func (p *parser) readTypeBody(el *xdm.Node, t *ComplexType, mixed bool) {
	if g := childElement(el, "all", "choice", "sequence", "group"); g != nil {
		t.Particle = p.readParticle(g)
	}

	// XSD 1.1 additions. They are read whatever the version, because a
	// schema that uses them is not made valid by pretending they are absent
	// — Options.Version decides whether they are *honoured*, which is where
	// the distinction belongs.
	p.readAssertions(el, t)
	p.readAttributes(el, &t.AttributeUses, &t.AttributeWildcard)

	switch {
	case t.Particle == nil && mixed:
		// A mixed type with no particle permits character data and no
		// elements. The spec models this as a particle matching the
		// empty sequence rather than as empty content.
		t.Content = ContentMixed
		t.Particle = &Particle{MinOccurs: 1, MaxOccurs: 1,
			Term: &ModelGroup{Compositor: CompositorSequence}}
	case t.Particle == nil:
		t.Content = ContentEmpty
	case mixed:
		t.Content = ContentMixed
	default:
		t.Content = ContentElementOnly
	}
}

// readSimpleContent reads <xs:simpleContent>, which gives a complex type
// character-data content and attributes.
func (p *parser) readSimpleContent(el *xdm.Node, t *ComplexType) {
	t.Content = ContentSimple
	body := childElement(el, "restriction", "extension")
	if body == nil {
		p.errs = append(p.errs, errorAt(el, "src-ct.1",
			"simpleContent must have a restriction or extension child"))
		return
	}
	if body.Name.Local == "extension" {
		t.DerivationMethod = DerivationExtension
	}

	base := body.AttrValue("base")
	if base == "" {
		p.errs = append(p.errs, errorAt(body, "src-ct.1",
			"a simpleContent %s must have a base", body.Name.Local))
	} else {
		p.resolveTypeRef(body, base, func(bt Type) {
			t.Base = bt
			switch b := bt.(type) {
			case *SimpleType:
				// Extending a simple type gives a complex type whose
				// content is that simple type.
				t.SimpleContent = b
			case *ComplexType:
				t.SimpleContent = b.SimpleContent
			}
		})
	}

	if inline := childElement(body, "simpleType"); inline != nil {
		t.SimpleContent = p.readSimpleType(inline)
	}
	// An assertion may sit inside the restriction or extension of a
	// simpleContent, where it constrains the element's value through
	// $value. Reading them only from a content model missed every one.
	p.readAssertions(body, t)
	p.readAttributes(body, &t.AttributeUses, &t.AttributeWildcard)
	p.inheritAttributes(t)
}

// readAssertions reads the XSD 1.1 <xs:assert> and <xs:openContent> children of
// a type body.
//
// They are read whatever the version, because a schema that uses them is not
// made valid by pretending they are absent — Options.Version decides whether
// they are honoured, which is where the distinction belongs.
func (p *parser) readAssertions(el *xdm.Node, t *ComplexType) {
	for _, c := range contentChildren(el) {
		if c.Name.URI != NSSchema {
			continue
		}
		switch c.Name.Local {
		case "assert":
			if a := p.readAssert(c); a != nil {
				t.Assertions = append(t.Assertions, a)
			}
		case "openContent":
			t.OpenContent = p.readOpenContent(c)
			t.declaredOpenContent = true
		}
	}
}

// readComplexContent reads <xs:complexContent>.
func (p *parser) readComplexContent(el *xdm.Node, t *ComplexType, mixed bool) {
	// mixed on complexContent overrides mixed on the complexType.
	if el.Attr("", "mixed") != nil {
		mixed = p.boolAttr(el, "mixed", mixed)
	}

	body := childElement(el, "restriction", "extension")
	if body == nil {
		p.errs = append(p.errs, errorAt(el, "src-ct.1",
			"complexContent must have a restriction or extension child"))
		return
	}
	if body.Name.Local == "extension" {
		t.DerivationMethod = DerivationExtension
	}

	base := body.AttrValue("base")
	if base == "" {
		p.errs = append(p.errs, errorAt(body, "src-ct.1",
			"a complexContent %s must have a base", body.Name.Local))
	} else {
		p.resolveTypeRef(body, base, func(bt Type) { t.Base = bt })
	}

	p.readTypeBody(body, t, mixed)
	p.inheritAttributes(t)

	// An extension's content model is the base's followed by the
	// derived one. The base may not be resolved yet, so the splice is
	// deferred.
	if t.DerivationMethod == DerivationExtension {
		own := t.Particle
		p.fixups = append(p.fixups, func() error {
			base, ok := t.Base.(*ComplexType)
			if !ok || base.Particle == nil {
				return nil
			}
			if own == nil {
				t.Particle = base.Particle
				if t.Content == ContentEmpty {
					t.Content = base.Content
				}
				return nil
			}
			// XSD 1.1 §3.4.2.3.3 clause 2.2: when both the base and
			// the extension are all groups, the result is one all
			// group holding both sets of particles — not a
			// sequence of two. A sequence would demand that every
			// base child precede every extension child, which is
			// exactly what an all group exists not to require, so
			// the 1.0 splice rejects documents the 1.1 schema
			// permits.
			if merged := mergeAllExtension(base.Particle, own); merged != nil {
				t.Particle = merged
				return nil
			}
			t.Particle = &Particle{
				MinOccurs: 1, MaxOccurs: 1,
				Term: &ModelGroup{
					Compositor: CompositorSequence,
					Particles:  []*Particle{base.Particle, own},
				},
			}
			return nil
		})
	}
}

// readParticle reads an element, group reference, or model group as a particle.
func (p *parser) readParticle(el *xdm.Node) *Particle {
	min, max, err := p.occurs(el)
	if err != nil {
		p.errs = append(p.errs, err)
		return nil
	}
	part := &Particle{MinOccurs: min, MaxOccurs: max}

	switch el.Name.Local {
	case "element":
		if ref := el.AttrValue("ref"); ref != "" {
			name, err := p.resolveQName(el, "ref", ref)
			if err != nil {
				p.errs = append(p.errs, err)
				return nil
			}
			p.fixups = append(p.fixups, func() error {
				d, ok := p.schema.Elements[name]
				if !ok {
					return errorAt(el, "src-resolve",
						"element ref %q names no element declaration", ref)
				}
				part.Term = d
				return nil
			})
			return part
		}
		part.Term = p.readElementDecl(el, ScopeLocal)

	case "any":
		part.Term = p.readWildcard(el)

	case "group":
		ref := el.AttrValue("ref")
		if ref == "" {
			p.errs = append(p.errs, errorAt(el, "src-element.2.1",
				"a group inside a content model must have a ref"))
			return nil
		}
		name, err := p.resolveQName(el, "ref", ref)
		if err != nil {
			p.errs = append(p.errs, err)
			return nil
		}
		p.fixups = append(p.fixups, func() error {
			g, ok := p.schema.ModelGroups[name]
			if !ok {
				return errorAt(el, "src-resolve",
					"group ref %q names no group definition", ref)
			}
			// The term is the group definition's model group. Erratum
			// E1-29 makes this a *distinct particle* from any other
			// reference to the same definition, which is why the
			// particle wrapper is not shared.
			part.Term = g.Group
			return nil
		})

	case "all", "choice", "sequence":
		part.Term = p.readModelGroup(el)

	default:
		p.errs = append(p.errs, errorAt(el, "",
			"xs:%s is not valid in a content model", el.Name.Local))
		return nil
	}
	return part
}

// readModelGroup reads an <xs:all>, <xs:choice> or <xs:sequence>.
func (p *parser) readModelGroup(el *xdm.Node) *ModelGroup {
	g := &ModelGroup{}
	switch el.Name.Local {
	case "all":
		g.Compositor = CompositorAll
	case "choice":
		g.Compositor = CompositorChoice
	case "sequence":
		g.Compositor = CompositorSequence
	}
	for _, c := range contentChildren(el) {
		if c.Name.URI != NSSchema {
			continue
		}
		if part := p.readParticle(c); part != nil {
			g.Particles = append(g.Particles, part)
		}
	}
	return g
}

// readModelGroupDef reads a top-level <xs:group>.
func (p *parser) readModelGroupDef(el *xdm.Node) *ModelGroupDef {
	name := el.AttrValue("name")
	if name == "" {
		p.errs = append(p.errs, errorAt(el, "",
			"a top-level group must have a name"))
		return nil
	}
	inner := childElement(el, "all", "choice", "sequence")
	if inner == nil {
		p.errs = append(p.errs, errorAt(el, "",
			"a group definition must contain an all, choice or sequence"))
		return nil
	}
	return &ModelGroupDef{Name: p.qnameFor(name), Group: p.readModelGroup(inner)}
}

// inheritAttributes adds the base type's attribute uses to a derived type.
//
// §3.4.2 makes {attribute uses} of a derived type include the base's, for both
// extension and restriction: an extension adds to them and a restriction may
// narrow one, but neither starts from nothing. Without this every attribute
// declared on a base type vanishes from its subtypes, which is silent — the
// schema loads and the document is simply rejected for carrying an attribute
// the base declared.
//
// The base may not be resolved yet, so this runs as a fixup. A use declared on
// the derived type wins over the inherited one of the same name, which is how a
// restriction narrows an attribute and how "prohibited" removes it.
func (p *parser) inheritAttributes(t *ComplexType) {
	p.fixups = append(p.fixups, func() error {
		base, ok := t.Base.(*ComplexType)
		if !ok || base == t {
			return nil
		}
		own := make(map[xdm.QName]bool, len(t.AttributeUses))
		for _, u := range t.AttributeUses {
			if u.Decl != nil {
				own[u.Decl.Name] = true
			}
		}
		for _, u := range base.AttributeUses {
			if u.Decl == nil || own[u.Decl.Name] {
				continue
			}
			t.AttributeUses = append(t.AttributeUses, u)
		}
		// Assertions accumulate down a derivation chain: a derived type
		// has to satisfy its base's as well as its own, whether it
		// extends or restricts. §3.4.2.4 makes {assertions} the base's
		// followed by the type's, and without it a restriction could
		// widen what its base accepted just by declaring an assertion
		// of its own — which is the opposite of restricting.
		if len(base.Assertions) > 0 {
			t.Assertions = append(append([]*Assertion{}, base.Assertions...),
				t.Assertions...)
		}
		// The attribute wildcard combines the way open content does:
		// unioned for an extension, since an extension may only widen
		// what its base admits, and replaced for a restriction. Taking
		// the base's only when the derived type declared none refused
		// attributes the base type accepted.
		if t.DerivationMethod == DerivationExtension {
			t.AttributeWildcard = unionWildcards(base.AttributeWildcard, t.AttributeWildcard)
		} else if t.AttributeWildcard == nil {
			t.AttributeWildcard = base.AttributeWildcard
		}
		// XSD 1.1 open content is inherited, but for an extension it is
		// combined rather than replaced (§3.4.2.3.3 clause 3): the
		// result admits what either the base or the extension admits.
		// An extension may only widen what its base accepts, so a
		// derived openContent that replaced the base's would let an
		// extension close content the base had opened — which is a
		// restriction wearing an extension's spelling.
		t.OpenContent = combineOpenContent(base.OpenContent, t.OpenContent,
			t.DerivationMethod, t.declaredOpenContent)
		return nil
	})
}

// readOpenContent reads an <xs:openContent> or <xs:defaultOpenContent>.
func (p *parser) readOpenContent(el *xdm.Node) *OpenContent {
	oc := &OpenContent{Mode: OpenInterleave}
	switch el.AttrValue("mode") {
	case "", "interleave":
		oc.Mode = OpenInterleave
	case "suffix":
		oc.Mode = OpenSuffix
	case "none":
		// An explicit "none" closes a content model that a
		// defaultOpenContent would otherwise have opened.
		return nil
	default:
		p.errs = append(p.errs, errorAt(el, "src-open-content",
			"mode=%q is not one of interleave, suffix or none",
			el.AttrValue("mode")))
	}
	if w := childElement(el, "any"); w != nil {
		oc.Wildcard = p.readWildcard(w)
	} else {
		// An openContent with no wildcard defaults to ##any, lax.
		oc.Wildcard = &Wildcard{Kind: NSAny, ProcessContents: ProcessLax}
	}
	return oc
}

// applyDefaultAttributes adds the document's XSD 1.1 defaultAttributes group to
// a complex type.
//
// The group is named once on <xs:schema> and applies to every complex type in
// the document, which is how 1.1 lets a schema put xml:lang or a version
// attribute everywhere without repeating it. A type opts out with
// defaultAttributesApply="false".
//
// It is read whatever the version, for the same reason the other 1.1
// constructs are: a schema that uses it is not made valid by pretending it is
// absent. A 1.0 schema simply never writes the attribute.
func (p *parser) applyDefaultAttributes(el *xdm.Node, t *ComplexType) {
	if p.doc.defaultAttributes == "" {
		return
	}
	if !p.boolAttr(el, "defaultAttributesApply", true) {
		return
	}
	name, err := p.resolveQName(el, "defaultAttributes", p.doc.defaultAttributes)
	if err != nil {
		p.errs = append(p.errs, err)
		return
	}
	p.fixups = append(p.fixups, func() error {
		g, ok := p.schema.AttributeGroups[name]
		if !ok {
			return errorAt(el, "src-resolve",
				"defaultAttributes names no attribute group %q",
				p.doc.defaultAttributes)
		}
		// The type's own uses win: a declaration that names the same
		// attribute overrides the default rather than colliding.
		own := make(map[xdm.QName]bool, len(t.AttributeUses))
		for _, u := range t.AttributeUses {
			if u.Decl != nil {
				own[u.Decl.Name] = true
			}
		}
		for _, u := range g.AttributeUses {
			if u.Decl != nil && !own[u.Decl.Name] {
				t.AttributeUses = append(t.AttributeUses, u)
			}
		}
		return nil
	})
}

// applyDefaultOpenContent gives a type the document's <xs:defaultOpenContent>
// when it declares none of its own.
//
// appliesToEmpty decides whether a type with no content model is opened too.
// It defaults to false, so declaring a default open content does not silently
// turn every empty type in the document into one that accepts anything.
func (p *parser) applyDefaultOpenContent(t *ComplexType) {
	if t.declaredOpenContent || p.doc.defaultOpenContent == nil {
		return
	}
	if !p.doc.appliesToEmpty && isEmptyContent(t) {
		return
	}
	if t.Content == ContentSimple {
		// Simple content has no element children to open.
		return
	}
	t.OpenContent = p.doc.defaultOpenContent
}

// mergeAllExtension combines an all-group base with an all-group extension into
// a single all group, or returns nil if either side is not one.
//
// Both particles must occur exactly once for the merge to be sound: an all
// group repeated as a whole is a different language from one whose members
// carry the repetition, and only the unrepeated form is what §3.4.2.3.3
// describes.
func mergeAllExtension(basePart, own *Particle) *Particle {
	baseAll := allGroupOf(basePart)
	ownAll := allGroupOf(own)
	if baseAll == nil || ownAll == nil {
		return nil
	}
	particles := make([]*Particle, 0, len(baseAll.Particles)+len(ownAll.Particles))
	particles = append(particles, optionalIf(basePart.MinOccurs == 0, baseAll.Particles)...)
	particles = append(particles, optionalIf(own.MinOccurs == 0, ownAll.Particles)...)
	return &Particle{
		MinOccurs: 1, MaxOccurs: 1,
		Term: &ModelGroup{Compositor: CompositorAll, Particles: particles},
	}
}

// allGroupOf returns the all group a particle is, seeing through a group
// reference, or nil.
//
// The indirection matters because <xs:group ref="..."/> naming an all group is
// how a schema shares one, and the reference is a distinct particle wrapping
// the same term.
//
// maxOccurs must be 1: an all group repeated as a whole is a different language
// from one whose members carry the repetition, and merging would lose that.
// minOccurs="0" is admitted, because an optional all group is one whose members
// are all optional — which the members' own minOccurs already say, and which
// §3.4.2.3.3 relies on when it merges an optional base into an extension.
func allGroupOf(p *Particle) *ModelGroup {
	if p == nil || p.MinOccurs > 1 || p.MaxOccurs != 1 {
		return nil
	}
	if g, ok := p.Term.(*ModelGroup); ok && g.Compositor == CompositorAll {
		return g
	}
	return nil
}

// optionalIf makes every particle optional when the group containing them was.
//
// An all group with minOccurs="0" may be absent entirely, which once its
// members are merged into a larger group can only be expressed by making each
// member optional. Without this, merging an optional base into an extension
// would turn its children into required ones.
func optionalIf(optional bool, ps []*Particle) []*Particle {
	if !optional {
		return ps
	}
	out := make([]*Particle, len(ps))
	for i, p := range ps {
		c := *p
		c.MinOccurs = 0
		out[i] = &c
	}
	return out
}

// combineOpenContent merges a base type's open content with a derived type's.
//
// For a restriction the derived declaration stands alone: a restriction may
// narrow what it accepts, so replacing is right and declaring none closes the
// content. For an extension the two are unioned, because an extension may only
// widen — the base's wildcard keeps admitting what it always did, and the
// extension's adds to it.
//
// mode="none" on an extension is the one case that does not union: it is how a
// type says it declares no open content of its own, so what remains is the
// base's alone rather than nothing.
func combineOpenContent(base, own *OpenContent, method Derivation, declared bool) *OpenContent {
	if method != DerivationExtension {
		if !declared && own == nil {
			return base
		}
		return own
	}
	if base == nil || base.Wildcard == nil {
		return own
	}
	if own == nil || own.Wildcard == nil {
		// Either nothing was declared, or mode="none" was: both leave
		// the base's open content in force.
		return base
	}
	return &OpenContent{
		// interleave is the weaker constraint — it admits the wildcard
		// anywhere rather than only after the model is satisfied — so a
		// union that either side allows anywhere allows anywhere.
		Mode:     combineOpenMode(base.Mode, own.Mode),
		Wildcard: unionWildcards(base.Wildcard, own.Wildcard),
	}
}

func combineOpenMode(a, b OpenContentMode) OpenContentMode {
	if a == OpenInterleave || b == OpenInterleave {
		return OpenInterleave
	}
	return OpenSuffix
}

// unionWildcards combines two wildcards into one admitting what either admits
// (§3.10.6 "Attribute Wildcard Union").
//
// The spec allows a union to be inexpressible as a single namespace
// constraint — two disjoint negations, for instance, whose union is everything
// — and where that happens this returns the wider ##any rather than failing.
// A union that admitted less than either operand would reject documents the
// base type accepted, which for an extension is the one thing that must not
// happen.
func unionWildcards(a, b *Wildcard) *Wildcard {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	// processContents takes the weaker of the two: the union admits names
	// neither operand alone did, and checking them strictly because one
	// side happened to be strict would reject what the union permits.
	pc := a.ProcessContents
	if b.ProcessContents > pc {
		pc = b.ProcessContents
	}

	out := &Wildcard{ProcessContents: pc}

	// A name is disallowed by the union only if both operands disallow it:
	// either one admitting it is enough.
	out.DisallowedNames = intersectNames(a, b)
	out.DisallowDefined = a.DisallowDefined && b.DisallowDefined
	out.DisallowDefinedSibling = a.DisallowDefinedSibling && b.DisallowDefinedSibling

	switch {
	case a.Kind == NSAny || b.Kind == NSAny:
		out.Kind = NSAny
		return out

	case a.Kind == NSEnumerated && b.Kind == NSEnumerated:
		out.Kind = NSEnumerated
		seen := map[string]bool{}
		for _, ns := range append(append([]string{}, a.Namespace...), b.Namespace...) {
			if !seen[ns] {
				seen[ns] = true
				out.Namespace = append(out.Namespace, ns)
			}
		}
		return out

	case a.Kind == NSNot && b.Kind == NSNot:
		// The union of two negations excludes only what both exclude.
		out.Kind = NSNot
		out.ExcludesAbsent = a.ExcludesAbsent && b.ExcludesAbsent
		for _, ns := range a.Namespace {
			if !b.Allows(ns) {
				out.Namespace = append(out.Namespace, ns)
			}
		}
		return out
	}

	// One negation and one enumeration: the negation loses whatever the
	// enumeration lists, since those are now admitted by the other side.
	not, enum := a, b
	if b.Kind == NSNot {
		not, enum = b, a
	}
	out.Kind = NSNot
	out.ExcludesAbsent = not.ExcludesAbsent
	listed := map[string]bool{}
	for _, ns := range enum.Namespace {
		listed[ns] = true
		if ns == "" {
			out.ExcludesAbsent = false
		}
	}
	for _, ns := range not.Namespace {
		if !listed[ns] {
			out.Namespace = append(out.Namespace, ns)
		}
	}
	return out
}

// intersectNames returns the disallowed names both wildcards refuse.
func intersectNames(a, b *Wildcard) []xdm.QName {
	if len(a.DisallowedNames) == 0 || len(b.DisallowedNames) == 0 {
		return nil
	}
	inB := make(map[xdm.QName]bool, len(b.DisallowedNames))
	for _, n := range b.DisallowedNames {
		inB[n] = true
	}
	var out []xdm.QName
	for _, n := range a.DisallowedNames {
		if inB[n] {
			out = append(out, n)
		}
	}
	return out
}

// isEmptyContent reports whether a type's content model admits nothing but the
// empty sequence.
//
// appliesToEmpty asks about the content, not about how it was spelled:
// <xs:sequence/> is an element-only type whose particle matches only the empty
// sequence, and it is empty content in every sense that matters here. Testing
// only for ContentEmpty misses it, and the default open content then opens a
// type the schema said to leave closed.
func isEmptyContent(t *ComplexType) bool {
	if t.Content == ContentEmpty {
		return true
	}
	if t.Content != ContentElementOnly {
		return false
	}
	return particleMatchesOnlyEmpty(t.Particle, 0)
}

// particleMatchesOnlyEmpty reports whether a particle admits nothing but the
// empty sequence.
//
// The depth bound guards a model group that reaches itself, which is legal to
// write; the content-model compiler reports it, but this runs first.
func particleMatchesOnlyEmpty(p *Particle, depth int) bool {
	if p == nil {
		return true
	}
	if depth > 32 {
		return false
	}
	if p.MaxOccurs == 0 {
		return true
	}
	g, ok := p.Term.(*ModelGroup)
	if !ok {
		// An element or wildcard particle admits something unless it
		// cannot occur at all.
		return false
	}
	for _, child := range g.Particles {
		if !particleMatchesOnlyEmpty(child, depth+1) {
			return false
		}
	}
	return true
}
