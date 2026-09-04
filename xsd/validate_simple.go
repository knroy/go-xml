package xsd

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// validateSimpleContent checks a lexical value against a simple type.
//
// The order is fixed by Datatype Valid (Part 2 §4.1.4) and is not the order the
// facets are written in: whiteSpace normalisation first, then the pattern facet
// against the normalised *lexical* form, then the conversion to a value, then
// every other facet against the *value*. Checking a bound before normalising,
// or a pattern after conversion, gives different answers on real schemas.
func (v *validator) validateSimpleContent(n *xdm.Node, lexical string, t *SimpleType, decl *ElementDecl) {
	if t == nil {
		return
	}
	normalized, err := validateSimpleValueIn(lexical, t, v.schema.Version, n)
	if err != nil {
		v.fail(n, "cvc-datatype-valid.1", "%v", err)
		return
	}

	// A fixed value constraint requires the instance to carry that value.
	// The comparison is on the normalised form, since that is what the
	// spec's [schema normalized value] holds.
	if decl != nil && decl.Constraint != nil && decl.Constraint.Fixed {
		// The schema's version — see validateAttribute on why the 1.0
		// default silently disarms this check on a 1.1 lexical form.
		want, err := validateSimpleValueVersion(decl.Constraint.Lexical, t,
			v.schema.Version)
		if err == nil && !fixedValueEqual(want, normalized, t) {
			v.fail(n, "cvc-elt.5.2.2.2.1",
				"value %q does not equal the fixed value %q",
				normalized, want)
		}
	}

	v.recordIDs(n, normalized, t)
	v.recordKeyValue(n, normalized, t)
}

// recordKeyValue notes a node's schema-normalized value and primitive, for the
// benefit of identity constraints.
//
// A key sequence compares values, not spellings: 24:00:00Z and 00:00:00Z are
// the same time and PT29H and P1DT5H the same duration, so a keyref written
// with one form must match a key written with the other. Deciding that needs
// the type, which is known here and gone by the time the constraint runs — the
// constraint sees only nodes, and type annotations are opt-in.
//
// The primitive is recorded for every type, not only the temporal ones,
// because values of different primitives are never equal however their
// spellings compare. idF012 puts it directly: one member is the boolean 1 and
// the other the decimal 1, and the constraint is satisfied — comparing the
// lexical forms alone made them a duplicate.
func (v *validator) recordKeyValue(n *xdm.Node, normalized string, t *SimpleType) {
	// A list takes its item type's primitive, not one of its own. A
	// singleton list is equal to the atomic value it contains — saxonData's
	// id022 matches a keyref typed as a list of xs:Name against a key typed
	// xs:Name — so the two have to reach the same key, which means the same
	// primitive on both sides.
	//
	// A union takes the primitive of the member that validated, which is
	// what primitiveOf already reports.
	item := t
	if item != nil && item.Variety == VarietyList && item.ItemType != nil {
		item = item.ItemType
	}
	prim := ""
	if p := primitiveOf(item); p != nil {
		prim = p.Name.Local
	}
	if prim == "" {
		return
	}
	if v.keyValues == nil {
		v.keyValues = map[*xdm.Node]keyValue{}
	}
	v.keyValues[n] = keyValue{normalized: normalized, primitive: prim}
}

// validateSimpleValue checks a lexical form against a simple type and returns
// the normalised value.
func validateSimpleValue(lexical string, t *SimpleType) (string, error) {
	return validateSimpleValueVersion(lexical, t, Version10)
}

// validateSimpleValueVersion is validateSimpleValue with the schema version,
// which a few lexical spaces depend on.
//
// Threading it as a parameter rather than storing it on the type is not an
// aesthetic choice: the built-in types are a process-wide singleton behind a
// sync.Once, so two schemas of different versions share the same *SimpleType
// and a version stored there would be whichever schema loaded last.
func validateSimpleValueVersion(lexical string, t *SimpleType, version Version) (string, error) {
	return validateSimpleValueIn(lexical, t, version, nil)
}

// validateSimpleValueIn is validateSimpleValueVersion with the instance node
// the lexical form came from, when there is one.
//
// The node is needed by exactly one check: an enumeration facet on a type whose
// value space is QNames compares expanded names, and expanding the instance's
// spelling takes the namespaces in scope where it was written. Everything else
// ignores it, which is why it is threaded as an extra parameter rather than
// made part of the type or the version.
func validateSimpleValueIn(lexical string, t *SimpleType, version Version, at *xdm.Node) (string, error) {
	// A definition naming a type that does not exist loaded anyway, because
	// the spec makes that an error only where the type is used. This is
	// where it is used, so it is an error now — and checking here also
	// keeps a half-built list or union from being walked, whose ItemType or
	// member slot is nil.
	if t.unresolved != "" {
		return "", fmt.Errorf(
			"src-resolve: type refers to %q, which no definition matches",
			t.unresolved)
	}
	switch t.Variety {
	case VarietyList:
		return validateListValueIn(lexical, t, at)
	case VarietyUnion:
		return validateUnionValueIn(lexical, t, at)
	}
	// Part 2 §3.2.18: an xs:QName value is a (namespace name, local name)
	// pair, and the namespace name is the one the prefix is bound to where
	// the value was written. A prefix with no binding therefore denotes no
	// value at all. That is a different failure from a bad lexical form and
	// is not reachable from the lexical check, which sees only that "a:one"
	// is shaped like a QName — true whichever prefixes happen to be in
	// scope. It needs the node, so it is checked here, where the node is
	// still in hand, rather than among the facets.
	if !qnamePrefixBound(at, lexical, t) {
		return "", &ParseError{
			Code: "cvc-datatype-valid.1.2.1",
			Message: "\"" + truncate(strings.TrimSpace(lexical)) +
				"\" uses a prefix with no in-scope namespace declaration",
		}
	}
	return validateAtomicValueBoundsIn(lexical, t, version, true, at)
}

// valueSpaceOnly reports whether a lexical form denotes a value in a type's
// value space, without asking whether the type's bounding facets admit it.
// See validateAtomicValueBounds for why the distinction matters.
func valueSpaceOnly(lexical string, t *SimpleType, version Version) (string, error) {
	if t.unresolved != "" {
		return "", fmt.Errorf(
			"src-resolve: type refers to %q, which no definition matches",
			t.unresolved)
	}
	switch t.Variety {
	case VarietyList:
		return validateListValue(lexical, t)
	case VarietyUnion:
		return validateUnionValue(lexical, t)
	}
	return validateAtomicValueBounds(lexical, t, version, false)
}

// validateAtomicValue checks an atomic value.
func validateAtomicValue(lexical string, t *SimpleType) (string, error) {
	return validateAtomicValueVersion(lexical, t, Version10)
}

func validateAtomicValueVersion(lexical string, t *SimpleType, version Version) (string, error) {
	return validateAtomicValueBounds(lexical, t, version, true)
}

// validateAtomicValueBounds is validateAtomicValueVersion with a say over
// whether the base's bounding facets apply.
//
// They always do for a value in a document. They do not when the "value" is a
// bounding facet being declared: "maxExclusive valid restriction" asks only
// whether it lies in the base's *value space*, and the relation it must hold to
// the base's own bounds is a separate clause with its own comparison — greater
// than, not greater than or equal. Applying the base's bounds here instead made
// re-stating a bound with the same value contradict itself, which the spec
// allows outright (d3_4_28v09).
func validateAtomicValueBounds(lexical string, t *SimpleType, version Version, bounds bool) (string, error) {
	return validateAtomicValueBoundsIn(lexical, t, version, bounds, nil)
}

func validateAtomicValueBoundsIn(lexical string, t *SimpleType, version Version, bounds bool, at *xdm.Node) (string, error) {
	ws := EffectiveWhiteSpace(t)
	normalized := ws.Normalize(lexical)

	steps := facetChain(t)
	if err := checkPatterns(steps, normalized); err != nil {
		return "", err
	}

	// The lexical form must belong to the primitive's lexical space. This
	// is where a value such as "abc" fails for xs:int, before any bound is
	// consulted.
	prim := ""
	if p := primitiveOf(t); p != nil {
		prim = p.Name.Local
	}
	if err := checkLexicalSpaceVersion(normalized, prim, version); err != nil {
		return "", err
	}
	// The integer branch narrows the *lexical* space, not only the value
	// space: xs:integer has no decimal point at all, so "+0.0" is not an
	// integer literal even though it denotes zero and has no fraction
	// digits. fractionDigits="0" alone counts the digits after the point
	// and finds none, which is why the facet does not catch it
	// (integer006).
	if err := checkIntegerLexical(normalized, t); err != nil {
		return "", err
	}
	// The string branch's derived types narrow the lexical space rather
	// than the value space, and they do it with patterns the spec states
	// in prose. xs:ID is an xs:NCName, so "87123_" is not one — it starts
	// with a digit — and nothing in the facets would have said so, since
	// these types carry none.
	if err := checkStringSubtype(normalized, t); err != nil {
		return "", err
	}

	if err := checkEnumerationIn(steps, normalized, t, at); err != nil {
		return "", err
	}
	if err := checkLengthForPrimitive(steps, normalized, prim); err != nil {
		return "", err
	}
	if bounds {
		if err := checkBounds(steps, normalized, prim); err != nil {
			return "", err
		}
	}
	if prim == "decimal" {
		if r, ok := new(big.Rat).SetString(normalized); ok {
			if err := checkDigitFacets(steps, r); err != nil {
				return "", err
			}
		}
	}
	if err := checkExplicitTimezone(steps, normalized, prim); err != nil {
		return "", err
	}
	if err := checkSimpleAssertions(steps, normalized, t); err != nil {
		return "", err
	}
	return normalized, nil
}

// checkExplicitTimezone applies the XSD 1.1 facet that says whether a date or
// time value must carry a timezone.
//
// It is what distinguishes xs:dateTimeStamp — an instant — from xs:dateTime,
// which may be a wall-clock reading with no way to place it on a timeline.
func checkExplicitTimezone(steps []facetStep, normalized, prim string) error {
	switch prim {
	case "dateTime", "time", "date", "gYearMonth", "gYear",
		"gMonthDay", "gDay", "gMonth":
	default:
		return nil
	}
	has := hasTimezone(normalized)
	for _, st := range steps {
		tz := st.facets.ExplicitTimezone
		if tz == nil {
			continue
		}
		switch *tz {
		case TimezoneRequired:
			if !has {
				return facetError(st.typ, FacetExplicitTimezone,
					"%s has no timezone, which this type requires", normalized)
			}
		case TimezoneProhibited:
			if has {
				return facetError(st.typ, FacetExplicitTimezone,
					"%s carries a timezone, which this type forbids", normalized)
			}
		}
		// The nearest facet wins; a base cannot re-tighten it.
		return nil
	}
	return nil
}

// hasTimezone reports whether a date or time literal ends in a timezone.
func hasTimezone(v string) bool {
	if strings.HasSuffix(v, "Z") {
		return true
	}
	if len(v) >= 6 {
		tz := v[len(v)-6:]
		if (tz[0] == '+' || tz[0] == '-') && tz[3] == ':' &&
			isDigits(tz[1:3], 2) && isDigits(tz[4:], 2) {
			return true
		}
	}
	return false
}

// validateListValue checks a whitespace-separated list.
//
// The length facets count *items*, not characters, and a pattern on a list
// matches the whole literal rather than each item — erratum E2-30, which is the
// opposite of what the per-item reading would suggest.
func validateListValue(lexical string, t *SimpleType) (string, error) {
	return validateListValueIn(lexical, t, nil)
}

func validateListValueIn(lexical string, t *SimpleType, at *xdm.Node) (string, error) {
	normalized := WhiteCollapse.Normalize(lexical)
	steps := facetChain(t)

	if err := checkPatterns(steps, normalized); err != nil {
		return "", err
	}

	var items []string
	if normalized != "" {
		items = splitFields(normalized)
	}
	if err := checkLengthFacets(steps, uint64(len(items)), "items"); err != nil {
		return "", err
	}
	if err := checkEnumerationIn(steps, normalized, t, at); err != nil {
		return "", err
	}

	if t.ItemType != nil {
		for _, item := range items {
			if _, err := validateSimpleValueIn(item, t.ItemType, Version10, at); err != nil {
				return "", fmt.Errorf("list item %q: %w", item, err)
			}
		}
	}

	// XSD 1.1 permits xs:assertion on a list, where $value is the sequence
	// of items — "count($value) eq count(distinct-values($value))" is how a
	// schema says a list has no duplicates. Running assertions only for the
	// atomic variety skipped every one of them.
	if err := checkSimpleAssertions(steps, normalized, t); err != nil {
		return "", err
	}
	return normalized, nil
}

// validateUnionValue checks a value against a union's members.
//
// The first member whose lexical space accepts the value wins — not the best or
// the longest match. Each member is tried under *its own* whiteSpace, because a
// union has no whiteSpace facet of its own: normalisation belongs to the member
// that validates. Normalising once up front would make " 42 " fail against
// union(xs:int, xs:string) or succeed as the wrong member.
func validateUnionValue(lexical string, t *SimpleType) (string, error) {
	return validateUnionValueIn(lexical, t, nil)
}

func validateUnionValueIn(lexical string, t *SimpleType, at *xdm.Node) (string, error) {
	steps := facetChain(t)

	// A restriction of a union carries no member list of its own; the members
	// it validates against are the ones it inherits. Reading only t.MemberTypes
	// made every such restriction admit nothing at all.
	members := t.MemberTypes
	if len(members) == 0 {
		members = unionMemberTypesOf(t)
	}

	for _, m := range members {
		if m == nil {
			continue
		}
		// The node is passed down: the QName and enumeration checks below the
		// member need it. A member that is itself a union will record its own
		// winner on the way through, but the outer winner is written *after*
		// this call returns and so overwrites it, which is the right order —
		// the outermost union is the one the node's annotation names.
		normalized, err := validateSimpleValueIn(lexical, m, Version10, at)
		if err != nil {
			continue
		}
		// A union admits only pattern and enumeration, and they apply
		// to the value as the member normalised it.
		if err := checkPatterns(steps, normalized); err != nil {
			continue
		}
		if err := checkEnumerationIn(steps, normalized, t, at); err != nil {
			continue
		}
		// An assertion on a union is not a member-selection criterion:
		// the member has already been chosen, and the assertion then
		// either holds for the value or the value is invalid. Treating
		// a failed assertion as "try the next member" would let a value
		// slip through as a member the schema did not intend.
		if err := checkSimpleAssertions(steps, normalized, t); err != nil {
			return "", err
		}
		// The winning member is recorded on the node, because which member
		// accepted the value is the one fact about a union that neither the
		// type nor the lexical form carries and that atomisation cannot
		// recover: XSD 1.0 §3.14.4 selects the member per *value*, so "100"
		// and "123-AB" under the same union are an xs:integer and a
		// my:partNumberType respectively. Discarding it — which is what this
		// function did — left the data model with a union whose derivation
		// chain runs to xs:anySimpleType and stops, so the node atomised to
		// xs:untypedAtomic.
		//
		// The node keeps its own annotation: the union's identity is still
		// true of the value and a large family of tests asks for it. Only the
		// second, per-value fact is added here.
		if at != nil {
			if mn := annotationName(m); mn != "" {
				at.UnionMember = mn
			}
		}
		return normalized, nil
	}
	return "", fmt.Errorf("value %q does not match any member type of the union",
		truncate(lexical))
}

// checkEnumeration applies the enumeration facet.
//
// The comparison is on the value space, not the lexical space: for a numeric
// type "1.0" and "1" are the same value and an enumeration of one admits the
// other. Comparing lexical forms would reject documents the spec accepts.
func checkEnumeration(steps []facetStep, normalized string, t *SimpleType) error {
	return checkEnumerationIn(steps, normalized, t, nil)
}

// checkEnumerationIn is checkEnumeration with the instance node the value was
// written on, which xs:QName and xs:NOTATION need and no other type does.
func checkEnumerationIn(steps []facetStep, normalized string, t *SimpleType, at *xdm.Node) error {
	prim := ""
	if p := primitiveOf(t); p != nil {
		prim = p.Name.Local
	}
	numeric := prim == "decimal" || prim == "float" || prim == "double"

	// xs:QName and xs:NOTATION have QNames, not strings, for their value
	// space, so two spellings denote the same value whenever their prefixes
	// bind the same URI. The instance's spelling is expanded against the
	// namespaces in scope where it was written; the facet's was expanded at
	// schema-load time against the schema document's. Without both
	// expansions "one:mp3" was refused by an enumeration of "smokey:mp3"
	// even though the two prefixes name one namespace.
	var want xdm.QName
	haveQName := false
	if prim == "QName" || prim == "NOTATION" {
		if at != nil {
			want, haveQName = resolveInstanceQName(at, normalized)
		} else if q, ok := ParseExpandedName(normalized); ok {
			// No node, so no in-scope namespaces to expand a prefix against.
			// A caller that has already resolved the prefix — the XPath
			// constructor of a NOTATION-derived type does it while the static
			// context still exists — hands the expanded name over in Clark
			// notation instead, and that is comparable with the enumeration
			// QNames the schema expanded at load time. Without this the
			// comparison fell back to matching prefixes literally, so
			// one:mp3 was refused by an enumeration written smokey:mp3.
			want, haveQName = q, true
		}
	}

	for _, st := range steps {
		if !st.facets.HasEnumerations {
			continue
		}
		ok := false
		for i, e := range st.facets.Enumerations {
			if haveQName && i < len(st.facets.EnumerationQNames) {
				cand := st.facets.EnumerationQNames[i]
				if cand.Local != "" && cand == want {
					ok = true
					break
				}
			}
			cand := EffectiveWhiteSpace(st.typ).Normalize(e)
			if cand == normalized {
				ok = true
				break
			}
			if numeric && numericEqual(cand, normalized) {
				ok = true
				break
			}
			if valueEqual(cand, normalized, prim) {
				ok = true
				break
			}
		}
		if !ok {
			return facetError(st.typ, FacetEnumeration,
				"%q is not one of the permitted values", truncate(normalized))
		}
	}
	return nil
}

// fixedValueEqual reports whether an instance value satisfies a fixed value
// constraint.
//
// The spec compares *values*, not spellings, and several primitives have more
// than one literal per value: fixed="1" on a union admitting xs:boolean is
// satisfied by "true" (stE050), and fixed="1.0" by "1.000" (stE055). Comparing
// the normalized lexical forms refused both.
//
// The canonical form is the one identity constraints already use, so a fixed
// value and a key agree on what equality means.
func fixedValueEqual(want, got string, t *SimpleType) bool {
	if want == got {
		return true
	}
	prim := ""
	if p := primitiveOf(atomicBaseOf(t)); p != nil {
		prim = p.Name.Local
	}
	if a, ok := canonicalValue(want, prim); ok {
		if b, ok2 := canonicalValue(got, prim); ok2 {
			return a == b
		}
	}
	if c, ok := canonicalTemporal(want, prim); ok {
		if d, ok2 := canonicalTemporal(got, prim); ok2 {
			return c == d
		}
	}
	// A list's value is a *sequence* of item values, so two lists are equal
	// when they have the same length and agree item by item. Whitespace
	// between items is a separator, not part of any value, which is why
	// "1 2 3" and "1  2   3" denote the same xs:int list.
	//
	// Falling through to the atomic path instead asked whether the whole
	// literal "1  2   3" canonicalises as a single int; it does not, so
	// every list comparison but the byte-identical one came back false.
	// addB183 is the case the spec names outright: fixed values are
	// compared as values, not as strings.
	if t != nil && t.Variety == VarietyList && t.ItemType != nil {
		wf, gf := splitFields(want), splitFields(got)
		if len(wf) != len(gf) {
			return false
		}
		for i := range wf {
			if !fixedValueEqual(wf[i], gf[i], t.ItemType) {
				return false
			}
		}
		return true
	}
	// A union takes the primitive of whichever member validated, and
	// §3.14.4 selects that member per *value*: the first member whose
	// lexical space the literal belongs to. Each side is therefore
	// canonicalised under its OWN winning member, not under every member
	// until one pair happens to agree.
	//
	// Trying every member is what was here before, and it made a union
	// equate values of different primitives. stE054 is the case: the union
	// is (boolean, int, double), the fixed value "1.0" and the instance
	// "1". The instance is a boolean — boolean is first and "1" is in its
	// lexical space — and the fixed value is a double; true is not 1.0d,
	// and the instance is invalid. Scanning members found "double" for
	// both and called them equal.
	//
	// fixed="1" against "true" (stE050) still holds, because both pick the
	// same member: boolean accepts each, and canonicalValue maps both to
	// boolean/true.
	if t != nil && t.Variety == VarietyUnion {
		mw := unionMemberFor(want, t)
		mg := unionMemberFor(got, t)
		if mw == nil || mg == nil || mw != mg {
			return false
		}
		return fixedValueEqual(want, got, mw)
	}
	return false
}

// valueConstraintType returns the simple type a declaration's value constraint
// is written in, or nil when there is none to compare against.
//
// A value constraint is only ever a simple value, so the type that governs it
// is the declaration's own simple type, or a complex type's {simple content}.
// Mixed and element-only content carry no simple type, and a comparison there
// has nothing to canonicalise with, so it falls back to the lexical form.
func valueConstraintType(t Type) *SimpleType {
	switch t := t.(type) {
	case *SimpleType:
		return t
	case *ComplexType:
		if t.Content == ContentSimple {
			return t.SimpleContent
		}
	}
	return nil
}

// fixedConstraintsAgree reports whether a restriction preserves a fixed value
// its base imposes.
//
// §3.4.6 derivation-ok-restriction 2.1.3 and 3.2.2 both say "with the same
// *value*", not the same spelling. Two literals that denote one value —
// "1 2 3" and "1  2   3" for a list of int, " akfhaf afkhaf  " and
// "akfhaf afkhaf" for a token, "  -1" and "-1" for an int — satisfy the
// clause, and comparing the strings rejected all three (addB183).
func fixedConstraintsAgree(base, restriction *ValueConstraint, t Type) bool {
	if base == nil || !base.Fixed {
		return true
	}
	if restriction == nil || !restriction.Fixed {
		return false
	}
	if restriction.Lexical == base.Lexical {
		return true
	}
	st := valueConstraintType(t)
	if st == nil {
		return false
	}
	// The literals are compared after the type's own whitespace
	// normalisation, which is what turns a fixed xs:token written with
	// leading spaces into the value it denotes.
	ws := EffectiveWhiteSpace(st)
	return fixedValueEqual(ws.Normalize(base.Lexical),
		ws.Normalize(restriction.Lexical), st)
}

// unionMemberFor returns the member type of a union that validates a value.
//
// §3.14.4 makes this "the first member type in {member type definitions} whose
// lexical space contains the literal", which is the same order and the same
// test validateUnionValueIn walks. Only the member is wanted here, so the
// facets the union itself imposes are left to the validation that already ran.
func unionMemberFor(lexical string, t *SimpleType) *SimpleType {
	if t == nil {
		return nil
	}
	members := t.MemberTypes
	if len(members) == 0 {
		members = unionMemberTypesOf(t)
	}
	for _, m := range members {
		if m == nil {
			continue
		}
		if _, err := validateSimpleValue(lexical, m); err == nil {
			return m
		}
	}
	return nil
}

// atomicBaseOf reduces a list to its item type, so that primitiveOf sees
// something with a primitive.
func atomicBaseOf(t *SimpleType) *SimpleType {
	if t != nil && t.Variety == VarietyList && t.ItemType != nil {
		return t.ItemType
	}
	return t
}

// numericEqual compares two numeric literals by value.
func numericEqual(a, b string) bool {
	ra, okA := new(big.Rat).SetString(a)
	rb, okB := new(big.Rat).SetString(b)
	return okA && okB && ra.Cmp(rb) == 0
}

// checkLengthForPrimitive applies the length facets, choosing the unit the
// primitive requires.
//
// The unit is not always characters: hexBinary and base64Binary are measured in
// octets, so "0F" has length 1 rather than 2. Measuring the literal would give
// the wrong answer for every binary-typed value.
func checkLengthForPrimitive(steps []facetStep, normalized, prim string) error {
	var n uint64
	switch prim {
	case "hexBinary":
		n = uint64(len(normalized) / 2)
	case "base64Binary":
		n = uint64(base64DecodedLen(normalized))
	case "string", "anyURI":
		n = uint64(len([]rune(normalized)))
	case "QName", "NOTATION":
		// The length facets are measured in the *value* space, and a
		// QName's value is a (namespace, local name) pair rather than a
		// string — there is no length to compare. Part 2 says as much
		// and deprecates writing the facet at all; the W3C suite
		// expects a 46-character QName to satisfy length="7", which is
		// only possible if the facet is ignored. Measuring the literal
		// rejected every one of those documents.
		return nil
	default:
		// Length does not apply to this primitive; the schema-component
		// constraint on facet applicability rejects it at parse time.
		return nil
	}
	unit := "characters"
	if prim == "hexBinary" || prim == "base64Binary" {
		unit = "octets"
	}
	return checkLengthFacets(steps, n, unit)
}

// base64DecodedLen returns the number of octets a base64 literal encodes.
func base64DecodedLen(s string) int {
	var n, pad int
	for _, r := range s {
		switch {
		case r == '=':
			pad++
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
		default:
			n++
		}
	}
	total := n + pad
	if total%4 != 0 {
		return 0
	}
	return total/4*3 - pad
}

// checkBounds applies minInclusive, maxInclusive, minExclusive and
// maxExclusive.
//
// The comparison is on the value, which for the numeric types means exact
// rational arithmetic rather than float64: xs:decimal has arbitrary precision,
// and comparing 18446744073709551615 as a float would lose the last digits and
// admit values outside xs:unsignedLong.
func checkBounds(steps []facetStep, normalized, prim string) error {
	switch prim {
	case "decimal", "float", "double":
	case "duration":
		return checkDurationBounds(steps, normalized)
	case "dateTime", "time", "date", "gYearMonth", "gYear",
		"gMonthDay", "gDay", "gMonth":
		return checkTemporalBounds(steps, normalized, prim)
	default:
		// The remaining primitives have no order, so the bound facets do
		// not apply and the facet-applicability constraint has already
		// refused any that were written.
		return nil
	}

	val, ok := new(big.Rat).SetString(normalized)
	if !ok {
		// Special float values have no rational form. INF and NaN are
		// outside any bound the schema can write, and comparing them
		// numerically is not meaningful.
		return nil
	}

	for _, st := range steps {
		f := st.facets
		cmp := func(lex *string) (int, bool) {
			if lex == nil {
				return 0, false
			}
			b, ok := new(big.Rat).SetString(strings.TrimSpace(*lex))
			if !ok {
				return 0, false
			}
			return val.Cmp(b), true
		}
		if c, ok := cmp(f.MinInclusive); ok && c < 0 {
			return facetError(st.typ, FacetMinInclusive,
				"%s is less than %s", normalized, *f.MinInclusive)
		}
		if c, ok := cmp(f.MaxInclusive); ok && c > 0 {
			return facetError(st.typ, FacetMaxInclusive,
				"%s is greater than %s", normalized, *f.MaxInclusive)
		}
		if c, ok := cmp(f.MinExclusive); ok && c <= 0 {
			return facetError(st.typ, FacetMinExclusive,
				"%s is not greater than %s", normalized, *f.MinExclusive)
		}
		if c, ok := cmp(f.MaxExclusive); ok && c >= 0 {
			return facetError(st.typ, FacetMaxExclusive,
				"%s is not less than %s", normalized, *f.MaxExclusive)
		}
	}
	return nil
}

// recordIDs notes xs:ID and xs:IDREF values for the document-level check.
//
// An ID is recorded against the element carrying it, not counted outright.
// XSD 1.1 lets one element have several ID attributes, and nothing stops two of
// them holding the same value — that is one ID appearing twice on one element,
// not a document with a duplicate. Counting occurrences would reject it.
func (v *validator) recordIDs(n *xdm.Node, value string, t *SimpleType) {
	v.recordIDsOwned(v.idOwner(n), value, t)
}

// idOwner returns the element an ID carried by n is bound to.
//
// The two versions differ, and the difference is the whole of the 1.1
// relaxation. XSD 1.0 permits an element at most one ID, so every value is its
// own binding and the carrying node is the owner. XSD 1.1 permits several per
// element and does not require them to differ, so what a value binds to is the
// element it identifies — the parent of the carrying node, whether that node is
// an attribute or an element whose content is the ID.
//
// Applying the 1.1 rule under 1.0 would accept a document with two elements
// sharing an ID whenever they happened to be siblings.
func (v *validator) idOwner(n *xdm.Node) *xdm.Node {
	if n == nil {
		return nil
	}
	if v.schema.Version < Version11 {
		if n.Kind == xdm.KindAttribute && n.Parent != nil {
			return n.Parent
		}
		return n
	}
	// The owner must be an *element*. A validation root's own simple
	// content has only the document node above it, so an ID there denotes
	// nothing — the suite puts it as "ID on root does not denote any
	// element". Falling back to the node itself would make the root denote
	// itself, and a reference to that value would then resolve against a
	// binding the spec does not create.
	if n.Parent != nil && n.Parent.Kind == xdm.KindElement {
		return n.Parent
	}
	return nil
}

// recordIDsOwned records bindings for a value already attributed to its owning
// element, which is what a defaulted attribute supplies directly.
func (v *validator) recordIDsOwned(owner *xdm.Node, value string, t *SimpleType) {
	switch idKind(t, value) {
	case "ID":
		v.recordID(owner, value)
	case "IDREF":
		v.idrefs = append(v.idrefs, idref{value: value, node: owner})
	case "IDS", "IDREFS", "MIXED":
		// Each item is classified on its own. A list of a union may
		// contribute a definition for one item and a reference for the
		// next, so the list kind cannot decide for all of them.
		item := t
		if t != nil && t.Variety == VarietyList {
			item = t.ItemType
		}
		for _, word := range splitFields(value) {
			switch idKind(item, word) {
			case "ID":
				v.recordID(owner, word)
			case "IDREF":
				v.idrefs = append(v.idrefs,
					idref{value: word, node: owner})
			}
		}
	}
}

// recordID notes one ID value as bound to an element.
//
// XSD 1.1 lets an element carry several IDs, from attributes and children
// alike, and nothing requires them to differ: the same value twice on one
// element is one binding, not a duplicate. What is still invalid is the same
// value bound to two different elements, which is the distinction between the
// valid and invalid cases the suite pairs up.
//
// owner is the element the ID identifies — see recordIDs, which derives it.
func (v *validator) recordID(owner *xdm.Node, value string) {
	if owner == nil {
		// Nothing to denote; see idOwner.
		return
	}
	if v.idOwners == nil {
		v.idOwners = map[string]*xdm.Node{}
	}
	if prev, seen := v.idOwners[value]; seen {
		if prev != owner {
			v.ids[value]++
		}
		return
	}
	v.idOwners[value] = owner
	v.ids[value]++
}

// idKind reports whether a type is or derives from xs:ID, xs:IDREF or
// xs:IDREFS.
//
// The value is passed because a union has to be looked *through*: a union of
// xs:ID and xs:boolean derives from xs:anySimpleType, so walking its base chain
// finds nothing, and every ID declared through one was silently not recorded.
// What decides is the member that actually validates the value, so the members
// are tried in the same order validateUnionValue tries them.
//
// A list of xs:IDREF behaves the same way, which is what xs:IDREFS is.
func idKind(t *SimpleType, value string) string {
	if t == nil {
		return ""
	}
	switch t.Variety {
	case VarietyUnion:
		for _, m := range t.MemberTypes {
			if m == nil {
				continue
			}
			if _, err := validateSimpleValue(value, m); err != nil {
				continue
			}
			return idKind(m, value)
		}
		return ""
	case VarietyList:
		// A list whose items are IDREFs contributes each item as a
		// reference, which is what xs:IDREFS is. XSD 1.1 also permits
		// a list of xs:ID — 1.0 forbade it because an element could
		// carry only one ID, and lifting that restriction lifts this
		// one with it — so each item is a definition.
		//
		// The item type is asked about each item rather than about the
		// whole literal. It matters when the item type is a union: a
		// list of "xs:ID or xs:integer" answers differently for "aaa"
		// than for "23", and asking with the joined literal answers for
		// neither, so every binding in such a list went unrecorded.
		return listItemKind(t.ItemType, value)
	}

	seen := map[*SimpleType]bool{}
	for cur := t; cur != nil && !seen[cur]; {
		seen[cur] = true
		switch cur.Name.Local {
		case "ID", "IDREF", "IDREFS":
			if cur.Name.URI == NSSchema {
				return cur.Name.Local
			}
		}
		base, ok := cur.Base.(*SimpleType)
		if !ok || base == cur {
			return ""
		}
		cur = base
	}
	return ""
}

// checkIDs applies Validation Root Valid (ID/IDREF) (§3.3.4).
//
// The rule has two clauses with distinct error codes: a reference to an
// undefined ID, and a multiply-defined ID. It is checked once at the validation
// root rather than per element, because an IDREF may legitimately point forward
// to an ID that has not been seen yet.
func (v *validator) checkIDs() {
	for value, count := range v.ids {
		if count > 1 {
			v.fail(nil, "cvc-id.2",
				"ID value %q is defined %d times", value, count)
		}
	}
	for _, ref := range v.idrefs {
		if v.ids[ref.value] == 0 {
			v.fail(ref.node, "cvc-id.1",
				"IDREF %q does not match any ID in the document", ref.value)
		}
	}
}

// checkTemporalBounds applies the bound facets to a date or time value.
//
// A bound that is not comparable to the value does not constrain it. The order
// on these types is partial — a value with a timezone and one without are only
// ordered when every timezone the absent one could stand for gives the same
// answer — and treating "indeterminate" as a failure would reject values the
// spec leaves undetermined.
func checkTemporalBounds(steps []facetStep, normalized, primitive string) error {
	val, ok := parseTemporal(normalized, primitive)
	if !ok {
		return nil
	}
	for _, st := range steps {
		f := st.facets
		cmp := func(lex *string) (int, bool) {
			if lex == nil {
				return 0, false
			}
			b, ok := parseTemporal(strings.TrimSpace(*lex), primitive)
			if !ok {
				return 0, false
			}
			return compareTemporal(val, b)
		}
		if c, ok := cmp(f.MinInclusive); ok && c < 0 {
			return facetError(st.typ, FacetMinInclusive,
				"%s is earlier than %s", normalized, *f.MinInclusive)
		}
		if c, ok := cmp(f.MaxInclusive); ok && c > 0 {
			return facetError(st.typ, FacetMaxInclusive,
				"%s is later than %s", normalized, *f.MaxInclusive)
		}
		if c, ok := cmp(f.MinExclusive); ok && c <= 0 {
			return facetError(st.typ, FacetMinExclusive,
				"%s is not later than %s", normalized, *f.MinExclusive)
		}
		if c, ok := cmp(f.MaxExclusive); ok && c >= 0 {
			return facetError(st.typ, FacetMaxExclusive,
				"%s is not earlier than %s", normalized, *f.MaxExclusive)
		}
	}
	return nil
}

// checkDurationBounds applies the bound facets to an xs:duration.
//
// Duration is only partially ordered because a month has no fixed length: P1M
// and P30D have no order at all. An incomparable bound does not constrain the
// value, for the same reason as above.
func checkDurationBounds(steps []facetStep, normalized string) error {
	val, ok := parseDuration(normalized)
	if !ok {
		return nil
	}
	for _, st := range steps {
		f := st.facets
		cmp := func(lex *string) (int, bool) {
			if lex == nil {
				return 0, false
			}
			b, ok := parseDuration(strings.TrimSpace(*lex))
			if !ok {
				return 0, false
			}
			return compareDuration(val, b)
		}
		if c, ok := cmp(f.MinInclusive); ok && c < 0 {
			return facetError(st.typ, FacetMinInclusive,
				"%s is shorter than %s", normalized, *f.MinInclusive)
		}
		if c, ok := cmp(f.MaxInclusive); ok && c > 0 {
			return facetError(st.typ, FacetMaxInclusive,
				"%s is longer than %s", normalized, *f.MaxInclusive)
		}
		if c, ok := cmp(f.MinExclusive); ok && c <= 0 {
			return facetError(st.typ, FacetMinExclusive,
				"%s is not longer than %s", normalized, *f.MinExclusive)
		}
		if c, ok := cmp(f.MaxExclusive); ok && c >= 0 {
			return facetError(st.typ, FacetMaxExclusive,
				"%s is not shorter than %s", normalized, *f.MaxExclusive)
		}
	}
	return nil
}

// valueEqual compares two literals of a primitive by value rather than by
// spelling.
//
// The date and duration families are the ones where this bites:
// 2010-09-19T24:00:00Z and 2010-09-20T00:00:00Z denote the same instant, and
// PT29H and P1DT5H the same length, so an enumeration listing either admits
// both. Comparing lexical forms rejects documents the spec accepts.
//
// A pair the order leaves indeterminate — one value with a timezone against one
// without, where the ±14 hour interval overlaps — is not equal. Equality has to
// be decided, and "might be equal for some timezone" is not that.
func valueEqual(a, b, primitive string) bool {
	switch primitive {
	case "duration":
		da, okA := parseDuration(a)
		db, okB := parseDuration(b)
		if !okA || !okB {
			return false
		}
		c, comparable := compareDuration(da, db)
		return comparable && c == 0

	case "dateTime", "date", "time",
		"gYear", "gYearMonth", "gMonth", "gMonthDay", "gDay":
		ta, okA := parseTemporal(a, primitive)
		tb, okB := parseTemporal(b, primitive)
		if !okA || !okB {
			return false
		}
		c, comparable := compareTemporal(ta, tb)
		return comparable && c == 0
	}
	return false
}

// checkStringSubtype applies the lexical constraints of the xs:string branch's
// named subtypes.
//
// Part 2 defines xs:Name, xs:NCName and their descendants by pattern, but
// states those patterns in prose rather than as facets on the type, so a schema
// that restricts xs:ID inherits nothing that would reject a value which is not
// an NCName. The check walks to the nearest built-in ancestor, since a
// user-defined restriction of xs:ID is still an xs:ID.
func checkStringSubtype(normalized string, t *SimpleType) error {
	name := nearestBuiltinName(t)
	if name == "" {
		return nil
	}
	ok := true
	switch name {
	case "NCName", "ID", "IDREF", "ENTITY":
		ok = isNCName(normalized)
	case "Name":
		ok = isXMLName(normalized)
	case "NMTOKEN":
		ok = isNmtoken(normalized)
	case "IDREFS", "ENTITIES", "NMTOKENS":
		// The list types are checked item by item where the list is
		// split; the whole literal is not a single token.
		return nil
	case "language":
		ok = isLanguage(normalized)
	default:
		return nil
	}
	if ok {
		return nil
	}
	return &ParseError{
		Code:    "cvc-datatype-valid.1.2.1",
		Message: "\"" + truncate(normalized) + "\" is not a valid xs:" + name,
	}
}

// checkIntegerLexical refuses a decimal point in a value whose type descends
// from xs:integer.
//
// Part 2 gives the integer branch its own lexical space — an optional sign and
// digits, nothing else — rather than deriving it from xs:decimal's by facet.
// The fractionDigits="0" that models it constrains the value, and "+0.0" has
// zero fraction digits, so the facet passes something the lexical space
// excludes.
func checkIntegerLexical(normalized string, t *SimpleType) error {
	if !descendsFromInteger(t) {
		return nil
	}
	if strings.ContainsAny(normalized, ".eE") {
		return &ParseError{
			Code: "cvc-datatype-valid.1.2.1",
			Message: "\"" + truncate(normalized) +
				"\" is not a valid integer literal",
		}
	}
	return nil
}

// descendsFromInteger reports whether xs:integer is on the type's base chain.
func descendsFromInteger(t *SimpleType) bool {
	seen := map[*SimpleType]bool{}
	for cur := t; cur != nil && !seen[cur]; {
		seen[cur] = true
		if cur.Name.URI == NSSchema && cur.Name.Local == "integer" {
			return true
		}
		base, ok := cur.Base.(*SimpleType)
		if !ok || base == cur {
			return false
		}
		cur = base
	}
	return false
}

// nearestBuiltinName returns the local name of the nearest ancestor of t that
// is a built-in in the schema namespace.
func nearestBuiltinName(t *SimpleType) string {
	for cur := t; cur != nil; {
		if cur.Name.URI == NSSchema && cur.Name.Local != "" {
			return cur.Name.Local
		}
		base, ok := cur.Base.(*SimpleType)
		if !ok || base == cur {
			return ""
		}
		cur = base
	}
	return ""
}

// isXMLName reports whether v is an XML Name: like an NCName but with a colon
// permitted anywhere a name character is.
func isXMLName(v string) bool {
	if v == "" {
		return false
	}
	for i, r := range v {
		if i == 0 {
			if !isNameStartRune(r) && r != ':' {
				return false
			}
			continue
		}
		if !isNameRune(r) && r != ':' {
			return false
		}
	}
	return true
}

// isNmtoken reports whether v is an Nmtoken: one or more name characters, with
// no restriction on the first.
func isNmtoken(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if !isNameRune(r) && r != ':' {
			return false
		}
	}
	return true
}

// isNameStartRune reports whether r may begin an XML Name, per the
// NameStartChar production of XML 1.0 Fifth Edition and XML 1.1 (which agree).
// The colon is deliberately absent: callers that permit one add it, and NCName
// is defined by its absence.
//
// The ranges are the production's, not "anything above ASCII". Treating every
// rune >= 0x80 as a name character accepted values the production excludes, and
// two of the exclusions are separators an XML 1.1 document can write as
// character references without their becoming whitespace: NEL (#x85) and LINE
// SEPARATOR (#x2028). saxonData's xv009.n02 and xv009.n03 are exactly those,
// each expected invalid as an xs:NMTOKENS item, and both were accepted.
func isNameStartRune(r rune) bool {
	switch {
	case r == '_':
		return true
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		return true
	case r >= 0xC0 && r <= 0xD6, r >= 0xD8 && r <= 0xF6, r >= 0xF8 && r <= 0x2FF:
		return true
	case r >= 0x370 && r <= 0x37D, r >= 0x37F && r <= 0x1FFF:
		return true
	case r >= 0x200C && r <= 0x200D, r >= 0x2070 && r <= 0x218F:
		return true
	case r >= 0x2C00 && r <= 0x2FEF, r >= 0x3001 && r <= 0xD7FF:
		return true
	case r >= 0xF900 && r <= 0xFDCF, r >= 0xFDF0 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0xEFFFF:
		return true
	}
	return false
}

// isNameRune reports whether r may appear after the first character of an XML
// Name, per the NameChar production.
func isNameRune(r rune) bool {
	switch {
	case isNameStartRune(r):
		return true
	case r == '-', r == '.', r >= '0' && r <= '9':
		return true
	case r == 0xB7:
		return true
	case r >= 0x300 && r <= 0x36F, r >= 0x203F && r <= 0x2040:
		return true
	}
	return false
}

// isLanguage reports whether v matches xs:language: a primary subtag of one to
// eight letters, then any number of subtags of one to eight alphanumerics.
func isLanguage(v string) bool {
	if v == "" {
		return false
	}
	for i, part := range strings.Split(v, "-") {
		if len(part) == 0 || len(part) > 8 {
			return false
		}
		for j := 0; j < len(part); j++ {
			c := part[j]
			isAlpha := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			isDigit := c >= '0' && c <= '9'
			// The primary subtag names a language and is letters
			// only; later subtags may be numeric region codes.
			if i == 0 && !isAlpha {
				return false
			}
			if i > 0 && !isAlpha && !isDigit {
				return false
			}
		}
	}
	return true
}

// listItemKind classifies a list by what its items contribute.
//
// A list of a union may hold IDs and IDREFs at once — the suite's id007 is
// exactly that, "a list type whose items may be either IDs or IDREFs" — so the
// answer is per item, and this only reports whether there is anything to
// record.
func listItemKind(item *SimpleType, value string) string {
	if item == nil {
		return ""
	}
	sawID, sawRef := false, false
	for _, word := range splitFields(value) {
		switch idKind(item, word) {
		case "ID":
			sawID = true
		case "IDREF":
			sawRef = true
		}
	}
	switch {
	case sawID && sawRef:
		return "MIXED"
	case sawID:
		return "IDS"
	case sawRef:
		return "IDREFS"
	}
	return ""
}

// expandFacetQName expands an enumeration facet's value as a QName against the
// namespaces in scope on the facet element itself.
//
// The prefix binds in the schema document, which is a different namespace
// context from the instance document the value will be compared against. An
// unprefixed name takes the default namespace: the value is a QName written in
// element content, not an attribute name, so the XPath attribute rule that
// leaves an unprefixed name in no namespace does not apply. That is what makes
// an instance writing a bare "mp3" under a default namespace match a facet
// written with a prefix bound to the same URI.
//
// The zero QName is returned when the prefix is not bound, which leaves the
// comparison to fall back on the lexical forms.
func expandFacetQName(el *xdm.Node, value string) xdm.QName {
	value = strings.TrimSpace(value)
	prefix, local := "", value
	if i := strings.IndexByte(value, ':'); i >= 0 {
		prefix, local = value[:i], value[i+1:]
	}
	if local == "" || strings.ContainsRune(local, ':') {
		return xdm.QName{}
	}
	uri, ok := el.LookupPrefix(prefix)
	if !ok {
		return xdm.QName{}
	}
	// Only URI and Local are set. An xdm.QName is compared as a whole
	// struct, so a prefix left on one side would make every comparison
	// against an instance value that spells it differently fail.
	return xdm.QName{URI: uri, Local: local}
}

// resolveInstanceQName expands a QName written as the value of an instance node
// against the namespaces in scope there.
//
// An unprefixed name takes the *default* namespace even when the value sits on
// an attribute. That is the rule for a QName appearing in content — Part 2
// §3.2.18 — and not the rule for an attribute's own name, which is in no
// namespace when unprefixed. The distinction is load-bearing: an instance
// writing a bare "mp3" under xmlns="http://notation.example.com" denotes the
// same notation as one writing "smokey:mp3" in the schema, and treating it as
// an absent namespace made those two values differ.
func resolveInstanceQName(at *xdm.Node, value string) (xdm.QName, bool) {
	value = strings.TrimSpace(value)
	prefix, local := "", value
	if i := strings.IndexByte(value, ':'); i >= 0 {
		prefix, local = value[:i], value[i+1:]
	}
	if local == "" || strings.ContainsRune(local, ':') {
		return xdm.QName{}, false
	}
	scope := at
	if scope.Kind == xdm.KindAttribute && scope.Parent != nil {
		scope = scope.Parent
	}
	uri, ok := scope.LookupPrefix(prefix)
	if !ok {
		return xdm.QName{}, false
	}
	// Only URI and Local are set: an xdm.QName compares as a whole struct,
	// and a prefix carried on one side would defeat every comparison.
	return xdm.QName{URI: uri, Local: local}, true
}

// ParseExpandedName reads the "{uri}local" spelling of an already-expanded
// name.
//
// It is how a caller with no instance node states a QName value whose prefix
// it has already resolved. A lexical QName never has this shape — "{" is not
// an NCName character — so accepting it here cannot capture a real instance
// value, and no schema document can contain one.
func ParseExpandedName(v string) (xdm.QName, bool) {
	if !strings.HasPrefix(v, "{") {
		return xdm.QName{}, false
	}
	i := strings.IndexByte(v, '}')
	if i < 0 {
		return xdm.QName{}, false
	}
	local := v[i+1:]
	if local == "" || strings.ContainsAny(local, "{}:") {
		return xdm.QName{}, false
	}
	return xdm.QName{URI: v[1:i], Local: local}, true
}

// qnamePrefixBound reports whether a QName- or NOTATION-valued lexical form
// has its prefix bound where it was written.
//
// Only those two primitives carry a namespace binding in their value space;
// for every other type the value is the characters and there is no prefix to
// resolve. A list or union of them is left alone: its items are checked where
// the list is split and where the member type is chosen, each with the same
// node in hand.
func qnamePrefixBound(n *xdm.Node, normalized string, t *SimpleType) bool {
	if n == nil || t.Variety != VarietyAtomic {
		return true
	}
	p := primitiveOf(t)
	if p == nil || (p.Name.Local != "QName" && p.Name.Local != "NOTATION") {
		return true
	}
	if !strings.Contains(normalized, ":") {
		// An unprefixed name takes the default namespace, or none. Either
		// way there is no binding that can be missing.
		return true
	}
	_, ok := resolveInstanceQName(n, normalized)
	return ok
}
