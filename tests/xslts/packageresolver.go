package xslts

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// envPackageResolver answers xsl:use-package from the packages an environment
// declares.
//
// The suite addresses a package by name and version, never by location: the
// environment states a file, a URI and a package-version, and the stylesheet
// names only the last two. Matching on the name and then choosing the highest
// version in range is what the resolver has to do; the file is an
// implementation detail of how the suite happens to store it.
type envPackageResolver struct {
	set *TestSet
	env *Environment
	// tc is the case itself, because a secondary package may be declared on
	// <test> rather than in the environment -- the suite writes it either
	// way, and a package used by only one case has no reason to be shared.
	tc *TestCase
	// entities is the resolver used for the package documents themselves, so
	// that a package is read under the same confinement a stylesheet is.
	entities xdm.EntityResolver
}

// ResolvePackage implements xslt.PackageResolver.
func (p envPackageResolver) ResolvePackage(name, versionMatch string) (*xdm.Node, error) {
	// Both sources are searched. A case-level declaration is not an override
	// of an environment one -- the suite never writes both for a name -- so
	// they are simply concatenated.
	var declared []EnvPackage
	if p.env != nil {
		declared = append(declared, p.env.Packages...)
	}
	if p.tc != nil {
		for _, pk := range p.tc.Test.Packages {
			if pk.Role == "principal" {
				// The principal package is the stylesheet under test, not a
				// library another module may use.
				continue
			}
			declared = append(declared, EnvPackage{
				File: pk.File, Role: pk.Role,
				URI: pk.URI, Version: pk.Version,
			})
		}
	}
	if len(declared) == 0 {
		return nil, fmt.Errorf("no package is declared")
	}
	// Every candidate is collected before one is chosen, because the choice
	// is "the highest version that matches" rather than "the first": the
	// suite's use-package-env-004 declares versions 1.0.0 and 2.0.0 of one
	// package precisely to test that.
	var bestFile string
	var bestVer version
	for _, pk := range declared {
		// The module is authoritative about its own identity. An xsl:package
		// carries its name and version, and where the catalog states them too
		// the two can disagree -- override-f-024's environment says 1.0.0 of
		// a module that calls itself 0.0.1, and the stylesheet asks for the
		// version the module states. The catalog's values are kept only as a
		// fallback for a module that declares none.
		if uri, ver := packageIdentity(
			filepath.Join(p.set.Dir, pk.File)); uri != "" {
			pk.URI, pk.Version = uri, ver
		}
		if pk.URI != name {
			continue
		}
		if pk.Version == "" {
			// Section 3.5 gives a package that states no package-version the
			// version "1.0". The default is applied here as well as in
			// packageIdentity because a <package> that names its uri in the
			// catalog never reaches that function, and a versionless package
			// then matched no expression at all -- not even "1".
			pk.Version = "1.0"
		}
		if !versionInRange(pk.Version, versionMatch) {
			continue
		}
		v := parseVersion(pk.Version)
		if bestFile == "" || compareVersions(v, bestVer) > 0 {
			bestFile, bestVer = pk.File, v
		}
	}
	if bestFile == "" {
		return nil, fmt.Errorf("no package %q matches version %q",
			name, versionMatch)
	}
	path := filepath.Join(p.set.Dir, bestFile)
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := xdm.ParseString(string(stripBOM(src)), xdm.ParseOptions{
		AllowDOCTYPE:     true,
		BaseURI:          fileURI(path),
		DocumentURI:      fileURI(path),
		ExternalEntities: p.entities,
	})
	if err != nil {
		return nil, err
	}
	return doc.Root, nil
}

// versionInRange reports whether a package version satisfies a matching
// expression, section 3.5.1.
//
//	PackageVersionRange ::= AnyVersion | VersionRanges
//	VersionRanges       ::= VersionRange (S? "," S? VersionRange)*
//	VersionRange        ::= PackageVersion | VersionPrefix |
//	                        VersionFrom | VersionTo | VersionFromTo
//	VersionPrefix       ::= PackageVersion ".*"
//	VersionFrom         ::= PackageVersion "+"
//	VersionTo           ::= "to" S (PackageVersion | VersionPrefix)
//	VersionFromTo       ::= PackageVersion S "to" S (PackageVersion | VersionPrefix)
//
// The separator between the two ends of a range is the keyword "to", not a
// hyphen: a hyphen inside a version introduces the NCName portion, so
// "2.0.0-alpha" is one version and not a range from 2.0.0 to alpha.
// Reading it as a range was why use-package-208 could not find the 2.0.0-alpha
// the environment declares, and why use-package-205's "to 1.5" matched
// nothing at all.
//
// The syntax is assumed well-formed here: the compiler has already applied
// the grammar and reported XTSE0020 for a range that does not conform, so a
// value that reaches this function is one of the forms above.
func versionInRange(version, match string) bool {
	match = strings.TrimSpace(match)
	if match == "" || match == "*" {
		return true
	}
	v := parseVersion(version)
	for _, alt := range strings.Split(match, ",") {
		if rangeMatches(v, strings.TrimSpace(alt)) {
			return true
		}
	}
	return false
}

// rangeMatches applies one VersionRange alternative.
func rangeMatches(v version, alt string) bool {
	if alt == "" {
		return false
	}
	// VersionTo: an open lower bound. The upper bound is "some version that
	// matches the VersionPrefix", so "to 3.3.*" admits 3.3.4621 -- the
	// comparison is against the greatest version the prefix covers, which is
	// what prefixUpperBound answers.
	if rest, ok := cutKeyword(alt, "to"); ok {
		return compareVersions(v, prefixUpperBound(rest)) <= 0
	}
	// VersionFromTo.
	if lo, hi, ok := cutFromTo(alt); ok {
		return compareVersions(v, parseVersion(lo)) >= 0 &&
			compareVersions(v, prefixUpperBound(hi)) <= 0
	}
	// VersionFrom: "1.3+" is 1.3 or later.
	if s, ok := strings.CutSuffix(alt, "+"); ok {
		return compareVersions(v, parseVersion(s)) >= 0
	}
	// VersionPrefix: "1.3.*" matches any version whose leading portions are
	// the prefix's portions. The note under 3.5.1 is explicit that this is a
	// portion-wise test and not a substring one, so 1.3.* does not match
	// 1.35.
	if p, ok := strings.CutSuffix(alt, ".*"); ok {
		pre := parseVersion(p)
		if len(v.parts) < len(pre.parts) {
			return false
		}
		return comparePortionsUpTo(v.parts, pre.parts) == 0
	}
	return compareVersions(v, parseVersion(alt)) == 0
}

// cutKeyword splits off a leading keyword the grammar requires to be
// followed by whitespace.
func cutKeyword(s, kw string) (string, bool) {
	if !strings.HasPrefix(s, kw) {
		return "", false
	}
	rest := s[len(kw):]
	if rest == "" || !isSpaceByte(rest[0]) {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// cutFromTo splits "V1 to V2" at the keyword, which must be delimited by
// whitespace on both sides: "to" is a legal NCName, so "1.0-to" is one
// version rather than a range.
func cutFromTo(s string) (lo, hi string, ok bool) {
	for i := 0; i+4 <= len(s); i++ {
		if !isSpaceByte(s[i]) || s[i+1:i+3] != "to" || !isSpaceByte(s[i+3]) {
			continue
		}
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+3:]), true
	}
	return "", "", false
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// prefixUpperBound reads the upper end of a range, which the grammar allows
// to be written either as a version or as a VersionPrefix.
//
// A bare version is its own bound. A prefix stands for the greatest version
// it covers, and since a version may carry arbitrarily many further portions
// there is no greatest one to name -- so the bound is marked open instead,
// and compareVersions stops at the prefix's length. That is what lets
// "to 3.3.*" admit 3.3.4621 while still refusing 3.4.0.
func prefixUpperBound(s string) version {
	if p, ok := strings.CutSuffix(s, ".*"); ok {
		v := parseVersion(p)
		v.prefix = true
		return v
	}
	return parseVersion(s)
}

// portion is one portion of a version number: an integer, or the NCName that
// may close the sequence.
//
// 3.5.1 calls both "portions" and orders them together, with the rule that an
// NCName sorts before any integer in the same position.
type portion struct {
	num   int
	name  string
	isNum bool
}

// version is a parsed package version: its portions, and whether it is really
// a prefix standing for every version that extends it.
type version struct {
	parts  []portion
	prefix bool
}

// parseVersion splits a version number into its portions, 3.5.1.
//
//	PackageVersion ::= NumericPart ( "-" NamePart )?
//	NumericPart    ::= IntegerLiteral ( "." IntegerLiteral )*
//	NamePart       ::= NCName
//
// Only the first hyphen separates the two parts. The note under 3.5.1 spells
// this out -- "1-alpha-2 is a valid version number, with two portions: 1 and
// alpha-2. The second hyphen is part of the NCName" -- which is what makes
// use-package-209's "2.0.0-arable-environment.27" a single NCName portion
// rather than three.
//
// Trailing zero portions are discarded, so that 1.0 and 1.0.0 are the same
// version. "A zero-valued integer that is not followed by another integer" is
// the specification's wording, and an NCName is not another integer: the
// zeroes of "2.0.0-alpha" are dropped just as they are in plain "2.0.0",
// leaving the portions 2 and alpha. Without that, 2.0.0-alpha compared
// greater than 2.0.0 instead of less.
func parseVersion(s string) version {
	s = strings.TrimSpace(s)
	num, name := s, ""
	hasName := false
	if i := strings.IndexByte(s, '-'); i >= 0 {
		num, name, hasName = s[:i], s[i+1:], true
	}
	var v version
	if num != "" {
		for _, part := range strings.Split(num, ".") {
			n, err := strconv.Atoi(part)
			if err != nil {
				break
			}
			v.parts = append(v.parts, portion{num: n, isNum: true})
		}
	}
	for len(v.parts) > 0 && v.parts[len(v.parts)-1].isNum &&
		v.parts[len(v.parts)-1].num == 0 {
		v.parts = v.parts[:len(v.parts)-1]
	}
	if hasName {
		v.parts = append(v.parts, portion{name: name})
	}
	return v
}

// comparePortion orders two portions, 3.5.1.
//
// Two integers compare as numbers and two NCNames by codepoint. Where one is
// an integer and the other an NCName, "the NCName comes first" -- which is
// how 1.0.3-rc1 sorts below 1.0.3.2.
func comparePortion(a, b portion) int {
	switch {
	case a.isNum && b.isNum:
		switch {
		case a.num < b.num:
			return -1
		case a.num > b.num:
			return 1
		}
		return 0
	case !a.isNum && !b.isNum:
		return strings.Compare(a.name, b.name)
	case a.isNum:
		return 1
	default:
		return -1
	}
}

// comparePortionsUpTo compares two portion sequences over the length of the
// shorter, answering 0 when they agree that far.
func comparePortionsUpTo(a, b []portion) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if c := comparePortion(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}

// compareVersions orders two versions, 3.5.1.
//
// Where the portions agree as far as the shorter runs, the shorter is the
// lesser if the next portion of the longer is an integer, and the greater if
// it is an NCName: "1.2 is less than 1.2.5, while 2.0 is greater than
// 2.0-rc1".
//
// A version marked prefix is an upper bound standing for every version that
// extends it, so the comparison stops once its portions are exhausted and
// anything agreeing that far compares equal to it.
func compareVersions(a, b version) int {
	if c := comparePortionsUpTo(a.parts, b.parts); c != 0 {
		return c
	}
	if b.prefix && len(a.parts) >= len(b.parts) {
		return 0
	}
	if a.prefix && len(b.parts) >= len(a.parts) {
		return 0
	}
	switch {
	case len(a.parts) == len(b.parts):
		return 0
	case len(a.parts) < len(b.parts):
		if b.parts[len(a.parts)].isNum {
			return -1
		}
		return 1
	default:
		if a.parts[len(b.parts)].isNum {
			return 1
		}
		return -1
	}
}

// packageIdentity reads the name and version an xsl:package declares.
//
// A <package> element in the catalog may name only a file. The package's
// identity then lives in the module itself, which is where a real processor
// would read it from too.
func packageIdentity(path string) (string, string) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	doc, err := xdm.ParseString(string(stripBOM(src)), xdm.ParseOptions{
		AllowDOCTYPE: true,
		BaseURI:      fileURI(path),
	})
	if err != nil {
		return "", ""
	}
	for _, el := range doc.Root.ChildElements() {
		if el.Name.Local != "package" {
			continue
		}
		ver := el.AttrValue("package-version")
		if ver == "" {
			ver = "1.0"
		}
		return el.AttrValue("name"), ver
	}
	return "", ""
}
