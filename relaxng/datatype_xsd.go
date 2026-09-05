package relaxng

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// xsdType adapts an XML Schema built-in datatype to RELAX NG's interface.
//
// The lexical rules are xsd's rather than reimplemented here: a schema naming
// xs:decimal through the datatype library means exactly what it means in an
// XSD schema, and having two implementations of that would guarantee they
// diverge.
type xsdType struct{ name string }

func xsdDatatype(name string) (datatype, error) {
	if xsd.BuiltinType(name) == nil {
		return nil, fmt.Errorf(
			"the XML Schema datatype library has no type named %q", name)
	}
	return xsdType{name: name}, nil
}

func (t xsdType) check(value string, params []param) error {
	canon, err := xsd.CheckBuiltinValue(t.name, value)
	if err != nil {
		return err
	}
	_ = canon
	return t.checkParams(value, params)
}

// checkParams applies the facets a <param> may set.
//
// RELAX NG names them exactly as XSD's facets, which is what makes the mapping
// direct. Only the facets the conformance suite exercises are implemented; an
// unrecognised one is an error rather than ignored, because a parameter that
// is silently dropped is a constraint the caller believes is enforced.
func (t xsdType) checkParams(value string, params []param) error {
	for _, p := range params {
		switch p.Name {
		case "length", "minLength", "maxLength":
			n, err := atoiParam(p)
			if err != nil {
				return err
			}
			l := len([]rune(value))
			switch p.Name {
			case "length":
				if l != n {
					return fmt.Errorf("length is %d, not %d", l, n)
				}
			case "minLength":
				if l < n {
					return fmt.Errorf("length %d is below minLength %d", l, n)
				}
			case "maxLength":
				if l > n {
					return fmt.Errorf("length %d exceeds maxLength %d", l, n)
				}
			}
		case "pattern":
			re, err := xsd.CompilePattern(p.Value)
			if err != nil {
				return err
			}
			if !re.Matches(value) {
				return fmt.Errorf("value does not match pattern %q", p.Value)
			}
		case "minInclusive", "maxInclusive", "minExclusive", "maxExclusive":
			if err := t.checkBound(value, p); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported parameter %q", p.Name)
		}
	}
	return nil
}

// equal compares by canonical form, so that two lexically different spellings
// of the same value — "1.0" and "1.00" as xs:decimal — are equal.
func (t xsdType) equal(a, b string) bool {
	ca, err := xsd.CheckBuiltinValue(t.name, a)
	if err != nil {
		return false
	}
	cb, err := xsd.CheckBuiltinValue(t.name, b)
	if err != nil {
		return false
	}
	return ca == cb
}

// atoiParam reads a length parameter as a non-negative integer.
//
// A value too large to represent is an error rather than a bound that wraps.
// The wrapped bound is worse than no bound at all: a minLength that overflows
// to a negative int is a constraint no string can fail, so a parameter written
// to reject everything would accept everything instead.
func atoiParam(p param) (int, error) {
	s := strings.TrimSpace(p.Value)
	if s == "" {
		return 0, fmt.Errorf("parameter %s has no value", p.Name)
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("parameter %s = %q is not a number", p.Name, p.Value)
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parameter %s = %q is too large to represent",
			p.Name, p.Value)
	}
	return n, nil
}

// equalIn compares two QNames by what they mean rather than how they are
// spelled.
//
// A qnamePat is a prefix and a local name, and the prefix is only a pointer to a
// namespace. "e:x" written in a schema and "f:x" written in a document are the
// same value when e and f are bound to the same namespace, and different when
// they are not — which is why comparing the lexical forms gives the wrong
// answer in both directions.
func (t xsdType) equalIn(a string, actx nsContext, b string, bctx nsContext) bool {
	if t.name != "QName" && t.name != "NOTATION" {
		return t.equal(a, b)
	}
	an, aok := resolveQName(strings.TrimSpace(a), actx)
	bn, bok := resolveQName(strings.TrimSpace(b), bctx)
	return aok && bok && an == bn
}

// resolveQName splits a lexical qnamePat and resolves its prefix.
//
// An unresolvable prefix is not an error here but a mismatch: the value simply
// does not name anything, so it equals nothing.
func resolveQName(s string, ctx nsContext) (xdm.QName, bool) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		prefix, local := s[:i], s[i+1:]
		uri, ok := ctx.prefixes[prefix]
		if !ok {
			if prefix == "xml" {
				uri = xdm.NSXML
			} else {
				return xdm.QName{}, false
			}
		}
		return xdm.QName{URI: uri, Local: local}, true
	}
	return xdm.QName{URI: ctx.dflt, Local: s}, true
}

// checkBound applies one of the four ordering facets.
//
// The comparison is numeric, not lexical: "0.9" and "0.90" are the same value
// and both below 1, which string ordering gets wrong in a way that would
// accept and reject arbitrary values. Only the numeric types are ordered this
// way, and a bound on anything else is refused rather than guessed at.
func (t xsdType) checkBound(value string, p param) error {
	v, ok := parseNumber(value)
	if !ok {
		return fmt.Errorf("%s applies to numbers, and %q is not one",
			p.Name, value)
	}
	b, ok := parseNumber(p.Value)
	if !ok {
		return fmt.Errorf("%s = %q is not a number", p.Name, p.Value)
	}
	c, ok := compareNumbers(v, b)
	if !ok {
		// One side is NaN, which is unordered against everything. A bound
		// cannot be met by a value that compares to nothing.
		return fmt.Errorf("%s: %q is not comparable to %q",
			p.Name, value, p.Value)
	}
	switch p.Name {
	case "minInclusive":
		if c < 0 {
			return fmt.Errorf("%s is below minInclusive %s", value, p.Value)
		}
	case "maxInclusive":
		if c > 0 {
			return fmt.Errorf("%s exceeds maxInclusive %s", value, p.Value)
		}
	case "minExclusive":
		if c <= 0 {
			return fmt.Errorf("%s is not above minExclusive %s", value, p.Value)
		}
	case "maxExclusive":
		if c >= 0 {
			return fmt.Errorf("%s is not below maxExclusive %s", value, p.Value)
		}
	}
	return nil
}

// number is a parsed numeric value held exactly wherever it can be.
//
// xs:integer and xs:decimal have arbitrary precision, so routing them through
// float64 makes consecutive values above 2^53 compare equal and admits values
// the bound was written to exclude. rat holds those exactly. INF and NaN have
// no rational form and reach comparison only from xs:float and xs:double,
// where they are genuinely float64 values, so they are carried as such.
type number struct {
	rat     *big.Rat
	special float64 // valid only when rat == nil
}

// parseNumber reads a value for comparison, exactly where the lexical form
// permits it.
func parseNumber(s string) (number, bool) {
	s = strings.TrimSpace(s)
	if r, ok := new(big.Rat).SetString(s); ok {
		// SetString also accepts forms big.Rat means but XSD does not, such
		// as "1/2"; those never reach here, because the lexical check has
		// already passed the value against its datatype.
		if !strings.ContainsAny(s, "/") {
			return number{rat: r}, true
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return number{}, false
	}
	return number{special: f}, true
}

// compareNumbers orders two values, reporting false when they are unordered
// — which happens only when one of them is NaN.
func compareNumbers(a, b number) (int, bool) {
	if a.rat != nil && b.rat != nil {
		return a.rat.Cmp(b.rat), true
	}
	af, aok := a.float()
	bf, bok := b.float()
	if !aok || !bok || math.IsNaN(af) || math.IsNaN(bf) {
		return 0, false
	}
	switch {
	case af < bf:
		return -1, true
	case af > bf:
		return 1, true
	}
	return 0, true
}

// float returns the value as a float64. An exact value converts; the
// conversion is only ever used against an infinity, where the rounding cannot
// change the answer.
func (n number) float() (float64, bool) {
	if n.rat == nil {
		return n.special, true
	}
	f, _ := n.rat.Float64()
	return f, true
}
