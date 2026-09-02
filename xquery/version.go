package xquery

import "github.com/knroy/go-xml/xpath"

// XQVersion is the version of the XQuery language a module is written in, as
// named by its version declaration (§4.1 VersionDecl).
//
// This is not the same thing as xpath.Version, and the two cannot be collapsed
// into one. xpath.Version selects a version of the *expression* language, and
// XQuery's expression language is XPath's: XQuery 1.0's is XPath 2.0's, 3.0's
// is XPath 3.0's, 3.1's is XPath 3.1's. But XQuery has rules of its own that
// XPath has no counterpart for -- the prolog, the module system, node
// construction -- and those changed on XQuery's own schedule. An unprefixed
// "declare option" name is the clearest case: it is XPST0081 in XQuery 1.0 and
// legal from 3.0, and XPath has no option declaration at all, so no value of
// xpath.Version can carry that distinction. Keeping the two types separate is
// what lets a decision point say which language's rule it is applying.
//
// The constants are in version order, so that "later than" is a comparison
// and the predicates below are the only place that knows which comparison.
// The zero value is therefore XQuery10 and is never what a module gets:
// newStaticContext sets the default explicitly, because the default is a
// policy decision about undeclared modules and not an accident of ordering.
type XQVersion int

const (
	// XQuery10 is XQuery 1.0, the 2007 Recommendation.
	XQuery10 XQVersion = iota
	// XQuery30 is XQuery 3.0, the 2014 Recommendation. It admits everything
	// in 1.0, and changes the answer to a handful of questions 1.0 had
	// already answered -- see the callers of atLeast30.
	XQuery30
	// XQuery31 is XQuery 3.1, the 2017 Recommendation. It is what this engine
	// implements, and what a module with no version declaration is compiled
	// as: §4.1 leaves the version of such a module implementation-defined.
	XQuery31
)

// defaultXQVersion is the version a module with no version declaration is
// compiled as.
//
// §4.1 makes a version declaration optional and does not say what a module
// without one is. This engine implements 3.1 throughout, so a module that
// declares nothing is judged by 3.1's rules -- which is also what every
// conformance suite run against this engine assumes.
const defaultXQVersion = XQuery31

// atLeast30 reports whether v admits the rules XQuery 3.0 introduced.
//
// Written as a method rather than a bare ">= XQuery30" at each use so that
// adding a later version does not mean auditing every comparison for whether
// it meant "exactly 3.0" or "3.0 and later" -- the same reasoning as
// xpath.Version.atLeast30.
func (v XQVersion) atLeast30() bool { return v >= XQuery30 }

// atLeast31 reports whether v admits the rules XQuery 3.1 introduced, on the
// same reasoning as atLeast30.
func (v XQVersion) atLeast31() bool { return v >= XQuery31 }

// String gives the version back in the spelling a version declaration uses,
// so an error message can quote what the module asked for.
func (v XQVersion) String() string {
	switch v {
	case XQuery10:
		return "1.0"
	case XQuery30:
		return "3.0"
	}
	return "3.1"
}

// xpathVersion is the version of the expression language this XQuery version
// is defined over.
//
// Each XQuery Recommendation is written as an extension of the XPath of the
// same generation: XQuery 1.0 over XPath 2.0, XQuery 3.0 over XPath 3.0,
// XQuery 3.1 over XPath 3.1. Every expression in a module is handed to the
// xpath package as a substring, and this is the version that package needs in
// order to judge it.
func (v XQVersion) xpathVersion() xpath.Version {
	switch v {
	case XQuery10:
		return xpath.XPath20
	case XQuery30:
		return xpath.XPath30
	}
	return xpath.XPath31
}

// parseXQVersion maps a version declaration's literal onto an XQVersion,
// reporting whether this processor implements it.
//
// A literal this processor does not implement is XQST0031 -- "the version
// number specified in a version declaration is not supported by the
// implementation" -- which is the specification's way of saying "this query
// was written for a language I do not have" rather than a syntax error. The
// error is raised by the caller, which has the position and the literal to
// quote.
func parseXQVersion(lit string) (XQVersion, bool) {
	switch lit {
	case "1.0":
		return XQuery10, true
	case "3.0":
		return XQuery30, true
	case "3.1":
		return XQuery31, true
	}
	return defaultXQVersion, false
}
