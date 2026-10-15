package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The vendored-schemas lane of tests/check.sh, run on its own. The block is cut
// out of the real script rather than copied, so the test exercises the text
// that ships; the helpers it calls are stubbed, and so is the Go driver, which
// makes the lane's decision -- not the corpus -- what is being measured.
//
// A clean checkout has no testdata/, and the lane used to run the driver with
// no roots, get its usage line back and report FAIL. A lane that measured
// nothing must read SKIP.
func TestVendoredLaneSkipsWithoutFixtureRoots(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh here; check.sh does not run on this host either")
	}
	src, err := os.ReadFile(filepath.Join("..", "check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)\n(_vend_roots=""\n.*?)\nif \[ "\$MODE" = fast \]`).
		FindSubmatch(src)
	if block == nil {
		t.Fatal("vendored lane not found in tests/check.sh")
	}
	stubs := `set -eu
failed=0; skipped=""
fail() { echo "FAIL $1"; failed=$((failed + 1)); }
skip() { echo "SKIPPED $1"; }
lane() { echo "LANE $1|$2|$3"; }
ratchetVendored() { :; }
# Like tests/corpora: with no root it prints its usage line and exits 2.
fakego() {
	echo "$*" > "$ROOT/driver-args"
	[ $# -gt 3 ] || { echo "usage: corpora {maindoc|walk|vendored} <dir>..." >&2; return 2; }
	echo "vendored schemas: 3 loaded" >&2
}
GO=fakego
`
	for _, tc := range []struct {
		name   string
		roots  []string
		lane   string
		driver bool
	}{
		{"no roots", nil, "vendored schemas|SKIP|not run -- fixture roots absent: xslt30-test qt3tests xspec", false},
		{"some roots", []string{"xspec"}, "vendored schemas|SKIP|not ratcheted -- xslt30-test qt3tests absent from testdata/", true},
		{"all roots", []string{"xslt30-test", "qt3tests", "xspec"}, "vendored schemas|PASS|vendored schemas: 3 loaded", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, r := range tc.roots {
				if err := os.MkdirAll(filepath.Join(root, "testdata", r), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(sh, "-c", stubs+string(block[1]))
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "ROOT="+root)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("lane exited %v:\n%s", err, out)
			}
			o := string(out)
			if strings.Contains(o, "FAIL") {
				t.Errorf("lane reported FAIL:\n%s", o)
			}
			if !strings.Contains(o, "LANE "+tc.lane+"\n") {
				t.Errorf("want lane %q, got:\n%s", tc.lane, o)
			}
			args, _ := os.ReadFile(filepath.Join(root, "driver-args"))
			if got := strings.HasPrefix(string(args), "run ./tests/corpora vendored "+root); got != tc.driver {
				t.Errorf("driver ran = %v (args %q), want %v", got, args, tc.driver)
			}
		})
	}
}
