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
		}
	}
	t.AttributeWildcard = p.readAttributes(el, &t.AttributeUses)

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
	t.AttributeWildcard = p.readAttributes(body, &t.AttributeUses)
	p.inheritAttributes(t)
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
		if t.AttributeWildcard == nil {
			t.AttributeWildcard = base.AttributeWildcard
		}
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
