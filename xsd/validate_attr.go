package xsd

import (
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// validateAttributes checks an element's attributes against a complex type's
// attribute uses and wildcard.
func (v *validator) validateAttributes(el *xdm.Node, t *ComplexType) {
	matched := make(map[*AttributeUse]bool, len(t.AttributeUses))

	// XSD 1.0 permits an element at most one attribute of a type derived
	// from xs:ID (Part 2 §3.3.8). ct-props-correct.5 catches the case where
	// both are declared in the type, but two ID attributes can also reach
	// one element without the type ever naming them together: attZ014a
	// declares an <anyAttribute> and two global ID-typed attributes, and
	// the instance carries both. Nothing about the type is wrong there —
	// only the instance is — so the rule has to be counted here, over the
	// attributes actually present. XSD 1.1 dropped it, which is why this is
	// gated on the version and why the same instance is expected valid
	// under 1.1.
	idAttrs := 0
	var firstID xdm.QName
	countID := func(a *xdm.Node, name xdm.QName, decl *AttributeDecl) {
		if v.schema.Version >= Version11 || decl == nil ||
			nearestBuiltinName(decl.Type) != "ID" {
			return
		}
		idAttrs++
		if idAttrs == 1 {
			firstID = name
			return
		}
		if idAttrs == 2 {
			v.fail(a, "cvc-complex-type.3",
				"attribute %s has a type derived from xs:ID, and so "+
					"does %s; XSD 1.0 allows an element at most one",
				attrName(name), attrName(firstID))
		}
	}

	for _, a := range el.Attrs {
		name := xdm.QName{URI: a.Name.URI, Local: a.Name.Local}

		// The four xsi: attributes are permitted on any element and are
		// not subject to the type's attribute uses — but a type may
		// still declare one, which is how XSD 1.1 makes xsi:type
		// mandatory. Skipping them outright left such a use unmatched,
		// so a type requiring xsi:type reported it missing even when
		// the instance carried it.
		if name.URI == NSInstance {
			switch name.Local {
			case "type", "nil", "schemaLocation", "noNamespaceSchemaLocation":
				if use := findAttributeUse(t.AttributeUses, name); use != nil {
					matched[use] = true
				}
				continue
			}
		}
		// A namespace declaration is not an attribute for this purpose.
		if name.URI == "http://www.w3.org/2000/xmlns/" || a.Name.Prefix == "xmlns" ||
			(a.Name.Prefix == "" && a.Name.Local == "xmlns") {
			continue
		}

		use := findAttributeUse(t.AttributeUses, name)
		if use != nil {
			matched[use] = true
			countID(a, name, use.Decl)
			v.validateAttribute(a, use.Decl, use.Constraint)
			continue
		}

		if t.AttributeWildcard != nil && t.AttributeWildcard.AllowsName(name, v.attributeDefined) {
			// A skipped wildcard contributes no declaration, so it
			// contributes no ID either: the count is over what the
			// schema says these attributes are, and it says nothing.
			if t.AttributeWildcard.ProcessContents != ProcessSkip {
				countID(a, name, v.schema.Attributes[name])
			}
			v.validateWildcardAttribute(a, t.AttributeWildcard, name)
			continue
		}

		v.fail(a, "cvc-complex-type.3.2.2",
			"attribute %s is not permitted here", attrName(name))
	}

	for _, use := range t.AttributeUses {
		if use.Decl == nil {
			// The declaration this use names is · absent · : it lives in a
			// namespace that was imported but never fetched. §5.3 Missing
			// Sub-components defers such a reference to validation rather
			// than making it a schema error, and there is nothing here to
			// require or to default from.
			continue
		}
		if use.Required && !matched[use] {
			v.fail(el, "cvc-complex-type.4",
				"required attribute %s is missing", attrName(use.Decl.Name))
			continue
		}
		if !matched[use] {
			// A defaulted attribute is an attribute for this
			// purpose: §3.4.5 makes the {value constraint} a
			// contribution to the infoset, indistinguishable from
			// one the instance wrote.
			if c := use.Constraint; c != nil || use.Decl.Constraint != nil {
				if c == nil {
					c = use.Decl.Constraint
				}
				if c.Lexical != "" {
					countID(el, use.Decl.Name, use.Decl)
				}
			}
			v.recordDefaultID(el, use)
			v.applyAttributeDefault(el, use)
		}
	}
}

// applyAttributeDefault adds an attribute the schema supplies by default.
//
// XSD 1.0 §3.4.5 (and 1.1 §3.4.5) make the {value constraint} of an unmatched
// attribute use a *contribution to the infoset*: the attribute information
// item is created, with [schema normalized value] set from the default, and
// nothing downstream can tell it from one the instance wrote. So a consumer of
// the PSVI — which for this package means the XSLT layer reading a validated
// result tree — must see @cost on an element whose type declares cost with a
// default, even though the instance omitted it.
//
// A `fixed` constraint on an *absent* attribute is also a default in the
// spec's model (§3.2.2 folds the two into one {value constraint}), and is
// applied here for the same reason; what a fixed constraint additionally does
// — reject a written value that differs — is checked in validateAttribute, not
// here.
//
// This is gated on Annotate for the same reason the type annotation is: it
// mutates the tree the caller handed in, and a caller who only asked "is this
// valid?" has not asked for their document to be rewritten. recordDefaultID
// above deliberately stays ungated, because ID/IDREF binding affects the
// validity *verdict* rather than the tree.
func (v *validator) applyAttributeDefault(el *xdm.Node, use *AttributeUse) {
	if !v.opts.Annotate || use == nil || use.Decl == nil || use.Prohibited {
		return
	}
	c := use.Constraint
	if c == nil {
		c = use.Decl.Constraint
	}
	if c == nil || c.Lexical == "" {
		return
	}
	// An invalid default is a schema error, already reported when the schema
	// was read; adding it to the tree would only spread the damage.
	normalized, err := validateSimpleValue(c.Lexical, use.Decl.Type)
	if err != nil {
		return
	}
	name := use.Decl.Name
	// A defaulted attribute in a namespace needs a prefix to serialize, and a
	// prefix already bound to that namespace is the one to use: it is the
	// author's own spelling, and inventing a second binding for a namespace
	// the element already names would be gratuitous.
	prefix := ""
	if name.URI != "" {
		prefix = prefixInScopeFor(el, name.URI)
		if prefix == "" {
			// Nothing is bound. XSD 1.0 says nothing about what to do here,
			// so 1.0 leaves the attribute off rather than guessing: one
			// serialized with an undeclared prefix is worse than one that is
			// missing.
			//
			// XSD 1.1 3.4.5.1 does say, under "namespace fixup": the
			// processor supplies a prefix and declares it on the element. The
			// attribute is part of the PSVI either way, and dropping it made
			// the difference visible to the XSLT layer -- import-schema-164
			// validates <doc><e/>...</doc> under strict validation and asks
			// for @p:foo on every <e>, prefixed and declared, whatever
			// bindings the instance happened to carry.
			if v.schema.Version < Version11 {
				return
			}
			prefix = declareFixupPrefix(el, name.URI)
		}
	}
	attr := &xdm.Node{
		Kind:  xdm.KindAttribute,
		Name:  xdm.QName{Prefix: prefix, Local: name.Local, URI: name.URI},
		Value: normalized,
	}
	if use.Decl.Type != nil {
		if a := annotationName(use.Decl.Type); a != "" {
			setResolvedAnnotation(attr, a, use.Decl.Type)
		}
	}
	el.AddAttr(attr)
}

// declareFixupPrefix binds a namespace on el and returns the prefix it chose.
//
// This is XSD 1.1 3.4.5.1's namespace fixup. The prefix is arbitrary -- the
// spec leaves the choice to the processor -- so it is generated rather than
// derived from the URI, and checked against what is already in scope so that
// the new binding cannot shadow one the element or an ancestor relies on.
func declareFixupPrefix(el *xdm.Node, uri string) string {
	for i := 0; ; i++ {
		prefix := "ns" + strconv.Itoa(i)
		if _, taken := el.LookupPrefix(prefix); taken {
			continue
		}
		el.AddNamespace(prefix, uri)
		return prefix
	}
}

// prefixInScopeFor finds a prefix bound to a namespace at an element, or ""
// when none is in scope.
func prefixInScopeFor(el *xdm.Node, uri string) string {
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			continue
		}
		for _, ns := range cur.Namespaces {
			if ns.Value == uri && ns.Name.Local != "" && ns.Name.Local != "xmlns" {
				return ns.Name.Local
			}
		}
	}
	return ""
}

// recordDefaultID binds the ID or IDREF a defaulted attribute contributes.
//
// XSD 1.1 permits a default on an attribute of type xs:ID or xs:IDREF, and the
// defaulted value takes part in ID/IDREF binding exactly as a written one does
// — the schema supplies it to the infoset, and nothing downstream can tell the
// difference. Skipping absent attributes therefore accepts documents where two
// elements end up sharing a defaulted ID, or where a defaulted IDREF points at
// nothing.
func (v *validator) recordDefaultID(el *xdm.Node, use *AttributeUse) {
	if use == nil || use.Decl == nil || use.Decl.Type == nil {
		return
	}
	c := use.Constraint
	if c == nil {
		c = use.Decl.Constraint
	}
	if c == nil || c.Lexical == "" {
		return
	}
	normalized, err := validateSimpleValue(c.Lexical, use.Decl.Type)
	if err != nil {
		// An invalid default is a schema error, reported when the
		// schema was read; there is no binding to record.
		return
	}
	// A defaulted xs:ENTITY can never resolve. The type requires the value
	// to name an unparsed entity declared in the document's DTD, and this
	// parser refuses a DOCTYPE unless the caller opts in and records no
	// entities when it does — so there is no table for the name to be in.
	// A written xs:ENTITY is left alone: the document at least had the
	// chance to declare one, and refusing it would make the type unusable
	// rather than merely unchecked. A defaulted one had no such chance,
	// since the schema supplied it.
	if nearestBuiltinName(use.Decl.Type) == "ENTITY" {
		v.fail(el, "cvc-attribute.3",
			"attribute %s defaults to %q, which names no declared unparsed "+
				"entity", attrName(use.Decl.Name), normalized)
		return
	}

	// The element itself is the owner: a defaulted attribute belongs to the
	// element it is supplied on, so recordID's parent step is already done.
	v.recordIDsOwned(el, normalized, use.Decl.Type)

	// An identity-constraint field may select this attribute, and the value
	// it sees has to be the defaulted one — the schema supplies it to the
	// infoset, so a field cannot tell it from a written one. idF016 puts
	// two elements under a unique, one carrying the value and one letting
	// it default, and expects them to collide.
	if v.defaultedAttrs == nil {
		v.defaultedAttrs = map[defaultedAttr]defaultedValue{}
	}
	prim := ""
	if p := primitiveOf(atomicBaseOf(use.Decl.Type)); p != nil {
		prim = p.Name.Local
	}
	v.defaultedAttrs[defaultedAttr{el: el, name: use.Decl.Name}] =
		defaultedValue{normalized: normalized, primitive: prim}
}

// defaultedValue is a defaulted attribute's value and the primitive it belongs
// to, so that a key built from it compares the same way a written one does.
type defaultedValue struct {
	normalized string
	primitive  string
}

// defaultedAttr identifies one defaulted attribute on one element.
type defaultedAttr struct {
	el   *xdm.Node
	name xdm.QName
}

// findAttributeUse returns the use declaring an attribute name.
func findAttributeUse(uses []*AttributeUse, name xdm.QName) *AttributeUse {
	for _, u := range uses {
		if u.Decl != nil && u.Decl.Name == name {
			return u
		}
	}
	return nil
}

func attrName(q xdm.QName) string {
	if q.URI == "" {
		return q.Local
	}
	return "{" + q.URI + "}" + q.Local
}

// validateAttribute checks one attribute against its declaration.
func (v *validator) validateAttribute(a *xdm.Node, decl *AttributeDecl, use *ValueConstraint) {
	if decl == nil || decl.Type == nil {
		return
	}
	normalized, err := validateSimpleValueIn(a.Value, decl.Type, v.schema.Version, a)
	if err != nil {
		v.fail(a, "cvc-attribute.3",
			"attribute %s: %v", attrName(decl.Name), err)
		return
	}

	// The use's value constraint overrides the declaration's, which is why
	// the same declaration can be fixed in one type and free in another.
	c := use
	if c == nil {
		c = decl.Constraint
	}
	if c != nil && c.Fixed {
		// The schema's version, not 1.0: the fixed value is a lexical
		// form and a few lexical spaces differ between the versions.
		// "+INF" is one — 1.1 admits it for xs:float and xs:double,
		// 1.0 does not — so under 1.0 defaults this validation failed
		// and, because the comparison is guarded on err == nil, the
		// whole fixed check was silently skipped. saxonData's
		// simple001.n01 (fixed="+INF", instance "-INF") and
		// simple001.n02 ("NaN") were accepted for exactly that reason.
		want, err := validateSimpleValueVersion(c.Lexical, decl.Type,
			v.schema.Version)
		if err == nil && !fixedValueEqual(want, normalized, decl.Type) {
			v.fail(a, "cvc-attribute.4",
				"attribute %s is fixed at %q but is %q",
				attrName(decl.Name), want, normalized)
		}
	}

	v.recordIDs(a, normalized, decl.Type)
	v.recordKeyValue(a, normalized, decl.Type)

	if v.opts.Annotate && decl.Type.Name.Local != "" {
		// SetTypeAnnotation rather than a bare assignment: it also records
		// the is-id and is-idrefs properties from the declared type. Those
		// are separate state from the annotation because XSLT's
		// input-type-annotations="strip" clears the annotation while
		// requiring them to survive, and fn:id/fn:idref are defined over
		// them rather than over the annotation.
		setResolvedAnnotation(a,
			xdm.AnnotationName(decl.Type.Name.URI, decl.Type.Name.Local),
			decl.Type)
	}
}

// validateWildcardAttribute checks an attribute matched by an attribute
// wildcard.
func (v *validator) validateWildcardAttribute(a *xdm.Node, w *Wildcard, name xdm.QName) {
	switch w.ProcessContents {
	case ProcessSkip:
		return
	case ProcessLax:
		if d, ok := v.schema.Attributes[name]; ok {
			v.validateAttribute(a, d, nil)
		}
	case ProcessStrict:
		d, ok := v.schema.Attributes[name]
		if !ok {
			v.fail(a, "cvc-complex-type.3.2.2",
				"no declaration for attribute %s, matched by a strict wildcard",
				attrName(name))
			return
		}
		v.validateAttribute(a, d, nil)
	}
}

// checkNoForeignAttributes reports attributes on an element whose type permits
// none.
//
// An element with a simple type may carry only the four xsi: attributes and
// namespace declarations. This is clause 3.1.1 of Element Locally Valid (Type),
// which names those four explicitly.
func (v *validator) checkNoForeignAttributes(el *xdm.Node, uses []*AttributeUse, w *Wildcard) {
	for _, a := range el.Attrs {
		name := xdm.QName{URI: a.Name.URI, Local: a.Name.Local}
		if name.URI == NSInstance {
			switch name.Local {
			case "type", "nil", "schemaLocation", "noNamespaceSchemaLocation":
				continue
			}
		}
		if name.URI == "http://www.w3.org/2000/xmlns/" || a.Name.Prefix == "xmlns" ||
			(a.Name.Prefix == "" && a.Name.Local == "xmlns") {
			continue
		}
		if findAttributeUse(uses, name) != nil {
			continue
		}
		if w != nil && w.AllowsName(name, v.attributeDefined) {
			continue
		}
		v.fail(a, "cvc-type.3.1.1",
			"attribute %s is not permitted on an element with a simple type",
			attrName(name))
	}
}

// checkLexicalSpace reports whether a normalised value belongs to a primitive's
// lexical space.
//
// The checks reuse the parsers the xpath package already has for casting, so
// that a value accepted here is one the XPath layer would produce the same
// value for. Writing a second set would be a way for the two to disagree.
func checkLexicalSpace(value, primitive string) error {
	return checkLexicalSpaceVersion(value, primitive, Version10)
}

func checkLexicalSpaceVersion(value, primitive string, version Version) error {
	if primitive == "" {
		// xs:anySimpleType, whose lexical space is unconstrained.
		return nil
	}
	if ok := lexicalOKVersion(value, primitive, version); !ok {
		return &ParseError{
			Code:    "cvc-datatype-valid.1.2.1",
			Message: "\"" + truncate(value) + "\" is not a valid xs:" + primitive,
		}
	}
	return nil
}

// lexicalOK dispatches to the per-primitive lexical check.
func lexicalOK(v, primitive string) bool {
	return lexicalOKVersion(v, primitive, Version10)
}

func lexicalOKVersion(v, primitive string, version Version) bool {
	switch primitive {
	case "string":
		return true
	case "boolean":
		return v == "true" || v == "false" || v == "1" || v == "0"
	case "decimal":
		return isDecimalLexical(v)
	case "float", "double":
		return isFloatLexical(v, version)
	case "hexBinary":
		return isHexBinary(v)
	case "base64Binary":
		return isBase64Binary(v)
	case "anyURI":
		return isAnyURILexical(v, version)
	case "QName", "NOTATION":
		return isQNameLexical(v)
	case "duration", "dateTime", "time", "date",
		"gYearMonth", "gYear", "gMonthDay", "gDay", "gMonth":
		return isDateTimeLexical(v, primitive, version)
	}
	return true
}

// isDecimalLexical reports whether v is an xs:decimal literal.
//
// The grammar is deliberately narrow: an optional sign, digits, an optional
// fraction. No exponent — that is xs:double — and no hexadecimal, which
// big.Rat.SetString would otherwise accept.
func isDecimalLexical(v string) bool {
	if v == "" {
		return false
	}
	i := 0
	if v[i] == '+' || v[i] == '-' {
		i++
	}
	digits, dot := 0, false
	for ; i < len(v); i++ {
		switch {
		case v[i] >= '0' && v[i] <= '9':
			digits++
		case v[i] == '.' && !dot:
			dot = true
		default:
			return false
		}
	}
	return digits > 0
}

// isFloatLexical reports whether v is an xs:float or xs:double literal.
func isFloatLexical(v string, version Version) bool {
	switch v {
	case "INF", "-INF", "NaN":
		return true
	case "+INF":
		// XSD 1.1 added the leading plus to the lexical space of the
		// two floating types; 1.0 admits only "INF". Accepting it under
		// 1.0 was one extra spelling of a value 1.0 already has — but
		// the suite checks the lexical space rather than the value, and
		// float018 and double018 both expect it refused there.
		return version >= Version11
	}
	if v == "" {
		return false
	}
	i := 0
	if v[i] == '+' || v[i] == '-' {
		i++
	}
	mant, dot := 0, false
	for ; i < len(v); i++ {
		if v[i] >= '0' && v[i] <= '9' {
			mant++
			continue
		}
		if v[i] == '.' && !dot {
			dot = true
			continue
		}
		break
	}
	if mant == 0 {
		return false
	}
	if i == len(v) {
		return true
	}
	if v[i] != 'e' && v[i] != 'E' {
		return false
	}
	i++
	if i < len(v) && (v[i] == '+' || v[i] == '-') {
		i++
	}
	exp := 0
	for ; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
		exp++
	}
	return exp > 0
}

func isHexBinary(v string) bool {
	if len(v)%2 != 0 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func isBase64Binary(v string) bool {
	n := 0
	for _, r := range v {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '+', r == '/', r == '=':
			n++
		case r == ' ':
		default:
			return false
		}
	}
	return n%4 == 0
}

// isQNameLexical reports whether v is a QName: an optional prefix and a local
// name, both NCNames.
//
// "xmlns" is excluded as a prefix. Namespaces in XML binds it to nothing — it
// is the attribute that *declares* bindings, and no declaration can bind it —
// so "xmlns:xsi" has a prefix that cannot resolve, whatever is in scope. The
// working group settled this on the 2010-02-05 telcon, "there is no binding
// for xmlns as a prefix, so these are not valid QNames", and QName009 has been
// marked stable against it since.
//
// A prefix that is merely *undeclared* is a different fault and not one this
// function can see: it needs the element's in-scope namespaces. Only the
// unbindable prefix is decidable from the literal alone.
func isQNameLexical(v string) bool {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		return isNCName(v[:i]) && v[:i] != "xmlns" && isNCName(v[i+1:])
	}
	return isNCName(v)
}

// isNCName reports whether v is an NCName: a name with no colon.
func isNCName(v string) bool {
	if v == "" {
		return false
	}
	for i, r := range v {
		if i == 0 {
			if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x80) {
				return false
			}
			continue
		}
		if !(r == '_' || r == '-' || r == '.' ||
			r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r >= 0x80) {
			return false
		}
	}
	return true
}

// isDateTimeLexical reports whether v matches a date or time primitive's
// lexical space.
//
// The shapes are checked rather than the values: whether 2024-02-31 names a
// real day is a value-space question the date parser answers, and this only
// establishes that the literal has the right form to be handed to it.
func isDateTimeLexical(v, primitive string, version Version) bool {
	if primitive == "duration" {
		return isDurationLexical(v)
	}

	body, tz := splitTimezone(v)
	if !tz {
		return false
	}
	switch primitive {
	case "dateTime":
		i := strings.IndexByte(body, 'T')
		return i > 0 && isDatePart(body[:i], version) && isTimePart(body[i+1:])
	case "date":
		return isDatePart(body, version)
	case "time":
		return isTimePart(body)
	case "gYear":
		return isSignedDigits(body, 4) && yearOK(body, version)
	case "gYearMonth":
		i := strings.LastIndexByte(body, '-')
		return i > 0 && isSignedDigits(body[:i], 4) && yearOK(body[:i], version) &&
			isDigits(body[i+1:], 2) && inRange(body[i+1:], 1, 12)
	case "gMonth":
		return strings.HasPrefix(body, "--") && isDigits(body[2:], 2) &&
			inRange(body[2:], 1, 12)
	case "gDay":
		return strings.HasPrefix(body, "---") && isDigits(body[3:], 2) &&
			inRange(body[3:], 1, 31)
	case "gMonthDay":
		if !strings.HasPrefix(body, "--") || len(body) != 7 || body[4] != '-' ||
			!isDigits(body[2:4], 2) || !isDigits(body[5:], 2) {
			return false
		}
		var month, day int64
		if !parseInt(body[2:4], &month) || !parseInt(body[5:], &day) {
			return false
		}
		// gMonthDay has no year, so February is given 29 days: --02-29
		// is a date that occurs, just not every year.
		return month >= 1 && month <= 12 && day >= 1 &&
			day <= daysInMonth(2000, month)
	}
	return false
}

// splitTimezone removes a trailing timezone and reports whether what remained
// was well formed.
func splitTimezone(v string) (string, bool) {
	if strings.HasSuffix(v, "Z") {
		return v[:len(v)-1], true
	}
	if len(v) >= 6 {
		tz := v[len(v)-6:]
		if (tz[0] == '+' || tz[0] == '-') && tz[3] == ':' &&
			isDigits(tz[1:3], 2) && isDigits(tz[4:], 2) {
			return v[:len(v)-6], true
		}
	}
	return v, true
}

func isDatePart(v string, version Version) bool {
	// A year may have more than four digits and may be negative, so the
	// split is on the last two hyphens rather than on fixed offsets.
	last := strings.LastIndexByte(v, '-')
	if last <= 0 {
		return false
	}
	mid := strings.LastIndexByte(v[:last], '-')
	if mid <= 0 {
		return false
	}
	if !isSignedDigits(v[:mid], 4) || !isDigits(v[mid+1:last], 2) ||
		!isDigits(v[last+1:], 2) {
		return false
	}
	if !yearOK(v[:mid], version) {
		return false
	}
	// The components must name a date that exists. 2001-02-30 is three
	// well-formed numbers and not a day, and -0003-02-29 is the same trap
	// with the proleptic Gregorian leap rule: year -3 is 4 BCE in
	// astronomical numbering, which is not divisible by four.
	var year, month, day int64
	if !parseInt(v[:mid], &year) || !parseInt(v[mid+1:last], &month) ||
		!parseInt(v[last+1:], &day) {
		return false
	}
	if v[0] == '-' {
		year = -year
	}
	return month >= 1 && month <= 12 && day >= 1 && day <= daysInMonth(year, month)
}

// daysInMonth is in temporal.go, applying the proleptic Gregorian leap rule to
// an astronomical year number: 0 is 1 BCE and -1 is 2 BCE, which is why year 0
// is a leap year and year -3 is not.

func isTimePart(v string) bool {
	if len(v) < 8 || v[2] != ':' || v[5] != ':' {
		return false
	}
	if !isDigits(v[:2], 2) || !isDigits(v[3:5], 2) || !isDigits(v[6:8], 2) {
		return false
	}
	var h, m, sec int64
	if !parseInt(v[:2], &h) || !parseInt(v[3:5], &m) || !parseInt(v[6:8], &sec) {
		return false
	}
	// 24:00:00 is the one hour-24 form the lexical space admits, and only
	// with zero minutes and seconds: it names the end of a day, not a
	// twenty-fifth hour.
	if h > 24 || m > 59 || sec > 59 || (h == 24 && (m != 0 || sec != 0)) {
		return false
	}
	if len(v) == 8 {
		return true
	}
	if h == 24 {
		// 24:00:00.5 would be past the end of the day.
		return v[8] == '.' && len(v) > 9 && isDigits(v[9:], -1) &&
			strings.Trim(v[9:], "0") == ""
	}
	return v[8] == '.' && len(v) > 9 && isDigits(v[9:], -1)
}

// isSignedDigits reports whether v is an optionally signed run of at least min
// digits, with no leading zeros beyond the minimum width.
func isSignedDigits(v string, min int) bool {
	if strings.HasPrefix(v, "-") {
		v = v[1:]
	}
	if !isDigits(v, -1) || len(v) < min {
		return false
	}
	// A year is exactly four digits unless it needs more, and one that
	// needs more may not be padded: "00000-02" is a five-digit spelling of
	// year zero, which the lexical space does not admit. Without this the
	// only thing distinguishing it from "0000-02" is a character the value
	// space cannot see.
	return len(v) == min || v[0] != '0'
}

// isDigits reports whether v is a run of digits, of exactly n of them when n is
// not negative.
func isDigits(v string, n int) bool {
	if v == "" {
		return false
	}
	if n >= 0 && len(v) != n {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}

// isDurationLexical reports whether v is an xs:duration literal.
func isDurationLexical(v string) bool {
	if strings.HasPrefix(v, "-") {
		v = v[1:]
	}
	if !strings.HasPrefix(v, "P") || v == "P" {
		return false
	}
	v = v[1:]

	date, timePart, hasT := strings.Cut(v, "T")
	if hasT && timePart == "" {
		// "P1DT" is not legal: a T must be followed by a time.
		return false
	}
	if !isDurationFields(date, "YMD") {
		return false
	}
	if hasT && !isDurationFields(timePart, "HMS") {
		return false
	}
	return date != "" || hasT
}

// isDurationFields checks one half of a duration against its designators.
//
// Only the seconds field may carry a fraction. Part 2 gives every other field
// unsigned integer digits, so "P200.5Y" is not a duration however reasonable
// it looks — the fraction has nowhere to go, since a year is not a fixed
// number of months (duration011).
func isDurationFields(v, designators string) bool {
	if v == "" {
		return true
	}
	pos := 0
	for len(v) > 0 {
		n, dots := 0, 0
		for n < len(v) && (v[n] >= '0' && v[n] <= '9' || v[n] == '.') {
			if v[n] == '.' {
				dots++
			}
			n++
		}
		if n == 0 || n == len(v) {
			return false
		}
		d := strings.IndexByte(designators[pos:], v[n])
		if d < 0 {
			return false
		}
		if dots > 0 {
			// A fraction is admitted only on seconds, and only one
			// point, with digits on BOTH sides of it. Part 2
			// §3.2.6.1 spells the seconds field \d+(\.\d+)?: the
			// fractional part is optional as a whole, but writing
			// the point commits you to at least one digit after it.
			// "PT12H30M12.S" (simple086.n01) has the point and no
			// digits, and was accepted because only the left side
			// was checked.
			if v[n] != 'S' || dots > 1 || v[0] == '.' || v[n-1] == '.' {
				return false
			}
		}
		pos += d + 1
		v = v[n+1:]
	}
	return true
}

// inRange reports whether a digit string denotes a value within bounds.
func inRange(v string, lo, hi int64) bool {
	var n int64
	if !parseInt(v, &n) {
		return false
	}
	return n >= lo && n <= hi
}

// yearOK rejects year zero under XSD 1.0.
//
// Part 2 §D.3.2 is blunt about it — "The year '0000' is an illegal year value"
// — and §3.2.7.1 repeats it in the lexical production. The note beside it
// explains why the rule did not last: ISO 8601:2000 arrived while 1.0 was being
// finished and does allow '0000', for the year 1 BCE, which is the ordinary
// astronomical convention. The working group recorded its intention to allow it
// "in a subsequent version", and 1.1 does.
//
// So this is a genuine version difference rather than an erratum, and the same
// literal has to be refused under one version and accepted under the other.
// dateTime011 pins the 1.0 half.
func yearOK(year string, version Version) bool {
	if version >= Version11 {
		return true
	}
	return strings.TrimLeft(strings.TrimPrefix(year, "-"), "0") != ""
}

// isAnyURILexical reports whether v is in the lexical space of xs:anyURI.
//
// The two versions genuinely differ here, and the suite states it in as many
// words on anyURI_b004: "In XSD 1.1, any sequence of characters is allowed in
// xs:anyURI, so the schema becomes valid", against expectations recorded as
// invalid for 1.0 and valid for 1.1.
//
// Under 1.0 the lexical space is the sequences which are legal URIs *after* the
// escaping algorithm of XML Linking §5.4 has been applied. That algorithm
// percent-escapes exactly the characters RFC 2396 excludes — space, <, >, ",
// {, }, |, \, ^, ` — so those are all in the lexical space rather than out of
// it: "foo<bar" escapes to "foo%3Cbar" and is fine, which anyURI_a014, a015 and
// a016 pin by expecting their schemas to load under both versions.
//
// What is left for 1.0 to reject is the syntax the escaping cannot repair: a
// malformed scheme. RFC 2396 requires a scheme to begin with a letter, so
// "99999...anyURI:" is not one, and no escaping makes it one because the colon
// is not escaped.
func isAnyURILexical(v string, version Version) bool {
	if version >= Version11 {
		return true
	}
	// A percent sign must introduce two hex digits; the escaping algorithm
	// leaves an existing escape alone, so a malformed one survives into the
	// result.
	for i := 0; i < len(v); i++ {
		if v[i] != '%' {
			continue
		}
		if i+2 >= len(v) || !isHexByte(v[i+1]) || !isHexByte(v[i+2]) {
			return false
		}
		i += 2
	}
	return schemeOK(v)
}

// schemeOK checks the scheme of a URI reference, if it has one.
//
// A colon before any slash, question mark or hash marks off a scheme, and RFC
// 2396 requires it to be a letter followed by letters, digits, "+", "-" or ".".
// A reference with no such colon is relative and has no scheme to check.
func schemeOK(v string) bool {
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '/', '?', '#':
			// A relative reference: everything before here is a
			// path segment, not a scheme.
			return true
		case ':':
			if i == 0 {
				// ":a" has an empty scheme.
				return false
			}
			if !isAlphaByte(v[0]) {
				return false
			}
			for j := 1; j < i; j++ {
				c := v[j]
				if !isAlphaByte(c) && !(c >= '0' && c <= '9') &&
					c != '+' && c != '-' && c != '.' {
					return false
				}
			}
			return true
		}
	}
	return true
}

func isAlphaByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isHexByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
