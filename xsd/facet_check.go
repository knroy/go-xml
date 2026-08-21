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
	}
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

	// "length and minLength or maxLength" (§4.3.1.4). The constraint has an
	// escape clause for the case where the base fixed the same minLength
	// and did not specify length, but the plain conflict — both written on
	// one restriction step — is always an error (string_length006).
	if t.Facets.Length != nil {
		if t.Facets.MinLength != nil {
			p.errs = append(p.errs, errorAt(el, "length-minLength-maxLength",
				"xs:length and xs:minLength may not both be specified "+
					"in the same derivation step"))
		}
		if t.Facets.MaxLength != nil {
			p.errs = append(p.errs, errorAt(el, "length-minLength-maxLength",
				"xs:length and xs:maxLength may not both be specified "+
					"in the same derivation step"))
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
	for _, st := range facetChain(t) {
		f := st.facets
		if out.Length == nil {
			out.Length = f.Length
		}
		if out.MinLength == nil {
			out.MinLength = f.MinLength
		}
		if out.MaxLength == nil {
			out.MaxLength = f.MaxLength
		}
		if out.TotalDigits == nil {
			out.TotalDigits = f.TotalDigits
		}
		if out.FractionDigits == nil {
			out.FractionDigits = f.FractionDigits
		}
		if out.MinInclusive == nil {
			out.MinInclusive = f.MinInclusive
		}
		if out.MaxInclusive == nil {
			out.MaxInclusive = f.MaxInclusive
		}
		if out.MinExclusive == nil {
			out.MinExclusive = f.MinExclusive
		}
		if out.MaxExclusive == nil {
			out.MaxExclusive = f.MaxExclusive
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
		if _, err := validateSimpleValueVersion(*lex, base, p.schema.Version); err != nil {
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
