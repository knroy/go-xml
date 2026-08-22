package relaxng

import (
	"fmt"
	"strings"
)

// Datatype decides whether a string is a legal value, and when two values are
// equal.
//
// RELAX NG defines exactly two built-in types — string and token — and leaves
// everything else to a *datatype library* named by a URI. The two built-ins
// differ only in equality: string compares literally, token compares after
// whitespace normalisation, which is why the same document can be valid
// against one and not the other.
type Datatype interface {
	// check reports whether value is legal, given the parameters.
	check(value string, params []Param) error
	// equal reports whether two values are the same, which <value> needs and
	// <data> does not.
	equal(a, b string) bool
}

// The built-in library, which is the empty datatypeLibrary URI.
const builtinLibrary = ""

// xsdLibrary is the URI of the XML Schema datatype library.
const xsdLibrary = "http://www.w3.org/2001/XMLSchema-datatypes"

// stringType is the built-in "string": every string is legal, and equality is
// literal.
type stringType struct{}

func (stringType) check(string, []Param) error { return nil }
func (stringType) equal(a, b string) bool      { return a == b }

// tokenType is the built-in "token": every string is legal, and equality
// ignores leading, trailing and repeated whitespace.
//
// The normalisation is the whole point of the type. Two documents differing
// only in indentation compare equal, which is what makes token the right
// choice for a value written across lines.
type tokenType struct{}

func (tokenType) check(string, []Param) error { return nil }
func (tokenType) equal(a, b string) bool {
	return normalizeToken(a) == normalizeToken(b)
}

func normalizeToken(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// lookupDatatype resolves a library URI and type name to a datatype.
//
// An unknown library is an error rather than a fallback to string: a schema
// naming a library this does not implement asks for checks that would not
// happen, and silently accepting everything is the failure mode a validator
// exists to prevent.
func lookupDatatype(library, name string) (Datatype, error) {
	switch library {
	case builtinLibrary:
		switch name {
		case "string":
			return stringType{}, nil
		case "token":
			return tokenType{}, nil
		}
		return nil, fmt.Errorf(
			"the built-in datatype library has only \"string\" and \"token\", not %q",
			name)
	case xsdLibrary:
		return xsdDatatype(name)
	}
	return nil, fmt.Errorf("unknown datatype library %q", library)
}
