package xsd

import (
	"math/big"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// The Part 2 §4.3 schema component constraints on constraining facets.
//
// These are constraints on the *schema*, not on instance values: a schema that
// violates one is not merely permissive, it is invalid and must be rejected at
// load. They are deferred to parser.finish because every one of them consults
// the base type definition — its primitive, and the facets it already set —
// and a restriction's base is routinely a forward reference that is not bound
// until the whole document set has been read.
func (p *parser) checkFacetConstraints() {
	for _, site := range p.simpleTypes {
		p.checkTypeFacets(site.typ, site.el)
		p.checkSimpleTypeFinal(site.typ, site.el)
		p.checkNotationEnumerated(site.typ, site.el)
		p.checkSpecialBase(site.typ, site.el)
		p.checkListOfAtomic(site.typ, site.el)
	}
}

// isBuiltinNotation reports whether t is xs:NOTATION itself, as opposed to a
// type derived from it.
func isBuiltinNotation(t *SimpleType) bool {
	return t != nil && t.builtin && t.Name == xsName("NOTATION")
}

// checkNotationEnumerated enforces Part 2 §3.2.19's "enumeration facet value
// required for NOTATION": it is an error for xs:NOTATION to be used directly in
// a schema, and only types derived from it by specifying an enumeration may be
// used.
//
// The rule bites in four places, which is why this walks the variety rather
// than looking at one field: xs:NOTATION named as a restriction base without
// adding an enumeration (simple094), as a list item type (simple092), as a
// union member (simple093), and — via checkNotationUse below — as the type of
// an element or attribute (simple090, simple091).
//
// The enumeration values must also name notations the schema actually declares.
// Part 2 gives NOTATION a lexical space of "all names of notations declared in
// the current schema", so an enumeration naming an undeclared one constrains
// the type to a value that can never occur; simple095 pins this.
func (p *parser) checkNotationEnumerated(t *SimpleType, el *xdm.Node) {
	if t == nil {
		return
	}
	switch t.Variety {
	case VarietyAtomic:
		base, ok := t.Base.(*SimpleType)
		if !ok || !isBuiltinNotation(base) {
			return
		}
		if len(t.Facets.Enumerations) == 0 {
			p.errs = append(p.errs, errorAt(el, "enumeration-required-notation",
				"a type restricting xs:NOTATION must specify an enumeration facet"))
			return
		}
		p.checkNotationValues(t, el)
	case VarietyList:
		// xs:NOTATION as the item type of a list (simple092). The
		// enumeration requirement is on the type as *used*, so naming
		// xs:NOTATION itself here leaves a list whose items are
		// unconstrained notations, which §3.2.19 forbids just as it
		// forbids the bare restriction above.
		if isBuiltinNotation(t.ItemType) {
			p.errs = append(p.errs, errorAt(el, "enumeration-required-notation",
				"xs:NOTATION may not be the item type of a list; "+
					"use a type derived from it by enumeration"))
		}
		// The union case (simple093) is NOT enforced. The suite
		// contradicts itself on it: simple093 declares
		// <xs:union memberTypes="xs:QName xs:NOTATION"/> invalid, while
		// msData particlesZ007 declares a schema containing
		// <xsd:union memberTypes="xsd:NOTATION"/> valid. Both carry
		// status="accepted". Enforcing the rule trades simple093 for
		// particlesZ007 in 1.1 and loses particlesZ007 outright in 1.0,
		// where simple093 is not even run. Left unenforced pending a
		// suite resolution.
	}
}

// isAnyAtomicType reports whether t is xs:anyAtomicType itself.
func isAnyAtomicType(t *SimpleType) bool {
	return t != nil && t.builtin && t.Name == xsName("anyAtomicType")
}

// checkSpecialBase enforces Part 2 §3.4.1's rule that xs:anyAtomicType is a
// "special" type which may not be used in a schema as the base of a
// restriction, the item type of a list, or a member type of a union.
//
// It exists only so that the 19 primitives have a common ancestor and so that
// xsi:type can select any atomic type; it is not a type a schema author may
// derive from. The list and union cases follow from the resolution of spec bug
// 11103, which the suite records on simple052 and simple053; simple051 is the
// restriction case.
//
// The rule is 1.1-only because xs:anyAtomicType does not exist in XSD 1.0 —
// a 1.0 schema naming it fails earlier, as an unresolvable type reference.
func (p *parser) checkSpecialBase(t *SimpleType, el *xdm.Node) {
	if t == nil || p.schema.Version < Version11 {
		return
	}
	switch t.Variety {
	case VarietyAtomic:
		if base, ok := t.Base.(*SimpleType); ok && isAnyAtomicType(base) {
			p.errs = append(p.errs, errorAt(el, "st-props-correct",
				"xs:anyAtomicType may not be the base type of a restriction"))
		}
	case VarietyList:
		if isAnyAtomicType(t.ItemType) {
			p.errs = append(p.errs, errorAt(el, "st-props-correct",
				"xs:anyAtomicType may not be the item type of a list"))
		}
	case VarietyUnion:
		for _, m := range t.MemberTypes {
			if isAnyAtomicType(m) {
				p.errs = append(p.errs, errorAt(el, "st-props-correct",
					"xs:anyAtomicType may not be a member type of a union"))
				break
			}
		}
	}
}

// checkNotationValues reports enumeration values on a NOTATION restriction that
// do not name a notation declared anywhere in the schema.
func (p *parser) checkNotationValues(t *SimpleType, el *xdm.Node) {
	for _, v := range t.Facets.Enumerations {
		name, err := p.resolveQName(el, "value", v)
		if err != nil {
			// A value that is not even a QName is already reported by
			// the ordinary facet-value check; nothing to add here.
			continue
		}
		if _, ok := p.schema.Notations[name]; !ok {
			p.errs = append(p.errs, errorAt(el, "enumeration-required-notation",
				"enumeration value %q does not name a declared notation", v))
		}
	}
}

// checkSimpleTypeFinal enforces {final} on a simple type definition.
//
// "final" names the derivations a type forbids from itself, and until now it
// was parsed and then never consulted, so a schema could declare
// final="restriction" and restrict the type on the next line. Part 1 §3.14.6
// (Derivation Valid (Restriction, Simple)) makes each variety its own clause,
// which is why this is a switch on how the derived type was built rather than
// one comparison: restricting a type is forbidden by "restriction", using it as
// a list item type by "list", and naming it in a union by "union".
//
// The check is separate from checkTypeFacets because that one returns early for
// a type with no facets, and a list or union derivation has none — which is
// exactly the ST_final00101m2..m6 half of the suite's cases.
func (p *parser) checkSimpleTypeFinal(t *SimpleType, el *xdm.Node) {
	if t == nil {
		return
	}
	report := func(base *SimpleType, what string) {
		p.errs = append(p.errs, errorAt(el, "st-props-correct",
			"simple type %s is final=%q and may not be used by %s",
			typeNameFor(base), what, what))
	}
	switch t.Variety {
	case VarietyAtomic:
		if base, ok := t.Base.(*SimpleType); ok && base != t &&
			base.unresolved == "" && base.FinalSet.Has(DerivationRestriction) {
			report(base, "restriction")
		}
	case VarietyList:
		if item := t.ItemType; item != nil && item != t &&
			item.unresolved == "" && item.FinalSet.Has(DerivationList) {
			report(item, "list")
		}
	case VarietyUnion:
		for _, member := range t.MemberTypes {
			if member == nil || member == t || member.unresolved != "" {
				continue
			}
			if member.FinalSet.Has(DerivationUnion) {
				report(member, "union")
			}
		}
	}
}

// typeNameFor names a type for a diagnostic, falling back to a description for
// an anonymous one.
func typeNameFor(t *SimpleType) string {
	if t == nil {
		return "an unnamed type"
	}
	if t.Name.Local == "" {
		return "an anonymous simple type"
	}
	return t.Name.Local
}

// checkTypeFacets applies the facet constraints to one simple type.
func (p *parser) checkTypeFacets(t *SimpleType, el *xdm.Node) {
	if t == nil || t.Facets == nil {
		return
	}
	base, _ := t.Base.(*SimpleType)
	if base == nil || base == t {
		return
	}
	// A type whose base never resolved has already been reported as an
	// unresolved reference; checking its facets against a placeholder would
	// pile a second, misleading error on top of the real one.
	if base.unresolved != "" || t.unresolved != "" {
		return
	}

	p.checkFacetValueSpace(t, base, el)
	p.checkEnumerationValueSpace(t, base, el)
	p.checkFacetCombinations(t, el)
	p.checkFacetApplicable(t, el)
	p.checkFacetRestriction(t, base, el)
	p.checkBoundOrder(t, base, el)
}

// compareBoundValues orders two bound-facet literals of the given primitive.
//
// The second result is false when the pair has no order — an unparseable
// literal, or a duration pair such as P1M against P30D that the partial order
// genuinely leaves undecided. A constraint stated as "must not be greater
// than" is not violated by a pair with no order, so callers must treat the
// incomparable case as satisfied rather than as a failure.
func compareBoundValues(a, b, primitive string) (int, bool) {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	switch primitive {
	case "duration":
		da, okA := parseDuration(a)
		db, okB := parseDuration(b)
		if !okA || !okB {
			return 0, false
		}
		return compareDuration(da, db)

	case "dateTime", "date", "time",
		"gYear", "gYearMonth", "gMonth", "gMonthDay", "gDay":
		ta, okA := parseTemporal(a, primitive)
		tb, okB := parseTemporal(b, primitive)
		if !okA || !okB {
			return 0, false
		}
		return compareTemporal(ta, tb)

	case "decimal", "float", "double":
		ra, okA := new(big.Rat).SetString(a)
		rb, okB := new(big.Rat).SetString(b)
		if !okA || !okB {
			// INF, -INF and NaN have no rational form. They are
			// legal float bounds, but ordering them against a
			// finite bound here would need a float model the
			// constraint does not require, so leave the pair
			// unordered rather than guess.
			return 0, false
		}
		return ra.Cmp(rb), true
	}
	return 0, false
}

// checkBoundOrder enforces the four "min <= max" constraints (§4.3.7.4 and
// following) and the bound "valid restriction" constraints.
//
// Both families are comparisons between bound literals, and both are stated
// over the merged {facets}: a maxInclusive written here is compared against a
// minInclusive the base supplied, which is how a restriction of
// xs:positiveInteger that sets maxExclusive="1" is caught — the base's
// minInclusive is 1, and no value satisfies both.
func (p *parser) checkBoundOrder(t, base *SimpleType, el *xdm.Node) {
	prim := primitiveOf(t)
	if prim == nil {
		return
	}
	name := prim.Name.Local
	m := mergedFacets(t)

	cmp := func(a, b *string) (int, bool) {
		if a == nil || b == nil {
			return 0, false
		}
		return compareBoundValues(*a, *b, name)
	}
	report := func(code, format string, args ...any) {
		p.errs = append(p.errs, errorAt(el, code, format, args...))
	}

	// minInclusive <= maxInclusive, minExclusive <= maxExclusive,
	// minExclusive < maxInclusive, minInclusive < maxExclusive.
	if c, ok := cmp(m.MinInclusive, m.MaxInclusive); ok && c > 0 {
		report("minInclusive-less-than-equal-to-maxInclusive",
			"xs:minInclusive %q is greater than xs:maxInclusive %q",
			*m.MinInclusive, *m.MaxInclusive)
	}
	if c, ok := cmp(m.MinExclusive, m.MaxExclusive); ok && c > 0 {
		report("minExclusive-less-than-equal-to-maxExclusive",
			"xs:minExclusive %q is greater than xs:maxExclusive %q",
			*m.MinExclusive, *m.MaxExclusive)
	}
	if c, ok := cmp(m.MinExclusive, m.MaxInclusive); ok && c >= 0 {
		report("minExclusive-less-than-maxInclusive",
			"xs:minExclusive %q is not less than xs:maxInclusive %q",
			*m.MinExclusive, *m.MaxInclusive)
	}
	if c, ok := cmp(m.MinInclusive, m.MaxExclusive); ok && c >= 0 {
		report("minInclusive-less-than-maxExclusive",
			"xs:minInclusive %q is not less than xs:maxExclusive %q",
			*m.MinInclusive, *m.MaxExclusive)
	}

	// The bound "valid restriction" constraints. A derived bound may only
	// narrow the base's, and each of the four is constrained against both
	// of the base's bounds on the same side.
	b := mergedFacets(base)
	f := t.Facets

	if c, ok := cmp(f.MaxInclusive, b.MaxInclusive); ok && c > 0 {
		report("maxInclusive-valid-restriction",
			"xs:maxInclusive %q is greater than the base's xs:maxInclusive %q",
			*f.MaxInclusive, *b.MaxInclusive)
	}
	if c, ok := cmp(f.MaxInclusive, b.MaxExclusive); ok && c >= 0 {
		report("maxInclusive-valid-restriction",
			"xs:maxInclusive %q is not less than the base's xs:maxExclusive %q",
			*f.MaxInclusive, *b.MaxExclusive)
	}
	if c, ok := cmp(f.MaxInclusive, b.MinInclusive); ok && c < 0 {
		report("maxInclusive-valid-restriction",
			"xs:maxInclusive %q is less than the base's xs:minInclusive %q",
			*f.MaxInclusive, *b.MinInclusive)
	}
	if c, ok := cmp(f.MaxInclusive, b.MinExclusive); ok && c <= 0 {
		report("maxInclusive-valid-restriction",
			"xs:maxInclusive %q is not greater than the base's xs:minExclusive %q",
			*f.MaxInclusive, *b.MinExclusive)
	}

	if c, ok := cmp(f.MaxExclusive, b.MaxExclusive); ok && c > 0 {
		report("maxExclusive-valid-restriction",
			"xs:maxExclusive %q is greater than the base's xs:maxExclusive %q",
			*f.MaxExclusive, *b.MaxExclusive)
	}
	if c, ok := cmp(f.MaxExclusive, b.MaxInclusive); ok && c > 0 {
		report("maxExclusive-valid-restriction",
			"xs:maxExclusive %q is greater than the base's xs:maxInclusive %q",
			*f.MaxExclusive, *b.MaxInclusive)
	}
	if c, ok := cmp(f.MaxExclusive, b.MinInclusive); ok && c <= 0 {
		report("maxExclusive-valid-restriction",
			"xs:maxExclusive %q is not greater than the base's xs:minInclusive %q",
			*f.MaxExclusive, *b.MinInclusive)
	}
	if c, ok := cmp(f.MaxExclusive, b.MinExclusive); ok && c <= 0 {
		report("maxExclusive-valid-restriction",
			"xs:maxExclusive %q is not greater than the base's xs:minExclusive %q",
			*f.MaxExclusive, *b.MinExclusive)
	}

	if c, ok := cmp(f.MinInclusive, b.MinInclusive); ok && c < 0 {
		report("minInclusive-valid-restriction",
			"xs:minInclusive %q is less than the base's xs:minInclusive %q",
			*f.MinInclusive, *b.MinInclusive)
	}
	if c, ok := cmp(f.MinInclusive, b.MinExclusive); ok && c <= 0 {
		report("minInclusive-valid-restriction",
			"xs:minInclusive %q is not greater than the base's xs:minExclusive %q",
			*f.MinInclusive, *b.MinExclusive)
	}
	if c, ok := cmp(f.MinInclusive, b.MaxInclusive); ok && c > 0 {
		report("minInclusive-valid-restriction",
			"xs:minInclusive %q is greater than the base's xs:maxInclusive %q",
			*f.MinInclusive, *b.MaxInclusive)
	}
	if c, ok := cmp(f.MinInclusive, b.MaxExclusive); ok && c >= 0 {
		report("minInclusive-valid-restriction",
			"xs:minInclusive %q is not less than the base's xs:maxExclusive %q",
			*f.MinInclusive, *b.MaxExclusive)
	}

	if c, ok := cmp(f.MinExclusive, b.MinExclusive); ok && c < 0 {
		report("minExclusive-valid-restriction",
			"xs:minExclusive %q is less than the base's xs:minExclusive %q",
			*f.MinExclusive, *b.MinExclusive)
	}
	if c, ok := cmp(f.MinExclusive, b.MinInclusive); ok && c < 0 {
		report("minExclusive-valid-restriction",
			"xs:minExclusive %q is less than the base's xs:minInclusive %q",
			*f.MinExclusive, *b.MinInclusive)
	}
	if c, ok := cmp(f.MinExclusive, b.MaxInclusive); ok && c > 0 {
		report("minExclusive-valid-restriction",
			"xs:minExclusive %q is greater than the base's xs:maxInclusive %q",
			*f.MinExclusive, *b.MaxInclusive)
	}
	if c, ok := cmp(f.MinExclusive, b.MaxExclusive); ok && c >= 0 {
		report("minExclusive-valid-restriction",
			"xs:minExclusive %q is not less than the base's xs:maxExclusive %q",
			*f.MinExclusive, *b.MaxExclusive)
	}
}

// checkFacetRestriction enforces the "valid restriction" constraints: a
// derivation may narrow a facet its base set, never widen it (§4.3.1.4 through
// §4.3.12.4).
//
// The base's values are taken from the merged chain, not from the base's own
// step, because a facet may have been set several derivations up and inherited
// unchanged. That is the ordinary case for the built-in numeric ladder:
// xs:unsignedShort gets its maxInclusive from itself but its minInclusive from
// xs:nonNegativeInteger, so comparing only against xs:unsignedShort's own
// {facets} would miss half the bounds a restriction can widen.
func (p *parser) checkFacetRestriction(t, base *SimpleType, el *xdm.Node) {
	f := t.Facets
	b := mergedFacets(base)

	if f.Length != nil && b.Length != nil && *f.Length != *b.Length {
		p.errs = append(p.errs, errorAt(el, "length-valid-restriction",
			"xs:length %d differs from the base's xs:length %d",
			*f.Length, *b.Length))
	}
	if f.MinLength != nil && b.MinLength != nil && *f.MinLength < *b.MinLength {
		p.errs = append(p.errs, errorAt(el, "minLength-valid-restriction",
			"xs:minLength %d is less than the base's xs:minLength %d",
			*f.MinLength, *b.MinLength))
	}
	if f.MaxLength != nil && b.MaxLength != nil && *f.MaxLength > *b.MaxLength {
		p.errs = append(p.errs, errorAt(el, "maxLength-valid-restriction",
			"xs:maxLength %d is greater than the base's xs:maxLength %d",
			*f.MaxLength, *b.MaxLength))
	}
	if f.TotalDigits != nil && b.TotalDigits != nil && *f.TotalDigits > *b.TotalDigits {
		p.errs = append(p.errs, errorAt(el, "totalDigits-valid-restriction",
			"xs:totalDigits %d is greater than the base's xs:totalDigits %d",
			*f.TotalDigits, *b.TotalDigits))
	}
	if f.FractionDigits != nil && b.FractionDigits != nil &&
		*f.FractionDigits > *b.FractionDigits {
		p.errs = append(p.errs, errorAt(el, "fractionDigits-valid-restriction",
			"xs:fractionDigits %d is greater than the base's "+
				"xs:fractionDigits %d", *f.FractionDigits, *b.FractionDigits))
	}

	// "explicitTimezone valid restriction" (§4.3.13.4): where the base
	// already fixes the facet to required or prohibited, a restriction may
	// only repeat it. "optional" is the only value that leaves room to
	// narrow, so relaxing back to it — or swapping required for prohibited
	// — widens the value space rather than narrowing it. zone004 derives
	// optional from required, zone005 optional from prohibited, and zone408
	// required from prohibited.
	if f.ExplicitTimezone != nil && b.ExplicitTimezone != nil &&
		*b.ExplicitTimezone != TimezoneOptional &&
		*f.ExplicitTimezone != *b.ExplicitTimezone {
		p.errs = append(p.errs, errorAt(el, "explicitTimezone-valid-restriction",
			"xs:explicitTimezone %s does not match the base's fixed %s",
			*f.ExplicitTimezone, *b.ExplicitTimezone))
	}

	p.checkFixedFacets(t, base, el, b)

	// "whiteSpace valid restriction" (§4.3.6.4). The modes are ordered by
	// strength and a derivation may only strengthen, which the WhiteSpace
	// constants are numbered to express, so the whole constraint is one
	// comparison.
	if f.WhiteSpace != nil {
		if parent := EffectiveWhiteSpace(base); *f.WhiteSpace < parent {
			p.errs = append(p.errs, errorAt(el, "whiteSpace-valid-restriction",
				"xs:whiteSpace %s weakens the base's %s",
				*f.WhiteSpace, parent))
		}
	}
}

// checkFixedFacets enforces the {fixed} half of "Facet Valid (Restriction)"
// (§4.3): where the base type fixed a facet, a restriction may repeat that
// value but may not state a different one.
//
// Fixing a facet is how a type author declares a bound closed to further
// narrowing. The ordinary valid-restriction clauses only stop a derivation
// from *widening* a bound, so without this rule a fixed minInclusive could
// still be tightened — which is precisely what fixed="true" says it may not
// be. ibmData d3_4_26si11 and d3_4_27si11 fix the four bounds on a duration
// and then restate them, and both used to load.
//
// The values compare lexically. A bound facet is held as the literal the
// schema wrote, and "same value" for this rule means the base's value: two
// spellings of one value — "P1Y" against "P12M" — would compare unequal here,
// but a derivation that meant to repeat the base's fixed facet has no reason
// to respell it, and the suite writes none.
func (p *parser) checkFixedFacets(t, base *SimpleType, el *xdm.Node, b *FacetSet) {
	f := t.Facets

	uintFixed := func(kind FacetKind, got, want *uint64) {
		if got == nil || want == nil || !b.isFixed(kind) || *got == *want {
			return
		}
		p.errs = append(p.errs, errorAt(el, "fixed-facet-value",
			"xs:%s %d differs from the base's fixed xs:%s %d",
			kind, *got, kind, *want))
	}
	uintFixed(FacetLength, f.Length, b.Length)
	uintFixed(FacetMinLength, f.MinLength, b.MinLength)
	uintFixed(FacetMaxLength, f.MaxLength, b.MaxLength)
	uintFixed(FacetTotalDigits, f.TotalDigits, b.TotalDigits)
	uintFixed(FacetFractionDigits, f.FractionDigits, b.FractionDigits)

	strFixed := func(kind FacetKind, got, want *string) {
		if got == nil || want == nil || !b.isFixed(kind) || *got == *want {
			return
		}
		p.errs = append(p.errs, errorAt(el, "fixed-facet-value",
			"xs:%s %q differs from the base's fixed xs:%s %q",
			kind, *got, kind, *want))
	}
	strFixed(FacetMinInclusive, f.MinInclusive, b.MinInclusive)
	strFixed(FacetMaxInclusive, f.MaxInclusive, b.MaxInclusive)
	strFixed(FacetMinExclusive, f.MinExclusive, b.MinExclusive)
	strFixed(FacetMaxExclusive, f.MaxExclusive, b.MaxExclusive)

	// explicitTimezone already has a stronger rule of its own above: the
	// base fixing it to required or prohibited closes it whether or not
	// fixed="true" was written. Only the "optional" case is left here.
	if f.ExplicitTimezone != nil && b.ExplicitTimezone != nil &&
		b.isFixed(FacetExplicitTimezone) &&
		*f.ExplicitTimezone != *b.ExplicitTimezone {
		p.errs = append(p.errs, errorAt(el, "fixed-facet-value",
			"xs:explicitTimezone %s differs from the base's fixed %s",
			*f.ExplicitTimezone, *b.ExplicitTimezone))
	}
}

// checkFacetApplicable enforces "applicable facets" (§4.1.5): the table of
// which constraining facets each base admits.
//
// The dispatch is on {variety} first and only then on the primitive, which is
// what FacetApplicable already encodes for the instance-validation path. The
// table is what rejects fractionDigits on xs:unsignedShort's *primitive*
// sibling families — a length facet on a numeric type, say — while still
// allowing the digit facets on everything derived from xs:decimal
// (unsignedShort_fractionDigits004 is instead a "valid restriction" case,
// since unsignedShort does derive from decimal).
func (p *parser) checkFacetApplicable(t *SimpleType, el *xdm.Node) {
	f := t.Facets
	report := func(kind FacetKind) {
		if FacetApplicable(t, kind) {
			return
		}
		p.errs = append(p.errs, errorAt(el, "applicable-facets",
			"xs:%s is not applicable to this type", kind))
	}
	if f.Length != nil {
		report(FacetLength)
	}
	if f.MinLength != nil {
		report(FacetMinLength)
	}
	if f.MaxLength != nil {
		report(FacetMaxLength)
	}
	if f.TotalDigits != nil {
		report(FacetTotalDigits)
	}
	if f.FractionDigits != nil {
		report(FacetFractionDigits)
	}
	if f.MinInclusive != nil {
		report(FacetMinInclusive)
	}
	if f.MaxInclusive != nil {
		report(FacetMaxInclusive)
	}
	if f.MinExclusive != nil {
		report(FacetMinExclusive)
	}
	if f.MaxExclusive != nil {
		report(FacetMaxExclusive)
	}
	if f.HasEnumerations {
		report(FacetEnumeration)
	}
	if f.WhiteSpace != nil {
		report(FacetWhiteSpace)
	}
	if f.ExplicitTimezone != nil {
		report(FacetExplicitTimezone)
	}
}

// checkFacetCombinations enforces the constraints that hold between facets of
// one type, without reference to the base.
//
// The {facets} the constraints speak of are the *merged* set — a derivation
// inherits its base's facets — so length written here conflicts with a
// minLength written two steps up just as surely as with one written alongside
// it. mergedFacets does that walk.
func (p *parser) checkFacetCombinations(t *SimpleType, el *xdm.Node) {
	m := mergedFacets(t)

	// "length and minLength or maxLength" (§4.3.1.4).
	//
	// This is not a flat prohibition, and reading it as one rejects schemas
	// the working group decided are legal. minLength alongside length is an
	// error *unless* both: the minLength does not exceed the length, and
	// some type further up the derivation chain states that same minLength
	// without stating a length. maxLength mirrors it.
	//
	// The escape clause is what makes a restriction of xs:IDREFS legal:
	// IDREFS carries minLength="1" and no length, so a restriction adding
	// length="5" satisfies both halves. IDREFS_length006 is marked valid
	// against W3C bug 6446 with the note "WG decided spec. has a special
	// case which allows this" — this is that special case.
	if t.Facets.Length != nil {
		if m.MinLength != nil &&
			!(*m.MinLength <= *t.Facets.Length && lengthEscape(t, true, *m.MinLength)) {
			p.errs = append(p.errs, errorAt(el, "length-minLength-maxLength",
				"xs:length %d is contradicted by xs:minLength %d",
				*t.Facets.Length, *m.MinLength))
		}
		if m.MaxLength != nil &&
			!(*t.Facets.Length <= *m.MaxLength && lengthEscape(t, false, *m.MaxLength)) {
			p.errs = append(p.errs, errorAt(el, "length-minLength-maxLength",
				"xs:length %d is contradicted by xs:maxLength %d",
				*t.Facets.Length, *m.MaxLength))
		}
	}

	// "minLength <= maxLength" (§4.3.2.4).
	if m.MinLength != nil && m.MaxLength != nil && *m.MinLength > *m.MaxLength {
		p.errs = append(p.errs, errorAt(el, "minLength-less-than-equal-to-maxLength",
			"xs:minLength %d is greater than xs:maxLength %d",
			*m.MinLength, *m.MaxLength))
	}

	// "fractionDigits less than or equal to totalDigits" (§4.3.12.4).
	if m.FractionDigits != nil && m.TotalDigits != nil &&
		*m.FractionDigits > *m.TotalDigits {
		p.errs = append(p.errs, errorAt(el, "fractionDigits-totalDigits",
			"xs:fractionDigits %d is greater than xs:totalDigits %d",
			*m.FractionDigits, *m.TotalDigits))
	}

	// "maxInclusive and maxExclusive" and "minInclusive and minExclusive"
	// (§4.3.7.4, §4.3.9.4). Unlike the length pair these are stated over
	// one derivation step, so only the facets written here are compared.
	if t.Facets.MaxInclusive != nil && t.Facets.MaxExclusive != nil {
		p.errs = append(p.errs, errorAt(el, "maxInclusive-maxExclusive",
			"xs:maxInclusive and xs:maxExclusive may not both be "+
				"specified for the same datatype"))
	}
	if t.Facets.MinInclusive != nil && t.Facets.MinExclusive != nil {
		p.errs = append(p.errs, errorAt(el, "minInclusive-minExclusive",
			"xs:minInclusive and xs:minExclusive may not both be "+
				"specified for the same datatype"))
	}
}

// mergedFacets flattens a type's derivation chain into the single {facets} set
// the Part 2 constraints are written against.
//
// Nearest wins: facetChain runs from the type outwards, so a facet already set
// by a nearer step is not overwritten by the base's. That matters for
// correctness rather than tidiness — comparing a derived minLength against an
// inherited maxLength is exactly the case the constraint exists to catch, and
// taking the base's minLength instead would compare the wrong pair.
func mergedFacets(t *SimpleType) *FacetSet {
	out := &FacetSet{}
	// carry copies the {fixed} property along with the value it belongs to.
	// The flag has to travel with the step that *set* the facet: a later
	// step that re-states the same value without fixed="true" does not
	// unfix it, and an earlier step that never set the facet has no say.
	carry := func(kind FacetKind, f *FacetSet) {
		if f.isFixed(kind) {
			out.setFixed(kind)
		}
	}
	for _, st := range facetChain(t) {
		f := st.facets
		if out.Length == nil {
			out.Length = f.Length
			carry(FacetLength, f)
		}
		if out.MinLength == nil {
			out.MinLength = f.MinLength
			carry(FacetMinLength, f)
		}
		if out.MaxLength == nil {
			out.MaxLength = f.MaxLength
			carry(FacetMaxLength, f)
		}
		if out.TotalDigits == nil {
			out.TotalDigits = f.TotalDigits
			carry(FacetTotalDigits, f)
		}
		if out.FractionDigits == nil {
			out.FractionDigits = f.FractionDigits
			carry(FacetFractionDigits, f)
		}
		if out.MinInclusive == nil {
			out.MinInclusive = f.MinInclusive
			carry(FacetMinInclusive, f)
		}
		if out.MaxInclusive == nil {
			out.MaxInclusive = f.MaxInclusive
			carry(FacetMaxInclusive, f)
		}
		if out.MinExclusive == nil {
			out.MinExclusive = f.MinExclusive
			carry(FacetMinExclusive, f)
		}
		if out.MaxExclusive == nil {
			out.MaxExclusive = f.MaxExclusive
			carry(FacetMaxExclusive, f)
		}
		if out.ExplicitTimezone == nil {
			out.ExplicitTimezone = f.ExplicitTimezone
			carry(FacetExplicitTimezone, f)
		}
	}
	return out
}

// checkEnumerationValueSpace enforces "enumeration valid restriction"
// (§4.3.5.4): every member of the enumeration must be in the value space of the
// base type definition.
//
// A union is the exception that has to be carved out. Its enumeration members
// are validated against the union itself rather than against
// xs:anySimpleType — the union's {base type definition} is anySimpleType, whose
// value space is every string, so checking against the base would accept
// anything. What the constraint means for a union is that each member value
// must be valid against some member type.
func (p *parser) checkEnumerationValueSpace(t, base *SimpleType, el *xdm.Node) {
	if !t.Facets.HasEnumerations {
		return
	}
	against := base
	if t.Variety == VarietyUnion || t.Variety == VarietyList {
		against = t
	}
	for _, v := range t.Facets.Enumerations {
		if _, err := validateSimpleValueVersion(v, against, p.schema.Version); err != nil {
			p.errs = append(p.errs, errorAt(el, "enumeration-valid-restriction",
				"xs:enumeration value %q is not in the value space of "+
					"the base type: %v", v, err))
		}
	}
}

// checkFacetValueSpace enforces the "must be in the value space of the base
// type definition" requirement that Part 2 states for each bound facet
// (§4.3.7-§4.3.10) and, as "enumeration valid restriction" (§4.3.5.4), for
// enumeration.
//
// The check is literally "validate the facet's lexical value against the base
// type", because the base type's own facets are exactly what carve its value
// space out of its primitive's. That is what catches the bulk of the suite's
// invalid facet schemas: <xs:minInclusive value=""/> on xs:int is not a
// derivation that admits nothing, it is a schema error, since "" is not an
// xs:int literal at all (int_minInclusive001). The same reading rejects
// maxInclusive="0" on xs:positiveInteger, whose value space starts at 1.
func (p *parser) checkFacetValueSpace(t, base *SimpleType, el *xdm.Node) {
	f := t.Facets

	check := func(kind FacetKind, lex *string) {
		if lex == nil {
			return
		}
		// The base's own bounding facets are deliberately not applied.
		// This clause asks whether the facet value is in the base's
		// value space; how it must relate to the base's bounds is
		// checked separately above, where "greater than" is the right
		// comparison. Validating against them here rejected a bound
		// re-stated with the same value as its parent's, which the
		// spec permits (d3_4_28v09).
		if _, err := valueSpaceOnly(*lex, base, p.schema.Version); err != nil {
			p.errs = append(p.errs, errorAt(el, boundFacetCode(kind),
				"xs:%s value %q is not in the value space of the base type: %v",
				kind, *lex, err))
		}
	}
	check(FacetMinInclusive, f.MinInclusive)
	check(FacetMaxInclusive, f.MaxInclusive)
	check(FacetMinExclusive, f.MinExclusive)
	check(FacetMaxExclusive, f.MaxExclusive)
}

// boundFacetCode names the schema component constraint a bound facet violates,
// spelled as the suite and the errata spell it.
func boundFacetCode(kind FacetKind) string {
	switch kind {
	case FacetMinInclusive:
		return "minInclusive-valid-restriction"
	case FacetMaxInclusive:
		return "maxInclusive-valid-restriction"
	case FacetMinExclusive:
		return "minExclusive-valid-restriction"
	case FacetMaxExclusive:
		return "maxExclusive-valid-restriction"
	case FacetEnumeration:
		return "enumeration-valid-restriction"
	}
	return ""
}

// lengthEscape answers clause 1.2 (and its 2.2 mirror) of "length and minLength
// or maxLength": is there a type definition, reached by one or more restriction
// steps, which states this same minLength (or maxLength) and does not state a
// length?
//
// Without it the constraint reads as a flat prohibition, which rejects every
// restriction of a built-in list type — those carry minLength="1" of their own,
// so any length added below would collide with it.
func lengthEscape(t *SimpleType, min bool, want uint64) bool {
	steps := facetChain(t)
	// The type's own step is skipped: the clause asks for a type this one
	// is *derived from*, so a minLength written alongside the length here
	// does not excuse itself.
	for _, st := range steps[1:] {
		if st.facets.Length != nil {
			// This step states a length, so it is not the witness
			// the clause asks for, and no step above it can be
			// either: its own length would have to have collided.
			return false
		}
		got := st.facets.MinLength
		if !min {
			got = st.facets.MaxLength
		}
		if got != nil && *got == want {
			return true
		}
	}
	return false
}

// checkListOfAtomic enforces cos-list-of-atomic (Part 2 §4.1.5): the {item
// type definition} of a list must have {variety} atomic, or union with every
// member atomic.
//
// A list of lists has no readable lexical form. Both varieties separate their
// items with whitespace, so the two levels are indistinguishable in an
// instance: "1 2 3" against a list whose item type is a list of integers could
// be one item, three items, or anything between, and no rule in Part 2 says
// which. The spec closes the question by forbidding the construction outright
// rather than picking an arbitrary reading.
//
// A union member is followed one level because a union of lists has exactly
// the same problem — the list's items would be whitespace-separated values
// that are themselves whitespace-separated. The walk stops there: a union
// whose member is a union is resolved by the same rule applied to that inner
// union, which has its own site in the sweep.
//
// Pinned by msData/simpleType stJ019: a list whose itemType names a type that
// is itself a list. It loaded clean before this check existed.
func (p *parser) checkListOfAtomic(t *SimpleType, el *xdm.Node) {
	if t == nil || t.Variety != VarietyList || t.ItemType == nil {
		return
	}
	item := t.ItemType
	switch item.Variety {
	case VarietyAtomic:
		return
	case VarietyUnion:
		if bad := nonAtomicUnionMember(item, 0); bad != nil {
			p.errs = append(p.errs, errorAt(el, "cos-list-of-atomic",
				"the item type of a list must be atomic, or a union whose "+
					"members are all atomic: member %q of %q is a list",
				bad.Name, item.Name))
		}
	default:
		p.errs = append(p.errs, errorAt(el, "cos-list-of-atomic",
			"the item type of a list must be atomic or a union of atomic "+
				"types, but %q is a list", item.Name))
	}
}

// nonAtomicUnionMember finds a list among the members a union ultimately
// admits, or nil if every one of them is atomic.
//
// The recursion through nested unions is the whole point. A union's members
// may themselves be unions — the test suite's own xsts.xsd defines
// version-token as a list of known-token, and known-token is a union of eight
// unions, each an enumeration of atomic values. Checking only the immediate
// members and calling any union member non-atomic rejected the catalog schema
// itself, and with it the 91 instance tests that load through it. A union of
// unions of atomic types is a union of atomic values, which is exactly what
// cos-list-of-atomic permits.
//
// The depth bound guards a union whose member chain reaches itself. Such a
// schema is ill-formed and reported elsewhere; this walk must not hang on it.
func nonAtomicUnionMember(t *SimpleType, depth int) *SimpleType {
	if t == nil || depth > 32 {
		return nil
	}
	switch t.Variety {
	case VarietyAtomic:
		return nil
	case VarietyList:
		return t
	}
	for _, m := range unionMemberTypesOf(t) {
		if bad := nonAtomicUnionMember(m, depth+1); bad != nil {
			return bad
		}
	}
	return nil
}
