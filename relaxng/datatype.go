package relaxng

import (
	"fmt"
	"strings"
)

// datatype decides whether a string is a legal value, and when two values are
// equal.
//
// RELAX NG defines exactly two built-in types — string and token — and leaves
// everything else to a *datatype library* named by a URI. The two built-ins
// differ only in equality: string compares literally, token compares after
// whitespace normalisation, which is why the same document can be valid
// against one and not the other.
type datatype interface {
	// check reports whether value is legal, given the parameters.
	check(value string, params []param) error
	// equal reports whether two values are the same, which <value> needs and
	// <data> does not.
	equal(a, b string) bool
}

// nsContext is what a value needs to be understood beyond its own characters.
//
// Only QName and NOTATION need it, and they need it badly: "e:x" and "f:x"
// are the same value when the two prefixes are bound to the same namespace,
// and different values when they are not. Nothing about the strings says so.
type nsContext struct {
	// prefixes maps a prefix to a namespace.
	prefixes map[string]string
	// dflt is the namespace an unprefixed name takes.
	dflt string
}

// contextualType is a datatype whose values depend on namespace bindings.
type contextualType interface {
	// equalIn compares two values, each read in its own context.
	equalIn(a string, actx nsContext, b string, bctx nsContext) bool
}

// The built-in library, which is the empty datatypeLibrary URI.
const builtinLibrary = ""

// xsdLibrary is the URI of the XML Schema datatype library.
const xsdLibrary = "http://www.w3.org/2001/XMLSchema-datatypes"

// stringType is the built-in "string": every string is legal, and equality is
// literal.
type stringType struct{}

func (stringType) check(_ string, params []param) error { return noParams("string", params) }
func (stringType) equal(a, b string) bool               { return a == b }

// tokenType is the built-in "token": every string is legal, and equality
// ignores leading, trailing and repeated whitespace.
//
// The normalisation is the whole point of the type. Two documents differing
// only in indentation compare equal, which is what makes token the right
// choice for a value written across lines.
type tokenType struct{}

func (tokenType) check(_ string, params []param) error { return noParams("token", params) }
func (tokenType) equal(a, b string) bool {
	return normalizeToken(a) == normalizeToken(b)
}

// noParams refuses a <param> on a built-in type.
//
// §4.16: the built-in library defines no parameters, so a schema that gives
// one is asking for a check that cannot happen. Ignoring it would accept
// values the author meant to exclude — which is the failure a validator
// exists to prevent, arriving quietly.
func noParams(name string, params []param) error {
	if len(params) == 0 {
		return nil
	}
	return fmt.Errorf(
		"the built-in type %q takes no parameters, and %q was given",
		name, params[0].Name)
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
func lookupDatatype(library, name string) (datatype, error) {
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

// checkParams validates a <data>'s parameters against its type, without a
// value to check them on.
//
// It exists so that a definition nothing refers to is still checked: whether a
// parameter is meaningful for a type is a property of the schema, not of any
// document, and finding out only when a document happens to reach it is
// finding out too late.
func checkParams(dt datatype, library, name string, params []param) error {
	if len(params) == 0 {
		return nil
	}
	if library == builtinLibrary {
		return noParams(name, params)
	}
	// A library type's parameters are checked against a value, and there is
	// no value here. Leaving them is the honest choice: reporting a facet
	// violation for a value the schema never mentions would be wrong.
	//
	// Whether a parameter is *well formed* is a different question, and one
	// that does not need a value. A length written as a number too large to
	// represent names no bound at all, so the schema says something its author
	// cannot have meant; that is an error in the schema, not in a document.
	for _, p := range params {
		switch p.Name {
		case "length", "minLength", "maxLength":
			if _, err := atoiParam(p); err != nil {
				return err
			}
		}
	}
	_ = dt
	return nil
}
