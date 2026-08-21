package xsd

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// Conditional inclusion (XSD 1.1 Part 1 §4.2.1, "Conditional inclusion").
//
// A schema document may carry attributes in the versioning namespace that say
// which processors should see an element at all. They are not constraints on
// the instance: an element the conditions exclude is treated as though it were
// not written, so one schema document can carry both a 1.0 and a 1.1 spelling
// of the same declaration and each processor reads the one it understands.
//
// This is why the check belongs in contentChildren rather than in each reader.
// An excluded element must vanish before anything looks at it — a reader that
// noticed it and skipped it would still have reported its errors, which is the
// opposite of what the feature is for.

// NSVersioning is the XSD 1.1 versioning namespace.
const NSVersioning = "http://www.w3.org/2007/XMLSchema-versioning"

// includeElement reports whether an element survives conditional inclusion.
//
// All four kinds of condition must hold, and each names a list that must hold
// in full: typeAvailable keeps the element only if *every* type listed is one
// this processor has, and typeUnavailable drops it only if every one is absent.
// A list mixing a known name with an unknown one therefore fails both — which
// is what the suite's vc012 and vc013 pin, using the same instance against the
// two spellings and expecting opposite answers.
//
// An element with no versioning attributes is always included, which is every
// element in a schema that does not use the feature.
func includeElement(el *xdm.Node, version Version) bool {
	for _, a := range el.Attrs {
		if a.Name.URI != NSVersioning {
			continue
		}
		// The attribute names are matched case-insensitively because
		// the suite contains both minVersion and minversion, and a
		// schema author who miscapitalises one gets silence otherwise:
		// an unrecognised versioning attribute is ignored, so the
		// element is included when it should have been dropped.
		switch strings.ToLower(a.Name.Local) {
		case "minversion":
			if !versionAtLeast(version, a.Value) {
				return false
			}
		case "maxversion":
			// maxVersion is exclusive: the element is for
			// processors *below* the named version.
			if versionAtLeast(version, a.Value) {
				return false
			}
		case "typeavailable":
			// Keep the element only when every named type is
			// definitely available; an unknown name leaves the
			// condition undecided, and an undecided condition
			// cannot vouch for the element.
			if typeListAvailability(el, a.Value, version) != isAvailable {
				return false
			}
		case "typeunavailable":
			// The mirror of typeAvailable: that keeps the element
			// when every named type is definitely available, so
			// this drops it in the same case. An unknown name
			// leaves the condition undecided and the element
			// stays — which is what separates the suite's vc011,
			// naming only a known type and so excluding the
			// element, from vc013, which mixes in an unresolvable
			// name and keeps it.
			if typeListAvailability(el, a.Value, version) == isAvailable {
				return false
			}
		case "facetavailable":
			if facetListAvailability(el, a.Value, version) != isAvailable {
				return false
			}
		case "facetunavailable":
			if facetListAvailability(el, a.Value, version) == isAvailable {
				return false
			}
		}
	}
	return true
}

// versionAtLeast reports whether the processor's version is at least the one
// named.
//
// The value is a decimal, and only 1.0 and 1.1 mean anything here: a schema
// asking for 1.2 is asking for a processor this is not, and one asking for 0.9
// is satisfied by any. Anything unparseable is treated as unsatisfiable rather
// than as satisfied, so a typo drops the element instead of silently keeping a
// declaration meant for some other processor.
func versionAtLeast(version Version, want string) bool {
	want = strings.TrimSpace(want)
	have := "1.0"
	if version >= Version11 {
		have = "1.1"
	}
	return compareDecimalVersion(have, want) >= 0
}

// compareDecimalVersion orders two dotted decimal version strings.
func compareDecimalVersion(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		x, y := 0, 0
		if i < len(as) {
			x = atoiOrZero(as[i])
		}
		if i < len(bs) {
			y = atoiOrZero(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func atoiOrZero(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return n
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// availability is the answer to "does this processor have this component".
type availability uint8

const (
	// notAvailable means the name was resolved and this processor does not
	// implement it.
	notAvailable availability = iota
	// isAvailable means the name was resolved and this processor has it.
	isAvailable
	// unknownAvailability means the name could not be resolved at all — an
	// unbound prefix, or a namespace this processor knows nothing about.
	unknownAvailability
)

// typeAvailable answers whether one named type is one this processor
// implements.
//
// A name outside the schema namespace is not "unavailable" but *unknown*: this
// processor has no way to say whether some other vocabulary defines it. The
// distinction is what separates the suite's vc011 from vc013 — the same
// instance, invalid against typeUnavailable="xs:integer" and valid against
// typeUnavailable="vc:list-of-QNames xs:integer", which only makes sense if an
// unresolvable name makes the whole condition indeterminate rather than
// contributing an answer of its own.
func typeAvailable(el *xdm.Node, word string, version Version) availability {
	name, ok := resolveVersioningQName(el, word)
	if !ok || name.URI != NSSchema {
		return unknownAvailability
	}
	if t, defined := builtinTypes()[name]; !defined || t == nil {
		return notAvailable
	}
	if version < Version11 && !availableIn10(name.Local) {
		return notAvailable
	}
	return isAvailable
}

// availabilityOf combines a list's answers.
//
// An unknown name anywhere in the list makes the whole list unknown, since the
// condition cannot be decided without knowing about that name. Otherwise the
// list is available only if every name is.
func availabilityOf(list []availability) availability {
	all := isAvailable
	for _, a := range list {
		switch a {
		case unknownAvailability:
			return unknownAvailability
		case notAvailable:
			all = notAvailable
		}
	}
	return all
}

// typeListAvailability answers a whole vc:typeAvailable or vc:typeUnavailable
// list.
func typeListAvailability(el *xdm.Node, list string, version Version) availability {
	words := splitFields(list)
	if len(words) == 0 {
		return isAvailable
	}
	out := make([]availability, 0, len(words))
	for _, word := range words {
		out = append(out, typeAvailable(el, word, version))
	}
	return availabilityOf(out)
}

// availableIn10 reports whether a built-in type name existed in XSD 1.0.
//
// The five 1.1 additions are the point of vc:typeAvailable: a schema uses it to
// offer a 1.0 processor a different declaration where it would otherwise refer
// to a type that processor has never heard of.
func availableIn10(local string) bool {
	switch local {
	case "dateTimeStamp", "yearMonthDuration", "dayTimeDuration",
		"anyAtomicType", "error":
		return false
	}
	return true
}

// facetAvailable answers whether one named facet is one this processor
// implements, with the same three-way result as typeAvailable.
func facetAvailable(el *xdm.Node, word string, version Version) availability {
	name, ok := resolveVersioningQName(el, word)
	if !ok || name.URI != NSSchema {
		return unknownAvailability
	}
	if !knownFacet(name.Local) {
		return notAvailable
	}
	if version < Version11 && !facetIn10(name.Local) {
		return notAvailable
	}
	return isAvailable
}

// facetListAvailability answers a whole vc:facetAvailable or
// vc:facetUnavailable list.
func facetListAvailability(el *xdm.Node, list string, version Version) availability {
	words := splitFields(list)
	if len(words) == 0 {
		return isAvailable
	}
	out := make([]availability, 0, len(words))
	for _, word := range words {
		out = append(out, facetAvailable(el, word, version))
	}
	return availabilityOf(out)
}

// knownFacet reports whether a local name is a constraining facet this
// implementation applies.
func knownFacet(local string) bool {
	switch local {
	case "length", "minLength", "maxLength", "pattern", "enumeration",
		"whiteSpace", "maxInclusive", "maxExclusive", "minInclusive",
		"minExclusive", "totalDigits", "fractionDigits",
		"assertion", "explicitTimezone":
		return true
	}
	return false
}

// facetIn10 reports whether a facet existed in XSD 1.0.
func facetIn10(local string) bool {
	switch local {
	case "assertion", "explicitTimezone":
		return false
	}
	return true
}

// resolveVersioningQName resolves a QName in a versioning attribute.
//
// An unprefixed name is in the absent namespace, which is no built-in, so such
// a name never names a type this processor implements.
func resolveVersioningQName(el *xdm.Node, value string) (xdm.QName, bool) {
	prefix, local := "", value
	if i := strings.IndexByte(value, ':'); i >= 0 {
		prefix, local = value[:i], value[i+1:]
	}
	if local == "" {
		return xdm.QName{}, false
	}
	if prefix == "" {
		return xdm.QName{Local: local}, true
	}
	uri, ok := el.LookupPrefix(prefix)
	if !ok {
		return xdm.QName{}, false
	}
	return xdm.QName{URI: uri, Local: local}, true
}




