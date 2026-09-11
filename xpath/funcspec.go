//go:generate go run ../cmd/genfunctions -spec ../testdata/xslt30-test/specs/functions-and-operators-31.html -out spec/function-signatures.json

package xpath

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// This file adds the declared-signature layer the audit's finding 6 asks for.
//
// The finding is that Function carries Name, Arity, Call and Since but no
// parameter types, so nothing in the program can state what a parameter
// accepts. That is why twelve functions each took an empty sequence for a
// parameter F&O declares without "?" — fn:round silently substituting a
// precision of 0 was the worst of them — and why each needed its own hand fix
// in commit 7668773, with nothing to prevent a thirteenth.
//
// The plan asks for "a compact internal type language that represents atomic
// unions, node tests, item(), function(*), maps/arrays, occurrence
// indicators". That language already exists in this package: SequenceType in
// ast.go is exactly it, ParseSequenceType in host.go parses it from the
// spelling the specification writes, and subtype.go implements the subtype
// relation over it. Nothing here redefines any of that. What is added is the
// per-function manifest, the two cardinality questions a declared type
// answers, and the check that asks them at call binding.

// AllowsEmpty reports whether the declared type permits an empty sequence:
// true for "?" and "*", false for "" and "+".
//
// This one question is the whole of the twelve-function defect class. F&O 3.1
// 2.5.4 makes passing () to a parameter whose declared type carries no "?" or
// "*" a type error; twelve functions instead treated it as a default value,
// an empty result, or a silent zero.
//
// empty-sequence() is the type of which () is the only instance, so it
// permits an empty sequence whatever indicator it carries.
func (t SequenceType) AllowsEmpty() bool {
	return t.Empty || t.Occurrence == "?" || t.Occurrence == "*"
}

// AllowsMany reports whether the declared type permits more than one item.
func (t SequenceType) AllowsMany() bool {
	return t.Occurrence == "*" || t.Occurrence == "+"
}

// FunctionCall is the signature of a function implementation, named so that
// FunctionSpec and Function refer to one type rather than repeating it.
type FunctionCall = func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error)

// FunctionSpec is the declared conformance metadata for one (name, arity)
// entry of the function library.
//
// It is the type the plan names. Invoke sits beside the declared types rather
// than in a parallel table so that a signature and its implementation cannot
// drift apart, which is the failure mode a second table would reintroduce.
type FunctionSpec struct {
	Name   xdm.QName
	Arity  int
	Params []SequenceType
	Result SequenceType
	// Since is the first language version in which the function exists, with
	// the same meaning as Function.Since.
	Since Version
	// Extension marks an entry that is not an F&O function — a host-language
	// addition such as fn:stream-available, which XSLT 3.0 defines for
	// streaming and F&O does not define at all. Extensions are excluded from
	// F&O completeness counts and are the documented exception to manifest
	// coverage.
	Extension bool
	// Invoke is the implementation, with the signature of Function.Call.
	Invoke FunctionCall
}

// checkArgCardinality reports a type error when an argument's cardinality is
// one the declared parameter type forbids.
//
// This is the structural replacement for twelve hand-written empty-sequence
// guards. It is deliberately narrow, and the narrowness is the point — the
// plan forbids "blanket eager checks that change existing error precedence":
//
//   - It checks CARDINALITY only: empty where the declaration permits none,
//     and several where it permits at most one. It does not check item types.
//     An item-type check here would be exactly the eager check the plan
//     rules out, because the conversion rules of F&O 2.5.4 are per-signature
//     — atomization, numeric promotion and URI-to-string promotion all
//     intervene — so an argument that is not literally an instance of the
//     declared type is very often a legal call.
//
//   - It never fires for a parameter declared "?" or "*". fn:contains("a",())
//     is true and math:pow((),2) is (), because $arg2 and $x really are
//     declared with "?". The controls of commit 7668773 pin all four such
//     cases, and a check that raised there would be a regression.
//
//   - It runs only where a spec exists. A function with no manifest entry is
//     bound exactly as before, so adding entries is a pure narrowing: it can
//     only turn a wrongly-accepted call into the error the specification
//     requires, never the reverse.
//
// The error is XPTY0004, which is what F&O 2.5.4 requires and what each of
// the twelve hand fixes raised individually.
func checkArgCardinality(name xdm.QName, params []SequenceType, args []xdm.Sequence) error {
	if len(params) != len(args) {
		// Arity is settled by lookup before this point, so a mismatch here
		// means the manifest disagrees with the registration. Checking the
		// shorter prefix would hide that; decline instead, and let the
		// enforcement test be what reports it.
		return nil
	}
	for i, p := range params {
		switch {
		case len(args[i]) == 0 && !p.AllowsEmpty():
			return xdm.Errorf("XPTY0004",
				"an empty sequence is not allowed as the %s argument of %s(), "+
					"which is declared %s", ordinal(i+1), displayName(name), p)
		case len(args[i]) > 1 && !p.AllowsMany():
			return xdm.Errorf("XPTY0004",
				"a sequence of %d items is not allowed as the %s argument of "+
					"%s(), which is declared %s",
				len(args[i]), ordinal(i+1), displayName(name), p)
		}
	}
	return nil
}

// displayName spells a QName the way the specification and the existing
// hand-written messages do: "fn:substring", not the Clark form.
func displayName(n xdm.QName) string {
	if p, ok := namespacePrefixes[n.URI]; ok {
		return p + ":" + n.Local
	}
	return n.Clark()
}

// ordinal spells an argument position, matching the wording the twelve hand
// fixes of commit 7668773 used ("the second argument of fn:substring()").
func ordinal(n int) string {
	names := []string{"", "first", "second", "third", "fourth", "fifth",
		"sixth", "seventh", "eighth"}
	if n > 0 && n < len(names) {
		return names[n]
	}
	return fmt.Sprintf("%dth", n)
}

// namespacePrefixes and prefixNamespaces relate the manifest's prefixed names
// to the expanded names the library is keyed by. The manifest writes
// "fn:substring"; Library.Lookup takes a QName.
var (
	namespacePrefixes = map[string]string{
		xdm.NSFN:    "fn",
		xdm.NSMath:  "math",
		xdm.NSMap:   "map",
		xdm.NSArray: "array",
	}
	prefixNamespaces = map[string]string{
		"fn":    xdm.NSFN,
		"math":  xdm.NSMath,
		"map":   xdm.NSMap,
		"array": xdm.NSArray,
	}
)

// parseSpecName expands a manifest name such as "fn:substring".
func parseSpecName(s string) (xdm.QName, bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return xdm.QName{}, false
	}
	uri, ok := prefixNamespaces[s[:i]]
	if !ok {
		return xdm.QName{}, false
	}
	return xdm.QName{URI: uri, Local: s[i+1:]}, true
}

// specKey keys the manifest the same way Library keys its functions, so the
// two can be compared without a second convention.
func specKey(name xdm.QName, arity int) string {
	return fmt.Sprintf("%s#%d", name.Clark(), arity)
}
