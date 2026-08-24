package xdm

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// TypeCode identifies an atomic type from the XML Schema built-in hierarchy.
//
// Only the types XPath 2.0 gives special treatment are enumerated. The rest of
// the schema hierarchy (xs:token, xs:NMTOKEN, and the other string subtypes)
// behaves identically to its base type for every operation this engine
// performs, so carrying them as distinct codes would add branches with no
// behavioural difference.
type TypeCode int

const (
	// TypeUntypedAtomic is the type of atomised nodes in a schemaless
	// document. It is the reason XPath 2.0 needs so few explicit casts: an
	// untypedAtomic operand is converted to the required type at the point of
	// use, but *only* in the specific contexts the spec lists.
	TypeUntypedAtomic TypeCode = iota
	TypeString
	TypeBoolean
	TypeDecimal
	TypeInteger
	TypeDouble
	TypeFloat
	TypeQName
	TypeAnyURI
	TypeDate
	TypeTime
	TypeDateTime
	TypeDuration
	TypeYearMonthDuration
	TypeDayTimeDuration
	TypeHexBinary
	TypeBase64Binary
	// The five Gregorian types denote a recurring or partial calendar point:
	// a year, a year and month, a month, a month and day, or a day.
	TypeGYear
	TypeGYearMonth
	TypeGMonth
	TypeGMonthDay
	TypeGDay
)

var typeNames = map[TypeCode]string{
	TypeUntypedAtomic:     "xs:untypedAtomic",
	TypeString:            "xs:string",
	TypeBoolean:           "xs:boolean",
	TypeDecimal:           "xs:decimal",
	TypeInteger:           "xs:integer",
	TypeDouble:            "xs:double",
	TypeFloat:             "xs:float",
	TypeQName:             "xs:QName",
	TypeAnyURI:            "xs:anyURI",
	TypeDate:              "xs:date",
	TypeTime:              "xs:time",
	TypeDateTime:          "xs:dateTime",
	TypeDuration:          "xs:duration",
	TypeYearMonthDuration: "xs:yearMonthDuration",
	TypeDayTimeDuration:   "xs:dayTimeDuration",
	TypeHexBinary:         "xs:hexBinary",
	TypeBase64Binary:      "xs:base64Binary",
	TypeGYear:             "xs:gYear",
	TypeGYearMonth:        "xs:gYearMonth",
	TypeGMonth:            "xs:gMonth",
	TypeGMonthDay:         "xs:gMonthDay",
	TypeGDay:              "xs:gDay",
}

func (t TypeCode) String() string {
	if n, ok := typeNames[t]; ok {
		return n
	}
	return "xs:anyAtomicType"
}

// IsNumeric reports whether t is one of the four numeric types. Numeric
// operands are promoted to a common type before arithmetic and comparison,
// which is what NumericPromote implements.
func (t TypeCode) IsNumeric() bool {
	switch t {
	case TypeDecimal, TypeInteger, TypeDouble, TypeFloat:
		return true
	}
	return false
}

// Atomic is a typed atomic value.
//
// The representation is a tagged union rather than an interface per type. The
// evaluator switches on Type constantly — every arithmetic op, comparison and
// function call — and a type switch across seventeen concrete types in those
// hot paths costs more than a single integer compare. It also keeps the
// numeric tower in one place, where the promotion rules are easy to audit.
type Atomic struct {
	Type TypeCode

	// str holds the value for string-like types (string, untypedAtomic,
	// anyURI, QName lexical form, binary types) and the canonical lexical
	// form for date/time/duration types.
	str string
	// num holds double and float values.
	num float64
	// dec holds decimal and integer values exactly. XPath 2.0 requires
	// xs:decimal arithmetic to be exact, so a float64 is not a legal
	// representation: 0.1 + 0.2 must equal 0.3 for xs:decimal operands.
	dec *big.Rat
	// b holds boolean values.
	b bool
	// qn holds the resolved QName for TypeQName.
	qn *QName
	// dur holds the parsed value for the duration types.
	dur *Duration
	// dt holds the parsed value for date/time types.
	dt *DateTime
	// derived names the XML Schema type this value was constructed as, when
	// that is narrower than Type — "int" for xs:int(0), which is stored as an
	// xs:integer because arithmetic and comparison only care about the
	// primitive.
	//
	// It exists for "instance of", which is the one place the distinction is
	// observable: xs:int(0) instance of xs:int is true, while the plain
	// literal 1 instance of xs:int is false, and both values are otherwise
	// identical. It is empty for every value that is not built by a derived
	// constructor, so the common path pays only the field.
	derived string
	// derivedMember names the member type of a union that actually accepted
	// this value, when derived names a union simple type (or a complex type
	// whose simple content is one).
	//
	// A union's members are siblings, not ancestors, so the upward derivation
	// walk that answers "instance of" for derived cannot reach them: nothing
	// links xs:integer to my:partIntegerUnion in the direction the walk runs.
	// Nor can the member simply replace derived, because a value of a union
	// type is an instance of the union as well — both names are true of the
	// same value at once, and the value is the only place that pairing is
	// known, member selection being a per-value fact rather than a per-type
	// one.
	//
	// Empty for every value not validated against a union.
	derivedMember string
}

// Derived returns the narrower XML Schema type this value was constructed as,
// or "" if it was not built by a derived-type constructor.
func (a *Atomic) Derived() string { return a.derived }

// DerivedMember returns the union member type this value was validated as, or
// "" when the value's type is not a union.
//
// It is a second answer alongside Derived, not a replacement for it: a value
// of a union type is an instance of both the union and the selected member.
func (a *Atomic) DerivedMember() string { return a.derivedMember }

// WithDerived returns a copy of a annotated as the named derived type.
//
// The union member is cleared: re-annotating the value as a different type
// makes any member recorded for the previous one meaningless, and carrying it
// forward would let a value claim membership in a union it no longer has.
func (a *Atomic) WithDerived(name string) *Atomic {
	c := *a
	c.derived = name
	c.derivedMember = ""
	return &c
}

// WithDerivedUnion returns a copy of a annotated as the named union type with
// the named member recorded as the one that accepted it.
func (a *Atomic) WithDerivedUnion(name, member string) *Atomic {
	c := *a
	c.derived = name
	c.derivedMember = member
	return &c
}

func (a *Atomic) isItem() {}

// TypeName implements Item.
func (a *Atomic) TypeName() string { return a.Type.String() }

// --- Constructors -----------------------------------------------------------

// NewString returns an xs:string.
func NewString(s string) *Atomic { return &Atomic{Type: TypeString, str: s} }

// NewUntypedAtomic returns an xs:untypedAtomic, the type produced by atomising
// a node in a document that has not been schema-validated.
func NewUntypedAtomic(s string) *Atomic { return &Atomic{Type: TypeUntypedAtomic, str: s} }

// NewAnyURI returns an xs:anyURI.
func NewAnyURI(s string) *Atomic { return &Atomic{Type: TypeAnyURI, str: s} }

// NewBinary returns an xs:hexBinary or xs:base64Binary holding the given
// lexical form.
//
// The value keeps its own type rather than collapsing to xs:string, because
// the two binary types are inter-convertible: casting hexBinary to
// base64Binary has to re-encode the underlying octets, and a value that has
// forgotten which encoding its lexical form uses cannot be decoded.
func NewBinary(s string, t TypeCode) *Atomic { return &Atomic{Type: t, str: s} }

// NewBoolean returns an xs:boolean.
func NewBoolean(v bool) *Atomic { return &Atomic{Type: TypeBoolean, b: v} }

// NewInteger returns an xs:integer. Integers are held as exact rationals so
// that they participate in decimal arithmetic without precision loss.
func NewInteger(v int64) *Atomic {
	return &Atomic{Type: TypeInteger, dec: new(big.Rat).SetInt64(v)}
}

// NewIntegerFromRat returns an xs:integer from an exact rational, which must
// have denominator 1. Used by arithmetic that has already established
// integrality (idiv, string-length, count).
func NewIntegerFromRat(r *big.Rat) *Atomic {
	return &Atomic{Type: TypeInteger, dec: new(big.Rat).Set(r)}
}

// NewDecimal returns an xs:decimal holding an exact value.
func NewDecimal(r *big.Rat) *Atomic {
	return &Atomic{Type: TypeDecimal, dec: new(big.Rat).Set(r)}
}

// NewDouble returns an xs:double.
func NewDouble(v float64) *Atomic { return &Atomic{Type: TypeDouble, num: v} }

// NewFloat returns an xs:float. The value is rounded to float32 precision on
// construction, because xs:float operations must produce float32 results.
func NewFloat(v float64) *Atomic {
	return &Atomic{Type: TypeFloat, num: float64(float32(v))}
}

// NewQNameValue returns an xs:QName.
func NewQNameValue(q QName) *Atomic {
	return &Atomic{Type: TypeQName, qn: &q, str: q.Lexical()}
}

// --- Accessors --------------------------------------------------------------

// Str returns the lexical/string content for string-like and date-like types.
func (a *Atomic) Str() string { return a.str }

// Bool returns the boolean value. Valid only for TypeBoolean.
func (a *Atomic) Bool() bool { return a.b }

// Rat returns the exact value for integer and decimal types, or nil.
func (a *Atomic) Rat() *big.Rat { return a.dec }

// QName returns the QName value, or nil.
func (a *Atomic) QName() *QName { return a.qn }

// Duration returns the duration value, or nil.
func (a *Atomic) DurationVal() *Duration { return a.dur }

// DateTimeVal returns the date/time value, or nil.
func (a *Atomic) DateTimeVal() *DateTime { return a.dt }

// Float64 returns the value as a float64 for any numeric type. Decimal and
// integer values are converted, which may lose precision; callers doing exact
// arithmetic must use Rat instead.
func (a *Atomic) Float64() float64 {
	switch a.Type {
	case TypeDouble, TypeFloat:
		return a.num
	case TypeInteger, TypeDecimal:
		if a.dec == nil {
			return 0
		}
		f, _ := a.dec.Float64()
		return f
	}
	return math.NaN()
}

// FitsInt64 reports whether the value can be represented as an int64 without
// wrapping.
//
// xs:integer is arbitrary-precision, so this is a real question: Int64()
// truncates the big.Int and silently returns a different number, which is
// worse than refusing.
func (a *Atomic) FitsInt64() bool {
	switch a.Type {
	case TypeInteger, TypeDecimal:
		if a.dec == nil {
			return true
		}
		q := new(big.Int).Quo(a.dec.Num(), a.dec.Denom())
		return q.IsInt64()
	case TypeDouble, TypeFloat:
		f := a.num
		return !math.IsNaN(f) && !math.IsInf(f, 0) &&
			f >= -9.223372036854776e18 && f <= 9.223372036854775e18
	}
	return true
}

// Int64 returns the value truncated to an int64. Valid for numeric types.
func (a *Atomic) Int64() int64 {
	switch a.Type {
	case TypeInteger, TypeDecimal:
		if a.dec == nil {
			return 0
		}
		q := new(big.Int).Quo(a.dec.Num(), a.dec.Denom())
		return q.Int64()
	case TypeDouble, TypeFloat:
		if math.IsNaN(a.num) || math.IsInf(a.num, 0) {
			return 0
		}
		return int64(math.Trunc(a.num))
	}
	return 0
}

// IsNaN reports whether a is a double or float NaN. NaN needs its own check
// throughout comparison, because it is the one value where the general
// "compare and negate" shortcut produces wrong answers.
func (a *Atomic) IsNaN() bool {
	return (a.Type == TypeDouble || a.Type == TypeFloat) && math.IsNaN(a.num)
}

// --- Lexical forms ----------------------------------------------------------

// String returns the XPath 2.0 canonical lexical representation, which is what
// fn:string and every implicit string conversion must produce. It is not a
// debug format: the exact spelling of doubles and decimals here is
// observable in stylesheet output.
func (a *Atomic) String() string {
	switch a.Type {
	case TypeString, TypeUntypedAtomic, TypeAnyURI, TypeQName,
		TypeHexBinary, TypeBase64Binary:
		return a.str
	case TypeBoolean:
		if a.b {
			return "true"
		}
		return "false"
	case TypeInteger:
		if a.dec == nil {
			return "0"
		}
		return new(big.Int).Quo(a.dec.Num(), a.dec.Denom()).String()
	case TypeDecimal:
		return formatDecimal(a.dec)
	case TypeDouble, TypeFloat:
		return formatDouble(a.num, a.Type == TypeFloat)
	case TypeDate, TypeTime, TypeDateTime:
		if a.dt != nil {
			return a.dt.Lexical(a.Type)
		}
		return a.str
	case TypeGYear, TypeGYearMonth, TypeGMonth, TypeGMonthDay, TypeGDay:
		if a.dt != nil {
			return LexicalGregorian(a.dt, a.Type)
		}
		return a.str
	case TypeDuration, TypeYearMonthDuration, TypeDayTimeDuration:
		if a.dur != nil {
			return a.dur.Lexical(a.Type)
		}
		return a.str
	}
	return a.str
}

// formatDecimal renders an xs:decimal without an exponent and without trailing
// zeros, which is what the canonical form requires. big.Rat.FloatString needs
// a fixed scale, so the scale is derived from the denominator and the result
// trimmed.
func formatDecimal(r *big.Rat) string {
	if r == nil {
		return "0"
	}
	if r.IsInt() {
		return r.Num().String()
	}
	// A denominator of 2^a*5^b terminates in at most max(a,b) digits; anything
	// else (which cannot arise from a valid xs:decimal literal, but can arise
	// from division) is rendered at 18 digits and trimmed.
	scale := decimalScale(r)
	s := r.FloatString(scale)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// maxDecimalScale bounds how many fractional digits are rendered for a value
// whose denominator terminates.
//
// A terminating decimal is rendered in full up to this, so the lexical form
// says what the value is. The bound exists only so that a rational built by
// repeated division cannot make formatting allocate without limit; a value
// needing more digits than this is rendered at the old 18 and is
// indistinguishable from any other approximation, which is what the previous
// unconditional cap made *every* long decimal.
const maxDecimalScale = 1024

// decimalScale returns the number of fractional digits needed to render r
// exactly, or 18 — the minimum precision XPath 2.0 requires an implementation
// to support — when r does not terminate within maxDecimalScale digits.
//
// Capping unconditionally at 18 made the lexical form disagree with the value:
// a literal with 360 fractional digits printed as "0" while comparing unequal
// to zero, so "0.000…1 eq 0" was false and "string(0.000…1)" was "0". The two
// answers cannot both be right, and the value is the one that must not move.
func decimalScale(r *big.Rat) int {
	d := new(big.Int).Set(r.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	zero, rem := new(big.Int), new(big.Int)
	a, b := 0, 0
	for {
		q, m := new(big.Int).QuoRem(d, two, rem)
		if m.Cmp(zero) != 0 {
			break
		}
		d = q
		a++
		if a > maxDecimalScale {
			break
		}
	}
	for {
		q, m := new(big.Int).QuoRem(d, five, rem)
		if m.Cmp(zero) != 0 {
			break
		}
		d = q
		b++
		if b > maxDecimalScale {
			break
		}
	}
	n := a
	if b > n {
		n = b
	}
	// A denominator with a factor other than 2 or 5 does not terminate — 1/3
	// is the everyday case — and neither does one needing more digits than
	// the bound. Both are rendered at the required minimum precision.
	if d.Cmp(big.NewInt(1)) != 0 || n > maxDecimalScale {
		return 18
	}
	if n == 0 {
		return 1
	}
	return n
}

// formatDouble renders xs:double and xs:float in the canonical form defined by
// XPath 2.0 fn:string: no exponent when the magnitude is in [1e-6, 1e6),
// scientific notation with a capital E outside that range, and the special
// spellings INF, -INF and NaN.
func formatDouble(v float64, isFloat bool) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "INF"
	case math.IsInf(v, -1):
		return "-INF"
	case v == 0:
		if math.Signbit(v) {
			return "-0"
		}
		return "0"
	}
	bitSize := 64
	if isFloat {
		bitSize = 32
	}
	abs := math.Abs(v)
	if abs >= 1e-6 && abs < 1e6 {
		s := strconv.FormatFloat(v, 'f', -1, bitSize)
		return s
	}
	// 'E' format gives Go's "1E+06"; XPath wants "1E6" with no plus and no
	// zero padding on the exponent.
	s := strconv.FormatFloat(v, 'E', -1, bitSize)
	mant, exp, ok := strings.Cut(s, "E")
	if !ok {
		return s
	}
	neg := strings.HasPrefix(exp, "-")
	exp = strings.TrimLeft(exp, "+-")
	exp = strings.TrimLeft(exp, "0")
	if exp == "" {
		exp = "0"
	}
	if neg {
		exp = "-" + exp
	}
	if !strings.Contains(mant, ".") {
		// Canonical form keeps a fractional part on the mantissa.
		mant += ".0"
	}
	return mant + "E" + exp
}

// --- Numeric promotion ------------------------------------------------------

// NumericPromote returns the common type for a binary numeric operation, per
// the XPath 2.0 promotion lattice: integer -> decimal -> float -> double.
// Both operands are converted to that type before the operation runs.
func NumericPromote(a, b TypeCode) TypeCode {
	rank := func(t TypeCode) int {
		switch t {
		case TypeInteger:
			return 0
		case TypeDecimal:
			return 1
		case TypeFloat:
			return 2
		case TypeDouble:
			return 3
		}
		return -1
	}
	ra, rb := rank(a), rank(b)
	if ra < 0 || rb < 0 {
		return TypeDouble
	}
	if ra > rb {
		return a
	}
	return b
}

// ErrType is the XPath type error, XPTY0004. It is returned rather than
// panicked so that a stylesheet error degrades one transform.
func ErrType(format string, args ...any) error {
	return Errorf("XPTY0004", format, args...)
}

// ErrCast is the XPath cast error, FORG0001.
func ErrCast(format string, args ...any) error {
	return Errorf("FORG0001", format, args...)
}

// NewDateTime returns a date, time or dateTime atomic value.
func NewDateTime(dt *DateTime, t TypeCode) *Atomic {
	return &Atomic{Type: t, dt: dt, str: dt.Lexical(t)}
}

// NewGregorian returns one of the five Gregorian atomic values.
func NewGregorian(dt *DateTime, t TypeCode) *Atomic {
	return &Atomic{Type: t, dt: dt, str: LexicalGregorian(dt, t)}
}

// NewDuration returns a duration atomic value of the given duration type.
func NewDuration(d *Duration, t TypeCode) *Atomic {
	return &Atomic{Type: t, dur: d, str: d.Lexical(t)}
}
