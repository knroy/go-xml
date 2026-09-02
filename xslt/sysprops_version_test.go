package xslt

import (
	"strings"
	"testing"
)

// TestProductVersionIsNotStale covers the defect that made this function
// exist: fn:system-property('xsl:product-version') answered "0.1" through the
// 1.0, 1.1 and 1.2 releases, because the value was a constant nobody edited.
//
// The test cannot assert a version number -- what the build reports depends on
// how the binary was built, which is the whole point. It asserts the two
// properties that were violated: the answer is never empty, and it is never
// the stale literal again.
func TestProductVersionIsNotStale(t *testing.T) {
	got := systemProperties["product-version"]
	if got == "" {
		t.Fatal("product-version is empty; a stylesheet asking cannot tell that from an unknown property")
	}
	if got == "0.1" {
		t.Errorf("product-version is %q, the hardcoded value this replaced", got)
	}
	if strings.HasPrefix(got, "v") {
		t.Errorf("product-version is %q; the v prefix is Go's, not the product's", got)
	}
	// The suite writes this property into xsl:package/@package-version after
	// stripping everything but digits and dots, so anything that is not a
	// bare triple becomes an invalid package version rather than an untidy
	// string. package-version-010 says so in its own description, and it
	// went from passing to failing when the first version of this function
	// answered a pseudo-version.
	if !isReleaseTriple(got) {
		t.Errorf("product-version is %q, which is not an N.N.N release triple; "+
			"package-version-010 writes it into @package-version", got)
	}
}

// TestIsReleaseTriple pins the filter, including the forms that reached it
// from a real build: a pseudo-version, a dirty working tree, and the two
// values debug.ReadBuildInfo gives when there is no module version at all.
func TestIsReleaseTriple(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"1.2.0", true},
		{"0.0.0", true},
		{"10.20.30", true},
		{"1.2", false},
		{"1.2.3.4", false},
		{"1.2.", false},
		{"", false},
		{"(devel)", false},
		{"1.2.1-0.20260902200715-20286fbc88af", false},
		{"unreleased", false},
	} {
		if got := isReleaseTriple(c.in); got != c.want {
			t.Errorf("isReleaseTriple(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestProductVersionAgreesWithAvailableSystemProperties pins section 18.2's
// requirement that the two functions agree, for this name specifically: it is
// now computed rather than literal, so it is the one entry that could go
// missing from the table without anyone editing the table.
func TestProductVersionAgreesWithAvailableSystemProperties(t *testing.T) {
	v, ok := systemPropertyValue("product-version", 0)
	if !ok {
		t.Fatal("fn:system-property does not answer for product-version")
	}
	if v != systemProperties["product-version"] {
		t.Errorf("system-property says %q, the table says %q", v, systemProperties["product-version"])
	}
}
