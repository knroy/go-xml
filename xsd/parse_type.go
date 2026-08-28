package xsd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// readSimpleType reads an <xs:simpleType>.
func (p *parser) readSimpleType(el *xdm.Node) *SimpleType {
	t := &SimpleType{Facets: &FacetSet{}}
	// Recorded for the deferred Part 2 facet constraints, which need the
	// defining element to place their diagnostics.
	p.simpleTypes = append(p.simpleTypes, simpleTypeSite{typ: t, el: el})
	p.checkLocalSimpleTypeForm(el)
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

	body := p.childElement(el, "restriction", "list", "union")
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
	inline := p.childElement(el, "simpleType")
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
			// st-props-correct.1 (Part 2 §4.1.5), via the {variety}
			// property — NOT an exclusion of anySimpleType by name,
			// which is why reading §3.14.6 alone does not find it:
			//
			//  1. Part 2 §4.1.1 (Part 1 §3.16.1) makes xs:anySimpleType
			//     the *simple ur-type definition*. It is a special
			//     component rather than a datatype, and its {variety}
			//     is *absent*.
			//  2. Part 1 §3.16.6 cos-st-restricts has a restriction
			//     inherit its base's {variety}, so restricting the
			//     ur-type yields a definition whose variety is absent.
			//  3. st-props-correct.1 requires the {variety} of every
			//     simple type definition to be one of atomic, list or
			//     union. Absent is none of them, so the derived type is
			//     not a legal component.
			//
			// xmllint reports exactly this, verbatim: "The variety is
			// absent." The rule is narrow by construction — naming
			// xs:anySimpleType as a declaration's type stays legal,
			// since that use creates no new simple type definition and
			// so trips no clause. Probed both ways before implementing.
			//
			// We model anySimpleType as VarietyAtomic internally so
			// that the base chain of the primitives works, so the
			// absent variety has to be detected by identity here
			// rather than by reading st.Variety.
			//
			// Pinned by msData/simpleType stZ005, stZ006, stZ010,
			// stZ011 and stZ012.
			if st == p.schema.anySimpleType() {
				p.errs = append(p.errs, errorAt(el, "st-props-correct.1",
					"xs:anySimpleType is the simple ur-type and has no "+
						"variety; it may not be the base of a restriction"))
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
		// The inline base's own itemType or memberTypes may be a forward
		// reference, filled in by a fixup that has not run yet, so the copy
		// above can take a nil where the settled component has a type. That
		// left a restricted list validating its length and its patterns but
		// never its items: <xs:restriction><xs:simpleType><xs:list
		// itemType="d"/></xs:simpleType><xs:length value="3"/></...> accepted
		// any three tokens at all. Re-reading once the graph has settled is
		// what makes the derived component agree with its base.
		p.postFixups = append(p.postFixups, func() error {
			t.Variety = st.Variety
			t.Primitive = st.Primitive
			t.ItemType = st.ItemType
			t.MemberTypes = st.MemberTypes
			return nil
		})
	default:
		p.errs = append(p.errs, errorAt(el, "src-simple-type.2",
			"a restriction must have a base attribute or an inline simpleType"))
	}

	p.readFacets(el, t.Facets)
}

// readSimpleList reads <xs:list>.
func (p *parser) readSimpleList(el *xdm.Node, t *SimpleType) {
	item := el.AttrValue("itemType")
	inline := p.childElement(el, "simpleType")
	switch {
	case item != "" && inline != nil:
		p.errs = append(p.errs, errorAt(el, "src-simple-type.3",
			"a list may not have both an itemType attribute and an "+
				"inline simpleType"))
	case item != "":
		p.resolveTypeRefLazy(el, item, func(bt Type) {
			st, ok := bt.(*SimpleType)
			if !ok {
				p.errs = append(p.errs, errorAt(el, "src-resolve",
					"list itemType %q is a complex type", item))
				return
			}
			t.ItemType = st
			// A list itemType that names no definition is
			// deliberately deferred, not reported here: saxonData
			// Missing/missing006 declares <xs:list itemType="absent"/>
			// and expects the schema to load, the error surfacing
			// only where the list type is actually used. Making this
			// a hard src-resolve error costs that test.
		}, func(ref string) { t.unresolved = ref })
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
					// §3.14.6 / src-resolve: a member type
					// that names no definition cannot be
					// deferred — the union cannot be built
					// without it. Only a namespace an
					// <xs:import> named but could not fetch
					// keeps the old deferred reading.
					if !p.absentNamespace(name.URI) {
						return errorAt(el, "src-resolve",
							"union member %q names no type definition", ref)
					}
					t.unresolved = ref
					return nil
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
	for _, c := range p.contentChildren(el) {
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

// repeatableFacets are the two constraining facets a single restriction may
// carry more than once. Part 2's schema-for-schemas marks exactly these with
// the comment "This one can be repeated"; every other facet contributes one
// value to the facet set, so writing it twice is an error rather than a
// silently-wins-last. The suite pins this with saxonData/Simple simple060
// through simple084, one group per repeated facet.
// xs:assertion joins them: 1.1 §4.3.13 makes {assertions} a sequence that
// several <xs:assertion> children extend, exactly as patterns do.
var repeatableFacets = map[string]bool{
	"pattern": true, "enumeration": true, "assertion": true,
}

// readFacets reads the constraining facet children of a restriction.
func (p *parser) readFacets(el *xdm.Node, f *FacetSet) {
	// Tracks which non-repeatable facets this restriction has already named,
	// so a second occurrence is reported rather than overwriting the first.
	seen := make(map[string]bool)
	for _, c := range p.contentChildren(el) {
		if c.Name.URI != NSSchema {
			continue
		}
		v := c.AttrValue("value")
		// Every constraining facet declares `value` as use="required" in the
		// schema for schemas (Part 2 §4.3, one declaration per facet). A facet
		// element without it is not a facet with an empty value — it is a
		// schema document that does not conform to the schema for schemas, and
		// §5.1 (Errors in Schema Construction and Structure) makes that an
		// error rather than something to interpret.
		//
		// Reading the absent attribute as "" instead constrained the type by
		// the empty string: <xs:enumeration values="yes"/>, a misspelling of
		// `value`, silently produced an enumeration whose only member was "",
		// so the misspelling was invisible and every legitimate value was
		// rejected at validation time with no hint why.
		//
		// xs:assertion is exempt: 1.1 §4.3.13 gives it `test`, not `value`.
		if knownFacet(c.Name.Local) && c.Name.Local != "assertion" &&
			c.Attr("", "value") == nil {
			p.errs = append(p.errs, errorAt(c, "src-facet-value",
				"facet xs:%s requires a value attribute", c.Name.Local))
			continue
		}
		if knownFacet(c.Name.Local) && !repeatableFacets[c.Name.Local] {
			if seen[c.Name.Local] {
				p.errs = append(p.errs, errorAt(c, "src-single-facet-value",
					"facet xs:%s appears more than once in a single restriction",
					c.Name.Local))
				continue
			}
			seen[c.Name.Local] = true
		}
		switch c.Name.Local {
		case "length":
			f.Length = p.uintFacet(c, v)
		case "minLength":
			f.MinLength = p.uintFacet(c, v)
		case "maxLength":
			f.MaxLength = p.uintFacet(c, v)
		case "totalDigits":
			// totalDigits is a positiveInteger, not a
			// nonNegativeInteger like the length facets: Part 2
			// §4.3.11 says so outright, and a value space holding
			// numbers expressible in at most zero digits is empty.
			// The suite writes totalDigits="0" against every
			// integer type in turn.
			f.TotalDigits = p.positiveUintFacet(c, v)
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
			pat, err := compilePatternVersion(v, p.schema.Version)
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
			// The expansion is recorded here even though most types
			// never need it: this is the only point where the facet's
			// own element — and so the schema document's namespace
			// bindings — is still reachable. checkEnumeration uses it
			// only when the type turns out to have QNames for its
			// value space, and the base type is not necessarily
			// resolved yet at this point.
			f.EnumerationQNames = append(f.EnumerationQNames,
				expandFacetQName(c, v))

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

		case "simpleType", "annotation", "assert", "openContent",
			"attribute", "attributeGroup", "anyAttribute",
			"all", "choice", "sequence", "group":
			// Handled by the caller. A simpleContent restriction
			// may hold facets alongside its attribute declarations
			// and its XSD 1.1 assertions, so reading facets there
			// meets elements that are not facets and must not be
			// reported as bad ones.

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

// positiveUintFacet reads a facet value that must be a positiveInteger.
func (p *parser) positiveUintFacet(el *xdm.Node, v string) *uint64 {
	n := p.uintFacet(el, v)
	if n == nil {
		return nil
	}
	if *n == 0 {
		p.errs = append(p.errs, errorAt(el, "",
			"xs:%s value %q is not a positive integer", el.Name.Local, v))
		return nil
	}
	return n
}

// readComplexType reads an <xs:complexType>.
func (p *parser) readComplexType(el *xdm.Node) *ComplexType {
	t := &ComplexType{DerivationMethod: DerivationRestriction}
	if name := el.AttrValue("name"); name != "" {
		t.Name = p.qnameFor(name)
	}
	// Every complex type is recorded, named or not. Schema.Types holds only
	// the named ones, so a constraint walked over that map alone never sees
	// a type declared inline in an <xs:element> — and inline is where the
	// suite puts most of them. particlesEa025 and particlesFb002 both hang
	// their offending content model off <xs:element name="doc">, and both
	// loaded clean while All Group Limited walked Schema.Types.
	p.schema.allComplexTypes = append(p.schema.allComplexTypes, t)
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

	// §3.4.2: a complex type that is not a child of <xs:schema>,
	// <xs:redefine> or <xs:override> is a *localComplexType*, and the
	// schema for schemas marks name, abstract, final and block prohibited
	// on it. A name on a local type is the reading that actually misleads:
	// it looks like a global definition and is not one — nothing can refer
	// to it (ctA042).
	if !topLevelType(el) {
		for _, a := range []string{"name", "abstract", "final", "block"} {
			if el.Attr("", a) != nil {
				p.errs = append(p.errs, errorAt(el, "src-ct",
					"%s is not permitted on a complexType that is "+
						"not a child of schema, redefine or override", a))
			}
		}
	}

	mixed := p.boolAttr(el, "mixed", false)

	if sc := p.childElement(el, "simpleContent"); sc != nil {
		p.readSimpleContent(sc, t)
		return t
	}
	if cc := p.childElement(el, "complexContent"); cc != nil {
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
	if g := p.childElement(el, "all", "choice", "sequence", "group"); g != nil {
		t.Particle = p.readParticle(g)
	}

	// XSD 1.1 additions. They are read whatever the version, because a
	// schema that uses them is not made valid by pretending they are absent
	// — Options.Version decides whether they are *honoured*, which is where
	// the distinction belongs.
	p.readAssertions(el, t)
	p.readAttributes(el, &t.AttributeUses, &t.AttributeWildcard, nil)
	// A type written without simpleContent or complexContent derives from
	// xs:anyType and inherits nothing, but it still needs the pass that
	// discards its prohibited uses — those are not {attribute uses} on any
	// type, derived or not. Only the two derivation paths called this, so a
	// use="prohibited" on a plain complexType survived into validation and
	// matched the very attribute it forbids (attF001).
	p.inheritAttributes(t)

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
	body := p.childElement(el, "restriction", "extension")
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
	}

	// An inline simpleType inside the restriction names the content
	// directly, and it is read first so that the deferred base resolution
	// can leave it alone. The base is resolved later than this runs, so a
	// fixup that assigned unconditionally would overwrite the inline type
	// with the base's — which is how a restriction narrowing
	// xs:anySimpleType to xs:float ended up validating against
	// xs:anySimpleType and accepting anything.
	inline := p.childElement(body, "simpleType")
	if inline != nil {
		t.SimpleContent = p.readSimpleType(inline)
	}

	if base != "" {
		p.resolveTypeRef(body, base, func(bt Type) {
			t.Base = bt
			if inline != nil {
				return
			}
			switch b := bt.(type) {
			case *SimpleType:
				// §3.4.2 / src-ct.1: only a simpleContent
				// *extension* may name a simple type as its base.
				// A simpleContent restriction restricts a complex
				// type that already has simple content, so a simple
				// base has nothing to restrict. stZ009 pins this
				// with <simpleContent><restriction
				// base="xs:anySimpleType"/>.
				if t.DerivationMethod != DerivationExtension {
					p.errs = append(p.errs, errorAt(body, "src-ct.1",
						"a simpleContent restriction base must be a "+
							"complex type, but %q is a simple type", base))
					return
				}
				// Extending a simple type gives a complex type whose
				// content is that simple type.
				t.SimpleContent = b
			case *ComplexType:
				t.SimpleContent = b.SimpleContent
			}
		})
		if inline == nil {
			// Facets may be written directly inside the
			// restriction, with no inline simpleType to hold them.
			// They narrow whatever content type the base supplies,
			// so the derived content is an anonymous restriction of
			// it — dropping them left a restriction that accepted
			// everything its base did, which is no restriction at
			// all.
			if facets := p.readFacetsOnly(body); facets != nil {
				p.fixups = append(p.fixups, func() error {
					base := t.SimpleContent
					if base == nil {
						base = inheritedSimpleContent(t)
					}
					if base == nil {
						return nil
					}
					t.SimpleContent = &SimpleType{
						Base:      base,
						Variety:   base.Variety,
						ItemType:  base.ItemType,
						Primitive: base.Primitive,
						Facets:    facets,
					}
					return nil
				})
			}

			// The base may itself be a complex type whose own
			// simple content is filled in by a later fixup, so
			// reading it above can find nothing. A second pass
			// after every fixup has run walks the chain to whatever
			// simple type is actually there — ordering between two
			// deferred resolutions is not something either one can
			// arrange for itself.
			p.fixups = append(p.fixups, func() error {
				if t.SimpleContent != nil {
					return nil
				}
				if sc := inheritedSimpleContent(t); sc != nil {
					t.SimpleContent = sc
					return nil
				}
				// The base's own content is not resolved yet;
				// try again after the fixups queued since.
				p.fixups = append(p.fixups, func() error {
					if t.SimpleContent == nil {
						t.SimpleContent = inheritedSimpleContent(t)
					}
					return nil
				})
				return nil
			})
		}
	}
	// An assertion may sit inside the restriction or extension of a
	// simpleContent, where it constrains the element's value through
	// $value. Reading them only from a content model missed every one.
	p.readAssertions(body, t)
	p.readAttributes(body, &t.AttributeUses, &t.AttributeWildcard, nil)
	p.inheritAttributes(t)
}

// readAssertions reads the XSD 1.1 <xs:assert> and <xs:openContent> children of
// a type body.
//
// They are read whatever the version, because a schema that uses them is not
// made valid by pretending they are absent — Options.Version decides whether
// they are honoured, which is where the distinction belongs.
func (p *parser) readAssertions(el *xdm.Node, t *ComplexType) {
	for _, c := range p.contentChildren(el) {
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

	body := p.childElement(el, "restriction", "extension")
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
		splice := func(seen map[*ComplexType]bool) {
			base, ok := t.Base.(*ComplexType)
			if !ok {
				return
			}
			// The base must be spliced before it is read. Fixups run
			// in the order they were queued, which is the order the
			// types were read, and that need not follow the
			// derivation chain: a type extending one whose own
			// splice has not run yet copied a half-built content
			// model, losing everything below it.
			//
			// elemZ010 is four types extending one another across
			// four documents, a extends b extends c extends d. Each
			// came out with two child elements instead of four,
			// three and two, so the instance was rejected at the
			// first child the model had lost.
			// The caller's seen set is passed down rather than a
			// fresh one, or a cycle in the base chain — which is
			// ill-formed, and reported by a later constraint —
			// recurses until the stack is gone.
			p.spliceExtension(base, seen)
			if base.Particle == nil {
				return
			}
			if own == nil {
				t.Particle = base.Particle
				if t.Content == ContentEmpty {
					t.Content = base.Content
				}
				return
			}
			// XSD 1.1 §3.4.2.3.3 clause 2.2: when both the base and
			// the extension are all groups, the result is one all
			// group holding both sets of particles — not a
			// sequence of two. A sequence would demand that every
			// base child precede every extension child, which is
			// exactly what an all group exists not to require, so
			// the 1.0 splice rejects documents the 1.1 schema
			// permits.
			//
			// This is 1.1-only. XSD 1.0 has no such clause: there
			// the splice is always a sequence, and a sequence
			// holding an all group violates All Group Limited
			// clause 1, so extending an all group is simply
			// illegal. mgA016 pins the split exactly — expected
			// invalid under 1.0 and valid under 1.1 — and
			// particlesFb002 is the same shape with an all
			// extending a choice.
			if p.schema.Version >= Version11 {
				if merged := mergeAllExtension(base.Particle, own); merged != nil {
					t.Particle = merged
					return
				}
			}
			// A base that matches only the empty sequence
			// contributes nothing to the extension, so the
			// effective content type is the extension's own
			// particle rather than a sequence of the two. Building
			// the sequence anyway would wrap an all group in a
			// sequence and break All Group Limited for a schema
			// that is fine — mgO014 extends <sequence/> with a
			// group whose term is an all group, and the W3C
			// expects it valid.
			if isEmptyContent(base) {
				t.Particle = own
				return
			}
			t.Particle = &Particle{
				MinOccurs: 1, MaxOccurs: 1,
				Term: &ModelGroup{
					Compositor: CompositorSequence,
					Particles:  []*Particle{base.Particle, own},
				},
			}
		}
		if p.pendingSplice == nil {
			p.pendingSplice = map[*ComplexType]func(map[*ComplexType]bool){}
			p.splicedNow = map[*ComplexType]bool{}
		}
		// A type may be read more than once — a redefine reads its
		// replacement over the original — and only the last splice
		// registered describes the type as it finally stands.
		p.pendingSplice[t] = splice
		// The splice runs as a post-fixup rather than a fixup. A type's
		// {base type definition} is itself filled in by a fixup, so
		// during the fixup pass a base may still be nil — and the
		// recursion above, which exists to splice a base before reading
		// it, cannot do anything with a base that is not there yet.
		// postFixups run once the fixups have drained, when every base
		// reference has been bound.
		p.postFixups = append(p.postFixups, func() error {
			p.spliceExtension(t, map[*ComplexType]bool{})
			return nil
		})

		// §3.4.6 cos-ct-extends.1.4.1: when the derived {content type}
		// is a particle (clause 1.4.2 in the 1.0 numbering: "one of the
		// following is true ... 1.4.2.1 The {content type} of the {base
		// type definition} must be empty"), the base's {content type}
		// must be empty or itself a particle. A complexContent
		// extension of a type whose content is *simple* has no way to
		// combine the two: the spec's clause 1.4.2.2.1 asks both
		// content types to be particles, and a simple content type is
		// not one.
		//
		// Checked on the source form rather than on t.Content, because
		// the splice above rewrites t.Content from the base and would
		// have already turned the derived type's ContentElementOnly
		// into ContentSimple by the time this could look.
		//
		// Under 1.1 the derived type need not add a content model for
		// this to be an error. 1.0's clause 1.4.2.2 let an extension
		// with an *empty* effective content simply inherit the base's
		// simple content type, so <complexContent><extension> over a
		// simpleContent base that adds only attributes was well-formed;
		// 1.1 requires the derived declaration to say simpleContent
		// too. particlesZ031 is exactly that pair, and the suite marks
		// it valid under 1.0 and invalid under 1.1.
		if own != nil || p.schema.Version >= Version11 {
			body := body
			p.postFixups = append(p.postFixups, func() error {
				bt, ok := t.Base.(*ComplexType)
				if !ok || bt.Content != ContentSimple {
					return nil
				}
				if own == nil {
					return errorAt(body, "cos-ct-extends.1.4.1",
						"a complexContent extension may not derive from "+
							"base type %q, whose content is simple",
						base)
				}
				return errorAt(body, "cos-ct-extends.1.4.1",
					"a complexContent extension may not add a content "+
						"model to base type %q, whose content is simple",
					base)
			})
		}
	}
}

// spliceExtension runs a type's own extension splice ahead of its turn, so that
// a type extending it sees the finished content model rather than a half-built
// one.
//
// The seen set guards a cycle in the base chain, which is ill-formed but must
// not hang here — the constraint that reports it runs later, and reaching it
// requires getting this far first.
func (p *parser) spliceExtension(t *ComplexType, seen map[*ComplexType]bool) {
	if t == nil || seen[t] || p.splicedNow[t] {
		return
	}
	pending := p.pendingSplice[t]
	if pending == nil {
		return
	}
	// The cycle guard is the caller's seen set, which is passed down the
	// chain. splicedNow is set only *after* the splice has run, because the
	// splice recurses into its own base first and marking it done up front
	// would stop that recursion at the second link.
	seen[t] = true
	pending(seen)
	p.splicedNow[t] = true
}

// readParticle reads an element, group reference, or model group as a particle.
func (p *parser) readParticle(el *xdm.Node) *Particle {
	min, max, err := p.occurs(el)
	if err != nil {
		p.errs = append(p.errs, err)
		return nil
	}
	// A particle spelled minOccurs=maxOccurs=0 "corresponds to no component
	// at all" — the mapping rules in §3.9.2 (and the parallel wording for
	// elements, groups and wildcards) say so explicitly, and Particle
	// Correct clause 2.2 confirms it by requiring {max occurs} >= 1 of every
	// particle that does exist. Returning nil here rather than a 0..0
	// particle keeps the rest of the system from having to special-case a
	// component the spec never creates: Particle Valid (Restriction) in
	// particular would otherwise compare a term that can never match
	// anything, which is how particlesJq010 (an out-of-namespace element at
	// 0..0 under a wildcard) and mgH014 (a 0..0 alternative dropped from a
	// choice) came to be rejected.
	if max == 0 {
		return nil
	}
	part := &Particle{MinOccurs: min, MaxOccurs: max}

	switch el.Name.Local {
	case "element":
		if ref := el.AttrValue("ref"); ref != "" {
			p.checkElementRefExclusions(el)
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
		// src-element.2.1: "either the name or the ref attribute must
		// be present, but not both". A local element with neither
		// declares nothing at all; elemP005 is <xs:all><xs:element/>.
		if el.AttrValue("name") == "" {
			p.errs = append(p.errs, errorAt(el, "src-element.2.1",
				"an element inside a content model must have a name or a ref"))
			return nil
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
		if el.Name.Local == "all" {
			p.checkAllOccurs(el, max)
		}
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
	for _, c := range p.contentChildren(el) {
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
	inner := p.childElement(el, "all", "choice", "sequence")
	if inner == nil {
		p.errs = append(p.errs, errorAt(el, "",
			"a group definition must contain an all, choice or sequence"))
		return nil
	}
	// The schema for schemas gives a named group definition the type
	// xs:namedGroup, which prohibits ref, minOccurs and maxOccurs outright:
	// a definition is not a use, so it has no occurrence range and cannot
	// also be a reference. The same type prohibits both occurrence
	// attributes on the <all> it may contain.
	//
	// None of these is read on this path — the definition's group is not
	// built as a particle — so each was silently discarded. mgO019 writes
	// <all maxOccurs="0"> inside a <group name="..."> and loaded clean,
	// while the identical group written inline was rejected.
	for _, attr := range []string{"ref", "minOccurs", "maxOccurs"} {
		if el.Attr("", attr) != nil {
			p.errs = append(p.errs, errorAt(el, "",
				"attribute %q is not allowed on a named group definition", attr))
		}
	}
	if inner.Name.Local == "all" {
		for _, attr := range []string{"minOccurs", "maxOccurs"} {
			if inner.Attr("", attr) != nil {
				p.errs = append(p.errs, errorAt(inner, "cos-all-limited.1.2",
					"attribute %q is not allowed on the xs:all of a "+
						"named group definition", attr))
			}
		}
	}
	// The <choice> or <sequence> inside a definition still carries an
	// occurrence range that must be well formed, even though the range
	// itself is discarded: the group definition has no {max occurs}, so
	// only the reference's range survives. Nothing validated it, because
	// this path never builds a particle and occurs() runs only in
	// readParticle — particlesEc009 writes <choice minOccurs="2"> with
	// maxOccurs defaulting to 1, which is p-props-correct.2.1, and it
	// loaded clean.
	if inner.Name.Local != "all" {
		if _, _, err := p.occurs(inner); err != nil {
			p.errs = append(p.errs, err)
		}
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
	// Inheritance runs on demand rather than on a scheduled pass. A type's
	// attributes depend on its base's, which depend on the base's base, and
	// on attribute groups whose own edges resolve through fixups too. There
	// is no fixed number of passes that covers every chain — UBL's
	// EndpointIDType extends udt:IdentifierType across a document boundary,
	// and counting passes left it with none of the seven attributes its
	// base declares.
	//
	// Resolving the base first, recursively, makes the depth irrelevant.
	//
	// It is scheduled behind one more fixup so that every type's *own*
	// attributes — the ones its attribute groups contribute, through fixups
	// of their own — are in place before any inheritance reads them. The
	// recursion handles depth in the base chain; this handles the one step
	// that is not a base chain at all.
	p.fixups = append(p.fixups, func() error {
		p.fixups = append(p.fixups, func() error {
			p.resolveAttributes(t, nil)
			return nil
		})
		return nil
	})
}

// resolveAttributes gives a type its inherited attributes, resolving its base
// chain first.
//
// The seen set guards a cycle in the base chain, which is ill-formed but must
// not hang. done marks a type already resolved, so a base shared by many
// derived types is walked once.
func (p *parser) resolveAttributes(t *ComplexType, seen map[*ComplexType]bool) {
	if t == nil || p.attrsDone[t] {
		return
	}
	// A built-in is never resolved here, and the guard has to sit at the
	// top rather than at the call site: the base chain of a user type ends
	// at xs:anyType, so the recursion below reaches a built-in even when
	// the caller did not.
	//
	// The built-ins come from a process-wide singleton, so one *ComplexType
	// for xs:anyType is shared by every schema ever loaded, and the tail of
	// this function *writes* to the type. Two concurrent Load calls
	// therefore wrote to one shared value. p.attrsDone cannot prevent it:
	// it is per-parser, so each Load believes it is the first.
	//
	// Nothing is lost. A built-in carries no attribute uses a schema
	// document wrote, which is what this walk exists to resolve.
	if t.Name.URI == NSSchema {
		return
	}
	if seen == nil {
		seen = map[*ComplexType]bool{}
	}
	if seen[t] {
		return
	}
	seen[t] = true

	base, isComplex := t.Base.(*ComplexType)
	if isComplex && base != t {
		p.resolveAttributes(base, seen)
	}
	p.attrsDone[t] = true
	// The base is fully resolved by now, so its {final} can be trusted.
	p.checkComplexDerivationFinal(t)
	p.checkContentDerivationForm(t)
	p.checkMixedConsistency(t)
	p.checkAttributeWildcardRestriction(t)
	p.checkOpenContentRestriction(t)
	p.inheritAttributesNow(t)

	// A prohibited use is never one of the type's {attribute uses}, whether
	// or not the type derives from anything. inheritAttributesNow drops them
	// too, but it returns early for a type with no complex base, which left
	// a use="prohibited" on a base-less type sitting in the list and so
	// *matching* the attribute it was written to forbid. attF001 is that
	// shape: a lone prohibited use, an instance carrying the attribute, and
	// no wildcard to admit it — expected invalid, and accepted before this.
	//
	// Dropping the use is also what keeps attZ002 valid: there the type has
	// an anyAttribute, and once the use is gone the wildcard is free to
	// admit the name. use="prohibited" removes the use, not the name.
	t.AttributeUses = dropProhibited(t.AttributeUses)
}

func (p *parser) inheritAttributesNow(t *ComplexType) {
	_ = func() error {
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
		// derivation-ok-restriction clauses 2 and 3 are checked here,
		// while both sides are still in hand: after dropProhibited the
		// evidence that a use was prohibited is gone, and clause 3 turns
		// on exactly that.
		if t.DerivationMethod == DerivationRestriction {
			p.checkAttributeRestriction(t, base)
		}
		// The prohibited uses have done their work now that inheritance
		// has run, and must not reach validation: a prohibited use is
		// not one of the type's {attribute uses}, and leaving it there
		// would let the attribute be matched and validated.
		t.AttributeUses = dropProhibited(t.AttributeUses)
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
		}
		// A restriction declaring no attribute wildcard has none: it
		// narrows what the base accepts, and inheriting the base's
		// wildcard would keep admitting every attribute the base did.
		// This is the attribute counterpart of a restriction closing
		// open content, and the same reasoning applies.
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
	}()
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
	if w := p.childElement(el, "any"); w != nil {
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
	if p.doc.defaultAttributes == "" || p.inOverride {
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
	// The spelling is captured now rather than read inside the fixup. A
	// fixup runs after every document has been read, by which time p.doc is
	// whatever was last in hand — nil, once the assembler is done with it.
	// Reading it there dereferenced that nil for the one input that reaches
	// the branch: a defaultAttributes naming a group that does not exist.
	spelling := p.doc.defaultAttributes
	p.fixups = append(p.fixups, func() error {
		g, ok := p.schema.AttributeGroups[name]
		if !ok {
			return errorAt(el, "src-resolve",
				"defaultAttributes names no attribute group %q",
				spelling)
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
		// The group's wildcard comes with it. A defaultAttributes group
		// whose whole content is an anyAttribute contributed nothing at
		// all otherwise, which is the form the feature is most often
		// written in — "let every type in this document take the xml:
		// attributes" is a wildcard, not a list of uses.
		if g.AttributeWildcard != nil {
			t.AttributeWildcard = unionWildcards(
				t.AttributeWildcard, g.AttributeWildcard)
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
	if t.declaredOpenContent || p.doc.defaultOpenContent == nil || p.inOverride {
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
	// An optional all group is all-or-nothing: minOccurs="0" says the group
	// may be absent, not that each member is independently optional. The
	// two differ as soon as the group has more than one member, and
	// flattening the members with minOccurs="0" apiece loses the
	// distinction — <child1/> alone would satisfy a group that requires
	// both or neither.
	//
	// The group is kept as a nested particle so matchAll can enforce it,
	// rather than being merged into the members.
	particles := make([]*Particle, 0, 2)
	particles = append(particles, allBranch(basePart, baseAll))
	particles = append(particles, allBranch(own, ownAll))
	return &Particle{
		MinOccurs: 1, MaxOccurs: 1,
		Term: &ModelGroup{Compositor: CompositorAll, Particles: particles},
	}
}

// allBranch returns the particle for one side of a merged all group.
//
// A group that must occur contributes its members directly, since there is
// nothing conditional about them. An optional one keeps its own particle, so
// that "all of these or none" survives the merge.
func allBranch(p *Particle, g *ModelGroup) *Particle {
	if p.MinOccurs != 0 {
		return &Particle{
			MinOccurs: 1, MaxOccurs: 1,
			Term: &ModelGroup{Compositor: CompositorAll, Particles: g.Particles},
		}
	}
	return &Particle{
		MinOccurs: 0, MaxOccurs: 1,
		Term: &ModelGroup{Compositor: CompositorAll, Particles: g.Particles},
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
		// A restriction declaring no open content closes it. That is
		// the point of restricting: the derived type accepts a subset,
		// and inheriting the base's wildcard would keep admitting
		// everything the base did. The suite's open014 pins it —
		// "a valid restriction: base has open content, derived does
		// not", with the instance that uses it expected invalid.
		//
		// The defaultOpenContent still applies where the type declares
		// nothing, which is what declared records: it is applied
		// separately, and reaching here with own == nil means neither
		// the type nor the document supplied one.
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

	// A name survives the union if either operand admits it outright. A
	// name one operand disallows is therefore kept out only when the other
	// would not have admitted it anyway — which is not the same as both
	// listing it. In the suite's wild046 the two branches are ##local and
	// "not the XSLT namespace"; only the second can reach xml:lang at all,
	// and it disallows the name, so the union does too even though the
	// first never mentions it.
	out.DisallowedNames = unionUnadmittedNames(a, b)
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

// unionUnadmittedNames returns the names the union still refuses.
//
// A name one operand disallows is refused only when the other does not admit
// it — either because its namespace constraint excludes it, or because it
// disallows the name too.
func unionUnadmittedNames(a, b *Wildcard) []xdm.QName {
	var out []xdm.QName
	seen := map[xdm.QName]bool{}
	keep := func(name xdm.QName, other *Wildcard) {
		if seen[name] || other.AllowsName(name, nil) {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, n := range a.DisallowedNames {
		keep(n, b)
	}
	for _, n := range b.DisallowedNames {
		keep(n, a)
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

// inheritedSimpleContent walks a base chain for the simple type a complex type
// with simple content is built on.
func inheritedSimpleContent(t *ComplexType) *SimpleType {
	seen := 0
	for cur := Type(t); cur != nil; {
		switch b := cur.(type) {
		case *SimpleType:
			return b
		case *ComplexType:
			if b.SimpleContent != nil {
				return b.SimpleContent
			}
			if b.Base == cur {
				return nil
			}
			cur = b.Base
		default:
			return nil
		}
		if seen++; seen > 64 {
			return nil
		}
	}
	return nil
}

// readFacetsOnly reads a restriction's facet children, returning nil when there
// are none.
//
// A simpleContent restriction may carry facets directly, with no inline
// simpleType to hold them: they narrow whatever content type the base supplies,
// so the derived content becomes an anonymous restriction of it.
func (p *parser) readFacetsOnly(body *xdm.Node) *FacetSet {
	if !hasFacetChild(body) {
		return nil
	}
	f := &FacetSet{}
	p.readFacets(body, f)
	return f
}

// hasFacetChild reports whether an element has any constraining facet child.
//
// xs:assert is deliberately not one of them. Inside a simpleContent it is a
// complex-type assertion, read by readAssertions, and it is spelled differently
// from the xs:assertion facet precisely so that the two can sit in the same
// place without ambiguity.
func hasFacetChild(el *xdm.Node) bool {
	for _, c := range el.ChildElements() {
		if c.Name.URI != NSSchema || c.Name.Local == "assert" {
			continue
		}
		if knownFacet(c.Name.Local) {
			return true
		}
	}
	return false
}

// dropProhibited removes the placeholder uses left by use="prohibited".
func dropProhibited(uses []*AttributeUse) []*AttributeUse {
	kept := uses[:0]
	for _, u := range uses {
		if u.Prohibited {
			continue
		}
		kept = append(kept, u)
	}
	return kept
}

// checkGroupCycles enforces Model Group Correct clause 2 (§3.8.6): within the
// {particles} of a group there must not be, at any depth, a particle whose
// {term} is the group itself.
//
// A cycle is not merely invalid, it is dangerous to this implementation: the
// content model is compiled into an automaton by walking the particle tree, so
// a group that reaches itself makes that walk non-terminating. Every consumer
// of the component graph would have to defend itself; rejecting the schema
// here means none of them has to.
//
// The search is over ModelGroup identity rather than group *names* because a
// reference is resolved to the definition's ModelGroup pointer — so a cycle
// through any number of intermediate definitions shows up as the same pointer
// reappearing on the current path. Pins groupB013 (a group whose choice refs
// itself), groupB014 (the sequence form) and groupB015 (a two-group cycle).
func (p *parser) checkGroupCycles() {
	// Only the named definitions are roots. An anonymous group inside a
	// complex type can only be part of a cycle by way of a <group ref>, and
	// that ref points at a named definition, which is a root already.
	for _, def := range p.schema.ModelGroups {
		if def == nil || def.Group == nil {
			continue
		}
		if cycleFrom(def.Group, map[*ModelGroup]bool{}) {
			p.errs = append(p.errs, &ParseError{
				Code: "mg-props-correct.2",
				Message: fmt.Sprintf(
					"group %q is circular: its content refers back to itself",
					def.Name.Local),
			})
		}
	}
}

// cycleFrom reports whether g reaches itself through the particle tree.
//
// path holds the groups on the current descent, not the groups already
// visited: a group legitimately reachable by two disjoint routes is not a
// cycle, and marking it seen on the first route would either miss a real cycle
// on the second or report one that is not there.
func cycleFrom(g *ModelGroup, path map[*ModelGroup]bool) bool {
	if path[g] {
		return true
	}
	path[g] = true
	defer delete(path, g)

	for _, part := range g.Particles {
		if part == nil {
			continue
		}
		if inner, ok := part.Term.(*ModelGroup); ok && cycleFrom(inner, path) {
			return true
		}
	}
	return false
}

// checkElementRefExclusions enforces src-element.2.2 (§3.3.3): when a local
// <element> carries ref, everything that would describe a declaration of its
// own must be absent.
//
// The list is exhaustive in the spec — "only minOccurs, maxOccurs, id are
// allowed in addition to ref, along with <annotation>" — because a reference
// *is* the declaration it names. Writing type or nillable beside a ref reads
// as though it modified the referenced declaration for this one use, and it
// does not; the attribute is simply ignored, so the schema does not mean what
// it appears to say. Rejecting it is the only way the author finds out.
//
// name is checked here rather than by the general attribute table because it
// is legal on <element> in the abstract, and only its combination with ref is
// the fault (clause 2.1: one of ref or name, but not both). Pins name00401m3,
// name00401m4, name00401m5 and the name00501m series.
func (p *parser) checkElementRefExclusions(el *xdm.Node) {
	for _, attr := range []string{
		"name", "type", "form", "block", "nillable", "default", "fixed",
		// 1.1 adds these two to <element>, and both describe a
		// declaration, so both fall under the same exclusion.
		"targetNamespace", "abstract",
	} {
		if el.Attr("", attr) != nil {
			p.errs = append(p.errs, errorAt(el, "src-element.2.2",
				"an element reference may not also carry %q", attr))
		}
	}
	// The children the clause names. An inline type or an identity
	// constraint belongs to a declaration, and a ref makes one elsewhere.
	for _, child := range []string{
		"simpleType", "complexType", "key", "keyref", "unique", "alternative",
	} {
		if c := p.childElement(el, child); c != nil {
			p.errs = append(p.errs, errorAt(el, "src-element.2.2",
				"an element reference may not also contain <%s>", child))
		}
	}
}

// checkAllOccurs enforces the occurrence half of All Group Limited (§3.8.6
// clause 1.2): the particle whose {term} is an all group must have
// {max occurs} = 1.
//
// An all group means "each of these once, in any order". Repeating the group
// as a whole is not a language any finite automaton built from these members
// describes — the members' own bounds are what "any order" is defined against
// — so the spec confines the group to a single occurrence rather than giving
// the repetition a meaning. maxOccurs="unbounded" is the same fault written
// differently.
//
// minOccurs is left alone for maxOccurs=1: minOccurs="0" is an optional all
// group, which is explicitly allowed and which §3.4.2.3.3 relies on. But
// maxOccurs="0" makes the group unusable, and XSD 1.0 rejects it while 1.1
// permits it — mgO001 is expected invalid under 1.0 and is not scored under
// 1.1 at all. Pins mgAb004, mgAb006, mgAb007, mgC013 and mgO003.
func (p *parser) checkAllOccurs(el *xdm.Node, max int) {
	if max == Unbounded || max > 1 {
		p.errs = append(p.errs, errorAt(el, "cos-all-limited.1.2",
			"an xs:all group must have maxOccurs=1"))
		return
	}
	if max == 0 && p.schema.Version < Version11 {
		p.errs = append(p.errs, errorAt(el, "cos-all-limited.1.2",
			"an xs:all group must have maxOccurs=1"))
	}
}

// topLevelType reports whether a type definition element sits where a global
// definition may sit: directly under <xs:schema>, or under an <xs:redefine> or
// <xs:override>, which stand in for the schema of the document they name.
func topLevelType(el *xdm.Node) bool {
	parent := el.Parent
	if parent == nil || parent.Name.URI != NSSchema {
		return false
	}
	switch parent.Name.Local {
	case "schema", "redefine", "override":
		return true
	}
	return false
}

// checkTypeBaseCycles enforces ct-props-correct.3 (§3.4.6) and the matching
// clause for simple types, st-props-correct.2 (§3.14.6): "Circular definitions
// are disallowed, except for the ur-type definition. That is, it must be
// possible to reach the ur-type definition by repeatedly following the {base
// type definition}."
//
// Nothing diagnosed this before. The consumers that walk a base chain each
// carry their own private guard — spliceExtension takes a seen set,
// derivationMethodsTo caps itself at 64 steps — added one at a time as a
// circular schema hung or blew the stack somewhere new. Those guards keep the
// walk terminating; none of them makes the schema invalid, so a type that is
// its own base loaded clean and simply behaved as though the extension were
// not there. addB101 is the minimal case: a complexType named sAddress whose
// complexContent extension names sAddress as its base.
//
// Runs after the fixups have drained, because {base type definition} is filled
// in by a fixup and before that point every base is nil.
func (p *parser) checkTypeBaseCycles() {
	// Only named global types are roots: an anonymous type has no name to
	// be referred to by, so it cannot be anyone's base and cannot close a
	// cycle that does not already pass through a named type.
	//
	// The maps are walked through a sorted name list rather than directly.
	// A schema with two circular types would otherwise report them in a
	// different order on each run, and the suite compares the first error.
	names := make([]xdm.QName, 0, len(p.schema.Types))
	for name := range p.schema.Types {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i].URI != names[j].URI {
			return names[i].URI < names[j].URI
		}
		return names[i].Local < names[j].Local
	})

	for _, name := range names {
		t := p.schema.Types[name]
		if t == nil {
			continue
		}
		// "except for the ur-type definition": xs:anyType is its own
		// {base type definition}, and xs:anySimpleType's chain ends at
		// it, so both satisfy "reachable from itself" trivially. The
		// exception is not a courtesy — it is what terminates every
		// other chain, and the first version of this check omitted it
		// and rejected 11,044 of 14,405 schemas with
		// `type "anyType" is circular`.
		//
		// Tested by *name*, not by "is its own base": a type that names
		// itself as its base is exactly the shortest cycle the clause
		// forbids, and skipping every such type let addB101 — a
		// complexType named sAddress whose extension names sAddress —
		// through the check that was written for it.
		if name.URI == NSSchema &&
			(name.Local == "anyType" || name.Local == "anySimpleType") {
			continue
		}
		if b := baseOf(t); b == t {
			p.errs = append(p.errs, &ParseError{
				Code: "ct-props-correct.3",
				Message: fmt.Sprintf(
					"type %q is circular: it is its own base type",
					name.Local),
			})
			continue
		}
		// Walk the base chain looking for a return to t. The chain is
		// finite in a well-formed schema — it ends at xs:anyType, whose
		// base is itself — so the step cap only bounds the ill-formed
		// case where the cycle does not pass through t.
		cur := baseOf(t)
		for steps := 0; cur != nil && steps < 4096; steps++ {
			if cur == t {
				p.errs = append(p.errs, &ParseError{
					Code: "ct-props-correct.3",
					Message: fmt.Sprintf(
						"type %q is circular: it is reachable from "+
							"itself by following base type definitions",
						name.Local),
				})
				break
			}
			next := baseOf(cur)
			if next == cur {
				// xs:anyType and xs:anySimpleType are their own
				// base; that is the chain's terminator, not a
				// cycle.
				break
			}
			cur = next
		}
	}
}

// baseOf returns a type's {base type definition}, or nil when it has none.
func baseOf(t Type) Type {
	switch v := t.(type) {
	case *ComplexType:
		return v.Base
	case *SimpleType:
		return v.Base
	}
	return nil
}

// checkUnionMemberCycles enforces the union half of st-props-correct.2
// (§3.14.6): "Circular definitions are disallowed ... it must be possible to
// reach a built-in primitive datatype or the simple ur-type definition by
// repeatedly following the {base type definition}" — which for a union means
// its transitive {member type definitions} must not contain the union itself.
//
// The base-chain walk in checkTypeBaseCycles does not see this: a union's
// members are not its base, so two unions naming each other as members have
// entirely acyclic base chains (both derive from xs:anySimpleType) while the
// membership graph is a two-cycle. simple017 is exactly that pair — chap
// unions dt, and dt unions chap.
//
// Runs beside checkTypeBaseCycles, after the fixups have drained, because
// MemberTypes is filled in by a fixup.
func (p *parser) checkUnionMemberCycles() {
	// Sorted for the same reason as checkTypeBaseCycles: two circular
	// unions must be reported in the same order on every run.
	names := make([]xdm.QName, 0, len(p.schema.Types))
	for name := range p.schema.Types {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i].URI != names[j].URI {
			return names[i].URI < names[j].URI
		}
		return names[i].Local < names[j].Local
	})

	for _, name := range names {
		st, ok := p.schema.Types[name].(*SimpleType)
		if !ok || st == nil || len(st.MemberTypes) == 0 {
			continue
		}
		// Breadth-first over the membership graph. `seen` is a visited
		// set rather than a descent path: unlike cycleFrom above, a
		// member legitimately reached twice is not interesting here,
		// because the only question asked is whether st is reachable
		// from st, and any route back to it will be found from
		// whichever visit came first.
		seen := map[*SimpleType]bool{}
		queue := append([]*SimpleType(nil), st.MemberTypes...)
		for len(queue) > 0 {
			m := queue[0]
			queue = queue[1:]
			if m == nil {
				continue
			}
			if m == st {
				p.errs = append(p.errs, &ParseError{
					Code: "st-props-correct.2",
					Message: fmt.Sprintf(
						"union type %q is circular: it is a member "+
							"of itself, directly or through "+
							"another union", name.Local),
				})
				break
			}
			if seen[m] {
				continue
			}
			seen[m] = true
			queue = append(queue, m.MemberTypes...)
			// A member may be a *restriction* of a union rather
			// than a union itself, and the members it inherits are
			// reached only through its base. simple015 is that
			// shape: dt restricts an inline union.
			if b, ok := m.Base.(*SimpleType); ok && b != m {
				queue = append(queue, b)
			}
		}
	}
}
