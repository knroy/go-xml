package xsd

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// FacetKind identifies a constraining facet (Part 2 §4.3).
type FacetKind uint8

// The twelve constraining facets. The fundamental facets (§4.2) are properties
// of a type rather than constraints an author writes, and are not modelled
// here.
const (
	FacetLength FacetKind = iota
	FacetMinLength
	FacetMaxLength
	FacetPattern
	FacetEnumeration
	FacetWhiteSpace
	FacetMaxInclusive
	FacetMaxExclusive
	FacetMinInclusive
	FacetMinExclusive
	FacetTotalDigits
	FacetFractionDigits
	// FacetExplicitTimezone is the XSD 1.1 facet constraining whether a
	// date or time value carries a timezone.
	FacetExplicitTimezone
)

// String names the facet as it is spelled in a schema document.
func (f FacetKind) String() string {
	switch f {
	case FacetLength:
		return "length"
	case FacetMinLength:
		return "minLength"
	case FacetMaxLength:
		return "maxLength"
	case FacetPattern:
		return "pattern"
	case FacetEnumeration:
		return "enumeration"
	case FacetWhiteSpace:
		return "whiteSpace"
	case FacetMaxInclusive:
		return "maxInclusive"
	case FacetMaxExclusive:
		return "maxExclusive"
	case FacetMinInclusive:
		return "minInclusive"
	case FacetMinExclusive:
		return "minExclusive"
	case FacetTotalDigits:
		return "totalDigits"
	case FacetFractionDigits:
		return "fractionDigits"
	case FacetExplicitTimezone:
		return "explicitTimezone"
	}
	return fmt.Sprintf("FacetKind(%d)", uint8(f))
}

// WhiteSpace is the value of the whiteSpace facet (Part 2 §4.3.6).
//
// The three modes are ordered by strength, and a derivation may only make the
// value stronger: preserve → replace → collapse. That ordering is what the
// comparison in checkWhiteSpaceRestriction relies on.
type WhiteSpace uint8

// The whiteSpace modes.
const (
	// WhitePreserve leaves the value alone. It is the value for xs:string
	// and for xs:anySimpleType.
	WhitePreserve WhiteSpace = iota
	// WhiteReplace maps tab, newline and carriage return to a space.
	WhiteReplace
	// WhiteCollapse additionally merges runs of spaces and trims the ends.
	// Every built-in type except the string family collapses.
	WhiteCollapse
)

// String names the mode as it is spelled in a schema document.
func (w WhiteSpace) String() string {
	switch w {
	case WhitePreserve:
		return "preserve"
	case WhiteReplace:
		return "replace"
	case WhiteCollapse:
		return "collapse"
	}
	return fmt.Sprintf("WhiteSpace(%d)", uint8(w))
}

// Normalize applies the whiteSpace facet to a lexical value.
//
// Only the four XML whitespace characters take part. Using unicode.IsSpace here
// would fold characters such as U+00A0 that XML does not treat as whitespace,
// which would silently accept values the spec rejects.
func (w WhiteSpace) Normalize(s string) string {
	switch w {
	case WhitePreserve:
		return s
	case WhiteReplace:
		return strings.Map(func(r rune) rune {
			if r == '\t' || r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, s)
	case WhiteCollapse:
		var b strings.Builder
		b.Grow(len(s))
		space := false
		started := false
		for _, r := range s {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				space = started
				continue
			}
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteRune(r)
			started = true
		}
		return b.String()
	}
	return s
}

// A Pattern is a compiled pattern facet.
//
// XSD patterns are anchored: the whole value must match. This differs from
// fn:matches in XPath, which is a containment test, so the regex translation
// this package shares with the xpath package must be wrapped before use. See
// compilePattern.
type Pattern struct {
	// Source is the pattern as written, kept for diagnostics and for the
	// schema-component constraints that compare patterns textually.
	Source string
	// re matches the whole value.
	re matcher
}

// matcher is the subset of *regexp.Regexp this package needs, named so that the
// pattern compiler can be tested without a real regexp.
type matcher interface {
	MatchString(s string) bool
}

// Matches reports whether the value satisfies the pattern.
func (p *Pattern) Matches(s string) bool { return p.re.MatchString(s) }

// FacetSet holds the constraining facets applied at one derivation step.
//
// A facet is present only if the schema document set it at this step; the
// inherited value comes from walking the base chain. Keeping the steps separate
// rather than flattening them is what makes the schema-component constraints on
// restriction checkable, since those compare a derived facet against the value
// it narrows.
type FacetSet struct {
	Length         *uint64
	MinLength      *uint64
	MaxLength      *uint64
	TotalDigits    *uint64
	FractionDigits *uint64

	// WhiteSpace is the whiteSpace facet, and Fixed records whether the
	// schema wrote fixed="true" on it. The built-in types fix it, which is
	// why a user type cannot loosen xs:token back to preserve.
	WhiteSpace      *WhiteSpace
	WhiteSpaceFixed bool

	// MinInclusive and friends hold bounds. They are stored as lexical
	// strings because the type they must be parsed against is the type
	// being defined, which is not complete while its own facets are read.
	MinInclusive *string
	MaxInclusive *string
	MinExclusive *string
	MaxExclusive *string

	// Patterns from a single derivation step are alternatives: a value
	// satisfying any one of them satisfies the step. Patterns from
	// *different* steps are conjunctive. That asymmetry is why patterns are
	// held per step rather than merged into one list.
	Patterns []*Pattern

	// Enumerations are the permitted values, lexical at this stage. An
	// empty non-nil slice is meaningful — it permits nothing — so presence
	// is tested with HasEnumeration rather than len.
	Enumerations    []string
	HasEnumerations bool

	// EnumerationQNames holds the expanded form of each enumeration value,
	// parallel to Enumerations, for a type whose value space is QNames —
	// xs:QName, xs:NOTATION and anything derived from them.
	//
	// Those two types compare by expanded name, not by spelling: Part 2
	// §3.2.18 gives xs:NOTATION the value space of the QNames of the
	// notations declared in the schema, so an instance writing "one:mp3"
	// satisfies an enumeration written "smokey:mp3" whenever both prefixes
	// bind the same URI. The prefix in the facet resolves against the
	// *schema* document's namespaces and the prefix in the instance against
	// the *instance* document's, so neither lexical form can be compared
	// against the other; the expansion has to be recorded here, where the
	// facet's own element is still in hand.
	//
	// An entry is the zero QName when the facet's prefix was not bound, and
	// is only populated for a namespace-sensitive type, so an empty slice
	// means "compare lexically" as before.
	EnumerationQNames []xdm.QName

	// Assertions are the XSD 1.1 <xs:assertion> facets. On a simple type an
	// assertion is a facet rather than a component, though it compiles the
	// same way.
	Assertions []*Assertion

	// ExplicitTimezone is the XSD 1.1 facet constraining whether a date or
	// time value carries a timezone.
	ExplicitTimezone *Timezone

	// Fixed records which facets this step wrote fixed="true" on. Part 2
	// §4.3 gives every constraining facet but pattern, enumeration and
	// assertion a {fixed} property, and "Facet Valid (Restriction)" then
	// forbids a derived type from stating a *different* value for one the
	// base fixed.
	//
	// whiteSpace keeps its own flag rather than joining this map: the
	// built-in types fix it, and they are constructed directly rather than
	// parsed, so the boolean predates any schema document.
	Fixed map[FacetKind]bool
}

// isFixed reports whether the step wrote fixed="true" on a facet.
func (f *FacetSet) isFixed(kind FacetKind) bool {
	return f != nil && f.Fixed[kind]
}

// setFixed records fixed="true" for a facet, allocating on first use so that
// the common unfixed case costs nothing.
func (f *FacetSet) setFixed(kind FacetKind) {
	if f.Fixed == nil {
		f.Fixed = make(map[FacetKind]bool)
	}
	f.Fixed[kind] = true
}

// IsEmpty reports whether the set constrains nothing.
//
// XPath 3.1 2.5 needs it: a *pure* union type is one whose {facets} property
// is empty, and only a pure union may be used as an item type. A union
// carrying facets is excluded because substituting a member for the union is
// unsafe there — the member is not constrained by the facets the union adds —
// which is the XSD 1.0 error XSD 1.1 corrected.
func (f *FacetSet) IsEmpty() bool {
	if f == nil {
		return true
	}
	return f.Length == nil && f.MinLength == nil && f.MaxLength == nil &&
		f.TotalDigits == nil && f.FractionDigits == nil &&
		f.WhiteSpace == nil &&
		f.MinInclusive == nil && f.MaxInclusive == nil &&
		f.MinExclusive == nil && f.MaxExclusive == nil &&
		len(f.Patterns) == 0 && !f.HasEnumerations &&
		len(f.Assertions) == 0 && f.ExplicitTimezone == nil
}

// Timezone is the value of the XSD 1.1 explicitTimezone facet.
type Timezone uint8

// The explicitTimezone values.
const (
	// TimezoneOptional permits a value with or without a timezone, which
	// is the default for every date and time type.
	TimezoneOptional Timezone = iota
	// TimezoneRequired demands one.
	TimezoneRequired
	// TimezoneProhibited forbids one.
	TimezoneProhibited
)

// String names the value as it is spelled in a schema document.
func (t Timezone) String() string {
	switch t {
	case TimezoneRequired:
		return "required"
	case TimezoneProhibited:
		return "prohibited"
	}
	return "optional"
}

// applicable records which facets each primitive type admits (Part 2 §4.1.5,
// and the per-type "Constraining facets" lists).
//
// The table is explicit rather than derived from a rule because the spec's
// assignment is not systematic: xs:boolean admits pattern and whiteSpace but
// *not* enumeration, and the digit facets belong to the decimal branch alone.
var applicable = map[string]map[FacetKind]bool{
	"string":       lengthFacets(),
	"hexBinary":    lengthFacets(),
	"base64Binary": lengthFacets(),
	"anyURI":       lengthFacets(),
	"QName":        lengthFacets(),
	"NOTATION":     lengthFacets(),

	// xs:boolean has no enumeration facet: with two values an enumeration
	// could only restate the type or make it a constant, and the spec
	// declines to allow either.
	"boolean": {FacetPattern: true, FacetWhiteSpace: true},

	"decimal": mergeFacets(orderedFacets(), map[FacetKind]bool{
		FacetTotalDigits: true, FacetFractionDigits: true,
	}),

	"float":    orderedFacets(),
	"double":   orderedFacets(),
	"duration": orderedFacets(),

	// The eight primitives whose lexical space has an optional timezone
	// are the eight that admit explicitTimezone (Part 2 §4.3.13).
	// xs:duration is deliberately not among them, and neither is any
	// numeric type: zone007 puts the facet on a restriction of xs:string,
	// which is what the table is here to refuse.
	"dateTime":   timezonedFacets(),
	"time":       timezonedFacets(),
	"date":       timezonedFacets(),
	"gYearMonth": timezonedFacets(),
	"gYear":      timezonedFacets(),
	"gMonthDay":  timezonedFacets(),
	"gDay":       timezonedFacets(),
	"gMonth":     timezonedFacets(),
}

// timezonedFacets is orderedFacets plus explicitTimezone, for the date and
// time primitives whose lexical space carries an optional timezone.
func timezonedFacets() map[FacetKind]bool {
	return mergeFacets(orderedFacets(), map[FacetKind]bool{
		FacetExplicitTimezone: true,
	})
}

// lengthFacets is the facet set of the string-like primitives.
func lengthFacets() map[FacetKind]bool {
	return map[FacetKind]bool{
		FacetLength: true, FacetMinLength: true, FacetMaxLength: true,
		FacetPattern: true, FacetEnumeration: true, FacetWhiteSpace: true,
	}
}

// orderedFacets is the facet set of the primitives that have an order but no
// length: the numeric and date/time families.
func orderedFacets() map[FacetKind]bool {
	return map[FacetKind]bool{
		FacetPattern: true, FacetEnumeration: true, FacetWhiteSpace: true,
		FacetMaxInclusive: true, FacetMaxExclusive: true,
		FacetMinInclusive: true, FacetMinExclusive: true,
	}
}

func mergeFacets(a, b map[FacetKind]bool) map[FacetKind]bool {
	out := make(map[FacetKind]bool, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// primitiveOf returns a simple type's primitive, walking the base chain when
// the field was not filled in.
//
// The field is set when a type is built from a base that already knew its own
// primitive, but a redefinition is read while its base is still being resolved,
// so the copy can be nil even though the chain leads to a primitive. Deriving
// it on demand is what keeps a redefined type's length and bound facets
// working — without it they were silently skipped, because the code that
// applies them dispatches on the primitive's name.
func primitiveOf(t *SimpleType) *SimpleType {
	seen := map[*SimpleType]bool{}
	for cur := t; cur != nil && !seen[cur]; {
		seen[cur] = true
		if cur.Primitive != nil {
			return cur.Primitive
		}
		base, ok := cur.Base.(*SimpleType)
		if !ok || base == cur {
			return nil
		}
		cur = base
	}
	return nil
}

// FacetApplicable reports whether a facet may be applied to a simple type.
//
// Dispatch is on {variety} first, and the union case is the one that catches
// implementations out: a union admits only pattern and enumeration whatever its
// members admit. In particular it has no whiteSpace facet, because
// normalisation belongs to whichever member type validates the value.
func FacetApplicable(t *SimpleType, f FacetKind) bool {
	switch t.Variety {
	case VarietyList:
		switch f {
		case FacetLength, FacetMinLength, FacetMaxLength,
			FacetPattern, FacetEnumeration, FacetWhiteSpace:
			return true
		}
		return false

	case VarietyUnion:
		return f == FacetPattern || f == FacetEnumeration
	}

	prim := primitiveOf(t)
	if prim == nil {
		// An atomic type with no primitive is xs:anySimpleType, which
		// constrains nothing.
		return false
	}
	set, ok := applicable[prim.Name.Local]
	if !ok {
		return false
	}
	return set[f]
}

// EffectiveWhiteSpace returns the whiteSpace value in force for a type, walking
// up the base chain until a step sets one.
//
// The walk terminates on xs:anySimpleType rather than on nil, and must also
// guard against xs:anyType, whose base type definition is *itself* — a
// deliberate self-loop in the spec that turns a naive walk into an infinite
// one.
func EffectiveWhiteSpace(t *SimpleType) WhiteSpace {
	for cur := t; cur != nil; {
		if cur.Facets != nil && cur.Facets.WhiteSpace != nil {
			return *cur.Facets.WhiteSpace
		}
		base, ok := cur.Base.(*SimpleType)
		if !ok || base == cur {
			break
		}
		cur = base
	}
	// xs:anySimpleType preserves; every built-in that differs sets the
	// facet explicitly.
	return WhitePreserve
}

// facetStep is one derivation step's facets together with the type that carried
// them, so that diagnostics can name the type a constraint came from.
type facetStep struct {
	typ    *SimpleType
	facets *FacetSet
}

// facetChain returns the derivation steps from t up to its primitive, nearest
// first.
//
// Collecting the chain rather than merging the facets is what lets the
// evaluator apply patterns conjunctively across steps while treating the
// patterns within one step as alternatives.
func facetChain(t *SimpleType) []facetStep {
	var out []facetStep
	seen := make(map[*SimpleType]bool)
	for cur := t; cur != nil && !seen[cur]; {
		seen[cur] = true
		if cur.Facets != nil {
			out = append(out, facetStep{typ: cur, facets: cur.Facets})
		}
		base, ok := cur.Base.(*SimpleType)
		if !ok || base == cur {
			break
		}
		cur = base
	}
	return out
}

// checkLengthFacets applies length, minLength and maxLength.
//
// What "length" counts depends on the variety and the primitive: characters for
// a string, octets for the binary types, and *items* for a list. Measuring a
// list in characters is a classic wrong answer, so the unit is chosen by the
// caller and passed in.
func checkLengthFacets(steps []facetStep, n uint64, unit string) error {
	for _, st := range steps {
		f := st.facets
		if f.Length != nil && n != *f.Length {
			return facetError(st.typ, FacetLength,
				"%d %s, want exactly %d", n, unit, *f.Length)
		}
		if f.MinLength != nil && n < *f.MinLength {
			return facetError(st.typ, FacetMinLength,
				"%d %s, want at least %d", n, unit, *f.MinLength)
		}
		if f.MaxLength != nil && n > *f.MaxLength {
			return facetError(st.typ, FacetMaxLength,
				"%d %s, want at most %d", n, unit, *f.MaxLength)
		}
	}
	return nil
}

// checkPatterns applies the pattern facets of every derivation step.
//
// Within a step the patterns are alternatives; across steps they are
// conjunctive. The spec expresses this by saying each step's patterns form one
// regular expression by union, and that a value must satisfy the pattern facet
// of every type in the derivation chain.
func checkPatterns(steps []facetStep, lexical string) error {
	for _, st := range steps {
		if len(st.facets.Patterns) == 0 {
			continue
		}
		ok := false
		for _, p := range st.facets.Patterns {
			if p.Matches(lexical) {
				ok = true
				break
			}
		}
		if !ok {
			return facetError(st.typ, FacetPattern,
				"%q does not match %s", lexical, describePatterns(st.facets.Patterns))
		}
	}
	return nil
}

func describePatterns(ps []*Pattern) string {
	if len(ps) == 1 {
		return fmt.Sprintf("pattern %q", ps[0].Source)
	}
	var b strings.Builder
	b.WriteString("any of ")
	for i, p := range ps {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", p.Source)
	}
	return b.String()
}

// checkDigitFacets applies totalDigits and fractionDigits to a decimal value.
//
// The counts are of the *value*, not the lexical form: "1.50" has two total
// digits and one fraction digit, because trailing zeros in the fraction are not
// significant. Counting the literal would reject values the spec accepts.
func checkDigitFacets(steps []facetStep, v *big.Rat) error {
	var total, frac uint64
	counted := false
	for _, st := range steps {
		f := st.facets
		if f.TotalDigits == nil && f.FractionDigits == nil {
			continue
		}
		if !counted {
			var ok bool
			total, frac, ok = countDigits(v)
			if !ok {
				// A value with no finite decimal expansion has
				// no digit count to compare. isDecimalLexical
				// admits only sign, digits and one point, so
				// nothing that reaches here can be such a
				// value; refusing it is better than inventing
				// a count that would let it pass.
				return facetError(st.typ, FacetTotalDigits,
					"value has no terminating decimal expansion")
			}
			counted = true
		}
		if f.TotalDigits != nil && total > *f.TotalDigits {
			return facetError(st.typ, FacetTotalDigits,
				"%d digits, want at most %d", total, *f.TotalDigits)
		}
		if f.FractionDigits != nil && frac > *f.FractionDigits {
			return facetError(st.typ, FacetFractionDigits,
				"%d fraction digits, want at most %d", frac, *f.FractionDigits)
		}
	}
	return nil
}

// decimalMagnitude is a terminating decimal split into an integer coefficient
// and a decimal scale: the value is coefficient / 10^scale.
type decimalMagnitude struct {
	coefficient *big.Int // absolute value; never negative
	scale       uint64   // number of fraction digits, exactly
}

// decimalMagnitudeOf converts a rational to its exact decimal form, reporting
// false if the value has no terminating decimal expansion.
//
// big.Rat holds lowest terms, so 1.5 is 3/2 rather than 15/10. A value is an
// exact decimal precisely when its denominator is 2^a * 5^b with nothing left
// over, and then the scale is max(a, b) — no more digits are needed and no
// fewer will do. Factoring the denominator is O(scale) cheap integer
// divisions; the earlier code instead multiplied the whole rational by ten
// once per digit and stopped at a fixed bound, which turned a value with more
// fraction digits than the bound into a *smaller* count and so let it pass a
// fractionDigits facet it violates.
func decimalMagnitudeOf(r *big.Rat) (decimalMagnitude, bool) {
	d := r.Denom()
	// The power of two is the number of trailing zero bits, which big.Int
	// already knows; dividing them out one at a time would cost a full
	// division per digit.
	a := uint64(d.TrailingZeroBits())
	rest := new(big.Int).Rsh(d, uint(a))

	// Strip powers of five in halving chunks rather than singly. Dividing
	// by 5^k removes k factors for one division, so the exponent is found
	// in a logarithmic number of divisions on a shrinking number instead of
	// one division per digit on the full-width one.
	var b uint64
	five := big.NewInt(5)
	q, m := new(big.Int), new(big.Int)
	for k := uint64(1); ; {
		p := new(big.Int).Exp(five, new(big.Int).SetUint64(k), nil)
		if p.BitLen() > rest.BitLen() {
			if k == 1 {
				break
			}
			k = 1
			continue
		}
		q.QuoRem(rest, p, m)
		if m.Sign() != 0 {
			if k == 1 {
				break
			}
			k /= 2
			continue
		}
		rest.Set(q)
		b += k
		k *= 2
	}

	if rest.Cmp(big.NewInt(1)) != 0 {
		// A prime other than 2 or 5 survives: 1/3 and its kind have no
		// finite decimal expansion, so no digit count describes them.
		return decimalMagnitude{}, false
	}
	scale := a
	if b > scale {
		scale = b
	}
	// numerator * 2^(scale-a) * 5^(scale-b) is the value written with
	// exactly `scale` fraction digits, an exact integer by construction.
	coef := new(big.Int).Abs(r.Num())
	if k := scale - a; k > 0 {
		coef.Lsh(coef, uint(k))
	}
	if k := scale - b; k > 0 {
		coef.Mul(coef, new(big.Int).Exp(five, new(big.Int).SetUint64(k), nil))
	}
	return decimalMagnitude{coefficient: coef, scale: scale}, true
}

// countDigits returns the total and fraction digit counts of a decimal value.
//
// The counts are of the value, not of the literal: 1.50 and 1.5 are the same
// value and both have two total digits and one fraction digit.
//
// The second result is false for a value with no terminating decimal
// expansion, for which neither count is defined. Every caller reaches here
// only past isDecimalLexical, whose grammar is sign, digits and at most one
// point — so the denominator is a power of ten and this cannot happen — but a
// count must never be invented for a value that does not have one.
func countDigits(v *big.Rat) (total, frac uint64, ok bool) {
	if v.Sign() == 0 {
		// Zero has one total digit and no fraction digits.
		return 1, 0, true
	}
	m, ok := decimalMagnitudeOf(v)
	if !ok {
		return 0, 0, false
	}
	digits := uint64(len(m.coefficient.String()))
	// A value such as 0.001 scales to 1, one digit, but the spec counts
	// three: the leading zeros of the fraction are significant to
	// totalDigits even though they are not to the value. The coefficient
	// can only be shorter than the scale when the integer part is zero,
	// and then the fraction digits are all the digits there are.
	if digits < m.scale {
		digits = m.scale
	}
	return digits, m.scale, true
}

// facetError builds the diagnostic for a failed facet, naming the type that
// imposed it.
//
// The type is named because a value can fail a facet several levels up its
// derivation chain, and "does not satisfy maxLength" without saying whose
// maxLength sends the reader hunting through the schema.
func facetError(t *SimpleType, f FacetKind, format string, args ...any) error {
	where := "an anonymous type"
	if t != nil && t.Name.Local != "" {
		where = t.Name.Local
	}
	return fmt.Errorf("%s facet of %s: %s", f, where, fmt.Sprintf(format, args...))
}
