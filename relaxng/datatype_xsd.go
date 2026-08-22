package relaxng

import (
	"fmt"
	"strings"

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
