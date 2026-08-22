package relaxng

import (
	"fmt"
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

func xsdDatatype(name string) (Datatype, error) {
	if xsd.BuiltinType(name) == nil {
		return nil, fmt.Errorf(
			"the XML Schema datatype library has no type named %q", name)
	}
	return xsdType{name: name}, nil
}

func (t xsdType) check(value string, params []Param) error {
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
func (t xsdType) checkParams(value string, params []Param) error {
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

func atoiParam(p Param) (int, error) {
	n := 0
	s := strings.TrimSpace(p.Value)
	if s == "" {
		return 0, fmt.Errorf("parameter %s has no value", p.Name)
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("parameter %s = %q is not a number", p.Name, p.Value)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// equalIn compares two QNames by what they mean rather than how they are
// spelled.
//
// A QName is a prefix and a local name, and the prefix is only a pointer to a
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

// resolveQName splits a lexical QName and resolves its prefix.
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
func (t xsdType) checkBound(value string, p Param) error {
	v, ok := parseNumber(value)
	if !ok {
		return fmt.Errorf("%s applies to numbers, and %q is not one",
			p.Name, value)
	}
	b, ok := parseNumber(p.Value)
	if !ok {
		return fmt.Errorf("%s = %q is not a number", p.Name, p.Value)
	}
	switch p.Name {
	case "minInclusive":
		if v < b {
			return fmt.Errorf("%v is below minInclusive %v", v, b)
		}
	case "maxInclusive":
		if v > b {
			return fmt.Errorf("%v exceeds maxInclusive %v", v, b)
		}
	case "minExclusive":
		if v <= b {
			return fmt.Errorf("%v is not above minExclusive %v", v, b)
		}
	case "maxExclusive":
		if v >= b {
			return fmt.Errorf("%v is not below maxExclusive %v", v, b)
		}
	}
	return nil
}

// parseNumber reads a value as a float for comparison.
func parseNumber(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
