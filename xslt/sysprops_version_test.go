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
