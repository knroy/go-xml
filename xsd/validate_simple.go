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
	normalized, err := validateSimpleValue(lexical, t)
	if err != nil {
		v.fail(n, "cvc-datatype-valid.1", "%v", err)
		return
	}

	// A fixed value constraint requires the instance to carry that value.
	// The comparison is on the normalised form, since that is what the
	// spec's [schema normalized value] holds.
	if decl != nil && decl.Constraint != nil && decl.Constraint.Fixed {
		want, err := validateSimpleValue(decl.Constraint.Lexical, t)
		if err == nil && want != normalized {
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
// Only the temporal families are recorded. Every other primitive's canonical
// form is its normalized lexical form, so the string comparison the constraint
// already does is a value comparison for them.
func (v *validator) recordKeyValue(n *xdm.Node, normalized string, t *SimpleType) {
	prim := ""
	if p := primitiveOf(t); p != nil {
		prim = p.Name.Local
	}
	switch prim {
	case "duration", "dateTime", "date", "time",
		"gYear", "gYearMonth", "gMonth", "gMonthDay", "gDay":
	default:
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
	switch t.Variety {
	case VarietyList:
		return validateListValue(lexical, t)
	case VarietyUnion:
		return validateUnionValue(lexical, t)
	}
	return validateAtomicValue(lexical, t)
}

// validateAtomicValue checks an atomic value.
func validateAtomicValue(lexical string, t *SimpleType) (string, error) {
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
	if err := checkLexicalSpace(normalized, prim); err != nil {
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

	if err := checkEnumeration(steps, normalized, t); err != nil {
		return "", err
	}
	if err := checkLengthForPrimitive(steps, normalized, prim); err != nil {
		return "", err
	}
	if err := checkBounds(steps, normalized, prim); err != nil {
		return "", err
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
	if err := checkEnumeration(steps, normalized, t); err != nil {
		return "", err
	}

	if t.ItemType != nil {
		for _, item := range items {
			if _, err := validateSimpleValue(item, t.ItemType); err != nil {
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
	steps := facetChain(t)

	for _, m := range t.MemberTypes {
		if m == nil {
			continue
		}
		normalized, err := validateSimpleValue(lexical, m)
		if err != nil {
			continue
		}
		// A union admits only pattern and enumeration, and they apply
		// to the value as the member normalised it.
		if err := checkPatterns(steps, normalized); err != nil {
			continue
		}
		if err := checkEnumeration(steps, normalized, t); err != nil {
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
	prim := ""
	if p := primitiveOf(t); p != nil {
		prim = p.Name.Local
	}
	numeric := prim == "decimal" || prim == "float" || prim == "double"

	for _, st := range steps {
		if !st.facets.HasEnumerations {
			continue
		}
		ok := false
		for _, e := range st.facets.Enumerations {
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

	seen := 0
	for cur := t; cur != nil; {
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
		if seen++; seen > 64 {
			return ""
		}
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

func isNameStartRune(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x80
}

func isNameRune(r rune) bool {
	return isNameStartRune(r) || r == '-' || r == '.' || r >= '0' && r <= '9'
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
