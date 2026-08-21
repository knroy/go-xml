package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// The corpora live in a top-level testdata/ directory rather than inside this
// package. They are shared reference material — production stylesheets and the
// Saxon output they are diffed against — and are not specific to the xslt
// package the way a Go testdata/ directory implies.
//
// Go still gives the name "testdata" its usual treatment there: the toolchain
// ignores any directory called testdata wherever it appears, so nothing in it
// is ever compiled or vetted.
const testdataDir = "testdata"

// testdataPath resolves a path inside the shared corpus directory.
//
// Tests run with the package directory as their working directory, so the path
// is relative to the repository root from here. Centralising it means a future
// move is one edit rather than one per call site.
func testdataPath(parts ...string) string {
	return filepath.Join(append([]string{"..", testdataDir}, parts...)...)
}

// requireTestdata skips a test when the corpus is absent, and reports anything
// else as a real failure — a missing corpus is a setup problem, but an
// unreadable one is a bug worth seeing.
func requireTestdata(t *testing.T, parts ...string) string {
	t.Helper()
	p := testdataPath(parts...)
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("corpus not present at %s", p)
		}
		t.Fatalf("corpus at %s is unreadable: %v", p, err)
	}
	return p
}
