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
	var bestVer []int
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
		v := parseVersionParts(pk.Version)
		if bestFile == "" || compareVersionParts(v, bestVer) > 0 {
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
// expression.
//
// The expression is a comma-separated list of alternatives, each either an
// exact version, a version with a trailing ".*" wildcard, or a "from-to"
// range with either end optional. "*" alone matches anything, and is what an
// xsl:use-package with no package-version means.
func versionInRange(version, match string) bool {
	match = strings.TrimSpace(match)
	if match == "" || match == "*" {
		return true
	}
	v := parseVersionParts(version)
	for _, alt := range strings.Split(match, ",") {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			continue
		}
		if lo, hi, isRange := strings.Cut(alt, "-"); isRange {
			// An open end means unbounded in that direction, which is how
			// "1.0-" says "1.0 or later".
			if lo != "" && compareVersionParts(v, parseVersionParts(lo)) < 0 {
				continue
			}
			if hi != "" && compareVersionParts(v, parseVersionParts(hi)) > 0 {
				continue
			}
			return true
		}
		if strings.HasSuffix(alt, ".*") {
			// A wildcard matches any version whose stated parts agree, so
			// "1.2.*" admits 1.2.0 and 1.2.7 but not 1.3.0.
			prefix := parseVersionParts(strings.TrimSuffix(alt, ".*"))
			if len(v) < len(prefix) {
				continue
			}
			if compareVersionParts(v[:len(prefix)], prefix) == 0 {
				return true
			}
			continue
		}
		if compareVersionParts(v, parseVersionParts(alt)) == 0 {
			return true
		}
	}
	return false
}

// parseVersionParts splits a dotted release sequence into its numbers.
//
// A non-numeric part -- the alphanumeric suffix a package version may carry --
// stops the parse, since it orders after every numbered release and comparing
// it as a number would be meaningless.
func parseVersionParts(v string) []int {
	var out []int
	for _, part := range strings.Split(strings.TrimSpace(v), ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

// compareVersionParts orders two dotted release sequences.
//
// A missing part reads as zero, so 1.0 and 1.0.0 are the same version, which
// is what the specification's ordering requires.
func compareVersionParts(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
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
