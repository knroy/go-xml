package xslt

import (
	"sort"

	"github.com/knroy/go-xml/internal/version"
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// productVersion is what fn:system-property('xsl:product-version') answers.
//
// It is the constant version.Version (internal/version), NOT a value read from the
// build. Both of the earlier answers were wrong, in opposite directions, and
// the reasons are worth keeping:
//
// A hand-typed constant said "0.1" through the 1.0, 1.1 and 1.2 releases,
// because a constant nobody edits is a constant nobody edits. A stylesheet
// dispatching on the property -- which is the only reason section 18.2
// defines it -- got an answer three tags out of date. It was reported from
// the field against 1.2.0.
//
// Reading debug.ReadBuildInfo replaced it, and is wrong for a LIBRARY.
// bi.Main.Version is the version of the MAIN module: the program being built,
// not this one. go-xml is the main module only for cmd/go-xml and for every
// test in this repository, which is exactly why no test caught it. Any
// program that embeds go-xml reports its OWN version here, and a program
// under development reports "(devel)", so the property answered 0.0.0 for
// every embedder. Reported from a browser wasm build whose go.mod said
// `require github.com/knroy/go-xml v1.3.0` and whose Main.Version said
// "(devel)".
//
// bi.Deps would carry the real answer in the one case where the importer
// built against a published module -- and in no other. Under a `replace`
// directive, inside a vendor/ tree, in a fork, and in trimmed or wasm builds
// where the module graph is not embedded, bi.Deps is empty or absent. So it
// is not kept as a fallback: it would fire precisely in the cases where it
// cannot answer, and a fallback to bi.Main.Version would be a fallback to the
// bug. A constant is correct in every one of those builds, which is why
// golang.org/x/text answers the same question the same way: unicode/norm
// declares its Version as a constant in the source rather than asking the
// build. (golang.org/x/telemetry DOES call ReadBuildInfo, because it asks the
// other question -- the version of the PROGRAM, which is exactly what
// bi.Main.Version answers.)
//
// What makes a constant admissible again, when it was not the first time, is
// that this one is CHECKED. The 0.1-for-three-releases failure was an
// unchecked constant, not a constant: nothing in the build could tell a right
// value from a wrong one. Now TestVersionIsReleasedAndDescribed fails every
// push and pull request unless version.Version is a release triple that
// CHANGELOG.md has a section for, and release.yml refuses a tag that disagrees with it.
// See internal/version for the full argument, including why the constant
// lives there and not here: it is the MODULE's version, and xsd, relaxng and
// xpath ship under the same tag.
//
// The 0.0.0 for a non-triple is kept. Version should never be one -- CI fails
// the build if it is -- but the shape of the answer is a CONTRACT rather than
// an internal invariant: package-version-010 writes this property straight
// into xsl:package/@package-version through a replace() that strips
// everything but digits and dots, so a pre-release or build suffix does not
// merely look untidy: its digits are spliced onto the triple and the package
// version is then invalid. The case says so in its own description: "this
// test may fail if the product version is not a valid package version".
func productVersion() string {
	if !version.IsReleaseTriple(version.Version) {
		return "0.0.0"
	}
	return version.Version
}

// systemProperties is every system property this processor defines, keyed by
// its local name in the XSLT namespace.
//
// One table rather than a switch because fn:available-system-properties has
// to return exactly the names fn:system-property answers for. XSLT 3.0
// section 18.2 requires the two to agree, and the suite tests it directly:
// available-system-properties-003 asks for every name the first function
// returns and compares each against what the second says. A switch and a
// separate list would drift the moment one was edited.
var systemProperties = map[string]string{
	"version":                          "3.0",
	"vendor":                           "go-xml",
	"vendor-url":                       "https://github.com/knroy/go-xml",
	"product-name":                     "go-xml",
	"product-version":                  productVersion(),
	"is-schema-aware":                  "no",
	"supports-serialization":           "yes",
	"supports-backwards-compatibility": "yes",

	// The four below are XSLT 3.0 additions. They are reported at every
	// version, because reporting a property is not the same as offering the
	// feature: a 2.0 stylesheet asking for one gets an honest answer rather
	// than the empty string that means "no such property".
	"supports-namespace-axis": "yes",
	// Streaming is not implemented, and is not a small omission to work
	// around: it wants a pull parser and a streamability static analysis.
	// Saying no here is what lets a stylesheet take its fallback path.
	"supports-streaming": "no",
	// xsl:evaluate, which this processor implements. §18.2 ties the answer to
	// element-available('xsl:evaluate'): the feature is "statically disabled"
	// only if BOTH report the absence, and that one already reported true.
	// Saying no here while xsl:evaluate went on evaluating left a stylesheet
	// unable to find out what it could actually do -- system-property-014
	// asks the property in a use-when and then runs the instruction, and got
	// a document that had taken the fallback path from a feature that works.
	"supports-dynamic-evaluation":     "yes",
	"supports-higher-order-functions": "yes",
	"xpath-version":                   "3.1",
	"xsd-version":                     "1.1",
}

// systemPropertyValue answers fn:system-property for a name in the XSLT
// namespace.
//
// The version property is the one answer that depends on the stylesheet
// rather than on the processor: section 18.2 defines it as the version of
// XSLT the stylesheet is being processed under, so a version="2.0" stylesheet
// must be told 2.0 even though this processor also implements 3.0. Reporting
// 3.0 to it would have a stylesheet take a 3.0 branch whose instructions its
// own declared version does not admit.
func systemPropertyValue(local string, v xpath.Version) (string, bool) {
	if local == "version" {
		if v.AtLeast31() {
			return "3.0", true
		}
		return "2.0", true
	}
	// The two version-reporting properties answer for the language actually
	// in force, on the same reasoning.
	if local == "xpath-version" && !v.AtLeast31() {
		return "2.0", true
	}
	s, ok := systemProperties[local]
	return s, ok
}

// availableSystemProperties is fn:available-system-properties, XSLT 3.0
// section 18.2: the QNames of every property fn:system-property will answer
// for.
//
// Sorted so that the result is the same on every run. The spec puts no order
// on it, but a map walk would make a stylesheet that serialises the list
// produce a different document each time, which is a poor thing for a
// transform to do and impossible to test.
func availableSystemProperties() xdm.Sequence {
	names := make([]string, 0, len(systemProperties))
	for k := range systemProperties {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make(xdm.Sequence, 0, len(names))
	for _, n := range names {
		out = append(out, xdm.NewQNameValue(xdm.QName{
			// The prefix is carried so that the QName prints as "xsl:version"
			// rather than as the bare local part, which is what a stylesheet
			// serialising the list expects to see.
			Prefix: "xsl", URI: xdm.NSXSL, Local: n,
		}))
	}
	return out
}

// registerSystemPropertyFuncs adds fn:available-system-properties.
//
// Registered Since XPath31 -- which is the version a version="3.0" stylesheet
// compiles in -- so that a 2.0 stylesheet calling it gets XPST0017, the same
// "unknown function" every other conforming processor raises, rather than an
// answer no 2.0 processor would give.
func registerSystemPropertyFuncs(l *xpath.Library) {
	l.Add(xpath.Function{
		Name:  xdm.QName{URI: xdm.NSFN, Local: "available-system-properties"},
		Arity: 0,
		Since: xpath.XPath31,
		Call: func(_ *xpath.Context, _ []xdm.Sequence) (xdm.Sequence, error) {
			return availableSystemProperties(), nil
		},
	})
}
