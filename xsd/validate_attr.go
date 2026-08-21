package xsd

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// validateAttributes checks an element's attributes against a complex type's
// attribute uses and wildcard.
func (v *validator) validateAttributes(el *xdm.Node, t *ComplexType) {
	matched := make(map[*AttributeUse]bool, len(t.AttributeUses))

	for _, a := range el.Attrs {
		name := xdm.QName{URI: a.Name.URI, Local: a.Name.Local}

		// The four xsi: attributes are permitted on any element and are
		// not subject to the type's attribute uses.
		if name.URI == NSInstance {
			switch name.Local {
			case "type", "nil", "schemaLocation", "noNamespaceSchemaLocation":
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
			v.validateAttribute(a, use.Decl, use.Constraint)
			continue
		}

		if t.AttributeWildcard != nil && t.AttributeWildcard.Allows(name.URI) {
			v.validateWildcardAttribute(a, t.AttributeWildcard, name)
			continue
		}

		v.fail(a, "cvc-complex-type.3.2.2",
			"attribute %s is not permitted here", attrName(name))
	}

	for _, use := range t.AttributeUses {
		if use.Required && !matched[use] {
			v.fail(el, "cvc-complex-type.4",
				"required attribute %s is missing", attrName(use.Decl.Name))
		}
	}
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
	normalized, err := validateSimpleValue(a.Value, decl.Type)
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
		want, err := validateSimpleValue(c.Lexical, decl.Type)
		if err == nil && want != normalized {
			v.fail(a, "cvc-attribute.4",
				"attribute %s is fixed at %q but is %q",
				attrName(decl.Name), want, normalized)
		}
	}

	v.recordIDs(a, normalized, decl.Type)

	if v.opts.Annotate && decl.Type.Name.Local != "" {
		a.TypeAnnotation = decl.Type.Name.Local
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
		if w != nil && w.Allows(name.URI) {
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
	if primitive == "" {
		// xs:anySimpleType, whose lexical space is unconstrained.
		return nil
	}
	if ok := lexicalOK(value, primitive); !ok {
		return &ParseError{
			Code:    "cvc-datatype-valid.1.2.1",
			Message: "\"" + truncate(value) + "\" is not a valid xs:" + primitive,
		}
	}
	return nil
}

// lexicalOK dispatches to the per-primitive lexical check.
func lexicalOK(v, primitive string) bool {
	switch primitive {
	case "string":
		return true
	case "boolean":
		return v == "true" || v == "false" || v == "1" || v == "0"
	case "decimal":
		return isDecimalLexical(v)
	case "float", "double":
		return isFloatLexical(v)
	case "hexBinary":
		return isHexBinary(v)
	case "base64Binary":
		return isBase64Binary(v)
	case "anyURI":
		// Almost every string is a legal anyURI; the exceptions are
		// values with characters that cannot appear in a URI reference
		// at all.
		return !strings.ContainsAny(v, " \t\n\r<>\"{}|\\^`")
	case "QName", "NOTATION":
		return isQNameLexical(v)
	case "duration", "dateTime", "time", "date",
		"gYearMonth", "gYear", "gMonthDay", "gDay", "gMonth":
		return isDateTimeLexical(v, primitive)
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
func isFloatLexical(v string) bool {
	switch v {
	case "INF", "-INF", "NaN":
		return true
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
func isQNameLexical(v string) bool {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		return isNCName(v[:i]) && isNCName(v[i+1:])
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
func isDateTimeLexical(v, primitive string) bool {
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
		return i > 0 && isDatePart(body[:i]) && isTimePart(body[i+1:])
	case "date":
		return isDatePart(body)
	case "time":
		return isTimePart(body)
	case "gYear":
		return isSignedDigits(body, 4)
	case "gYearMonth":
		i := strings.LastIndexByte(body, '-')
		return i > 0 && isSignedDigits(body[:i], 4) && isDigits(body[i+1:], 2)
	case "gMonth":
		return strings.HasPrefix(body, "--") && isDigits(body[2:], 2)
	case "gDay":
		return strings.HasPrefix(body, "---") && isDigits(body[3:], 2)
	case "gMonthDay":
		return strings.HasPrefix(body, "--") && len(body) == 7 &&
			isDigits(body[2:4], 2) && body[4] == '-' && isDigits(body[5:], 2)
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

func isDatePart(v string) bool {
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
	return isSignedDigits(v[:mid], 4) && isDigits(v[mid+1:last], 2) &&
		isDigits(v[last+1:], 2)
}

func isTimePart(v string) bool {
	if len(v) < 8 || v[2] != ':' || v[5] != ':' {
		return false
	}
	if !isDigits(v[:2], 2) || !isDigits(v[3:5], 2) || !isDigits(v[6:8], 2) {
		return false
	}
	if len(v) == 8 {
		return true
	}
	return v[8] == '.' && len(v) > 9 && isDigits(v[9:], -1)
}

// isSignedDigits reports whether v is an optionally signed run of at least min
// digits, with no leading zeros beyond the minimum width.
func isSignedDigits(v string, min int) bool {
	if strings.HasPrefix(v, "-") {
		v = v[1:]
	}
	return isDigits(v, -1) && len(v) >= min
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
func isDurationFields(v, designators string) bool {
	if v == "" {
		return true
	}
	pos := 0
	for len(v) > 0 {
		n := 0
		for n < len(v) && (v[n] >= '0' && v[n] <= '9' || v[n] == '.') {
			n++
		}
		if n == 0 || n == len(v) {
			return false
		}
		d := strings.IndexByte(designators[pos:], v[n])
		if d < 0 {
			return false
		}
		pos += d + 1
		v = v[n+1:]
	}
	return true
}
