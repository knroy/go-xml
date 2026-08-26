package xpath

import (
	"math"
	"math/big"
	"strings"
	"sync"

	"github.com/knroy/go-xml/xdm"
)

// Builtins returns the standard fn: function library.
//
// The library is built once and shared: Function values hold no mutable state
// (everything they need comes from the Context passed at call time), so a
// single instance is safe for concurrent transforms. Rebuilding it per
// transform would cost several hundred map inserts for no benefit.
func Builtins() FunctionLibrary {
	builtinsOnce.Do(func() {
		l := NewLibrary(nil)
		registerStringFuncs(l)
		registerSeqFuncs(l)
		registerNumericFuncs(l)
		registerMathFuncs(l)
		registerSeq30Funcs(l)
		registerHOFuncs(l)
		registerFormatNumber(l)
		registerFormatInteger(l)
		registerPathFunc(l)
		registerParseXML(l, XPath30)
		registerAnalyzeString(l)
		registerUnparsedText(l, XPath30)
		registerFormatDateTimeSince(l, XPath30)
		registerNodeFuncs(l)
		registerRegexFuncs(l)
		registerContextFuncs(l)
		registerDateFuncs(l)
		registerTimezoneAdjust(l)
		registerCurrentDateTime(l)
		registerQNameFuncs(l)
		registerURIFuncs(l)
		registerMiscFuncs(l)
		registerConstructors(l)
		builtinLibrary = l
	})
	return builtinLibrary
}

var (
	builtinsOnce   sync.Once
	builtinLibrary FunctionLibrary
)

// registerConstructors adds the xs: constructor functions, which are spelled
// as function calls but are defined to behave exactly like "cast as".
//
// They exist because "xs:date($x)" reads better than "$x cast as xs:date" and
// because XSLT 1.0-era habits expect the call form. Implementing them by
// delegating to CastAtomic keeps one set of casting rules rather than two that
// can drift.
func registerConstructors(l *Library) {
	types := map[string]xdm.TypeCode{
		"string":            xdm.TypeString,
		"boolean":           xdm.TypeBoolean,
		"decimal":           xdm.TypeDecimal,
		"integer":           xdm.TypeInteger,
		"double":            xdm.TypeDouble,
		"float":             xdm.TypeFloat,
		"anyURI":            xdm.TypeAnyURI,
		"date":              xdm.TypeDate,
		"time":              xdm.TypeTime,
		"dateTime":          xdm.TypeDateTime,
		"duration":          xdm.TypeDuration,
		"yearMonthDuration": xdm.TypeYearMonthDuration,
		"dayTimeDuration":   xdm.TypeDayTimeDuration,
		"untypedAtomic":     xdm.TypeUntypedAtomic,
		"gYear":             xdm.TypeGYear,
		"gYearMonth":        xdm.TypeGYearMonth,
		"gMonth":            xdm.TypeGMonth,
		"gMonthDay":         xdm.TypeGMonthDay,
		"gDay":              xdm.TypeGDay,
		"hexBinary":         xdm.TypeHexBinary,
		"base64Binary":      xdm.TypeBase64Binary,
	}

	// The string subtypes are not plain aliases for xs:string: each applies a
	// whitespace facet, a pattern facet, or both. xs:normalizedString replaces
	// tab, CR and LF with spaces; xs:token additionally collapses runs of
	// spaces and trims; the name-like types then require the result to match
	// a production. Mapping them all onto xs:string made every one of them a
	// no-op, so xs:token("a\tb") kept its tab.
	//
	// The *value* is still an xs:string, since this engine does not carry the
	// subtype lattice in its type codes. What the constructor must not do is
	// skip the facets, because those change the value itself.
	registerStringSubtypes(l)
	registerIntegerSubtypes(l)

	for name, code := range types {
		target := code
		l.register(xdm.NSXS, name, 1, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			atoms := xdm.Atomize(args[0])
			// A constructor applied to the empty sequence yields the empty
			// sequence rather than raising, so "xs:date($optional)" is safe.
			if len(atoms) == 0 {
				return xdm.Empty(), nil
			}
			it, err := atoms.Single()
			if err != nil {
				return nil, err
			}
			out, err := CastAtomic(it.(*xdm.Atomic), target)
			if err != nil {
				return nil, err
			}
			return xdm.One(out), nil
		})
	}
}

// registerStringSubtypes adds the xs: constructors derived from xs:string.
func registerStringSubtypes(l *Library) {
	// collapse is the xs:token whitespace facet; replace is xs:normalizedString's.
	replace := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == '\t' || r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, s)
	}
	// collapseXMLSpace rather than strings.Fields: XML whitespace is exactly
	// space, tab, carriage return and newline, where unicode.IsSpace is a
	// wider set that swallows U+00A0.
	collapse := collapseXMLSpace

	type subtype struct {
		name  string
		norm  func(string) string
		check func(string) bool
	}
	subtypes := []subtype{
		{"normalizedString", replace, nil},
		{"token", collapse, nil},
		// xs:language is a BCP 47 primary tag plus optional subtags.
		{"language", collapse, isLanguageTag},
		{"Name", collapse, isXMLName},
		{"NCName", collapse, isNCName},
		{"ID", collapse, isNCName},
		{"IDREF", collapse, isNCName},
		{"ENTITY", collapse, isNCName},
		{"NMTOKEN", collapse, isNmtoken},
	}

	for _, st := range subtypes {
		norm, check, name := st.norm, st.check, st.name
		l.register(xdm.NSXS, name, 1, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			atoms := xdm.Atomize(args[0])
			if len(atoms) == 0 {
				return xdm.Empty(), nil
			}
			it, err := atoms.Single()
			if err != nil {
				return nil, err
			}
			a := it.(*xdm.Atomic)
			// These are subtypes of xs:string, so anything that casts to
			// xs:string casts to them: xs:token(1) is the token "1". Requiring
			// a string-like source refused every value that would have had to
			// go through its string form, which is most of them. The general
			// gate below still applies — a QName does not cast to a string,
			// and neither does a duration.
			if err := castPermitted(a.Type, xdm.TypeString); err != nil {
				return nil, xdm.ErrCast("cannot cast %s to xs:%s", a.TypeName(), name)
			}
			v := norm(a.String())
			if check != nil && !check(v) {
				return nil, xdm.ErrCast("invalid xs:%s %q", name, a.String())
			}
			return xdm.One(xdm.NewString(v).WithDerived(name)), nil
		})
	}
}

// isXMLName reports whether s matches the XML Name production, which is an
// NCName that may also contain colons.
func isXMLName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == ':' {
			continue
		}
		if i == 0 {
			if !isNameStartRune(r) {
				return false
			}
			continue
		}
		if !isNameRune(r) {
			return false
		}
	}
	return true
}

// isNmtoken is a Name without the restriction on the first character.
func isNmtoken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != ':' && !isNameRune(r) {
			return false
		}
	}
	return true
}

// isLanguageTag matches the xs:language pattern: an alphabetic primary tag of
// up to 8 characters, then any number of alphanumeric subtags.
func isLanguageTag(s string) bool {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if p == "" || len(p) > 8 {
			return false
		}
		for _, r := range p {
			isAlpha := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
			isDigit := r >= '0' && r <= '9'
			if i == 0 && !isAlpha {
				return false
			}
			if i > 0 && !isAlpha && !isDigit {
				return false
			}
		}
	}
	return len(parts) > 0
}

// registerIntegerSubtypes adds the xs: constructors derived from xs:integer.
//
// Each carries a range facet, and that range is what makes them different from
// their base type: xs:byte(128) is not a byte. Mapping them all onto
// xs:integer dropped the bound, so every one accepted any value and
// "128 castable as xs:byte" answered true.
//
// The resulting value is still an xs:integer, since this engine does not carry
// the subtype lattice in its type codes; what the constructor must not skip is
// the range check.
func registerIntegerSubtypes(l *Library) {
	// A nil bound means unbounded in that direction.
	i := func(v int64) *big.Int { return big.NewInt(v) }
	unsignedLongMax, _ := new(big.Int).SetString("18446744073709551615", 10)

	type rangeFacet struct {
		name     string
		min, max *big.Int
	}
	for _, f := range []rangeFacet{
		{"long", i(math.MinInt64), i(math.MaxInt64)},
		{"int", i(math.MinInt32), i(math.MaxInt32)},
		{"short", i(-32768), i(32767)},
		{"byte", i(-128), i(127)},
		{"unsignedLong", i(0), unsignedLongMax},
		{"unsignedInt", i(0), i(4294967295)},
		{"unsignedShort", i(0), i(65535)},
		{"unsignedByte", i(0), i(255)},
		{"nonNegativeInteger", i(0), nil},
		{"positiveInteger", i(1), nil},
		{"nonPositiveInteger", nil, i(0)},
		{"negativeInteger", nil, i(-1)},
	} {
		f := f
		l.register(xdm.NSXS, f.name, 1, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			atoms := xdm.Atomize(args[0])
			if len(atoms) == 0 {
				return xdm.Empty(), nil
			}
			it, err := atoms.Single()
			if err != nil {
				return nil, err
			}
			v, err := CastAtomic(it.(*xdm.Atomic), xdm.TypeInteger)
			if err != nil {
				return nil, err
			}
			// The bound is checked on the exact value rather than an int64
			// that could have wrapped on the way in.
			n := new(big.Int).Quo(v.Rat().Num(), v.Rat().Denom())
			if f.min != nil && n.Cmp(f.min) < 0 || f.max != nil && n.Cmp(f.max) > 0 {
				return nil, xdm.ErrCast("FORG0001: %s is out of range for xs:%s", n, f.name)
			}
			// The constructed value remembers the type it was built as, so
			// that "instance of" can tell it from a plain xs:integer.
			return xdm.One(v.WithDerived(f.name)), nil
		})
	}

	// xs:dateTimeStamp is xs:dateTime with explicitTimezone="required", so
	// the constructor casts to xs:dateTime and then insists on the
	// timezone the facet demands. A value without one is not in the type's
	// value space, which is FORG0001 rather than a type error — the same
	// shape as the out-of-range integer subtypes above.
	l.register(xdm.NSXS, "dateTimeStamp", 1, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		atoms := xdm.Atomize(args[0])
		if len(atoms) == 0 {
			return xdm.Empty(), nil
		}
		it, err := atoms.Single()
		if err != nil {
			return nil, err
		}
		v, err := CastAtomic(it.(*xdm.Atomic), xdm.TypeDateTime)
		if err != nil {
			return nil, err
		}
		if dt := v.DateTimeVal(); dt == nil || !dt.HasTZ {
			return nil, xdm.ErrCast(
				"FORG0001: xs:dateTimeStamp requires a timezone")
		}
		return xdm.One(v.WithDerived("dateTimeStamp")), nil
	})
}
