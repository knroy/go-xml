package xslt

import (
	"runtime/debug"
	"strings"
	"testing"

	"github.com/knroy/go-xml/internal/version"
)

// TestProductVersionIsTheModuleConstant is the xslt half of the version
// check. internal/version asserts what the constant may be; this asserts that
// the property actually answers it.
func TestProductVersionIsTheModuleConstant(t *testing.T) {
	if got := productVersion(); got != version.Version {
		t.Errorf("productVersion() = %q, want the module constant %q", got, version.Version)
	}
}

// TestProductVersionDoesNotDependOnTheMainModule is the in-tree stand-in for
// the defect that no in-tree test could catch.
//
// productVersion read debug.ReadBuildInfo().Main.Version -- the version of the
// program being built. In this repository go-xml IS the main module for every
// test binary, so the reading was locally correct and wrong for every
// embedder, which is exactly why the suite was silent about it. It was found
// in the field, from a browser wasm build whose go.mod required
// github.com/knroy/go-xml v1.3.0 and whose Main.Version was "(devel)".
//
// A test cannot change its own main module, so this asserts the property that
// makes the class impossible instead: the answer is the constant, and is not
// what build info reports. Under `go test` Main.Version is "(devel)" -- a
// non-triple -- so any implementation reading it answers 0.0.0, and this
// fails.
func TestProductVersionDoesNotDependOnTheMainModule(t *testing.T) {
	got := systemProperties["product-version"]
	if got != version.Version {
		t.Fatalf("product-version is %q, want the constant %q", got, version.Version)
	}
	if got == "0.0.0" {
		t.Errorf("product-version is %q -- the answer a build-info reading gives "+
			"whenever the main module has no release version, which is every "+
			"embedder under development and every test binary here", got)
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build info; the comparison below has nothing to compare against")
	}
	main := strings.TrimPrefix(bi.Main.Version, "v")
	if version.IsReleaseTriple(main) {
		// The main module happens to carry a release version, so reading it
		// would agree by coincidence and prove nothing. Say so rather than
		// pass quietly: a test that can stop testing without saying so is the
		// shape of the original failure.
		t.Skipf("SKIPPED, NOT PASSED: the main module reports %q, a release "+
			"triple, so build info and the constant cannot be told apart in "+
			"this build", main)
	}
	if got == main {
		t.Errorf("product-version is %q, which is what build info says the MAIN "+
			"module is; it must be this library's version", got)
	}
}
